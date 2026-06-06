package server

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"go.kenn.io/agentsview/internal/config"
	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/insight"
	"go.kenn.io/agentsview/internal/service"
	"go.kenn.io/agentsview/internal/sync"
	"go.kenn.io/agentsview/internal/web"
	"go.kenn.io/kit/daemon"
)

// VersionInfo holds build-time version metadata.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	ReadOnly  bool   `json:"read_only,omitempty"`
}

const daemonService = "agentsview"

// Server is the HTTP server that serves the SPA and REST API.
type Server struct {
	mu          gosync.RWMutex
	cfg         config.Config
	db          db.Store
	engine      *sync.Engine
	sessions    service.SessionService
	broadcaster *Broadcaster
	mux         *http.ServeMux
	api         huma.API
	httpSrv     *http.Server
	version     VersionInfo
	dataDir     string

	// baseCtx, when set, is used as the base context for all
	// incoming requests. Cancelling it causes SSE handlers to
	// exit promptly, which unblocks graceful shutdown.
	baseCtx context.Context

	generateStreamFunc insight.GenerateStreamFunc
	spaFS              fs.FS
	spaHandler         http.Handler

	// handlerDelay is injected before each timeout-wrapped
	// handler, used only by tests to guarantee handlers
	// exceed a short timeout. Zero in production.
	handlerDelay time.Duration

	// updateCheckFn is the function called to check for
	// updates. Defaults to update.CheckForUpdate; tests
	// can override it via WithUpdateChecker.
	updateCheckFn UpdateCheckFunc

	// basePath is a URL prefix for reverse-proxy deployments
	// (e.g. "/agentsview"). When set, all routes are served
	// under this prefix and a <base href> tag is injected
	// into the SPA's index.html.
	basePath string
}

// New creates a new Server.
func New(
	cfg config.Config, database db.Store, engine *sync.Engine,
	opts ...Option,
) *Server {
	dist, err := web.Assets()
	if err != nil {
		log.Fatalf("embedded frontend not found: %v", err)
	}

	// Pick the backend that matches the concrete store. A local
	// *db.DB plus a sync engine yields a full read/write backend;
	// any other combination (PG reader, or local DB with nil
	// engine when used by a read-only daemon) yields a read-only
	// backend whose Sync returns db.ErrReadOnly.
	var sessions service.SessionService
	if local, ok := database.(*db.DB); ok && engine != nil {
		sessions = service.NewDirectBackend(local, engine)
	} else {
		sessions = service.NewReadOnlyBackend(database)
	}

	s := &Server{
		cfg:      cfg,
		db:       database,
		engine:   engine,
		sessions: sessions,
		mux:      http.NewServeMux(),
		generateStreamFunc: func(
			ctx context.Context, agent, prompt string,
			onLog insight.LogFunc,
		) (insight.Result, error) {
			return insight.GenerateStreamWithOptions(
				ctx, agent, prompt, onLog,
				insight.GenerateOptions{
					Agents: insightAgentConfig(cfg.Agent),
				},
			)
		},
		spaFS:      dist,
		spaHandler: http.FileServerFS(dist),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.routes()
	return s
}

// Option configures a Server.
type Option func(*Server)

func insightAgentConfig(
	cfg map[string]config.AgentConfig,
) map[string]insight.AgentConfig {
	if len(cfg) == 0 {
		return nil
	}
	agents := make(map[string]insight.AgentConfig, len(cfg))
	for name, agentCfg := range cfg {
		agents[name] = insight.AgentConfig{Binary: agentCfg.Binary}
	}
	return agents
}

// WithVersion sets the build-time version metadata.
func WithVersion(v VersionInfo) Option {
	return func(s *Server) { s.version = v }
}

// WithDataDir sets the data directory used for update caching.
func WithDataDir(dir string) Option {
	return func(s *Server) { s.dataDir = dir }
}

// WithBaseContext sets the base context for all incoming HTTP
// requests. When this context is cancelled, request contexts
// are also cancelled, causing long-lived handlers (SSE) to
// exit and unblocking graceful shutdown.
func WithBaseContext(ctx context.Context) Option {
	return func(s *Server) { s.baseCtx = ctx }
}

// WithBroadcaster wires an event broadcaster into the server so the
// /api/v1/events handler has something to subscribe to. Required for
// live-refresh SSE; absent in PG serve mode where the engine is nil.
func WithBroadcaster(b *Broadcaster) Option {
	return func(s *Server) { s.broadcaster = b }
}

// WithUpdateChecker overrides the update check function,
// allowing tests to substitute a deterministic stub.
func WithUpdateChecker(f UpdateCheckFunc) Option {
	return func(s *Server) { s.updateCheckFn = f }
}

// WithBasePath sets a URL prefix for reverse-proxy deployments.
// The path must start with "/" and not end with "/" (e.g.
// "/agentsview"). When set, the server strips this prefix from
// incoming requests and injects a <base href> tag into the SPA.
func WithBasePath(path string) Option {
	return func(s *Server) {
		s.basePath = strings.TrimRight(path, "/")
	}
}

// WithGenerateFunc overrides the insight generation function,
// allowing tests to substitute a stub. Nil is ignored.
func WithGenerateFunc(f insight.GenerateFunc) Option {
	return func(s *Server) {
		if f != nil {
			s.generateStreamFunc = func(
				ctx context.Context, agent, prompt string,
				_ insight.LogFunc,
			) (insight.Result, error) {
				return f(ctx, agent, prompt)
			}
		}
	}
}

// WithGenerateStreamFunc overrides the streaming insight
// generation function used by the SSE handler. Nil is ignored.
func WithGenerateStreamFunc(f insight.GenerateStreamFunc) Option {
	return func(s *Server) {
		if f != nil {
			s.generateStreamFunc = f
		}
	}
}

func (s *Server) humaConfig() huma.Config {
	version := s.version.Version
	if version == "" {
		version = "dev"
	}
	cfg := huma.DefaultConfig("AgentsView API", version)
	cfg.Info.Description = "HTTP API for browsing, searching, syncing, and managing local agent sessions."
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	if s.basePath != "" {
		cfg.Servers = []*huma.Server{{
			URL:         s.basePath,
			Description: "Configured reverse-proxy base path",
		}}
	}
	return cfg
}

func (s *Server) apiRoute(
	method, path, summary string,
	handler http.Handler,
	options ...func(*huma.Operation),
) {
	op := &huma.Operation{
		OperationID:        operationID(method, path),
		Method:             method,
		Path:               path,
		Summary:            summary,
		Tags:               []string{operationTag(path)},
		Parameters:         pathParameters(path),
		RequestBody:        defaultRequestBody(method),
		Responses:          defaultResponses("application/json"),
		SkipValidateParams: true,
		SkipValidateBody:   true,
	}
	for _, option := range options {
		option(op)
	}
	if !op.Hidden {
		s.api.OpenAPI().AddOperation(op)
	}
	s.api.Adapter().Handle(op, func(ctx huma.Context) {
		r, w := humago.Unwrap(ctx)
		handler.ServeHTTP(w, r)
	})
}

func withResponseContent(contentType string) func(*huma.Operation) {
	return func(op *huma.Operation) {
		op.Responses = defaultResponses(contentType)
	}
}

func withNoRequestBody() func(*huma.Operation) {
	return func(op *huma.Operation) {
		op.RequestBody = nil
	}
}

func withRequestContent(contentType string) func(*huma.Operation) {
	return func(op *huma.Operation) {
		op.RequestBody = &huma.RequestBody{
			Content: map[string]*huma.MediaType{
				contentType: {Schema: genericObjectSchema()},
			},
		}
	}
}

func defaultRequestBody(method string) *huma.RequestBody {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return &huma.RequestBody{
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: genericObjectSchema()},
			},
		}
	default:
		return nil
	}
}

func defaultResponses(contentType string) map[string]*huma.Response {
	successSchema := genericObjectSchema()
	if contentType == "text/event-stream" ||
		strings.HasPrefix(contentType, "text/") {
		successSchema = &huma.Schema{Type: huma.TypeString}
	}
	if contentType == "application/octet-stream" ||
		strings.HasPrefix(contentType, "image/") {
		successSchema = &huma.Schema{
			Type:   huma.TypeString,
			Format: "binary",
		}
	}
	return map[string]*huma.Response{
		"200": responseWithContent("OK", contentType, successSchema),
		"201": responseWithContent(
			"Created", "application/json", genericObjectSchema(),
		),
		"204": {Description: "No Content"},
		"400": errorResponse("Bad Request"),
		"401": errorResponse("Unauthorized"),
		"403": errorResponse("Forbidden"),
		"404": errorResponse("Not Found"),
		"409": errorResponse("Conflict"),
		"500": errorResponse("Internal Server Error"),
		"501": errorResponse("Not Implemented"),
		"503": errorResponse("Service Unavailable"),
		"504": errorResponse("Gateway Timeout"),
	}
}

func responseWithContent(
	description, contentType string,
	schema *huma.Schema,
) *huma.Response {
	return &huma.Response{
		Description: description,
		Content: map[string]*huma.MediaType{
			contentType: {Schema: schema},
		},
	}
}

func errorResponse(description string) *huma.Response {
	return responseWithContent(
		description,
		"application/json",
		&huma.Schema{
			Type: huma.TypeObject,
			Properties: map[string]*huma.Schema{
				"error": {Type: huma.TypeString},
			},
			Required: []string{"error"},
		},
	)
}

func genericObjectSchema() *huma.Schema {
	return &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: true,
	}
}

func pathParameters(path string) []*huma.Param {
	var params []*huma.Param
	for rest := path; ; {
		start := strings.Index(rest, "{")
		if start == -1 {
			return params
		}
		rest = rest[start+1:]
		end := strings.Index(rest, "}")
		if end == -1 {
			return params
		}
		name := rest[:end]
		params = append(params, &huma.Param{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   &huma.Schema{Type: huma.TypeString},
		})
		rest = rest[end+1:]
	}
}

func operationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	lastDash := false
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastDash = false
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func operationTag(path string) string {
	switch {
	case path == "/api/ping":
		return "Health"
	case strings.HasPrefix(path, "/api/v1/analytics/"):
		return "Analytics"
	case strings.HasPrefix(path, "/api/v1/usage/"):
		return "Usage"
	case strings.HasPrefix(path, "/api/v1/insights"):
		return "Insights"
	case strings.HasPrefix(path, "/api/v1/search"):
		return "Search"
	case strings.HasPrefix(path, "/api/v1/secrets"):
		return "Secrets"
	case strings.HasPrefix(path, "/api/v1/settings"):
		return "Settings"
	case strings.HasPrefix(path, "/api/v1/config/"):
		return "Config"
	case strings.HasPrefix(path, "/api/v1/import/"):
		return "Import"
	case strings.HasPrefix(path, "/api/v1/trends/"):
		return "Trends"
	case strings.HasPrefix(path, "/api/v1/starred"):
		return "Starred"
	case strings.HasPrefix(path, "/api/v1/pins") ||
		strings.Contains(path, "/pins") ||
		strings.Contains(path, "/pin"):
		return "Pins"
	case strings.HasPrefix(path, "/api/v1/sync") ||
		strings.HasPrefix(path, "/api/v1/resync") ||
		strings.Contains(path, "/sync"):
		return "Sync"
	case strings.HasPrefix(path, "/api/v1/assets/"):
		return "Assets"
	case strings.HasPrefix(path, "/api/v1/openers"):
		return "Openers"
	case strings.HasPrefix(path, "/api/v1/projects") ||
		strings.HasPrefix(path, "/api/v1/machines") ||
		strings.HasPrefix(path, "/api/v1/agents") ||
		strings.HasPrefix(path, "/api/v1/stats") ||
		strings.HasPrefix(path, "/api/v1/version"):
		return "Metadata"
	default:
		return "Sessions"
	}
}

func (s *Server) routes() {
	s.api = humago.New(s.mux, s.humaConfig())

	s.apiRoute(
		http.MethodGet,
		"/api/ping",
		"Ping daemon",
		daemon.NewPingHandler(daemon.PingHandlerOptions{
			Service: daemonService,
			Version: s.version.Version,
		}),
	)

	// API v1 routes
	s.apiRoute(http.MethodGet, "/api/v1/sessions", "List sessions", s.withTimeout(s.handleListSessions), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/sidebar-index", "List sidebar sessions", s.withTimeout(s.handleSidebarSessionIndex), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}", "Get session", s.withTimeout(s.handleGetSession), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/messages", "List session messages", s.withTimeout(s.handleGetMessages), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/tool-calls", "List session tool calls", s.withTimeout(s.handleToolCalls), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/children", "List child sessions", s.withTimeout(s.handleGetChildSessions), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/activity", "Get session activity", s.withTimeout(s.handleGetSessionActivity), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/timing", "Get session timing", s.withTimeout(s.handleSessionTiming), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/usage", "Get session usage", s.withTimeout(s.handleSessionUsage), withNoRequestBody())
	// SSE: Do not use timeout, as this is a long-lived connection.
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/watch", "Watch session events", http.HandlerFunc(s.handleWatchSession), withNoRequestBody(), withResponseContent("text/event-stream"))
	// SSE: Do not use timeout, as this is a long-lived connection.
	s.apiRoute(http.MethodGet, "/api/v1/events", "Watch server events", http.HandlerFunc(s.handleEvents), withNoRequestBody(), withResponseContent("text/event-stream"))
	// Export: Do not use timeout handler to support large downloads and avoid buffering.
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/export", "Export session as HTML", http.HandlerFunc(s.handleExportSession), withNoRequestBody(), withResponseContent("text/html"))
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/md", "Export session as Markdown", http.HandlerFunc(s.handleMarkdownSession), withNoRequestBody(), withResponseContent("text/markdown"))
	s.apiRoute(http.MethodPost, "/api/v1/sessions/{id}/publish", "Publish session", s.withTimeout(s.handlePublishSession))
	s.apiRoute(http.MethodPost, "/api/v1/sessions/{id}/resume", "Resume session", s.withTimeout(s.handleResumeSession))
	s.apiRoute(http.MethodGet, "/api/v1/openers", "List openers", s.withTimeout(s.handleListOpeners), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/directory", "Get session directory", s.withTimeout(s.handleGetSessionDir), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/search", "Search within a session", s.withTimeout(s.handleSearchSession), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/sessions/{id}/open", "Open session directory", s.withTimeout(s.handleOpenSession))
	s.apiRoute(http.MethodPost, "/api/v1/sessions/sync", "Sync a session", s.withTimeout(s.handleSyncSession))
	s.apiRoute(http.MethodPost, "/api/v1/sessions/upload", "Upload a session export", s.withTimeout(s.handleUploadSession), withRequestContent("multipart/form-data"))
	s.apiRoute(http.MethodGet, "/api/v1/analytics/summary", "Get analytics summary", s.withTimeout(s.handleAnalyticsSummary), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/activity", "Get analytics activity", s.withTimeout(s.handleAnalyticsActivity), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/heatmap", "Get analytics heatmap", s.withTimeout(s.handleAnalyticsHeatmap), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/projects", "Get analytics by project", s.withTimeout(s.handleAnalyticsProjects), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/hour-of-week", "Get analytics by hour of week", s.withTimeout(s.handleAnalyticsHourOfWeek), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/sessions", "Get session shape analytics", s.withTimeout(s.handleAnalyticsSessionShape), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/velocity", "Get velocity analytics", s.withTimeout(s.handleAnalyticsVelocity), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/tools", "Get tool analytics", s.withTimeout(s.handleAnalyticsTools), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/top-sessions", "Get top sessions", s.withTimeout(s.handleAnalyticsTopSessions), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/analytics/signals", "Get signal analytics", s.withTimeout(s.handleAnalyticsSignals), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/trends/terms", "Get trend terms", s.withTimeout(s.handleTrendsTerms), withNoRequestBody())

	s.apiRoute(http.MethodGet, "/api/v1/usage/summary", "Get usage summary", s.withTimeout(s.handleUsageSummary), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/usage/top-sessions", "Get top usage sessions", s.withTimeout(s.handleUsageTopSessions), withNoRequestBody())

	s.apiRoute(http.MethodGet, "/api/v1/insights", "List insights", s.withTimeout(s.handleListInsights), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/insights/{id}", "Get insight", s.withTimeout(s.handleGetInsight), withNoRequestBody())
	s.apiRoute(http.MethodDelete, "/api/v1/insights/{id}", "Delete insight", s.withTimeout(s.handleDeleteInsight))
	s.apiRoute(http.MethodPost, "/api/v1/insights/generate", "Generate insight", http.HandlerFunc(s.handleGenerateInsight), withResponseContent("text/event-stream"))

	s.apiRoute(http.MethodGet, "/api/v1/search", "Search sessions", s.withTimeout(s.handleSearch), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/search/content", "Search session content", s.withTimeout(s.handleSearchContent), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/secrets", "List secret findings", s.withTimeout(s.handleListSecrets), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/projects", "List projects", s.withTimeout(s.handleListProjects), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/machines", "List machines", s.withTimeout(s.handleListMachines), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/agents", "List agents", s.withTimeout(s.handleListAgents), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/stats", "Get stats", s.withTimeout(s.handleGetStats), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/version", "Get server version", s.withTimeout(s.handleGetVersion), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/secrets/scan", "Scan secrets", http.HandlerFunc(s.handleScanSecrets), withResponseContent("text/event-stream"))
	s.apiRoute(http.MethodPost, "/api/v1/sync", "Trigger sync", http.HandlerFunc(s.handleTriggerSync), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/resync", "Trigger full resync", http.HandlerFunc(s.handleTriggerResync), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sync/status", "Get sync status", s.withTimeout(s.handleSyncStatus), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/config/github", "Get GitHub config", s.withTimeout(s.handleGetGithubConfig), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/config/github", "Set GitHub config", s.withTimeout(s.handleSetGithubConfig))
	s.apiRoute(http.MethodGet, "/api/v1/config/terminal", "Get terminal config", s.withTimeout(s.handleGetTerminalConfig), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/config/terminal", "Set terminal config", s.withTimeout(s.handleSetTerminalConfig))
	s.apiRoute(http.MethodGet, "/api/v1/update/check", "Check for updates", s.withTimeout(s.handleCheckUpdate), withNoRequestBody())

	s.apiRoute(http.MethodGet, "/api/v1/settings", "Get settings", s.withTimeout(s.handleGetSettings), withNoRequestBody())
	s.apiRoute(http.MethodPut, "/api/v1/settings", "Update settings", s.withTimeout(s.handleUpdateSettings))
	s.apiRoute(http.MethodGet, "/api/v1/settings/worktree-mappings", "List worktree mappings", s.withTimeout(s.handleListWorktreeMappings), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/settings/worktree-mappings", "Create worktree mapping", s.withTimeout(s.handleCreateWorktreeMapping))
	s.apiRoute(http.MethodPut, "/api/v1/settings/worktree-mappings/{id}", "Update worktree mapping", s.withTimeout(s.handleUpdateWorktreeMapping))
	s.apiRoute(http.MethodDelete, "/api/v1/settings/worktree-mappings/{id}", "Delete worktree mapping", s.withTimeout(s.handleDeleteWorktreeMapping))
	s.apiRoute(http.MethodPost, "/api/v1/settings/worktree-mappings/apply", "Apply worktree mappings", s.withTimeout(s.handleApplyWorktreeMappings), withNoRequestBody())

	s.apiRoute(http.MethodGet, "/api/v1/starred", "List starred sessions", s.withTimeout(s.handleListStarred), withNoRequestBody())
	s.apiRoute(http.MethodPut, "/api/v1/sessions/{id}/star", "Star session", s.withTimeout(s.handleStarSession), withNoRequestBody())
	s.apiRoute(http.MethodDelete, "/api/v1/sessions/{id}/star", "Unstar session", s.withTimeout(s.handleUnstarSession))
	s.apiRoute(http.MethodPost, "/api/v1/starred/bulk", "Bulk star sessions", s.withTimeout(s.handleBulkStar))

	// Session management
	s.apiRoute(http.MethodPatch, "/api/v1/sessions/{id}/rename", "Rename session", s.withTimeout(s.handleRenameSession))
	s.apiRoute(http.MethodDelete, "/api/v1/sessions/{id}", "Delete session", s.withTimeout(s.handleDeleteSession))
	s.apiRoute(http.MethodPost, "/api/v1/sessions/{id}/restore", "Restore session", s.withTimeout(s.handleRestoreSession), withNoRequestBody())
	s.apiRoute(http.MethodDelete, "/api/v1/sessions/{id}/permanent", "Permanently delete session", s.withTimeout(s.handlePermanentDeleteSession))
	s.apiRoute(http.MethodGet, "/api/v1/trash", "List trash", s.withTimeout(s.handleListTrash), withNoRequestBody())
	s.apiRoute(http.MethodDelete, "/api/v1/trash", "Empty trash", s.withTimeout(s.handleEmptyTrash))

	// Pinned messages
	s.apiRoute(http.MethodGet, "/api/v1/pins", "List pins", s.withTimeout(s.handleListPins), withNoRequestBody())
	s.apiRoute(http.MethodGet, "/api/v1/sessions/{id}/pins", "List session pins", s.withTimeout(s.handleListSessionPins), withNoRequestBody())
	s.apiRoute(http.MethodPost, "/api/v1/sessions/{id}/messages/{messageId}/pin", "Pin message", s.withTimeout(s.handlePinMessage))
	s.apiRoute(http.MethodDelete, "/api/v1/sessions/{id}/messages/{messageId}/pin", "Unpin message", s.withTimeout(s.handleUnpinMessage))
	// Import: no timeout wrapper (large files may take longer).
	s.apiRoute(http.MethodPost, "/api/v1/import/claude-ai", "Import Claude.ai archive", http.HandlerFunc(s.handleImportClaudeAI), withRequestContent("multipart/form-data"))
	// ChatGPT import: no timeout wrapper.
	s.apiRoute(http.MethodPost, "/api/v1/import/chatgpt", "Import ChatGPT archive", http.HandlerFunc(s.handleImportChatGPT), withRequestContent("multipart/form-data"))
	// Assets: no timeout wrapper (static files).
	s.apiRoute(http.MethodGet, "/api/v1/assets/{filename}", "Get imported asset", http.HandlerFunc(s.handleGetAsset), withNoRequestBody(), withResponseContent("image/*"))

	// SPA fallback: serve embedded frontend
	// Do not use timeout handler for static assets to avoid buffering.
	s.mux.Handle("/", http.HandlerFunc(s.handleSPA))
}

func (s *Server) handleGetVersion(
	w http.ResponseWriter, _ *http.Request,
) {
	writeJSON(w, http.StatusOK, s.version)
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// Try to serve the exact file
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	f, err := s.spaFS.Open(path)
	if err == nil {
		f.Close()
		// For index.html with a base path, inject <base href>.
		if s.basePath != "" && path == "index.html" {
			s.serveIndexWithBase(w, r)
			return
		}
		s.spaHandler.ServeHTTP(w, r)
		return
	}

	// SPA fallback: serve index.html for all routes
	if s.basePath != "" {
		s.serveIndexWithBase(w, r)
		return
	}
	r.URL.Path = "/"
	s.spaHandler.ServeHTTP(w, r)
}

// serveIndexWithBase reads the embedded index.html, injects a
// <base href> tag, and rewrites root-relative asset paths so
// everything resolves correctly behind a reverse proxy subpath.
func (s *Server) serveIndexWithBase(
	w http.ResponseWriter, _ *http.Request,
) {
	f, err := s.spaFS.Open("index.html")
	if err != nil {
		http.Error(w, "index.html not found",
			http.StatusInternalServerError)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "reading index.html",
			http.StatusInternalServerError)
		return
	}
	html := string(data)

	// Rewrite root-relative asset paths (href="/...", src="/...")
	// to include the base path prefix so the browser fetches
	// assets through the reverse proxy.
	bp := s.basePath
	html = strings.ReplaceAll(html, `href="/`, `href="`+bp+`/`)
	html = strings.ReplaceAll(html, `src="/`, `src="`+bp+`/`)

	// Inject <base href> AFTER rewriting paths so it doesn't
	// get double-prefixed by the replacement above.
	baseTag := fmt.Sprintf(
		`<base href="%s/">`, bp,
	)
	html = strings.Replace(
		html, "<head>", "<head>\n    "+baseTag, 1,
	)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(html))
}

// SetPort updates the listen port (for testing).
func (s *Server) SetPort(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Port = port
}

// SetGithubToken updates the GitHub token for testing.
func (s *Server) SetGithubToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.GithubToken = token
}

// githubToken returns the current GitHub token (thread-safe).
func (s *Server) githubToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.GithubToken
}

// Handler returns the http.Handler with middleware applied.
func (s *Server) Handler() http.Handler {
	allowedOrigins := buildAllowedOrigins(
		s.cfg.Host, s.cfg.Port, s.cfg.PublicOrigins,
	)
	allowedHosts := buildAllowedHosts(
		s.cfg.Host, s.cfg.Port,
		s.cfg.PublicURL, s.cfg.PublicOrigins,
	)
	bindAll := isBindAll(s.cfg.Host)
	bindAllIPs := map[string]bool(nil)
	if bindAll {
		bindAllIPs = localInterfaceIPs()
	}
	h := cspMiddleware(s.cfg.Host, s.cfg.Port, s.basePath,
		s.authMiddleware(
			hostCheckMiddleware(
				allowedHosts, bindAll, s.cfg.Port, bindAllIPs,
				corsMiddleware(
					allowedOrigins, bindAll, s.cfg.Port, bindAllIPs, logMiddleware(s.mux),
				),
			),
		),
	)
	if s.basePath != "" {
		inner := h
		prefix := s.basePath
		h = http.HandlerFunc(func(
			w http.ResponseWriter, r *http.Request,
		) {
			p := r.URL.Path
			// Redirect /basepath to /basepath/ for the SPA.
			if p == prefix {
				http.Redirect(w, r,
					prefix+"/", http.StatusMovedPermanently)
				return
			}
			// Only match full path-segment prefixes to
			// prevent /basepathFOO from being handled.
			if !strings.HasPrefix(p, prefix+"/") {
				http.NotFound(w, r)
				return
			}
			http.StripPrefix(prefix, inner).
				ServeHTTP(w, r)
		})
	}
	return h
}

// cspMiddleware sets a Content-Security-Policy header on non-API
// responses. The policy pins the exact host:port origin so that
// even if Tauri's compile-time CSP uses a wildcard port, the
// intersection narrows to the actual runtime port.
func cspMiddleware(host string, port int, basePath string, next http.Handler) http.Handler {
	policy := buildCSPPolicy(host, port, basePath)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Security-Policy", policy)
			w.Header().Set("X-Frame-Options", "DENY")
		}
		next.ServeHTTP(w, r)
	})
}

// buildCSPPolicy constructs the Content-Security-Policy string.
//
// The server's own origin (host:port) is pinned in the resource
// directives (default/script/img/style/font) because WebKitGTK in a
// Tauri webview may not resolve 'self' to the Go server origin after
// navigating from tauri://localhost.
//
// connect-src is intentionally widened to any http/https/ws/wss
// origin. The "Connect to Remote Server" feature (see
// frontend/src/lib/api/client.ts) lets the user point the SPA at an
// arbitrary remote agentsview API origin stored client-side, which
// this server cannot know when the policy is built. This mirrors the
// backend, where authenticated remote requests already bypass the
// host-check and CORS restrictions (see isRemoteAuth in auth.go and
// corsMiddleware). Security tradeoff: a broad connect-src means that
// if an XSS ever executed in the app, exfiltration would be easier;
// the other directives stay pinned so script execution remains gated
// to 'self'.
func buildCSPPolicy(host string, port int, basePath string) string {
	// serverOrigin is the pinned http origin for the configured
	// host:port, used in the resource directives so resources load
	// correctly regardless of how the webview resolves 'self'.
	serverOrigin := "http://" + net.JoinHostPort(host, strconv.Itoa(port))
	resourceSrc := "'self' " + serverOrigin

	baseURI := "'none'"
	if basePath != "" {
		baseURI = "'self'"
	}

	return fmt.Sprintf(
		"default-src %[1]s; "+
			"script-src %[1]s; "+
			"connect-src 'self' http: https: ws: wss:; "+
			"img-src %[1]s data:; "+
			"style-src %[1]s 'unsafe-inline' https://fonts.googleapis.com; "+
			"font-src %[1]s data: https://fonts.gstatic.com; "+
			"object-src 'none'; "+
			"base-uri %[2]s; "+
			"frame-ancestors 'none'",
		resourceSrc, baseURI,
	)
}

// buildAllowedHosts returns the set of Host header values that
// are legitimate for this server. This defends against DNS
// rebinding attacks where an attacker's domain resolves to
// 127.0.0.1 — the browser sends the attacker's domain as the
// Host header, which we reject.
func buildAllowedHosts(
	host string, port int,
	publicURL string, publicOrigins []string,
) map[string]bool {
	hosts := make(map[string]bool)
	add := func(h string) {
		hosts[net.JoinHostPort(h, strconv.Itoa(port))] = true
		// Browsers may omit port 80 from the Host header.
		// IPv6 literals need brackets (e.g., [::1]).
		if port == 80 {
			if strings.Contains(h, ":") {
				hosts["["+h+"]"] = true
			} else {
				hosts[h] = true
			}
		}
	}
	add(host)
	switch host {
	case "127.0.0.1":
		add("localhost")
	case "localhost":
		add("127.0.0.1")
	case "0.0.0.0", "::":
		add("127.0.0.1")
		add("localhost")
		add("::1")
	case "::1":
		add("127.0.0.1")
		add("localhost")
	}
	if publicURL != "" {
		addHostHeadersFromOrigin(hosts, publicURL)
	}
	for _, origin := range publicOrigins {
		addHostHeadersFromOrigin(hosts, origin)
	}
	return hosts
}

// hostCheckMiddleware validates the Host header against expected
// values to prevent DNS rebinding attacks. Only applied to /api/
// routes — the SPA fallback is left accessible for flexibility.
func hostCheckMiddleware(
	allowedHosts map[string]bool, bindAll bool, port int, allowedIPs map[string]bool, next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// Authenticated remote requests bypass host checks.
			if isRemoteAuth(r) {
				next.ServeHTTP(w, r)
				return
			}
			hostAllowed := allowedHosts[r.Host]
			// In bind-all mode, also allow local-interface IP-literal
			// hosts on the configured port so LAN clients can reach the
			// API while still rejecting rebinding via attacker-controlled
			// domains.
			if !hostAllowed && bindAll {
				hostAllowed = isAllowedBindAllHost(r.Host, port, allowedIPs)
			}
			if !hostAllowed {
				allowed := sortedHosts(allowedHosts)
				log.Printf(
					"host check rejected %s %s: Host %q not in allowed "+
						"set %v; if reaching agentsview through a forwarded "+
						"port or remote host, restart with --public-url "+
						"<origin> matching your browser URL",
					r.Method, r.URL.Path, r.Host, allowed,
				)
				http.Error(
					w, hostRejectionMessage(r.Host, allowed),
					http.StatusForbidden,
				)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sortedHosts returns the allowed Host header values as a sorted
// slice for deterministic log and error output.
func sortedHosts(hosts map[string]bool) []string {
	out := make([]string, 0, len(hosts))
	for h := range hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// hostRejectionMessage builds a self-explaining 403 body for a
// rejected Host header. It names the offending Host, lists the
// allowed values, and points at --public-url so users behind SSH
// port-forwarding, reverse proxies, or remote dev environments
// (exe.dev, Codespaces, Coder, WSL2) can diagnose without devtools.
func hostRejectionMessage(host string, allowed []string) string {
	return fmt.Sprintf(
		"Forbidden: request Host %q is not in the allowed set %v. "+
			"If you are reaching agentsview through SSH port-forwarding, "+
			"a reverse proxy, or a remote dev environment, restart the "+
			"server with --public-url <origin> matching the URL in your "+
			"browser (for example --public-url http://%s).",
		host, allowed, host,
	)
}

// httpOrigin formats an HTTP origin string. It uses
// net.JoinHostPort to handle IPv6 bracket formatting correctly
// (e.g., [::1]:8080). Browsers omit the port from the Origin
// header for default ports (80 for HTTP), so for port 80 both
// forms are returned.
func httpOrigin(host string, port int) []string {
	hp := net.JoinHostPort(host, strconv.Itoa(port))
	origin := "http://" + hp
	if port == 80 {
		// net.JoinHostPort brackets IPv6, so use it for the
		// portless form too: JoinHostPort("::1","") is not
		// valid, so bracket manually when needed.
		bare := host
		if strings.Contains(host, ":") {
			bare = "[" + host + "]"
		}
		return []string{origin, "http://" + bare}
	}
	return []string{origin}
}

// buildAllowedOrigins returns the set of origins that should be
// permitted by CORS. For loopback addresses, both "127.0.0.1"
// and "localhost" are allowed because browsers treat them as
// distinct origins.
func buildAllowedOrigins(host string, port int, publicOrigins []string) map[string]bool {
	origins := make(map[string]bool)
	add := func(h string) {
		for _, o := range httpOrigin(h, port) {
			origins[o] = true
		}
	}
	add(host)
	// When binding to a loopback address, also allow the other
	// loopback variants because browsers treat them as distinct
	// origins. When binding to 0.0.0.0 or :: (all interfaces),
	// allow all loopback origins since that's how browsers will
	// access a bind-all server.
	switch host {
	case "127.0.0.1":
		add("localhost")
	case "localhost":
		add("127.0.0.1")
	case "0.0.0.0", "::":
		add("127.0.0.1")
		add("localhost")
		add("::1")
	case "::1":
		add("127.0.0.1")
		add("localhost")
	}
	for _, origin := range publicOrigins {
		origins[origin] = true
	}
	return origins
}

func addHostHeadersFromOrigin(hosts map[string]bool, origin string) {
	u, err := url.Parse(origin)
	if err != nil || u == nil || u.Host == "" {
		return
	}
	hosts[u.Host] = true
	if u.Port() != "" {
		return
	}
	defaultPort := "80"
	if u.Scheme == "https" {
		defaultPort = "443"
	}
	hosts[net.JoinHostPort(u.Hostname(), defaultPort)] = true
}

// isBindAll returns true when the server is listening on all
// interfaces (0.0.0.0 or ::), meaning LAN clients may connect
// via the machine's real IP.
func isBindAll(host string) bool {
	return host == "0.0.0.0" || host == "::"
}

// isAllowedBindAllHost returns true for Host header values that are
// local-interface IP literals on the server's configured port.
func isAllowedBindAllHost(
	hostHeader string, port int, allowedIPs map[string]bool,
) bool {
	host, ok := parseHostHeader(hostHeader, port)
	if !ok {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return allowedIPs[ip.String()]
}

// parseHostHeader validates and normalizes an HTTP Host header for
// the configured server port, returning the host portion.
func parseHostHeader(hostHeader string, port int) (string, bool) {
	if hostHeader == "" {
		return "", false
	}
	host, gotPort, err := net.SplitHostPort(hostHeader)
	if err == nil {
		return host, gotPort == strconv.Itoa(port)
	}
	// Browsers may omit :80 from Host for default HTTP port.
	if port != 80 {
		return "", false
	}
	host = hostHeader
	// Strip IPv6 brackets for ParseIP.
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	return host, true
}

// localInterfaceIPs returns canonical IP strings assigned to local
// network interfaces (including loopback).
func localInterfaceIPs() map[string]bool {
	ips := map[string]bool{
		"127.0.0.1": true,
		"::1":       true,
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			default:
				continue
			}
			if ip == nil {
				continue
			}
			ips[ip.String()] = true
		}
	}
	return ips
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	srv := &http.Server{
		Addr:        addr,
		Handler:     s.Handler(),
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	if s.baseCtx != nil {
		ctx := s.baseCtx
		srv.BaseContext = func(_ net.Listener) context.Context {
			return ctx
		}
	}
	s.mu.Lock()
	s.httpSrv = srv
	s.mu.Unlock()
	log.Printf("Starting server at http://%s", addr)

	listenCtx := context.Background()
	if s.baseCtx != nil {
		listenCtx = s.baseCtx
	}
	ln, err := daemon.Listen(
		listenCtx,
		daemon.Endpoint{
			Network: daemon.NetworkTCP,
			Address: addr,
		},
		daemon.WithRuntimeStore(daemon.RuntimeStore{Dir: s.dataDir}),
	)
	if err != nil {
		return err
	}
	return srv.Serve(ln)
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	srv := s.httpSrv
	s.mu.RUnlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// FindAvailablePort finds an available port starting from the
// given port, binding to the specified host.
func FindAvailablePort(host string, start int) int {
	if start == 0 {
		addr := net.JoinHostPort(host, "0")
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			defer ln.Close()
			if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
				return tcpAddr.Port
			}
		}
		return start
	}

	for port := start; port < start+100; port++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			ln.Close()
			return port
		}
	}
	return start
}

// isMutating returns true for HTTP methods that change state.
func isMutating(method string) bool {
	return method == http.MethodPost ||
		method == http.MethodPut ||
		method == http.MethodPatch ||
		method == http.MethodDelete
}

func corsMiddleware(
	allowedOrigins map[string]bool, bindAll bool, port int, allowedIPs map[string]bool, next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			origin := r.Header.Get("Origin")

			// Authenticated remote requests: allow any origin.
			if isRemoteAuth(r) {
				if origin != "" {
					w.Header().Set(
						"Access-Control-Allow-Origin", origin,
					)
				}
				ensureVaryHeader(w.Header(), "Origin")
				w.Header().Set(
					"Access-Control-Allow-Methods",
					"GET, POST, PUT, PATCH, DELETE, OPTIONS",
				)
				w.Header().Set(
					"Access-Control-Allow-Headers",
					"Content-Type, Authorization",
				)
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// For reads (GET/HEAD), allow empty Origin (same-origin
			// requests often omit it). For mutating methods and
			// preflights, require Origin to be present and allowed.
			originAllowed := allowedOrigins[origin]
			// In bind-all mode, allow local-interface IP-literal
			// origins on the configured port so LAN UI access works
			// without opening wildcard cross-origin access.
			if !originAllowed && bindAll {
				originAllowed = isAllowedBindAllOrigin(origin, port, allowedIPs)
			}
			safeForReads := origin == "" || originAllowed

			if originAllowed {
				w.Header().Set(
					"Access-Control-Allow-Origin", origin,
				)
			}
			// Always set Vary so caches don't serve a
			// response without CORS headers to a
			// legitimate origin.
			ensureVaryHeader(w.Header(), "Origin")
			w.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS",
			)
			w.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, Authorization",
			)
			if r.Method == http.MethodOptions {
				if !safeForReads {
					http.Error(
						w, "Forbidden", http.StatusForbidden,
					)
					return
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Block state-changing requests unless Origin
			// is present and recognized. This prevents
			// CSRF via simple requests (e.g., <form> POST)
			// and DNS rebinding where Origin is absent.
			if !originAllowed && isMutating(r.Method) {
				http.Error(
					w, "Forbidden", http.StatusForbidden,
				)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// isAllowedBindAllOrigin returns true when Origin is an http://
// local-interface IP-literal origin using the configured server port.
func isAllowedBindAllOrigin(origin string, port int, allowedIPs map[string]bool) bool {
	u, err := url.Parse(origin)
	if err != nil || u == nil {
		return false
	}
	if u.Scheme != "http" || u.Host == "" {
		return false
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return false
	}
	gotPort := u.Port()
	portOK := false
	if port == 80 {
		portOK = gotPort == "" || gotPort == "80"
	} else {
		portOK = gotPort == strconv.Itoa(port)
	}
	if !portOK {
		return false
	}
	return allowedIPs[ip.String()]
}

// ensureVaryHeader appends token to Vary if not already present,
// preserving any existing Vary values.
func ensureVaryHeader(h http.Header, token string) {
	if token == "" {
		return
	}
	seen := make(map[string]bool)
	values := make([]string, 0, 4)
	for _, vary := range h.Values("Vary") {
		for part := range strings.SplitSeq(vary, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			key := strings.ToLower(p)
			if seen[key] {
				continue
			}
			seen[key] = true
			values = append(values, p)
		}
	}
	tokenKey := strings.ToLower(token)
	if !seen[tokenKey] {
		values = append(values, token)
	}
	if len(values) == 0 {
		return
	}
	h.Set("Vary", strings.Join(values, ", "))
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}

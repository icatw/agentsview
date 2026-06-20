package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/db"
	"go.kenn.io/agentsview/internal/dbtest"
	"go.kenn.io/agentsview/internal/parser"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func TestProcessFileProviderAuthoritativeUsesProviderSourceRef(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-source-ref"
	sourcePath := writeProcessProviderClaudeSession(
		t, root, sessionID,
	)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.Equal(t, sessionID, res.results[0].Session.ID)
	assert.Equal(t, parser.AgentClaude, res.results[0].Session.Agent)
	assert.Equal(t, "devbox", res.results[0].Session.Machine)
	assert.Equal(t, sourcePath, res.results[0].Session.File.Path)
	assert.Equal(t, res.results[0].Session.File.Mtime, res.mtime)
	assert.Len(t, res.results[0].Messages, 2)
}

func TestProcessFileProviderAuthoritativeHonorsDiscoveredProject(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-discovered-project"
	sourcePath := writeProcessProviderClaudeSession(
		t, root, sessionID,
	)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Project:         "stored_project",
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.Equal(t, "stored_project", res.results[0].Session.Project)
}

func TestProcessFileProviderAuthoritativeClaudeDuplicatePrefersAppendProgress(t *testing.T) {
	archiveRoot := t.TempDir()
	liveRoot := t.TempDir()
	sessionID := "provider-duplicate-preferred"
	archivePath := writeProcessProviderClaudeSession(
		t, archiveRoot, sessionID,
	)
	livePath := writeProcessProviderClaudeSession(
		t, liveRoot, sessionID,
	)

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {archiveRoot, liveRoot},
		},
		Machine: "devbox",
	})

	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  archivePath,
		Agent: parser.AgentClaude,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	f, err := os.OpenFile(livePath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(testjsonl.ClaudeAssistantJSON(
		"live follow-up",
		"2026-06-01T10:02:00Z",
	) + "\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{archiveRoot, liveRoot},
		Machine: "devbox",
	})
	require.True(t, ok)
	archiveSource, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		StoredFilePath: archivePath,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            archivePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &archiveSource,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.False(t, res.skip)
	assert.Equal(t, livePath, res.results[0].Session.File.Path)
	assert.Len(t, res.results[0].Messages, 3)
	assert.Equal(t, "live follow-up", res.results[0].Messages[2].Content)
}

func TestProcessFileProviderAuthoritativeClaudeDuplicateUsesPreferredProject(t *testing.T) {
	archiveRoot := t.TempDir()
	liveRoot := t.TempDir()
	sessionID := "provider-duplicate-project"
	archivePath := writeProcessProviderClaudeSession(
		t, archiveRoot, sessionID,
	)
	livePath := writeProcessProviderClaudeSession(
		t, liveRoot, sessionID,
	)

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {archiveRoot, liveRoot},
		},
		Machine: "devbox",
	})

	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  archivePath,
		Agent: parser.AgentClaude,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	f, err := os.OpenFile(livePath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(testjsonl.ClaudeAssistantJSON(
		"live follow-up",
		"2026-06-01T10:02:00Z",
	) + "\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{archiveRoot, liveRoot},
		Machine: "devbox",
	})
	require.True(t, ok)
	archiveSource, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		StoredFilePath: archivePath,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            archivePath,
		Project:         "archive-project",
		Agent:           parser.AgentClaude,
		ProviderSource:  &archiveSource,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.Equal(t, livePath, res.results[0].Session.File.Path)
	assert.Equal(t, "-Users-dev-code-demo", res.results[0].Session.Project)
}

func TestProcessFileProviderAuthoritativeClaudeDuplicateUpdatesStaleSource(t *testing.T) {
	archiveRoot := t.TempDir()
	liveRoot := t.TempDir()
	sessionID := "provider-duplicate-stale-source"
	archivePath := writeProcessProviderClaudeSession(
		t, archiveRoot, sessionID,
	)
	livePath := writeProcessProviderClaudeSession(
		t, liveRoot, sessionID,
	)

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {archiveRoot, liveRoot},
		},
		Machine: "devbox",
	})

	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  archivePath,
		Agent: parser.AgentClaude,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	f, err := os.OpenFile(livePath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(testjsonl.ClaudeAssistantJSON(
		"live follow-up",
		"2026-06-01T10:02:00Z",
	) + "\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{archiveRoot, liveRoot},
		Machine: "devbox",
	})
	require.True(t, ok)
	archiveSource, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		StoredFilePath: archivePath,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            livePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &archiveSource,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.Equal(t, livePath, res.results[0].Session.File.Path)
	require.Len(t, res.results[0].Messages, 3)
	assert.Equal(t, "live follow-up", res.results[0].Messages[2].Content)
}

func TestProcessFileProviderAuthoritativeClaudeExcludedIDsDeletePrefixedRows(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-excluded-usage"
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.ClaudeUserJSON(
			"/usage",
			"2026-06-01T10:00:00Z",
			"/Users/dev/code/demo",
		)+"\n"),
		0o644,
	))

	database := dbtest.OpenTestDB(t)
	require.NoError(t, database.UpsertSession(db.Session{
		ID:      "host~" + sessionID,
		Project: "stale",
		Machine: "host",
		Agent:   string(parser.AgentClaude),
	}))
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		IDPrefix: "host~",
		Machine:  "devbox",
	})

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		StoredFilePath: sourcePath,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})
	require.NoError(t, res.err)
	assert.Empty(t, res.results)
	assert.Equal(t, []string{sessionID}, res.excludedSessionIDs)

	results := make(chan syncJob, 1)
	results <- syncJob{
		processResult: res,
		path:          sourcePath,
	}
	close(results)
	stats := engine.collectAndBatch(context.Background(), results, 1, 1, nil, syncWriteDefault)

	assert.Equal(t, []string{"host~" + sessionID}, stats.parserExcludedIDs)
	assert.Equal(t, 1, stats.filesOK)
	assert.Equal(t, 1, stats.parserExcludedFiles)
	got, err := database.GetSession(context.Background(), "host~"+sessionID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestProcessFileProviderAuthoritativeUsesIncrementalParse(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-incremental"
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"hello",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
		)),
		0o644,
	))

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})
	first := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})
	require.NoError(t, first.err)
	require.Len(t, first.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: first.results[0].Session,
			msgs: first.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	f, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(testjsonl.ClaudeAssistantJSON(
		"world",
		"2026-06-01T10:01:00Z",
	) + "\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	require.NotNil(t, res.incremental)
	assert.Empty(t, res.results)
	assert.Equal(t, sessionID, res.incremental.sessionID)
	assert.Len(t, res.incremental.msgs, 1)
	assert.Equal(t, "world", res.incremental.msgs[0].Content)
}

func TestProcessFileProviderAuthoritativeIncrementalFallbackForceReplaces(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-incremental-fallback"
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	first, err := json.Marshal(map[string]any{
		"type":      "assistant",
		"timestamp": "2026-06-01T10:01:00Z",
		"uuid":      "a1",
		"message": map[string]any{
			"id":    "msg_split",
			"model": "claude-sonnet-4-20250514",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 1,
			},
			"content": []map[string]any{{
				"type": "text",
				"text": "Hello",
			}},
			"stop_reason": "tool_use",
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"hello",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
			string(first),
		)),
		0o644,
	))

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})
	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)
	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	second, err := json.Marshal(map[string]any{
		"type":       "assistant",
		"timestamp":  "2026-06-01T10:02:00Z",
		"uuid":       "a2",
		"parentUuid": "a1",
		"message": map[string]any{
			"id":    "msg_split",
			"model": "claude-sonnet-4-20250514",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 2,
			},
			"content": []map[string]any{{
				"type": "text",
				"text": "Hello world",
			}},
			"stop_reason": "end_turn",
		},
	})
	require.NoError(t, err)
	f, err := os.OpenFile(sourcePath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(string(second) + "\n")
	require.NoError(t, f.Close())
	require.NoError(t, err)

	provider, ok = parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err = provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	assert.Nil(t, res.incremental)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.Len(t, res.results[0].Messages, 2)
	assert.Equal(t, "Hello world", res.results[0].Messages[1].Content)
}

func TestProcessFileProviderAuthoritativeSameSizeRewriteForceReplaces(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-same-size-rewrite"
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	original := testjsonl.JoinJSONL(
		testjsonl.ClaudeUserJSON(
			"first",
			"2026-06-01T10:00:00Z",
			"/Users/dev/code/demo",
		),
		testjsonl.ClaudeAssistantJSON(
			"stale assistant",
			"2026-06-01T10:01:00Z",
		),
	)
	require.NoError(t, os.WriteFile(sourcePath, []byte(original), 0o644))

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})
	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)
	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)
	inc, ok := database.GetSessionForIncremental(sourcePath)
	require.True(t, ok)
	stored, err := database.GetSessionFull(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.NotNil(t, stored.FileHash)
	unchangedFingerprint := parser.SourceFingerprint{
		Size:    inc.FileSize,
		MTimeNS: inc.FileMtime,
		Hash:    *stored.FileHash,
	}
	assert.False(t,
		engine.providerIncrementalFreshnessChanged(
			context.Background(), inc, unchangedFingerprint,
		),
	)
	changedHashFingerprint := unchangedFingerprint
	changedHashFingerprint.Hash += "-changed"
	assert.True(t,
		engine.providerIncrementalFreshnessChanged(
			context.Background(), inc, changedHashFingerprint,
		),
	)

	replacement := ""
	for padding := range 4096 {
		candidate := testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"replacement"+strings.Repeat("x", padding),
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
		)
		if len(candidate) == len(original) {
			replacement = candidate
			break
		}
	}
	require.NotEmpty(t, replacement)
	require.NoError(t, os.WriteFile(sourcePath, []byte(replacement), 0o644))
	now := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(sourcePath, now, now))

	provider, ok = parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err = provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	assert.False(t, res.skip)
	assert.Nil(t, res.incremental)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.Len(t, res.results[0].Messages, 1)
	assert.Contains(t, res.results[0].Messages[0].Content, "replacement")
}

func TestProcessFileProviderAuthoritativeReplacementForceReplaces(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-replaced-file"
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"old",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
		)),
		0o644,
	))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	oldInode, oldDevice := getFileIdentity(info)
	if oldInode == 0 || oldDevice == 0 {
		t.Skip("file identity unavailable")
	}

	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})
	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	source, found, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
		RawSessionID: sessionID,
	})
	require.NoError(t, err)
	require.True(t, found)
	initial := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})
	require.NoError(t, initial.err)
	require.Len(t, initial.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess: initial.results[0].Session,
			msgs: initial.results[0].Messages,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)
	stored, err := database.GetSessionFull(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.NotNil(t, stored.FileInode)
	require.NotNil(t, stored.FileDevice)
	assert.Equal(t, oldInode, *stored.FileInode)
	assert.Equal(t, oldDevice, *stored.FileDevice)

	replacementPath := filepath.Join(
		filepath.Dir(sourcePath),
		sessionID+"-replacement.jsonl",
	)
	require.NoError(t, os.WriteFile(
		replacementPath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"replacement",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
			testjsonl.ClaudeAssistantJSON(
				"new assistant",
				"2026-06-01T10:01:00Z",
			),
		)),
		0o644,
	))
	require.NoError(t, os.Rename(replacementPath, sourcePath))
	replacementInfo, err := os.Stat(sourcePath)
	require.NoError(t, err)
	newInode, newDevice := getFileIdentity(replacementInfo)
	if oldInode == newInode && oldDevice == newDevice {
		t.Skip("replacement kept same file identity")
	}

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            sourcePath,
		Agent:           parser.AgentClaude,
		ProviderSource:  &source,
		ProviderProcess: true,
	})

	require.NoError(t, res.err)
	assert.Nil(t, res.incremental)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.Len(t, res.results[0].Messages, 2)
	assert.Equal(t, "replacement", res.results[0].Messages[0].Content)
}

func TestProcessFileProviderAuthoritativeSourceErrorsOnlyForceParse(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "data.sqlite3")
	kiroDB := openProcessProviderKiroDB(t, dbPath)
	_, err := kiroDB.Exec(
		`INSERT INTO conversations_v2
			(key, conversation_id, value, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		"/home/user/code/kiro-app",
		"malformed-session",
		"{corrupt",
		int64(1779012000000),
		int64(1779012030000),
	)
	require.NoError(t, err)

	provider, ok := parser.NewProvider(parser.AgentKiro, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentKiro: {root},
		},
		Machine: "devbox",
	})
	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            dbPath,
		Agent:           parser.AgentKiro,
		ProviderSource:  &sources[0],
		ProviderProcess: true,
	})
	require.NoError(t, res.err)
	assert.Empty(t, res.results)
	assert.Empty(t, res.sessionErrs)

	engine.forceParse = true
	res = engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:            dbPath,
		Agent:           parser.AgentKiro,
		ProviderSource:  &sources[0],
		ProviderProcess: true,
	})
	require.NoError(t, res.err)
	require.Len(t, res.sessionErrs, 1)
	assert.Equal(t, "kiro:malformed-session", res.sessionErrs[0].sessionID)
	assert.Contains(t, res.sessionErrs[0].err.Error(), "malformed payload")
}

func TestClassifyProviderChangedPathCarriesProviderSourceRef(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-classify-source-ref"
	sourcePath := writeProcessProviderClaudeSession(
		t, root, sessionID,
	)
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationShadowCompare,
		},
	})

	files := engine.classifyProviderChangedPath(sourcePath)

	require.Len(t, files, 1)
	require.NotNil(t, files[0].ProviderSource)
	assert.Equal(t, sourcePath, files[0].Path)
	assert.Equal(t, sourcePath, files[0].ProviderSource.DisplayPath)
	assert.True(t, files[0].ForceParse)
	assert.False(t, files[0].ProviderProcess)
}

func TestClassifyPathsAttachesProviderSourceWithoutMigratingProcess(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-classify-ordinary"
	sourcePath := writeProcessProviderClaudeSession(
		t, root, sessionID,
	)
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})

	files := engine.classifyPaths([]string{sourcePath})

	require.Len(t, files, 1)
	require.NotNil(t, files[0].ProviderSource)
	assert.Equal(t, sourcePath, files[0].Path)
	assert.Equal(t, sourcePath, files[0].ProviderSource.DisplayPath)
	assert.False(t, files[0].ForceParse)
	assert.False(t, files[0].ProviderProcess)

	_, ok := engine.processProviderFile(context.Background(), files[0])
	assert.False(t, ok)
}

func TestAttachProviderSourcesCarriesSourceRefForFullSyncDiscovery(t *testing.T) {
	root := t.TempDir()
	sessionID := "provider-full-discovery"
	sourcePath := writeProcessProviderClaudeSession(
		t, root, sessionID,
	)
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
	})

	files := engine.attachProviderSources(context.Background(), nil, []parser.DiscoveredFile{{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	}})

	require.Len(t, files, 1)
	require.NotNil(t, files[0].ProviderSource)
	assert.Equal(t, sourcePath, files[0].ProviderSource.DisplayPath)
	assert.False(t, files[0].ProviderProcess)
}

func writeProcessProviderClaudeSession(
	t *testing.T,
	root string,
	sessionID string,
) string {
	t.Helper()
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", sessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"Use the provider source ref.",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
			testjsonl.ClaudeAssistantJSON(
				[]map[string]string{{
					"type": "text",
					"text": "Provider source ref parsed.",
				}},
				"2026-06-01T10:01:00Z",
			),
		)),
		0o644,
	))
	return sourcePath
}

func openProcessProviderKiroDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	_, err = database.Exec(`
		CREATE TABLE conversations_v2 (
			key TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			value TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (key, conversation_id)
		);
	`)
	require.NoError(t, err)
	return database
}

func TestProcessFileProviderAuthoritativeForgeVirtualSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".forge.db")
	forgeDB := openProcessProviderForgeDB(t, dbPath)
	seedProcessProviderForgeConversation(t, forgeDB)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  dbPath + "#forge-provider-process",
		Agent: parser.AgentForge,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.NotZero(t, res.mtime)
	assert.Equal(t, "forge:forge-provider-process", res.results[0].Session.ID)
	assert.Equal(t, res.results[0].Session.File.Mtime, res.mtime)
	assert.Equal(t, parser.AgentForge, res.results[0].Session.Agent)
	assert.Equal(t, "devbox", res.results[0].Session.Machine)
	assert.Len(t, res.results[0].Messages, 2)
}

func TestProcessFileProviderChangedPathForgeVirtualSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".forge.db")
	forgeDB := openProcessProviderForgeDB(t, dbPath)
	seedProcessProviderForgeConversation(t, forgeDB)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	for _, eventPath := range []string{
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
	} {
		files := engine.classifyProviderChangedPath(eventPath)
		require.Len(t, files, 1, eventPath)
		assert.Equal(t, dbPath+"#forge-provider-process", files[0].Path)
		assert.Equal(t, parser.AgentForge, files[0].Agent)
		assert.False(t, files[0].ForceParse, eventPath)
		assert.True(t, files[0].ProviderProcess)
	}

	files := engine.classifyProviderChangedPath(dbPath)

	res := engine.processFile(context.Background(), files[0])

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.Equal(t, "forge:forge-provider-process", res.results[0].Session.ID)
}

func TestClassifyProviderChangedPathUsesStoredSourceHints(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".forge.db")
	forgeDB := openProcessProviderForgeDB(t, dbPath)
	seedProcessProviderForgeConversation(t, forgeDB)
	virtualPath := dbPath + "#forge-provider-process"

	database := dbtest.OpenTestDB(t)
	require.NoError(t, database.UpsertSession(db.Session{
		ID:       "forge:forge-provider-process",
		Project:  "forge",
		Machine:  "devbox",
		Agent:    string(parser.AgentForge),
		FilePath: &virtualPath,
	}))
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	_, err := forgeDB.Exec(
		`DELETE FROM conversations WHERE conversation_id = ?`,
		"forge-provider-process",
	)
	require.NoError(t, err)

	files := engine.classifyProviderChangedPath(dbPath)

	require.Len(t, files, 1)
	assert.Equal(t, virtualPath, files[0].Path)
	assert.Equal(t, parser.AgentForge, files[0].Agent)
	assert.True(t, files[0].ProviderProcess)
	require.NotNil(t, files[0].ProviderSource)
	assert.Equal(t, virtualPath, files[0].ProviderSource.DisplayPath)
}

func TestClassifyProviderChangedPathUsesConfiguredFactories(t *testing.T) {
	root := t.TempDir()
	changedPath := filepath.Join(root, "custom.trigger")
	sourcePath := filepath.Join(root, "configured-source.jsonl")
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
		ProviderFactories: []parser.ProviderFactory{
			configuredChangedPathProviderFactory{
				agent:      parser.AgentClaude,
				sourcePath: sourcePath,
			},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationShadowCompare,
		},
	})

	files := engine.classifyProviderChangedPath(changedPath)

	require.Len(t, files, 1)
	assert.Equal(t, sourcePath, files[0].Path)
	assert.Equal(t, parser.AgentClaude, files[0].Agent)
	assert.Equal(t, "configured_provider", files[0].Project)
	require.NotNil(t, files[0].ProviderSource)
	assert.Equal(t, sourcePath, files[0].ProviderSource.DisplayPath)
}

func TestProcessFileProviderAuthoritativePiebaldVirtualSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "app.db")
	piebaldDB := openProcessProviderPiebaldDB(t, dbPath)
	seedProcessProviderPiebaldChat(t, piebaldDB)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentPiebald: {root},
		},
		Machine: "devbox",
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  dbPath + "#42",
		Agent: parser.AgentPiebald,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.NotZero(t, res.mtime)
	assert.Equal(t, "piebald:42", res.results[0].Session.ID)
	assert.Equal(t, res.results[0].Session.File.Mtime, res.mtime)
	assert.Equal(t, parser.AgentPiebald, res.results[0].Session.Agent)
	assert.Equal(t, "devbox", res.results[0].Session.Machine)
	assert.Len(t, res.results[0].Messages, 2)
}

func TestProcessFileProviderAuthoritativeWarpVirtualSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "warp.sqlite")
	warpDB := openProcessProviderWarpDB(t, dbPath)
	seedProcessProviderWarpConversation(t, warpDB)

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentWarp: {root},
		},
		Machine: "devbox",
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  dbPath + "#conv-001",
		Agent: parser.AgentWarp,
	})

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.NotZero(t, res.mtime)
	assert.Equal(t, "warp:conv-001", res.results[0].Session.ID)
	assert.Equal(t, res.results[0].Session.File.Mtime, res.mtime)
	assert.Equal(t, parser.AgentWarp, res.results[0].Session.Agent)
	assert.Equal(t, "devbox", res.results[0].Session.Machine)
	assert.NotEmpty(t, res.results[0].Messages)
}

func TestProcessFileProviderAuthoritativeUsesSkipCache(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".forge.db")
	forgeDB := openProcessProviderForgeDB(t, dbPath)
	seedProcessProviderForgeConversation(t, forgeDB)
	virtualPath := dbPath + "#forge-provider-process"

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	first := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentForge,
	})
	require.NoError(t, first.err)
	require.Len(t, first.results, 1)
	require.NotZero(t, first.mtime)
	engine.skipCache[virtualPath] = first.mtime

	second := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentForge,
	})

	require.NoError(t, second.err)
	assert.True(t, second.skip)
	assert.True(t, second.cacheSkip)
	assert.Equal(t, first.mtime, second.mtime)
	assert.Empty(t, second.results)
}

func TestProcessFileProviderAuthoritativeSkipsStoredFreshSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".forge.db")
	forgeDB := openProcessProviderForgeDB(t, dbPath)
	seedProcessProviderForgeConversation(t, forgeDB)
	virtualPath := dbPath + "#forge-provider-process"
	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	first := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentForge,
	})
	require.NoError(t, first.err)
	require.Len(t, first.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess:         first.results[0].Session,
			msgs:         first.results[0].Messages,
			usageEvents:  first.results[0].UsageEvents,
			forceReplace: first.forceReplace,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)
	require.Empty(t, engine.skipCache)

	second := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentForge,
	})

	require.NoError(t, second.err)
	assert.True(t, second.skip)
	assert.True(t, second.cacheSkip)
	assert.Equal(t, first.mtime, second.mtime)
	assert.Empty(t, second.results)
}

func TestProcessFileProviderAuthoritativePiebaldDoesNotSkipStoredFreshSource(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "app.db")
	piebaldDB := openProcessProviderPiebaldDB(t, dbPath)
	seedProcessProviderPiebaldChat(t, piebaldDB)
	virtualPath := dbPath + "#42"
	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentPiebald: {root},
		},
		Machine: "devbox",
	})

	first := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentPiebald,
	})
	require.NoError(t, first.err)
	require.Len(t, first.results, 1)
	written, _, failed := engine.writeBatch(
		[]pendingWrite{{
			sess:         first.results[0].Session,
			msgs:         first.results[0].Messages,
			usageEvents:  first.results[0].UsageEvents,
			forceReplace: first.forceReplace,
		}},
		syncWriteDefault,
		false,
	)
	require.Equal(t, 0, failed)
	require.Equal(t, 1, written)

	second := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  virtualPath,
		Agent: parser.AgentPiebald,
	})

	require.NoError(t, second.err)
	assert.False(t, second.skip)
	require.Len(t, second.results, 1)
	assert.Equal(t, "piebald:42", second.results[0].Session.ID)
}

func TestProcessFileUsesProviderMigratedAgents(t *testing.T) {
	for _, agent := range []parser.AgentType{
		parser.AgentForge,
		parser.AgentPiebald,
		parser.AgentWarp,
	} {
		assert.True(t, processFileUsesProvider(agent), agent)
	}
	assert.False(t, processFileUsesProvider(parser.AgentClaude))
	assert.False(t, processFileUsesProvider(parser.AgentClaudeAI))
}

func openProcessProviderForgeDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	_, err = database.Exec(`
		CREATE TABLE conversations (
			conversation_id TEXT PRIMARY KEY NOT NULL,
			title TEXT,
			workspace_id BIGINT NOT NULL,
			context TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP,
			metrics TEXT
		)
	`)
	require.NoError(t, err)
	return database
}

func seedProcessProviderForgeConversation(t *testing.T, database *sql.DB) {
	t.Helper()
	seedProcessProviderForgeConversationWithID(
		t, database, "forge-provider-process",
	)
}

func seedProcessProviderForgeConversationWithID(
	t *testing.T,
	database *sql.DB,
	conversationID string,
) {
	t.Helper()
	contextJSON := processProviderForgeContext(t, conversationID)
	_, err := database.Exec(
		`INSERT INTO conversations
			(conversation_id, title, workspace_id, context, created_at, updated_at, metrics)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		conversationID,
		"Provider Process",
		int64(1),
		contextJSON,
		"2026-06-01 10:20:00",
		"2026-06-01 10:21:00",
		`{"input_tokens":25,"output_tokens":8}`,
	)
	require.NoError(t, err)
}

func processProviderForgeContext(t *testing.T, conversationID string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"conversation_id": conversationID,
		"messages": []map[string]any{
			{
				"message": map[string]any{
					"text": map[string]any{
						"role":      "User",
						"content":   "Use the provider parser.",
						"model":     "gpt-5.4",
						"timestamp": "2026-06-01T10:20:00Z",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     map[string]any{"actual": 10},
					"completion_tokens": map[string]any{"actual": 0},
					"cached_tokens":     map[string]any{"actual": 2},
				},
			},
			{
				"message": map[string]any{
					"text": map[string]any{
						"role":      "Assistant",
						"content":   "Provider parse complete.",
						"model":     "gpt-5.4",
						"timestamp": "2026-06-01T10:21:00Z",
					},
				},
				"usage": map[string]any{
					"prompt_tokens":     map[string]any{"actual": 15},
					"completion_tokens": map[string]any{"actual": 8},
					"cached_tokens":     map[string]any{"actual": 2},
				},
			},
		},
	})
	require.NoError(t, err)
	return string(raw)
}

func openProcessProviderPiebaldDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	_, err = database.Exec(`
		CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			directory TEXT NOT NULL,
			name TEXT NOT NULL
		);
		CREATE TABLE chats (
			id INTEGER PRIMARY KEY,
			title TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			is_deleted BOOLEAN NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			current_directory TEXT,
			worktree_path TEXT,
			branch_name TEXT,
			project_id INTEGER
		);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY,
			parent_chat_id INTEGER NOT NULL,
			parent_message_id INTEGER,
			role TEXT NOT NULL,
			model TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			input_tokens BIGINT,
			output_tokens BIGINT,
			reasoning_tokens BIGINT,
			cache_read_tokens BIGINT,
			cache_write_tokens BIGINT,
			status TEXT NOT NULL,
			finish_reason TEXT,
			error TEXT,
			enabled INTEGER NOT NULL DEFAULT 1
		);
		CREATE TABLE message_parts (
			id INTEGER PRIMARY KEY,
			parent_chat_message_id INTEGER NOT NULL,
			part_index INTEGER NOT NULL,
			part_type TEXT NOT NULL
		);
		CREATE TABLE message_part_text (
			message_part_id INTEGER PRIMARY KEY,
			is_thinking BOOLEAN NOT NULL DEFAULT FALSE
		);
		CREATE TABLE message_content_nodes (
			id INTEGER PRIMARY KEY,
			parent_text_part_id INTEGER NOT NULL,
			node_index INTEGER NOT NULL,
			node_type TEXT NOT NULL
		);
		CREATE TABLE message_node_text (
			node_id INTEGER PRIMARY KEY,
			content TEXT NOT NULL
		);
		CREATE TABLE message_part_tool_call (
			message_part_id INTEGER PRIMARY KEY,
			provider_tool_use_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			tool_input TEXT NOT NULL,
			tool_result TEXT,
			tool_error TEXT,
			tool_state TEXT NOT NULL DEFAULT 'pending',
			sub_agent_chat_id INTEGER
		);
	`)
	require.NoError(t, err)
	return database
}

func seedProcessProviderPiebaldChat(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO projects (id, directory, name) VALUES (1, '/repo/app', 'app')`,
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO chats
			(id, title, created_at, updated_at, is_deleted, message_count,
			 current_directory, branch_name, project_id)
		 VALUES (42, 'Provider Process', '2026-05-01T10:00:00Z',
			 '2026-05-01T10:05:00Z', 0, 2, '/repo/app', 'main', 1)`,
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO messages
			(id, parent_chat_id, role, model, created_at, updated_at, status)
		 VALUES (100, 42, 'user', '', '2026-05-01T10:00:01Z',
			 '2026-05-01T10:00:01Z', 'completed')`,
	)
	seedProcessProviderPiebaldTextPart(
		t, database, 200, 100, 0, "Use the provider parser.",
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO messages
			(id, parent_chat_id, role, model, created_at, updated_at,
			 input_tokens, output_tokens, cache_read_tokens, status, finish_reason)
		 VALUES (101, 42, 'assistant', 'claude-test',
			 '2026-05-01T10:00:02Z', '2026-05-01T10:00:03Z',
			 10, 20, 5, 'completed', 'end_turn')`,
	)
	seedProcessProviderPiebaldTextPart(
		t, database, 201, 101, 0, "Provider parse complete.",
	)
}

func seedProcessProviderPiebaldTextPart(
	t *testing.T,
	database *sql.DB,
	partID, msgID int64,
	idx int,
	text string,
) {
	t.Helper()
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO message_parts
			(id, parent_chat_message_id, part_index, part_type)
		 VALUES (?, ?, ?, 'text')`,
		partID, msgID, idx,
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO message_part_text
			(message_part_id, is_thinking)
		 VALUES (?, 0)`,
		partID,
	)
	nodeID := partID + 100000
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO message_content_nodes
			(id, parent_text_part_id, node_index, node_type)
		 VALUES (?, ?, 0, 'text')`,
		nodeID, partID,
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO message_node_text
			(node_id, content)
		 VALUES (?, ?)`,
		nodeID, text,
	)
}

func openProcessProviderWarpDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	_, err = database.Exec(`
		CREATE TABLE agent_conversations (
			id INTEGER PRIMARY KEY NOT NULL,
			conversation_id TEXT NOT NULL,
			conversation_data TEXT NOT NULL,
			last_modified_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX ux_agent_conversations_conversation_id
			ON agent_conversations (conversation_id);

		CREATE TABLE ai_queries (
			id INTEGER PRIMARY KEY NOT NULL,
			exchange_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL,
			start_ts DATETIME NOT NULL,
			input TEXT NOT NULL,
			working_directory TEXT,
			output_status TEXT NOT NULL,
			model_id TEXT NOT NULL DEFAULT '',
			planning_model_id TEXT NOT NULL DEFAULT '',
			coding_model_id TEXT NOT NULL DEFAULT ''
		);
		CREATE UNIQUE INDEX ux_ai_queries_exchange_id
			ON ai_queries(exchange_id);
	`)
	require.NoError(t, err)
	return database
}

func seedProcessProviderWarpConversation(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO agent_conversations
			(conversation_id, conversation_data, last_modified_at)
		 VALUES (?, ?, ?)`,
		"conv-001",
		`{
			"conversation_usage_metadata":{
				"token_usage":[
					{"model_id":"Claude Opus 4","warp_tokens":1000,"byok_tokens":0}
				]
			}
		}`,
		"2026-04-07 10:00:00",
	)
	mustExecProcessProviderSQL(t, database,
		`INSERT INTO ai_queries
			(exchange_id, conversation_id, start_ts, input, working_directory,
			 output_status, model_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"ex-001",
		"conv-001",
		"2026-04-07 09:50:00.000000",
		`[{"Query":{"text":"Use the provider parser.","context":[]}}]`,
		"/repo/app",
		`"Completed"`,
		"auto-genius",
	)
}

type configuredChangedPathProviderFactory struct {
	agent      parser.AgentType
	sourcePath string
}

func (f configuredChangedPathProviderFactory) Definition() parser.AgentDef {
	return parser.AgentDef{Type: f.agent}
}

func (f configuredChangedPathProviderFactory) Capabilities() parser.Capabilities {
	return parser.Capabilities{}
}

func (f configuredChangedPathProviderFactory) NewProvider(
	cfg parser.ProviderConfig,
) parser.Provider {
	return configuredChangedPathProvider{
		ProviderBase: parser.ProviderBase{
			Def:    f.Definition(),
			Config: cfg.Clone(),
		},
		sourcePath: f.sourcePath,
	}
}

type configuredChangedPathProvider struct {
	parser.ProviderBase
	sourcePath string
}

func (p configuredChangedPathProvider) SourcesForChangedPath(
	context.Context,
	parser.ChangedPathRequest,
) ([]parser.SourceRef, error) {
	return []parser.SourceRef{{
		Provider:       p.Def.Type,
		Key:            p.sourcePath,
		DisplayPath:    p.sourcePath,
		FingerprintKey: p.sourcePath,
		ProjectHint:    "configured_provider",
	}}, nil
}

func (p configuredChangedPathProvider) Parse(
	context.Context,
	parser.ParseRequest,
) (parser.ParseOutcome, error) {
	return parser.ParseOutcome{}, nil
}

func mustExecProcessProviderSQL(
	t *testing.T,
	database *sql.DB,
	query string,
	args ...any,
) {
	t.Helper()
	_, err := database.Exec(query, args...)
	require.NoError(t, err)
}

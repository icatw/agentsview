package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func TestObserveProviderSourceMatchesGeminiLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(
		root,
		"tmp",
		"alias",
		"chats",
		"session-2026-06-19T12-00-gemini-shadow.json",
	)
	writeProviderShadowSourceFile(
		t,
		filepath.Join(root, "projects.json"),
		`{"projects":{"/Users/alice/code/provider-app":"alias"}}`,
	)
	writeProviderShadowSourceFile(
		t,
		sourcePath,
		testjsonl.GeminiSessionJSON(
			"gemini-shadow",
			"alias",
			"2024-01-01T10:00:00Z",
			"2024-01-01T10:00:05Z",
			[]map[string]any{
				testjsonl.GeminiUserMsg("u1", "2024-01-01T10:00:00Z", "hello gemini"),
				testjsonl.GeminiAssistantMsg("a1", "2024-01-01T10:00:05Z", "hi", &testjsonl.GeminiMsgOpts{
					Model: "gemini-2.5-pro",
					ToolCalls: []testjsonl.GeminiToolCall{{
						ID:           "tc1",
						Name:         "read_file",
						DisplayName:  "Read File",
						Args:         map[string]string{"path": "main.go"},
						ResultOutput: "package main",
					}},
				}),
			},
		),
	)

	provider, ok := parser.NewProvider(parser.AgentGemini, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseGeminiSession(
		sourcePath,
		"provider_app",
		"devbox",
	)
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)

	legacySession.File.Hash = observation.Fingerprint.Hash
	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func TestObserveProviderSourceMatchesCopilotLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "session-state", "copilot-shadow", "events.jsonl")
	workspacePath := filepath.Join(root, "session-state", "copilot-shadow", "workspace.yaml")
	writeProviderShadowSourceFile(t, sourcePath, copilotShadowFixture())
	writeProviderShadowSourceFile(t, workspacePath, "name: Provider title\n")

	provider, ok := parser.NewProvider(parser.AgentCopilot, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, legacyUsage, err := parser.ParseCopilotSession(sourcePath, "devbox")
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)

	legacySession.File.Hash = observation.Fingerprint.Hash
	legacySession.File.Size = observation.Fingerprint.Size
	legacySession.File.Mtime = observation.Fingerprint.MTimeNS
	legacySession.UsageEvents = legacyUsage
	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, legacyUsage, observation.Results[0].UsageEvents)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func copilotShadowFixture() string {
	return strings.Join([]string{
		`{"type":"session.start","data":{"sessionId":"copilot-shadow","context":{"cwd":"/Users/alice/code/copilot-app","branch":"main"}},"timestamp":"2025-01-15T10:00:00Z"}`,
		`{"type":"user.message","data":{"content":"hello copilot"},"timestamp":"2025-01-15T10:00:01Z"}`,
		`{"type":"assistant.message","data":{"content":"hi"},"timestamp":"2025-01-15T10:00:02Z"}`,
		`{"type":"session.shutdown","data":{"modelMetrics":{"gpt-5":{"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":30,"cacheWriteTokens":10,"reasoningTokens":5}}}},"timestamp":"2025-01-15T10:00:03Z"}`,
	}, "\n") + "\n"
}

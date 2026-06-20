package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesDeepSeekTUILegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "session_123.json")
	writeProviderShadowSourceFile(t, sourcePath, deepSeekTUIShadowFixture())

	provider, ok := parser.NewProvider(parser.AgentDeepSeekTUI, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseDeepSeekTUISession(sourcePath, "devbox")
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)

	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func deepSeekTUIShadowFixture() string {
	return `{
  "metadata": {
    "id": "session_123",
    "title": "Investigate DeepSeek TUI",
    "created_at": "2026-06-01T10:00:00Z",
    "updated_at": "2026-06-01T10:02:00Z",
    "model": "deepseek-chat",
    "workspace": "/Users/alice/code/sample-project"
  },
  "messages": [
    {"role": "user", "content": "Inspect server logs", "timestamp": "2026-06-01T10:00:05Z"},
    {"role": "assistant", "content": [{"type": "text", "text": "The server failed during startup."}], "timestamp": "2026-06-01T10:00:10Z"}
  ]
}`
}

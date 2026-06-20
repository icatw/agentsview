package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesCommandCodeLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "project", "sess_123.jsonl")
	writeProviderShadowSourceFile(t, sourcePath, commandCodeShadowFixture())

	provider, ok := parser.NewProvider(parser.AgentCommandCode, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseCommandCodeSession(sourcePath, "devbox")
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

func TestObserveProviderSourceMatchesIflowLegacyParser(t *testing.T) {
	root := t.TempDir()
	project := "test-project"
	rawID := "5de701fc-7454-4858-a249-95cac4fd3b51"
	sourcePath := filepath.Join(root, project, "session-"+rawID+".jsonl")
	copyProviderShadowFixture(t, "../parser/testdata/iflow/session-"+rawID+".jsonl", sourcePath)

	provider, ok := parser.NewProvider(parser.AgentIflow, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacyResults, err := parser.ParseIflowSession(sourcePath, project, "devbox")
	require.NoError(t, err)
	require.NotEmpty(t, legacyResults)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, len(legacyResults))

	assert.Equal(t, legacyResults, observation.Results)
	assert.Equal(t, []string{"iflow:" + rawID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func writeProviderShadowSourceFile(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func copyProviderShadowFixture(t *testing.T, src, dst string) {
	t.Helper()

	data, err := os.ReadFile(src)
	require.NoError(t, err)
	writeProviderShadowSourceFile(t, dst, string(data))
}

func commandCodeShadowFixture() string {
	return `{"id":"m1","timestamp":"2026-06-01T10:00:00Z","sessionId":"sess_123","role":"user","content":[{"type":"text","text":"Inspect server logs"}],"gitBranch":"feature/command-code","metadata":{"version":2,"cwd":"/Users/alice/code/sample-project"}}
{"id":"m2","timestamp":"2026-06-01T10:00:03Z","sessionId":"sess_123","role":"assistant","content":[{"type":"text","text":"The error is in the startup path."}],"gitBranch":"feature/command-code","metadata":{"version":2}}`
}

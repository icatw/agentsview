package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesQwenLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(
		root,
		"-Users-alice-code-sample-project",
		"chats",
		"session-123.jsonl",
	)
	writeProviderShadowSourceFile(t, sourcePath, qwenShadowFixture("session-123"))

	provider, ok := parser.NewProvider(parser.AgentQwen, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseQwenSession(
		sourcePath,
		"sample_project",
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

func qwenShadowFixture(sessionID string) string {
	return strings.Join([]string{
		`{"uuid":"u1","sessionId":"` + sessionID + `","timestamp":"2026-05-05T11:08:38.572Z","type":"user","cwd":"/Users/alice/code/sample-project","message":{"role":"user","parts":[{"text":"Calculate .089 * 7.85788"}]}}`,
		`{"uuid":"u2","sessionId":"` + sessionID + `","timestamp":"2026-05-05T11:08:46.529Z","type":"assistant","cwd":"/Users/alice/code/sample-project","model":"qwen","message":{"role":"model","parts":[{"text":"The user wants multiplication.","thought":true},{"text":"0.089 times 7.85788 is 0.69935132"}]},"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"cachedContentTokenCount":5}}`,
	}, "\n")
}

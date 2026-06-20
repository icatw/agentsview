package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesPositronLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "positron-shadow"
	hashDir := filepath.Join(root, "workspaceStorage", "workspace-hash")
	chatDir := filepath.Join(hashDir, "chatSessions")
	sourcePath := filepath.Join(chatDir, sessionID+".jsonl")
	writeProviderShadowSourceFile(
		t,
		filepath.Join(hashDir, "workspace.json"),
		`{"folder":"file:///Users/alice/code/positron-app"}`,
	)
	writeProviderShadowSourceFile(t, sourcePath, vscodeCopilotShadowFixture(sessionID))

	provider, ok := parser.NewProvider(parser.AgentPositron, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParsePositronSession(
		sourcePath, "positron-app", "devbox",
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

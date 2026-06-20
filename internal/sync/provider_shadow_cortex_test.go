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

func TestObserveProviderSourceMatchesCortexLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "11111111-2222-3333-4444-555555555555"
	sourcePath := filepath.Join(root, sessionID+".json")
	writeProviderShadowSourceFile(t, sourcePath, cortexShadowMetadata(sessionID))
	writeProviderShadowSourceFile(
		t,
		filepath.Join(root, sessionID+".history.jsonl"),
		cortexShadowHistory(),
	)

	provider, ok := parser.NewProvider(parser.AgentCortex, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseCortexSession(sourcePath, "devbox")
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

func cortexShadowMetadata(sessionID string) string {
	return `{
	"session_id": "` + sessionID + `",
	"title": "Test session",
	"working_directory": "/home/user/project",
	"created_at": "2024-06-01T10:00:00Z",
	"last_updated": "2024-06-01T10:05:00Z"
}`
}

func cortexShadowHistory() string {
	return strings.Join([]string{
		`{"role":"user","id":"m1","content":[{"type":"text","text":"Hello from JSONL"}]}`,
		`{"role":"assistant","id":"m2","content":[{"type":"text","text":"Got it"}]}`,
	}, "\n")
}

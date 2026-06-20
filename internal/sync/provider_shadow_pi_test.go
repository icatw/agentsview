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

func TestObserveProviderSourceMatchesPiLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "encoded-cwd", "session-123.jsonl")
	writeProviderShadowSourceFile(t, sourcePath, piShadowFixture("session-123"))

	provider, ok := parser.NewProvider(parser.AgentPi, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParsePiSession(sourcePath, "", "devbox")
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

func piShadowFixture(sessionID string) string {
	return strings.Join([]string{
		`{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2025-01-01T10:00:00Z","cwd":"/Users/alice/code/pi-project"}`,
		`{"type":"message","id":"msg-1","timestamp":"2025-01-01T10:00:01Z","message":{"role":"user","content":"Inspect the Pi source."}}`,
		`{"type":"message","id":"msg-2","timestamp":"2025-01-01T10:00:02Z","message":{"role":"assistant","content":"Looks ready.","model":"claude-opus-4-5","usage":{"input_tokens":10,"output_tokens":5}}}`,
	}, "\n")
}

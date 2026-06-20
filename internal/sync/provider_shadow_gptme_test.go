package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesGptmeLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "2026-06-13-write-hello-world"
	sourcePath := filepath.Join(root, sessionID, "conversation.jsonl")
	writeProviderShadowSourceFile(t, sourcePath, gptmeShadowFixture())

	provider, ok := parser.NewProvider(parser.AgentGptme, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseGptmeSession(sourcePath, "devbox")
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

func gptmeShadowFixture() string {
	return `{"role":"user","content":"Write hello world.","timestamp":"2026-06-13T10:00:01.000000"}` + "\n" +
		`{"role":"assistant","content":"Hello from gptme.","timestamp":"2026-06-13T10:00:02.000000","metadata":{"model":"demo-model","usage":{"input_tokens":10,"output_tokens":4}}}` + "\n"
}

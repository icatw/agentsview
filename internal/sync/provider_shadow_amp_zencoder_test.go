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

func TestObserveProviderSourceMatchesAmpLegacyParser(t *testing.T) {
	root := t.TempDir()
	threadID := "T-019ca26f-aaaa-bbbb-cccc-dddddddddddd"
	sourcePath := filepath.Join(root, threadID+".json")
	writeProviderShadowSourceFile(t, sourcePath, ampShadowFixture(threadID))

	provider, ok := parser.NewProvider(parser.AgentAmp, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseAmpSession(sourcePath, "devbox")
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

func TestObserveProviderSourceMatchesZencoderLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "abc-def-123"
	sourcePath := filepath.Join(root, sessionID+".jsonl")
	writeProviderShadowSourceFile(t, sourcePath, zencoderShadowFixture(sessionID))

	provider, ok := parser.NewProvider(parser.AgentZencoder, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseZencoderSession(sourcePath, "devbox")
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

func ampShadowFixture(threadID string) string {
	return `{
  "v": 1,
  "id": "` + threadID + `",
  "created": 1704067200000,
  "title": "Migrate database schema",
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "Migrate the DB schema."}]},
    {"role": "assistant", "content": [{"type": "text", "text": "Sure, I will help."}]}
  ],
  "env": {"initial": {"trees": [{"displayName": "amp-project"}]}},
  "meta": {"traces": []}
}`
}

func zencoderShadowFixture(sessionID string) string {
	return strings.Join([]string{
		`{"id":"` + sessionID + `","createdAt":"2024-01-01T00:00:00Z","updatedAt":"2024-01-01T00:01:00Z"}`,
		`{"role":"system","content":"Working directory: /Users/alice/code/sample-project"}`,
		`{"role":"user","content":[{"type":"text","text":"hello"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"OK."}]}`,
	}, "\n")
}

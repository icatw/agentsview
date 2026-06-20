package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesHermesLegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "child.jsonl")
	writeProviderShadowSourceFile(
		t,
		sourcePath,
		hermesShadowJSONLFixture("provider question"),
	)

	provider, ok := parser.NewProvider(parser.AgentHermes, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseHermesSession(
		sourcePath,
		"",
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

func hermesShadowJSONLFixture(firstMessage string) string {
	return `{"role":"session_meta","platform":"cli","timestamp":"2026-05-14T10:00:00.000000"}` + "\n" +
		`{"role":"user","content":"` + firstMessage + `","timestamp":"2026-05-14T10:01:00.000000"}` + "\n" +
		`{"role":"assistant","content":"Done.","timestamp":"2026-05-14T10:02:00.000000"}` + "\n"
}

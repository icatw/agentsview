package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesQwenPawLegacyParser(t *testing.T) {
	root := t.TempDir()
	rootPath := writeQwenPawShadowSession(t, root, "default", "", "root_1", "root question")
	consolePath := writeQwenPawShadowSession(
		t,
		root,
		"default",
		"console",
		"console_1",
		"console question",
	)

	provider, ok := parser.NewProvider(parser.AgentQwenPaw, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)

	paths := map[string]struct{}{
		rootPath:    {},
		consolePath: {},
	}
	for _, source := range sources {
		_, ok := paths[source.DisplayPath]
		require.True(t, ok, "unexpected source %s", source.DisplayPath)

		legacySession, legacyMessages, err := parser.ParseQwenPawSession(
			source.DisplayPath,
			source.ProjectHint,
			"devbox",
		)
		require.NoError(t, err)
		require.NotNil(t, legacySession)

		observation, err := ObserveProviderSource(
			context.Background(),
			provider,
			ProviderObserveRequest{
				Source:  source,
				Machine: "devbox",
			},
		)
		require.NoError(t, err)
		require.Len(t, observation.Results, 1)
		legacySession.File.Hash = observation.Fingerprint.Hash

		assert.Equal(t, *legacySession, observation.Results[0].Session)
		assert.Equal(t, legacyMessages, observation.Results[0].Messages)
		assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
		assert.Empty(t, observation.Planned.Diagnostics)
	}
}

func writeQwenPawShadowSession(
	t *testing.T,
	root string,
	workspace string,
	subdir string,
	stem string,
	firstMessage string,
) string {
	t.Helper()
	parts := []string{root, workspace, "sessions"}
	if subdir != "" {
		parts = append(parts, subdir)
	}
	dir := filepath.Join(parts...)
	path := filepath.Join(dir, stem+".json")
	writeProviderShadowSourceFile(t, path, qwenPawShadowFixture(firstMessage))
	return path
}

func qwenPawShadowFixture(firstMessage string) string {
	return `{"agent":{"memory":{"content":[` +
		`[{"id":"u1","name":"user","role":"user","content":[{"type":"text","text":"` + firstMessage + `"}],"metadata":{},"timestamp":"2026-04-19 22:37:34.004"},[]],` +
		`[{"id":"a1","name":"Friday","role":"assistant","content":[{"type":"text","text":"Done."}],"metadata":{},"timestamp":"2026-04-19 22:37:35.123"},[]]` +
		`]}}}`
}

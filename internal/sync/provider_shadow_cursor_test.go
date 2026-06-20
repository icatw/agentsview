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

func TestObserveProviderSourceMatchesCursorLegacyParser(t *testing.T) {
	root := t.TempDir()
	firstProject := "Users-fiona-Documents-first"
	secondProject := "Users-fiona-Documents-second"
	firstPath := writeCursorShadowJSONLTranscript(
		t,
		filepath.Join(root, firstProject, "agent-transcripts"),
		"shared.jsonl",
		"first question",
	)
	secondPath := writeCursorShadowJSONLTranscript(
		t,
		filepath.Join(root, secondProject, "agent-transcripts"),
		"shared.jsonl",
		"second question",
	)

	provider, ok := parser.NewProvider(parser.AgentCursor, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)

	paths := map[string]struct{}{
		firstPath:  {},
		secondPath: {},
	}
	for _, source := range sources {
		_, ok := paths[source.DisplayPath]
		require.True(t, ok, "unexpected source %s", source.DisplayPath)

		legacySession, legacyMessages, err := parser.ParseCursorSession(
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

func writeCursorShadowJSONLTranscript(
	t *testing.T,
	dir string,
	name string,
	firstMessage string,
) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"role":"user","message":{"content":"<user_query>`+firstMessage+`</user_query>"}}`+"\n"+
			`{"role":"assistant","message":{"content":"Done."}}`+"\n"),
		0o644,
	))
	return path
}

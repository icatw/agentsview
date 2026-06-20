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

func TestObserveProviderSourceMatchesKimiLegacyParser(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "abc123", "uuid-1", "wire.jsonl")
	newPath := filepath.Join(
		root,
		"wd_kimi-code_057f5c09ee3f",
		"session_uuid-2",
		"agents",
		"main",
		"wire.jsonl",
	)
	writeProviderShadowSourceFile(t, legacyPath, kimiShadowFixture("legacy question"))
	writeProviderShadowSourceFile(t, newPath, kimiShadowFixture("new layout question"))

	provider, ok := parser.NewProvider(parser.AgentKimi, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)

	for _, source := range sources {
		t.Run(source.ProjectHint, func(t *testing.T) {
			legacySession, legacyMessages, err := parser.ParseKimiSession(
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
		})
	}
}

func kimiShadowFixture(firstMessage string) string {
	return strings.Join([]string{
		`{"type":"metadata","protocol_version":"1.3"}`,
		`{"timestamp":1704067200.0,"message":{"type":"TurnBegin","payload":{"user_input":[{"type":"text","text":"` + firstMessage + `"}]}}}`,
		`{"timestamp":1704067201.0,"message":{"type":"ContentPart","payload":{"type":"text","text":"Done."}}}`,
		`{"timestamp":1704067202.0,"message":{"type":"TurnEnd","payload":{}}}`,
	}, "\n")
}

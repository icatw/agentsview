package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesClawLegacyParsers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent parser.AgentType
		parse func(string, string, string) (*parser.ParsedSession, []parser.ParsedMessage, error)
	}{
		{
			name:  "openclaw",
			agent: parser.AgentOpenClaw,
			parse: parser.ParseOpenClawSession,
		},
		{
			name:  "qclaw",
			agent: parser.AgentQClaw,
			parse: parser.ParseQClawSession,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sourcePath := filepath.Join(root, "main", "sessions", "abc-123.jsonl")
			writeProviderShadowSourceFile(
				t,
				sourcePath,
				clawShadowFixture("abc-123", "provider question"),
			)

			provider, ok := parser.NewProvider(tc.agent, parser.ProviderConfig{
				Roots:   []string{root},
				Machine: "devbox",
			})
			require.True(t, ok)
			sources, err := provider.Discover(context.Background())
			require.NoError(t, err)
			require.Len(t, sources, 1)

			legacySession, legacyMessages, err := tc.parse(sourcePath, "", "devbox")
			require.NoError(t, err)
			require.NotNil(t, legacySession)

			observation, err := ObserveProviderSource(
				context.Background(),
				provider,
				ProviderObserveRequest{
					Source:  sources[0],
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

func clawShadowFixture(sessionID string, firstMessage string) string {
	return `{"type":"session","version":3,"id":"` + sessionID + `","timestamp":"2026-02-25T10:00:00Z","cwd":"/home/user/project"}` + "\n" +
		`{"type":"message","id":"m1","timestamp":"2026-02-25T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"` + firstMessage + `"}],"timestamp":"2026-02-25T10:00:01Z"}}` + "\n" +
		`{"type":"message","id":"m2","timestamp":"2026-02-25T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}],"timestamp":"2026-02-25T10:00:02Z"}}` + "\n"
}

package sync

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesWorkBuddyLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "11111111-1111-4111-8111-111111111111"
	subagentID := "agent-123"
	sourcePath := filepath.Join(root, "proj", sessionID+".jsonl")
	subagentPath := filepath.Join(root, "proj", sessionID, "subagents", subagentID+".jsonl")
	writeProviderShadowSourceFile(t, sourcePath, workBuddyShadowFixture("hello"))
	writeProviderShadowSourceFile(t, subagentPath, workBuddyShadowFixture("sub task"))

	provider, ok := parser.NewProvider(parser.AgentWorkBuddy, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)

	for _, source := range sources {
		t.Run(filepath.Base(source.DisplayPath), func(t *testing.T) {
			legacySession, legacyMessages, err := parser.ParseWorkBuddySession(
				source.DisplayPath,
				"proj",
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

func workBuddyShadowFixture(firstMessage string) string {
	return fmt.Sprintf(
		`{"id":"u1","timestamp":1778749186168,"type":"message","role":"user","content":[{"type":"input_text","text":%q}],"cwd":"/tmp/cwd-project"}
{"id":"a1","timestamp":1778749187168,"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}],"providerData":{"model":"gpt-5.5","usage":{"inputTokens":20,"outputTokens":4,"cacheReadInputTokens":5}}}
{"id":"fc1","timestamp":1778749188168,"type":"function_call","name":"Bash","callId":"call_1","arguments":"{\"command\":\"pwd\"}"}
`, firstMessage)
}

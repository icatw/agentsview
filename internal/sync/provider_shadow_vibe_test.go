package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesVibeLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionDir := "session_20260616_083518_abc123"
	sessionID := "uuid-1234"
	messagesPath := filepath.Join(root, sessionDir, "messages.jsonl")
	metaPath := filepath.Join(root, sessionDir, "meta.json")
	writeProviderShadowSourceFile(t, messagesPath, vibeShadowMessagesFixture("provider question"))
	writeProviderShadowSourceFile(t, metaPath, vibeShadowMetaFixture(sessionID, "Provider title"))

	provider, ok := parser.NewProvider(parser.AgentVibe, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, legacyUsageEvents, err := parser.ParseVibeSessionWrapper(
		messagesPath,
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
	legacySession.File.Size = observation.Fingerprint.Size
	legacySession.File.Mtime = observation.Fingerprint.MTimeNS
	legacySession.File.Hash = observation.Fingerprint.Hash

	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	require.NotEmpty(t, legacyUsageEvents)
	require.NotEmpty(t, observation.Results[0].UsageEvents)
	assert.Equal(t, legacyUsageEvents, observation.Results[0].UsageEvents)
	assert.Equal(t, []string{"vibe:" + sessionDir}, observation.ExcludedSessionIDs)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func vibeShadowMessagesFixture(firstMessage string) string {
	return `{"role":"user","content":"` + firstMessage + `"}` + "\n" +
		`{"role":"assistant","content":"Done."}` + "\n"
}

func vibeShadowMetaFixture(sessionID, title string) string {
	return `{"session_id":"` + sessionID + `","title":"` + title + `",` +
		`"config":{"active_model":"mistral-medium-3.5"},` +
		`"stats":{"session_prompt_tokens":100,"session_completion_tokens":40}}`
}

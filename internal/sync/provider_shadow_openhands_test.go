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

func TestObserveProviderSourceMatchesOpenHandsLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "086c7ecf-6cb7-46b6-9fbc-b900358d1247"
	sessionDir := writeOpenHandsShadowSession(
		t,
		root,
		"086c7ecf6cb746b69fbcb900358d1247",
		sessionID,
		"provider question",
	)

	provider, ok := parser.NewProvider(parser.AgentOpenHands, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseOpenHandsSession(sessionDir, "devbox")
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

func writeOpenHandsShadowSession(
	t *testing.T,
	root string,
	dirName string,
	sessionID string,
	firstMessage string,
) string {
	t.Helper()
	sessionDir := filepath.Join(root, dirName)
	eventsDir := filepath.Join(sessionDir, "events")
	require.NoError(t, os.MkdirAll(eventsDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sessionDir, "base_state.json"),
		[]byte(`{"id":"`+sessionID+`","agent":{"llm":{"model":"test-model"}}}`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(sessionDir, "TASKS.json"),
		[]byte(`[]`),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(eventsDir, "event-00000-user.json"),
		[]byte(`{
			"id":"e0",
			"timestamp":"2026-04-02T15:25:40.706887",
			"source":"user",
			"llm_message":{"role":"user","content":[{"type":"text","text":"`+firstMessage+`"}]},
			"kind":"MessageEvent"
		}`),
		0o644,
	))
	return sessionDir
}

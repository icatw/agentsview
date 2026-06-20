package sync_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
	"go.kenn.io/agentsview/internal/sync"
)

func TestObserveProviderSourceMatchesAntigravityIDELegacyParser(t *testing.T) {
	root := t.TempDir()
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dbPath := filepath.Join(root, "conversations", id+".db")
	writeAntigravityShadowDB(t, dbPath, "ide prompt from db")

	provider, ok := parser.NewProvider(parser.AgentAntigravity, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, legacyUsageEvents, err := parser.ParseAntigravitySession(
		dbPath, "", "devbox",
	)
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := sync.ObserveProviderSource(context.Background(), provider, sync.ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	legacySession.File.Hash = observation.Fingerprint.Hash

	assert.True(t, observation.ForceReplace)
	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, legacyUsageEvents, observation.Results[0].UsageEvents)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func TestObserveProviderSourceMatchesAntigravityCLILegacyParser(t *testing.T) {
	root := t.TempDir()
	id := "33333333-4444-5555-6666-777777777777"
	dbPath := filepath.Join(root, "conversations", id+".db")
	writeAntigravityShadowDB(t, dbPath, "cli prompt from db")
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "history.jsonl"),
		[]byte(`{"display":"cli prompt from db","timestamp":1779000000000,`+
			`"workspace":"/tmp/antigravity-cli","conversationId":"`+id+`"}`+"\n"),
		0o644,
	))

	provider, ok := parser.NewProvider(parser.AgentAntigravityCLI, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, dbPath, sources[0].DisplayPath)

	legacySession, legacyMessages, legacyUsageEvents, legacyStatus, err :=
		parser.ParseAntigravityCLISessionWithStatus(
			dbPath, "/tmp/antigravity-cli", "devbox",
		)
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := sync.ObserveProviderSource(context.Background(), provider, sync.ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	legacySession.File.Hash = observation.Fingerprint.Hash

	assert.True(t, observation.ForceReplace)
	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, legacyUsageEvents, observation.Results[0].UsageEvents)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	if legacyStatus.NeedsRetry {
		assert.Equal(t, []string{legacySession.ID}, observation.Planned.RetrySessionIDs())
	} else {
		assert.Empty(t, observation.Planned.RetrySessionIDs())
	}
	assert.Empty(t, observation.Planned.Diagnostics)
}

func writeAntigravityShadowDB(t *testing.T, path, prompt string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	createAntigravityCLIDisplayStepDB(t, path, prompt)
}

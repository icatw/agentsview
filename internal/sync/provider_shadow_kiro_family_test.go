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

func TestObserveProviderSourceMatchesKiroSQLiteLegacyParser(t *testing.T) {
	root := t.TempDir()
	store := createKiroSQLiteDB(t, root)
	sessionID := "sqlite-session"
	store.addSession(
		t,
		"/home/user/code/kiro-app",
		sessionID,
		readKiroSQLiteFixture(t, "standard_payload.json"),
		1779012000000,
		1779012030000,
	)

	provider, ok := parser.NewProvider(parser.AgentKiro, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseKiroSQLiteSession(
		store.path, sessionID, "devbox",
	)
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := sync.ObserveProviderSource(context.Background(), provider, sync.ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)

	assert.True(t, observation.ForceReplace)
	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func TestObserveProviderSourceMatchesKiroIDELegacyParser(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(
		root,
		"workspace-sessions",
		"encoded-workspace",
		"new-session.json",
	)
	writeKiroIDEProviderShadowSource(t, sourcePath, "New IDE question")

	provider, ok := parser.NewProvider(parser.AgentKiroIDE, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseKiroIDESession(
		sourcePath, "devbox",
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

	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func writeKiroIDEProviderShadowSource(t *testing.T, path, question string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(
		`{"sessionId":"new-session",`+
			`"title":"New title",`+
			`"workspaceDirectory":"/home/user/dev/new-app",`+
			`"history":[`+
			`{"message":{"role":"user","content":"`+question+`","id":"m1"}},`+
			`{"message":{"role":"assistant","content":"New IDE answer","id":"m2"}}`+
			`]}`+"\n",
	), 0o644))
}

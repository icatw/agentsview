package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func TestObserveProviderSourceMatchesCodexLegacyParser(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	uuid := "019eb791-cf7d-75c1-8439-9ed74c12abcd"
	sourcePath := filepath.Join(
		root,
		"2026",
		"06",
		"11",
		"rollout-2026-06-11T12-44-06-"+uuid+".jsonl",
	)
	writeProviderShadowSourceFile(
		t,
		sourcePath,
		testjsonl.JoinJSONL(
			testjsonl.CodexSessionMetaJSON(
				uuid,
				"/home/user/code/api",
				"codex_cli_rs",
				"2026-06-11T12:44:06Z",
			),
			testjsonl.CodexMsgJSON("user", "provider question", "2026-06-11T12:44:07Z"),
		),
	)
	writeProviderShadowSourceFile(
		t,
		filepath.Join(base, parser.CodexSessionIndexFilename),
		`{"id":"`+uuid+`","thread_name":"Provider title","updated_at":"2026-06-11T17:34:20Z"}`+"\n",
	)

	provider, ok := parser.NewProvider(parser.AgentCodex, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseCodexSession(sourcePath, "devbox", false)
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

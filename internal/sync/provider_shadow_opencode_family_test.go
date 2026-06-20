package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesOpenCodeFamilyLegacyParsers(t *testing.T) {
	tests := []struct {
		name          string
		agent         parser.AgentType
		sessionSubdir string
		sessionID     string
		project       string
		parse         func(string, string) (*parser.ParsedSession, []parser.ParsedMessage, error)
	}{
		{
			name:          "opencode",
			agent:         parser.AgentOpenCode,
			sessionSubdir: "session",
			sessionID:     "ses_opencode_shadow",
			project:       "opencode-shadow",
			parse:         parser.ParseOpenCodeFile,
		},
		{
			name:          "kilo",
			agent:         parser.AgentKilo,
			sessionSubdir: "session",
			sessionID:     "ses_kilo_shadow",
			project:       "kilo-shadow",
			parse:         parser.ParseKiloFile,
		},
		{
			name:          "mimocode",
			agent:         parser.AgentMiMoCode,
			sessionSubdir: "session_diff",
			sessionID:     "ses_mimo_shadow",
			project:       "mimo-shadow",
			parse:         parser.ParseMiMoCodeFile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			sessionPath := writeOpenCodeFamilyShadowStorage(
				t,
				root,
				tc.sessionSubdir,
				tc.sessionID,
				tc.project,
				"provider question",
			)

			provider, ok := parser.NewProvider(tc.agent, parser.ProviderConfig{
				Roots:   []string{root},
				Machine: "devbox",
			})
			require.True(t, ok)
			sources, err := provider.Discover(context.Background())
			require.NoError(t, err)
			require.Len(t, sources, 1)

			legacySession, legacyMessages, err := tc.parse(sessionPath, "devbox")
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
		})
	}
}

func writeOpenCodeFamilyShadowStorage(
	t *testing.T,
	root,
	sessionSubdir,
	sessionID,
	project,
	firstMessage string,
) string {
	t.Helper()

	sessionPath := filepath.Join(
		root,
		"storage",
		sessionSubdir,
		"global",
		sessionID+".json",
	)
	writeProviderShadowSourceFile(
		t,
		sessionPath,
		`{"id":"`+sessionID+`",`+
			`"directory":"/home/user/code/`+project+`",`+
			`"title":"Provider Session",`+
			`"time":{"created":1700000000000,"updated":1700000060000}}`,
	)
	writeProviderShadowSourceFile(
		t,
		filepath.Join(root, "storage", "message", sessionID, "msg_1.json"),
		`{"id":"msg_1","sessionID":"`+sessionID+`","role":"user",`+
			`"time":{"created":1700000000000}}`,
	)
	writeProviderShadowSourceFile(
		t,
		filepath.Join(root, "storage", "part", "msg_1", "prt_1.json"),
		`{"id":"prt_1","sessionID":"`+sessionID+`","messageID":"msg_1",`+
			`"type":"text","text":"`+firstMessage+`",`+
			`"time":{"created":1700000000000}}`,
	)
	return sessionPath
}

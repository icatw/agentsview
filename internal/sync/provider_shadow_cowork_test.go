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

func TestObserveProviderSourceMatchesCoworkLegacyParser(t *testing.T) {
	root := t.TempDir()
	cli := "c0000000-0000-4000-8000-000000000201"
	sessionUUID := "50000000-0000-4000-8000-000000000201"
	workspaceDir := filepath.Join(root, "org", "workspace")
	sessionDir := filepath.Join(workspaceDir, "local_"+sessionUUID)
	metaPath := sessionDir + ".json"
	transcriptPath := filepath.Join(
		sessionDir,
		".claude",
		"projects",
		"-Users-dev-code-demo",
		cli+".jsonl",
	)
	writeProviderShadowSourceFile(t, metaPath, coworkShadowMeta(sessionUUID, cli))
	writeProviderShadowSourceFile(
		t,
		transcriptPath,
		strings.Join(coworkShadowTranscript(cli), "\n")+"\n",
	)

	provider, ok := parser.NewProvider(parser.AgentCowork, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacyResults, legacyExcluded, err := parser.ParseCoworkSession(
		transcriptPath,
		"devbox",
	)
	require.NoError(t, err)
	require.Len(t, legacyResults, 1)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	legacyResults[0].Session.File.Hash = observation.Fingerprint.Hash

	assert.Equal(t, legacyResults[0].Session, observation.Results[0].Session)
	assert.Equal(t, legacyResults[0].Messages, observation.Results[0].Messages)
	assert.Equal(t, legacyResults[0].UsageEvents, observation.Results[0].UsageEvents)
	assert.Equal(t, legacyExcluded, observation.ExcludedSessionIDs)
	assert.Equal(t, []string{legacyResults[0].Session.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func coworkShadowMeta(sessionUUID, cli string) string {
	return `{"sessionId":"local_` + sessionUUID + `",` +
		`"cliSessionId":"` + cli + `",` +
		`"title":"Provider title",` +
		`"userSelectedFolders":["/Users/dev/code/demo"],` +
		`"createdAt":1772373600000,` +
		`"lastActivityAt":1772373605000}`
}

func coworkShadowTranscript(cli string) []string {
	return []string{
		`{"type":"ai-title","aiTitle":"Auto title","sessionId":"` + cli + `"}`,
		`{"type":"user","uuid":"u1","parentUuid":null,` +
			`"sessionId":"` + cli + `","cwd":"/Users/dev/code/demo",` +
			`"gitBranch":"main","version":"2.1.119",` +
			`"timestamp":"2026-03-01T10:00:00.000Z",` +
			`"message":{"role":"user","content":"provider question"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1",` +
			`"sessionId":"` + cli + `","requestId":"req_1",` +
			`"timestamp":"2026-03-01T10:00:05.000Z",` +
			`"message":{"role":"assistant","id":"msg_1",` +
			`"model":"claude-sonnet-4-6","stop_reason":"end_turn",` +
			`"content":[{"type":"text","text":"Done."}],` +
			`"usage":{"input_tokens":10,"cache_read_input_tokens":2,` +
			`"output_tokens":5}}}`,
	}
}

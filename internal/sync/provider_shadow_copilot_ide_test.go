package sync

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesVSCodeCopilotLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "vscode-shadow"
	hashDir := filepath.Join(root, "workspaceStorage", "workspace-hash")
	chatDir := filepath.Join(hashDir, "chatSessions")
	sourcePath := filepath.Join(chatDir, sessionID+".jsonl")
	writeProviderShadowSourceFile(
		t,
		filepath.Join(hashDir, "workspace.json"),
		`{"folder":"file:///Users/alice/code/copilot-app"}`,
	)
	writeProviderShadowSourceFile(t, sourcePath, vscodeCopilotShadowFixture(sessionID))

	provider, ok := parser.NewProvider(parser.AgentVSCodeCopilot, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseVSCodeCopilotSession(
		sourcePath, "copilot-app", "devbox",
	)
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

func TestObserveProviderSourceMatchesVisualStudioCopilotLegacyParser(t *testing.T) {
	root := t.TempDir()
	conversationID := "4a8f63f6-7626-4416-a874-fc7bd2c3f005"
	tracePath := filepath.Join(
		root,
		"20260612T194439_257709a3_VSGitHubCopilot_traces.jsonl",
	)
	writeProviderShadowSourceFile(
		t,
		tracePath,
		visualStudioCopilotShadowFixture(conversationID),
	)
	virtualPath := parser.VisualStudioCopilotVirtualPath(tracePath, conversationID)

	provider, ok := parser.NewProvider(parser.AgentVSCopilot, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParseVisualStudioCopilotSession(
		virtualPath, "visualstudio", "devbox",
	)
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

func vscodeCopilotShadowFixture(sessionID string) string {
	return strings.Join([]string{
		`{"kind":0,"v":{"version":3,"sessionId":"` + sessionID + `","creationDate":1770650022790,"requests":[]}}`,
		`{"kind":2,"k":["requests"],"v":[{"requestId":"req1","timestamp":1770650031889,"message":{"text":"Hello VS Code","parts":[]},"response":[{"value":"Hi from VS Code"}],"modelId":"copilot/gpt-4o"}]}`,
	}, "\n") + "\n"
}

func visualStudioCopilotShadowFixture(conversationID string) string {
	return visualStudioCopilotShadowTraceLineJSON(conversationID,
		"chat gpt-5.5", "1781293600000000000", "1781293610000000000",
		map[string]string{
			"gen_ai.operation.name": "chat",
			"gen_ai.input.messages": `[{"role":"user","parts":[{"type":"text","content":"Update the XAML."}]}]`,
		}) + "\n"
}

func visualStudioCopilotShadowTraceLineJSON(
	conversationID, name, start, end string,
	attrs map[string]string,
) string {
	allAttrs := []string{
		`{"key":"gen_ai.conversation.id","value":{"stringValue":"` +
			conversationID + `"}}`,
	}
	for key, value := range attrs {
		encoded, _ := json.Marshal(value)
		allAttrs = append(allAttrs,
			`{"key":"`+key+`","value":{"stringValue":`+
				string(encoded)+`}}`,
		)
	}
	return `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"trace","spanId":"span","name":"` +
		name + `","startTimeUnixNano":"` + start +
		`","endTimeUnixNano":"` + end +
		`","attributes":[` + strings.Join(allAttrs, ",") +
		`] }]}]}]}`
}

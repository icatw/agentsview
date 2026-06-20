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

func TestObserveProviderSourceMatchesClaudeLegacyParser(t *testing.T) {
	root := t.TempDir()
	projectDir := "-Users-dev-code-demo"
	sourcePath := filepath.Join(root, projectDir, "claude-shadow.jsonl")
	writeProviderShadowSourceFile(t, sourcePath, claudeShadowFixture())

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacyResults, legacyExcluded, err := parser.ParseClaudeSessionWithExclusions(
		sourcePath,
		"demo",
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

func claudeShadowFixture() string {
	return testjsonl.JoinJSONL(
		testjsonl.ClaudeUserJSON("provider question", "2026-01-02T03:04:05Z"),
		testjsonl.ClaudeAssistantJSON("Done.", "2026-01-02T03:04:06Z"),
	)
}

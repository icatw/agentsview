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
	usagePath := filepath.Join(root, projectDir, "usage-only.jsonl")
	writeProviderShadowSourceFile(t, usagePath, claudeShadowUsageOnlyFixture())

	provider, ok := parser.NewProvider(parser.AgentClaude, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 2)
	sourcesByPath := make(map[string]parser.SourceRef, len(sources))
	for _, source := range sources {
		sourcesByPath[source.DisplayPath] = source
	}
	source, ok := sourcesByPath[sourcePath]
	require.True(t, ok)
	usageSource, ok := sourcesByPath[usagePath]
	require.True(t, ok)

	legacyResults, legacyExcluded, err := parser.ParseClaudeSessionWithExclusions(
		sourcePath,
		"demo",
		"devbox",
	)
	require.NoError(t, err)
	require.Len(t, legacyResults, 1)
	require.True(t, claudeShadowHasTokenUsage(legacyResults[0].Messages))

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  source,
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	require.True(t, claudeShadowHasTokenUsage(observation.Results[0].Messages))
	legacyResults[0].Session.File.Hash = observation.Fingerprint.Hash

	assert.Equal(t, legacyResults[0].Session, observation.Results[0].Session)
	assert.Equal(t, legacyResults[0].Messages, observation.Results[0].Messages)
	assert.Equal(t, legacyResults[0].UsageEvents, observation.Results[0].UsageEvents)
	assert.Equal(t, legacyExcluded, observation.ExcludedSessionIDs)
	assert.Equal(t, []string{legacyResults[0].Session.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)

	_, usageLegacyExcluded, err := parser.ParseClaudeSessionWithExclusions(
		usagePath,
		"demo",
		"devbox",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"usage-only"}, usageLegacyExcluded)

	usageObservation, err := ObserveProviderSource(
		context.Background(),
		provider,
		ProviderObserveRequest{
			Source:  usageSource,
			Machine: "devbox",
		},
	)
	require.NoError(t, err)
	assert.Empty(t, usageObservation.Results)
	assert.Equal(t, usageLegacyExcluded, usageObservation.ExcludedSessionIDs)
	assert.Empty(t, usageObservation.Planned.Diagnostics)
}

func claudeShadowFixture() string {
	return testjsonl.JoinJSONL(
		testjsonl.ClaudeUserJSON("provider question", "2026-01-02T03:04:05Z"),
		`{"type":"assistant","timestamp":"2026-01-02T03:04:06Z","message":{"model":"claude-sonnet-4-20250514","content":[{"type":"text","text":"Done."}],"usage":{"input_tokens":100,"output_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":5}}}`,
	)
}

func claudeShadowUsageOnlyFixture() string {
	return testjsonl.JoinJSONL(
		testjsonl.ClaudeUserJSON(
			"<command-name>/usage</command-name>\n"+
				"<command-message>usage</command-message>",
			"2026-01-02T03:04:07Z",
		),
	)
}

func claudeShadowHasTokenUsage(messages []parser.ParsedMessage) bool {
	for _, message := range messages {
		if len(message.TokenUsage) > 0 ||
			message.HasContextTokens ||
			message.HasOutputTokens {
			return true
		}
	}
	return false
}

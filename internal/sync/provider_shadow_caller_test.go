package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/dbtest"
	"go.kenn.io/agentsview/internal/parser"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func TestProcessFileShadowObservesProviderWithoutReplacingLegacy(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", "shadow-caller.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"compare through the caller",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
			testjsonl.ClaudeAssistantJSON(
				"provider stayed shadow-only",
				"2026-06-01T10:01:00Z",
			),
		)),
		0o644,
	))

	legacyResults, legacyExcluded, err := parser.ParseClaudeSessionWithExclusions(
		sourcePath, "demo", "devbox",
	)
	require.NoError(t, err)
	require.Len(t, legacyResults, 1)
	require.Empty(t, legacyExcluded)
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	providerResult := legacyResults[0]
	providerResult.Session.File.Inode, providerResult.Session.File.Device = getFileIdentity(info)
	hash, err := ComputeFileHash(sourcePath)
	require.NoError(t, err)
	providerResult.Session.File.Hash = hash

	source := parser.SourceRef{
		Provider:       parser.AgentClaude,
		Key:            sourcePath,
		DisplayPath:    sourcePath,
		FingerprintKey: sourcePath,
		ProjectHint:    "demo",
	}
	provider := &shadowCallerProvider{
		shadowTestProvider: shadowTestProvider{
			ProviderBase: parser.ProviderBase{
				Def: parser.AgentDef{
					Type:        parser.AgentClaude,
					DisplayName: "Claude Code",
				},
			},
			fingerprint: parser.SourceFingerprint{
				Key:     sourcePath,
				Size:    info.Size(),
				MTimeNS: info.ModTime().UnixNano(),
			},
			outcome: parser.ParseOutcome{
				Results: []parser.ParseResultOutcome{{
					Result:      providerResult,
					DataVersion: parser.DataVersionCurrent,
				}},
				ResultSetComplete: true,
			},
		},
		source: source,
	}
	var comparisons []ProviderShadowComparison
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationShadowCompare,
		},
		ProviderShadowRecorder: func(comparison ProviderShadowComparison) {
			comparisons = append(comparisons, comparison)
		},
	})

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.NoError(t, result.err)
	require.Len(t, result.results, 1)
	assert.Equal(t, "shadow-caller", result.results[0].Session.ID)
	assert.Equal(t, parser.AgentClaude, result.results[0].Session.Agent)
	require.Len(t, comparisons, 1)
	assert.NoError(t, comparisons[0].Err)
	assert.Empty(t, comparisons[0].Mismatches)
	assert.Equal(t, sourcePath, comparisons[0].File.Path)
	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
}

func TestProcessFileShadowRecordsCachedSkipAsNotComparable(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "-Users-dev-code-demo", "shadow-skip.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(sourcePath), 0o755))
	require.NoError(t, os.WriteFile(
		sourcePath,
		[]byte(testjsonl.JoinJSONL(
			testjsonl.ClaudeUserJSON(
				"already cached",
				"2026-06-01T10:00:00Z",
				"/Users/dev/code/demo",
			),
		)),
		0o644,
	))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	provider := &shadowCallerProvider{
		shadowTestProvider: shadowTestProvider{
			ProviderBase: parser.ProviderBase{
				Def: parser.AgentDef{
					Type:        parser.AgentClaude,
					DisplayName: "Claude Code",
				},
			},
		},
		source: parser.SourceRef{
			Provider: parser.AgentClaude,
			Key:      sourcePath,
		},
	}
	var comparisons []ProviderShadowComparison
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationShadowCompare,
		},
		ProviderShadowRecorder: func(comparison ProviderShadowComparison) {
			comparisons = append(comparisons, comparison)
		},
	})
	engine.InjectSkipCache(map[string]int64{
		sourcePath: info.ModTime().UnixNano(),
	})

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.True(t, result.skip)
	require.Len(t, comparisons, 1)
	assert.Equal(t, "legacy skip", comparisons[0].NotComparableReason)
	assert.Empty(t, comparisons[0].Mismatches)
	assert.Empty(t, provider.calls)
}

type shadowCallerProvider struct {
	shadowTestProvider
	source parser.SourceRef
}

func (p *shadowCallerProvider) FindSource(
	context.Context,
	parser.FindSourceRequest,
) (parser.SourceRef, bool, error) {
	return p.source, true, nil
}

type shadowCallerFactory struct {
	provider *shadowCallerProvider
}

func (f shadowCallerFactory) Definition() parser.AgentDef {
	return f.provider.Definition()
}

func (f shadowCallerFactory) Capabilities() parser.Capabilities {
	return f.provider.Capabilities()
}

func (f shadowCallerFactory) NewProvider(parser.ProviderConfig) parser.Provider {
	return f.provider
}

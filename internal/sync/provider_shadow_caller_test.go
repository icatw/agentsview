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

func TestProcessFileProviderAuthoritativeUsesInjectedProvider(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	source := parser.SourceRef{
		Provider:       parser.AgentClaude,
		Key:            sourcePath,
		DisplayPath:    sourcePath,
		FingerprintKey: sourcePath,
		ProjectHint:    "provider-project",
	}
	providerResult := parser.ParseResult{
		Session: parser.ParsedSession{
			ID:      "provider-owned",
			Project: "provider-project",
			Agent:   parser.AgentClaude,
			Machine: "devbox",
			File: parser.FileInfo{
				Path:  sourcePath,
				Mtime: info.ModTime().UnixNano(),
			},
		},
		Messages: []parser.ParsedMessage{{
			Role:      parser.RoleUser,
			Content:   "parsed through provider",
			Timestamp: info.ModTime(),
			Ordinal:   0,
		}},
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
				ForceReplace:      true,
			},
		},
		source: source,
	}
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentClaude: {root},
		},
		Machine: "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
		},
	})

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.NoError(t, result.err)
	require.Len(t, result.results, 1)
	assert.Equal(t, "provider-owned", result.results[0].Session.ID)
	assert.Equal(t, "provider-project", result.results[0].Session.Project)
	assert.Equal(t, info.ModTime().UnixNano(), result.mtime)
	assert.True(t, result.forceReplace)
	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
}

func TestProcessFileProviderAuthoritativeKeepsRetryStatePerResult(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "multi-provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	source := parser.SourceRef{
		Provider:       parser.AgentClaude,
		Key:            sourcePath,
		DisplayPath:    sourcePath,
		FingerprintKey: sourcePath,
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
				Results: []parser.ParseResultOutcome{
					{
						Result: parser.ParseResult{Session: parser.ParsedSession{
							ID: "provider-current", Agent: parser.AgentClaude,
						}},
						DataVersion: parser.DataVersionCurrent,
					},
					{
						Result: parser.ParseResult{Session: parser.ParsedSession{
							ID: "provider-retry", Agent: parser.AgentClaude,
						}},
						DataVersion: parser.DataVersionNeedsRetry,
					},
				},
				ResultSetComplete: true,
			},
		},
		source: source,
	}
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{parser.AgentClaude: {root}},
		Machine:   "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
		},
	})

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.NoError(t, result.err)
	require.Len(t, result.results, 2)
	assert.False(t, result.needsRetryForSession("provider-current"))
	assert.True(t, result.needsRetryForSession("provider-retry"))
}

func TestProcessFileProviderAuthoritativeForceParseAllowsStaleSourceLookup(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "force-provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
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
			fingerprint: parser.SourceFingerprint{
				Key:     sourcePath,
				Size:    info.Size(),
				MTimeNS: info.ModTime().UnixNano(),
			},
			outcome: parser.ParseOutcome{ResultSetComplete: true},
		},
		source: parser.SourceRef{
			Provider:       parser.AgentClaude,
			Key:            sourcePath,
			DisplayPath:    sourcePath,
			FingerprintKey: sourcePath,
		},
	}
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{parser.AgentClaude: {root}},
		Machine:   "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
		},
	})
	engine.forceParse = true

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.NoError(t, result.err)
	assert.False(t, provider.findRequest.RequireFreshSource)
	assert.True(t, provider.parseRequest.ForceParse)
}

func TestProcessFileProviderAuthoritativeNotFoundFails(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "missing-provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
	found := false
	provider := &shadowCallerProvider{
		shadowTestProvider: shadowTestProvider{
			ProviderBase: parser.ProviderBase{
				Def: parser.AgentDef{
					Type:        parser.AgentClaude,
					DisplayName: "Claude Code",
				},
			},
		},
		findFound: &found,
	}
	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{parser.AgentClaude: {root}},
		Machine:   "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
		},
	})

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.Error(t, result.err)
	assert.Contains(t, result.err.Error(), "provider source not found")
	assert.Empty(t, provider.calls)
}

type shadowCallerProvider struct {
	shadowTestProvider
	source      parser.SourceRef
	findRequest parser.FindSourceRequest
	findFound   *bool
}

func (p *shadowCallerProvider) FindSource(
	_ context.Context,
	req parser.FindSourceRequest,
) (parser.SourceRef, bool, error) {
	p.findRequest = req
	if p.findFound != nil && !*p.findFound {
		return parser.SourceRef{}, false, nil
	}
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

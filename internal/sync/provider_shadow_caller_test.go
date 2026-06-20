package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/db"
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
				Key:     sourcePath + "#fingerprint",
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

func TestProcessFileProviderAuthoritativeSuppressesUncleanSkipCache(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "unclean-provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	source := parser.SourceRef{
		Provider:       parser.AgentClaude,
		Key:            sourcePath,
		DisplayPath:    sourcePath,
		FingerprintKey: sourcePath + "#source-key",
	}
	makeEngine := func(outcome parser.ParseOutcome, parseErr error) *Engine {
		t.Helper()
		provider := &shadowCallerProvider{
			shadowTestProvider: shadowTestProvider{
				ProviderBase: parser.ProviderBase{
					Def: parser.AgentDef{
						Type:        parser.AgentClaude,
						DisplayName: "Claude Code",
					},
				},
				fingerprint: parser.SourceFingerprint{
					Key:     sourcePath + "#fingerprint",
					Size:    info.Size(),
					MTimeNS: info.ModTime().UnixNano(),
				},
				outcome:  outcome,
				parseErr: parseErr,
			},
			source: source,
		}
		return NewEngine(dbtest.OpenTestDB(t), EngineConfig{
			AgentDirs: map[parser.AgentType][]string{parser.AgentClaude: {root}},
			Machine:   "devbox",
			ProviderFactories: []parser.ProviderFactory{
				shadowCallerFactory{provider: provider},
			},
			ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
				parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
			},
		})
	}

	tests := []struct {
		name     string
		outcome  parser.ParseOutcome
		parseErr error
		wantErr  bool
	}{
		{
			name:    "whole source parse error",
			wantErr: true,
			parseErr: errors.New(
				"provider source failed",
			),
		},
		{
			name: "incomplete empty result set",
			outcome: parser.ParseOutcome{
				ResultSetComplete: false,
			},
		},
		{
			name: "source error",
			outcome: parser.ParseOutcome{
				ResultSetComplete: true,
				SourceErrors: []parser.SourceError{{
					SourceKey: sourcePath,
					Err:       errors.New("session failed"),
				}},
			},
		},
		{
			name: "retry result",
			outcome: parser.ParseOutcome{
				ResultSetComplete: true,
				Results: []parser.ParseResultOutcome{{
					Result: parser.ParseResult{Session: parser.ParsedSession{
						ID: "provider-retry", Agent: parser.AgentClaude,
					}},
					DataVersion: parser.DataVersionNeedsRetry,
				}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := makeEngine(tt.outcome, tt.parseErr)

			result := engine.processFile(context.Background(), parser.DiscoveredFile{
				Path:  sourcePath,
				Agent: parser.AgentClaude,
			})

			if tt.wantErr {
				require.Error(t, result.err)
			} else {
				require.NoError(t, result.err)
			}
			assert.True(t, result.cacheSkip)
			assert.True(t, result.noCacheSkip)

			stats := engine.collectAndBatch(
				context.Background(),
				singleSyncJob(syncJob{processResult: result, path: sourcePath}),
				1,
				1,
				nil,
				syncWriteDefault,
			)
			if tt.wantErr {
				assert.Equal(t, 1, stats.Failed)
			}
			cache := engine.SnapshotSkipCache()
			assert.NotContains(t, cache, sourcePath+"#source-key")
			assert.NotContains(t, cache, sourcePath)
		})
	}
}

func TestSyncSingleSessionProviderAuthoritativeBypassesProviderSkipCache(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "single-provider-owned.jsonl")
	require.NoError(t, os.WriteFile(sourcePath, []byte("{}\n"), 0o644))
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)

	sourceKey := sourcePath + "#source-key"
	providerResult := parser.ParseResult{
		Session: parser.ParsedSession{
			ID:           "provider-owned",
			Project:      "provider-project",
			Agent:        parser.AgentClaude,
			Machine:      "devbox",
			StartedAt:    info.ModTime(),
			EndedAt:      info.ModTime(),
			MessageCount: 1,
			File: parser.FileInfo{
				Path:  sourcePath,
				Mtime: info.ModTime().UnixNano(),
			},
		},
		Messages: []parser.ParsedMessage{{
			Role:      parser.RoleUser,
			Content:   "explicit provider resync",
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
				Key:     sourcePath + "#fingerprint",
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
		source: parser.SourceRef{
			Provider:       parser.AgentClaude,
			Key:            sourcePath,
			DisplayPath:    sourcePath,
			FingerprintKey: sourceKey,
			ProjectHint:    "provider-project",
		},
	}
	database := dbtest.OpenTestDB(t)
	filePath := sourcePath
	fileSize := info.Size()
	fileMtime := info.ModTime().UnixNano()
	require.NoError(t, database.UpsertSession(db.Session{
		ID:        "provider-owned",
		Project:   "old-project",
		Machine:   "devbox",
		Agent:     string(parser.AgentClaude),
		FilePath:  &filePath,
		FileSize:  &fileSize,
		FileMtime: &fileMtime,
	}))
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{parser.AgentClaude: {root}},
		Machine:   "devbox",
		ProviderFactories: []parser.ProviderFactory{
			shadowCallerFactory{provider: provider},
		},
		ProviderMigrationModes: map[parser.AgentType]parser.ProviderMigrationMode{
			parser.AgentClaude: parser.ProviderMigrationProviderAuthoritative,
		},
	})
	engine.InjectSkipCache(map[string]int64{
		sourceKey: info.ModTime().UnixNano(),
	})

	require.NoError(t, engine.SyncSingleSession("provider-owned"))

	assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
	assert.True(t, provider.parseRequest.ForceParse)
	cache := engine.SnapshotSkipCache()
	assert.NotContains(t, cache, sourceKey)
}

func singleSyncJob(job syncJob) <-chan syncJob {
	results := make(chan syncJob, 1)
	results <- job
	close(results)
	return results
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
			FingerprintKey: sourcePath + "#source-key",
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

func TestProcessFileProviderAuthoritativeTranslatesSkipReason(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "skip-provider-owned.jsonl")
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
			outcome: parser.ParseOutcome{
				ResultSetComplete: true,
				SkipReason:        parser.SkipNoSession,
			},
		},
		source: parser.SourceRef{
			Provider:       parser.AgentClaude,
			Key:            sourcePath,
			DisplayPath:    sourcePath,
			FingerprintKey: sourcePath + "#source-key",
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

	result := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  sourcePath,
		Agent: parser.AgentClaude,
	})

	require.NoError(t, result.err)
	assert.True(t, result.skip)
	assert.True(t, result.cacheSkip)
	assert.Equal(t, sourcePath+"#source-key", result.cacheKey)
	assert.Equal(t, info.ModTime().UnixNano(), result.mtime)
	assert.Empty(t, result.results)

	results := make(chan syncJob, 1)
	results <- syncJob{
		processResult: result,
		path:          sourcePath,
	}
	close(results)
	stats := engine.collectAndBatch(context.Background(), results, 1, 1, nil, syncWriteDefault)

	assert.Equal(t, 1, stats.Skipped)
	cache := engine.SnapshotSkipCache()
	assert.Equal(t, info.ModTime().UnixNano(), cache[sourcePath+"#source-key"])
	_, cachedByPath := cache[sourcePath]
	assert.False(t, cachedByPath)
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

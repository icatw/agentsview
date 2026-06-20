package sync

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesDBBackedLegacyParsers(t *testing.T) {
	for _, tc := range []struct {
		name          string
		agent         parser.AgentType
		rawSessionID  string
		writeDB       func(*testing.T, string) string
		legacyResults func(*testing.T, string) []parser.ParseResult
	}{
		{
			name:         "forge",
			agent:        parser.AgentForge,
			rawSessionID: "forge-provider-process",
			writeDB: func(t *testing.T, root string) string {
				dbPath := filepath.Join(root, ".forge.db")
				forgeDB := openProcessProviderForgeDB(t, dbPath)
				seedProcessProviderForgeConversation(t, forgeDB)
				return dbPath
			},
			legacyResults: func(t *testing.T, dbPath string) []parser.ParseResult {
				sess, msgs, err := parser.ParseForgeSession(
					dbPath, "forge-provider-process", "devbox",
				)
				require.NoError(t, err)
				require.NotNil(t, sess)
				return []parser.ParseResult{{Session: *sess, Messages: msgs}}
			},
		},
		{
			name:         "piebald",
			agent:        parser.AgentPiebald,
			rawSessionID: "42",
			writeDB: func(t *testing.T, root string) string {
				dbPath := filepath.Join(root, "app.db")
				piebaldDB := openProcessProviderPiebaldDB(t, dbPath)
				seedProcessProviderPiebaldChat(t, piebaldDB)
				return dbPath
			},
			legacyResults: func(t *testing.T, dbPath string) []parser.ParseResult {
				results, err := parser.ParsePiebaldSessionResults(
					dbPath, "42", "devbox",
				)
				require.NoError(t, err)
				require.NotEmpty(t, results)
				return results
			},
		},
		{
			name:         "warp",
			agent:        parser.AgentWarp,
			rawSessionID: "conv-001",
			writeDB: func(t *testing.T, root string) string {
				dbPath := filepath.Join(root, "warp.sqlite")
				warpDB := openProcessProviderWarpDB(t, dbPath)
				seedProcessProviderWarpConversation(t, warpDB)
				return dbPath
			},
			legacyResults: func(t *testing.T, dbPath string) []parser.ParseResult {
				sess, msgs, err := parser.ParseWarpSession(
					dbPath, "conv-001", "devbox",
				)
				require.NoError(t, err)
				require.NotNil(t, sess)
				return []parser.ParseResult{{Session: *sess, Messages: msgs}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dbPath := tc.writeDB(t, root)

			provider, ok := parser.NewProvider(tc.agent, parser.ProviderConfig{
				Roots:   []string{root},
				Machine: "devbox",
			})
			require.True(t, ok)
			source, ok, err := provider.FindSource(context.Background(), parser.FindSourceRequest{
				RawSessionID: tc.rawSessionID,
			})
			require.NoError(t, err)
			require.True(t, ok)

			legacyResults := tc.legacyResults(t, dbPath)
			observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
				Source:  source,
				Machine: "devbox",
			})
			require.NoError(t, err)
			require.Len(t, observation.Results, len(legacyResults))
			for i := range legacyResults {
				legacyResults[i].Session.File.Hash = observation.Fingerprint.Hash
			}

			assert.True(t, observation.ForceReplace)
			assert.Equal(t, legacyResults, observation.Results)
			assert.Equal(t, parseResultSessionIDs(legacyResults), observation.Planned.DataVersionSessionIDs())
			assert.Empty(t, observation.Planned.Diagnostics)
		})
	}
}

func parseResultSessionIDs(results []parser.ParseResult) []string {
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.Session.ID)
	}
	return ids
}

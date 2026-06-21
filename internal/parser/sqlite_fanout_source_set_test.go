package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteFanoutSourceSetDiscoversWatchesFindsAndFingerprints(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "state.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("sqlite"), 0o644))
	metas := map[string][]SQLiteFanoutSessionMeta{
		dbPath: {
			{
				SessionID:   "session-b",
				VirtualPath: VirtualSourcePath(dbPath, "session-b"),
				FileMtime:   20,
			},
			{
				SessionID:   "session-a",
				VirtualPath: VirtualSourcePath(dbPath, "session-a"),
				FileMtime:   10,
			},
		},
	}
	roots := []string{root}
	sources := NewSQLiteFanoutSourceSet(
		AgentForge,
		roots,
		SQLiteFanoutSourceSetOptions{
			DBName: "state.db",
			FindDB: func(root string) string {
				path := filepath.Join(root, "state.db")
				if IsRegularFile(path) {
					return path
				}
				return ""
			},
			ListMeta: func(dbPath string) ([]SQLiteFanoutSessionMeta, error) {
				return append([]SQLiteFanoutSessionMeta(nil), metas[dbPath]...), nil
			},
		},
	)
	roots[0] = filepath.Join(root, "mutated")

	discovered, err := sources.Discover(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{
		VirtualSourcePath(dbPath, "session-a"),
		VirtualSourcePath(dbPath, "session-b"),
	}, sourceDisplayPaths(discovered))

	plan, err := sources.WatchPlan(context.Background())
	require.NoError(t, err)
	require.Len(t, plan.Roots, 1)
	assert.Equal(t, root, plan.Roots[0].Path)
	assert.False(t, plan.Roots[0].Recursive)
	assert.ElementsMatch(t, []string{"state.db", "state.db-*"}, plan.Roots[0].IncludeGlobs)

	storedStale := VirtualSourcePath(dbPath, "session-stale")
	changed, err := sources.SourcesForChangedPath(
		context.Background(),
		ChangedPathRequest{
			Path:              dbPath + "-wal",
			EventKind:         "write",
			WatchRoot:         root,
			StoredSourcePaths: []string{storedStale},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		VirtualSourcePath(dbPath, "session-a"),
		VirtualSourcePath(dbPath, "session-b"),
		storedStale,
	}, sourceDisplayPaths(changed))

	found, ok, err := sources.FindSource(
		context.Background(),
		FindSourceRequest{
			RawSessionID:       "session-a",
			StoredFilePath:     VirtualSourcePath(dbPath, "session-a"),
			RequireFreshSource: true,
		},
	)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, VirtualSourcePath(dbPath, "session-a"), found.DisplayPath)

	fingerprint, err := sources.Fingerprint(context.Background(), found)
	require.NoError(t, err)
	assert.Equal(t, VirtualSourcePath(dbPath, "session-a"), fingerprint.Key)
	assert.Equal(t, int64(10), fingerprint.MTimeNS)

	delete(metas, dbPath)
	_, ok, err = sources.FindSource(
		context.Background(),
		FindSourceRequest{
			StoredFilePath:     VirtualSourcePath(dbPath, "session-a"),
			RequireFreshSource: true,
		},
	)
	require.NoError(t, err)
	assert.False(t, ok, "fresh lookup rejects deleted rows")

	staleSource, ok, err := sources.FindSource(
		context.Background(),
		FindSourceRequest{StoredFilePath: VirtualSourcePath(dbPath, "session-a")},
	)
	require.NoError(t, err)
	require.True(t, ok, "non-fresh lookup preserves tombstone source identity")
	assert.Equal(t, VirtualSourcePath(dbPath, "session-a"), staleSource.DisplayPath)
}

func TestSQLiteFanoutSourceSetRejectsInvalidVirtualPaths(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "state.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("sqlite"), 0o644))
	sources := NewSQLiteFanoutSourceSet(
		AgentForge,
		[]string{root},
		SQLiteFanoutSourceSetOptions{
			DBName: "state.db",
			FindDB: func(root string) string {
				return filepath.Join(root, "state.db")
			},
			ListMeta: func(dbPath string) ([]SQLiteFanoutSessionMeta, error) {
				return nil, nil
			},
		},
	)

	for _, path := range []string{
		dbPath,
		VirtualSourcePath(filepath.Join(root, "other.db"), "session-a"),
		filepath.Join(root, "nested", "state.db") + "#session-a",
		VirtualSourcePath(dbPath, ""),
	} {
		t.Run(path, func(t *testing.T) {
			_, ok, err := sources.FindSource(
				context.Background(),
				FindSourceRequest{StoredFilePath: path},
			)

			require.NoError(t, err)
			assert.False(t, ok)
		})
	}
}

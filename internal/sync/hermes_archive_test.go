package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/dbtest"
	"go.kenn.io/agentsview/internal/parser"
)

func TestHermesArchiveEffectiveInfoIncludesDirectTranscripts(t *testing.T) {
	root := t.TempDir()
	stateDB := writeHermesArchiveStateDB(t, root)
	transcriptPath := filepath.Join(root, "sessions", "extra.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcriptPath), 0o755))
	require.NoError(t, os.WriteFile(transcriptPath, []byte("{}\n{}\n"), 0o644))

	stateInfo, err := os.Stat(stateDB)
	require.NoError(t, err)
	transcriptInfo, err := os.Stat(transcriptPath)
	require.NoError(t, err)
	transcriptTime := time.Now().Add(2 * time.Second).Truncate(time.Second)
	require.NoError(t, os.Chtimes(transcriptPath, transcriptTime, transcriptTime))

	got := hermesArchiveEffectiveInfo(stateDB, stateInfo)

	assert.Equal(t, stateInfo.Size()+transcriptInfo.Size(), got.Size())
	assert.Equal(t, transcriptTime.UnixNano(), got.ModTime().UnixNano())
}

func TestHermesArchiveTranscriptFilesUsesDirectSessionFiles(t *testing.T) {
	sessionsDir := t.TempDir()
	writeFile := func(name string) {
		t.Helper()
		path := filepath.Join(sessionsDir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("{}\n"), 0o644))
	}
	writeFile("extra.jsonl")
	writeFile("session_child.json")
	writeFile("child.json")
	writeFile("session_child.txt")
	writeFile(filepath.Join("nested", "session_nested.json"))

	got := hermesArchiveTranscriptFiles(sessionsDir)

	assert.Equal(t, []string{
		filepath.Join(sessionsDir, "extra.jsonl"),
		filepath.Join(sessionsDir, "session_child.json"),
	}, got)
}

func TestHermesArchiveEffectiveInfoChangesWhenTranscriptRemoved(t *testing.T) {
	root := t.TempDir()
	stateDB := writeHermesArchiveStateDB(t, root)
	transcriptPath := filepath.Join(root, "sessions", "extra.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcriptPath), 0o755))
	require.NoError(t, os.WriteFile(transcriptPath, []byte("{}\n{}\n"), 0o644))

	stateInfo, err := os.Stat(stateDB)
	require.NoError(t, err)
	before := hermesArchiveEffectiveInfo(stateDB, stateInfo)

	require.NoError(t, os.Remove(transcriptPath))
	after := hermesArchiveEffectiveInfo(stateDB, stateInfo)

	assert.NotEqual(t, before.Size(), after.Size())
	assert.Equal(t, stateInfo.Size(), after.Size())
}

func TestProcessFileHermesArchiveSkipCacheUsesAggregateMtime(t *testing.T) {
	root := t.TempDir()
	stateDB := writeHermesArchiveStateDB(t, root)
	transcriptPath := filepath.Join(root, "sessions", "extra.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcriptPath), 0o755))
	require.NoError(t, os.WriteFile(transcriptPath, []byte("{}\n"), 0o644))
	transcriptTime := time.Now().Add(2 * time.Second).Truncate(time.Second)
	require.NoError(t, os.Chtimes(transcriptPath, transcriptTime, transcriptTime))

	engine := NewEngine(dbtest.OpenTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentHermes: {filepath.Join(root, "sessions")},
		},
		Machine: "local",
	})
	engine.InjectSkipCache(map[string]int64{
		stateDB: transcriptTime.UnixNano(),
	})

	res := engine.processFile(context.Background(), parser.DiscoveredFile{
		Path:  stateDB,
		Agent: parser.AgentHermes,
	})

	require.NoError(t, res.err)
	assert.True(t, res.skip)
	assert.True(t, res.cacheSkip)
	assert.Equal(t, transcriptTime.UnixNano(), res.mtime)
}

func TestProcessHermesArchivePersistsAggregateFingerprint(t *testing.T) {
	root := t.TempDir()
	stateDB := writeHermesArchiveStateDB(t, root)
	transcriptPath := filepath.Join(root, "sessions", "extra.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcriptPath), 0o755))
	require.NoError(t, os.WriteFile(
		transcriptPath,
		[]byte(
			`{"role":"session_meta","platform":"cli","timestamp":"2026-05-14T10:00:00.000000"}`+"\n"+
				`{"role":"user","content":"new transcript","timestamp":"2026-05-14T10:01:00.000000"}`+"\n",
		),
		0o644,
	))

	stateInfo, err := os.Stat(stateDB)
	require.NoError(t, err)
	effectiveInfo := hermesArchiveEffectiveInfo(stateDB, stateInfo)
	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentHermes: {filepath.Join(root, "sessions")},
		},
		Machine: "local",
	})

	res := engine.processHermes(parser.DiscoveredFile{
		Path:  stateDB,
		Agent: parser.AgentHermes,
	}, stateInfo)

	require.NoError(t, res.err)
	require.NotEmpty(t, res.results)
	for _, result := range res.results {
		assert.Equal(t, stateDB, result.Session.File.Path)
		assert.Equal(t, effectiveInfo.Size(), result.Session.File.Size)
		assert.Equal(t, effectiveInfo.ModTime().UnixNano(), result.Session.File.Mtime)
	}

	pending := make([]pendingWrite, 0, len(res.results))
	for _, result := range res.results {
		pending = append(pending, pendingWrite{
			sess:        result.Session,
			msgs:        result.Messages,
			usageEvents: result.UsageEvents,
		})
	}
	written, _, failed := engine.writeBatch(pending, syncWriteDefault, true)
	require.Equal(t, 0, failed)
	require.NotZero(t, written)

	storedSize, storedMtime, ok := database.GetFileInfoByPath(stateDB)
	require.True(t, ok)
	assert.Equal(t, effectiveInfo.Size(), storedSize)
	assert.Equal(t, effectiveInfo.ModTime().UnixNano(), storedMtime)

	second := engine.processHermes(parser.DiscoveredFile{
		Path:  stateDB,
		Agent: parser.AgentHermes,
	}, stateInfo)
	require.NoError(t, second.err)
	assert.True(t, second.skip)
}

func TestSyncSingleHermesArchivePersistsAggregateFingerprint(t *testing.T) {
	root := t.TempDir()
	stateDB := writeHermesArchiveStateDB(t, root)
	transcriptPath := filepath.Join(root, "sessions", "extra.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcriptPath), 0o755))
	require.NoError(t, os.WriteFile(
		transcriptPath,
		[]byte(
			`{"role":"session_meta","platform":"cli","timestamp":"2026-05-14T10:00:00.000000"}`+"\n"+
				`{"role":"user","content":"new transcript","timestamp":"2026-05-14T10:01:00.000000"}`+"\n",
		),
		0o644,
	))

	stateInfo, err := os.Stat(stateDB)
	require.NoError(t, err)
	effectiveInfo := hermesArchiveEffectiveInfo(stateDB, stateInfo)
	database := dbtest.OpenTestDB(t)
	engine := NewEngine(database, EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentHermes: {filepath.Join(root, "sessions")},
		},
		Machine: "local",
	})

	ok, err := engine.syncSingleHermesArchive("hermes:child", transcriptPath, "")
	require.NoError(t, err)
	require.True(t, ok)

	storedSize, storedMtime, found := database.GetFileInfoByPath(stateDB)
	require.True(t, found)
	assert.Equal(t, effectiveInfo.Size(), storedSize)
	assert.Equal(t, effectiveInfo.ModTime().UnixNano(), storedMtime)
}

func writeHermesArchiveStateDB(t *testing.T, root string) string {
	t.Helper()
	stateDB := filepath.Join(root, "state.db")
	conn, err := sql.Open("sqlite3", stateDB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = conn.Exec(`
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			user_id TEXT,
			model TEXT,
			model_config TEXT,
			system_prompt TEXT,
			parent_session_id TEXT,
			started_at REAL NOT NULL,
			ended_at REAL,
			end_reason TEXT,
			message_count INTEGER DEFAULT 0,
			tool_call_count INTEGER DEFAULT 0,
			input_tokens INTEGER DEFAULT 0,
			output_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0,
			reasoning_tokens INTEGER DEFAULT 0,
			billing_provider TEXT,
			billing_base_url TEXT,
			billing_mode TEXT,
			estimated_cost_usd REAL,
			actual_cost_usd REAL,
			cost_status TEXT,
			cost_source TEXT,
			pricing_version TEXT,
			title TEXT,
			api_call_count INTEGER DEFAULT 0
		);
		CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT,
			tool_call_id TEXT,
			tool_calls TEXT,
			tool_name TEXT,
			timestamp REAL NOT NULL,
			token_count INTEGER,
			finish_reason TEXT,
			reasoning TEXT,
			reasoning_content TEXT,
			reasoning_details TEXT,
			codex_reasoning_items TEXT,
			codex_message_items TEXT
		);
		INSERT INTO sessions (
			id, source, model, started_at, ended_at, message_count
		) VALUES (
			'child', 'discord', 'gpt-5.4', 1778767200.0, 1778767800.0, 1
		);
		INSERT INTO messages (
			session_id, role, content, timestamp
		) VALUES (
			'child', 'user', 'state db message', 1778767210.0
		);
	`)
	require.NoError(t, err)
	return stateDB
}

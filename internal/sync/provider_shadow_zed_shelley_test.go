package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesZedLegacyParser(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, parser.ZedThreadsDBRelPath)
	threadID := "10431c84-c47b-4e6c-b2df-f9f3b9ad025b"
	writeZedShadowDB(t, dbPath, threadID)

	provider, ok := parser.NewProvider(parser.AgentZed, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacyResults, err := parser.ParseZedSessions(dbPath, "devbox")
	require.NoError(t, err)
	require.Len(t, legacyResults, 1)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	legacyResults[0].Session.File.Hash = observation.Fingerprint.Hash

	assert.True(t, observation.ForceReplace)
	assert.Equal(t, legacyResults[0].Session, observation.Results[0].Session)
	assert.Equal(t, legacyResults[0].Messages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacyResults[0].Session.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func TestObserveProviderSourceMatchesShelleyLegacyParser(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "shelley.db")
	conversationID := "cMAIN1"
	writeShelleyShadowDB(t, dbPath, conversationID)

	provider, ok := parser.NewProvider(parser.AgentShelley, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	info, err := os.Stat(dbPath)
	require.NoError(t, err)
	legacyResult, err := parser.ParseShelleyConversationDirect(
		dbPath, conversationID, "devbox", info,
	)
	require.NoError(t, err)
	require.NotNil(t, legacyResult)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)

	assert.True(t, observation.ForceReplace)
	assert.Equal(t, legacyResult.Session, observation.Results[0].Session)
	assert.Equal(t, legacyResult.Messages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacyResult.Session.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func writeZedShadowDB(t *testing.T, dbPath, threadID string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0o755))
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		summary TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		data_type TEXT NOT NULL,
		data BLOB NOT NULL,
		parent_id TEXT,
		folder_paths TEXT,
		folder_paths_order TEXT,
		created_at TEXT
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO threads (
		id, summary, updated_at, data_type, data,
		parent_id, folder_paths, created_at
	) VALUES (?, ?, ?, ?, ?, NULL, '', ?)`,
		threadID,
		"Provider Zed thread",
		"2026-06-08T09:14:10Z",
		"json",
		[]byte(`{"messages":[{"User":{"content":[{"Text":"Hello Zed"}]}}]}`),
		"2026-06-08T09:12:41Z",
	)
	require.NoError(t, err)
}

func writeShelleyShadowDB(t *testing.T, dbPath, conversationID string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dbPath), 0o755))
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE conversations (
		conversation_id TEXT PRIMARY KEY,
		slug TEXT,
		user_initiated BOOLEAN NOT NULL DEFAULT TRUE,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		cwd TEXT,
		archived BOOLEAN NOT NULL DEFAULT FALSE,
		parent_conversation_id TEXT,
		model TEXT,
		current_generation INTEGER NOT NULL DEFAULT 1
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE messages (
		message_id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		sequence_id INTEGER NOT NULL,
		type TEXT NOT NULL,
		llm_data TEXT,
		user_data TEXT,
		usage_data TEXT,
		display_data TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		generation INTEGER NOT NULL DEFAULT 1,
		excluded_from_context BOOLEAN NOT NULL DEFAULT FALSE
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO conversations (
		conversation_id, slug, user_initiated, created_at, updated_at, cwd, model
	) VALUES (?, ?, TRUE, ?, ?, ?, ?)`,
		conversationID,
		"provider-shelley",
		"2026-06-15T10:00:00Z",
		"2026-06-15T10:00:06Z",
		"/home/u/dev/app",
		"claude-sonnet-4-6",
	)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO messages (
		message_id, conversation_id, sequence_id, type, llm_data, usage_data, created_at
	) VALUES (?, ?, 1, 'user', ?, NULL, ?)`,
		conversationID+"-m1",
		conversationID,
		`{"Role":0,"Content":[{"Type":2,"Text":"Add a Shelley parser"}]}`,
		"2026-06-15T10:00:00Z",
	)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO messages (
		message_id, conversation_id, sequence_id, type, llm_data, usage_data, created_at
	) VALUES (?, ?, 2, 'agent', ?, ?, ?)`,
		conversationID+"-m2",
		conversationID,
		`{"Role":1,"Content":[{"Type":2,"Text":"Done."}]}`,
		`{"input_tokens":500,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":50,"model":"claude-sonnet-4-6"}`,
		"2026-06-15T10:00:05Z",
	)
	require.NoError(t, err)
}

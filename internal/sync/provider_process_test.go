package sync

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestProcessFileProviderShadowCompareForgeVirtualSource(t *testing.T) {
	root := t.TempDir()
	dbPath := writeProcessProviderForgeDB(t, root)
	engine := NewEngine(openTestDB(t), EngineConfig{
		AgentDirs: map[parser.AgentType][]string{
			parser.AgentForge: {root},
		},
		Machine: "devbox",
	})

	files := engine.classifyProviderChangedPath(dbPath)
	require.Len(t, files, 1)
	assert.Equal(t, dbPath+"#conv-001", files[0].Path)
	assert.Equal(t, parser.AgentForge, files[0].Agent)

	res := engine.processFile(context.Background(), files[0])

	require.NoError(t, res.err)
	require.Len(t, res.results, 1)
	assert.True(t, res.forceReplace)
	assert.NotZero(t, res.mtime)
	assert.Equal(t, "forge:conv-001", res.results[0].Session.ID)
	assert.Equal(t, parser.AgentForge, res.results[0].Session.Agent)
	assert.Equal(t, "devbox", res.results[0].Session.Machine)
	assert.Len(t, res.results[0].Messages, 2)
}

func TestProcessFileUsesProviderDBBackedFamily(t *testing.T) {
	for _, agent := range []parser.AgentType{
		parser.AgentForge,
		parser.AgentPiebald,
		parser.AgentWarp,
	} {
		assert.True(t, processFileUsesProvider(agent), agent)
	}
	assert.False(t, processFileUsesProvider(parser.AgentClaude))
}

func writeProcessProviderForgeDB(t *testing.T, root string) string {
	t.Helper()
	dbPath := filepath.Join(root, ".forge.db")
	database, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	_, err = database.Exec(`
		CREATE TABLE conversations (
			conversation_id TEXT PRIMARY KEY NOT NULL,
			title TEXT,
			workspace_id BIGINT NOT NULL,
			context TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP,
			metrics TEXT
		);
	`)
	require.NoError(t, err)
	_, err = database.Exec(
		`INSERT INTO conversations
			(conversation_id, title, workspace_id, context, created_at, updated_at, metrics)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"conv-001",
		"Provider Process",
		int64(1),
		`{"conversation_id":"conv-001","messages":[`+
			`{"message":{"text":{"role":"User","content":"Run provider process.","raw_content":{"Text":"Run provider process."},"timestamp":"2026-05-02T09:58:16Z"}}},`+
			`{"message":{"text":{"role":"Assistant","content":"Processed through provider.","timestamp":"2026-05-02T09:58:17Z"}}}`+
			`]}`,
		"2026-05-02 09:58:16.000000000",
		"2026-05-02 09:58:17.000000000",
		"",
	)
	require.NoError(t, err)
	return dbPath
}

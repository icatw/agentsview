package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.kenn.io/agentsview/internal/parser"
)

func TestObserveProviderSourceMatchesPositronLegacyParser(t *testing.T) {
	root := t.TempDir()
	sessionID := "positron-shadow"
	hashDir := filepath.Join(root, "workspaceStorage", "workspace-hash")
	chatDir := filepath.Join(hashDir, "chatSessions")
	sourcePath := filepath.Join(chatDir, sessionID+".jsonl")
	workspacePath := filepath.Join(hashDir, "workspace.json")
	writeProviderShadowSourceFile(
		t,
		workspacePath,
		`{"folder":"file:///Users/alice/code/positron-app"}`,
	)
	writeProviderShadowSourceFile(t, sourcePath, vscodeCopilotShadowFixture(sessionID))

	provider, ok := parser.NewProvider(parser.AgentPositron, parser.ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)
	sources, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, sources, 1)

	legacySession, legacyMessages, err := parser.ParsePositronSession(
		sourcePath, "positron-app", "devbox",
	)
	require.NoError(t, err)
	require.NotNil(t, legacySession)

	observation, err := ObserveProviderSource(context.Background(), provider, ProviderObserveRequest{
		Source:  sources[0],
		Machine: "devbox",
	})
	require.NoError(t, err)
	require.Len(t, observation.Results, 1)
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	effectiveInfo := positronEffectiveInfo(sourcePath, info)
	legacySession.File.Size = effectiveInfo.Size()
	legacySession.File.Mtime = effectiveInfo.ModTime().UnixNano()
	expectedHash := positronShadowCompositeHash(t, sourcePath, workspacePath)
	assert.Equal(t, expectedHash, observation.Fingerprint.Hash)
	legacySession.File.Hash = expectedHash

	assert.Equal(t, *legacySession, observation.Results[0].Session)
	assert.Equal(t, legacyMessages, observation.Results[0].Messages)
	assert.Equal(t, []string{legacySession.ID}, observation.Planned.DataVersionSessionIDs())
	assert.Empty(t, observation.Planned.Diagnostics)
}

func positronShadowCompositeHash(t *testing.T, sourcePath, workspacePath string) string {
	t.Helper()
	chatHash, err := ComputeFileHash(sourcePath)
	require.NoError(t, err)
	workspaceHash, err := ComputeFileHash(workspacePath)
	require.NoError(t, err)
	hash, err := ComputeHash(strings.NewReader(
		"chat\x00" + chatHash + "\x00workspace\x00" + workspaceHash,
	))
	require.NoError(t, err)
	return hash
}

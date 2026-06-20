package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.kenn.io/agentsview/internal/testjsonl"
)

func TestCodexProviderFactoryReplacesLegacyAdapter(t *testing.T) {
	factory, ok := ProviderFactoryByType(AgentCodex)
	require.True(t, ok)
	_, legacyFactory := factory.(legacyProviderFactory)
	assert.False(t, legacyFactory)

	provider, ok := NewProvider(AgentCodex, ProviderConfig{
		Roots:   []string{t.TempDir()},
		Machine: "devbox",
	})
	require.True(t, ok)
	_, legacyProvider := provider.(*legacyProvider)
	assert.False(t, legacyProvider)
}

func TestCodexProviderSourceMethods(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e1"
	sourcePath := writeCodexProviderSession(t, root, uuid, "Rename me")
	indexPath := filepath.Join(base, CodexSessionIndexFilename)
	require.NoError(t, os.WriteFile(indexPath, []byte(
		`{"id":"`+uuid+`","thread_name":"Renamed title","updated_at":"2026-06-11T17:34:20Z"}`+"\n",
	), 0o644))
	newer := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(indexPath, newer, newer))

	provider, ok := NewProvider(AgentCodex, ProviderConfig{
		Roots:   []string{root},
		Machine: "devbox",
	})
	require.True(t, ok)

	plan, err := provider.WatchPlan(context.Background())
	require.NoError(t, err)
	require.Len(t, plan.Roots, 2)
	assert.Equal(t, root, plan.Roots[0].Path)
	assert.True(t, plan.Roots[0].Recursive)
	assert.Equal(t, []string{"*.jsonl"}, plan.Roots[0].IncludeGlobs)
	assert.Equal(t, base, plan.Roots[1].Path)
	assert.False(t, plan.Roots[1].Recursive)
	assert.Equal(t, []string{CodexSessionIndexFilename}, plan.Roots[1].IncludeGlobs)

	discovered, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	source := discovered[0]
	assert.Equal(t, AgentCodex, source.Provider)
	assert.Equal(t, sourcePath, source.DisplayPath)
	assert.Equal(t, sourcePath, source.FingerprintKey)

	found, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		FullSessionID: "host~codex:" + uuid,
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, sourcePath, found.DisplayPath)

	for _, path := range []string{sourcePath, indexPath} {
		changed, err := provider.SourcesForChangedPath(
			context.Background(),
			ChangedPathRequest{Path: path, EventKind: "write"},
		)
		require.NoError(t, err)
		require.Len(t, changed, 1)
		assert.Equal(t, sourcePath, changed[0].DisplayPath)
	}

	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	fingerprint, err := provider.Fingerprint(context.Background(), found)
	require.NoError(t, err)
	assert.Equal(t, sourcePath, fingerprint.Key)
	assert.Equal(t, info.Size(), fingerprint.Size)
	assert.Equal(t, newer.UnixNano(), fingerprint.MTimeNS)
	assert.NotEmpty(t, fingerprint.Hash)

	outcome, err := provider.Parse(context.Background(), ParseRequest{
		Source:      found,
		Fingerprint: fingerprint,
	})
	require.NoError(t, err)
	require.True(t, outcome.ResultSetComplete)
	require.Len(t, outcome.Results, 1)
	result := outcome.Results[0]
	assert.Equal(t, DataVersionCurrent, result.DataVersion)
	assert.Equal(t, "codex:"+uuid, result.Result.Session.ID)
	assert.Equal(t, AgentCodex, result.Result.Session.Agent)
	assert.Equal(t, "devbox", result.Result.Session.Machine)
	assert.Equal(t, "api", result.Result.Session.Project)
	assert.Equal(t, "Renamed title", result.Result.Session.SessionName)
	assert.Equal(t, fingerprint.Hash, result.Result.Session.File.Hash)
	assert.Len(t, result.Result.Messages, 1)
}

func TestCodexProviderParseIncremental(t *testing.T) {
	root := t.TempDir()
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e2"
	initial := testjsonl.JoinJSONL(
		testjsonl.CodexSessionMetaJSON(uuid, "/tmp/api", "codex_cli_rs", tsEarly),
		testjsonl.CodexMsgJSON("user", "hello", tsEarlyS1),
	)
	sourcePath := writeCodexProviderSessionContent(t, root, uuid, initial)
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	offset := info.Size()

	appendFile(t, sourcePath, testjsonl.JoinJSONL(
		testjsonl.CodexMsgJSON("assistant", "world", tsEarlyS5),
	))
	currentInfo, err := os.Stat(sourcePath)
	require.NoError(t, err)

	provider, ok := NewProvider(AgentCodex, ProviderConfig{
		Roots: []string{root},
	})
	require.True(t, ok)
	source, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		FullSessionID: "codex:" + uuid,
	})
	require.NoError(t, err)
	require.True(t, ok)

	outcome, status, err := provider.ParseIncremental(
		context.Background(),
		IncrementalRequest{
			Source:       source,
			Fingerprint:  SourceFingerprint{Size: currentInfo.Size()},
			SessionID:    "codex:" + uuid,
			Offset:       offset,
			StartOrdinal: 1,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, IncrementalApplied, status)
	assert.Equal(t, "codex:"+uuid, outcome.SessionID)
	assert.Equal(t, int64(len(testjsonl.JoinJSONL(
		testjsonl.CodexMsgJSON("assistant", "world", tsEarlyS5),
	))), outcome.ConsumedBytes)
	require.Len(t, outcome.Messages, 1)
	assert.Equal(t, RoleAssistant, outcome.Messages[0].Role)
	assert.Equal(t, "world", outcome.Messages[0].Content)
}

func TestCodexProviderParseIncrementalFallback(t *testing.T) {
	root := t.TempDir()
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e3"
	initial := testjsonl.JoinJSONL(
		testjsonl.CodexSessionMetaJSON(uuid, "/tmp/api", "codex_cli_rs", tsEarly),
		testjsonl.CodexTurnContextJSON("gpt-5.5", tsEarlyS1),
		testjsonl.CodexMsgJSON("user", "run command", tsEarlyS1),
		testjsonl.CodexFunctionCallWithCallIDJSON(
			"exec_command", "call_cmd",
			map[string]any{"cmd": "sleep 1"}, tsEarlyS5,
		),
	)
	sourcePath := writeCodexProviderSessionContent(t, root, uuid, initial)
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	offset := info.Size()
	appendFile(t, sourcePath, testjsonl.JoinJSONL(
		testjsonl.CodexFunctionCallOutputJSON("call_cmd", "done", tsLate),
		testjsonl.CodexTokenCountJSON(tsLate, 100_000, 250, 64_000),
	))
	currentInfo, err := os.Stat(sourcePath)
	require.NoError(t, err)

	provider, ok := NewProvider(AgentCodex, ProviderConfig{Roots: []string{root}})
	require.True(t, ok)
	source, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		FullSessionID: "codex:" + uuid,
	})
	require.NoError(t, err)
	require.True(t, ok)

	outcome, status, err := provider.ParseIncremental(
		context.Background(),
		IncrementalRequest{
			Source:       source,
			Fingerprint:  SourceFingerprint{Size: currentInfo.Size()},
			SessionID:    "codex:" + uuid,
			Offset:       offset,
			StartOrdinal: 2,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, IncrementalNeedsFullParse, status)
	assert.True(t, outcome.ForceReplace)
}

func TestCodexProviderParseIncrementalIndexOnlyChangeNeedsFullParse(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e4"
	sourcePath := writeCodexProviderSession(t, root, uuid, "Rename me")
	info, err := os.Stat(sourcePath)
	require.NoError(t, err)
	indexPath := filepath.Join(base, CodexSessionIndexFilename)
	require.NoError(t, os.WriteFile(indexPath, []byte(
		`{"id":"`+uuid+`","thread_name":"Renamed","updated_at":"2026-06-11T17:34:20Z"}`+"\n",
	), 0o644))
	newer := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(indexPath, newer, newer))

	provider, ok := NewProvider(AgentCodex, ProviderConfig{Roots: []string{root}})
	require.True(t, ok)
	source, ok, err := provider.FindSource(context.Background(), FindSourceRequest{
		FullSessionID: "codex:" + uuid,
	})
	require.NoError(t, err)
	require.True(t, ok)
	fingerprint, err := provider.Fingerprint(context.Background(), source)
	require.NoError(t, err)
	require.Greater(t, fingerprint.MTimeNS, info.ModTime().UnixNano())

	outcome, status, err := provider.ParseIncremental(
		context.Background(),
		IncrementalRequest{
			Source:       source,
			Fingerprint:  fingerprint,
			SessionID:    "codex:" + uuid,
			Offset:       info.Size(),
			StartOrdinal: 1,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, IncrementalNeedsFullParse, status)
	assert.True(t, outcome.ForceReplace)
}

func TestCodexProviderDiscoverDedupesLiveAndArchivedByUUID(t *testing.T) {
	base := t.TempDir()
	liveRoot := filepath.Join(base, "sessions")
	archivedRoot := filepath.Join(base, "archived_sessions")
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e5"
	livePath := writeCodexProviderSession(t, liveRoot, uuid, "live")
	archivedPath := writeCodexProviderArchivedSession(
		t, archivedRoot, uuid, "archived",
	)

	provider, ok := NewProvider(AgentCodex, ProviderConfig{
		Roots: []string{archivedRoot, liveRoot},
	})
	require.True(t, ok)
	discovered, err := provider.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	assert.Equal(t, livePath, discovered[0].DisplayPath)
	assert.NotEqual(t, archivedPath, discovered[0].DisplayPath)
}

func TestCodexProviderChangedPathClassifiesRemovedTranscript(t *testing.T) {
	root := t.TempDir()
	uuid := "019eb791-cf7d-75c1-8439-9ed74c1229e6"
	sourcePath := writeCodexProviderSession(t, root, uuid, "remove")
	provider, ok := NewProvider(AgentCodex, ProviderConfig{Roots: []string{root}})
	require.True(t, ok)
	require.NoError(t, os.Remove(sourcePath))

	changed, err := provider.SourcesForChangedPath(
		context.Background(),
		ChangedPathRequest{Path: sourcePath, EventKind: "remove"},
	)
	require.NoError(t, err)
	require.Len(t, changed, 1)
	assert.Equal(t, sourcePath, changed[0].DisplayPath)
}

func TestCodexProviderIndexPathClassifiesAllSiblingSources(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "sessions")
	firstUUID := "019eb791-cf7d-75c1-8439-9ed74c1229e7"
	secondUUID := "019eb791-cf7d-75c1-8439-9ed74c1229e8"
	firstPath := writeCodexProviderSession(t, root, firstUUID, "first")
	secondPath := writeCodexProviderSession(t, root, secondUUID, "second")
	indexPath := filepath.Join(base, CodexSessionIndexFilename)
	require.NoError(t, os.WriteFile(indexPath, []byte(
		`{"id":"`+firstUUID+`","thread_name":"Only first remains","updated_at":"2026-06-11T17:34:20Z"}`+"\n",
	), 0o644))

	provider, ok := NewProvider(AgentCodex, ProviderConfig{Roots: []string{root}})
	require.True(t, ok)
	changed, err := provider.SourcesForChangedPath(
		context.Background(),
		ChangedPathRequest{Path: indexPath, EventKind: "write"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{firstPath, secondPath}, sourceDisplayPaths(changed))
}

func writeCodexProviderSession(
	t *testing.T,
	root, uuid, prompt string,
) string {
	t.Helper()
	content := testjsonl.JoinJSONL(
		testjsonl.CodexSessionMetaJSON(uuid, "/home/user/code/api", "codex_cli_rs", tsEarly),
		testjsonl.CodexMsgJSON("user", prompt, tsEarlyS1),
	)
	return writeCodexProviderSessionContent(t, root, uuid, content)
}

func writeCodexProviderArchivedSession(
	t *testing.T,
	root, uuid, prompt string,
) string {
	t.Helper()
	content := testjsonl.JoinJSONL(
		testjsonl.CodexSessionMetaJSON(uuid, "/home/user/code/archive", "codex_cli_rs", tsEarly),
		testjsonl.CodexMsgJSON("user", prompt, tsEarlyS1),
	)
	path := filepath.Join(root, "rollout-2026-06-11T12-44-06-"+uuid+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func writeCodexProviderSessionContent(
	t *testing.T,
	root, uuid, content string,
) string {
	t.Helper()
	path := filepath.Join(
		root,
		"2026",
		"06",
		"11",
		"rollout-2026-06-11T12-44-06-"+uuid+".jsonl",
	)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

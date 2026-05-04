# Issue 364 Watch Degradation Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development
> (if subagents available) or superpowers:executing-plans to implement this
> plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `agentsview serve` start reliably when session archives contain
thousands of directories by binding the HTTP listener before file watcher setup
and degrading oversized watcher roots to periodic polling.

**Architecture:** Keep fsnotify for roots that fit within a process-wide watcher
budget and degrade the rest to the existing polling path. The watcher owns the
remaining budget so depletion across multiple recursive roots is centralized and
testable. The serve runtime starts before watcher registration, so watcher
resource use can no longer prevent the web UI from binding.

**Tech Stack:** Go 1.26, `fsnotify`, Cobra CLI runtime, existing `internal/sync`
watcher and `cmd/agentsview` serve runtime tests.

______________________________________________________________________

## File Structure

- Modify `internal/sync/watcher.go`

  - Add watcher-owned budget state and a result type for recursive watch setup.
  - Keep `WatchRecursive(root string) (watched, unwatched int, err error)` as a
    compatibility wrapper.
  - Add an unexported test-friendly constructor/helper or option so tests can
    use a tiny budget without changing public API.

- Modify `internal/sync/watcher_test.go`

  - Add deterministic tests for budget exhaustion through a real
    `fsnotify.Watcher`.
  - Keep existing watcher behavior tests passing.

- Modify `cmd/agentsview/main.go`

  - Move watcher startup to after server startup in `runServe`.
  - Update `startFileWatcher` to inspect the richer recursive watch result.
  - Add one stdout summary line when roots are degraded to polling.

- Modify `cmd/agentsview/serve_runtime.go`

  - Add a small internal post-listen hook so production can run watcher setup
    after the local listener is bound, and tests can block that hook while
    checking the listener.

- Modify or add `cmd/agentsview/serve_runtime_test.go`

  - Add a regression test that proves the listener is reachable before a
    blocking watcher-start stub returns.

______________________________________________________________________

### Task 1: Add Watcher-Owned Budget Result

**Files:**

- Modify: `internal/sync/watcher.go`

- Test: `internal/sync/watcher_test.go`

- [ ] **Step 1: Write failing budget exhaustion test**

Add a test that builds a root with more directories than a tiny budget, uses a
real watcher with that tiny budget, and asserts the walk stops early.

```go
func TestWatchRecursiveBudget_DegradesWhenBudgetExhausted(t *testing.T) {
	root := t.TempDir()
	for i := range 5 {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("dir-%d", i)), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	w, err := NewWatcher(time.Second, func(_ []string) {}, nil)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	w.Start()
	t.Cleanup(func() { w.Stop() })
	w.setRecursiveWatchBudgetForTest(3)

	result := w.WatchRecursiveBudgeted(root)
	if result.Watched != 3 {
		t.Fatalf("Watched = %d, want 3", result.Watched)
	}
	if !result.BudgetExhausted {
		t.Fatal("BudgetExhausted = false, want true")
	}
	if result.Unwatched == 0 {
		t.Fatal("Unwatched = 0, want remaining directories counted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test -tags fts5 ./internal/sync -run TestWatchRecursiveBudget_DegradesWhenBudgetExhausted -count=1
```

Expected: FAIL because `setRecursiveWatchBudgetForTest` and
`WatchRecursiveBudgeted` do not exist.

- [ ] **Step 3: Implement budget result and watcher-owned budget**

In `internal/sync/watcher.go`, add:

```go
const defaultRecursiveWatchBudget = 8192

type RecursiveWatchResult struct {
	Watched             int
	Unwatched           int
	Err                 error
	BudgetExhausted     bool
	ResourceExhausted   bool
	ResourceExhaustedAt string
}
```

Extend `Watcher`:

```go
recursiveWatchBudget int
```

Initialize it in `NewWatcher`:

```go
recursiveWatchBudget: defaultRecursiveWatchBudget,
```

Add a test-only unexported helper in `watcher.go`:

```go
func (w *Watcher) setRecursiveWatchBudgetForTest(n int) {
	w.recursiveWatchBudget = n
}
```

Add `WatchRecursiveBudgeted(root string) RecursiveWatchResult` and make
`WatchRecursive` a compatibility wrapper:

```go
func (w *Watcher) WatchRecursive(root string) (watched int, unwatched int, err error) {
	result := w.WatchRecursiveBudgeted(root)
	return result.Watched, result.Unwatched, result.Err
}
```

`WatchRecursiveBudgeted` should decrement `w.recursiveWatchBudget` on each
successful `Add`. When the remaining budget is zero, count the current directory
and later directories under the root as unwatched, set `BudgetExhausted`, and
avoid additional `Add` calls.

- [ ] **Step 4: Run test to verify it passes**

Run:

```bash
go test -tags fts5 ./internal/sync -run TestWatchRecursiveBudget_DegradesWhenBudgetExhausted -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/watcher.go internal/sync/watcher_test.go
git commit -m "fix(sync): cap recursive file watcher setup"
```

______________________________________________________________________

### Task 2: Stop Recursive Walk On Resource Exhaustion

**Files:**

- Modify: `internal/sync/watcher.go`

- Test: `internal/sync/watcher_test.go`

- [ ] **Step 1: Write errno classification tests**

Add focused tests for resource exhaustion detection. This does not need real
descriptor exhaustion.

```go
func TestIsWatchResourceExhaustion(t *testing.T) {
	if !isWatchResourceExhaustion(syscall.EMFILE) {
		t.Fatal("EMFILE should be resource exhaustion")
	}
	if !isWatchResourceExhaustion(syscall.ENOSPC) {
		t.Fatal("ENOSPC should be resource exhaustion")
	}
	if isWatchResourceExhaustion(os.ErrNotExist) {
		t.Fatal("ErrNotExist should not be resource exhaustion")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test -tags fts5 ./internal/sync -run TestIsWatchResourceExhaustion -count=1
```

Expected: FAIL because `isWatchResourceExhaustion` does not exist.

- [ ] **Step 3: Implement errno classification and early stop**

In `internal/sync/watcher.go`, import `errors` and `syscall`, then add:

```go
func isWatchResourceExhaustion(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENOSPC)
}
```

Inside `WatchRecursiveBudgeted`, when `w.watcher.Add(path)` returns an error:

```go
result.Unwatched++
if isWatchResourceExhaustion(addErr) {
	result.ResourceExhausted = true
	result.ResourceExhaustedAt = path
	return filepath.SkipAll
}
```

After `WalkDir`, if the returned error is `filepath.SkipAll`, clear `result.Err`
because this is an intentional degradation, not a fatal walk error.

- [ ] **Step 4: Run watcher tests**

Run:

```bash
go test -tags fts5 ./internal/sync -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/watcher.go internal/sync/watcher_test.go
git commit -m "fix(sync): stop watching on resource exhaustion"
```

______________________________________________________________________

### Task 3: Surface Degradation In `startFileWatcher`

**Files:**

- Modify: `cmd/agentsview/main.go`

- Test: existing manual compile via `go test ./cmd/agentsview`

- [ ] **Step 1: Update `startFileWatcher` to use result**

Replace:

```go
watched, uw, _ := watcher.WatchRecursive(r.root)
totalWatched += watched
if uw > 0 {
	unwatchedDirs = append(unwatchedDirs, r.dir)
	log.Printf(
		"Couldn't watch %d directories under %s, will poll every %s",
		uw, r.dir, unwatchedPollInterval,
	)
}
```

with:

```go
result := watcher.WatchRecursiveBudgeted(r.root)
totalWatched += result.Watched
if result.Unwatched > 0 || result.BudgetExhausted || result.ResourceExhausted || result.Err != nil {
	unwatchedDirs = append(unwatchedDirs, r.dir)
	log.Printf(
		"Couldn't watch %d directories under %s, will poll every %s",
		result.Unwatched, r.dir, unwatchedPollInterval,
	)
	if result.Err != nil {
		log.Printf("watching %s: %v", r.dir, result.Err)
	}
}
```

After the existing `Watching ...` stdout line, add:

```go
if len(unwatchedDirs) > 0 {
	fmt.Printf(
		"Polling %d roots every %s for changes\n",
		len(unwatchedDirs), unwatchedPollInterval,
	)
}
```

- [ ] **Step 2: Run command package tests**

Run:

```bash
go test -tags fts5 ./cmd/agentsview -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/agentsview/main.go
git commit -m "fix(serve): report polled watcher roots"
```

______________________________________________________________________

### Task 4: Start Watcher After Listener Is Bound

**Files:**

- Modify: `cmd/agentsview/main.go`

- Modify: `cmd/agentsview/serve_runtime.go`

- Test: `cmd/agentsview/serve_runtime_test.go`

- [ ] **Step 1: Write startup ordering test**

Add a test that starts a minimal server through `startServerWithOptionalCaddy`
in a goroutine, blocks a new post-listen hook, and verifies the listener is
dialable while that hook is blocked.

```go
func TestServeRuntimeListenerBoundBeforePostListenHookReturns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open DB: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	cfg := config.Config{
		Host:    "127.0.0.1",
		Port:    0,
		DataDir: dir,
		DBPath:  dbPath,
	}
	var prepErr error
	cfg, prepErr = prepareServeRuntimeConfig(cfg, serveRuntimeOptions{
		Mode:          "serve",
		RequestedPort: 0,
	})
	if prepErr != nil {
		t.Fatalf("prepareServeRuntimeConfig: %v", prepErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv := server.New(cfg, database, nil, server.WithBaseContext(ctx))

	hookStarted := make(chan struct{})
	releaseHook := make(chan struct{})
	resultCh := make(chan struct {
		rt  *serveRuntime
		err error
	}, 1)

	go func() {
		rt, err := startServerWithOptionalCaddy(ctx, cfg, srv, serveRuntimeOptions{
			Mode:          "serve",
			RequestedPort: 0,
			PostListen: func() {
				close(hookStarted)
				<-releaseHook
			},
		})
		resultCh <- struct {
			rt  *serveRuntime
			err error
		}{rt: rt, err: err}
	}()

	select {
	case <-hookStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("post-listen hook did not start")
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port)), 2*time.Second)
	if err != nil {
		t.Fatalf("listener was not reachable while hook was blocked: %v", err)
	}
	_ = conn.Close()

	close(releaseHook)
	var rt *serveRuntime
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("startServerWithOptionalCaddy: %v", result.err)
		}
		rt = result.rt
	case <-time.After(2 * time.Second):
		t.Fatal("startServerWithOptionalCaddy did not return after hook release")
	}

	cancel()
	if err := waitForServerRuntime(ctx, srv, rt); err != nil {
		t.Fatalf("waitForServerRuntime: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test -tags fts5 ./cmd/agentsview -run TestServeRuntimeListenerBoundBeforePostListenHookReturns -count=1
```

Expected: FAIL because `serveRuntimeOptions.PostListen` does not exist.

- [ ] **Step 3: Add post-listen hook to serve runtime**

In `cmd/agentsview/serve_runtime.go`, extend:

```go
type serveRuntimeOptions struct {
	Mode          string
	RequestedPort int
	PostListen    func()
}
```

In `startServerWithOptionalCaddy`, after `waitForLocalPort` succeeds and before
managed Caddy startup:

```go
if opts.PostListen != nil {
	opts.PostListen()
}
```

- [ ] **Step 4: Move watcher setup into the post-listen hook in `runServe`**

In `cmd/agentsview/main.go`, remove the early watcher startup block before
pricing/auth/server creation:

```go
stopWatcher, unwatchedDirs := startFileWatcher(cfg, engine)
defer stopWatcher()
...
if len(unwatchedDirs) > 0 {
	go startUnwatchedPoll(engine)
}
```

Before calling `startServerWithOptionalCaddy`, prepare watcher state and pass a
`PostListen` closure in `rtOpts`. This keeps watcher setup in the same
production path exercised by the startup-ordering test:

```go
var stopWatcher func()
if engine != nil {
	rtOpts.PostListen = func() {
		var unwatchedDirs []string
		stopWatcher, unwatchedDirs = startFileWatcher(cfg, engine)
		if len(unwatchedDirs) > 0 {
			go startUnwatchedPoll(engine)
		}
	}
}
```

After `startServerWithOptionalCaddy` succeeds, defer watcher cleanup if the hook
populated it:

```go
if stopWatcher != nil {
	defer stopWatcher()
}
```

Keep `go startPeriodicSync(engine, database)` before or after server startup,
but only run it when `engine != nil`.

- [ ] **Step 5: Run startup ordering test**

Run:

```bash
go test -tags fts5 ./cmd/agentsview -run TestServeRuntimeListenerBoundBeforePostListenHookReturns -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/agentsview/main.go cmd/agentsview/serve_runtime.go cmd/agentsview/serve_runtime_test.go
git commit -m "fix(serve): start watcher after listener"
```

______________________________________________________________________

### Task 5: Full Verification

**Files:**

- Verify only; no planned edits.

- [ ] **Step 1: Run focused Go tests**

Run:

```bash
go test -tags fts5 ./internal/sync ./cmd/agentsview -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader Go tests if practical**

Run:

```bash
go test -tags fts5 ./internal/... ./cmd/agentsview -count=1
```

Expected: PASS. If this is too slow or environment-dependent, record the exact
failure or timeout in the handoff.

- [ ] **Step 3: Check git status**

Run:

```bash
git status --short --branch
```

Expected: clean working tree on `fix/issue-364`.

- [ ] **Step 4: Commit any final test/doc adjustments**

Only if verification required edits:

```bash
git add <changed-files>
git commit -m "test: cover watcher degradation startup"
```

# Issue 364 Watch Degradation Design

## Context

Issue 364 reports `agentsview serve` failing after initial sync with:

```text
fatal: server failed to start: listen tcp 127.0.0.1:8080: socket: too many open files
```

The current startup sequence runs initial sync, recursively registers file
watchers for supported agent roots, and only then starts the HTTP listener.
Large session archives can consume enough process file descriptors during
fsnotify setup that the later `net.Listen` call fails.

Issue 302 addressed the same pattern for OpenHands by adding shallow watch mode,
but other recursive agent roots can still create thousands of watches.

## Goals

- `agentsview serve` must not fail because file watching consumes too many file
  descriptors.
- Small and medium installations should keep near-real-time updates where
  practical.
- Large directory trees should degrade to periodic polling with visible startup
  messaging.
- The fix should protect future agent integrations without requiring every agent
  definition to choose a perfect watch mode up front.

## Non-Goals

- Remove fsnotify entirely.
- Change sync parser behavior or database schema.
- Add user-facing configuration before the internal policy has proven useful.

## Recommended Approach

Start the HTTP server before expensive watcher registration, then make recursive
watch setup best-effort with an internal watch budget.

The sequence becomes:

1. Load config, open the database, and run initial sync as today.
1. Prepare and start the HTTP server.
1. After the server is listening, start file watching.
1. Register recursive watches only while under an internal budget.
1. When a root exceeds the budget or watch registration fails with resource
   exhaustion, mark that root as unwatched.
1. Periodically poll unwatched roots using the existing polling path.

This preserves live updates where they are cheap and prevents watcher setup from
blocking the web UI from starting.

## Components

- `cmd/agentsview/main.go`

  - Reorder `runServe` so server startup happens before `startFileWatcher`.
  - Keep watcher cleanup tied to serve runtime shutdown.
  - Make startup output report both watched directories and polled roots.

- `internal/sync/watcher.go`

  - Add a budget-aware recursive watch method or extend `WatchRecursive` with an
    option/result that reports why watching stopped.
  - Treat `EMFILE` and similar add failures as degradation signals, not fatal
    startup failures.

- Tests

  - Cover budget exhaustion without requiring real OS file descriptor
    exhaustion.
  - Cover watcher result accounting for watched and unwatched directories.
  - Cover serve/startup wiring enough to prevent returning to "watch before
    listen" behavior.

## Data Flow

`startFileWatcher` resolves each file-backed agent root. For each root, it asks
the watcher to register either a shallow watch or a budgeted recursive watch.
Successful watched directories stay on the fsnotify path and trigger debounced
`engine.SyncPaths(paths)`. Roots that cannot be fully watched are added to the
unwatched set. If the set is non-empty, `startUnwatchedPoll` periodically calls
`engine.SyncAll`.

## Error Handling

Watcher creation failure remains non-fatal and falls back to polling all roots.
Recursive watch failures are root-scoped: a large or failing root is degraded to
polling while other roots can still use fsnotify. The server listener is already
active before this work starts, so watcher resource pressure cannot prevent the
UI from becoming available.

## Testing

Unit tests should inject a low watch budget or a failing add operation so the
degradation path is deterministic. Integration-level CLI tests should avoid
depending on platform file descriptor limits and instead assert startup ordering
or the fact that watcher setup no longer gates listener startup.

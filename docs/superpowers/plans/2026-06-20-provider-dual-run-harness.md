# Provider Dual-Run Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the root-level provider migration harness so provider branches
must opt into shadow comparison instead of only adding parallel provider
implementations.

**Architecture:** The parser package owns the per-`AgentType` migration manifest
because provider branches already change parser factories. The sync package owns
the source-level observation helper because it converts provider `Fingerprint`
and `Parse` calls into engine-shaped planned effects without touching the live
database.

**Tech Stack:** Go 1.26, `testing`, `github.com/stretchr/testify`, git-spice
stacked branches.

______________________________________________________________________

### Task 1: Provider Migration Manifest

**Files:**

- Create: `internal/parser/provider_migration.go`

- Modify: `internal/parser/provider_test.go`

- [ ] **Step 1: Write the failing manifest tests**

Add tests that prove the manifest covers the registry and rejects a concrete
provider left in `legacy-only` mode:

```go
func TestProviderMigrationModesCoverRegistry(t *testing.T) {
	err := ValidateProviderMigrationModes(ProviderFactories(), ProviderMigrationModes())
	require.NoError(t, err)
}

func TestProviderMigrationModesRejectConcreteProviderLeftLegacyOnly(t *testing.T) {
	factory := testProviderFactory{def: AgentDef{Type: AgentCodex, DisplayName: "Codex"}}
	modes := map[AgentType]ProviderMigrationMode{
		AgentCodex: ProviderMigrationLegacyOnly,
	}

	err := ValidateProviderMigrationModes([]ProviderFactory{factory}, modes)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "codex")
	assert.Contains(t, err.Error(), "shadow-compare")
}
```

- [ ] **Step 2: Run the parser tests and verify RED**

Run:

```bash
go test -tags "fts5" ./internal/parser -run TestProviderMigrationModes -count=1
```

Expected: FAIL because `ProviderMigrationMode`, `ProviderMigrationModes`, and
`ValidateProviderMigrationModes` do not exist yet.

- [ ] **Step 3: Implement the manifest types and validation**

Create `internal/parser/provider_migration.go` with:

```go
type ProviderMigrationMode string

const (
	ProviderMigrationLegacyOnly          ProviderMigrationMode = "legacy-only"
	ProviderMigrationShadowCompare       ProviderMigrationMode = "shadow-compare"
	ProviderMigrationProviderAuthoritative ProviderMigrationMode = "provider-authoritative"
	ProviderMigrationImportOnly          ProviderMigrationMode = "import-only"
)
```

Add a registry-covering manifest initialized to `legacy-only`, return copies to
callers, and validate:

- every provider factory has one mode;

- no extra manifest entry points at an unknown agent;

- concrete non-legacy factories cannot remain `legacy-only`;

- `shadow-compare`, `provider-authoritative`, and `import-only` require a
  concrete factory;

- `import-only` is allowed only for Claude.ai and ChatGPT.

- [ ] **Step 4: Run the parser tests and verify GREEN**

Run:

```bash
go test -tags "fts5" ./internal/parser -run TestProviderMigrationModes -count=1
```

Expected: PASS.

### Task 2: Source-Level Provider Observation

**Files:**

- Create: `internal/sync/provider_shadow.go`

- Create: `internal/sync/provider_shadow_test.go`

- [ ] **Step 1: Write failing observation tests**

Add tests that use a fake provider to prove the helper:

- calls `Fingerprint` before `Parse`;
- converts `ParseOutcome` into an observation;
- records planned data-version/source/diagnostic effects in memory;
- never accepts a mismatched `SourceRef.Provider`.

The main test should assert:

```go
assert.Equal(t, []string{"fingerprint", "parse"}, provider.calls)
assert.Equal(t, []string{"codex:one"}, observation.Planned.DataVersionSessionIDs())
assert.Equal(t, []string{"codex:two"}, observation.Planned.RetrySessionIDs())
assert.Equal(t, []string{"source-key"}, observation.Planned.SourceKeys)
assert.Len(t, observation.Planned.Diagnostics, 1)
```

- [ ] **Step 2: Run the sync tests and verify RED**

Run:

```bash
go test -tags "fts5" ./internal/sync -run TestObserveProviderSource -count=1
```

Expected: FAIL because `ObserveProviderSource` and observation types do not
exist.

- [ ] **Step 3: Implement the minimal observation helper**

Create `internal/sync/provider_shadow.go` with:

```go
type ProviderObserveRequest struct {
	Source     parser.SourceRef
	Machine    string
	ForceParse bool
}

type ProviderObservation struct {
	Results            []parser.ParseResult
	ExcludedSessionIDs []string
	SourceErrors       []parser.SourceError
	SkipReason         parser.SkipReason
	ForceReplace       bool
	Planned            ProviderPlannedEffects
}
```

`ObserveProviderSource` checks the source/provider type match, calls
`Fingerprint`, calls `Parse`, and builds in-memory planned effects. It must not
accept a `db.DB`, `Engine`, writer callback, or mutable skip-cache reference.

- [ ] **Step 4: Run the sync tests and verify GREEN**

Run:

```bash
go test -tags "fts5" ./internal/sync -run TestObserveProviderSource -count=1
```

Expected: PASS.

### Task 3: Validation And Commit

**Files:**

- Modify as needed from Tasks 1-2.

- [ ] **Step 1: Format and verify**

Run:

```bash
go fmt ./...
go test -tags "fts5" ./internal/parser -run TestProviderMigrationModes -count=1
go test -tags "fts5" ./internal/sync -run TestObserveProviderSource -count=1
go test -tags "fts5" ./internal/parser -count=1
go test -tags "fts5" ./internal/sync -count=1
go vet ./...
git diff --check
```

Expected: all commands pass. If `go fmt ./...` rewrites unrelated comments,
restore only unrelated user-owned changes before committing.

- [ ] **Step 2: Commit on `provider-facade-core`**

Commit the root harness slice with a conventional message:

```bash
git add docs/superpowers/plans/2026-06-20-provider-dual-run-harness.md internal/parser/provider_migration.go internal/parser/provider_test.go internal/sync/provider_shadow.go internal/sync/provider_shadow_test.go
git commit -m "feat(parser): add provider migration harness"
```

- [ ] **Step 3: Restack and submit**

Run:

```bash
git-spice upstack restack
git-spice stack submit --update-only --no-web --nav-comment=false
```

Expected: dependent provider PRs are replayed on the harness branch and existing
draft PRs are updated without creating new PRs.

# Lifecycle Install Implementation Plan

> **已执行的历史计划（2026-08-11）：** 本计划中的 red 阶段、旧 Install parse-only 状态和 self-heal 替换说明记录实施过程；当前行为以 Feature 010 spec、ADR-002、ADR-003 和 ADR-009 为准，Install 已实现，Uninstall 仍为 parse-only。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the command-specific Install lifecycle so canonical Cloud powers can be installed, upgraded, downgraded, or skipped across one or all detected Agents under one recoverable lock, with structured results rendered only after a successful unlock.

**Architecture:** Keep Install behind a CLI-owned port and a dedicated lifecycle service. Lifecycle owns name/type/version resolution, Agent scheduling, Config reads, Cache/Cloud staging, and outcome classification; TransactionManager remains the only writer of journal-driven target and manifest state, including stale-manifest repair. Cache returns the public staging root directly, and the existing version package supplies a narrow pure selector.

**Tech Stack:** Go 1.26.5, Cobra, standard `context`/`errors`/`fmt`/`io`/`log/slog`/`regexp`/`slices`/`sort`/`strings`, `github.com/Masterminds/semver/v3`, existing APM lock/transaction/adapter/config/cache/cloud packages, Go `testing`.

## Global Constraints

- Follow [spec.md](spec.md) exactly; do not add Uninstall lifecycle behavior, Plugin/MCP adapters, a generic lifecycle runner, parallel Agent execution, retries beyond Fetch/Get/Fetch, or new output modes.
- Explicit CLI versions must be exactly `vMAJOR.MINOR.PATCH`; reject missing `v`, leading zeros, whitespace, prerelease, and build metadata.
- Match Power names case-sensitively and exactly. Preserve nonblank Name and Agent input bytes; do not trim or case-fold values before lifecycle execution.
- When `--type` is omitted, detect cross-type ambiguity by Name before applying an explicit version. A version must never silently choose a Type.
- Trust every Cloud `PowerRef` to contain canonical Type, Name, and three-component v-prefixed SemVer. Do not add invalid-Cloud validation, skip, fallback, or degraded sorting behavior.
- Hold one global lock across Recover, synchronous recovery Warn, Agent refresh, Cloud List/Get, Cache Fetch, Config reads, and every serial Transaction Install. Render only after a successful Release.
- Recovery success writes exactly one synchronous `slog.Warn` while the lock is held with `operation`, `agent`, `power`, `version`, original `phase`, `direction`, and `actions`.
- `--agent=all` uses the Registry snapshot's Agent ID lexical order and is fail-fast. The current Agent becomes `failed`; all remaining Agents become `unexecuted`.
- Same-version installs trust Config and return `unchanged` without checking the target, Fetching, or calling TransactionManager.
- If a different Previous version exists but its target is missing, TransactionManager must Remove only that Agent/Type/Name manifest entry, clean staging, return `ErrStaleManifest`, create no journal, call no Adapter, and stop the command.
- Cache `Fetch(domain.PowerRef)` returns the public staging root string. Adapter continues to locate `staging/<type>/<name>` from that root and the PowerRef.
- Output each target exactly as `<agent>/<type>/<name>@<target-version> <outcome>\n`, where outcome is `installed`, `upgraded`, `downgraded`, `unchanged`, `failed`, or `unexecuted`.
- Only a successful-Release `*cli.InstallBatchError` permits rendering while returning an error. Fatal and Release errors suppress all business output.
- Lifecycle, Version, Cache, Cloud, Adapter, Transaction, and render helpers never call `slog.Error` or print directly. `CLIEntry.Execute` remains the single Error/usage exit.
- Add concise normative Chinese comments to important exported interfaces, results, constructors, sentinel errors, and lifecycle methods.
- Preserve unrelated user changes. Add exactly one completed behavior entry to `docs/Changelog.md` only after implementation and verification, using `~/.codex/docs/Changelog.md`.

## File and Responsibility Map

| File | Responsibility |
| --- | --- |
| `internal/cache/cache.go` | Define `Fetch(PowerRef) (stagingRoot string, error)` and remove `Content`. |
| `internal/cache/filesystem.go` | Return the public staging root after atomic staging publication. |
| `internal/cache/filesystem_test.go`, `internal/cloud/local_cache_integration_test.go` | Protect the new root-return contract and leaf layout. |
| `internal/version/selector.go`, `selector_test.go` | Select an exact requested version or maximum trusted SemVer without mutating input. |
| `internal/transaction/errors.go` | Export `ErrStaleManifest`. |
| `internal/transaction/artifacts.go` | Safely clean canonical staging before a journal exists. |
| `internal/transaction/install.go`, `install_test.go` | Repair a missing managed target by removing one manifest entry and aborting before journal/Adapter. |
| `internal/cli/install_contract.go`, `install_render.go` | Own Install lifecycle/result/error types and exact line rendering. |
| `internal/cli/install_contract_test.go` | Protect outcomes, error unwrapping, exact output, and writer-prefix semantics. |
| `internal/lifecycle/install_resolve.go`, `install_resolve_test.go` | Resolve exact Name, Type ambiguity, and target Version without Cloud input mutation. |
| `internal/lifecycle/install.go`, `install_agent.go` | Orchestrate the one-lock lifecycle and one-Agent Cache/Transaction flow. |
| `internal/lifecycle/install_test.go` | Protect ordering, recovery logging, outcomes, fail-fast, release, and all error combinations. |
| `internal/lifecycle/dependencies.go` | Add the narrow `VersionSelector` dependency alias/interface. |
| `internal/cli/install.go`, `install_test.go` | Validate syntax, create lifecycle after logging, invoke, render, and preserve batch/writer causes. |
| `internal/cli/root.go`, CLI tests | Inject an independent Install factory without changing Search/List behavior. |
| `cmd/apm/main.go`, `main_test.go` | Construct default Install dependencies and verify bundled installation end to end. |
| ADR/C4/overview/BUILD/Feature docs | Record canonical version, lock, stale entry, staging-root, outcome, and implemented-status contracts. |
| `docs/Changelog.md` | Add one reverse-chronological completed Feature 010 entry after all checks pass. |

---

### Task 1: Make Cache Fetch Return the Public Staging Root

**Files:**
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/filesystem_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`

**Interfaces:**
- Consumes: `domain.PowerRef`, `cache.ErrMiss`, existing `FilesystemCache.stagingPath`.
- Produces: `PowerCache.Fetch(domain.PowerRef) (string, error)`, where success returns the canonical public staging root.

- [ ] **Step 1: Change tests to require the root-return contract**

Replace assertions on `Content.Path` with assertions on the returned string. The focused success test must have this shape:

```go
func TestFilesystemCacheFetchesVersionAndReturnsStagingRoot(t *testing.T) {
	stateHome, cacheHome := setCacheEnvironment(t)
	source := filepath.Join(cacheHome, "apm", "skills", "demo", "v1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o640); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.Fetch(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(stateHome, ".apm", "staging")
	if root != wantRoot {
		t.Fatalf("Fetch() root = %q, want %q", root, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(root, "skills", "demo", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
```

In `TestLocalCloudAndCacheFetchGetFetch`, assert `stagingRoot == filepath.Join(stateHome, ".apm", "staging")` and continue checking leaf files under `filepath.Join(stagingRoot, "skills", ref.Name)`.

- [ ] **Step 2: Run the focused tests and verify the old API fails**

Run:

```bash
go test -count=1 ./internal/cache ./internal/cloud -run 'Fetch|LocalCloudAndCache'
```

Expected: FAIL to compile because `Fetch` still returns `cache.Content` and tests compare it with a string.

- [ ] **Step 3: Replace the Cache interface and implementation result**

Make `internal/cache/cache.go` contain:

```go
package cache

import "apm/internal/domain"

// PowerCache 定义将版本化能力物化到公共 staging 根的操作。
type PowerCache interface {
	Fetch(domain.PowerRef) (stagingRoot string, err error)
}
```

Change `FilesystemCache.Fetch` to:

```go
// Fetch 将指定版本的缓存 Skill 复制到公共 staging，并返回 staging 根目录。
func (c *FilesystemCache) Fetch(ref domain.PowerRef) (stagingRoot string, resultErr error) {
	if ref.Version == "" {
		return "", errors.New("power version is required")
	}
	if err := os.RemoveAll(c.stagingPath); err != nil {
		return "", fmt.Errorf("remove staging path %q: %w", c.stagingPath, err)
	}
	if err := os.RemoveAll(c.stagingTempPath); err != nil {
		return "", fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err)
	}
	defer func() {
		if err := os.RemoveAll(c.stagingTempPath); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err))
		}
	}()

	if ref.Type != domain.PowerTypeSkill {
		return "", ErrMiss
	}
	source := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrMiss
		}
		return "", fmt.Errorf("stat cache path %q: %w", source, err)
	}
	target := filepath.Join(c.stagingTempPath, "skills", ref.Name)
	if err := filesystem.CopyDir(source, target); err != nil {
		return "", fmt.Errorf("copy cache path %q to staging path %q: %w", source, target, err)
	}
	if err := os.MkdirAll(filepath.Dir(c.stagingPath), 0o755); err != nil {
		return "", fmt.Errorf("create staging parent path %q: %w", filepath.Dir(c.stagingPath), err)
	}
	if err := os.Rename(c.stagingTempPath, c.stagingPath); err != nil {
		return "", fmt.Errorf("rename staging temporary path %q to staging path %q: %w", c.stagingTempPath, c.stagingPath, err)
	}
	return c.stagingPath, nil
}
```

- [ ] **Step 4: Run Cache and Cloud tests**

Run:

```bash
go test -count=1 ./internal/cache ./internal/cloud
```

Expected: PASS. Also run `rg -n 'cache\.Content|Content\.Path' cmd internal`; expected: no matches.

- [ ] **Step 5: Commit the Cache contract**

```bash
git add internal/cache/cache.go internal/cache/filesystem.go internal/cache/filesystem_test.go internal/cloud/local_cache_integration_test.go
git commit -m "refactor(cache): return staging root from fetch"
```

---

### Task 2: Implement Exact and Latest Version Selection

**Files:**
- Modify: `internal/version/selector.go`
- Create: `internal/version/selector_test.go`

**Interfaces:**
- Consumes: trusted canonical version strings such as `v1.9.0` and `v1.10.0`.
- Produces: `(*Selector).Select(candidates []string, requested string) (string, error)` without input mutation.

- [ ] **Step 1: Write selector tests**

Create table-driven tests with exact assertions:

```go
func TestSelectorSelectsRequestedOrLatestVersion(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		requested  string
		want       string
		wantErr    string
	}{
		{name: "latest", candidates: []string{"v1.9.0", "v1.10.0", "v0.9.0"}, want: "v1.10.0"},
		{name: "exact", candidates: []string{"v1.0.0", "v2.0.0"}, requested: "v1.0.0", want: "v1.0.0"},
		{name: "empty", wantErr: "no candidate versions"},
		{name: "missing exact", candidates: []string{"v1.0.0"}, requested: "v2.0.0", wantErr: `version "v2.0.0" not found`},
	}
	selector := version.NewSelector()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]string(nil), tt.candidates...)
			got, err := selector.Select(tt.candidates, tt.requested)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Select() error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("Select() = %q, %v; want %q, nil", got, err, tt.want)
			}
			if !reflect.DeepEqual(tt.candidates, before) {
				t.Fatalf("Select() changed candidates: got %v, want %v", tt.candidates, before)
			}
		})
	}
}
```

- [ ] **Step 2: Run the selector tests and verify failure**

Run: `go test -count=1 ./internal/version`

Expected: FAIL because the zero-value placeholder returns `"", nil`.

- [ ] **Step 3: Implement the selector**

Replace the placeholder with:

```go
package version

import (
	"fmt"

	"github.com/Masterminds/semver/v3"
)

// Selector 在可信的规范候选中选择显式版本或 SemVer 最大版本。
type Selector struct{}

// NewSelector 创建无状态版本选择器。
func NewSelector() *Selector { return &Selector{} }

// Select 精确选择 requested；requested 为空时选择最大 SemVer，且不修改输入。
func (s *Selector) Select(candidates []string, requested string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("select power version: no candidate versions")
	}
	if requested != "" {
		for _, candidate := range candidates {
			if candidate == requested {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("select power version: version %q not found", requested)
	}
	latest := candidates[0]
	for _, candidate := range candidates[1:] {
		if semver.MustParse(candidate).GreaterThan(semver.MustParse(latest)) {
			latest = candidate
		}
	}
	return latest, nil
}
```

- [ ] **Step 4: Run tests and commit**

```bash
go test -count=1 ./internal/version
git add internal/version/selector.go internal/version/selector_test.go
git commit -m "feat(version): select exact or latest power version"
```

Expected: tests PASS and commit succeeds.

---

### Task 3: Repair Missing Managed Targets Before Starting a Transaction

**Files:**
- Modify: `internal/transaction/errors.go`
- Modify: `internal/transaction/artifacts.go`
- Modify: `internal/transaction/install.go`
- Modify: `internal/transaction/install_test.go`

**Interfaces:**
- Consumes: `InstallRequest.Previous`, `ManifestWriter.Remove`, canonical `StagingRoot`, Adapter `TargetPath`.
- Produces: exported sentinel `transaction.ErrStaleManifest`; a missing managed target removes exactly one manifest entry, cleans staging, and returns before journal creation.

- [ ] **Step 1: Replace the old self-heal test with stale-entry tests**

Extend `recordingManifestWriter` with `removedAgent domain.AgentID`, store the Agent in `Remove`, and replace `TestInstallManagedMissingTargetSelfHeals` with:

```go
func TestInstallManagedMissingTargetRemovesManifestCleansStagingAndStops(t *testing.T) {
	fixture := newInstallFixture(t)
	old := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	fixture.request.Previous = &old
	if err := os.MkdirAll(fixture.staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.staging, "staged"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := fixture.service.Install(context.Background(), fixture.request)
	if !errors.Is(err, ErrStaleManifest) {
		t.Fatalf("Install() error = %v, want ErrStaleManifest", err)
	}
	if fixture.manifests.removedAgent != fixture.request.Agent || fixture.manifests.removed.Name != old.Name || fixture.manifests.removed.Type != old.Type {
		t.Fatalf("Remove = agent %q ref %+v, want agent %q ref %+v", fixture.manifests.removedAgent, fixture.manifests.removed, fixture.request.Agent, old)
	}
	if _, statErr := os.Lstat(fixture.staging); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", statErr)
	}
	if fixture.journals.found {
		t.Fatal("stale repair created a journal")
	}
	if !reflect.DeepEqual(fixture.events, []string{"manifest:remove"}) {
		t.Fatalf("events = %v, want only manifest removal", fixture.events)
	}
}
```

Add `TestInstallManagedMissingTargetJoinsRemoveAndStagingCleanupFailures`: set `manifests.removeErr`, inject `files.removeAll` failure only for `fixture.staging`, call Install, and assert `errors.Is` matches `ErrStaleManifest`, the Remove error, and cleanup error; assert no journal or Adapter event.

- [ ] **Step 2: Run focused transaction tests and verify old behavior fails**

Run:

```bash
go test -count=1 ./internal/transaction -run 'InstallManagedMissingTarget|RecordingManifestWriterShape'
```

Expected: FAIL because `ErrStaleManifest` is undefined and current Install publishes the missing target.

- [ ] **Step 3: Add the sentinel and a pre-journal staging cleanup helper**

Add to `errors.go`:

```go
// ErrStaleManifest 表示清单引用的受管安装目标已经缺失。
var ErrStaleManifest = errors.New("installed power manifest target is missing")
```

Refactor `artifacts.go` so committed cleanup and stale cleanup share this exact helper:

```go
func cleanupInstallStagingWithOps(stagingRoot, targetPath string, files fileOps) error {
	if err := validateCanonicalStaging(stagingRoot, ErrUnsafePath); err != nil {
		return err
	}
	physicalStaging, err := filesystemutil.ResolvePhysicalPath(stagingRoot)
	if err != nil {
		return fmt.Errorf("resolve physical staging %q: %w", stagingRoot, err)
	}
	physicalTarget, err := filesystemutil.ResolvePhysicalPath(targetPath)
	if err != nil {
		return fmt.Errorf("resolve physical target %q: %w", targetPath, err)
	}
	targetWithinStaging, err := filesystemutil.PathEqualOrWithin(physicalStaging, physicalTarget)
	if err != nil {
		return fmt.Errorf("compare staging and target: %w", err)
	}
	stagingWithinTarget, err := filesystemutil.PathEqualOrWithin(physicalTarget, physicalStaging)
	if err != nil {
		return fmt.Errorf("compare target and staging: %w", err)
	}
	if targetWithinStaging || stagingWithinTarget {
		return fmt.Errorf("physical staging %q overlaps target %q: %w", physicalStaging, physicalTarget, ErrUnsafePath)
	}
	if err := files.removeAll(stagingRoot); err != nil {
		return fmt.Errorf("remove install staging %q: %w", stagingRoot, err)
	}
	return nil
}
```

After `cleanupCommittedStagingWithOps` validates the journal, delegate to `cleanupInstallStagingWithOps(journal.StagingRoot, journal.TargetPath, files)`.

- [ ] **Step 4: Change Install's missing-target branch**

Immediately after the authoritative target existence check and before transaction ID generation, add:

```go
if request.Previous != nil && !targetExists {
	staleErr := fmt.Errorf("installed target %q for agent %q power %q is missing: %w", target, request.Agent, request.Power.Name, ErrStaleManifest)
	removeErr := s.manifests.Remove(request.Agent, request.Previous.Type, request.Previous.Name)
	cleanupErr := cleanupInstallStagingWithOps(request.StagingRoot, target, s.files)
	if removeErr != nil {
		return errors.Join(
			staleErr,
			fmt.Errorf("remove stale manifest entry for agent %q power %q: %w", request.Agent, request.Previous.Name, removeErr),
			cleanupErr,
		)
	}
	return errors.Join(
		fmt.Errorf("removed stale manifest entry for agent %q power %q after target disappeared: %w", request.Agent, request.Previous.Name, ErrStaleManifest),
		cleanupErr,
	)
}
```

Keep `Previous == nil && targetExists` as the existing unmanaged-target conflict.

- [ ] **Step 5: Run focused and full transaction tests**

```bash
go test -count=1 ./internal/transaction -run 'Install|CleanupCommittedStaging|RecordingManifestWriterShape'
go test -count=1 ./internal/transaction ./internal/config
```

Expected: PASS. The existing `TestJSONRepositoryRemoveDeletesAllMatchesAndPreservesOrder` remains green, proving `Remove(agent,type,name)` preserves unrelated entries.

- [ ] **Step 6: Commit stale repair**

```bash
git add internal/transaction/errors.go internal/transaction/artifacts.go internal/transaction/install.go internal/transaction/install_test.go
git commit -m "fix(transaction): remove stale install manifest entries"
```

---

### Task 4: Define the CLI-Owned Install Result Contract

**Files:**
- Create: `internal/cli/install_contract.go`
- Create: `internal/cli/install_render.go`
- Create: `internal/cli/install_contract_test.go`

**Interfaces:**
- Consumes: `context.Context`, `cli.Flags`, `domain.AgentID`, `domain.PowerRef`, `io.Writer`.
- Produces: `InstallOutcome`, `InstallItem`, `InstallResult`, `InstallBatchError`, `InstallLifecycle`, `InstallLifecycleFactory`, and `renderInstall`.

- [ ] **Step 1: Write contract and renderer tests in package `cli`**

The exact-output test must use all six outcomes:

```go
func TestRenderInstallWritesExactOutcomeLines(t *testing.T) {
	result := InstallResult{Items: []InstallItem{
		{Agent: domain.AgentClaudeCode, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeInstalled},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeUpgraded},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.0.0"}, Outcome: InstallOutcomeDowngraded},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "same", Version: "v1.0.0"}, Outcome: InstallOutcomeUnchanged},
		{Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "bad", Version: "v1.0.0"}, Outcome: InstallOutcomeFailed},
		{Agent: domain.AgentHermes, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "later", Version: "v1.0.0"}, Outcome: InstallOutcomeUnexecuted},
	}}
	var output bytes.Buffer
	if err := renderInstall(&output, result); err != nil {
		t.Fatal(err)
	}
	want := "claude-code/skill/demo@v2.0.0 installed\n" +
		"codex/skill/demo@v2.0.0 upgraded\n" +
		"hermes-agent/skill/demo@v2.0.0 downgraded\n" +
		"codex/skill/same@v1.0.0 unchanged\n" +
		"codex/skill/bad@v1.0.0 failed\n" +
		"hermes-agent/skill/later@v1.0.0 unexecuted\n"
	if output.String() != want {
		t.Fatalf("renderInstall() = %q, want %q", output.String(), want)
	}
}
```

Add tests that `InstallBatchError.Unwrap()` preserves its cause and that a writer failing on the second call returns a wrapped `write install result` error while preserving the first complete line.

- [ ] **Step 2: Run the test and verify failure**

Run: `go test -count=1 ./internal/cli -run 'InstallOutcome|RenderInstall|InstallBatchError'`

Expected: FAIL to compile because the Install contract does not exist.

- [ ] **Step 3: Add the contract**

Create `install_contract.go` with the exact types approved by the spec. Implement the error methods as:

```go
func (e *InstallBatchError) Error() string {
	return fmt.Sprintf("install power for agent %q failed: %v", e.Agent, e.Err)
}

// Unwrap 返回当前失败 Agent 的底层原因。
func (e *InstallBatchError) Unwrap() error { return e.Err }
```

Declare all six constants as typed `InstallOutcome` strings, and add the compile-time-facing port:

```go
// InstallLifecycle 定义 install 命令唯一需要的生命周期入口。
type InstallLifecycle interface {
	Install(context.Context, Flags) (InstallResult, error)
}

// InstallLifecycleFactory 在日志初始化后创建 Install 生命周期。
type InstallLifecycleFactory func() (InstallLifecycle, error)
```

- [ ] **Step 4: Add the renderer**

Create `install_render.go`:

```go
func renderInstall(writer io.Writer, result InstallResult) error {
	for _, item := range result.Items {
		line := string(item.Agent) + "/" + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version + " " + string(item.Outcome) + "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write install result: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 5: Run and commit**

```bash
go test -count=1 ./internal/cli -run 'InstallOutcome|RenderInstall|InstallBatchError'
git add internal/cli/install_contract.go internal/cli/install_render.go internal/cli/install_contract_test.go
git commit -m "feat(cli): define install result contract"
```

Expected: focused tests PASS.

---

### Task 5: Resolve Install Power Type and Version

**Files:**
- Modify: `internal/lifecycle/dependencies.go`
- Create: `internal/lifecycle/install_resolve.go`
- Create: `internal/lifecycle/install_resolve_test.go`

**Interfaces:**
- Consumes: `cli.Flags{Name,Type,Version}`, trusted `[]domain.PowerRef`, `VersionSelector.Select([]string,string)`.
- Produces: `resolveInstallPower(flags, refs, selector) (domain.PowerRef, error)` with ambiguity-before-version semantics.

- [ ] **Step 1: Add a recording selector and resolution tests**

Define a test selector that records candidates/request and returns a configured value. Cover these exact cases:

```go
tests := []struct {
	name        string
	flags       cli.Flags
	refs        []domain.PowerRef
	selected    string
	want        domain.PowerRef
	wantErr     string
	wantNoCall  bool
}{
	{name: "unique inferred type latest", flags: cli.Flags{Name: "demo"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}}, selected: "v2.0.0", want: domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v2.0.0"}},
	{name: "explicit exact type and version", flags: cli.Flags{Name: "demo", Type: "plugin", Version: "v1.0.0"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}}, selected: "v1.0.0", want: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}},
	{name: "case sensitive name", flags: cli.Flags{Name: "Demo"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}, wantErr: `power "Demo" not found`, wantNoCall: true},
	{name: "cross type ambiguity before explicit version", flags: cli.Flags{Name: "demo", Version: "v1.0.0"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, {Name: "demo", Type: domain.PowerTypePlugin, Version: "v2.0.0"}}, wantErr: "multiple power types", wantNoCall: true},
	{name: "explicit type does not fall back", flags: cli.Flags{Name: "demo", Type: "mcp"}, refs: []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}, wantErr: `type "mcp" not found`, wantNoCall: true},
}
```

For every case, clone `refs` before calling and assert `reflect.DeepEqual(refs, before)`. In the ambiguity case assert the selector was not called even though `flags.Version` is set.

- [ ] **Step 2: Run tests and verify failure**

Run: `go test -count=1 ./internal/lifecycle -run ResolveInstallPower`

Expected: FAIL because `VersionSelector` and `resolveInstallPower` do not exist.

- [ ] **Step 3: Add the narrow dependency and resolver**

Add to `dependencies.go`:

```go
// VersionSelector 定义 Install 在可信候选中选择目标版本的最小接口。
type VersionSelector interface {
	Select([]string, string) (string, error)
}
```

Implement `resolveInstallPower` by:

1. collecting refs whose Name exactly equals `flags.Name`;
2. if Type is empty, collecting distinct Types and sorting their string values for a deterministic ambiguity error;
3. rejecting zero or multiple inferred Types before calling the selector;
4. if Type is explicit, filtering only exact `domain.PowerType(flags.Type)`;
5. passing candidate versions and `flags.Version` to `selector.Select`;
6. returning `{Name: flags.Name, Type: selectedType, Version: selectedVersion}`.

The central branch must read:

```go
if flags.Type == "" && len(types) > 1 {
	values := make([]string, 0, len(types))
	for powerType := range types {
		values = append(values, string(powerType))
	}
	slices.Sort(values)
	return domain.PowerRef{}, fmt.Errorf("install power %q has multiple power types %v; specify --type", flags.Name, values)
}
```

Wrap selector failures as `select version for install power %q type %q: %w`.

- [ ] **Step 4: Run tests with the real selector and commit**

```bash
go test -count=1 ./internal/version ./internal/lifecycle -run 'Selector|ResolveInstallPower'
git add internal/lifecycle/dependencies.go internal/lifecycle/install_resolve.go internal/lifecycle/install_resolve_test.go
git commit -m "feat(lifecycle): resolve install power references"
```

Expected: PASS.

---

### Task 6: Implement Successful Install Lifecycle Paths

**Files:**
- Replace: `internal/lifecycle/install.go`
- Create: `internal/lifecycle/install_agent.go`
- Create: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/service.go` only if the shared typed-nil helper or package comments require relocation; do not add a generic runner.

**Interfaces:**
- Consumes: `Locker`, `TransactionManager`, `CloudClient`, `PowerCache`, `AdapterRegistry`, `ConfigRepository`, `VersionSelector`, `cli.InstallResult`.
- Produces: `NewInstall(Locker, TransactionManager, CloudClient, PowerCache, AdapterRegistry, ConfigRepository, VersionSelector) (*InstallService, error)`, `(*InstallService).Install(context.Context, cli.Flags)`, one-Agent Fetch/Get/Transaction flow, and install-specific recovery logging.

- [ ] **Step 1: Build focused fakes and write successful-path tests**

Create fakes that append exact event names to one shared `[]string`: `acquire`, `recover`, `refresh`, `cloud:list`, `resolve:<agent>`, `config:find:<agent>`, `cache:fetch:<agent>`, `cloud:get`, `transaction:install:<agent>`, and `release`.

Write these tests with exact outcomes and dependency suppression:

- `TestInstallFirstInstallOrdersDependenciesAndReturnsInstalled`
- `TestInstallCacheMissGetsCloudAndFetchesAgain`
- `TestInstallSameVersionReturnsUnchangedWithoutFetchOrTransaction`
- `TestInstallClassifiesUpgradeAndDowngradeBySemVer`
- `TestInstallAllUsesSnapshotOrderAndFetchesSeparatelyPerChangedAgent`
- `TestInstallRecoveryWarnIsWrittenBeforeRefreshAndRelease`
- `TestInstallNoJournalIsSilentUnlessVerbose`
- `TestNewInstallRejectsNilAndTypedNilDependencies`

The first test's required subsequence is:

```go
wantEvents := []string{
	"acquire", "recover", "refresh", "cloud:list",
	"resolve:codex", "config:find:codex", "cache:fetch:codex",
	"transaction:install:codex", "release",
}
```

The all-Agent test must seed snapshot order `claude-code`, `codex`, `hermes-agent`, return unchanged for Codex, and assert Fetch occurs for Claude Code and Hermes only, once each and in that order.

- [ ] **Step 2: Run focused tests and verify failure**

Run:

```bash
go test -count=1 ./internal/lifecycle -run 'InstallFirst|InstallCacheMiss|InstallSameVersion|InstallClassifies|InstallAllUses|InstallRecoveryWarn|InstallNoJournal|NewInstall'
```

Expected: FAIL because `InstallService` and `NewInstall` do not exist.

- [ ] **Step 3: Implement the service constructor and target selection**

Define:

```go
type InstallService struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
	cache        PowerCache
	registry     AdapterRegistry
	config       ConfigRepository
	versions     VersionSelector
}

// NewInstall 创建持有完整共享状态区间的 Install 生命周期。
func NewInstall(locker Locker, transactions TransactionManager, cloud CloudClient, cache PowerCache, registry AdapterRegistry, config ConfigRepository, versions VersionSelector) (*InstallService, error)
```

Use `isNilInterface` for all seven dependencies and return dependency-specific messages such as `create Install lifecycle with nil power cache`. Add `var _ cli.InstallLifecycle = (*InstallService)(nil)`.

Add `installAgents`:

```go
func installAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	target := domain.AgentID(flags.Agent)
	if target == domain.AllAgents {
		agents := snapshot.Ordered()
		if len(agents) == 0 {
			return nil, errors.New("install power: no supported installed agents")
		}
		return agents, nil
	}
	if !snapshot.Contains(target) {
		return nil, fmt.Errorf("install power for agent %q: unsupported or not installed", target)
	}
	return []domain.AgentID{target}, nil
}
```

- [ ] **Step 4: Implement one-Agent work**

In `install_agent.go`, implement:

```go
func (s *InstallService) fetchInstallStaging(ref domain.PowerRef) (string, error) {
	staging, err := s.cache.Fetch(ref)
	if err == nil {
		return staging, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		return "", fmt.Errorf("fetch install power %+v from cache: %w", ref, err)
	}
	if err := s.cloud.Get(ref); err != nil {
		return "", fmt.Errorf("get install power %+v from Cloud: %w", ref, err)
	}
	staging, err = s.cache.Fetch(ref)
	if err != nil {
		return "", fmt.Errorf("fetch downloaded install power %+v from cache: %w", ref, err)
	}
	return staging, nil
}
```

Implement `installOne` in this exact order: Resolve Adapter, Config Find, same-version early return, Fetch/Get/Fetch, Transaction Install, then outcome classification. Use `semver.MustParse` only for trusted target and Config-canonical Previous versions.

- [ ] **Step 5: Implement the one-lock success orchestration**

The public method must:

1. Acquire;
2. Recover and log;
3. Refresh snapshot;
4. choose Agents;
5. Cloud List;
6. resolve target Power;
7. iterate Agents serially;
8. Release explicitly;
9. return a non-nil Items slice.

Implement `logInstallRecovery` independently from Search/List. Its Warn fields must use `logging.Agent`, `logging.Power`, `logging.Version`, `logging.Phase`, `direction`, and `actions`. The no-journal Debug message is exactly `no active transaction before install`.

- [ ] **Step 6: Run successful-path tests**

```bash
go test -count=1 ./internal/lifecycle -run 'InstallFirst|InstallCacheMiss|InstallSameVersion|InstallClassifies|InstallAllUses|InstallRecoveryWarn|InstallNoJournal|NewInstall'
```

Expected: PASS. Inspect the Warn test to ensure the Unlock's pre-release callback sees `level=WARN` before `release` is appended.

- [ ] **Step 7: Commit the successful lifecycle**

```bash
git add internal/lifecycle/install.go internal/lifecycle/install_agent.go internal/lifecycle/install_test.go internal/lifecycle/service.go
git commit -m "feat(lifecycle): orchestrate successful installs"
```

---

### Task 7: Enforce Fail-Fast, Fatal, and Release Error Boundaries

**Files:**
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/install_test.go`

**Interfaces:**
- Consumes: successful orchestration from Task 6 and `cli.InstallBatchError` from Task 4.
- Produces: complete failed/unexecuted results only after successful Release; fatal/Release suppression; joined business and Release causes.

- [ ] **Step 1: Add fail-fast result tests**

Add `TestInstallAllFailsFastAndCompletesResultAfterRelease`. Seed three Agents in lexical order, make the second Agent's Config Find fail, and assert:

```go
want := cli.InstallResult{Items: []cli.InstallItem{
	{Agent: domain.AgentClaudeCode, Power: target, Outcome: cli.InstallOutcomeInstalled},
	{Agent: domain.AgentCodex, Power: target, Outcome: cli.InstallOutcomeFailed},
	{Agent: domain.AgentHermes, Power: target, Outcome: cli.InstallOutcomeUnexecuted},
}}
```

Assert no Hermes Resolve/Find/Fetch/Transaction event exists and Release occurs. Declare `var batch *cli.InstallBatchError`, assert `errors.As(err, &batch)` succeeds, and assert the marker unwraps the Config cause. Add the same assertion for one concrete Agent: one `failed` item plus marker.

- [ ] **Step 2: Add fatal and Release matrices**

Create table tests for these pre-target failures: Acquire, Recover, Refresh, concrete Agent missing, all snapshot empty, Cloud List, Name not found, Type ambiguity, explicit Type not found, version not found. For each, assert zero result and no `InstallBatchError`.

Create table tests for these post-target failures: Adapter Resolve, Config Find, first Fetch, Cloud Get, second Fetch, Transaction Install including `transaction.ErrStaleManifest`. For each, assert a `failed` item after successful Release and a marker containing the cause.

Add:

- `TestInstallRecoveryFailureStillReleasesAndJoinsReleaseError`
- `TestInstallFatalBusinessAndReleaseFailuresAreJoinedWithoutResult`
- `TestInstallBatchAndReleaseFailuresSuppressResultAndMarker`
- `TestInstallReleaseFailureAfterSuccessSuppressesCompletedResult`

The batch+release test must assert both causes with `errors.Is`, `result.Items == nil`, and `errors.As(err, &batch) == false`.

- [ ] **Step 3: Run tests and verify incomplete error behavior**

Run:

```bash
go test -count=1 ./internal/lifecycle -run 'InstallAllFailsFast|InstallFatal|InstallRecoveryFailure|InstallBatchAndRelease|InstallReleaseFailure'
```

Expected: at least fail-fast marker construction or Release suppression tests FAIL until the orchestration is completed.

- [ ] **Step 4: Implement delayed marker construction**

Store the raw failed Agent and raw business error during iteration. Append `failed`, then append `unexecuted` for `agents[index+1:]`, but do not create `InstallBatchError` yet.

Release handling must follow this structure:

```go
if releaseErr := unlock.Release(); releaseErr != nil {
	return cli.InstallResult{}, errors.Join(
		businessErr,
		fmt.Errorf("release install lock: %w", releaseErr),
	)
}
result := cli.InstallResult{Items: items}
if businessErr != nil {
	return result, &cli.InstallBatchError{Agent: failedAgent, Err: businessErr}
}
return result, nil
```

Every earlier failure after Acquire must call a command-specific helper that releases and joins errors. Never pass an already-created `InstallBatchError` into that helper.

- [ ] **Step 5: Run all lifecycle tests and static output scans**

```bash
go test -count=1 ./internal/lifecycle
rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle --glob '*.go'
```

Expected: tests PASS; scan has no matches.

- [ ] **Step 6: Commit failure semantics**

```bash
git add internal/lifecycle/install.go internal/lifecycle/install_agent.go internal/lifecycle/install_test.go
git commit -m "feat(lifecycle): enforce install fail-fast results"
```

---

### Task 8: Wire Install Into Cobra and Render After Lifecycle Return

**Files:**
- Modify: `internal/cli/install.go`
- Create: `internal/cli/install_test.go`
- Modify: `internal/cli/flags.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `internal/cli/search_test.go`
- Modify: `internal/cli/list_test.go`

**Interfaces:**
- Consumes: `InstallLifecycleFactory`, `InstallResult`, `InstallBatchError`, `renderInstall`.
- Produces: canonical explicit version validation, independent Install factory injection, success/batch rendering, and unchanged single Error/usage behavior.

- [ ] **Step 1: Update test entry factories and write Install CLI tests**

Update every `cli.New` call to pass a third command factory. Search/List test helpers must pass an Install factory that calls `t.Fatal` if invoked. The common helper must return an empty Install service:

```go
type emptyInstallLifecycle struct{}

func (emptyInstallLifecycle) Install(context.Context, cli.Flags) (cli.InstallResult, error) {
	return cli.InstallResult{Items: []cli.InstallItem{}}, nil
}
```

Create `install_test.go` with tests for:

- exact pass-through of Agent, Type, Name, Version;
- Name with surrounding spaces preserved;
- explicit canonical versions `v0.0.0`, `v1.2.3`, `v10.20.30`;
- invalid versions `1.2.3`, `v1.2`, `v1.2.3.4`, `v01.2.3`, `v1.02.3`, `v1.2.03`, `v1.2.3-alpha`, `v1.2.3+build`, ` v1.2.3`, `v1.2.3 `, `v-1.2.3` rejected before factory;
- exact six-outcome rendering;
- fatal result suppression;
- batch result rendering followed by one Error/usage;
- writer failure prefix and batch+writer `errors.Join`;
- factory after verbose setup, nil factory, nil/typed-nil service, constructor error;
- repeated Execute flag/factory isolation;
- Uninstall does not invoke Install factory.

Update the old valid CLI case from `demo=1.2.3` to `demo=v1.2.3`.

- [ ] **Step 2: Run CLI tests and verify compile/behavior failure**

Run:

```bash
go test -count=1 ./internal/cli -run 'Install|CLIParses|RepeatedExecution'
```

Expected: FAIL because `cli.New`, root construction, and `newInstallCommand` do not yet accept the factory and old Install remains parse-only.

- [ ] **Step 3: Add canonical explicit-version validation**

In `flags.go`, add:

```go
var canonicalInstallVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validateInstallVersion(version string) error {
	if version == "" {
		return nil
	}
	if !canonicalInstallVersion.MatchString(version) {
		return fmt.Errorf("invalid power version %q: must be vMAJOR.MINOR.PATCH", version)
	}
	return nil
}
```

Call this only after `parsePower` has distinguished absent version from `name=`.

- [ ] **Step 4: Inject the factory through CLIEntry and root**

Add `newInstallLifecycle InstallLifecycleFactory` to `CLIEntry`. Change signatures to:

```go
func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory) *CLIEntry

func newRootCommand(stdout, stderr io.Writer, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory) *cobra.Command
```

Register `newInstallCommand(newInstallLifecycle)` and leave `newUninstallCommand()` parse-only. Update package comments to say Search/List/Install.

- [ ] **Step 5: Invoke lifecycle and implement renderable-error handling**

Change `newInstallCommand` to accept the factory. After validation and `logParsedCommand`:

```go
if newLifecycle == nil {
	return errors.New("install lifecycle factory is required")
}
service, err := newLifecycle()
if err != nil {
	return fmt.Errorf("create install lifecycle: %w", err)
}
if isNilInterface(service) {
	return errors.New("create install lifecycle: nil service")
}
result, err := service.Install(command.Context(), flags)
if err == nil {
	return renderInstall(command.OutOrStdout(), result)
}
var batch *InstallBatchError
if !errors.As(err, &batch) {
	return err
}
if renderErr := renderInstall(command.OutOrStdout(), result); renderErr != nil {
	return errors.Join(err, renderErr)
}
return err
```

- [ ] **Step 6: Run the full CLI package**

```bash
go test -count=1 ./internal/cli
```

Expected: PASS. Error cases have empty stdout except batch/writer-prefix cases, exactly one `level=ERROR`, and one generated Usage.

- [ ] **Step 7: Commit CLI wiring**

```bash
git add internal/cli/install.go internal/cli/install_test.go internal/cli/flags.go internal/cli/root.go internal/cli/cli_test.go internal/cli/search_test.go internal/cli/list_test.go
git commit -m "feat(cli): execute and render install lifecycle"
```

---

### Task 9: Construct Default Install Dependencies and Prove End-to-End Installation

**Files:**
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `cache.NewFilesystem`, `version.NewSelector`, shared JSON Repository, transaction Manager, Registry, local Cloud, Locker, `lifecycle.NewInstall`.
- Produces: `newInstallLifecycle() (cli.InstallLifecycle, error)` and a default CLI entry with independent Search/List/Install factories.

- [ ] **Step 1: Write default lifecycle integration tests**

Add `TestNewInstallLifecycleInstallsBundledSkillAndThenReturnsUnchanged`:

1. set isolated `APM_HOME` and `XDG_CACHE_HOME`;
2. clear `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and `HERMES_HOME`;
3. change to repository root;
4. create `$APM_HOME/.codex`;
5. call `newInstallLifecycle()` and Install with `{Agent:"codex", Name:"foo-before-summary"}`;
6. assert one item `codex/skill/foo-before-summary@v0.0.1 installed`;
7. assert target `SKILL.md` exists and Config Find returns the same canonical ref;
8. call Install again and assert one `unchanged` item;
9. assert no active journal file remains and staging does not remain after the successful Adapter path.

Add `TestNewInstallLifecycleRejectsAllWhenNoAgentsAreInstalled` and assert the Cloud/cache/manifest roots are not created beyond the lock/transaction state needed for recovery.

- [ ] **Step 2: Run cmd tests and verify missing factory failure**

Run: `go test -count=1 ./cmd/apm -run NewInstallLifecycle`

Expected: FAIL because `newInstallLifecycle` is undefined.

- [ ] **Step 3: Add default dependency construction**

Implement:

```go
// newInstallLifecycle 创建 Install 命令所需的默认依赖，并共享清单读写 Repository。
func newInstallLifecycle() (cli.InstallLifecycle, error) {
	locker, err := lock.NewDefault()
	if err != nil {
		return nil, fmt.Errorf("create default lock: %w", err)
	}
	repository := config.NewJSONRepository()
	transactions, err := transaction.NewManager(repository)
	if err != nil {
		return nil, fmt.Errorf("create transaction manager: %w", err)
	}
	cloudClient, err := newLocalCloudClient()
	if err != nil {
		return nil, err
	}
	powerCache, err := cache.NewFilesystem()
	if err != nil {
		return nil, fmt.Errorf("create power cache: %w", err)
	}
	return lifecycle.NewInstall(
		locker,
		transactions,
		cloudClient,
		powerCache,
		adapter.NewRegistry(),
		repository,
		version.NewSelector(),
	)
}
```

Pass `newInstallLifecycle` as the final argument to `cli.New` in `main`.

- [ ] **Step 4: Run integration and full Go tests**

```bash
go test -count=1 ./cmd/apm -run 'New(Search|List|Install)Lifecycle'
go test -count=1 ./...
```

Expected: PASS.

- [ ] **Step 5: Commit default wiring**

```bash
git add cmd/apm/main.go cmd/apm/main_test.go
git commit -m "feat(apm): wire default install lifecycle"
```

---

### Task 10: Align ADR, C4, Feature Documentation, Changelog, and Final Verification

**Files:**
- Modify: `ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md`
- Modify: `ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md`
- Modify: `ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cli-entry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/lock-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/features/003-local-cloud-cache/feature-overview.md`
- Modify: `ivory-tower/features/003-local-cloud-cache/spec.md`
- Modify: `ivory-tower/features/006-agent-skill-adapters/feature-overview.md`
- Modify: `ivory-tower/features/007-central-lifecycle-transactions/feature-overview.md`
- Modify: `ivory-tower/features/007-central-lifecycle-transactions/spec.md`
- Modify: `ivory-tower/features/009-lifecycle-list/feature-overview.md`
- Modify: `ivory-tower/features/009-lifecycle-list/spec.md`
- Modify: `ivory-tower/features/010-lifecycle-install/feature-overview.md`
- Modify: `ivory-tower/features/010-lifecycle-install/spec.md` only to record implemented status without changing approved requirements.
- Modify: `BUILD.md`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: all implemented contracts and actual commit history from Tasks 1–9.
- Produces: one coherent authoritative documentation set and one completed-request Changelog entry.

- [ ] **Step 1: Revise the three authoritative ADRs**

ADR-002 must state:

- explicit Install versions require canonical v-prefixed three-part SemVer;
- Name is exact and case-sensitive;
- cross-Type Name collisions require `--type` before version selection;
- output is `<agent>/<type>/<name>@<version> <outcome>`;
- successful-Release batch failures render then return nonzero.

ADR-003 must state this exact lock order:

```text
Install: Acquire → Recover → lock-held Warn → RefreshInstalled → target validation
         → Cloud List/version resolution → serial Config/Cache/Cloud/Transaction work
         → successful Release → CLI render.
```

It must also state fail-fast, delayed marker construction, and Release suppression.

ADR-009 must delete the old “managed target missing self-heals by installing” rule and replace it with `ManifestWriter.Remove(agent,type,name)`, canonical staging cleanup, `ErrStaleManifest`, no journal/Adapter, and command abort. Add `Revised: 2026-08-11` where absent.

- [ ] **Step 2: Update C4 and lifecycle diagrams**

The CLI diagram must add Install port/result/batch error/renderer and retain Uninstall as parse-only. Core Lifecycle must show a dedicated Install service with seven dependencies. Cache Manager must show `Fetch(PowerRef) stagingRoot`; Adapter must show it locates the leaf using PowerRef. Lock Manager must add Install's one-lock interval without changing Search or List boundaries.

Rewrite `install.mmd` to match implemented flow, including:

- recovery Warn under lock;
- snapshot validation before Cloud;
- ambiguity before version;
- same-version unchanged without target check;
- Fetch/Get/Fetch per changed Agent;
- Transaction stale-entry Remove and abort;
- fail-fast completed/failed/unexecuted result;
- Release before renderer and Release-failure suppression.

Remove old nodes claiming lifecycle/transaction directly prints or that missing managed targets self-heal.

- [ ] **Step 3: Update project, historical feature, and BUILD status**

State that Search, List, and Install are implemented while Uninstall remains parse-only. Feature 003 overview and spec must supersede leaf `Content.Path` with the staging-root string. Feature 006 must state Adapter derives its leaf. Feature 007 must mark only Install's missing-managed-target self-heal as superseded by Feature 010; keep Uninstall's target-missing semantics unchanged. Feature 009 overview and spec must mark “install remains parse-only” as that feature's historical boundary.

BUILD must include valid commands:

```bash
./apm install foo-before-summary --agent codex
./apm install foo-before-summary=v0.0.1 --agent codex --type skill
./apm install foo-before-summary --agent all
```

and the exact six-outcome line contract.

- [ ] **Step 4: Commit documentation before the Changelog entry**

```bash
git add BUILD.md ivory-tower
git commit -m "docs(install): record lifecycle implementation"
```

- [ ] **Step 5: Run fresh full verification**

Run every command and record only actual passing evidence:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
cp go.mod /tmp/apm-go.mod.before
cp go.sum /tmp/apm-go.sum.before
go mod tidy
cmp go.mod /tmp/apm-go.mod.before
cmp go.sum /tmp/apm-go.sum.before
git diff --check
test -z "$(rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle internal/transaction internal/cache internal/version --glob '*.go' || true)"
test -z "$(rg -n 'cache\.Content|Content\.Path|func install\(context\.Context' cmd internal --glob '*.go' || true)"
rg -n 'parse-only|自愈|self-heal|Fetch\(.*Content' BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'
```

Expected: all build/test/check commands and both `test -z` scans exit 0; both `cmp` commands exit 0. Review the final documentation scan line by line: `parse-only` may describe only Uninstall or an explicitly historical Feature boundary; old Install self-heal and `Fetch(...Content...)` may appear only in text explicitly labeled superseded.

- [ ] **Step 6: Run isolated CLI black-box acceptance**

Build to a temporary path, create only a Codex root, and keep all state isolated:

```bash
APM_ACCEPT_ROOT=$(mktemp -d)
APM_ACCEPT_BIN="$APM_ACCEPT_ROOT/apm"
go build -o "$APM_ACCEPT_BIN" ./cmd/apm
mkdir -p "$APM_ACCEPT_ROOT/home/.codex"
APM_HOME="$APM_ACCEPT_ROOT/home" XDG_CACHE_HOME="$APM_ACCEPT_ROOT/cache" CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME= "$APM_ACCEPT_BIN" install foo-before-summary --agent codex >"$APM_ACCEPT_ROOT/first.out" 2>"$APM_ACCEPT_ROOT/first.err"
test "$(cat "$APM_ACCEPT_ROOT/first.out")" = "codex/skill/foo-before-summary@v0.0.1 installed"
test ! -s "$APM_ACCEPT_ROOT/first.err"
APM_HOME="$APM_ACCEPT_ROOT/home" XDG_CACHE_HOME="$APM_ACCEPT_ROOT/cache" CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME= "$APM_ACCEPT_BIN" install foo-before-summary=v0.0.1 --type skill --agent codex >"$APM_ACCEPT_ROOT/second.out" 2>"$APM_ACCEPT_ROOT/second.err"
test "$(cat "$APM_ACCEPT_ROOT/second.out")" = "codex/skill/foo-before-summary@v0.0.1 unchanged"
test ! -s "$APM_ACCEPT_ROOT/second.err"
test -f "$APM_ACCEPT_ROOT/home/.codex/skills/foo-before-summary/SKILL.md"
test -f "$APM_ACCEPT_ROOT/home/.apm/manifests/codex.json"
test ! -e "$APM_ACCEPT_ROOT/home/.apm/transactions/active.json"
```

Then run an invalid-version check and assert nonzero, empty stdout, one Error, and Usage:

```bash
set +e
APM_HOME="$APM_ACCEPT_ROOT/home" XDG_CACHE_HOME="$APM_ACCEPT_ROOT/cache" "$APM_ACCEPT_BIN" install foo-before-summary=0.0.1 --agent codex >"$APM_ACCEPT_ROOT/invalid.out" 2>"$APM_ACCEPT_ROOT/invalid.err"
INVALID_STATUS=$?
set -e
test "$INVALID_STATUS" -ne 0
test ! -s "$APM_ACCEPT_ROOT/invalid.out"
test "$(grep -c 'level=ERROR' "$APM_ACCEPT_ROOT/invalid.err")" -eq 1
grep -q 'must be vMAJOR.MINOR.PATCH' "$APM_ACCEPT_ROOT/invalid.err"
grep -q 'Usage:' "$APM_ACCEPT_ROOT/invalid.err"
```

Do not delete the acceptance root until assertions finish; afterward it may be removed as an explicit narrow temporary path.

- [ ] **Step 7: Add the single Changelog entry**

Run:

```bash
git log --reverse --format='- `%h %s`' de999d7^..HEAD
```

Copy every returned line verbatim into one new top `2026-08-11` “新增功能” entry. The entry text must state:

- end-to-end Install lifecycle and independent CLI port;
- canonical explicit versions, exact Name, and Type ambiguity;
- one-lock recovery/refresh/Cloud/Cache/Transaction sequence;
- installed/upgraded/downgraded/unchanged and fail-fast failed/unexecuted output;
- stale single-entry Remove and abort;
- Cache staging-root contract;
- Uninstall remains parse-only;
- the exact fresh verification and black-box evidence from Steps 5–6.

Do not claim a check passed unless its command exited 0.

- [ ] **Step 8: Commit Changelog and final audit**

```bash
git add docs/Changelog.md
git commit -m "docs(install): record completed feature"
git status --short
git log -20 --oneline
```

Expected: clean worktree after commit; recent history contains the design, plan, all Tasks 1–9 commits, documentation, and Changelog.

## Completion Checklist

- [ ] Cache Fetch returns the public staging root string and no `cache.Content` remains.
- [ ] Version Selector performs exact or maximum trusted SemVer selection without mutation.
- [ ] Transaction missing-target behavior Removes one stale manifest entry, cleans staging, returns `ErrStaleManifest`, and never creates a journal or calls Adapter.
- [ ] Install CLI accepts only canonical explicit versions and retains exact Name/Agent/Type values.
- [ ] Cross-Type Name ambiguity is detected before explicit version selection.
- [ ] Install holds one lock through recovery, refresh, Cloud, Cache, Config, and every serial transaction.
- [ ] Recovery Warn is synchronous under lock and complete; no-journal output is Debug-only.
- [ ] Same-version calls do not inspect the target, Fetch, or invoke TransactionManager.
- [ ] `all` is lexical and fail-fast; completed/failed/unexecuted outcomes are complete and deterministic.
- [ ] Only successful-Release batch errors render; fatal and Release failures suppress result output.
- [ ] CLI exact lines, writer failures, typed nils, repeated Execute, and one Error/usage are covered.
- [ ] Default wiring installs the bundled Skill and the second invocation is unchanged.
- [ ] ADR/C4/Feature/BUILD docs match production, Uninstall remains parse-only, and one Changelog entry records the completed request.
- [ ] Full test, race, vet, build, tidy diff, formatting, static scans, and isolated black-box checks pass from a clean worktree.

# Lifecycle List Implementation Plan

> **历史计划（Install/Uninstall 状态已被 Feature 010/011 supersede）：** 本计划中的 install/uninstall parse-only 描述只记录 Feature 009 的当时边界；两者现均已接线。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the command-specific List lifecycle so Cloud listings run without shared-state locking, installed listings recover and read manifests under the global lock, and the CLI renders deterministic structured results with well-defined partial failures.

**Architecture:** Keep Search and List behind separate CLI-owned ports and separate lifecycle services. List has a lock-free Cloud branch and a lock-protected installed branch; both return copied, sorted `cli.ListResult` values and never render directly. Tighten the two upstream contracts List depends on: Registry returns Agent IDs in lexical order, and Config accepts only canonical `vMAJOR.MINOR.PATCH` versions.

**Tech Stack:** Go 1.26.5, Cobra, standard `context`/`errors`/`fmt`/`io`/`log/slog`/`regexp`/`slices`/`sort`/`strings`, `github.com/Masterminds/semver/v3`, existing APM lock/transaction/adapter/config/cloud packages, Go `testing`.

## Global Constraints

- Follow [spec.md](spec.md) exactly; do not add install/uninstall lifecycle behavior, a generic command runner, JSON/table/pagination output, Cloud validation, or manifest migration.
- Default `list` must not call Locker, TransactionManager, AdapterRegistry, or ConfigRepository; it only calls Cloud.
- `list --installed` holds the global lock across Recover, synchronous recovery Warn, Registry refresh, target validation, and all Config reads; filtering, sorting, and rendering happen only after successful Release.
- Recovery success writes exactly one synchronous `slog.Warn` while the lock is still held with `operation`, `agent`, `power`, `version`, original `phase`, `direction`, and `actions`.
- Lifecycle, Registry, Config, Cloud, and render helpers never call `slog.Error` or print directly; CLIEntry remains the single Error/usage exit.
- Cloud versions are trusted canonical `vMAJOR.MINOR.PATCH`; Config rejects every other version form on read and write without modifying an invalid file.
- Cloud output is exactly `<type>/<name>@<version>\n`; installed output is always exactly `<agent>/<type>/<name>@<version>\n`.
- Only a successful-Release `--agent=all` read aggregation may return `ListResult` with a renderable `*cli.PartialListError`; every other error suppresses business stdout.
- Add concise, normative Chinese comments to important exported constructors, interfaces, results, partial-error types, and lifecycle methods.
- Preserve unrelated user changes. Add exactly one Changelog entry only after all behavior is complete and verified, using `~/.codex/docs/Changelog.md`.

## File and Responsibility Map

| File | Responsibility |
| --- | --- |
| `internal/config/json_repository.go` | Validate canonical versions before returning or writing PowerRefs. |
| `internal/config/json_repository{,_internal}_test.go` | Public and private Repository version/safety tests. |
| `internal/adapter/registry.go`, `registry_test.go` | Detect and return installed Agents in ID lexical order. |
| `internal/cli/list_contract.go`, `list_render.go` | CLI-owned List port/result/partial error and exact line rendering. |
| `internal/lifecycle/list_filter.go` | Pure type filtering, flattening, copying, and stable deterministic sorting. |
| `internal/lifecycle/list.go`, `list_installed.go` | Lock-free Cloud and lock-protected installed orchestration. |
| `internal/lifecycle/service.go`, `preflight.go`, `search.go` | Search-only service after aggregate interface removal. |
| `internal/cli/list.go`, `root.go` | Conditional flags, List invocation, partial rendering, and factory injection. |
| `cmd/apm/main.go` | Independent default Search and List dependency construction. |
| ADR/C4/overview/BUILD/Feature docs | Authoritative CLI, lock, order, version, output, and status documentation. |
| `docs/Changelog.md` | One completed behavior entry for this request. |

---

### Task 1: Enforce Canonical Versions in Config Repository

**Files:**
- Modify: `internal/config/errors.go`
- Modify: `internal/config/json_repository.go`
- Modify: `internal/config/json_repository_test.go`
- Modify: `internal/config/json_repository_internal_test.go`

**Interfaces:**
- Consumes: `config.ErrInvalidPower`, `domain.PowerGroups`, and existing `JSONRepository.List/Find/Set/Remove`.
- Produces: internal `validateCanonicalVersion(string) error`, `validatePowerGroups(string, domain.AgentID, domain.PowerGroups) error`, and `read(operation string, agent domain.AgentID)`.

- [ ] **Step 1: Normalize existing valid Config fixtures**

Change every valid test version such as `1.2.3`, `first`, `second`, and `1` to canonical values such as `v1.2.3`, `v1.0.0`, `v2.0.0`, and `v0.0.1`. Keep noncanonical strings only in new invalid tables. Change the existing `unchecked content` case: an empty version now expects `ErrInvalidPower` rather than a successful read.

```bash
rg -n '"version":"?(?:[0-9]|first|second|same|old|new)' internal/config --glob '*_test.go'
```

Expected after edits: matches are limited to deliberately invalid inputs and their assertions.

- [ ] **Step 2: Write failing public read and Set tests**

Add imports for `fmt` and `strconv`, then add this matrix for public reads:

```go
func TestJSONRepositoryRejectsNonCanonicalManifestVersions(t *testing.T) {
    tests := []string{
        "", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3",
        "v1.02.3", "v1.2.03", "v1.2.3-alpha", "v1.2.3+build",
        " v1.2.3", "v1.2.3 ", "v1.two.3", "v-1.2.3",
    }
    for _, version := range tests {
        t.Run(fmt.Sprintf("version_%q", version), func(t *testing.T) {
            home := t.TempDir()
            t.Setenv("APM_HOME", home)
            writeManifest(t, domain.AgentCodex, fmt.Sprintf(
                `{"agent":"codex","skills":[{"name":"demo","version":%q}]}`,
                version,
            ))
            _, err := config.NewJSONRepository().List(domain.AgentCodex)
            if !errors.Is(err, config.ErrInvalidPower) {
                t.Fatalf("List() error = %v, want ErrInvalidPower", err)
            }
            for _, want := range []string{"List", "codex", "skill", "entry 0", "demo", strconv.Quote(version)} {
                if !strings.Contains(err.Error(), want) {
                    t.Fatalf("List() error = %q, want %q", err, want)
                }
            }
        })
    }
}
```

Add one manifest with an invalid unrelated MCP entry and prove `Find(skill, ...)` and `Remove(skill, ...)` both fail closed. Add a blocking-path Set test proving validation precedes I/O:

```go
func TestJSONRepositorySetRejectsNonCanonicalVersionBeforeIO(t *testing.T) {
    home := t.TempDir()
    t.Setenv("APM_HOME", home)
    if err := os.WriteFile(filepath.Join(home, ".apm"), []byte("blocking"), 0o600); err != nil { t.Fatal(err) }
    err := config.NewJSONRepository().Set(domain.AgentCodex, domain.PowerRef{
        Type: domain.PowerTypeSkill, Name: "demo", Version: "1.2.3",
    })
    if !errors.Is(err, config.ErrInvalidPower) || !strings.Contains(err.Error(), `version "1.2.3"`) {
        t.Fatalf("Set() error = %v, want version ErrInvalidPower", err)
    }
}
```

Also add a valid table for `v0.0.0`, `v1.2.3`, and `v123.456.789`; each value must round-trip unchanged through Set and List.

- [ ] **Step 3: Write a failing private write safety test**

Import `bytes`. Arrange an existing valid target, capture its `os.FileInfo` and bytes, then call private write with an invalid version:

```go
target, err := paths.ManifestPath(domain.AgentCodex)
if err != nil { t.Fatal(err) }
if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil { t.Fatal(err) }
original := []byte("{\"agent\":\"codex\",\"skills\":[]}\n")
if err := os.WriteFile(target, original, 0o600); err != nil { t.Fatal(err) }
before, err := os.Stat(target)
if err != nil { t.Fatal(err) }

err = (&JSONRepository{}).write(domain.AgentCodex, domain.PowerGroups{
    domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2"}},
})
if !errors.Is(err, ErrInvalidPower) { t.Fatalf("write() error = %v, want ErrInvalidPower", err) }

after, statErr := os.Stat(target)
contents, readErr := os.ReadFile(target)
if statErr != nil || readErr != nil || !os.SameFile(before, after) || !bytes.Equal(contents, original) {
    t.Fatalf("invalid write changed target: stat=%v read=%v contents=%q", statErr, readErr, contents)
}
```

- [ ] **Step 4: Run focused tests and verify the validator is missing**

```bash
go test -count=1 ./internal/config -run 'Canonical|NonCanonical|InvalidVersion'
```

Expected: FAIL because noncanonical versions are still returned/written.

- [ ] **Step 5: Implement one canonical validator and full-group validation**

```go
var canonicalVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func validateCanonicalVersion(version string) error {
    if !canonicalVersionPattern.MatchString(version) {
        return fmt.Errorf("version %q must match vMAJOR.MINOR.PATCH: %w", version, ErrInvalidPower)
    }
    if _, err := semver.NewVersion(version); err != nil {
        return fmt.Errorf("parse version %q: %w", version, errors.Join(ErrInvalidPower, err))
    }
    return nil
}

func validatePowerGroups(operation string, agent domain.AgentID, groups domain.PowerGroups) error {
    for _, powerType := range []domain.PowerType{domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP} {
        for index, power := range groups[powerType] {
            if err := validateCanonicalVersion(power.Version); err != nil {
                return fmt.Errorf("%s agent %q type %q entry %d power %q version %q: %w",
                    operation, agent, powerType, index, power.Name, power.Version, err)
            }
        }
    }
    return nil
}
```

Import `regexp` and `github.com/Masterminds/semver/v3`.

- [ ] **Step 6: Validate caller-aware reads, Set before I/O, and write before touching disk**

Change private read to `read(operation string, agent domain.AgentID)`, pass `List`, `Find`, `Set`, or `Remove` from each public method, and after JSON conversion run:

```go
groups := manifestToPowerGroups(document)
if err := validatePowerGroups(operation, agent, groups); err != nil { return nil, err }
return groups, nil
```

In Set, after Type/Name checks and before read:

```go
if err := validateCanonicalVersion(power.Version); err != nil {
    return fmt.Errorf("Set agent %q power %q version %q: %w", agent, power.Name, power.Version, err)
}
```

At the first line of private write:

```go
if err := validatePowerGroups("write", agent, groups); err != nil { return err }
```

Update the exported sentinel comment so its current scope is accurate:

```go
// ErrInvalidPower 表示能力引用的类别、名称或版本无效。
var ErrInvalidPower = errors.New("invalid power reference")
```

- [ ] **Step 7: Verify and commit Config behavior**

```bash
gofmt -w internal/config/errors.go internal/config/json_repository.go internal/config/json_repository_test.go internal/config/json_repository_internal_test.go
go test -count=1 ./internal/config
go test -count=1 ./...
go vet ./internal/config
git diff --check
git add internal/config/errors.go internal/config/json_repository.go internal/config/json_repository_test.go internal/config/json_repository_internal_test.go
git commit -m "feat(config): require canonical power versions"
```

Expected: PASS; invalid versions wrap `ErrInvalidPower` and never modify disk.

---

### Task 2: Make Registry Refresh Order Match ADR-004

**Files:**
- Modify: `internal/adapter/registry.go`
- Modify: `internal/adapter/registry_test.go`

**Interfaces:**
- Consumes: `Registry.RefreshInstalled() (domain.AgentSnapshot, error)`.
- Produces: the same interface with Detect calls and snapshot ordered lexically by raw Agent ID, independent of registration order.

- [ ] **Step 1: Add a complete recording Adapter fake**

```go
type recordingDetectAdapter struct {
    agent domain.AgentID
    calls *[]domain.AgentID
    found bool
    err error
}
func (a recordingDetectAdapter) DetectAgent() (bool, error) {
    *a.calls = append(*a.calls, a.agent)
    return a.found, a.err
}
func (recordingDetectAdapter) TargetPath(domain.PowerRef) (string, error) { return "", nil }
func (recordingDetectAdapter) Install(domain.PowerRef, string) (InstallResult, error) {
    return InstallResult{}, nil
}
```

- [ ] **Step 2: Write failing order and error tests**

```go
func TestRegistryRefreshInstalledSortsDetectionAndSnapshotByAgentID(t *testing.T) {
    var calls []domain.AgentID
    registry := &Registry{
        ordered: []domain.AgentID{domain.AgentCodex, domain.AgentHermes, domain.AgentClaudeCode},
        byAgent: map[domain.AgentID]Adapter{},
    }
    for _, agent := range registry.ordered {
        registry.byAgent[agent] = recordingDetectAdapter{agent: agent, calls: &calls, found: agent != domain.AgentCodex}
    }
    snapshot, err := registry.RefreshInstalled()
    if err != nil { t.Fatal(err) }
    wantCalls := []domain.AgentID{domain.AgentClaudeCode, domain.AgentCodex, domain.AgentHermes}
    wantSnapshot := []domain.AgentID{domain.AgentClaudeCode, domain.AgentHermes}
    if !slices.Equal(calls, wantCalls) || !slices.Equal(snapshot.Ordered(), wantSnapshot) {
        t.Fatalf("calls/snapshot = %v/%v, want %v/%v", calls, snapshot.Ordered(), wantCalls, wantSnapshot)
    }
}
```

Add a failure at the lexically first Agent and assert later detectors are not called, the snapshot is zero, and the error includes that Agent and wraps the cause.

- [ ] **Step 3: Run focused tests and observe registration-order behavior**

```bash
go test -count=1 ./internal/adapter -run 'RegistryRefreshInstalled'
```

Expected: FAIL because Registry currently traverses `r.ordered` unchanged.

- [ ] **Step 4: Sort a copy before detection**

```go
func (r *Registry) RefreshInstalled() (domain.AgentSnapshot, error) {
    ordered := slices.Clone(r.ordered)
    slices.Sort(ordered)
    installed := make([]domain.AgentID, 0, len(ordered))
    for _, agent := range ordered {
        adapter := r.byAgent[agent]
        found, err := adapter.DetectAgent()
        if err != nil {
            return domain.AgentSnapshot{}, fmt.Errorf("refresh installed agent %q: %w", agent, err)
        }
        if found { installed = append(installed, agent) }
    }
    return domain.AgentSnapshot{Agents: installed}, nil
}
```

Import `slices`; do not mutate `r.ordered`.
Also update `RefreshInstalled`'s Chinese comment to state that detection and the returned snapshot use Agent ID lexical order.

- [ ] **Step 5: Verify and commit Registry behavior**

```bash
gofmt -w internal/adapter/registry.go internal/adapter/registry_test.go
go test -count=1 ./internal/adapter
go vet ./internal/adapter
git diff --check
git add internal/adapter/registry.go internal/adapter/registry_test.go
git commit -m "fix(adapter): order installed agent refresh"
```

Expected: PASS; detection and snapshot order now match ADR-004.

---

### Task 3: Define the List Port, Partial Error, and Exact Renderer

**Files:**
- Create: `internal/cli/list_contract.go`
- Create: `internal/cli/list_render.go`
- Create: `internal/cli/list_contract_test.go`

**Interfaces:**
- Consumes: `cli.Flags`, `domain.AgentID`, and `domain.PowerRef`.
- Produces: `ListLifecycle`, `ListLifecycleFactory`, `ListedPower`, `ListResult`, `AgentFailure`, `PartialListError`, and unexported `renderList(io.Writer, ListResult) error`.

- [ ] **Step 1: Write compile, exact-render, writer, and partial-error tests**

Use package `cli` so the renderer stays unexported:

```go
type listLifecycleCompileStub struct{}
func (listLifecycleCompileStub) List(context.Context, Flags) (ListResult, error) {
    return ListResult{}, nil
}
var _ ListLifecycle = listLifecycleCompileStub{}

func TestRenderListWritesExactCloudAndInstalledLines(t *testing.T) {
    result := ListResult{Powers: []ListedPower{
        {Power: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "cloud", Version: "v2.0.0"}},
        {Agent: domain.AgentCodex, Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "local", Version: "v1.0.0"}},
    }}
    var output strings.Builder
    if err := renderList(&output, result); err != nil { t.Fatal(err) }
    if got, want := output.String(), "mcp/cloud@v2.0.0\ncodex/skill/local@v1.0.0\n"; got != want {
        t.Fatalf("renderList() = %q, want %q", got, want)
    }
}

func TestPartialListErrorReportsAgentsAndUnwrapsCauses(t *testing.T) {
    first, second := errors.New("first"), errors.New("second")
    err := &PartialListError{Failures: []AgentFailure{
        {Agent: domain.AgentClaudeCode, Err: first},
        {Agent: domain.AgentCodex, Err: second},
    }}
    if !errors.Is(err, first) || !errors.Is(err, second) { t.Fatalf("missing cause: %v", err) }
    if got := err.Error(); !strings.Contains(got, "claude-code") || !strings.Contains(got, "codex") {
        t.Fatalf("Error() = %q, want both agents", got)
    }
}
```

Add empty-result and prefix writer cases. On a failed second write, expect `write list result` and preserve the first complete line.

- [ ] **Step 2: Run tests and verify the contract is undefined**

```bash
go test -count=1 ./internal/cli -run 'List|Partial'
```

Expected: FAIL to compile because the List contract and renderer do not exist.

- [ ] **Step 3: Implement CLI-owned List types with Chinese comments**

Create `list_contract.go`:

```go
package cli

import (
    "context"
    "fmt"
    "strings"
    "apm/internal/domain"
)

// ListedPower 表示 List 可渲染的一条 Power；Cloud 记录的 Agent 为空，已安装记录的 Agent 必填。
type ListedPower struct { Agent domain.AgentID; Power domain.PowerRef }

// ListResult 表示 CLI 可直接渲染的 List 业务结果。
type ListResult struct { Powers []ListedPower }

// AgentFailure 保存 all 模式中一个 Agent 的清单读取失败。
type AgentFailure struct { Agent domain.AgentID; Err error }

// PartialListError 表示 all 模式已成功解锁，成功记录可以渲染，但部分 Agent 读取失败。
type PartialListError struct { Failures []AgentFailure }

func (e *PartialListError) Error() string {
    parts := make([]string, 0, len(e.Failures))
    for _, failure := range e.Failures {
        parts = append(parts, fmt.Sprintf("agent %q: %v", failure.Agent, failure.Err))
    }
    return "list installed powers failed: " + strings.Join(parts, "; ")
}
func (e *PartialListError) Unwrap() []error {
    causes := make([]error, 0, len(e.Failures))
    for _, failure := range e.Failures { causes = append(causes, failure.Err) }
    return causes
}

// ListLifecycle 定义 list 命令唯一需要的生命周期入口。
type ListLifecycle interface { List(context.Context, Flags) (ListResult, error) }

// ListLifecycleFactory 在日志初始化后创建 List 生命周期。
type ListLifecycleFactory func() (ListLifecycle, error)
```

- [ ] **Step 4: Implement exact line rendering**

Create `list_render.go`:

```go
func renderList(writer io.Writer, result ListResult) error {
    for _, item := range result.Powers {
        prefix := ""
        if item.Agent != "" { prefix = string(item.Agent) + "/" }
        line := prefix + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version + "\n"
        if _, err := io.WriteString(writer, line); err != nil {
            return fmt.Errorf("write list result: %w", err)
        }
    }
    return nil
}
```

- [ ] **Step 5: Verify and commit the CLI contract**

```bash
gofmt -w internal/cli/list_contract.go internal/cli/list_render.go internal/cli/list_contract_test.go
go test -count=1 ./internal/cli -run 'List|Partial'
go vet ./internal/cli
git diff --check
git add internal/cli/list_contract.go internal/cli/list_render.go internal/cli/list_contract_test.go
git commit -m "feat(list): define CLI result contract"
```

---

### Task 4: Implement Pure List Filtering and Sorting

**Files:**
- Create: `internal/lifecycle/list_filter.go`
- Create: `internal/lifecycle/list_filter_test.go`

**Interfaces:**
- Consumes: `cli.Flags`, `cli.ListResult`, and canonical Cloud/Config `domain.PowerRef` values.
- Produces: internal `agentPowerGroups`, `makeCloudListResult(cli.Flags, []domain.PowerRef) cli.ListResult`, and `makeInstalledListResult(cli.Flags, []agentPowerGroups) cli.ListResult`.

- [ ] **Step 1: Write failing Cloud all-version tests**

```go
func TestMakeCloudListResultFiltersTypeKeepsAllVersionsAndSorts(t *testing.T) {
    refs := []domain.PowerRef{
        {Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.9.0"},
        {Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.10.0"},
        {Type: domain.PowerTypePlugin, Name: "alpha", Version: "v2.0.0"},
    }
    got := makeCloudListResult(cli.Flags{}, refs)
    want := cli.ListResult{Powers: []cli.ListedPower{
        {Power: refs[2]}, {Power: refs[1]}, {Power: refs[0]},
    }}
    if !reflect.DeepEqual(got, want) { t.Fatalf("got %#v, want %#v", got, want) }
}
```

Add exact Type filtering, complete-duplicate stability, non-nil empty result, and input-not-modified assertions.

- [ ] **Step 2: Write failing installed flatten/filter/sort tests**

```go
func TestMakeInstalledListResultAddsAgentFiltersAndSorts(t *testing.T) {
    input := []agentPowerGroups{
        {agent: domain.AgentCodex, groups: domain.PowerGroups{
            domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
        }},
        {agent: domain.AgentClaudeCode, groups: domain.PowerGroups{
            domain.PowerTypeSkill: {
                {Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.9.0"},
                {Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.10.0"},
            },
        }},
    }
    got := makeInstalledListResult(cli.Flags{Installed: true, Agent: "all"}, input)
    want := []cli.ListedPower{
        {Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][1]},
        {Agent: domain.AgentClaudeCode, Power: input[1].groups[domain.PowerTypeSkill][0]},
        {Agent: domain.AgentCodex, Power: input[0].groups[domain.PowerTypeSkill][0]},
    }
    if !reflect.DeepEqual(got.Powers, want) { t.Fatalf("powers = %#v, want %#v", got.Powers, want) }
}
```

Also assert every item has its Agent, exact Type filtering works, fully equal records remain stable, and input groups are unchanged.

- [ ] **Step 3: Run tests and verify helpers are missing**

```bash
go test -count=1 ./internal/lifecycle -run 'Make.*ListResult'
```

Expected: FAIL to compile.

- [ ] **Step 4: Implement copying, filtering, flattening, and stable ordering**

```go
type agentPowerGroups struct { agent domain.AgentID; groups domain.PowerGroups }

func makeCloudListResult(flags cli.Flags, refs []domain.PowerRef) cli.ListResult {
    powers := make([]cli.ListedPower, 0, len(refs))
    for _, ref := range refs {
        if flags.Type != "" && ref.Type != domain.PowerType(flags.Type) { continue }
        powers = append(powers, cli.ListedPower{Power: ref})
    }
    sortListedPowers(powers)
    return cli.ListResult{Powers: powers}
}

func makeInstalledListResult(flags cli.Flags, inputs []agentPowerGroups) cli.ListResult {
    powers := make([]cli.ListedPower, 0)
    for _, input := range inputs {
        for _, refs := range input.groups {
            for _, ref := range refs {
                if flags.Type != "" && ref.Type != domain.PowerType(flags.Type) { continue }
                powers = append(powers, cli.ListedPower{Agent: input.agent, Power: ref})
            }
        }
    }
    sortListedPowers(powers)
    return cli.ListResult{Powers: powers}
}

func sortListedPowers(powers []cli.ListedPower) {
    sort.SliceStable(powers, func(i, j int) bool {
        left, right := powers[i], powers[j]
        if left.Agent != right.Agent { return left.Agent < right.Agent }
        if left.Power.Type != right.Power.Type { return left.Power.Type < right.Power.Type }
        if left.Power.Name != right.Power.Name { return left.Power.Name < right.Power.Name }
        if left.Power.Version == right.Power.Version { return false }
        return semver.MustParse(left.Power.Version).GreaterThan(semver.MustParse(right.Power.Version))
    })
}
```

Do not mutate or sort inputs. Always allocate result slices with `make(..., 0, ...)`.

- [ ] **Step 5: Verify and commit result computation**

```bash
gofmt -w internal/lifecycle/list_filter.go internal/lifecycle/list_filter_test.go
go test -count=1 ./internal/lifecycle -run 'Make.*ListResult'
go vet ./internal/lifecycle
git diff --check
git add internal/lifecycle/list_filter.go internal/lifecycle/list_filter_test.go
git commit -m "feat(list): filter and sort power results"
```

---

### Task 5: Implement Independent Search and List Lifecycle Services

**Files:**
- Modify: `internal/lifecycle/service.go`
- Modify: `internal/lifecycle/preflight.go`
- Modify: `internal/lifecycle/search.go`
- Modify: `internal/lifecycle/search_test.go`
- Rewrite: `internal/lifecycle/list.go`
- Create: `internal/lifecycle/list_installed.go`
- Create: `internal/lifecycle/list_test.go`
- Modify: `cmd/apm/main.go`

**Interfaces:**
- Consumes: Tasks 1–4 contracts plus `Locker`, `TransactionManager`, `CloudClient`, `AdapterRegistry`, and `ConfigRepository`.
- Produces: `NewSearch(...) (*SearchService, error)`, `NewList(...) (*ListService, error)`, and `(*ListService).List(context.Context, cli.Flags) (cli.ListResult, error)`; removes aggregate `Lifecycle` and Search's no-op methods.

- [ ] **Step 1: Change Search tests to the command-specific constructor**

Update `search_test.go` to expect `*SearchService` and call `NewSearch`. Keep every order/log/error assertion unchanged. Rename `TestNewRejectsNilSearchDependencies` to `TestNewSearchRejectsNilDependencies` and expect messages beginning `create Search lifecycle`.

```bash
go test -count=1 ./internal/lifecycle -run '^TestSearch|^TestNewSearch'
```

Expected: FAIL because `SearchService` and `NewSearch` do not exist.

- [ ] **Step 2: Make the existing service Search-only**

Rewrite `service.go`:

```go
// Package lifecycle 提供命令专属的 Power 生命周期服务。
package lifecycle

type SearchService struct {
    locker Locker
    transactions TransactionManager
    cloud CloudClient
}

// NewSearch 创建具有恢复、锁和 Cloud 依赖的 Search 生命周期。
func NewSearch(locker Locker, transactions TransactionManager, cloud CloudClient) (*SearchService, error) {
    switch {
    case locker == nil:
        return nil, errors.New("create Search lifecycle with nil locker")
    case transactions == nil:
        return nil, errors.New("create Search lifecycle with nil transaction manager")
    case cloud == nil:
        return nil, errors.New("create Search lifecycle with nil Cloud client")
    }
    return &SearchService{locker: locker, transactions: transactions, cloud: cloud}, nil
}

var _ cli.SearchLifecycle = (*SearchService)(nil)
```

Delete lifecycle `ListResult`, aggregate `Lifecycle`, `New`, and Service `List/Install/Uninstall`. Change receivers in `preflight.go` and `search.go` from `*Service` to `*SearchService` without changing flow.
Update `newSearchLifecycle` in `cmd/apm/main.go` to return `lifecycle.NewSearch(...)` in the same change, so no intermediate commit references the deleted constructor. Do not otherwise change CLI construction in this task.

- [ ] **Step 3: Prove the Search refactor is behavior-preserving**

```bash
gofmt -w internal/lifecycle/service.go internal/lifecycle/preflight.go internal/lifecycle/search.go internal/lifecycle/search_test.go
go test -count=1 ./internal/lifecycle -run '^TestSearch|^TestNewSearch'
```

Expected: PASS with unchanged Acquire/Recover/Warn/Release/Cloud order.

- [ ] **Step 4: Define List-specific dependency fakes**

In `list_test.go`, create uniquely named fakes implementing every interface method. Each method appends a call such as `acquire`, `recover`, `refresh`, `config:<agent>`, `release`, or `cloud-list`. Use a `listFixture` holding results/errors plus optional `beforeRefresh` and `beforeRelease` callbacks.

```go
type listFixture struct {
    acquireErr, recoverErr, releaseErr, refreshErr, cloudErr error
    recovery transaction.RecoveryResult
    snapshot domain.AgentSnapshot
    groups map[domain.AgentID]domain.PowerGroups
    configErrs map[domain.AgentID]error
    cloudRefs []domain.PowerRef
    beforeRefresh, beforeRelease func()
}
```

Implement unused fake methods (`Resolve`, `Find`, `Set`, `Remove`, transaction Install/Uninstall, Cloud Get) with correct signatures and no side effects.

- [ ] **Step 5: Write failing constructor and lock-free Cloud tests**

Cover each nil dependency, exact Cloud result, and Cloud cause. Prove no shared-state fake is called:

```go
func TestListCloudDoesNotTouchSharedState(t *testing.T) {
    calls := []string{}
    service := newListService(t, &calls, listFixture{cloudRefs: []domain.PowerRef{{
        Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0",
    }}})
    result, err := service.List(context.Background(), cli.Flags{})
    if err != nil { t.Fatal(err) }
    if want := []string{"cloud-list"}; !reflect.DeepEqual(calls, want) {
        t.Fatalf("calls = %v, want %v", calls, want)
    }
    if len(result.Powers) != 1 || result.Powers[0].Agent != "" {
        t.Fatalf("result = %#v, want one Cloud item", result)
    }
}
```

- [ ] **Step 6: Write failing installed happy-path and real-time Warn test**

Use a complete recovery result and sorted snapshot `[claude-code, codex]`. Both fake Registry and fake Unlock callbacks must assert `level=WARN` already exists before appending their calls:

```go
func TestListInstalledRecoversWarnsRefreshesReadsThenReleases(t *testing.T) {
    // Install logging.NewLogger and configure recovery fields/actions.
    result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "all"})
    if err != nil { t.Fatal(err) }
    wantCalls := []string{"acquire", "recover", "refresh", "config:claude-code", "config:codex", "release"}
    if !reflect.DeepEqual(calls, wantCalls) { t.Fatalf("calls = %v, want %v", calls, wantCalls) }
    if len(result.Powers) != 2 || result.Powers[0].Agent != domain.AgentClaudeCode || result.Powers[1].Agent != domain.AgentCodex {
        t.Fatalf("result = %#v, want ordered claude-code/codex powers", result)
    }
    for _, want := range []string{"level=WARN", "operation=install", "agent=codex", "power=demo", "version=v2.0.0", "phase=publishing", "direction=commit", "actions="} {
        if !strings.Contains(stderr.String(), want) { t.Fatalf("stderr = %q, want %q", stderr.String(), want) }
    }
}
```

- [ ] **Step 7: Write the failing fatal-path table**

Cover Acquire, Recover, Refresh, unavailable concrete Agent, concrete Config read, and Release. For every Acquire-success case expect one Release. For business+Release combinations, assert all causes with `errors.Is`; assert result has no powers and later dependencies were not called.

```go
tests := []struct {
    name string
    flags cli.Flags
    fixture listFixture
    wantCalls []string
    wantErrs []error
}{
    {name: "acquire", flags: installedAll, fixture: listFixture{acquireErr: acquireErr}, wantCalls: []string{"acquire"}, wantErrs: []error{acquireErr}},
    {name: "recover and release", flags: installedAll, fixture: listFixture{recoverErr: recoverErr, releaseErr: releaseErr}, wantCalls: []string{"acquire", "recover", "release"}, wantErrs: []error{recoverErr, releaseErr}},
    {name: "refresh", flags: installedAll, fixture: listFixture{refreshErr: refreshErr}, wantCalls: []string{"acquire", "recover", "refresh", "release"}, wantErrs: []error{refreshErr}},
}
```

- [ ] **Step 8: Write failing empty/partial/partial-plus-release tests**

Add explicit assertions:

```go
// Empty snapshot: acquire, recover, refresh, release; non-nil empty result; nil error.
// Partial: claude-code Config failure, codex success, Release succeeds;
//          result contains codex and errors.As(err, *cli.PartialListError) succeeds.
// Partial + Release failure: all Agent/Release causes survive, result is empty,
//          and errors.As(err, *cli.PartialListError) is false.
```

Assert Agent failures remain in lexical traversal order and a later Agent is read after an earlier failure.

- [ ] **Step 9: Write no-journal normal/verbose tests**

In normal logging, installed no-journal execution produces no stdout/stderr. In verbose logging it emits `level=DEBUG msg="no active transaction before installed list"`. Default Cloud List must produce no recovery Debug and no recovery call under either logger.

- [ ] **Step 10: Run List tests and observe missing service**

```bash
go test -count=1 ./internal/lifecycle -run '^TestList|^TestNewList'
```

Expected: FAIL because `NewList` and installed orchestration do not exist.

- [ ] **Step 11: Implement List construction and Cloud dispatch**

Rewrite `list.go`:

```go
type ListService struct {
    locker Locker
    transactions TransactionManager
    cloud CloudClient
    registry AdapterRegistry
    config ConfigRepository
}

// NewList 创建 List 生命周期；默认 Cloud 分支不得调用共享状态依赖。
func NewList(locker Locker, transactions TransactionManager, cloud CloudClient, registry AdapterRegistry, config ConfigRepository) (*ListService, error) {
    switch {
    case locker == nil: return nil, errors.New("create List lifecycle with nil locker")
    case transactions == nil: return nil, errors.New("create List lifecycle with nil transaction manager")
    case cloud == nil: return nil, errors.New("create List lifecycle with nil Cloud client")
    case registry == nil: return nil, errors.New("create List lifecycle with nil adapter registry")
    case config == nil: return nil, errors.New("create List lifecycle with nil config repository")
    }
    return &ListService{locker: locker, transactions: transactions, cloud: cloud, registry: registry, config: config}, nil
}

var _ cli.ListLifecycle = (*ListService)(nil)

// List 根据 Installed 选择无锁 Cloud 查询或锁内本地清单查询，并只返回结构化结果。
func (s *ListService) List(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
    if flags.Installed { return s.listInstalled(ctx, flags) }
    refs, err := s.cloud.List()
    if err != nil { return cli.ListResult{}, fmt.Errorf("list Cloud powers: %w", err) }
    return makeCloudListResult(flags, refs), nil
}
```

- [ ] **Step 12: Implement installed target selection and release helper**

In `list_installed.go`, add:

```go
func installedListAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
    target := domain.AgentID(flags.Agent)
    if target == domain.AllAgents { return snapshot.Ordered(), nil }
    if !snapshot.Contains(target) {
        return nil, fmt.Errorf("list installed powers for agent %q: unsupported or not installed", target)
    }
    return []domain.AgentID{target}, nil
}

func releaseInstalledList(unlock lock.Unlock, businessErr error) error {
    if err := unlock.Release(); err != nil {
        return errors.Join(businessErr, fmt.Errorf("release installed list lock: %w", err))
    }
    return businessErr
}
```

- [ ] **Step 13: Implement installed orchestration with an explicit successful Release gate**

```go
func (s *ListService) listInstalled(ctx context.Context, flags cli.Flags) (cli.ListResult, error) {
    unlock, err := s.locker.Acquire()
    if err != nil { return cli.ListResult{}, fmt.Errorf("acquire installed list lock: %w", err) }
    recovery, err := s.transactions.Recover(ctx)
    if err != nil {
        return cli.ListResult{}, releaseInstalledList(unlock, fmt.Errorf("recover transaction before installed list: %w", err))
    }
    logInstalledListRecovery(recovery)
    snapshot, err := s.registry.RefreshInstalled()
    if err != nil {
        return cli.ListResult{}, releaseInstalledList(unlock, fmt.Errorf("refresh installed agents for list: %w", err))
    }
    agents, err := installedListAgents(flags, snapshot)
    if err != nil { return cli.ListResult{}, releaseInstalledList(unlock, err) }

    inputs := make([]agentPowerGroups, 0, len(agents))
    failures := make([]cli.AgentFailure, 0)
    for _, agent := range agents {
        groups, listErr := s.config.List(agent)
        if listErr != nil {
            wrapped := fmt.Errorf("list installed powers for agent %q: %w", agent, listErr)
            if domain.AgentID(flags.Agent) != domain.AllAgents {
                return cli.ListResult{}, releaseInstalledList(unlock, wrapped)
            }
            failures = append(failures, cli.AgentFailure{Agent: agent, Err: wrapped})
            continue
        }
        inputs = append(inputs, agentPowerGroups{agent: agent, groups: groups})
    }
    if releaseErr := unlock.Release(); releaseErr != nil {
        causes := []error{fmt.Errorf("release installed list lock: %w", releaseErr)}
        for _, failure := range failures { causes = append(causes, failure.Err) }
        return cli.ListResult{}, errors.Join(causes...)
    }
    result := makeInstalledListResult(flags, inputs)
    if len(failures) != 0 { return result, &cli.PartialListError{Failures: failures} }
    return result, nil
}
```

- [ ] **Step 14: Implement List-specific recovery logging**

Do not call Search preflight:

```go
func logInstalledListRecovery(result transaction.RecoveryResult) {
    if !result.Recovered {
        slog.Debug("no active transaction before installed list")
        return
    }
    slog.Warn("transaction recovered",
        slog.String("operation", string(result.Operation)),
        logging.Agent(result.Agent), logging.Power(result.Power.Name),
        logging.Version(result.Power.Version), logging.Phase(string(result.OriginalPhase)),
        slog.String("direction", string(result.Direction)), slog.Any("actions", result.Actions))
}
```

- [ ] **Step 15: Run lifecycle tests and static scans**

```bash
gofmt -w internal/lifecycle/service.go internal/lifecycle/preflight.go internal/lifecycle/search.go internal/lifecycle/search_test.go internal/lifecycle/list.go internal/lifecycle/list_installed.go internal/lifecycle/list_test.go
go test -count=1 ./internal/lifecycle
go test -count=1 ./...
go vet ./internal/lifecycle
test -z "$(rg -n 'type Lifecycle interface|func \(s \*SearchService\) (List|Install|Uninstall)' internal/lifecycle --glob '*.go')"
test -z "$(rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle --glob '*.go')"
git diff --check
```

Expected: PASS; Search is unchanged, Cloud List calls only Cloud, and installed List satisfies all lock/log/release/partial invariants.

- [ ] **Step 16: Commit lifecycle orchestration**

```bash
git add internal/lifecycle cmd/apm/main.go
git commit -m "feat(list): implement lifecycle orchestration"
```

---

### Task 6: Wire List CLI and Default Application Dependencies

**Files:**
- Modify: `internal/cli/list.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `internal/cli/search_test.go`
- Create: `internal/cli/list_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `ListLifecycleFactory`, `ListResult`, `*PartialListError`, `lifecycle.NewSearch`, and `lifecycle.NewList`.
- Produces: CLIEntry with independent Search/List factories, conditional `--agent`, exact rendering, and `newListLifecycle() (cli.ListLifecycle, error)`.

- [ ] **Step 1: Add a recording List fake and failing parameter table**

```go
type recordingListLifecycle struct {
    flags cli.Flags
    result cli.ListResult
    err error
    calls int
}
func (l *recordingListLifecycle) List(_ context.Context, flags cli.Flags) (cli.ListResult, error) {
    l.calls++
    l.flags = flags
    return l.result, l.err
}
```

Add a test entry helper that always supplies a Search fake which fails the test if List accidentally dispatches to Search:

```go
func newListEntry(t *testing.T, stdout, stderr io.Writer, factory cli.ListLifecycleFactory) *cli.CLIEntry {
    t.Helper()
    original := slog.Default()
    t.Cleanup(func() { slog.SetDefault(original) })
    return cli.New(stdout, stderr, func(verbose bool) {
        slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
    }, func() (cli.SearchLifecycle, error) {
        t.Fatal("List dispatched to Search factory")
        return nil, nil
    }, factory)
}
```

Test these exact cases:

```go
[]struct{ args []string; wantErr string }{
    {args: []string{"list"}},
    {args: []string{"list", "--type=skill"}},
    {args: []string{"list", "--agent=all"}, wantErr: "--agent requires --installed"},
    {args: []string{"list", "--agent="}, wantErr: "--agent requires --installed"},
    {args: []string{"list", "--installed"}, wantErr: "--agent is required"},
    {args: []string{"list", "--installed", "--agent= \t "}, wantErr: "--agent is required"},
}
```

Invalid cases must not call the factory/lifecycle; stdout is empty; stderr has one Error and Usage.

- [ ] **Step 2: Add failing success, fatal, writer, and partial rendering tests**

Cover exact Cloud lines, Agent-prefixed concrete/all lines, empty output, fatal suppression, prefix writer preservation, and:

```go
func TestListPartialFailureRendersSuccessesThenReturnsError(t *testing.T) {
    readErr := errors.New("manifest unreadable")
    lifecycle := &recordingListLifecycle{
        result: cli.ListResult{Powers: []cli.ListedPower{{
            Agent: domain.AgentCodex,
            Power: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
        }}},
        err: &cli.PartialListError{Failures: []cli.AgentFailure{{Agent: domain.AgentClaudeCode, Err: readErr}}},
    }
    var stdout, stderr bytes.Buffer
    entry := newListEntry(t, &stdout, &stderr, func() (cli.ListLifecycle, error) { return lifecycle, nil })
    err := entry.Execute([]string{"list", "--installed", "--agent=all"})
    if !errors.Is(err, readErr) { t.Fatalf("Execute() error = %v, want %v", err, readErr) }
    if got, want := stdout.String(), "codex/skill/demo@v1.0.0\n"; got != want { t.Fatalf("stdout = %q, want %q", got, want) }
    if got := stderr.String(); strings.Count(got, "level=ERROR") != 1 || !strings.Contains(got, "Usage:") {
        t.Fatalf("stderr = %q, want one Error and Usage", got)
    }
}
```

Force a writer error during partial rendering and assert both writer and Agent causes survive the returned join.

- [ ] **Step 3: Add failing factory/help/repeated-execution tests**

Assert factory runs after verbose logger setup; nil factory, nil service, and construction error are contextual; List help explains `--agent` requires `--installed`; repeated Execute does not leak Installed/Agent/Type/logger/service. Update shared CLI tests so bare `list` is legal and missing Agent is only an installed error.

- [ ] **Step 4: Run CLI tests and observe parse-only behavior**

```bash
go test -count=1 ./internal/cli -run 'List|Repeated|ParsesCommands'
```

Expected: FAIL because List has no factory/lifecycle/rendering and bare List still requires Agent.

- [ ] **Step 5: Implement conditional List validation**

```go
func validateListFlags(command *cobra.Command, flags Flags) error {
    if !flags.Installed {
        if command.Flags().Changed("agent") { return errors.New("--agent requires --installed") }
        return validatePowerType(flags)
    }
    return validateCommon(flags)
}
```

Use Cobra Changed so explicit `--agent=` is rejected in Cloud mode. Keep exact Agent values after only checking installed values are nonblank.

- [ ] **Step 6: Invoke lifecycle and only render marked partial errors**

Change `newListCommand` to accept `ListLifecycleFactory`. After validation/logging, reject nil factory/service and wrap factory errors with `create List lifecycle`. Then:

```go
result, err := service.List(command.Context(), flags)
if err == nil { return renderList(command.OutOrStdout(), result) }
var partial *PartialListError
if !errors.As(err, &partial) { return err }
if renderErr := renderList(command.OutOrStdout(), result); renderErr != nil {
    return errors.Join(err, renderErr)
}
return err
```

Register `--agent` with help text equivalent to `target agent ID or all; requires --installed`, and keep `--installed`/`--type` descriptions specific to their behavior.

- [ ] **Step 7: Inject the independent List factory through CLIEntry**

Extend `CLIEntry`, `New`, `newRootCommand`, and `root.AddCommand`:

```go
func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc,
    newSearch SearchLifecycleFactory, newList ListLifecycleFactory) *CLIEntry
```

Update every CLI test constructor with an empty List fake. Search-only helpers pass a List factory that fails the test if called. Keep preparse logging and the single Execute error exit unchanged.

- [ ] **Step 8: Verify all CLI tests**

```bash
gofmt -w internal/cli/list.go internal/cli/root.go internal/cli/list_test.go internal/cli/cli_test.go internal/cli/search_test.go
go test -count=1 ./internal/cli
go vet ./internal/cli
git diff --check
```

Expected: PASS; Search still rejects `--agent` as unknown, while List accepts it only with installed.

- [ ] **Step 9: Add failing default factory integration tests**

Update Search expectations for `NewSearch`, then add:

```go
func TestNewListLifecycleListsBundledCloud(t *testing.T) {
    // Isolate APM_HOME/XDG_CACHE_HOME and chdir to repository root.
    service, err := newListLifecycle()
    if err != nil { t.Fatal(err) }
    result, err := service.List(context.Background(), cli.Flags{Type: "skill"})
    if err != nil { t.Fatal(err) }
    want := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "foo-before-summary", Version: "v0.0.1"}
    found := false
    for _, item := range result.Powers { if item.Agent == "" && item.Power == want { found = true } }
    if !found { t.Fatalf("result = %#v, want bundled %#v", result, want) }
}

func TestNewListLifecycleListsInstalledManifest(t *testing.T) {
    home := t.TempDir()
    t.Setenv("APM_HOME", home)
    t.Setenv("XDG_CACHE_HOME", t.TempDir())
    t.Setenv("CODEX_HOME", "")
    t.Setenv("CLAUDE_CONFIG_DIR", "")
    t.Setenv("HERMES_HOME", "")
    if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil { t.Fatal(err) }
    ref := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}
    if err := config.NewJSONRepository().Set(domain.AgentCodex, ref); err != nil { t.Fatal(err) }
    service, err := newListLifecycle()
    if err != nil { t.Fatal(err) }
    result, err := service.List(context.Background(), cli.Flags{Installed: true, Agent: "codex"})
    if err != nil { t.Fatal(err) }
    want := cli.ListedPower{Agent: domain.AgentCodex, Power: ref}
    if len(result.Powers) != 1 || result.Powers[0] != want { t.Fatalf("result = %#v, want %#v", result, want) }
}
```

- [ ] **Step 10: Implement independent default List dependencies**

Call CLI with both factories; Search already uses `lifecycle.NewSearch` from Task 5:

```go
entry := cli.New(os.Stdout, os.Stderr, logging.SetupLogging, newSearchLifecycle, newListLifecycle)
```

Extract only a small `newLocalCloudClient` path helper for Search/List. Do not create a lifecycle container or runner. List must share one Repository between recovery manager and reads:

```go
// newListLifecycle 创建 List 命令所需的默认生命周期依赖。
func newListLifecycle() (cli.ListLifecycle, error) {
    locker, err := lock.NewDefault()
    if err != nil { return nil, fmt.Errorf("create default lock: %w", err) }
    repository := config.NewJSONRepository()
    transactions, err := transaction.NewManager(repository)
    if err != nil { return nil, fmt.Errorf("create transaction manager: %w", err) }
    cloudClient, err := newLocalCloudClient()
    if err != nil { return nil, err }
    return lifecycle.NewList(locker, transactions, cloudClient, adapter.NewRegistry(), repository)
}
```

Update `internal/cli/root.go`'s package and constructor comments so they say CLI invokes and renders both Search and List; do not claim that only Search is wired.

- [ ] **Step 11: Run integration and full Go verification**

```bash
gofmt -w cmd/apm/main.go cmd/apm/main_test.go
go test -count=1 ./cmd/apm ./internal/cli ./internal/lifecycle ./internal/config ./internal/adapter
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: PASS; bundled Cloud and isolated installed reads use their intended boundaries.

- [ ] **Step 12: Commit CLI and application wiring**

```bash
git add internal/cli cmd/apm
git commit -m "feat(list): render lifecycle results"
```

---

### Task 7: Align ADRs, Architecture Docs, Changelog, and Acceptance

**Files:**
- Modify: `ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md`
- Modify: `ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md`
- Modify: `ivory-tower/adr/ADR-004-discover-installed-agents-before-local-operations.md`
- Modify: `ivory-tower/adr/ADR-007-use-stateless-config-repository.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/{core-lifecycle,adapter-registry,lock-manager}.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/list.mmd`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/features/005-agent-power-manifest-config/{spec,feature-overview}.md`
- Modify: `ivory-tower/features/008-lifecycle-search/{spec,feature-overview}.md`
- Modify: `ivory-tower/features/009-lifecycle-list/feature-overview.md`
- Modify: `BUILD.md`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: completed Tasks 1–6, accepted Feature 009 spec, ADR IDs, and Changelog template.
- Produces: one cross-referenced current description and final verification evidence.

- [ ] **Step 1: Revise ADR-002 syntax, output, and partial semantics**

Use exactly:

```text
apm list [--type=<skill|plugin|mcp>]
apm list --installed --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

State default List rejects an explicit Agent with `--agent requires --installed`; installed requires Agent; Cloud shows all versions; installed always prefixes Agent; `all` partial reads output successes then return nonzero.

- [ ] **Step 2: Revise ADR-003 and ADR-004 execution contracts**

ADR-003 must state:

```text
Cloud List: no lock, no Recover, Cloud query → result computation → CLI render.
Installed List: Acquire → Recover → lock-held Warn → RefreshInstalled → all Config reads
                → successful Release → result computation → CLI render.
```

Document the successful-Release-only partial marker and no-output Release-failure rule. ADR-004 states Registry sorts a copied registered-Agent list before Detect and returns the successful snapshot in the same lexical order.

- [ ] **Step 3: Revise ADR-007 canonical Config contract**

Replace “non-empty, no SemVer validation” with exact `vMAJOR.MINOR.PATCH`. Record full-manifest validation on List/Find/Remove/Set read, pre-I/O Set input validation, private-write validation, `ErrInvalidPower` context, and deliberate absence of migration or cleanup.

- [ ] **Step 4: Update C4 and lifecycle diagrams**

Show separate Search/List ports and services; remove aggregate Lifecycle and empty methods. List diagram has no lock/recovery on Cloud and one lock region around installed recovery/refresh/reads. Move every print after Release and show partial success/error. Registry and Lock diagrams state lexical order and mode-specific List lock scope.

- [ ] **Step 5: Update overview, historical feature notes, and BUILD**

Project overview/DSL say Search and List are implemented, install/uninstall parse-only. Add a Feature 005 supersession note for canonical versions. Mark Feature 008's “List remains parse-only” as a historical boundary superseded by Feature 009. BUILD includes:

```bash
./apm list
./apm list --type skill
./apm list --installed --agent codex
./apm list --installed --agent all --type skill
```

Document both exact output forms and invalid `list --agent all`.

- [ ] **Step 6: Scan docs for contradictions and required references**

```bash
rg -n 'list.*仅.*解析|list.*parse-only|list --agent=<agent-id\|all>.*\[--installed\]|四.*命令.*入口获取|List.*打印.*释放锁|版本.*不验证|内置配置顺序' ivory-tower BUILD.md
rg -n 'ADR-002|ADR-003|ADR-004|ADR-007|vMAJOR\.MINOR\.PATCH|PartialListError|--agent requires --installed' ivory-tower/features/009-lifecycle-list ivory-tower/adr
```

Expected: no unmarked current contradiction; required decisions and links exist.

- [ ] **Step 7: Read Changelog template and add one completed-request entry**

```bash
sed -n '1,160p' ~/.codex/docs/Changelog.md
git log --oneline 71924a6..HEAD
```

Add one reverse-chronological `2026-08-11` `新增功能` entry. Include design `316a367`, this plan commit, and all Feature 009 implementation/fix/docs commits. Describe no-lock Cloud, locked installed recovery/reads, exact output, partial errors, Registry order, canonical Config versions, no migration, and install/uninstall limits. Add verification evidence only after Step 9 passes.

- [ ] **Step 8: Run complete static and Go verification**

```bash
go mod tidy
git diff --exit-code -- go.mod go.sum
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
test -z "$(rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle internal/config internal/adapter --glob '*.go')"
```

Expected: all commands exit 0; tidy changes no module file; direct Error/print scan is empty.

- [ ] **Step 9: Run CLI black-box acceptance in isolated directories**

```bash
apm_test_bin="$(mktemp)"
apm_test_home="$(mktemp -d)"
apm_test_cache="$(mktemp -d)"
go build -o "$apm_test_bin" ./cmd/apm

APM_HOME="$apm_test_home" XDG_CACHE_HOME="$apm_test_cache" "$apm_test_bin" list --type skill > /tmp/apm-list-cloud.out 2> /tmp/apm-list-cloud.err
test -s /tmp/apm-list-cloud.out
test ! -s /tmp/apm-list-cloud.err
rg -n '^skill/[^/@]+@v[0-9]+\.[0-9]+\.[0-9]+$' /tmp/apm-list-cloud.out

set +e
APM_HOME="$apm_test_home" XDG_CACHE_HOME="$apm_test_cache" "$apm_test_bin" list --agent all > /tmp/apm-list-invalid.out 2> /tmp/apm-list-invalid.err
invalid_status=$?
set -e
test "$invalid_status" -ne 0
test ! -s /tmp/apm-list-invalid.out
test "$(rg -c 'level=ERROR' /tmp/apm-list-invalid.err)" -eq 1
rg -n -- '--agent requires --installed|Usage:' /tmp/apm-list-invalid.err
```

The exact non-empty installed output is already proven by `TestNewListLifecycleListsInstalledManifest`. For an additional black-box factory check without writing a manifest, run:

```bash
mkdir -p "$apm_test_home/.codex"
APM_HOME="$apm_test_home" XDG_CACHE_HOME="$apm_test_cache" \
  CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME= \
  "$apm_test_bin" list --installed --agent codex > /tmp/apm-list-installed.out 2> /tmp/apm-list-installed.err
test ! -s /tmp/apm-list-installed.out
test ! -s /tmp/apm-list-installed.err
```

Remove only the explicit temp paths and `/tmp/apm-list-*.{out,err}` files created here.

- [ ] **Step 10: Record evidence and commit docs/Changelog**

```bash
git add ivory-tower/adr ivory-tower/c4/components ivory-tower/project-overview.md \
  ivory-tower/features/005-agent-power-manifest-config \
  ivory-tower/features/008-lifecycle-search \
  ivory-tower/features/009-lifecycle-list/feature-overview.md \
  BUILD.md docs/Changelog.md
git commit -m "docs(list): record lifecycle implementation"
```

- [ ] **Step 11: Verify final clean state**

```bash
git status --short
git log --oneline 71924a6..HEAD
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: clean status, all requested commits visible, and all checks pass.

---

## Completion Checklist

- [ ] Bare Cloud List accepts optional Type, rejects any explicitly supplied Agent, lists all versions, and never touches shared-state dependencies.
- [ ] Installed List requires Agent and holds one lock across Recover/Warn/Refresh/Config reads, then releases before computation/rendering.
- [ ] Recovery Warn is observable before Registry and Release with every required field.
- [ ] `all` uses lexical Agent order, continues Config failures, and only exposes a renderable partial error after successful Release.
- [ ] Fatal or Release errors suppress business output and preserve joined causes.
- [ ] Cloud and installed renderers produce only the exact accepted line formats.
- [ ] Registry detection/snapshot order matches ADR-004.
- [ ] Config returns/writes only canonical `vMAJOR.MINOR.PATCH` and never modifies invalid existing manifests.
- [ ] Aggregate Lifecycle and Search no-op methods are gone; install/uninstall remain parse-only.
- [ ] ADR-002/003/004/007, C4, overview, feature notes, BUILD, and Changelog agree with code.
- [ ] Focused/full tests, vet, build, gofmt, tidy, diff, static scans, and black-box acceptance all pass.

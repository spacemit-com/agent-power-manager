# Lifecycle Uninstall Implementation Plan

> **已执行的历史计划（2026-08-12）：** 本计划中的 parse-only、red 阶段和逐步替换说明只记录实施过程；当前 Uninstall 行为以 [spec.md](spec.md) 与 [ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 为准。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the complete `uninstall` lifecycle, including local manifest resolution, recoverable serial transactions, synchronous Warn logging, structured fail-fast summaries, CLI rendering, default wiring, and aligned architecture documentation.

**Architecture:** A dedicated `UninstallService` owns one global lock from recovery through all per-Agent Config/Adapter/Transaction work and returns structured results only after a successful Release. `transaction.Service.Uninstall` remains the sole owner of target inspection and journal/quarantine/manifest mutation, but now returns `TargetMissing` even alongside an error; CLI remains the sole renderer and Error/usage exit point. Search, List, and Install production lifecycles stay unchanged.

**Tech Stack:** Go 1.26.5, Cobra, `log/slog`, Masterminds SemVer through the existing Config contract, filesystem journal transactions, Mermaid/C4 Markdown, Go `testing`.

## Global Constraints

- Follow strict TDD for every behavioral task: add a focused failing test, run it and observe the expected failure, implement only the required behavior, rerun focused tests, then commit.
- Before every Go task is committed, run `gofmt -w` on that task's changed Go files and confirm `gofmt -l` reports none of them.
- CLI syntax is exactly `apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]`.
- Reject every position argument containing `=` before lifecycle construction with `uninstall does not accept a version`; Cobra usage and one outer Error must be produced.
- Preserve every nonblank Power Name exactly and match `PowerRef.Name` case-sensitively and exactly. Do not trim the stored Name, case-fold, normalize Unicode, or perform substring matching.
- If `--type` is present, compare it exactly and use the first same-Type/Name record in manifest order. If omitted, resolve Type independently inside each Agent manifest; same-Type duplicates still select the first record, cross-Agent Type differences are valid, and only same-Agent cross-Type matches are ambiguous.
- Trust Config Repository's canonical Type/Name/`vMAJOR.MINOR.PATCH` contract. Do not revalidate, skip, migrate, sort, or degrade invalid manifest data.
- Acquire exactly once. While holding the same lock: Recover, synchronously log recovery Warn, RefreshInstalled, read all reached manifests, Resolve adapters, and execute serial Uninstall transactions.
- No active journal may produce only command-specific Debug diagnostics. Recovery failure stops immediately after one Release attempt.
- A missing manifest record synchronously logs `power is not installed`, produces an Agent-only `unexecuted` item, and continues. Even if all Agents are unexecuted for this reason, exit successfully after Release and rendering.
- A confirmed missing target synchronously logs `installed power target is missing`; a successful transaction produces a full-Power `completed` item, while a simultaneous transaction error produces a full-Power `failed` item.
- The first business failure stops Config reads and execution for later Agents. Add Agent-only `unexecuted` items for every later Agent.
- Render only after a successful Release. A successful-Release batch error renders then exits nonzero; fatal and Release errors suppress all business stdout. Synchronous Warn output is not retracted after a later Release failure.
- Exact full line: `<agent>/<type>/<name>@<version> <completed|failed|unexecuted>\n`. Exact Agent-only line: `<agent> <failed|unexecuted>\n`.
- Lifecycle, Transaction, Adapter, Config, and renderer must not call `slog.Error` or directly print business output. The CLI root remains the only Error/usage exit point.
- Do not refactor Search, List, or Install production lifecycle code, ports, result contracts, or control flow. Only mechanically update their Manager test doubles for the changed `Uninstall` signature.
- Do not add Cloud, Cache, VersionSelector, Adapter Uninstall, Plugin/MCP adapters, parallel execution, continue-after-failure, or a shared lifecycle/batch runner.
- Add concise, normative Chinese comments to every important new or changed public interface, result type, outcome, marker, constructor, and execution method.
- Do not add a Changelog entry until all behavior and documentation are complete. Then add one Feature 011 entry for this entire user request using `/home/liangff/.codex/docs/Changelog.md`.

---

## File and Responsibility Map

- `internal/transaction/manager.go`: public transaction-layer `UninstallResult` and changed Manager signature.
- `internal/transaction/uninstall.go`: preserve `TargetMissing` on all returns after the target fact is known.
- `internal/transaction/uninstall_test.go`: transaction fact/error regressions and unchanged journal semantics.
- `internal/cli/uninstall_contract.go`: Uninstall port, outcomes, optional-Power result, batch marker, and factory.
- `internal/cli/uninstall_render.go`: exact two-form stdout rendering only.
- `internal/cli/uninstall_contract_test.go`: result, marker, exact rendering, and writer failure tests.
- `internal/lifecycle/uninstall_resolve.go`: pure per-Agent manifest Name/Type resolution without input mutation.
- `internal/lifecycle/uninstall_resolve_test.go`: exact matching, local ambiguity, Type filtering, and input immutability.
- `internal/lifecycle/uninstall.go`: service construction, lock/recovery/refresh/Agent targeting, serial batch, Release boundary.
- `internal/lifecycle/uninstall_agent.go`: one-Agent Config/Adapter/Transaction operation and synchronous business Warn helpers.
- `internal/lifecycle/uninstall_test.go`: constructor, ordering, Warn timing, fail-fast, result, and error boundary tests.
- `internal/cli/uninstall.go`: Cobra parsing, factory invocation, lifecycle dispatch, conditional rendering.
- `internal/cli/uninstall_test.go`: command parameter, dispatch, output, factory, batch, and writer behavior.
- `internal/cli/root.go` and existing CLI tests: inject the fourth independent factory without changing other command behavior.
- `cmd/apm/main.go` and `cmd/apm/main_test.go`: default Uninstall dependency graph and filesystem integration.
- ADR/C4/Feature/BUILD/project docs: record the approved interface and completed lifecycle boundaries.
- `docs/Changelog.md`: one completed Feature 011 entry after all source behavior and docs are final.

---

### Task 1: Return the Confirmed Missing-Target Fact from Transaction Uninstall

**Files:**
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/uninstall.go`
- Modify: `internal/transaction/uninstall_test.go`
- Modify: `internal/lifecycle/search_test.go`
- Modify: `internal/lifecycle/list_test.go`
- Modify: `internal/lifecycle/install_test.go`

**Interfaces:**
- Consumes: existing `UninstallRequest`, journal phases, target/quarantine operations, and manifest `Remove` behavior.
- Produces: `transaction.UninstallResult{TargetMissing bool}` and `Manager.Uninstall(context.Context, UninstallRequest) (UninstallResult, error)`.
- Constraint: once `TargetMissing` becomes true, every later error return must preserve it.

- [ ] **Step 1: Extend focused transaction tests with exact result assertions**

Change existing success calls and error calls to capture both values. Add or extend these exact cases:

```go
func TestUninstallReportsExistingTargetWasQuarantined(t *testing.T) {
    fixture := newUninstallFixture(t)
    if err := os.MkdirAll(fixture.target, 0o755); err != nil {
        t.Fatal(err)
    }

    result, err := fixture.service.Uninstall(context.Background(), fixture.request)
    if err != nil {
        t.Fatal(err)
    }
    if result.TargetMissing {
        t.Fatalf("Uninstall() result = %+v, want TargetMissing false", result)
    }
}

func TestUninstallReportsInitiallyMissingTarget(t *testing.T) {
    fixture := newUninstallFixture(t)

    result, err := fixture.service.Uninstall(context.Background(), fixture.request)
    if err != nil {
        t.Fatal(err)
    }
    if !result.TargetMissing {
        t.Fatalf("Uninstall() result = %+v, want TargetMissing true", result)
    }
}
```

Extend `TestUninstallRenameRaceWithExternalRemovalCommits` to assert true. Add a post-confirmation failure test:

```go
func TestUninstallPreservesMissingFactWhenManifestRemovalFails(t *testing.T) {
    fixture := newUninstallFixture(t)
    fixture.manifests.removeErr = errors.New("remove failed")

    result, err := fixture.service.Uninstall(context.Background(), fixture.request)
    if !errors.Is(err, fixture.manifests.removeErr) {
        t.Fatalf("Uninstall() error = %v, want %v", err, fixture.manifests.removeErr)
    }
    if !result.TargetMissing {
        t.Fatalf("Uninstall() result = %+v, want confirmed missing fact", result)
    }
}
```

For create/validation/initial context failures, assert `TargetMissing == false`. For committing update, post-commit cancellation, manifest Remove, transaction-directory cleanup, and journal Delete failures on an initially absent target, assert `TargetMissing == true`.

- [ ] **Step 2: Run focused tests and verify the old signature fails**

Run:

```bash
go test -count=1 ./internal/transaction -run '^TestUninstall'
```

Expected: FAIL to compile because current `Uninstall` returns only `error` and `UninstallResult` is undefined.

- [ ] **Step 3: Add the transaction result type and interface signature**

In `internal/transaction/manager.go`, add the normative Chinese comment and exact signature:

```go
// UninstallResult 描述卸载事务已确认的目标状态；该事实可与后续错误同时返回。
type UninstallResult struct {
    TargetMissing bool
}

type Manager interface {
    Recover(context.Context) (RecoveryResult, error)
    Install(context.Context, InstallRequest) error
    Uninstall(context.Context, UninstallRequest) (UninstallResult, error)
}
```

- [ ] **Step 4: Preserve the result through every Uninstall return path**

Use a local result from the beginning of `Service.Uninstall` and return it with every error:

```go
func (s *Service) Uninstall(ctx context.Context, request UninstallRequest) (UninstallResult, error) {
    result := UninstallResult{}
    // Existing validation, journal, rollback, commit, and cleanup behavior stays unchanged.
```

Set the fact only after authoritative confirmation:

```go
targetExists, err := s.pathExists(target)
if err != nil {
    return result, s.rollbackUninstall(journal, fmt.Errorf("inspect uninstall target %q: %w", target, err))
}
if !targetExists {
    result.TargetMissing = true
}

if targetExists {
    // Existing directory creation and rename.
    if err := s.files.rename(target, artifacts.quarantine); err != nil {
        existsAfterFailure, inspectErr := s.pathExists(target)
        if inspectErr != nil {
            return result, errors.Join(err, fmt.Errorf("recheck uninstall target %q: %w", target, inspectErr))
        }
        if existsAfterFailure {
            return result, s.rollbackUninstall(journal, fmt.Errorf("quarantine uninstall target %q: %w", target, err))
        }
        result.TargetMissing = true
    }
}
```

Every later return must be `return result, err`; success is `return result, nil`. Do not change `rollbackUninstall`'s private signature or existing transaction phase decisions.

- [ ] **Step 5: Mechanically update all existing callers and Manager test doubles**

In transaction tests, replace single-value calls with `_, err :=` where the result is irrelevant. Update only these three lifecycle fake methods:

```go
func (*stubTransactionManager) Uninstall(context.Context, transaction.UninstallRequest) (transaction.UninstallResult, error) {
    return transaction.UninstallResult{}, nil
}
```

Apply the same signature to `listFakeTransactions` and `installFakeTransactions`. Do not change Search/List/Install production files or test expectations.

- [ ] **Step 6: Run transaction and existing lifecycle regression tests**

Run:

```bash
go test -count=1 ./internal/transaction ./internal/lifecycle
```

Expected: PASS. Existing uninstall journal/quarantine/rollback tests must remain green, and Search/List/Install lifecycle tests must compile without behavior changes.

- [ ] **Step 7: Commit the transaction result contract**

```bash
git add internal/transaction/manager.go internal/transaction/uninstall.go internal/transaction/uninstall_test.go internal/lifecycle/search_test.go internal/lifecycle/list_test.go internal/lifecycle/install_test.go
git commit -m "refactor(transaction): report missing uninstall targets"
```

---

### Task 2: Define the Uninstall CLI Result and Renderer Contract

**Files:**
- Create: `internal/cli/uninstall_contract.go`
- Create: `internal/cli/uninstall_render.go`
- Create: `internal/cli/uninstall_contract_test.go`

**Interfaces:**
- Consumes: `domain.AgentID`, optional copied `*domain.PowerRef`, and `io.Writer`.
- Produces: `UninstallLifecycle`, `UninstallLifecycleFactory`, `UninstallOutcome`, `UninstallItem`, `UninstallResult`, `UninstallBatchError`, and `renderUninstall`.

- [ ] **Step 1: Write contract and exact-rendering tests**

Create internal-package tests that exercise both line forms and writer failure:

```go
func TestRenderUninstallWritesExactPowerAndAgentOnlyLines(t *testing.T) {
    completed := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.2.3"}
    failed := domain.PowerRef{Type: domain.PowerTypePlugin, Name: "other", Version: "v2.0.0"}
    skipped := domain.PowerRef{Type: domain.PowerTypeMCP, Name: "later", Version: "v3.0.0"}
    result := UninstallResult{Items: []UninstallItem{
        {Agent: domain.AgentClaudeCode, Power: &completed, Outcome: UninstallOutcomeCompleted},
        {Agent: domain.AgentCodex, Power: &failed, Outcome: UninstallOutcomeFailed},
        {Agent: domain.AgentHermes, Power: &skipped, Outcome: UninstallOutcomeUnexecuted},
        {Agent: domain.AgentCodex, Outcome: UninstallOutcomeFailed},
        {Agent: domain.AgentHermes, Outcome: UninstallOutcomeUnexecuted},
    }}
    var output strings.Builder

    if err := renderUninstall(&output, result); err != nil {
        t.Fatal(err)
    }
    want := "claude-code/skill/demo@v1.2.3 completed\n" +
        "codex/plugin/other@v2.0.0 failed\n" +
        "hermes-agent/mcp/later@v3.0.0 unexecuted\n" +
        "codex failed\n" +
        "hermes-agent unexecuted\n"
    if output.String() != want {
        t.Fatalf("renderUninstall() = %q, want %q", output.String(), want)
    }
}

func TestUninstallBatchErrorUnwrapsCause(t *testing.T) {
    cause := errors.New("remove failed")
    err := &UninstallBatchError{Agent: domain.AgentCodex, Err: cause}
    if !errors.Is(err, cause) {
        t.Fatalf("errors.Is(%v, cause) = false", err)
    }
    if got, want := err.Error(), `uninstall power for agent "codex" failed: remove failed`; got != want {
        t.Fatalf("Error() = %q, want %q", got, want)
    }
}
```

Add a writer that succeeds once and then returns a sentinel; assert the completed prefix remains and the returned error wraps the sentinel with `write uninstall result`.

- [ ] **Step 2: Run the CLI contract tests and verify failure**

Run:

```bash
go test -count=1 ./internal/cli -run '^(TestRenderUninstall|TestUninstallBatchError)'
```

Expected: FAIL because the Uninstall contract and renderer do not exist.

- [ ] **Step 3: Add the command-specific port and result types**

Implement the exact approved shapes with Chinese comments:

```go
type UninstallOutcome string

const (
    UninstallOutcomeCompleted  UninstallOutcome = "completed"
    UninstallOutcomeFailed     UninstallOutcome = "failed"
    UninstallOutcomeUnexecuted UninstallOutcome = "unexecuted"
)

type UninstallItem struct {
    Agent   domain.AgentID
    Power   *domain.PowerRef
    Outcome UninstallOutcome
}

type UninstallResult struct {
    Items []UninstallItem
}

type UninstallBatchError struct {
    Agent domain.AgentID
    Err   error
}

func (e *UninstallBatchError) Error() string {
    return fmt.Sprintf("uninstall power for agent %q failed: %v", e.Agent, e.Err)
}

func (e *UninstallBatchError) Unwrap() error { return e.Err }

type UninstallLifecycle interface {
    Uninstall(context.Context, Flags) (UninstallResult, error)
}

type UninstallLifecycleFactory func() (UninstallLifecycle, error)
```

- [ ] **Step 4: Implement the exact renderer**

```go
func renderUninstall(writer io.Writer, result UninstallResult) error {
    for _, item := range result.Items {
        prefix := string(item.Agent)
        if item.Power != nil {
            prefix += "/" + string(item.Power.Type) + "/" + item.Power.Name + "@" + item.Power.Version
        }
        if _, err := io.WriteString(writer, prefix+" "+string(item.Outcome)+"\n"); err != nil {
            return fmt.Errorf("write uninstall result: %w", err)
        }
    }
    return nil
}
```

Do not validate or infer missing Power fields in the renderer.

- [ ] **Step 5: Run the complete contract tests**

Run:

```bash
go test -count=1 ./internal/cli -run '^(TestRenderUninstall|TestUninstallBatchError)'
```

Expected: PASS.

- [ ] **Step 6: Commit the CLI result contract**

```bash
git add internal/cli/uninstall_contract.go internal/cli/uninstall_render.go internal/cli/uninstall_contract_test.go
git commit -m "feat(cli): define uninstall result contract"
```

---

### Task 3: Resolve a Power Independently from Each Agent Manifest

**Files:**
- Create: `internal/lifecycle/uninstall_resolve.go`
- Create: `internal/lifecycle/uninstall_resolve_test.go`

**Interfaces:**
- Consumes: `cli.Flags{Name, Type}` and a trusted `domain.PowerGroups` value returned by Config.
- Produces: `resolveUninstallPower(flags cli.Flags, groups domain.PowerGroups) (domain.PowerRef, bool, error)`.
- Constraint: do not modify the map, its slices, or any `PowerRef` value.

- [ ] **Step 1: Write resolver table tests**

Cover these exact cases in one table, including explicit/inferred same-Type duplicates selecting the first record and cross-Type ambiguity listing each matched Type once even when one Type is duplicated:

```go
tests := []struct {
    name    string
    flags   cli.Flags
    groups  domain.PowerGroups
    want    domain.PowerRef
    found   bool
    wantErr string
}{
    {
        name: "exact name and inferred type",
        flags: cli.Flags{Name: "Demo"},
        groups: domain.PowerGroups{domain.PowerTypeSkill: {
            {Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
            {Type: domain.PowerTypeSkill, Name: "Demo", Version: "v2.0.0"},
        }},
        want: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "Demo", Version: "v2.0.0"},
        found: true,
    },
    {
        name: "explicit type does not fall back",
        flags: cli.Flags{Name: "demo", Type: "plugin"},
        groups: domain.PowerGroups{domain.PowerTypeSkill: {
            {Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
        }},
        found: false,
    },
    {
        name: "same agent cross type ambiguity",
        flags: cli.Flags{Name: "demo"},
        groups: domain.PowerGroups{
            domain.PowerTypeSkill:  {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
            domain.PowerTypePlugin: {{Type: domain.PowerTypePlugin, Name: "demo", Version: "v2.0.0"}},
        },
        wantErr: "multiple power types [plugin skill]; specify --type",
    },
    {name: "no exact name", flags: cli.Flags{Name: "Demo"}, groups: domain.PowerGroups{
        domain.PowerTypeSkill: {{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}},
    }, found: false},
}
```

Clone every input map and slice before the call and assert deep equality afterward. Add a separate test showing Agent A may resolve Skill while Agent B may resolve Plugin using independent calls.

- [ ] **Step 2: Run resolver tests and verify failure**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^TestResolveUninstallPower'
```

Expected: FAIL because `resolveUninstallPower` is undefined.

- [ ] **Step 3: Implement deterministic local resolution**

Use the fixed canonical Type order only to inspect groups; sort the ambiguity names before formatting so map iteration cannot affect errors:

```go
func resolveUninstallPower(flags cli.Flags, groups domain.PowerGroups) (domain.PowerRef, bool, error) {
    types := []domain.PowerType{
        domain.PowerTypeSkill,
        domain.PowerTypePlugin,
        domain.PowerTypeMCP,
    }
    if flags.Type != "" {
        powerType := domain.PowerType(flags.Type)
        for _, ref := range groups[powerType] {
            if ref.Name == flags.Name {
                return ref, true, nil
            }
        }
        return domain.PowerRef{}, false, nil
    }

    matches := make([]domain.PowerRef, 0, 1)
    matchedTypes := make([]string, 0, 1)
    for _, powerType := range types {
        for _, ref := range groups[powerType] {
            if ref.Name == flags.Name {
                matches = append(matches, ref)
                matchedTypes = append(matchedTypes, string(powerType))
                break
            }
        }
    }
    if len(matches) == 0 {
        return domain.PowerRef{}, false, nil
    }
    if len(matches) > 1 {
        slices.Sort(matchedTypes)
        return domain.PowerRef{}, false, fmt.Errorf(
            "uninstall power %q has multiple power types %v; specify --type",
            flags.Name, matchedTypes,
        )
    }
    return matches[0], true, nil
}
```

Do not parse or compare versions. Repository preserves legal duplicate same-Type/Name records: explicit and inferred resolution select the first record in manifest order, while ambiguity is based on distinct matching Types. A successful transaction `Remove` clears every matching Type/Name record.

- [ ] **Step 4: Run focused resolver tests**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^TestResolveUninstallPower'
```

Expected: PASS.

- [ ] **Step 5: Commit the resolver**

```bash
git add internal/lifecycle/uninstall_resolve.go internal/lifecycle/uninstall_resolve_test.go
git commit -m "feat(lifecycle): resolve uninstall manifests"
```

---

### Task 4: Implement the Dedicated Uninstall Lifecycle and Synchronous Warns

**Files:**
- Replace: `internal/lifecycle/uninstall.go`
- Create: `internal/lifecycle/uninstall_agent.go`
- Create: `internal/lifecycle/uninstall_test.go`

**Interfaces:**
- Consumes: `Locker`, `TransactionManager`, `AdapterRegistry`, `ConfigRepository`, `resolveUninstallPower`, `transaction.UninstallResult`.
- Produces: `NewUninstall(...) (*UninstallService, error)` and `(*UninstallService).Uninstall(context.Context, cli.Flags) (cli.UninstallResult, error)`.
- Result invariant: Power is copied into a distinct local before assigning `*domain.PowerRef` to an item.

- [ ] **Step 1: Build the lifecycle fake fixture and write constructor/order tests**

Create fakes that record exact events and allow per-Agent Config groups, Resolve errors, Transaction results/errors, recovery, refresh, and release errors. The transaction fake method must be:

```go
func (m *uninstallFakeTransactions) Uninstall(_ context.Context, request transaction.UninstallRequest) (transaction.UninstallResult, error) {
    *m.calls = append(*m.calls, "transaction:uninstall:"+string(request.Agent))
    return m.fixture.transactionResults[request.Agent], m.fixture.transactionErrors[request.Agent]
}
```

Add table tests for ordinary nil and typed-nil Locker, Manager, Registry, and Config with exact messages such as `create Uninstall lifecycle with nil locker`.

Add a successful all-Agent order test with Claude Code and Codex in the snapshot and distinct local refs. Expected event subsequence:

```go
[]string{
    "acquire", "recover", "refresh",
    "config:claude-code", "resolve:claude-code", "transaction:uninstall:claude-code",
    "config:codex", "resolve:codex", "transaction:uninstall:codex",
    "release",
}
```

Expected items are both full-Power completed and ordered by the snapshot.

Before implementation, also add core failure-boundary tests: Config List failure produces an Agent-only failed item and later Agent-only unexecuted items; Adapter Resolve and Transaction failures after a determined resolution produce full-Power failed items; Release failure returns zero results and no `UninstallBatchError`; Recover failure still attempts Release and stops before Refresh.

- [ ] **Step 2: Write synchronous logging tests**

Install a `logging.NewLogger(&stdout, &stderr, false)`. Use `beforeRelease` callbacks to assert Warn text already exists.

Recovery test fields must include:

```go
[]string{
    "level=WARN", `msg="transaction recovered"`, "operation=install",
    "agent=codex", "power=demo", "version=v1.0.0",
    "phase=publishing", "direction=commit", "actions=",
}
```

Add one no-journal test with a non-verbose logger and assert both stdout and stderr are empty. Add one verbose test and assert stderr contains only a Debug diagnostic with `msg="no active transaction before uninstall"`, with no Warn or business stdout.

Missing-record test must assert exactly one Warn containing:

```go
[]string{`msg="power is not installed"`, "operation=uninstall", "agent=codex", "power=Demo"}
```

Without an explicit Type, assert no `type=` attribute. With `Type: "plugin"`, assert `type=plugin`.

Target-missing success test must assert exactly one Warn containing:

```go
[]string{
    `msg="installed power target is missing"`, "operation=uninstall",
    "agent=codex", "power=demo", "type=skill", "version=v1.2.3",
}
```

and a full-Power completed item.

- [ ] **Step 3: Run focused lifecycle tests and verify failure**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestNewUninstall|TestUninstall)'
```

Expected: FAIL because the current parse-only lifecycle function has no service or constructor.

- [ ] **Step 4: Add the service constructor and compile assertion**

In `uninstall.go`, replace the parse-only function with the focused service without changing existing lifecycle structs:

```go
// UninstallService 实现 Uninstall 命令在单个全局锁区间内的本地卸载生命周期。
type UninstallService struct {
    locker       Locker
    transactions TransactionManager
    registry     AdapterRegistry
    config       ConfigRepository
}

// NewUninstall 创建持有完整共享状态区间的 Uninstall 生命周期。
func NewUninstall(locker Locker, transactions TransactionManager, registry AdapterRegistry, config ConfigRepository) (*UninstallService, error) {
    switch {
    case isNilInterface(locker):
        return nil, errors.New("create Uninstall lifecycle with nil locker")
    case isNilInterface(transactions):
        return nil, errors.New("create Uninstall lifecycle with nil transaction manager")
    case isNilInterface(registry):
        return nil, errors.New("create Uninstall lifecycle with nil adapter registry")
    case isNilInterface(config):
        return nil, errors.New("create Uninstall lifecycle with nil config repository")
    }
    return &UninstallService{locker: locker, transactions: transactions, registry: registry, config: config}, nil
}

var _ cli.UninstallLifecycle = (*UninstallService)(nil)
```

- [ ] **Step 5: Implement one-Agent behavior and synchronous business Warns**

Use a separate method so its inputs and outputs stay explicit:

```go
func (s *UninstallService) uninstallOne(ctx context.Context, agent domain.AgentID, flags cli.Flags) (cli.UninstallItem, error) {
    groups, err := s.config.List(agent)
    if err != nil {
        return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeFailed},
            fmt.Errorf("list installed powers for agent %q: %w", agent, err)
    }
    ref, found, err := resolveUninstallPower(flags, groups)
    if err != nil {
        return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeFailed}, err
    }
    if !found {
        logUninstallNotInstalled(agent, flags)
        return cli.UninstallItem{Agent: agent, Outcome: cli.UninstallOutcomeUnexecuted}, nil
    }

    copied := ref
    item := cli.UninstallItem{Agent: agent, Power: &copied}
    resolved, err := s.registry.Resolve(agent, ref.Type)
    if err != nil {
        item.Outcome = cli.UninstallOutcomeFailed
        return item, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
    }
    transactionResult, err := s.transactions.Uninstall(ctx, transaction.UninstallRequest{
        Agent: agent, Power: ref, Adapter: resolved,
    })
    if transactionResult.TargetMissing {
        logUninstallMissingTarget(agent, ref)
    }
    if err != nil {
        item.Outcome = cli.UninstallOutcomeFailed
        return item, fmt.Errorf("uninstall power %+v for agent %q: %w", ref, agent, err)
    }
    item.Outcome = cli.UninstallOutcomeCompleted
    return item, nil
}
```

Implement the exact `slog.Warn` messages/fields from the spec. Use `logging.Agent`, `logging.Power`, and `logging.Version`; use `slog.String("operation", "uninstall")` and `slog.String("type", string(...))`. Do not add target paths.

```go
func logUninstallNotInstalled(agent domain.AgentID, flags cli.Flags) {
    attributes := []any{
        slog.String("operation", "uninstall"),
        logging.Agent(agent),
        logging.Power(flags.Name),
    }
    if flags.Type != "" {
        attributes = append(attributes, slog.String("type", flags.Type))
    }
    slog.Warn("power is not installed", attributes...)
}

func logUninstallMissingTarget(agent domain.AgentID, ref domain.PowerRef) {
    slog.Warn(
        "installed power target is missing",
        slog.String("operation", "uninstall"),
        logging.Agent(agent),
        logging.Power(ref.Name),
        slog.String("type", string(ref.Type)),
        logging.Version(ref.Version),
    )
}
```

- [ ] **Step 6: Implement the single-lock serial orchestration**

The orchestration must follow this shape:

```go
func (s *UninstallService) Uninstall(ctx context.Context, flags cli.Flags) (cli.UninstallResult, error) {
    unlock, err := s.locker.Acquire()
    if err != nil {
        return cli.UninstallResult{}, fmt.Errorf("acquire uninstall lock: %w", err)
    }
    recovery, err := s.transactions.Recover(ctx)
    if err != nil {
        return cli.UninstallResult{}, releaseUninstall(unlock, fmt.Errorf("recover transaction before uninstall: %w", err))
    }
    logUninstallRecovery(recovery)

    snapshot, err := s.registry.RefreshInstalled()
    if err != nil {
        return cli.UninstallResult{}, releaseUninstall(unlock, fmt.Errorf("refresh installed agents for uninstall: %w", err))
    }
    agents, err := uninstallAgents(flags, snapshot)
    if err != nil {
        return cli.UninstallResult{}, releaseUninstall(unlock, err)
    }

    items := make([]cli.UninstallItem, 0, len(agents))
    var failedAgent domain.AgentID
    var businessErr error
    for index, agent := range agents {
        item, itemErr := s.uninstallOne(ctx, agent, flags)
        items = append(items, item)
        if itemErr == nil {
            continue
        }
        failedAgent, businessErr = agent, itemErr
        for _, later := range agents[index+1:] {
            items = append(items, cli.UninstallItem{Agent: later, Outcome: cli.UninstallOutcomeUnexecuted})
        }
        break
    }

    if releaseErr := unlock.Release(); releaseErr != nil {
        return cli.UninstallResult{}, errors.Join(businessErr, fmt.Errorf("release uninstall lock: %w", releaseErr))
    }
    result := cli.UninstallResult{Items: items}
    if businessErr != nil {
        return result, &cli.UninstallBatchError{Agent: failedAgent, Err: businessErr}
    }
    return result, nil
}
```

Implement the command-specific helpers exactly; do not reuse Install control-flow helpers:

```go
func uninstallAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
    target := domain.AgentID(flags.Agent)
    if target == domain.AllAgents {
        agents := snapshot.Ordered()
        if len(agents) == 0 {
            return nil, errors.New("uninstall power: no supported installed agents")
        }
        return agents, nil
    }
    if !snapshot.Contains(target) {
        return nil, fmt.Errorf("uninstall power for agent %q: unsupported or not installed", target)
    }
    return []domain.AgentID{target}, nil
}

func releaseUninstall(unlock lock.Unlock, businessErr error) error {
    if err := unlock.Release(); err != nil {
        return errors.Join(businessErr, fmt.Errorf("release uninstall lock: %w", err))
    }
    return businessErr
}

func logUninstallRecovery(result transaction.RecoveryResult) {
    if !result.Recovered {
        slog.Debug("no active transaction before uninstall")
        return
    }
    slog.Warn(
        "transaction recovered",
        slog.String("operation", string(result.Operation)),
        logging.Agent(result.Agent),
        logging.Power(result.Power.Name),
        logging.Version(result.Power.Version),
        logging.Phase(string(result.OriginalPhase)),
        slog.String("direction", string(result.Direction)),
        slog.Any("actions", result.Actions),
    )
}
```

- [ ] **Step 7: Run focused and complete lifecycle tests**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestNewUninstall|TestUninstall)'
go test -count=1 ./internal/lifecycle
```

Expected: PASS. Search/List/Install tests remain unchanged except for Task 1's fake signature.

- [ ] **Step 8: Commit the lifecycle**

```bash
git add internal/lifecycle/uninstall.go internal/lifecycle/uninstall_agent.go internal/lifecycle/uninstall_test.go
git commit -m "feat(lifecycle): orchestrate uninstall transactions"
```

---

### Task 5: Protect Fail-Fast, Result Suppression, and Fact-Plus-Error Boundaries

**Files:**
- Modify: `internal/lifecycle/uninstall_test.go`

**Interfaces:**
- Consumes: Task 4's complete service and fakes.
- Produces: regression-protected batch marker timing, later-Agent suppression, and Warn-plus-error behavior without new production semantics.

- [ ] **Step 1: Add exact batch and fail-fast tests**

Add these independent cases:

1. Config List failure on the first Agent returns `<agent> failed`, never calls Resolve/Transaction for it, never reads later Config, and appends each later Agent as Agent-only unexecuted.
2. Same-Agent cross-Type ambiguity follows the same shape and error contains `specify --type`.
3. Adapter Resolve failure after a determined ref produces a full-Power failed item.
4. Transaction failure produces a full-Power failed item and later Agent-only unexecuted items.
5. Missing records before a later successful Agent produce unexecuted then completed, return nil, and preserve order.
6. All missing records return all Agent-only unexecuted items and nil.
7. The input `AgentSnapshot.Agents` slice remains byte-for-byte/deeply equal after success and failure paths.

The later-Agent no-access assertion must use callbacks that fail the test if `config:<later>`, `resolve:<later>`, or `transaction:uninstall:<later>` occurs after the first failure.

- [ ] **Step 2: Add target-missing fact with simultaneous error test**

Configure the transaction fake with both:

```go
transactionResults: map[domain.AgentID]transaction.UninstallResult{
    domain.AgentCodex: {TargetMissing: true},
},
transactionErrors: map[domain.AgentID]error{
    domain.AgentCodex: errors.New("manifest removal failed"),
},
```

Assert, before Release, that stderr already contains exactly one target-missing Warn with operation, agent, power, type, and version. Assert the current item is full-Power failed and the returned error is `*cli.UninstallBatchError` after successful Release.

- [ ] **Step 3: Add Release and fatal suppression tests**

Cover:

- Recover error still calls Release once and stops before Refresh.
- Recover + Release errors are both reachable through `errors.Is`.
- Refresh, unsupported concrete Agent, and empty all snapshot return zero results and no batch marker.
- Business error + Release error returns zero results, preserves both causes, and `errors.As(err, *UninstallBatchError)` is false.
- A prior missing-record or target-missing Warn remains in stderr after Release failure, but the returned result has zero items.
- Recovery Warn and business Warn callbacks assert both were emitted before Release.

- [ ] **Step 4: Run the new regression tests**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^TestUninstall'
```

Expected: PASS because Task 4 implements the approved boundary. A failure is review evidence that Task 4 is incomplete; do not weaken the assertion.

- [ ] **Step 5: Verify the production patterns protected by the tests**

Keep these non-negotiable patterns:

```go
if releaseErr := unlock.Release(); releaseErr != nil {
    return cli.UninstallResult{}, errors.Join(
        businessErr,
        fmt.Errorf("release uninstall lock: %w", releaseErr),
    )
}
result := cli.UninstallResult{Items: items}
if businessErr != nil {
    return result, &cli.UninstallBatchError{Agent: failedAgent, Err: businessErr}
}
```

and:

```go
transactionResult, transactionErr := s.transactions.Uninstall(ctx, request)
if transactionResult.TargetMissing {
    logUninstallMissingTarget(agent, ref)
}
if transactionErr != nil {
    item.Outcome = cli.UninstallOutcomeFailed
    return item, fmt.Errorf("uninstall power %+v for agent %q: %w", ref, agent, transactionErr)
}
```

Do not change production code when the tests pass. If a regression test fails, correct only the owning Task 4 branch with these patterns, rerun the focused test, and keep the review fix in a separate commit. Do not add an “already logged” error marker; Warns are facts, while the final error remains unlogged until CLI handling.

- [ ] **Step 6: Run lifecycle regression tests and static output scan**

Run:

```bash
go test -count=1 ./internal/lifecycle
! rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle --glob '*.go'
```

Expected: PASS and no direct Error/print matches.

- [ ] **Step 7: Commit lifecycle hardening**

```bash
git add internal/lifecycle/uninstall_test.go
git commit -m "test(lifecycle): protect uninstall error boundaries"
```

---

### Task 6: Connect the Cobra Command and Conditional Renderer

**Files:**
- Replace: `internal/cli/uninstall.go`
- Create: `internal/cli/uninstall_test.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `internal/cli/search_test.go`
- Modify: `internal/cli/list_test.go`
- Modify: `internal/cli/install_test.go`

**Interfaces:**
- Consumes: `UninstallLifecycleFactory`, `UninstallResult`, `UninstallBatchError`, and `renderUninstall`.
- Produces: a fourth independently injected CLI lifecycle factory and executable `uninstall` command.

- [ ] **Step 1: Write command parsing and dispatch tests**

Create `recordingUninstallLifecycle` and an entry helper that makes Search/List/Install factories fail the test if called. Add exact cases:

```go
func TestUninstallPassesOriginalAgentTypeAndName(t *testing.T) {
    // Execute: uninstall "  Demo  " --agent=" codex " --type=plugin
    // Want: cli.Flags{Agent: " codex ", Type: "plugin", Name: "  Demo  "}
}

func TestUninstallRejectsVersionSyntaxBeforeFactory(t *testing.T) {
    for _, argument := range []string{"demo=v1.2.3", "demo=", "a=b=c"} {
        // Factory calls t.Fatal. Assert error contains
        // "uninstall does not accept a version", stdout is empty,
        // stderr has exactly one level=ERROR and Cobra Usage.
    }
}
```

Also test whitespace-only Name, missing/whitespace Agent, invalid Type, extra args, factory after verbose setup, repeated execution, nil factory, nil/typed-nil service, and constructor error context.
Pass an unknown command flag and assert Cobra reports it once with usage without constructing the Uninstall lifecycle.

- [ ] **Step 2: Write conditional rendering tests**

Add exact expected stdout tests for:

- success with full completed plus Agent-only unexecuted;
- fatal error with a nonempty returned result still suppresses stdout;
- `UninstallBatchError` renders full failed and later Agent-only unexecuted before returning the cause;
- writer failure preserves the written prefix and wraps `write uninstall result`;
- batch plus writer failure preserves both causes.

- [ ] **Step 3: Run focused CLI tests and verify the parse-only command fails**

Run:

```bash
go test -count=1 ./internal/cli -run '^(TestUninstall|TestCLIParsesCommands)'
```

Expected: FAIL because `newUninstallCommand` accepts no factory and does not invoke a lifecycle or renderer.

- [ ] **Step 4: Add the fourth independent factory to CLIEntry**

Update the package comment, struct, constructor, Execute, and root builder consistently:

```go
type CLIEntry struct {
    // existing fields
    newUninstallLifecycle UninstallLifecycleFactory
}

func New(
    stdout, stderr io.Writer,
    setupLogging SetupLoggingFunc,
    newSearchLifecycle SearchLifecycleFactory,
    newListLifecycle ListLifecycleFactory,
    newInstallLifecycle InstallLifecycleFactory,
    newUninstallLifecycle UninstallLifecycleFactory,
) *CLIEntry
```

Register `newUninstallCommand(newUninstallLifecycle)`. Update every existing `cli.New` test helper with a no-op Uninstall factory or a `t.Fatal` factory appropriate to the command under test. Do not change the three existing command contracts.

- [ ] **Step 5: Implement the command**

Use this RunE control flow:

```go
func newUninstallCommand(newLifecycle UninstallLifecycleFactory) *cobra.Command {
    var flags Flags
    command := &cobra.Command{
        Use:   "uninstall <power>",
        Short: "Uninstall a power",
        Args:  cobra.ExactArgs(1),
        RunE: func(command *cobra.Command, args []string) error {
            if err := validateCommon(flags); err != nil {
                return err
            }
            if strings.Contains(args[0], "=") {
                return errors.New("uninstall does not accept a version")
            }
            flags.Name = args[0]
            if strings.TrimSpace(flags.Name) == "" {
                return errors.New("power name must not be empty")
            }
            logParsedCommand(command, flags, args)
            if newLifecycle == nil {
                return errors.New("uninstall lifecycle factory is required")
            }
            service, err := newLifecycle()
            if err != nil {
                return fmt.Errorf("create uninstall lifecycle: %w", err)
            }
            if isNilInterface(service) {
                return errors.New("create uninstall lifecycle: nil service")
            }
            result, err := service.Uninstall(command.Context(), flags)
            if err == nil {
                return renderUninstall(command.OutOrStdout(), result)
            }
            var batch *UninstallBatchError
            if !errors.As(err, &batch) {
                return err
            }
            if renderErr := renderUninstall(command.OutOrStdout(), result); renderErr != nil {
                return errors.Join(err, renderErr)
            }
            return err
        },
    }
    bindCommonFlags(command, &flags)
    return command
}
```

- [ ] **Step 6: Run complete CLI tests**

Run:

```bash
go test -count=1 ./internal/cli
```

Expected: PASS. Search/List/Install tests must still prove they do not dispatch to the Uninstall factory.

- [ ] **Step 7: Commit CLI execution**

```bash
git add internal/cli/root.go internal/cli/uninstall.go internal/cli/uninstall_test.go internal/cli/cli_test.go internal/cli/search_test.go internal/cli/list_test.go internal/cli/install_test.go
git commit -m "feat(cli): execute and render uninstall lifecycle"
```

---

### Task 7: Wire Default Dependencies and Prove Filesystem Behavior

**Files:**
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `lifecycle.NewUninstall`, `config.NewJSONRepository`, `transaction.NewManager`, `adapter.NewRegistry`, default global lock.
- Produces: `newUninstallLifecycle() (cli.UninstallLifecycle, error)` and main CLI wiring.

- [ ] **Step 1: Write default lifecycle integration tests**

Add a real install-then-uninstall test in an isolated environment:

```go
func TestNewUninstallLifecycleRemovesBundledSkillAndThenReportsNotInstalled(t *testing.T) {
    home := t.TempDir()
    t.Setenv("APM_HOME", home)
    t.Setenv("XDG_CACHE_HOME", t.TempDir())
    t.Setenv("CODEX_HOME", "")
    t.Setenv("CLAUDE_CONFIG_DIR", "")
    t.Setenv("HERMES_HOME", "")
    changeToRepositoryRoot(t)
    if err := os.Mkdir(filepath.Join(home, ".codex"), 0o755); err != nil {
        t.Fatal(err)
    }

    installer, err := newInstallLifecycle()
    if err != nil { t.Fatal(err) }
    flags := cli.Flags{Agent: "codex", Name: "foo-before-summary"}
    if _, err := installer.Install(context.Background(), flags); err != nil { t.Fatal(err) }

    uninstaller, err := newUninstallLifecycle()
    if err != nil { t.Fatal(err) }
    result, err := uninstaller.Uninstall(context.Background(), flags)
    if err != nil { t.Fatal(err) }
    // Assert one full-Power completed item, absent target, absent manifest entry,
    // and absent active journal.
}
```

Install a test logger before the second call; assert one Agent-only unexecuted item, nil error, and exactly one not-installed Warn with no Error.

Add a target-missing integration test by seeding a canonical manifest without creating its target. Assert full completed, complete target-missing Warn fields, removed manifest entry, and no active journal.

- [ ] **Step 2: Run cmd tests and verify missing factory failure**

Run:

```bash
go test -count=1 ./cmd/apm -run '^(TestNewUninstall|TestNewInstallLifecycleInstallsBundledSkill)'
```

Expected: FAIL because `newUninstallLifecycle` is undefined and main does not inject it.

- [ ] **Step 3: Add default construction and main injection**

```go
func main() {
    entry := cli.New(
        os.Stdout,
        os.Stderr,
        logging.SetupLogging,
        newSearchLifecycle,
        newListLifecycle,
        newInstallLifecycle,
        newUninstallLifecycle,
    )
    if err := entry.Execute(os.Args[1:]); err != nil {
        os.Exit(1)
    }
}

// newUninstallLifecycle 创建 Uninstall 命令所需的默认依赖，并共享清单读写 Repository。
func newUninstallLifecycle() (cli.UninstallLifecycle, error) {
    locker, err := lock.NewDefault()
    if err != nil {
        return nil, fmt.Errorf("create default lock: %w", err)
    }
    repository := config.NewJSONRepository()
    transactions, err := transaction.NewManager(repository)
    if err != nil {
        return nil, fmt.Errorf("create transaction manager: %w", err)
    }
    return lifecycle.NewUninstall(locker, transactions, adapter.NewRegistry(), repository)
}
```

- [ ] **Step 4: Run cmd and cross-package integration tests**

Run:

```bash
go test -count=1 ./cmd/apm ./internal/cli ./internal/lifecycle ./internal/transaction
```

Expected: PASS.

- [ ] **Step 5: Commit default wiring**

```bash
git add cmd/apm/main.go cmd/apm/main_test.go
git commit -m "feat(apm): wire default uninstall lifecycle"
```

---

### Task 8: Record the Approved ADR and Architecture Boundaries

**Files:**
- Modify: `ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md`
- Modify: `ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md`
- Modify: `ivory-tower/adr/ADR-004-discover-installed-agents-before-local-operations.md`
- Modify: `ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/uninstall.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cli-entry.mmd`
- Modify as required by current model: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify as required by current model: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/project-overview.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/features/011-lifecycle-uninstall/feature-overview.md`
- Modify: `ivory-tower/features/011-lifecycle-uninstall/spec.md`
- Modify only when currently contradictory: historical Feature 007/008/009/010 docs found by the scan below.

**Interfaces:**
- Consumes: completed production behavior and the approved Feature 011 spec.
- Produces: one consistent current architecture narrative and the ADR-009 record for the structured target fact.

- [ ] **Step 1: Scan every current contradiction before editing**

Run:

```bash
rg -n 'Uninstall.*parse-only|uninstall.*parse-only|Uninstall 仍|uninstall 仍|尚未.*Uninstall|尚未.*uninstall|Uninstall\(.*error|Uninstall.*打印|自愈' \
  BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'
```

Classify every hit as current, historical, or superseded. Current docs must be corrected. Historical plans may retain old text only when the surrounding section explicitly labels it historical/superseded.

- [ ] **Step 2: Revise the four ADRs with exact decisions**

ADR-009 must record:

```text
Manager.Uninstall returns UninstallResult{TargetMissing bool} plus error.
TargetMissing is true for initial absence and rename-race confirmed absence.
Once true, later committing/manifest/cleanup/journal errors preserve the fact.
Transaction does not log Warn; lifecycle consumes the fact synchronously under lock.
```

Set `Revised: 2026-08-12` while preserving original Date and Accepted status. ADR-002 must record version rejection, per-Agent local Type resolution, missing-record unexecuted, and exact two-form result lines. ADR-003 must record one-lock Uninstall, synchronous Warns, fail-fast, post-Release marker/rendering, and Release suppression. ADR-004 must describe Uninstall as connected rather than future work.

- [ ] **Step 3: Update Mermaid/C4 models**

The Uninstall lifecycle diagram must show:

```text
Acquire → Recover/Warn → Refresh → next Agent → Config.List
  ├─ no record → synchronous not-installed Warn → Agent-only unexecuted → continue
  ├─ ambiguity/read failure → Agent-only failed → later unexecuted → Release
  └─ determined ref → Resolve → Transaction.Uninstall
       ├─ TargetMissing → synchronous full-ref Warn
       ├─ success → full-ref completed → continue
       └─ error → full-ref failed → later unexecuted → Release
Release success → CLI render; Release failure → suppress summary
```

Update Core Lifecycle and CLI Entry to show four independent ports/factories/results. Keep Adapter without an Uninstall method; show Transaction using `TargetPath` and returning the target fact.

- [ ] **Step 4: Update Feature, project, and build documentation**

Set Feature 011 status to implemented with the actual completion date. Add exact CLI examples and both output forms to BUILD. Update project-overview and current feature statements so Search/List/Install/Uninstall are all connected, while Plugin/MCP adapters remain unsupported.

Do not add a Changelog entry in this task.

- [ ] **Step 5: Run documentation consistency and diff checks**

Run:

```bash
rg -n 'Uninstall.*parse-only|uninstall.*parse-only|Uninstall 仍|uninstall 仍|尚未.*Uninstall|尚未.*uninstall|Uninstall\(.*error' \
  BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'
git diff --check
```

Expected: no unqualified current contradiction. Every remaining hit must be explicitly historical or superseded and reviewed line by line.

- [ ] **Step 6: Commit architecture documentation**

```bash
git add BUILD.md ivory-tower
git commit -m "docs(uninstall): record lifecycle implementation"
```

---

### Task 9: Complete Changelog and Final Acceptance Verification

**Files:**
- Modify: `docs/Changelog.md`
- Modify only if verification exposes a real defect: the smallest source/test/doc file that owns that defect.

**Interfaces:**
- Consumes: all Feature 011 commits beginning with design commit `2f9dd8b` and all verification evidence.
- Produces: one reverse-chronological completed Changelog entry and a clean, fully verified branch.

- [ ] **Step 1: Run formatting, module, unit, race, vet, and build checks**

Preserve module files before tidy and run:

```bash
cp go.mod /tmp/apm-uninstall-go.mod.before
cp go.sum /tmp/apm-uninstall-go.sum.before
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
go mod tidy
cmp go.mod /tmp/apm-uninstall-go.mod.before
cmp go.sum /tmp/apm-uninstall-go.sum.before
git diff --check
```

Expected: every command exits 0 and tidy leaves `go.mod`/`go.sum` unchanged.

- [ ] **Step 2: Run static boundary scans**

```bash
! rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle internal/transaction internal/adapter --glob '*.go'
! rg -n 'func \([^)]*\) Uninstall\(' internal/adapter --glob '*.go'
rg -n 'Uninstall\(context\.Context, UninstallRequest\) \(UninstallResult, error\)' internal/transaction/manager.go
rg -n 'newUninstallLifecycle|NewUninstall|UninstallLifecycleFactory' cmd internal --glob '*.go'
```

Expected: the negative scans have no matches; positive scans show the intended transaction contract and independent wiring.

- [ ] **Step 3: Perform an isolated CLI black-box acceptance**

Build a temporary binary and use explicit temporary directories; do not delete broad or unresolved paths:

```bash
accept_root=$(mktemp -d)
accept_bin="$accept_root/apm"
accept_home="$accept_root/home"
accept_cache="$accept_root/cache"
mkdir -p "$accept_home/.codex" "$accept_cache"
go build -o "$accept_bin" ./cmd/apm

APM_HOME="$accept_home" XDG_CACHE_HOME="$accept_cache" \
CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME= \
"$accept_bin" install foo-before-summary --agent codex --type skill \
  >"$accept_root/install.out" 2>"$accept_root/install.err"

APM_HOME="$accept_home" XDG_CACHE_HOME="$accept_cache" \
CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME= \
"$accept_bin" uninstall foo-before-summary --agent codex \
  >"$accept_root/uninstall.out" 2>"$accept_root/uninstall.err"
```

Assert `uninstall.out` is exactly `codex/skill/foo-before-summary@v0.0.1 completed\n`, `uninstall.err` is empty, target and manifest entry are absent, and no active journal exists.

Run the same Uninstall again. Assert stdout is exactly `codex unexecuted\n`; stderr contains exactly one `level=WARN`, `msg="power is not installed"`, `operation=uninstall`, `agent=codex`, and `power=foo-before-summary`; exit status is 0.

Run `uninstall foo-before-summary=v0.0.1 --agent codex`. Assert nonzero status, empty stdout, exactly one `level=ERROR`, `uninstall does not accept a version`, and `Usage:` in stderr.

- [ ] **Step 4: Fix only failures revealed by acceptance and rerun their owning checks**

If a command fails, identify the owning layer from the exact assertion, add a focused regression test first, make the smallest correction, and rerun both the focused check and Steps 1–3. Do not broaden scope or refactor passing lifecycles.

- [ ] **Step 5: Add the single completed Changelog entry**

Read `/home/liangff/.codex/docs/Changelog.md` again. Obtain the exact related commit list with:

```bash
git log --reverse --format='- `%h %s`' 2f9dd8b^..HEAD
```

Insert one new `## 2026-08-12` entry at the top of `docs/Changelog.md` with:

- category `新增功能`;
- the user request summarized as completing Lifecycle Uninstall and clarifying its local resolution, Warn, fail-fast, and structured result boundaries;
- every exact related commit line from the command above;
- the implemented transaction target-fact contract, one-lock lifecycle, per-Agent local Type resolution, both Warns, both stdout forms, and CLI-only Error behavior;
- the exact successful verification commands and black-box outputs from Steps 1–3;
- the remaining limitation that only built-in Skill adapters exist and external filesystem races remain outside APM's guarantee.

Do not claim a check that was not actually run and observed successful.

- [ ] **Step 6: Commit the completed Changelog**

```bash
git add docs/Changelog.md
git commit -m "docs(uninstall): record completed feature"
```

- [ ] **Step 7: Run final clean-tree verification**

```bash
go test -count=1 ./...
git diff --check
git status --short
```

Expected: tests PASS, diff check exits 0, and status has no output.

---

## Execution Notes

- Execute tasks in order. Task 1 deliberately changes the shared Manager signature first so every later task compiles against the final contract.
- Review each task before starting the next; reject out-of-scope changes even when tests pass.
- Keep commits task-scoped. If a review fix is required, add a focused fix commit rather than rewriting unrelated history.
- Do not mark Feature 011 implemented or write its Changelog until source behavior, default wiring, and documentation all pass acceptance.

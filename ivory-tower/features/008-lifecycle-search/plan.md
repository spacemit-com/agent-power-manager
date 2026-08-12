# Lifecycle Search Implementation Plan

> **历史计划（已被后续 feature 修订）：** 本计划中的命令 parse-only 状态只记录当时步骤；Feature 009、010、011 已分别实现 List、Install、Uninstall。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement lifecycle Search with locked transaction recovery, unlock-before-Cloud querying, latest-version selection, and exact CLI rendering.

**Architecture:** CLI owns a command-specific `SearchLifecycle` port and `SearchResult`. The lifecycle implementation injects Locker, TransactionManager, and CloudClient; it recovers and emits a synchronous success Warn under lock, releases, then filters and sorts Cloud references before returning structured data.

**Tech Stack:** Go 1.26.5, Cobra, `log/slog`, `gofrs/flock`, `github.com/Masterminds/semver/v3`, `golang.org/x/text/cases`, Mermaid/C4 Markdown.

## Global Constraints

- Syntax is exactly `apm search <keyword> [--type=<skill|plugin|mcp>]`; Search must not register `--agent`.
- Cloud references are contractually normalized: known lowercase Type, canonical Name, and `vMAJOR.MINOR.PATCH` without prerelease/build metadata.
- Do not validate, skip, or define degraded ordering for invalid Cloud data.
- Unicode-fold keyword and Name; match only a case-insensitive substring of `PowerRef.Name`.
- If `--type` is present, compare it exactly with `PowerRef.Type`.
- Keep maximum SemVer per original `(Type, Name)`; stable-sort by original Type then Name ascending.
- Hold the global lock only for recovery. A successful recovery Warn is synchronous under lock; Cloud `List()` runs only after successful Release.
- Lifecycle receives no Renderer and writes no business stdout. CLI renders exactly `<type>/<name>@<version>\n`.
- Lifecycle logs no Error. CLI logs every returned error exactly once after lifecycle attempts unlock.
- Do not wire or refactor list/install/uninstall lifecycle behavior or signatures.
- Add concise Chinese comments to important new Search interfaces and constructors.
- Preserve unrelated worktree changes.
- Add one reverse-chronological Changelog entry only after implementation and verification finish.

## File Map

| File | Responsibility |
| --- | --- |
| `internal/cloud/*test.go` | Align committed fixture expectations with `v0.0.1`. |
| `go.mod`, `go.sum` | SemVer and Unicode-fold dependencies. |
| `internal/cli/search_contract.go` | CLI-owned Search port, factory, and result. |
| `internal/lifecycle/search_filter.go` | Pure matching, latest-version selection, sorting. |
| `internal/lifecycle/service.go`, `preflight.go`, `search.go` | Inject dependencies and orchestrate recovery/unlock/Cloud. |
| `internal/lifecycle/search*_test.go` | Pure algorithm and orchestration tests. |
| `internal/cli/flags.go`, `search.go`, `root.go` | Search parsing, factory invocation, rendering. |
| `internal/cli/cli_test.go`, `search_test.go` | Shared CLI and Search-specific tests. |
| `cmd/apm/main.go`, `main_test.go` | Default dependencies and real integration. |
| ADR/C4/overview/`BUILD.md` | Synchronize syntax, lock boundary, and ownership. |
| `docs/Changelog.md` | One completed-request record. |

---

### Task 1: Restore the v-prefixed Cloud baseline

**Files:**
- Modify: `internal/cloud/local_client_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`

**Interfaces:**
- Consumes: committed fixture `skill/foo-before-summary@v0.0.1`.
- Produces: green Cloud tests expecting exactly `v0.0.1`.

- [ ] **Step 1: Reproduce the stale assertions**

Run:

```bash
go test -count=1 ./internal/cloud
```

Expected: FAIL in the committed-fixture list and cache integration tests because they still use `0.0.1`.

- [ ] **Step 2: Correct only those expectations**

Use this value in both tests:

```go
domain.PowerRef{
    Name: "foo-before-summary",
    Type: domain.PowerTypeSkill,
    Version: "v0.0.1",
}
```

Do not change LocalClient production behavior or accept unprefixed versions.

- [ ] **Step 3: Verify and commit**

```bash
go test -count=1 ./internal/cloud
git add internal/cloud/local_client_test.go internal/cloud/local_cache_integration_test.go
git commit -m "test(cloud): align v-prefixed fixture"
```

Expected: tests PASS and commit succeeds.

---

### Task 2: Define the Search port and pure selection algorithm

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/cli/search_contract.go`
- Create: `internal/lifecycle/search_filter.go`
- Create: `internal/lifecycle/search_filter_test.go`

**Interfaces:**
- Consumes: `cli.Flags`, `[]domain.PowerRef`.
- Produces: `cli.SearchResult`, `cli.SearchLifecycle`, `cli.SearchLifecycleFactory`, `filterSearchPowers(cli.Flags, []domain.PowerRef) []domain.PowerRef`.

- [ ] **Step 1: Write failing pure tests**

Create tests named:

```go
func TestFilterSearchPowersMatchesNameSelectsLatestAndSorts(t *testing.T)
func TestFilterSearchPowersAppliesExactType(t *testing.T)
func TestFilterSearchPowersMatchesOnlyName(t *testing.T)
func TestFilterSearchPowersKeepsSameNameInDifferentTypes(t *testing.T)
func TestFilterSearchPowersReturnsNonNilEmptySlice(t *testing.T)
func TestFilterSearchPowersDoesNotModifyInput(t *testing.T)
```

The primary case must contain:

```go
flags := cli.Flags{Keyword: "  STRASSE  "}
refs := []domain.PowerRef{
    {Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.9.0"},
    {Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.10.0"},
    {Type: domain.PowerTypePlugin, Name: "StraßePlugin", Version: "v2.0.0"},
    {Type: domain.PowerTypeMCP, Name: "unrelated", Version: "v9.0.0"},
}
want := []domain.PowerRef{
    {Type: domain.PowerTypePlugin, Name: "StraßePlugin", Version: "v2.0.0"},
    {Type: domain.PowerTypeSkill, Name: "StraßeTool", Version: "v1.10.0"},
}
```

Prove keyword `skill` does not match Type alone and keyword `1.2.3` does not match Version alone.

- [ ] **Step 2: Verify RED**

```bash
go test -count=1 ./internal/lifecycle -run '^TestFilterSearchPowers'
```

Expected: FAIL because `filterSearchPowers` is undefined.

- [ ] **Step 3: Add dependencies and contract**

```bash
go get github.com/Masterminds/semver/v3 golang.org/x/text
```

Create:

```go
// SearchResult 表示 CLI 可直接渲染的搜索业务结果。
type SearchResult struct {
    Powers []domain.PowerRef
}

// SearchLifecycle 定义 search 命令唯一需要的生命周期入口。
type SearchLifecycle interface {
    Search(context.Context, Flags) (SearchResult, error)
}

// SearchLifecycleFactory 在日志初始化后创建 Search 生命周期。
type SearchLifecycleFactory func() (SearchLifecycle, error)
```

- [ ] **Step 4: Implement the pure algorithm**

Use `cases.Fold()`, `strings.TrimSpace`, and this grouping/comparison shape:

```go
var searchFold = cases.Fold()

type searchPowerKey struct {
    powerType domain.PowerType
    name string
}

func filterSearchPowers(flags cli.Flags, refs []domain.PowerRef) []domain.PowerRef {
    keyword := searchFold.String(strings.TrimSpace(flags.Keyword))
    latest := make(map[searchPowerKey]domain.PowerRef)

    for _, ref := range refs {
        if flags.Type != "" && ref.Type != domain.PowerType(flags.Type) {
            continue
        }
        if !strings.Contains(searchFold.String(ref.Name), keyword) {
            continue
        }
        key := searchPowerKey{powerType: ref.Type, name: ref.Name}
        current, found := latest[key]
        if !found || semver.MustParse(ref.Version).GreaterThan(semver.MustParse(current.Version)) {
            latest[key] = ref
        }
    }

    powers := make([]domain.PowerRef, 0, len(latest))
    for _, ref := range latest {
        powers = append(powers, ref)
    }
    sort.SliceStable(powers, func(i, j int) bool {
        if powers[i].Type != powers[j].Type {
            return powers[i].Type < powers[j].Type
        }
        return powers[i].Name < powers[j].Name
    })
    return powers
}
```

`MustParse` expresses the Cloud precondition. Add no invalid-data branch or test.

- [ ] **Step 5: Verify GREEN and commit**

```bash
gofmt -w internal/cli/search_contract.go internal/lifecycle/search_filter.go internal/lifecycle/search_filter_test.go
go test -count=1 ./internal/lifecycle -run '^TestFilterSearchPowers'
go test -count=1 ./internal/cli ./internal/lifecycle
git add go.mod go.sum internal/cli/search_contract.go internal/lifecycle/search_filter.go internal/lifecycle/search_filter_test.go
git commit -m "feat(search): select latest matching powers"
```

Expected: all tests PASS.

---

### Task 3: Implement locked recovery and Search lifecycle

**Files:**
- Modify: `internal/lifecycle/service.go`
- Modify: `internal/lifecycle/preflight.go`
- Modify: `internal/lifecycle/search.go`
- Create: `internal/lifecycle/search_test.go`

**Interfaces:**
- Consumes: Locker, TransactionManager, CloudClient, `filterSearchPowers`.
- Produces: `New(Locker, TransactionManager, CloudClient) (*Service, error)`, `(*Service).Search(context.Context, cli.Flags) (cli.SearchResult, error)`.

- [ ] **Step 1: Write failing orchestration tests**

Create fakes implementing full aliases. Their methods append `acquire`, `recover`, `release`, and `cloud-list` to one shared slice:

```go
type stubUnlock struct { calls *[]string; err error }
func (u *stubUnlock) Release() error {
    *u.calls = append(*u.calls, "release")
    return u.err
}

type stubLocker struct { calls *[]string; unlock Unlock; err error }
func (l *stubLocker) Acquire() (Unlock, error) {
    *l.calls = append(*l.calls, "acquire")
    return l.unlock, l.err
}

type stubTransactionManager struct {
    calls *[]string
    result transaction.RecoveryResult
    err error
}
func (m *stubTransactionManager) Recover(context.Context) (transaction.RecoveryResult, error) {
    *m.calls = append(*m.calls, "recover")
    return m.result, m.err
}
func (*stubTransactionManager) Install(context.Context, transaction.InstallRequest) error { return nil }
func (*stubTransactionManager) Uninstall(context.Context, transaction.UninstallRequest) error { return nil }

type stubCloudClient struct {
    calls *[]string
    before func()
    refs []domain.PowerRef
    err error
}
func (c *stubCloudClient) List() ([]domain.PowerRef, error) {
    if c.before != nil { c.before() }
    *c.calls = append(*c.calls, "cloud-list")
    return c.refs, c.err
}
func (*stubCloudClient) Get(domain.PowerRef) error { return nil }
```

Add tests:

```go
func TestSearchRecoversWarnsUnlocksThenListsCloud(t *testing.T)
func TestSearchNoJournalHasNoNormalOutput(t *testing.T)
func TestSearchNoJournalWritesDebugWhenVerbose(t *testing.T)
func TestSearchAcquireFailureStopsBeforeRecovery(t *testing.T)
func TestSearchRecoveryFailureReleasesAndStopsBeforeCloud(t *testing.T)
func TestSearchJoinsRecoveryAndReleaseFailures(t *testing.T)
func TestSearchReleaseFailureStopsBeforeCloud(t *testing.T)
func TestSearchCloudFailurePreservesCause(t *testing.T)
func TestNewRejectsNilSearchDependencies(t *testing.T)
```

In the success test, use `logging.NewLogger`; the Cloud fake must assert stderr already contains `level=WARN`. Require:

```go
wantCalls := []string{"acquire", "recover", "release", "cloud-list"}
```

And Warn fields:

```go
[]string{
    "operation=install", "agent=codex", "power=demo",
    "version=v2.0.0", "phase=publishing",
    "direction=commit", "actions=",
}
```

- [ ] **Step 2: Verify RED**

```bash
go test -count=1 ./internal/lifecycle -run '^(TestSearch|TestNewRejectsNilSearchDependencies)'
```

Expected: FAIL against the existing zero-value service stub.

- [ ] **Step 3: Inject dependencies and update only Search signature**

Make `Lifecycle.Search` context-aware and result-returning while leaving the other three signatures/bodies unchanged:

```go
type Service struct {
    locker Locker
    transactions TransactionManager
    cloud CloudClient
}

// New 创建具有恢复、锁和 Cloud 依赖的生命周期服务。
func New(locker Locker, transactions TransactionManager, cloud CloudClient) (*Service, error) {
    switch {
    case locker == nil:
        return nil, errors.New("create lifecycle service with nil locker")
    case transactions == nil:
        return nil, errors.New("create lifecycle service with nil transaction manager")
    case cloud == nil:
        return nil, errors.New("create lifecycle service with nil cloud client")
    }
    return &Service{locker: locker, transactions: transactions, cloud: cloud}, nil
}
```

Add compile assertions for `Lifecycle` and `cli.SearchLifecycle`.

- [ ] **Step 4: Implement recovery preflight**

Replace the empty preflight function with `recoverSearch(ctx)`:

```go
unlock, err := s.locker.Acquire()
if err != nil {
    return fmt.Errorf("acquire search recovery lock: %w", err)
}
result, recoveryErr := s.transactions.Recover(ctx)
if recoveryErr != nil {
    releaseErr := unlock.Release()
    if releaseErr != nil {
        return errors.Join(
            fmt.Errorf("recover transaction before search: %w", recoveryErr),
            fmt.Errorf("release search recovery lock: %w", releaseErr),
        )
    }
    return fmt.Errorf("recover transaction before search: %w", recoveryErr)
}
if result.Recovered {
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
} else {
    slog.Debug("no active transaction before search")
}
if err := unlock.Release(); err != nil {
    return fmt.Errorf("release search recovery lock: %w", err)
}
return nil
```

Do not defer Release and do not log Error.

- [ ] **Step 5: Implement Search orchestration**

```go
// Search 在锁内恢复共享状态，并在解锁后查询和筛选 Cloud Power。
func (s *Service) Search(ctx context.Context, flags cli.Flags) (cli.SearchResult, error) {
    if err := s.recoverSearch(ctx); err != nil {
        return cli.SearchResult{}, err
    }
    refs, err := s.cloud.List()
    if err != nil {
        return cli.SearchResult{}, fmt.Errorf("list Cloud powers for search: %w", err)
    }
    return cli.SearchResult{Powers: filterSearchPowers(flags, refs)}, nil
}
```

- [ ] **Step 6: Verify and commit**

```bash
gofmt -w internal/lifecycle/service.go internal/lifecycle/preflight.go internal/lifecycle/search.go internal/lifecycle/search_test.go
go test -count=1 ./internal/lifecycle
test -z "$(rg -n 'slog\.Error|fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln)' internal/lifecycle --glob '*.go' --glob '!**/*_test.go')"
git add internal/lifecycle/service.go internal/lifecycle/preflight.go internal/lifecycle/search.go internal/lifecycle/search_test.go
git commit -m "feat(search): recover before cloud query"
```

Expected: PASS and static scan empty.

---

### Task 4: Wire Cobra Search and exact rendering

**Files:**
- Modify: `internal/cli/flags.go`, `search.go`, `root.go`, `cli_test.go`
- Create: `internal/cli/search_test.go`
- Modify: `cmd/apm/main.go`
- Create: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: Search factory and port.
- Produces: four-argument `cli.New`, Search-only type flag, exact renderer.

- [ ] **Step 1: Write failing CLI tests**

Use a stub that captures Flags and returns SearchResult. Add:

```go
func TestSearchInvokesLifecycleWithTrimmedKeywordAndType(t *testing.T)
func TestSearchRendersExactResultLines(t *testing.T)
func TestSearchEmptyResultWritesNothing(t *testing.T)
func TestSearchRejectsAgentAsUnknownFlag(t *testing.T)
func TestSearchHelpDoesNotListAgentFlag(t *testing.T)
func TestSearchLifecycleErrorIsLoggedOnceWithUsage(t *testing.T)
func TestSearchFactoryRunsAfterVerboseLoggerSetup(t *testing.T)
func TestSearchRenderFailureIsLoggedOnce(t *testing.T)
func TestSearchRenderFailurePreservesCompletedPrefix(t *testing.T)
```

Exact output fixture:

```go
result := cli.SearchResult{Powers: []domain.PowerRef{
    {Type: domain.PowerTypeMCP, Name: "alpha", Version: "v2.0.0"},
    {Type: domain.PowerTypeSkill, Name: "beta", Version: "v1.0.0"},
}}
want := "mcp/alpha@v2.0.0\nskill/beta@v1.0.0\n"
```

Unknown Agent must contain `unknown flag: --agent`, one Error, usage, empty stdout.

Also create `cmd/apm/main_test.go`. With isolated `APM_HOME` and `XDG_CACHE_HOME`, change cwd from `cmd/apm` to the repository root, call `newSearchLifecycle`, Search for `summary`, and require:

```go
cli.SearchResult{Powers: []domain.PowerRef{{
    Type: domain.PowerTypeSkill,
    Name: "foo-before-summary",
    Version: "v0.0.1",
}}}
```

- [ ] **Step 2: Update shared CLI expectations**

Change legal Search calls to omit Agent. Keep Agent-required tests for the other commands. Verbose Search must assert command/type/args and absence of `agent=`. Repeated execution must verify verbose/type reset, not missing Agent. Make the test helper inject an empty-result factory.

- [ ] **Step 3: Verify RED**

```bash
go test -count=1 ./internal/cli ./cmd/apm
```

Expected: FAIL because Search is still parse-only, requires Agent, and the default factory is undefined.

- [ ] **Step 4: Separate type-only validation and binding**

```go
func validatePowerType(flags Flags) error {
    switch flags.Type {
    case "", "skill", "plugin", "mcp":
        return nil
    default:
        return fmt.Errorf("invalid --type %q: must be skill, plugin, or mcp", flags.Type)
    }
}

func validateCommon(flags Flags) error {
    if strings.TrimSpace(flags.Agent) == "" {
        return errors.New("--agent is required")
    }
    return validatePowerType(flags)
}
```

Add `bindTypeFlag`; Search calls it, while `bindCommonFlags` binds Agent then calls it.

- [ ] **Step 5: Inject factory through a fresh root**

Add `newSearchLifecycle SearchLifecycleFactory` to CLIEntry. Change:

```go
func New(
    stdout, stderr io.Writer,
    setupLogging SetupLoggingFunc,
    newSearchLifecycle SearchLifecycleFactory,
) *CLIEntry
```

Pass it through `newRootCommand` to `newSearchCommand`. Invoke the factory only inside RunE, after `preparseLogging`. Change parsed logging to omit Agent when empty.

Implement the lazy default factory in `cmd/apm/main.go` and pass it to `cli.New`:

```go
func newSearchLifecycle() (cli.SearchLifecycle, error) {
    locker, err := lock.NewDefault()
    if err != nil { return nil, fmt.Errorf("create default lock: %w", err) }
    transactions, err := transaction.NewManager(config.NewJSONRepository())
    if err != nil { return nil, fmt.Errorf("create transaction manager: %w", err) }
    resourcesRoot, err := filepath.Abs(filepath.Join("internal", "cloud", "resources"))
    if err != nil { return nil, fmt.Errorf("resolve local Cloud resources: %w", err) }
    cloudClient, err := cloud.NewLocalClient(resourcesRoot)
    if err != nil { return nil, fmt.Errorf("create local Cloud client: %w", err) }
    return lifecycle.New(locker, transactions, cloudClient)
}
```

Because CLI calls the factory in RunE, constructor errors use the configured logger.

- [ ] **Step 6: Implement Search command and renderer**

Use this RunE body:

```go
RunE: func(command *cobra.Command, args []string) error {
    if err := validatePowerType(flags); err != nil {
        return err
    }
    flags.Keyword = strings.TrimSpace(args[0])
    if flags.Keyword == "" {
        return errors.New("keyword must not be empty")
    }
    logParsedCommand(command, flags, args)
    if newLifecycle == nil {
        return errors.New("search lifecycle factory is required")
    }
    service, err := newLifecycle()
    if err != nil {
        return fmt.Errorf("create search lifecycle: %w", err)
    }
    if service == nil {
        return errors.New("create search lifecycle: nil service")
    }
    result, err := service.Search(command.Context(), flags)
    if err != nil {
        return err
    }
    return renderSearch(command.OutOrStdout(), result)
},
```

Then add the renderer:

```go
func renderSearch(writer io.Writer, result SearchResult) error {
    for _, ref := range result.Powers {
        line := string(ref.Type) + "/" + ref.Name + "@" + ref.Version + "\n"
        if _, err := io.WriteString(writer, line); err != nil {
            return fmt.Errorf("write search result: %w", err)
        }
    }
    return nil
}
```

Do not use `slog.Info` or `fmt.Fprint*`.

- [ ] **Step 7: Verify and commit**

```bash
gofmt -w internal/cli/flags.go internal/cli/search.go internal/cli/root.go internal/cli/cli_test.go internal/cli/search_test.go cmd/apm/main.go cmd/apm/main_test.go
go test -count=1 ./internal/cli ./internal/lifecycle ./cmd/apm
go build -o /dev/null ./cmd/apm
test -z "$(rg -n 'fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln)' internal/cli --glob '*.go' --glob '!**/*_test.go')"
git add internal/cli/flags.go internal/cli/search.go internal/cli/root.go internal/cli/cli_test.go internal/cli/search_test.go cmd/apm/main.go cmd/apm/main_test.go
git commit -m "feat(search): render lifecycle results"
```

Expected: PASS and static scan empty.

---

### Task 5: Revise ADRs, diagrams, and user docs

**Files:**
- Modify: `ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md`
- Modify: `ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md`
- Modify: `ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: CLI Entry/Core Lifecycle/common recovery/Search Mermaid files
- Modify: `BUILD.md`

**Interfaces:**
- Consumes: implemented signatures and accepted Feature 008 spec.
- Produces: consistent architecture records referencing ADR-002/003/009.

- [ ] **Step 1: Revise ADR-002**

Change Search syntax to:

```text
apm search <keyword> [--type=<skill|plugin|mcp>]
```

State Agent applies only to list/install/uninstall; Search does not register it and Cobra reports it unknown. Remove the claim that all four verbs share both flags.

- [ ] **Step 2: Amend ADR-003**

Mark it revised on 2026-08-11 while retaining Accepted. Record exactly:

- acquire only for recovery or other APM shared-state business;
- return structured results and render outside the lock;
- Search: acquire → Recover → synchronous success Warn → Release → Cloud;
- recovery errors return after attempted Release and CLI logs once;
- lifecycles without shared-state work do not lock;
- future list/install/uninstall features choose their own non-coupled boundaries.

Add “hold through rendering” as rejected due to writer latency and unnecessary serialization.

- [ ] **Step 3: Synchronize ADR-009 and overview**

Link Feature 008 from ADR-009 and state Search now connects Manager recovery; other lifecycle commands remain future wiring. In overview, record no Agent dimension for Search, lifecycle result ownership, CLI rendering, release-before-Cloud, and parse-only status of list/install/uninstall.

- [ ] **Step 4: Update C4 and Mermaid**

Show `SearchLifecycle.Search(Context, Flags) (SearchResult, error)`, `SearchResult.Powers`, and CLI rendering. Search flow must be:

```text
validate → acquire → recover
recovered → Warn → release
no journal → optional Debug → release
failure → attempted release → CLI Error + usage
release success → Cloud List → fold/filter → latest SemVer
→ stable Type/Name sort → SearchResult → CLI renderer
```

Update `apm-cli.dsl` so CLI owns rendering and Core Lifecycle no longer claims printing.

- [ ] **Step 5: Update BUILD.md**

Use:

```bash
./apm search summary
./apm search summary --type skill
./apm --verbose search summary
```

Document exact output `skill/foo-before-summary@v0.0.1`; Search is implemented, other three commands remain parse-only.

- [ ] **Step 6: Verify docs and commit**

```bash
git diff --check
! rg -n 'apm search .*--agent|search.*必填.*agent|Search\(Flags\) \(\[\]PowerRef' BUILD.md ivory-tower/adr ivory-tower/project-overview.md ivory-tower/c4/components --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'ADR-003|SearchResult|解锁|CLI.*渲染' ivory-tower/features/008-lifecycle-search ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md ivory-tower/project-overview.md ivory-tower/c4/components
git add BUILD.md ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md ivory-tower/project-overview.md ivory-tower/c4/components/apm-cli.dsl ivory-tower/c4/components/apm-cli-diagrams/cli-entry.mmd ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd ivory-tower/c4/components/apm-cli-lifecycle-diagrams/common-journal-recovery.mmd ivory-tower/c4/components/apm-cli-lifecycle-diagrams/search.mmd
git commit -m "docs(search): revise lifecycle lock boundary"
```

Expected: negative scan empty, positive scan shows synchronized references.

---

### Task 6: Final verification and Changelog

**Files:**
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: every Feature 008 commit beginning at design `86a4eb5`.
- Produces: clean verified repository and one completed-request record.

- [ ] **Step 1: Run all gates**

```bash
test -z "$(gofmt -l cmd internal)"
git diff --check
go mod tidy
git diff --check
go test -count=1 ./internal/cloud ./internal/lifecycle ./internal/cli ./cmd/apm
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
```

Expected: every command exits 0. If tidy changes module files, verify and commit them as `chore(search): tidy lifecycle dependencies`.

- [ ] **Step 2: Run acceptance checks**

```bash
apm_accept_root=$(mktemp -d)
go build -o "$apm_accept_root/apm" ./cmd/apm
APM_HOME="$apm_accept_root/home" XDG_CACHE_HOME="$apm_accept_root/cache" "$apm_accept_root/apm" search summary >"$apm_accept_root/search.out" 2>"$apm_accept_root/search.err"
test "$(cat "$apm_accept_root/search.out")" = 'skill/foo-before-summary@v0.0.1'
test ! -s "$apm_accept_root/search.err"
if APM_HOME="$apm_accept_root/home" XDG_CACHE_HOME="$apm_accept_root/cache" "$apm_accept_root/apm" search summary --agent=all >"$apm_accept_root/agent.out" 2>"$apm_accept_root/agent.err"; then exit 1; fi
test ! -s "$apm_accept_root/agent.out"
test "$(rg -c 'level=ERROR' "$apm_accept_root/agent.err")" -eq 1
rg -n 'unknown flag: --agent' "$apm_accept_root/agent.err"
rg -n 'Usage:' "$apm_accept_root/agent.err"
test -z "$(rg -n 'fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln)|log\.(Print|Printf|Println|Fatal|Fatalf|Fatalln)|slog\.New' cmd internal --glob '*.go' --glob '!**/*_test.go')"
test -z "$(rg -n 'slog\.Error' internal/lifecycle --glob '*.go')"
rm -rf -- "$apm_accept_root"
```

Expected: exact normal output, unknown Agent failure with one Error and usage, empty static scans.

- [ ] **Step 3: Collect exact commits**

```bash
git log --reverse --format='- `%h %s`' 86a4eb5^..HEAD
```

Copy only Feature 008 design, plan, test, implementation, dependency, and documentation commits.

- [ ] **Step 4: Add the 2026-08-11 Changelog entry**

Use classification `新增功能`. Summarize the user request, list the exact commits, describe locked Recover/realtime Warn/unlock-before-Cloud/Unicode Name matching/latest SemVer/stable ordering/exact rendering, and record actual successful commands. Explicitly state list/install/uninstall remain unconnected. Do not claim a failed gate passed.

- [ ] **Step 5: Verify and commit Changelog**

```bash
git diff --check
git status --short
git add docs/Changelog.md
git commit -m "docs(search): record lifecycle implementation"
git status --short
```

Expected: final status empty.

---

## Completion Checklist

- [ ] Search rejects `--agent` as unknown with usage and nonzero exit.
- [ ] Search returns SearchResult and receives no Renderer.
- [ ] Recovery is locked; success Warn precedes Release and Cloud.
- [ ] Recovery errors attempt Release and are logged once by CLI.
- [ ] Cloud/filter/version/sort/render all follow successful Release.
- [ ] Unicode Name-only matching, exact type, maximum SemVer, and output format are covered.
- [ ] list/install/uninstall lifecycle interfaces and wiring are untouched.
- [ ] ADR-002, amended ADR-003, ADR-009, diagrams, overview, and BUILD.md agree.
- [ ] Important Search interfaces/constructors have Chinese comments.
- [ ] Focused/full tests, vet, build, formatting, diff, and static scans pass.
- [ ] One Changelog entry contains all related commits and actual evidence.

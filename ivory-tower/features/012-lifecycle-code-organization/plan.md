# Lifecycle Code Organization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize the four command-specific lifecycle implementations and tests into predictable, responsibility-based files while preserving every public interface and observable behavior.

**Architecture:** Keep one `lifecycle` package and four independent `SearchService`, `ListService`, `InstallService`, and `UninstallService` implementations. Put each public service and orchestration method in its command's main file, keep substantial result/installed/agent/resolve responsibilities in command-prefixed files, and avoid any cross-command runner or lock/recovery abstraction.

**Tech Stack:** Go 1.24, Cobra-owned CLI lifecycle contracts, `log/slog`, Masterminds SemVer, Go test/race/vet/build tooling.

## Global Constraints

- Work only in `internal/lifecycle` production/tests plus this Feature 012 plan; do not modify `internal/cli`, `cmd/apm`, other packages, ADR, C4, BUILD, project overview, or `docs/Changelog.md`.
- Preserve the exact signatures and behavior of `NewSearch`, `NewList`, `NewInstall`, `NewUninstall`, `Search`, `List`, `Install`, and `Uninstall`.
- Preserve nil and typed-nil validation order, exact error text, wrapping/join trees, `errors.Is/As` behavior, structured results, partial/batch markers, and non-nil empty slices.
- Preserve all dependency call conditions, counts, and order; preserve each command's existing lock interval and Release/result suppression behavior.
- Preserve every `slog` level, message, attribute, value, and synchronous position relative to Release and subsequent work.
- Do not create a shared lifecycle runner, recovery helper, lock template, batch framework, application container, or cross-command test fixture.
- Retain every existing test function name, input, assertion, and ordering expectation when splitting test files.
- Add concise Chinese comments only at important lifecycle business boundaries; explain reasons and constraints rather than translating each statement.
- Delete `internal/lifecycle/batch.go` only because `orderAgents` is private, unused, and always returns `nil`; do not replace it.
- Use `apply_patch` for file content changes and preserve unrelated worktree changes.
- This is a behavior-neutral refactor, so do not add a `docs/Changelog.md` entry.

## Target File Map

| File | Responsibility |
|---|---|
| `internal/lifecycle/doc.go` | Package-level Chinese documentation only. |
| `internal/lifecycle/dependencies.go` | Public dependency aliases/interfaces and private typed-nil dependency validation. |
| `internal/lifecycle/search.go` | `SearchService`, constructor, interface assertion, Search orchestration, locked recovery and recovery logging. |
| `internal/lifecycle/search_result.go` | Search folding, filtering, latest-version selection and ordering. |
| `internal/lifecycle/list.go` | `ListService`, constructor, interface assertion, and Cloud/installed branch entry. |
| `internal/lifecycle/list_installed.go` | Installed List lock/recovery/read flow, Agent selection, partial failures and `agentPowerGroups`. |
| `internal/lifecycle/list_result.go` | Cloud/installed List result copying, filtering, flattening and ordering. |
| `internal/lifecycle/install.go` | `InstallService`, constructor, interface assertion, top-level locked batch flow, Agent selection, Release and recovery logging. |
| `internal/lifecycle/install_agent.go` | Cache materialization and one-Agent install/outcome behavior. |
| `internal/lifecycle/install_resolve.go` | Install name/type/version resolution. |
| `internal/lifecycle/uninstall.go` | `UninstallService`, constructor, interface assertion, top-level locked fail-fast flow, Agent selection, Release and recovery logging. |
| `internal/lifecycle/uninstall_agent.go` | One-Agent manifest/adapter/transaction behavior and fact Warn logging. |
| `internal/lifecycle/uninstall_resolve.go` | Uninstall local manifest resolution. |

The target test files are `search_test.go`, `search_result_test.go`, `list_test.go`, `list_installed_test.go`, `list_result_test.go`, `install_test.go`, `install_agent_test.go`, `install_resolve_test.go`, `uninstall_test.go`, `uninstall_agent_test.go`, and `uninstall_resolve_test.go`.

Because the accepted spec forbids new behavior and the existing suite already characterizes the relevant contracts, each task uses a green-baseline/refactor/green cycle instead of manufacturing a failing product test.

---

### Task 1: Normalize Package-Level Files and Remove Dead Batch Placeholder

**Files:**
- Create: `internal/lifecycle/doc.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/service.go`
- Delete: `internal/lifecycle/batch.go`
- Test: `internal/lifecycle/*_test.go`

**Interfaces:**
- Consumes: Existing dependency types `AdapterRegistry`, `ConfigRepository`, `TransactionManager`, `Locker`, `PowerCache`, `CloudClient`, and `VersionSelector`.
- Produces: The same dependency names and the same private `isNilInterface(any) bool`, now located in `dependencies.go`; no API or behavior changes.

- [ ] **Step 1: Establish the package baseline and dead-code fact**

Run:

```bash
go test -count=1 ./internal/lifecycle
rg -n 'orderAgents\(' internal/lifecycle
```

Expected: lifecycle tests PASS; `rg` returns only the declaration in `internal/lifecycle/batch.go` and exits successfully.

- [ ] **Step 2: Create the conventional package documentation file**

Use `apply_patch` to create exactly:

```go
// Package lifecycle 提供命令专属的 Power 生命周期服务。
package lifecycle
```

Remove the same package comment from `service.go` so there is one package documentation owner.

- [ ] **Step 3: Move typed-nil dependency validation beside dependency declarations**

Use `apply_patch` to add `reflect` to `dependencies.go` and move this function from `service.go` without changing its body:

```go
func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
```

`service.go` must still contain `SearchService`, `NewSearch`, and the `cli.SearchLifecycle` assertion at the end of this task; Task 2 moves them.

- [ ] **Step 4: Delete only the dead placeholder**

Use `apply_patch` to delete `internal/lifecycle/batch.go`. Do not introduce another `orderAgents` or alter any Agent selection helper.

- [ ] **Step 5: Format and verify the package**

Run:

```bash
gofmt -w internal/lifecycle/doc.go internal/lifecycle/dependencies.go internal/lifecycle/service.go
test -z "$(gofmt -l internal/lifecycle)"
go test -count=1 ./internal/lifecycle
test "$(rg -l '^func isNilInterface\(' internal/lifecycle --glob '*.go' | wc -l)" -eq 1
test -z "$(rg -l 'orderAgents\(' internal/lifecycle --glob '*.go' || true)"
git diff --check
```

Expected: every command exits 0; the single `isNilInterface` declaration is in `dependencies.go`; there is no `orderAgents` reference.

- [ ] **Step 6: Commit the package-level cleanup**

```bash
git add internal/lifecycle/doc.go internal/lifecycle/dependencies.go internal/lifecycle/service.go internal/lifecycle/batch.go
git commit -m "refactor(lifecycle): organize shared declarations"
```

---

### Task 2: Consolidate Search Entry and Rename Pure Result Files

**Files:**
- Modify: `internal/lifecycle/search.go`
- Create: `internal/lifecycle/search_result.go`
- Delete: `internal/lifecycle/service.go`
- Delete: `internal/lifecycle/preflight.go`
- Delete: `internal/lifecycle/search_filter.go`
- Modify: `internal/lifecycle/search_test.go`
- Create: `internal/lifecycle/search_result_test.go`
- Delete: `internal/lifecycle/search_filter_test.go`

**Interfaces:**
- Consumes: `Locker`, `TransactionManager`, `CloudClient`, `isNilInterface`, `cli.SearchLifecycle`, `cli.Flags`, and `transaction.RecoveryResult` exactly as defined before this task.
- Produces: Unchanged `SearchService`, `NewSearch(Locker, TransactionManager, CloudClient) (*SearchService, error)`, `Search(context.Context, cli.Flags) (cli.SearchResult, error)`, private `recoverSearch`, and private `filterSearchPowers`.

- [ ] **Step 1: Establish the Search baseline and test-name inventory**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestSearch|TestNewSearch|TestFilterSearch)'
{ rg '^func Test' internal/lifecycle/search_test.go; rg '^func Test' internal/lifecycle/search_filter_test.go; } | sort > /tmp/apm-feature012-search-tests.before
```

Expected: tests PASS and the inventory contains every current Search lifecycle and result test.

- [ ] **Step 2: Make `search.go` the complete Search entry**

Use `apply_patch` to arrange declarations in this order:

```go
type SearchService struct {
	locker       Locker
	transactions TransactionManager
	cloud        CloudClient
}

func NewSearch(locker Locker, transactions TransactionManager, cloud CloudClient) (*SearchService, error)

var _ cli.SearchLifecycle = (*SearchService)(nil)

func (s *SearchService) Search(ctx context.Context, flags cli.Flags) (cli.SearchResult, error)

func (s *SearchService) recoverSearch(ctx context.Context) error
```

Move the existing constructor and assertion from `service.go`, and the existing `recoverSearch` body from `preflight.go`, without changing executable statements, error text, logging or order. Retain required imports for `errors`, `fmt`, `log/slog`, `logging`, and CLI types.

Add concise Chinese comments immediately around the two non-obvious boundaries:

```go
// Recover 会修改跨命令共享状态，因此获取锁后才能执行并同步记录恢复事实。
```

and, before Cloud List after successful recovery Release:

```go
// Cloud 查询不访问共享状态；只有恢复锁成功释放后才允许继续，避免扩大锁区间。
```

Existing accurate comments may be retained instead of duplicated. Delete `service.go` and `preflight.go` only after all Search declarations are present in `search.go`.

- [ ] **Step 3: Rename Search pure-result production and tests**

Use `apply_patch` to create `search_result.go` with the unchanged declarations currently in `search_filter.go`:

```go
var searchFold = cases.Fold()

type searchPowerKey struct {
	powerType domain.PowerType
	name      string
}

func filterSearchPowers(flags cli.Flags, refs []domain.PowerRef) []domain.PowerRef
```

Delete `search_filter.go`. Move all six `TestFilterSearchPowers...` functions unchanged from `search_filter_test.go` into `search_result_test.go`, then delete `search_filter_test.go`. Keep all original test names and assertions.

- [ ] **Step 4: Verify Search behavior and the complete test inventory**

Run:

```bash
gofmt -w internal/lifecycle/search.go internal/lifecycle/search_result.go internal/lifecycle/search_test.go internal/lifecycle/search_result_test.go
go test -count=1 ./internal/lifecycle -run '^(TestSearch|TestNewSearch|TestFilterSearch)'
{ rg '^func Test' internal/lifecycle/search_test.go; rg '^func Test' internal/lifecycle/search_result_test.go; } | sort > /tmp/apm-feature012-search-tests.after
cmp /tmp/apm-feature012-search-tests.before /tmp/apm-feature012-search-tests.after
test ! -e internal/lifecycle/service.go
test ! -e internal/lifecycle/preflight.go
test ! -e internal/lifecycle/search_filter.go
test ! -e internal/lifecycle/search_filter_test.go
git diff --check
```

Expected: tests PASS, inventories are identical, obsolete files do not exist, and the diff is clean.

- [ ] **Step 5: Commit the Search organization**

```bash
git add internal/lifecycle
git commit -m "refactor(lifecycle): organize search files"
```

---

### Task 3: Separate List Cloud, Installed, and Result Responsibilities

**Files:**
- Modify: `internal/lifecycle/list.go`
- Modify: `internal/lifecycle/list_installed.go`
- Create: `internal/lifecycle/list_result.go`
- Delete: `internal/lifecycle/list_filter.go`
- Modify: `internal/lifecycle/list_test.go`
- Create: `internal/lifecycle/list_installed_test.go`
- Create: `internal/lifecycle/list_result_test.go`
- Delete: `internal/lifecycle/list_filter_test.go`

**Interfaces:**
- Consumes: Existing `ListService`, `NewList`, `List`, `listInstalled`, `makeCloudListResult`, `makeInstalledListResult`, and `cli.PartialListError` contracts.
- Produces: The same symbols and behavior, with private `agentPowerGroups` moved from result calculation to the installed-flow file that creates it.

- [ ] **Step 1: Establish List baselines and inventory all List tests**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestList|TestNewList|TestMakeCloudList|TestMakeInstalledList)'
{ rg '^func Test' internal/lifecycle/list_test.go; rg '^func Test' internal/lifecycle/list_filter_test.go; } | sort > /tmp/apm-feature012-list-tests.before
```

Expected: all List tests PASS.

- [ ] **Step 2: Clarify the two List branches in production code**

Keep `ListService`, `NewList`, its interface assertion and `List` in `list.go`. Add concise Chinese comments that preserve these facts:

```go
// Cloud 分支不读取本地共享状态，因此直接查询并返回结果，不获取全局锁。
```

In `list_installed.go`, move `agentPowerGroups` from `list_filter.go` unchanged:

```go
type agentPowerGroups struct {
	agent  domain.AgentID
	groups domain.PowerGroups
}
```

Add short comments at the installed-flow boundaries stating that Recover, snapshot refresh and all Config reads share one lock interval; realtime recovery logging occurs while locked; result conversion waits until successful Release. Do not change the body order of `listInstalled`, `installedListAgents`, `releaseInstalledList`, or `logInstalledListRecovery`.

- [ ] **Step 3: Rename the pure List result implementation**

Use `apply_patch` to create `list_result.go` from `list_filter.go`, retaining unchanged bodies for:

```go
func makeCloudListResult(flags cli.Flags, refs []domain.PowerRef) cli.ListResult
func makeInstalledListResult(flags cli.Flags, inputs []agentPowerGroups) cli.ListResult
func sortListedPowers(powers []cli.ListedPower)
```

Delete `list_filter.go` after removing `agentPowerGroups` and moving all result functions.

- [ ] **Step 4: Split List tests by the production branch they protect**

Keep the fixture/stub declarations, `TestNewListRejectsNilDependencies`, `TestListCloudDoesNotTouchSharedState`, `TestListCloudFailurePreservesCause`, `TestListCloudVerboseDoesNotRecoverOrLog`, and `newListService` in `list_test.go`.

Move every `TestListInstalled...` function unchanged into `list_installed_test.go`:

```text
TestListInstalledRecoversWarnsRefreshesReadsThenReleases
TestListInstalledFatalPathsReleaseOnceAndSuppressResults
TestListInstalledEmptySnapshotReturnsNonNilEmptyResult
TestListInstalledAllReturnsPartialAndContinuesAfterAgentFailure
TestListInstalledAllKeepsFailureOrderAndReadsLaterAgents
TestListInstalledPartialAndReleaseFailureSuppressesResultAndMarker
TestListInstalledNoJournalHasNoNormalOutput
TestListInstalledNoJournalWritesDebugWhenVerbose
```

Move every existing `TestMakeCloudListResult...` and `TestMakeInstalledListResult...` function unchanged from `list_filter_test.go` to `list_result_test.go`, then delete `list_filter_test.go`. The new test files remain `package lifecycle`, so they can reuse fixtures from `list_test.go` without a support file.

- [ ] **Step 5: Verify List behavior, test preservation and file boundaries**

Run:

```bash
gofmt -w internal/lifecycle/list.go internal/lifecycle/list_installed.go internal/lifecycle/list_result.go internal/lifecycle/list_test.go internal/lifecycle/list_installed_test.go internal/lifecycle/list_result_test.go
go test -count=1 ./internal/lifecycle -run '^(TestList|TestNewList|TestMakeCloudList|TestMakeInstalledList)'
{ rg '^func Test' internal/lifecycle/list_test.go; rg '^func Test' internal/lifecycle/list_installed_test.go; rg '^func Test' internal/lifecycle/list_result_test.go; } | sort > /tmp/apm-feature012-list-tests.after
cmp /tmp/apm-feature012-list-tests.before /tmp/apm-feature012-list-tests.after
test ! -e internal/lifecycle/list_filter.go
test ! -e internal/lifecycle/list_filter_test.go
test "$(rg -l '^type agentPowerGroups struct' internal/lifecycle --glob '*.go')" = "internal/lifecycle/list_installed.go"
git diff --check
```

Expected: all commands exit 0 and no List test was lost or renamed.

- [ ] **Step 6: Commit the List organization**

```bash
git add internal/lifecycle
git commit -m "refactor(lifecycle): organize list files"
```

---

### Task 4: Split Install Flow Tests and Document Main Boundaries

**Files:**
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_test.go`
- Create: `internal/lifecycle/install_agent_test.go`
- Verify unchanged: `internal/lifecycle/install_agent.go`
- Verify unchanged: `internal/lifecycle/install_resolve.go`
- Verify unchanged: `internal/lifecycle/install_resolve_test.go`

**Interfaces:**
- Consumes and produces unchanged `InstallService`, `NewInstall`, `Install`, `installAgents`, `releaseInstall`, `logInstallRecovery`, `fetchInstallStaging`, `installOne`, and `resolveInstallPower`.
- Produces no new production symbol; only the specified comments and responsibility-aligned test files. Production declaration order remains Service, constructor, interface assertion, public method, then private helpers.

- [ ] **Step 1: Establish Install baseline and test inventory**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestInstall|TestNewInstall|TestResolveInstall)'
{ rg '^func Test' internal/lifecycle/install_test.go; rg '^func Test' internal/lifecycle/install_resolve_test.go; } | sort > /tmp/apm-feature012-install-tests.before
```

Expected: all Install tests PASS.

- [ ] **Step 2: Add concise comments to the Install orchestration boundaries**

Keep executable code in `install.go` unchanged. Add or refine short Chinese comments around these existing stages:

```go
// Recover、快照、Power 解析和逐 Agent Transaction 共同读写受保护状态，必须位于同一锁区间。
```

Before the per-Agent loop/failure completion:

```go
// 批处理首错即停止；后续 Agent 补为 unexecuted，使成功解锁后的汇总保持完整顺序。
```

Before handling Release/result:

```go
// Release 失败时受保护状态不可确认，必须抑制全部可渲染结果并保留已发生的错误原因。
```

Do not change calls, branches, return values, error construction, recovery log code, or helper signatures. Keep the file order as Service, constructor, interface assertion, `Install`, Agent selection, Release helper, recovery logger.

- [ ] **Step 3: Move per-Agent behavior tests into `install_agent_test.go`**

Keep all fixture/stub types, `newInstallService`, assertion helpers and these top-level orchestration tests in `install_test.go`:

```text
TestInstallAllUsesSnapshotOrderAndFetchesSeparatelyPerChangedAgent
TestInstallAllFailsFastAndCompletesResultAfterRelease
TestInstallFatalPreTargetFailuresReturnZeroResultWithoutBatchError
TestInstallRecoveryFailureStillReleasesAndJoinsReleaseError
TestInstallFatalBusinessAndReleaseFailuresAreJoinedWithoutResult
TestInstallBatchAndReleaseFailuresSuppressResultAndMarker
TestInstallReleaseFailureAfterSuccessSuppressesCompletedResult
TestInstallRecoveryWarnIsWrittenBeforeRefreshAndRelease
TestInstallNoJournalIsSilentUnlessVerbose
TestNewInstallRejectsNilAndTypedNilDependencies
```

Move these tests unchanged to `install_agent_test.go`:

```text
TestInstallFirstInstallOrdersDependenciesAndReturnsInstalled
TestInstallCacheMissGetsCloudAndFetchesAgain
TestInstallSameVersionReturnsUnchangedWithoutFetchOrTransaction
TestInstallClassifiesUpgradeAndDowngradeBySemVer
TestInstallConcreteFailureReturnsFailedItemAndMarkerAfterRelease
TestInstallAgentFailuresReturnFailedResultAndMarkerAfterRelease
```

Both files remain `package lifecycle`; do not duplicate fixtures or helpers. Keep `install_resolve_test.go` unchanged.

- [ ] **Step 4: Verify Install behavior and exact test preservation**

Run:

```bash
gofmt -w internal/lifecycle/install.go internal/lifecycle/install_test.go internal/lifecycle/install_agent_test.go
go test -count=1 ./internal/lifecycle -run '^(TestInstall|TestNewInstall|TestResolveInstall)'
{ rg '^func Test' internal/lifecycle/install_test.go; rg '^func Test' internal/lifecycle/install_agent_test.go; rg '^func Test' internal/lifecycle/install_resolve_test.go; } | sort > /tmp/apm-feature012-install-tests.after
cmp /tmp/apm-feature012-install-tests.before /tmp/apm-feature012-install-tests.after
git diff --check
```

Expected: tests PASS and the before/after test declarations are identical.

- [ ] **Step 5: Commit the Install organization**

```bash
git add internal/lifecycle/install.go internal/lifecycle/install_test.go internal/lifecycle/install_agent_test.go
git commit -m "refactor(lifecycle): organize install tests"
```

---

### Task 5: Split Uninstall Flow Tests and Document Main Boundaries

**Files:**
- Modify: `internal/lifecycle/uninstall.go`
- Modify: `internal/lifecycle/uninstall_test.go`
- Create: `internal/lifecycle/uninstall_agent_test.go`
- Verify unchanged: `internal/lifecycle/uninstall_agent.go`
- Verify unchanged: `internal/lifecycle/uninstall_resolve.go`
- Verify unchanged: `internal/lifecycle/uninstall_resolve_test.go`

**Interfaces:**
- Consumes and produces unchanged `UninstallService`, `NewUninstall`, `Uninstall`, `uninstallAgents`, `releaseUninstall`, `logUninstallRecovery`, `uninstallOne`, fact logging helpers, and `resolveUninstallPower`.
- Produces no new production symbol; only the specified comments and responsibility-aligned test files. Production declaration order remains Service, constructor, interface assertion, public method, then private helpers.

- [ ] **Step 1: Establish Uninstall baseline and test inventory**

Run:

```bash
go test -count=1 ./internal/lifecycle -run '^(TestUninstall|TestNewUninstall|TestResolveUninstall)'
{ rg '^func Test' internal/lifecycle/uninstall_test.go; rg '^func Test' internal/lifecycle/uninstall_resolve_test.go; } | sort > /tmp/apm-feature012-uninstall-tests.before
```

Expected: all Uninstall tests PASS.

- [ ] **Step 2: Add concise comments to Uninstall orchestration boundaries**

Keep executable code unchanged and add or refine short Chinese comments around these facts:

```go
// Recover、快照、本地清单解析和逐 Agent Transaction 都访问共享状态，必须位于同一锁区间。
```

Before the Agent loop/fail-fast completion:

```go
// 每个 Agent 的事实 Warn 在锁内实时记录；首个失败停止后续业务并补齐 unexecuted 汇总项。
```

Before Release/result handling:

```go
// 只有 Release 成功后才能返回可渲染汇总；Release 失败时保留业务错误但抑制结果。
```

Do not change error composition, result pointers, Warn order, transaction fact consumption, or helper signatures. Keep the file order as Service, constructor, interface assertion, `Uninstall`, Agent selection, Release helper, recovery logger.

- [ ] **Step 3: Move one-Agent manifest and transaction tests into `uninstall_agent_test.go`**

Keep all fixture/stub declarations, `recordUninstallEvent`, `newUninstallService`, assertion helpers and these orchestration tests in `uninstall_test.go`:

```text
TestNewUninstallRejectsNilDependencies
TestUninstallAllUsesSnapshotOrderAndDistinctPowerPointers
TestUninstallAllConfigFailurePreventsLaterAgentLifecycleAccess
TestUninstallAllSameAgentTypeAmbiguityPreventsLaterAgentLifecycleAccess
TestUninstallAllMissingThenSuccessfulRecordsPreserveSnapshotOrder
TestUninstallAllMissingRecordsReturnOnlyUnexecutedItems
TestUninstallDoesNotMutateAgentSnapshotAcrossSuccessAndFailure
TestUninstallReleaseFailureSuppressesResultAndBatchError
TestUninstallRecoverFailureReleasesAndStopsBeforeRefresh
TestUninstallRecoveryWarnsSynchronouslyBeforeRelease
TestUninstallNoJournalHasNoNormalOutput
TestUninstallNoJournalWritesOnlyDebugWhenVerbose
TestUninstallRecoverAndReleaseFailuresPreserveBothCauses
TestUninstallFatalPreBatchFailuresSuppressResultsAndBatchMarker
TestUninstallFactWarningsRemainVisibleWhenReleaseSuppressesResult
TestUninstallRecoveryAndBusinessWarningsAreBothEmittedBeforeRelease
```

Move these tests unchanged to `uninstall_agent_test.go`:

```text
TestUninstallConfigFailureReturnsAgentOnlyFailedAndLaterUnexecutedItems
TestUninstallPostResolutionFailuresReturnFullPowerFailedItems
TestUninstallMissingRecordWarnsSynchronouslyWithOptionalType
TestUninstallMissingTargetWarnsSynchronouslyAndCompletes
TestUninstallAdapterResolutionFailureKeepsResolvedPowerInFailedItem
TestUninstallTransactionFailureSuppressesLaterAgentAndKeepsResolvedPower
TestUninstallMissingTargetFactWarnsBeforeReleaseWhenTransactionAlsoFails
```

Both files remain `package lifecycle`; do not duplicate fixture or assertion code. Keep `uninstall_resolve_test.go` unchanged.

- [ ] **Step 4: Verify Uninstall behavior and exact test preservation**

Run:

```bash
gofmt -w internal/lifecycle/uninstall.go internal/lifecycle/uninstall_test.go internal/lifecycle/uninstall_agent_test.go
go test -count=1 ./internal/lifecycle -run '^(TestUninstall|TestNewUninstall|TestResolveUninstall)'
{ rg '^func Test' internal/lifecycle/uninstall_test.go; rg '^func Test' internal/lifecycle/uninstall_agent_test.go; rg '^func Test' internal/lifecycle/uninstall_resolve_test.go; } | sort > /tmp/apm-feature012-uninstall-tests.after
cmp /tmp/apm-feature012-uninstall-tests.before /tmp/apm-feature012-uninstall-tests.after
git diff --check
```

Expected: tests PASS and the before/after test declarations are identical.

- [ ] **Step 5: Commit the Uninstall organization**

```bash
git add internal/lifecycle/uninstall.go internal/lifecycle/uninstall_test.go internal/lifecycle/uninstall_agent_test.go
git commit -m "refactor(lifecycle): organize uninstall tests"
```

---

### Task 6: Run Full Behavior-Preservation Acceptance

**Files:**
- Verify: `internal/lifecycle/*.go`
- Verify unchanged: `internal/cli/**`
- Verify unchanged: `cmd/apm/**`
- Verify unchanged: `ivory-tower/adr/**`
- Verify unchanged: `docs/Changelog.md`

**Interfaces:**
- Consumes: Tasks 1–5 and accepted Feature 012 spec.
- Produces: A clean, fully verified behavior-neutral refactor with no additional source or documentation commit required.

- [ ] **Step 1: Verify the exact production and test file layout**

Run:

```bash
find internal/lifecycle -maxdepth 1 -type f -name '*.go' -printf '%f\n' | sort
```

Confirm the production files are exactly:

```text
dependencies.go
doc.go
install.go
install_agent.go
install_resolve.go
list.go
list_installed.go
list_result.go
search.go
search_result.go
uninstall.go
uninstall_agent.go
uninstall_resolve.go
```

and test files are exactly the eleven files listed in the Target File Map. There must be no `batch.go`, `service.go`, `preflight.go`, `search_filter.go`, `search_filter_test.go`, `list_filter.go`, or `list_filter_test.go`.

- [ ] **Step 2: Verify public constructors and four independent CLI assertions**

Run:

```bash
rg -n '^func New(Search|List|Install|Uninstall)\(' internal/lifecycle
rg -n '^var _ cli\.(Search|List|Install|Uninstall)Lifecycle = ' internal/lifecycle
test "$(rg -l '^func New(Search|List|Install|Uninstall)\(' internal/lifecycle | wc -l)" -eq 4
test "$(rg '^var _ cli\.(Search|List|Install|Uninstall)Lifecycle = ' internal/lifecycle | wc -l)" -eq 4
test -z "$(rg -n 'type (Service|LifecycleService|Runner) struct' internal/lifecycle --glob '*.go' || true)"
```

Manually compare the four constructor signature lines with commit `7cad708`:

```bash
git grep -h -E '^func New(Search|List|Install|Uninstall)\(' 7cad708 -- internal/lifecycle
```

Expected: the signatures match exactly and each command still has its own compile-time CLI assertion. The generic-runner scan is empty.

- [ ] **Step 3: Verify scope and behavioral strings did not drift**

Run:

```bash
feature012_plan_commit="$(git log -1 --format=%H -- ivory-tower/features/012-lifecycle-code-organization/plan.md)"
git diff --name-only "${feature012_plan_commit}"..HEAD
git diff --name-only "${feature012_plan_commit}"..HEAD -- internal/cli cmd/apm ivory-tower/adr BUILD.md ivory-tower/project-overview.md docs/Changelog.md
rg -n 'transaction recovered|no active transaction before|release (search recovery|installed list|install|uninstall) lock' internal/lifecycle
```

Expected: the first command lists only `internal/lifecycle` files; the scoped second command prints nothing. Review the third command against the existing command-specific error and logging tests; do not normalize or deduplicate its strings.

- [ ] **Step 4: Run formatting, focused and full verification**

Run each command separately and require exit code 0:

```bash
test -z "$(gofmt -l internal/lifecycle)"
go test -count=1 ./internal/lifecycle
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command succeeds with no formatting or diff errors.

- [ ] **Step 5: Confirm no Changelog entry and a clean worktree**

Run:

```bash
git diff --exit-code 7cad708..HEAD -- docs/Changelog.md
git status --short
```

Expected: the Changelog comparison exits 0 and `git status --short` prints nothing. Do not create a verification-only commit.

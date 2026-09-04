# List 全版本查询与 Show 详情 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修改 Cloud `apm list` 的 `--all` 语义以按名称展开全部版本，并新增支持最新/精确版本详情查询的 `apm show` 命令。

**Architecture:** 保持现有 Cobra → lifecycle factory → immutable catalog snapshot → renderer 分层。List 在同一 snapshot 中根据 `Name`、`Type`、`All` 决定 latest 去重或全版本保留；Show 新增独立 CLI/lifecycle/result/rendering 端口，复用 CatalogManager 与 Snapshot.Resolve 的类型推断、歧义和版本选择能力。

**Tech Stack:** Go、Cobra、Masterminds/semver、现有 `internal/cli`/`internal/lifecycle` 测试工具与 catalog fake。

**Spec:** `ivory-tower/features/029-list-all-versions-show/spec.md`

## Global Constraints

- `list --all`/`-a` 只有在提供位置参数 `[name]` 时有效；缺少 name 必须在 lifecycle factory 前失败。
- Cloud `list <name> --all` 保留该名称全部版本，排序为 type/name 升序、version SemVer 降序；默认 list 仍每个 `(type,name)` 只留最新版本。
- `list --all` 不再控制 description/tags；`list --json` 始终输出身份数组，installed 语义不变。
- `show <name>` 只读取 Cloud catalog；默认最新版本，`--version` 仅接受 `vMAJOR.MINOR.PATCH` 精确版本；跨类型名称需 `--type`。
- Show 文本字段顺序固定为 type、name、version、description、tags；JSON 为单对象且 tags 始终数组。
- 只读命令继续 Inspect journal、Snapshot catalog、stale 时 Warn，不 Recover、不访问 installed 状态、不自动 fallback。
- 重要接口添加简要规范中文注释；行为性源代码变更完成后更新 `docs/Changelog.md`。

---

### Task 1: List 全版本集合语义（TDD）

**Files:**
- Modify: `internal/lifecycle/list_result.go`
- Test: `internal/lifecycle/list_result_test.go`

**Interfaces:**
- Consumes: existing `cli.Flags{Name, Type, All}` and `[]domain.CatalogEntry`.
- Produces: `makeCatalogListResult` that keeps all versions only when `flags.All && flags.Name != ""`; otherwise retains latest per `(type,name)`.

- [ ] **Step 1: Write failing lifecycle tests**
  Add cases proving `makeCatalogListResult(cli.Flags{Name:"demo", All:true}, entries)` returns `v2.0.0`, `v1.2.0`, `v1.0.0` in descending SemVer order; `All:true` with empty name still returns latest per identity (CLI validation is covered in Task 2); type/name filters remain exact; description/tags are carried per version.
- [ ] **Step 2: Run focused tests and verify RED**
  Run `go test ./internal/lifecycle -run 'TestMakeCatalogListResult|TestListCatalog' -count=1`; expect the all-versions assertion to fail because current code always deduplicates.
- [ ] **Step 3: Implement minimal selection branch**
  In `makeCatalogListResult`, filter type/name first. If `flags.All && flags.Name != ""`, append every matching entry as `cli.ListedPower` and call existing `sortListedPowers`; otherwise retain current map-based latest selection. Add a Chinese comment documenting that `--all` is name-scoped version expansion.
- [ ] **Step 4: Run focused tests and verify GREEN**
  Re-run the command from Step 2; expect all focused tests to pass.
- [ ] **Step 5: Commit**
  `git add internal/lifecycle/list_result.go internal/lifecycle/list_result_test.go && git commit -m "feat(list): expand all versions for named powers"`

### Task 2: List CLI flag validation and identity-only rendering (TDD)

**Files:**
- Modify: `internal/cli/list.go`, `internal/cli/power_render.go`, `internal/cli/list_render.go`
- Test: `internal/cli/list_test.go`, `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: Task 1 `Flags.All` behavior and existing `ListLifecycle`.
- Produces: `list --all` validation; list renderers no longer use `All` as metadata switch.

- [ ] **Step 1: Write failing CLI tests**
  Add tests that `list --all` and `list -a` return an error containing `--all requires name` without factory calls; `list demo --all` passes `Flags{Name:"demo",All:true}` and renders only identity lines even when Description/Tags are populated; `list demo --json --all` emits an identity array without description/tags; installed `list --installed --agent=codex --all` preserves local exact records and does not add metadata.
- [ ] **Step 2: Run focused tests and verify RED**
  Run `go test ./internal/cli -run 'TestList|TestCLIParses' -count=1`; expect failures for missing validation and metadata still being rendered with `--all`.
- [ ] **Step 3: Implement validation and renderer changes**
  In `newListCommand`, after positional name parsing, reject `flags.All && flags.Name == ""` before `validateListFlags`/factory. Change `renderList` and installed render paths to identity-only metadata (`powerMetadata{}`), while preserving array/group formatting and JSON shape. Keep the `--all` flag registration/help text explicit about all versions for a named Cloud power.
- [ ] **Step 4: Run focused tests and verify GREEN**
  Re-run Step 2 tests, then `go test ./internal/cli -count=1`; expect pass.
- [ ] **Step 5: Commit**
  `git add internal/cli/list.go internal/cli/power_render.go internal/cli/list_render.go internal/cli/list_test.go internal/cli/cli_test.go && git commit -m "feat(list): scope all flag to named versions"`

### Task 3: Show lifecycle and result model (TDD)

**Files:**
- Create: `internal/cli/show_contract.go`, `internal/lifecycle/show.go`, `internal/lifecycle/show_result.go`
- Test: `internal/lifecycle/show_test.go`

**Interfaces:**
- Consumes: existing `CatalogManager`, `catalog.Snapshot.Resolve`, `transaction.Inspector`, `cli.Flags`.
- Produces: `cli.ShowResult`, `cli.ShowLifecycle`, `cli.ShowLifecycleFactory`, and `ShowService.Show(context.Context, cli.Flags) (cli.ShowResult,error)`.

- [ ] **Step 1: Write failing lifecycle tests**
  Add a snapshot fake with same-name entries across versions/types. Test latest selection for `{Name:"demo"}`, exact selection for `{Name:"demo",Type:"skill",Version:"v1.0.0"}`, `ErrAmbiguous` without type, `ErrNotFound` for missing name/version, invalid version rejection at lifecycle boundary, journal inspect before snapshot, and stale warning without changing result.
- [ ] **Step 2: Run focused tests and verify RED**
  Run `go test ./internal/lifecycle -run 'TestShow' -count=1`; expect compile/test failure because Show types do not exist.
- [ ] **Step 3: Implement Show service**
  Add documented interfaces and result carrying one `cli.ListedPower`/`domain.CatalogEntry` equivalent with Description/Tags. `ShowService` validates non-empty name, optional type via snapshot resolution, optional canonical version via `validateInstallVersion`, resolves through one immutable snapshot, and wraps not-found/ambiguity errors with `show catalog power` context. Perform `inspectJournal` and `warnStaleCatalog` exactly like Search/List.
- [ ] **Step 4: Run focused tests and verify GREEN**
  Re-run lifecycle Show tests and existing lifecycle package tests.
- [ ] **Step 5: Commit**
  `git add internal/cli/show_contract.go internal/lifecycle/show.go internal/lifecycle/show_result.go internal/lifecycle/show_test.go && git commit -m "feat(show): add catalog detail lifecycle"`

### Task 4: Show CLI command, rendering, and root factory wiring (TDD)

**Files:**
- Create: `internal/cli/show.go`, `internal/cli/show_render.go`, `internal/cli/show_test.go`
- Modify: `internal/cli/root.go`, `internal/cli/cli_test.go`, `cmd/apm/main.go` and its tests as needed for factory composition.

**Interfaces:**
- Consumes: Task 3 `ShowLifecycle`/`ShowResult`.
- Produces: `apm show <name> [--type] [--version] [--json]` and composition-root factory injection.

- [ ] **Step 1: Write failing CLI tests**
  Add tests for exact one name, trimming/empty rejection, invalid type/version before factory, unknown `--agent`, forwarding `Name/Type/Version/JSON`, text field order and tags output, JSON single object with tags array, and lifecycle/factory errors with Cobra usage. Add root help assertion for `show`.
- [ ] **Step 2: Run focused tests and verify RED**
  Run `go test ./internal/cli -run 'TestShow|TestCLIHelp' -count=1`; expect compile/test failure because command and root factory slot are absent.
- [ ] **Step 3: Implement command and renderer**
  Add `newShowCommand`, `renderShow`, and stable text/JSON encoders with Chinese comments on public interfaces/helpers. Extend `CLIEntry`/`New`/`newRootCommand` with `ShowLifecycleFactory`; register `show` alongside existing commands. Validate canonical name and version before factory; call lifecycle then render one result.
- [ ] **Step 4: Update composition root and test factories**
  Wire `cmd/apm` to construct `lifecycle.NewShow` from the shared inspector/catalog manager; update all test `cli.New` calls with an empty Show factory. Ensure existing command factories remain independent.
- [ ] **Step 5: Run focused tests and verify GREEN**
  Run `go test ./internal/cli ./cmd/apm -count=1` and fix only feature-related failures.
- [ ] **Step 6: Commit**
  `git add internal/cli cmd/apm && git commit -m "feat(show): expose catalog details command"`

### Task 5: Documentation, ADR, Changelog, and integration coverage

**Files:**
- Create: `ivory-tower/adr/ADR-028-list-all-versions-and-show.md`
- Modify: `ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md`, `ivory-tower/project-overview.md`, `BUILD.md`, `docs/Changelog.md`, relevant `cmd/apm` integration tests.

**Interfaces:**
- Consumes: completed CLI/lifecycle behavior from Tasks 1–4.
- Produces: documented command contract, ADR reference, examples, and regression acceptance evidence.

- [ ] **Step 1: Write failing integration/documentation assertions**
  Extend bundled/local catalog integration tests to assert named `list --all` includes historical versions, unnamed `list --all` fails, and `show` text/JSON returns latest and exact versions. Add documentation checks only where existing tests already inspect help/examples.
- [ ] **Step 2: Run focused integration tests and verify RED**
  Run `go test ./cmd/apm -run 'List|Show|Bundled|CLI' -count=1`; expect new assertions to fail until composition and behavior are integrated.
- [ ] **Step 3: Implement ADR and docs**
  Record ADR-028 with accepted decision, alternatives, compatibility impact, and references to Feature 029. Update ADR-002 list syntax/`--all` clauses, Project Overview scope/architecture, BUILD examples/help text. Add one reverse-chronological behavioral Changelog entry listing related commits and verification commands.
- [ ] **Step 4: Run full verification**
  Run `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l cmd internal`, `go build -o /dev/null ./cmd/apm`, and `git diff --check`; all must exit 0.
- [ ] **Step 5: Commit**
  `git add ivory-tower/adr/ADR-028-list-all-versions-and-show.md ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md ivory-tower/project-overview.md BUILD.md docs/Changelog.md cmd/apm && git commit -m "docs: record list all versions and show contract"`

## Review Checkpoints

- After each task, inspect the diff and run the task's focused tests before proceeding.
- Before final completion, re-read Feature 029 section-by-section against the implementation and run the full verification commands above.
- Any implementation decision that materially changes the approved spec must be recorded as a ruling and reflected in ADR-028 before completion.


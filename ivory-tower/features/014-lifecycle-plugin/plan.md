# Lifecycle Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 Codex、Claude Code 和 Hermes Agent 实现与 Skill 一致的版本化 Plugin 搜索、安装、升级、降级、卸载和可恢复 lifecycle。

**Architecture:** 新增独立 Plugin 领域模型、Cloud definition、类型专属 Adapter 和 `plugin` transaction driver，复用现有 CLI、锁、版本选择、清单 Repository、Cache 基础设施和唯一活动 journal。Codex/Claude 使用稳定全量 marketplace，Hermes 使用 Plugin Cache/Staging、原子目录发布和 CLI enable/disable；lifecycle/transaction 只按 deployment mode 分支，不按 Agent ID 分支。

**Tech Stack:** Go、Cobra、标准库 `os/exec`/`encoding/json`/`path/filepath`、Masterminds SemVer、现有 filesystem/lock/config/transaction 基础设施、Git、Mermaid/C4 文档。

## Global Constraints

- 规格权威来源是 `ivory-tower/features/014-lifecycle-plugin/spec.md`；实现不得缩减其中任何验收项。
- marketplace 稳定且无版本，包含所有插件的所有版本；`.claude-plugin/marketplace.json` 是 APM 唯一权威索引。
- 原生插件版本使用 `MAJOR.MINOR.PATCH`，APM `PowerRef` 使用精确 `vMAJOR.MINOR.PATCH`；实际名固定为 `<logical-name>-<canonical-apm-version>`。
- Codex/Claude 每次安装先 add 或定向 update Cloud marketplace；同名异源失败，marketplace 永不由 APM remove。
- Codex/Claude Plugin 不使用 APM Cache/Staging；Hermes 仅在确实需要发布目标时使用 Plugin Cache/Staging。
- Plugin 卸载不连接 Cloud、不更新 marketplace、不访问 Cache，只删除对应 `{type=plugin,name}` 清单项。
- APM 只判断 Plugin `present/absent`，不持久化、不恢复、不以 enabled/disabled 影响业务判断；APM 新安装默认启用。
- 安装前通过目标 Agent CLI Inspect；写操作后不追加 list 验证。
- Skill、MCP、Plugin 共用唯一 schema v1 journal；Plugin 使用互斥的 `DriverPlugin`/`PluginJournalPayload`，非法格式统一返回 `ErrInvalidJournal`。
- 自动化测试必须使用 fake CLI 和临时 Agent home，不得执行或修改真实 Codex、Claude、Hermes 环境。
- 所有重要导出接口和关键约束使用简短、规范的中文注释。
- 生产代码遵循 TDD；每个任务先观察聚焦测试按预期失败，再做最小实现并运行回归。
- 不修改既有 CLI 语法、渲染格式、config manifest schema 或版本选择外部契约。
- 行为实现、ADR/C4/当前状态文档和最终全量验证全部完成前，不写 `docs/Changelog.md`；完成后每个用户请求只写一条记录。

---

## File Structure

### Production units

| File | Responsibility |
|---|---|
| `internal/domain/plugin.go` | Plugin definition、deployment mode、版本规范化、逻辑名/实际名双向映射。 |
| `internal/cloud/local_plugin.go` | 只读 Claude marketplace 权威索引；Plugin List/GetPlugin/Cache source 定位。 |
| `internal/cloud/local_skill.go` | 承接从 LocalClient 聚合文件移出的既有 Skill Get。 |
| `internal/cache/filesystem.go` | Skill/Plugin 两类不可变 cache 到干净 staging 的物化。 |
| `internal/adapter/cli_runner.go` | MCP/Plugin 共用的受限 Agent CLI 执行器。 |
| `internal/adapter/plugin_adapter.go` | Plugin Adapter 接口、fact、target、mutation 与公共状态分类。 |
| `internal/adapter/plugin_codex.go` | Codex marketplace-backed Plugin Adapter。 |
| `internal/adapter/plugin_claude.go` | Claude user-scope marketplace-backed Plugin Adapter。 |
| `internal/adapter/plugin_hermes.go` | Hermes filesystem-backed Plugin Adapter。 |
| `internal/lifecycle/install_plugin.go` | Plugin 安装状态表、Cloud/Cache 编排、结果分类。 |
| `internal/lifecycle/uninstall_plugin.go` | Plugin 离线卸载和 target-missing 编排。 |
| `internal/transaction/plugin_install.go` | Plugin 安装/切换事务入口与 marketplace-backed 实现。 |
| `internal/transaction/plugin_uninstall.go` | Plugin 卸载事务入口与 marketplace-backed 实现。 |
| `internal/transaction/plugin_filesystem.go` | Hermes 原子发布、隔离、回滚和清理。 |
| `internal/transaction/plugin_recovery.go` | Plugin journal 的 CLI/路径恢复。 |

### Test units

| File | Responsibility |
|---|---|
| `internal/domain/plugin_test.go` | 纯映射和校验表。 |
| `internal/cloud/local_plugin_test.go` | marketplace 权威解析与精确 definition。 |
| `internal/adapter/plugin_adapter_test.go` | 公共 Plugin Adapter 契约。 |
| `internal/adapter/plugin_codex_test.go` | Codex argv/JSON/冲突。 |
| `internal/adapter/plugin_claude_test.go` | Claude argv/JSON/user scope/冲突。 |
| `internal/adapter/plugin_hermes_test.go` | Hermes list/enable/disable/路径。 |
| `internal/lifecycle/install_plugin_test.go` | 安装状态表和依赖调用顺序。 |
| `internal/lifecycle/uninstall_plugin_test.go` | 离线卸载状态表。 |
| `internal/lifecycle/plugin_integration_test.go` | 三 Agent fake CLI 端到端与环境隔离。 |
| `internal/transaction/plugin_install_test.go` | 安装/切换 phase 和 mutation 结果。 |
| `internal/transaction/plugin_uninstall_test.go` | 卸载 phase 和 target missing。 |
| `internal/transaction/plugin_recovery_test.go` | Plugin 恢复方向和跨类型恢复。 |

---

### Task 1: Add Plugin Domain Mapping and Cloud Authority Index

**Files:**
- Create: `internal/domain/plugin.go`
- Create: `internal/domain/plugin_test.go`
- Create: `internal/cloud/local_plugin.go`
- Create: `internal/cloud/local_plugin_test.go`
- Create: `internal/cloud/resources/plugins/.agents/plugins/marketplace.json`
- Create: `internal/cloud/resources/plugins/.claude-plugin/marketplace.json`
- Create: `internal/cloud/resources/plugins/plugins/demo-plugin-v0.0.1/.codex-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/plugins/demo-plugin-v0.0.1/.claude-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/plugins/demo-plugin-v0.0.1/skills/demo-plugin/SKILL.md`
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_client.go`
- Test: `internal/cloud/local_client_test.go`

**Interfaces:**
- Consumes: `domain.PowerRef`, `domain.PowerTypePlugin`, `cloud.ErrNotFound`, `semver.NewVersion`.
- Produces:

```go
type PluginDeploymentMode string

const (
    PluginDeploymentMarketplace PluginDeploymentMode = "marketplace"
    PluginDeploymentFilesystem  PluginDeploymentMode = "filesystem"
)

type MarketplaceDefinition struct {
    Name   string
    Source string
}

type PluginDefinition struct {
    ActualName  string
    Marketplace MarketplaceDefinition
}

func ActualPluginName(ref PowerRef) (string, error)
func PluginRefFromMarketplace(actualName, nativeVersion string) (PowerRef, error)
func (definition PluginDefinition) Validate() error

type cloud.Client interface {
    List() ([]domain.PowerRef, error)
    Get(domain.PowerRef) error
    GetMCP(domain.PowerRef) (domain.MCPServer, error)
    GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
}
```

- [ ] **Step 1: Write failing domain mapping tests**

```go
func TestPluginNameMappingRoundTrips(t *testing.T) {
    ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.0.1"}
    actual, err := domain.ActualPluginName(ref)
    if err != nil || actual != "demo-plugin-v0.0.1" {
        t.Fatalf("ActualPluginName() = %q, %v", actual, err)
    }
    got, err := domain.PluginRefFromMarketplace(actual, "0.0.1")
    if err != nil || got != ref {
        t.Fatalf("PluginRefFromMarketplace() = %#v, %v", got, err)
    }
}

func TestPluginNameMappingRejectsUnsafeOrNonCanonicalValues(t *testing.T) {
    cases := []domain.PowerRef{
        {Name: "../demo", Type: domain.PowerTypePlugin, Version: "v0.0.1"},
        {Name: "demo", Type: domain.PowerTypeSkill, Version: "v0.0.1"},
        {Name: "demo", Type: domain.PowerTypePlugin, Version: "0.0.1"},
    }
    for _, ref := range cases {
        if _, err := domain.ActualPluginName(ref); err == nil {
            t.Fatalf("ActualPluginName(%#v) error = nil", ref)
        }
    }
}
```

- [ ] **Step 2: Run domain tests and observe the missing API failure**

Run: `go test -count=1 ./internal/domain -run Plugin`

Expected: FAIL because `ActualPluginName`, `PluginRefFromMarketplace`, and Plugin definition types are undefined.

- [ ] **Step 3: Implement the domain model and exact reversible mapping**

Use `semver.StrictNewVersion(strings.TrimPrefix(ref.Version, "v"))`, require exact reconstruction `"v"+parsed.String() == ref.Version`, validate one safe path segment, and strip only the exact `"-v"+parsed.String()` suffix from marketplace names. Return errors wrapping a new domain-local validation error only if the package already uses domain sentinels; otherwise return contextual `fmt.Errorf` values and let Adapter expose public sentinels.

- [ ] **Step 4: Write failing Cloud marketplace tests**

Create fixtures under `t.TempDir()` and assert:

```go
func TestLocalClientListsPluginsFromClaudeMarketplaceOnly(t *testing.T) {
    // Write .claude-plugin/marketplace.json with demo-v0.0.1 and demo-v0.1.0.
    // Write a deliberately unrelated Codex marketplace entry.
    // Assert List returns only logical plugin/demo at v0.0.1 and v0.1.0,
    // sorted with existing Skill/MCP refs by Type/Name/Version.
}

func TestLocalClientGetPluginReturnsActualNameAndMarketplace(t *testing.T) {
    // Assert ActualName, marketplace name, and the absolute resource-root source.
}
```

Also cover missing index as empty list, exact ref miss as `cloud.ErrNotFound`, malformed JSON, duplicate logical ref, invalid native version, unsafe source, and symlink source.

- [ ] **Step 5: Run Cloud tests and observe missing Plugin discovery**

Run: `go test -count=1 ./internal/cloud -run 'Plugin|List'`

Expected: FAIL because List does not read Plugin marketplace and `GetPlugin` is absent.

- [ ] **Step 6: Implement the minimal authority-index reader and GetPlugin**

Define unexported decode structs with `name`, `plugins[].name`, `plugins[].version`, and `plugins[].source`. Allow unknown native fields, reject a second JSON value, resolve local `source` beneath the physical marketplace root, and keep Codex marketplace unread. Make `List()` append Plugin refs and preserve stable Type/Name/Version sorting.

- [ ] **Step 7: Add the minimal dual-marketplace demo resource**

Use marketplace name `apm`, actual plugin name `demo-plugin-v0.0.1`, native version `0.0.1`, source `./plugins/demo-plugin-v0.0.1`, and one minimal APM-owned skill whose frontmatter name is `demo-plugin`.

- [ ] **Step 8: Run focused and package regression tests**

Run: `go test -count=1 ./internal/domain ./internal/cloud`

Expected: PASS.

- [ ] **Step 9: Commit the domain and Cloud discovery slice**

```bash
git add internal/domain/plugin.go internal/domain/plugin_test.go internal/cloud/client.go internal/cloud/local_client.go internal/cloud/local_client_test.go internal/cloud/local_plugin.go internal/cloud/local_plugin_test.go internal/cloud/resources/plugins
git commit -m "feat(plugin): add cloud marketplace discovery"
```

---

### Task 2: Materialize Hermes Plugin Resources through Cache and Staging

**Files:**
- Create: `internal/cloud/local_skill.go`
- Modify: `internal/cloud/local_client.go`
- Modify: `internal/cloud/local_plugin.go`
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/filesystem_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`
- Test: `internal/cloud/local_plugin_test.go`

**Interfaces:**
- Consumes: `cloud.Client.Get(domain.PowerRef) error`, `cache.PowerCache.Fetch(domain.PowerRef) (string, error)`, `filesystem.CopyDir`.
- Produces:

```text
cache/plugins/<logical-name>/<vMAJOR.MINOR.PATCH>
staging/plugins/<logical-name>
```

- [ ] **Step 1: Write failing Plugin cache/staging tests**

Add a table proving Skill still stages at `staging/skills/<name>`, Plugin stages at `staging/plugins/<name>`, MCP returns `cache.ErrMiss`, and every Fetch removes unrelated previous staging content. Add an integration sequence:

```text
Fetch(plugin) -> ErrMiss
Cloud.Get(plugin) -> nil
Fetch(plugin) -> canonical staging root
```

Assert the staged leaf contains both plugin manifests and `skills/demo-plugin/SKILL.md`.

- [ ] **Step 2: Run cache tests and observe Plugin miss**

Run: `go test -count=1 ./internal/cache ./internal/cloud -run 'Plugin|Cache|Fetch'`

Expected: FAIL because Cloud.Get and Cache.Fetch reject Plugin.

- [ ] **Step 3: Move existing Skill Get into a focused file without changing behavior**

Move the current Skill copy implementation from `local_client.go` to `getSkill(ref)` in `local_skill.go`. Keep LocalClient.Get as a small type switch:

```go
switch ref.Type {
case domain.PowerTypeSkill:
    return c.getSkill(ref)
case domain.PowerTypePlugin:
    return c.getPlugin(ref)
default:
    return ErrNotFound
}
```

- [ ] **Step 4: Implement atomic Plugin cache population**

`getPlugin` must reuse the exact authority entry selected by `GetPlugin`, copy its source into `cache/.tmp/plugins/<logical>/<version>`, and rename to `cache/plugins/<logical>/<version>`. Existing final cache content is an idempotent hit. Cleanup only the configured cache temp root.

- [ ] **Step 5: Extend FilesystemCache Fetch by explicit type directory**

Map Skill to `skills`, Plugin to `plugins`, and reject MCP/unknown types. Preserve canonical staging validation, clean-build behavior, source error context, atomic staging rename, and the returned staging root.

- [ ] **Step 6: Run focused and regression tests**

Run: `go test -count=1 ./internal/cache ./internal/cloud`

Expected: PASS, including unchanged Skill tests.

- [ ] **Step 7: Commit the resource materialization slice**

```bash
git add internal/cloud/local_client.go internal/cloud/local_skill.go internal/cloud/local_plugin.go internal/cloud/local_plugin_test.go internal/cloud/local_cache_integration_test.go internal/cache/filesystem.go internal/cache/filesystem_test.go
git commit -m "feat(plugin): stage hermes plugin resources"
```

---

### Task 3: Generalize the Agent CLI Runner and Define Plugin Adapter Contracts

**Files:**
- Rename: `internal/adapter/mcp_runner.go` -> `internal/adapter/cli_runner.go`
- Rename: `internal/adapter/mcp_runner_test.go` -> `internal/adapter/cli_runner_test.go`
- Create: `internal/adapter/plugin_adapter.go`
- Create: `internal/adapter/plugin_adapter_test.go`
- Modify: `internal/adapter/mcp_adapter.go`
- Modify: `internal/adapter/mcp_codex.go`
- Modify: `internal/adapter/mcp_claude.go`
- Modify: `internal/adapter/mcp_hermes.go`
- Modify: `internal/adapter/mcp_agent_test_helpers_test.go`
- Modify: `internal/adapter/errors.go`

**Interfaces:**
- Consumes: existing MCP command runner behavior unchanged.
- Produces:

```go
type agentCLIResult struct {
    Path            string
    Stdout          string
    Stderr          string
    ExitCode        int
    Started         bool
    TimedOut        bool
    StdoutTruncated bool
    StderrTruncated bool
}

type agentCLIRunner interface {
    Run(context.Context, string, []string, string) (agentCLIResult, error)
}

type PluginPresence string
const (
    PluginAbsent  PluginPresence = "absent"
    PluginPresent PluginPresence = "present"
)

type PluginFact struct {
    Presence   PluginPresence
    ActualName string
    Selector   string
}

type PluginTarget struct {
    ActualName string
    Selector   string
}

type PluginMutationState string
type PluginMutationResult struct { State PluginMutationState }

type PluginAdapter interface {
    DeploymentMode() domain.PluginDeploymentMode
    PrepareInstall(context.Context, domain.PluginDefinition) error
    Inspect(context.Context, string) (PluginFact, error)
    Add(context.Context, PluginTarget) (PluginMutationResult, error)
    Remove(context.Context, PluginTarget) (PluginMutationResult, error)
    TargetPath(string) (string, error)
}
```

- [ ] **Step 1: Rename runner tests and make them demand generic names**

Update the existing timeout, process-group kill, bounded output, start failure, exit code and stdin tests to construct `agentCLICommandRunner` through `newDefaultAgentCLIRunner`. Keep all numeric timeout/output-limit assertions unchanged.

- [ ] **Step 2: Run Adapter tests and observe compile failures**

Run: `go test -count=1 ./internal/adapter -run 'Runner|MCP'`

Expected: FAIL until all MCP code uses the generic runner names.

- [ ] **Step 3: Rename runner types with no behavioral changes**

Use `git mv`, rename only identifiers, and keep default timeout `30*time.Second`, output limit `64*1024`, dynamic `exec.LookPath`, process-group cancellation and error wrapping exactly equivalent.

- [ ] **Step 4: Run all existing MCP Adapter tests**

Run: `go test -count=1 ./internal/adapter -run 'MCP|Runner'`

Expected: PASS, proving the refactor did not change MCP behavior.

- [ ] **Step 5: Write failing Plugin contract validation tests**

Cover empty/unsafe actual names, absent fact with a selector, present fact without a selector, invalid deployment mode, mutation state outside the declared enum, and typed-nil adapters. Assert new sentinels through `errors.Is`:

```go
ErrInvalidPlugin
ErrMarketplaceConflict
ErrPluginConflict
ErrPluginStateUnknown
ErrPluginMutationUnknown
```

- [ ] **Step 6: Implement Plugin contract types and validation helpers**

Keep command JSON parsing out of this file. Add helpers that classify a started successful command as applied, a command known not to start as not-applied, and a started non-confirmed/truncated command as unknown with operation/CLI/diagnostic context.

- [ ] **Step 7: Run Adapter package tests**

Run: `go test -count=1 ./internal/adapter`

Expected: PASS.

- [ ] **Step 8: Commit the shared CLI and contract slice**

```bash
git add internal/adapter
git commit -m "refactor(adapter): share agent cli runner"
```

---

### Task 4: Implement Codex and Claude Marketplace-Backed Plugin Adapters

**Files:**
- Create: `internal/adapter/plugin_codex.go`
- Create: `internal/adapter/plugin_codex_test.go`
- Create: `internal/adapter/plugin_claude.go`
- Create: `internal/adapter/plugin_claude_test.go`
- Modify: `internal/adapter/plugin_adapter.go`

**Interfaces:**
- Consumes: `agentCLIRunner`, `domain.PluginDefinition`, `PluginFact`, `PluginTarget`, Plugin sentinels.
- Produces:

```go
func newCodexPluginAdapter(agentCLIRunner) PluginAdapter
func newClaudePluginAdapter(agentCLIRunner) PluginAdapter
```

- [ ] **Step 1: Write failing Codex command-contract tests**

Use a recording runner and assert exact calls for absent marketplace add, matching marketplace upgrade, source conflict, plugin inspect, add, and remove:

```text
codex plugin marketplace list --json
codex plugin marketplace add /srv/apm-marketplace --json
codex plugin marketplace upgrade apm --json
codex plugin list --json
codex plugin add demo-plugin-v0.0.1@apm --json
codex plugin remove demo-plugin-v0.0.1@apm --json
```

Test local-path absolute clean comparison, exact non-local source comparison, missing source fact, duplicate actual-name selectors, disabled installed entry remaining present, truncated JSON, nonzero exit and timeout.

- [ ] **Step 2: Write failing Claude command-contract tests**

Assert exact user-scope calls:

```text
claude plugin marketplace list --json
claude plugin marketplace add /srv/apm-marketplace --scope user
claude plugin marketplace update apm
claude plugin list --json
claude plugin install demo-plugin-v0.0.1@apm --scope user
claude plugin uninstall demo-plugin-v0.0.1@apm --scope user
```

Cover the same conflict/unknown/presence cases using Claude's `id`, `enabled`, and marketplace JSON shapes.

- [ ] **Step 3: Run focused tests and observe missing adapters**

Run: `go test -count=1 ./internal/adapter -run 'CodexPlugin|ClaudePlugin'`

Expected: FAIL because constructors and methods are undefined.

- [ ] **Step 4: Implement Codex Adapter**

Parse marketplace list and installed plugin list with JSON decoders that reject trailing values. `Inspect(actualName)` scans installed entries by exact `name`; zero matches is absent, one yields its exact `pluginId`, more than one returns `ErrPluginStateUnknown`. Ignore enabled. Use only the Cloud-specified marketplace name for upgrade.

- [ ] **Step 5: Implement Claude Adapter**

Mirror the same public contract while preserving Claude argv and user scope. `Inspect` returns the exact installed `id`; it does not reconstruct selector from marketplace source.

- [ ] **Step 6: Run Adapter package tests**

Run: `go test -count=1 ./internal/adapter`

Expected: PASS, with all MCP tests still green.

- [ ] **Step 7: Commit marketplace-backed adapters**

```bash
git add internal/adapter/plugin_adapter.go internal/adapter/plugin_codex.go internal/adapter/plugin_codex_test.go internal/adapter/plugin_claude.go internal/adapter/plugin_claude_test.go
git commit -m "feat(plugin): add codex and claude adapters"
```

---

### Task 5: Implement Hermes Plugin Adapter and Registry Resolution

**Files:**
- Create: `internal/adapter/plugin_hermes.go`
- Create: `internal/adapter/plugin_hermes_test.go`
- Modify: `internal/adapter/config.go`
- Modify: `internal/adapter/config_test.go`
- Modify: `internal/adapter/registry.go`
- Modify: `internal/adapter/registry_test.go`

**Interfaces:**
- Consumes: `agentConfig.rootPath`, `PluginAdapter`.
- Produces:

```go
func newHermesPluginAdapter(agentCLIRunner, agentConfig) PluginAdapter
func (r *Registry) ResolvePlugin(domain.AgentID) (PluginAdapter, error)
```

- [ ] **Step 1: Write failing Hermes Adapter tests**

Assert:

```text
hermes plugins list --json
hermes plugins enable demo-plugin-v0.0.1
hermes plugins disable demo-plugin-v0.0.1
```

List JSON with `status: enabled` and `status: disabled` must both return present; absent actual name returns absent. `PrepareInstall` must not call the runner. `DeploymentMode` must be filesystem. `TargetPath` must use `HERMES_HOME/plugins/<actual-name>` and reject unsafe actual names before path I/O.

- [ ] **Step 2: Write failing Registry tests**

For Codex, Claude and Hermes, assert non-nil `ResolveSkill`, `ResolveMCP`, and `ResolvePlugin`; unknown Agent must return `ErrUnsupportedAdapter` from all three. Preserve lexical RefreshInstalled order and zero Plugin CLI calls during refresh.

- [ ] **Step 3: Run focused tests and observe missing Hermes/Registry behavior**

Run: `go test -count=1 ./internal/adapter -run 'HermesPlugin|Registry|AgentConfig'`

Expected: FAIL.

- [ ] **Step 4: Implement Hermes Adapter**

Use list JSON only for exact-name presence and ignore status/version. Treat successful enable/disable as applied; preserve started/truncated/timeout facts through common mutation helpers. Do not invoke `hermes plugins install` or `remove`.

- [ ] **Step 5: Register all Plugin Adapters with one shared runner**

Add `pluginByAgent` without merging maps. Construct Codex/Claude/Hermes Plugin Adapters alongside existing Skill/MCP adapters; keep `ordered` and RefreshInstalled logic unchanged.

- [ ] **Step 6: Run Adapter regression tests**

Run: `go test -count=1 ./internal/adapter`

Expected: PASS.

- [ ] **Step 7: Commit Hermes and Registry support**

```bash
git add internal/adapter/plugin_hermes.go internal/adapter/plugin_hermes_test.go internal/adapter/config.go internal/adapter/config_test.go internal/adapter/registry.go internal/adapter/registry_test.go
git commit -m "feat(plugin): add hermes adapter registry"
```

---

### Task 6: Add Plugin Journal Schema and Transaction Contracts

**Files:**
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/validation_test.go`
- Modify: `internal/transaction/journal_schema_test.go`

**Interfaces:**
- Consumes: `adapter.PluginAdapter`, `adapter.PluginTarget`, `domain.PluginDefinition`.
- Produces:

```go
const DriverPlugin Driver = "plugin"

type PluginFilesystemJournalPayload struct {
    StagingRoot           string `json:"staging_root,omitempty"`
    SourcePath            string `json:"source_path,omitempty"`
    TargetPath            string `json:"target_path,omitempty"`
    PreviousTargetPath    string `json:"previous_target_path,omitempty"`
    TransactionDir        string `json:"transaction_dir,omitempty"`
    PublishPath           string `json:"publish_path,omitempty"`
    PreviousQuarantinePath string `json:"previous_quarantine_path,omitempty"`
    TargetQuarantinePath  string `json:"target_quarantine_path,omitempty"`
}

type PluginJournalPayload struct {
    Deployment      domain.PluginDeploymentMode       `json:"deployment"`
    Target          adapter.PluginTarget               `json:"target"`
    Previous        *adapter.PluginTarget              `json:"previous,omitempty"`
    TargetPresent   bool                               `json:"target_present"`
    PreviousPresent bool                               `json:"previous_present"`
    Filesystem      *PluginFilesystemJournalPayload   `json:"filesystem,omitempty"`
}

type PluginInstallRequest struct {
    Agent       domain.AgentID
    Power       domain.PowerRef
    Previous    *domain.PowerRef
    Definition  domain.PluginDefinition
    StagingRoot string
    Adapter     adapter.PluginAdapter
}

type PluginUninstallRequest struct {
    Agent      domain.AgentID
    Power      domain.PowerRef
    ActualName string
    Adapter    adapter.PluginAdapter
}

type PluginAdapterResolver interface {
    ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
}
```

`Manager` adds `InstallPlugin(context.Context, PluginInstallRequest) error` and `UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)`. `NewManager(manifests, registry)` keeps the same call shape by requiring the registry to implement both MCP and Plugin resolvers.

- [ ] **Step 1: Write failing journal envelope tests**

Add a valid marketplace install, valid filesystem update and valid uninstall fixture. Add invalid cases for: wrong Power type, missing Plugin payload, Plugin plus Filesystem/AgentCLI payload, unknown deployment, marketplace with filesystem paths, filesystem without exact paths, first install with previous target, update without matching previous logical ref, invalid phase, unsafe selector, and schema other than 1. Every invalid decode/validation path must satisfy `errors.Is(err, ErrInvalidJournal)`.

- [ ] **Step 2: Run journal tests and observe missing Plugin schema**

Run: `go test -count=1 ./internal/transaction -run 'Journal|Validate'`

Expected: FAIL because DriverPlugin and Plugin payload are undefined.

- [ ] **Step 3: Implement request and journal types**

Add Chinese comments for new exported interfaces and request types. Clone pointer fields when creating journals. Keep existing Skill/MCP JSON keys and schema literals unchanged.

- [ ] **Step 4: Implement strict Plugin validation**

Validate exact allowed phase sets:

```text
install: removing-old, adding-new, rolling-back, committing
uninstall: removing, rolling-back, committing
```

For filesystem payload, derive the expected source as `<canonical-staging>/plugins/<logical-name>`, expected transaction root beneath the Hermes plugins parent, and exact artifact leaf names. Reject all non-applicable non-empty fields.

- [ ] **Step 5: Add Plugin resolver storage without changing recovery routing**

Extend Service/NewManager dependency validation so the unchanged constructor receives both MCP and Plugin resolver capabilities from Registry. Do not route `DriverPlugin` in `Recover` in this contract-only task; Task 7 adds routing together with the complete recovery implementation and its behavioral tests.

- [ ] **Step 6: Run transaction contract tests**

Run: `go test -count=1 ./internal/transaction -run 'Journal|Validate|Manager'`

Expected: PASS for schema, request and dependency validation tests; existing Skill/MCP recovery tests remain unchanged.

- [ ] **Step 7: Run repository compile regression**

Run: `go test -run '^$' ./...`

Expected: PASS after updating existing test fakes with `ResolvePlugin`, `InstallPlugin`, and `UninstallPlugin` methods that fail tests if unexpectedly called.

- [ ] **Step 8: Commit transaction contracts**

```bash
git add internal/transaction internal/lifecycle internal/adapter/registry_test.go cmd/apm/main_test.go
git commit -m "feat(plugin): define recoverable transactions"
```

---

### Task 7: Implement Marketplace-Backed Plugin Transactions and Recovery

**Files:**
- Create: `internal/transaction/plugin_install.go`
- Create: `internal/transaction/plugin_install_test.go`
- Create: `internal/transaction/plugin_uninstall.go`
- Create: `internal/transaction/plugin_uninstall_test.go`
- Create: `internal/transaction/plugin_recovery.go`
- Create: `internal/transaction/plugin_recovery_test.go`
- Modify: `internal/transaction/recovery.go`
- Modify: `internal/transaction/journal.go`

**Interfaces:**
- Consumes: Task 6 request/payload contracts and Adapter mutation states.
- Produces: working marketplace deployment for `InstallPlugin`, `UninstallPlugin`, and `recoverPlugin` without Cloud access.

- [ ] **Step 1: Write failing first-install and update transaction tests**

Assert exact event order:

```text
first install: inspect-target, journal:create(adding-new), add-target,
               journal:update(committing), manifest:set, journal:delete

update: inspect-target, inspect-previous, journal:create(removing-old),
        remove-previous, journal:update(adding-new), add-target,
        journal:update(committing), manifest:set, journal:delete
```

Cover same-version absent repair, unmanaged target conflict, old target absent, context cancellation, manifest failure, journal create/update/delete failure, and every mutation state.

- [ ] **Step 2: Run install tests and observe missing behavior**

Run: `go test -count=1 ./internal/transaction -run 'PluginInstall'`

Expected: FAIL.

- [ ] **Step 3: Implement marketplace install/update state machine**

Re-inspect target and previous immediately before journal creation. Build target selector from Cloud definition and actual name; obtain previous selector only from Adapter Inspect. Never call `PrepareInstall`, Cloud or Cache in transaction. On explicit not-applied, persist rolling-back and restore only an originally present previous plugin. On unknown, retain journal.

- [ ] **Step 4: Write failing uninstall transaction tests**

Assert absent target removes one manifest entry with `TargetMissing=true` and no journal. Present target order is inspect, create removing journal, remove, update committing, manifest remove, journal delete. Cover unknown/not-applied, cancellation and manifest/journal failures.

- [ ] **Step 5: Implement marketplace uninstall**

Use the exact selector returned by transaction-time Inspect. Never reconstruct marketplace identity from Cloud or a constant.

- [ ] **Step 6: Write failing marketplace recovery direction tests**

Cover every table row from the spec. Assert `adding-new + target present` commits, `adding-new + absent` rolls back, `removing-old` rolls back, uninstall `removing + absent` commits, and committing never calls Adapter mutation. Verify resolver receives journal Agent and no Cloud dependency exists.

- [ ] **Step 7: Implement marketplace recovery**

Persist `rolling-back` or `committing` before recovery mutations. Inspect exact actual names. Append Plugin-specific structured RecoveryAction kinds for add/remove/restore, then commit/remove manifest and delete journal.

- [ ] **Step 8: Run focused and transaction regression tests**

Run: `go test -count=1 ./internal/transaction -run 'Plugin|MCP|Journal|Recovery'`

Expected: PASS.

- [ ] **Step 9: Commit marketplace transactions**

```bash
git add internal/transaction/plugin_install.go internal/transaction/plugin_install_test.go internal/transaction/plugin_uninstall.go internal/transaction/plugin_uninstall_test.go internal/transaction/plugin_recovery.go internal/transaction/plugin_recovery_test.go internal/transaction/recovery.go internal/transaction/journal.go
git commit -m "feat(plugin): transact marketplace installs"
```

---

### Task 8: Implement Hermes Filesystem Plugin Transactions and Recovery

**Files:**
- Create: `internal/transaction/plugin_filesystem.go`
- Modify: `internal/transaction/plugin_install.go`
- Modify: `internal/transaction/plugin_install_test.go`
- Modify: `internal/transaction/plugin_uninstall.go`
- Modify: `internal/transaction/plugin_uninstall_test.go`
- Modify: `internal/transaction/plugin_recovery.go`
- Modify: `internal/transaction/plugin_recovery_test.go`
- Modify: `internal/transaction/artifacts.go`
- Modify: `internal/transaction/artifacts_test.go`

**Interfaces:**
- Consumes: filesystem deployment mode, canonical Plugin staging contract, `fileOps`, `filesystem.CopyDir` semantics.
- Produces: Hermes same-parent private artifact layout and full rollback/commit recovery.

- [ ] **Step 1: Write failing Hermes first-install filesystem tests**

Use temporary staging and Hermes roots. Assert order and artifacts:

```text
inspect target absent
journal adding-new
copy staging/plugins/demo-plugin to .apm-transactions/<id>/publish
rename publish to plugins/demo-plugin-v0.0.1
enable actual name
journal committing
manifest set
staging/transaction cleanup
journal delete
```

Inject copy failure, target-appeared race, rename failure, enable not-applied/unknown, cleanup failure and context cancellation.

- [ ] **Step 2: Write failing Hermes update and uninstall tests**

Update must disable and quarantine an originally present old directory before publishing/enabling new. Old absent skips disable/quarantine. Rollback removes the new target, restores only originally present old quarantine, enables the restored old name, and retains the old manifest. Uninstall must disable, quarantine, commit manifest removal and delete quarantine.

- [ ] **Step 3: Run filesystem Plugin tests and observe missing mode implementation**

Run: `go test -count=1 ./internal/transaction -run 'Plugin.*Filesystem|HermesPlugin'`

Expected: FAIL.

- [ ] **Step 4: Implement exact private artifact paths**

Use:

```text
<plugins-root>/.apm-transactions/<id>/publish
<plugins-root>/.apm-transactions/<id>/previous
<plugins-root>/.apm-transactions/<id>/target
```

Create/chmod only physical directories, reject symlinks, require all paths to share the validated physical plugins parent, and remove the shared `.apm-transactions` root only when empty.

- [ ] **Step 5: Implement filesystem install/update/uninstall branches**

Keep deployment dispatch in the transaction entry methods. Do not call marketplace Adapter operations for filesystem publication except `Add` after target publication and `Remove` before quarantine.

- [ ] **Step 6: Write failing Hermes recovery tests**

Cover interruptions before/after old quarantine, after publish before enable, after enable before committing, committing before manifest, uninstall before/after quarantine, both target and quarantine existing, missing staging, symlink artifact and repeated cleanup. Assert recovery never calls Cloud/Cache.

- [ ] **Step 7: Implement filesystem recovery and structured actions**

For `adding-new + target present`, call Adapter Add to ensure the APM install default is enabled, then commit. For rollback, disable/remove new target if present, restore old only when `PreviousPresent`, enable restored old, clean exact artifacts and delete journal. Preserve journal on any indeterminate CLI/path state.

- [ ] **Step 8: Run transaction package and race tests**

Run:

```bash
go test -count=1 ./internal/transaction
go test -race -count=1 ./internal/transaction
```

Expected: PASS.

- [ ] **Step 9: Commit filesystem transactions**

```bash
git add internal/transaction
git commit -m "feat(plugin): transact hermes plugin files"
```

---

### Task 9: Wire Plugin Business State into Install and Uninstall Lifecycle

**Files:**
- Create: `internal/lifecycle/install_plugin.go`
- Create: `internal/lifecycle/install_plugin_test.go`
- Create: `internal/lifecycle/uninstall_plugin.go`
- Create: `internal/lifecycle/uninstall_plugin_test.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/uninstall_agent.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/uninstall_test.go`
- Modify: `internal/lifecycle/list_test.go`
- Modify: `internal/lifecycle/search_test.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `CloudClient.GetPlugin`, `AdapterRegistry.ResolvePlugin`, `TransactionManager.InstallPlugin/UninstallPlugin`, Plugin cache and version selector.
- Produces: live Plugin branches behind existing CLI lifecycle contracts.

- [ ] **Step 1: Write failing install state-table tests**

Build fakes recording Cloud, prepare, inspect, cache and transaction events. Cover every spec row and assert:

- `GetPlugin -> PrepareInstall -> Inspect` order;
- unchanged still prepares marketplace but skips Cache/transaction;
- unmanaged target returns joined `ErrPluginConflict`/`ErrTargetConflict`;
- same-version absent calls transaction and reports unchanged;
- upgrade/downgrade target conflict stops before Cache/transaction;
- marketplace mode never calls Cache;
- filesystem mode calls Fetch/Get/Fetch only when target publication is required.

- [ ] **Step 2: Run install lifecycle tests and observe unsupported Plugin**

Run: `go test -count=1 ./internal/lifecycle -run 'InstallPlugin|InstallOne'`

Expected: FAIL because Plugin still routes to `ErrUnsupportedAdapter`.

- [ ] **Step 3: Implement installPlugin and dispatch**

Keep `fetchInstallStaging` generic for Skill/Plugin. Construct `PluginInstallRequest` with a cloned Previous ref and staging only for filesystem deployment. Determine outcome after transaction success with existing SemVer comparison.

- [ ] **Step 4: Write failing offline uninstall tests**

Assert exact order `manifest list/resolve -> ResolvePlugin -> ActualPluginName -> Inspect -> transaction`. Cloud and Cache fakes must panic if called. Cover target absent warning/result, selector ambiguity, one-entry manifest deletion, `--agent=all` fail-fast and preservation of other Plugin/Skill/MCP entries.

- [ ] **Step 5: Implement uninstallPlugin and dispatch**

Do not add Plugin-specific Cloud fields to `UninstallService`. Reuse existing target-missing Warn via `UninstallResult.TargetMissing`.

- [ ] **Step 6: Update interface fakes and composition tests**

Add no-op/fail-fast Plugin methods to existing lifecycle fake managers/registries. Extend the bundled local Cloud composition test to prove Plugin appears in Search/List without executing a real Agent CLI.

- [ ] **Step 7: Run lifecycle/CLI composition regression**

Run:

```bash
go test -count=1 ./internal/lifecycle ./internal/cli ./cmd/apm
go build -o /dev/null ./cmd/apm
```

Expected: PASS.

- [ ] **Step 8: Commit lifecycle wiring**

```bash
git add internal/lifecycle cmd/apm/main_test.go
git commit -m "feat(plugin): wire lifecycle install uninstall"
```

---

### Task 10: Add Fully Isolated Three-Agent Plugin Integration Coverage

**Files:**
- Create: `internal/lifecycle/plugin_integration_test.go`
- Modify: `internal/lifecycle/mcp_integration_test.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: production Local Cloud, Cache, Registry, Transaction Manager and lifecycle constructors.
- Produces: black-box evidence that all Plugin mutations stay inside fake Agent environments.

- [ ] **Step 1: Build fake CLI state machines in the new integration test**

Create executable scripts or test helper binaries named `codex`, `claude`, and `hermes` under `t.TempDir()/fake-bin`. Each must persist marketplace/plugin state only under its temporary Agent home and emit the exact JSON shapes consumed by its Adapter. Record every resolved executable path and argv.

- [ ] **Step 2: Write failing end-to-end scenarios**

For each Agent run through service APIs:

```text
install demo-plugin=v0.0.1
install demo-plugin=v0.0.1 -> unchanged
install demo-plugin=v0.1.0 -> upgraded (test fixture adds the second version)
install demo-plugin=v0.0.1 -> downgraded
uninstall demo-plugin -> completed
```

Add unmanaged target, target-missing manifest repair, marketplace source conflict, interrupted journal recovered by a subsequent Search, and multi-Agent fail-fast. For Hermes assert versioned directories and staging cleanup; for Codex/Claude assert Plugin Cache/Staging remain absent.

- [ ] **Step 3: Run integration tests and observe fixture/behavior gaps**

Run: `go test -count=1 ./internal/lifecycle -run 'PluginIntegration' -v`

Expected: FAIL until the second demo marketplace version and all fake command paths are complete.

- [ ] **Step 4: Add the v0.1.0 demo resource to both marketplaces**

Create a second actual plugin directory `demo-plugin-v0.1.0` with native version `0.1.0`, duplicated real files rather than symlinks, and update both marketplace files. Keep logical skill content minimal and version-independent.

- [ ] **Step 5: Make all integration scenarios pass without production shortcuts**

Fix only production behavior or fake CLI fidelity demonstrated by the failing scenario. Do not bypass Adapter JSON parsing, transaction journal or filesystem publication in tests.

- [ ] **Step 6: Prove real environment isolation**

Assert:

- every executed program path has prefix `fake-bin`;
- `APM_HOME`, `XDG_CACHE_HOME`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `HERMES_HOME` all have temporary absolute values;
- no recorded argv contains `/home/liangff/.local/bin/hermes`;
- test-created paths remain beneath their `t.TempDir()` roots.

- [ ] **Step 7: Run integration and broad race tests**

Run:

```bash
go test -count=1 ./internal/lifecycle -run 'PluginIntegration|MCPIntegration' -v
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
```

Expected: PASS.

- [ ] **Step 8: Commit integration coverage**

```bash
git add internal/cloud/resources/plugins internal/lifecycle/plugin_integration_test.go internal/lifecycle/mcp_integration_test.go cmd/apm/main_test.go
git commit -m "test(plugin): cover isolated agent lifecycle"
```

---

### Task 11: Record ADR-012, Align Current Architecture Docs, and Verify Completion

**Files:**
- Create: `ivory-tower/adr/ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md`
- Create: `ivory-tower/features/014-lifecycle-plugin/verification.md`
- Modify: `ivory-tower/adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/common-journal-recovery.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/uninstall.mmd`
- Modify: `ivory-tower/features/006-agent-skill-adapters/feature-overview.md`
- Modify: `ivory-tower/features/010-lifecycle-install/feature-overview.md`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: completed implementation and authoritative verification output.
- Produces: ADR-012 and current-state documentation that cites it; one final Changelog entry for this user request.

- [ ] **Step 1: Write ADR-012 from the accepted decision set**

Use `ivory-tower/adr/adr-template-bare-minimal.md`, status Accepted, date 2026-08-13. Record context, decision, consequences and rejected alternatives:

- stable full marketplace with versioned actual names;
- Claude marketplace as authority;
- marketplace-backed versus filesystem-backed deployment;
- marketplace prepare outside Plugin transaction;
- offline uninstall;
- shared schema v1 envelope with Plugin driver;
- rejected per-Agent lifecycle duplication and universal Power Driver refactor.

- [ ] **Step 2: Update ADR-010 and current-state documentation**

Add ADR-012 links without rewriting executed historical plans. Replace current claims that Plugin is unsupported with the exact support matrix. Document Cloud List/GetPlugin, conditional Plugin Cache, three type-specific registry resolvers and three journal drivers.

- [ ] **Step 3: Update C4 source diagrams**

Show:

- lifecycle Skill/MCP/Plugin branches;
- Codex/Claude marketplace and Hermes filesystem paths;
- Plugin-only conditional Cache/Staging;
- `ResolvePlugin` and Plugin CLI access;
- single journal recovery routing across all three Power types.

Do not edit generated `.structurizr` cache/log files.

- [ ] **Step 4: Run formatting, focused, full, race, vet and build verification**

Run exactly:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./internal/domain ./internal/cloud ./internal/cache ./internal/adapter ./internal/transaction ./internal/lifecycle ./internal/cli ./cmd/apm
go test -count=1 ./...
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0; no package reports a failed or skipped required Plugin test.

- [ ] **Step 5: Run static requirement scans**

Run:

```bash
! rg -n 'Plugin (不受支持|仍明确不支持|unsupported)' BUILD.md ivory-tower/project-overview.md ivory-tower/c4 --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'ResolvePlugin|DriverPlugin|GetPlugin|ADR-012' internal BUILD.md ivory-tower --glob '*.go' --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'SchemaVersion:\s*1|SchemaVersion != 1' internal/transaction internal/lifecycle --glob '*.go'
! rg -n 'SchemaVersion:\s*2|SchemaVersion != 2' internal --glob '*.go'
```

Expected: negative scans have no matches and exit 0; positive scans show all intended boundaries.

- [ ] **Step 6: Write verification.md with requirement-by-requirement evidence**

Map every acceptance criterion in `spec.md` to concrete test names, source files and the Step 4/5 command results. Mark no item proved solely by code inspection when a behavioral test exists.

- [ ] **Step 7: Add one final Changelog entry**

Read `/home/liangff/.codex/docs/Changelog.md` completely and add one reverse-chronological entry listing all Feature 014 implementation commits, behavior summary, verification commands and fake-CLI isolation evidence. Do not add separate entries for docs, tests or intermediate tasks.

- [ ] **Step 8: Re-run final diff and status checks after Changelog**

Run:

```bash
git diff --check
git status --short
```

Expected: only intended Task 11 documentation/Changelog files are uncommitted.

- [ ] **Step 9: Commit ADR, architecture docs and Changelog**

```bash
git add BUILD.md docs/Changelog.md ivory-tower/adr ivory-tower/c4 ivory-tower/features/006-agent-skill-adapters/feature-overview.md ivory-tower/features/010-lifecycle-install/feature-overview.md ivory-tower/features/014-lifecycle-plugin/verification.md ivory-tower/project-overview.md
git commit -m "docs(plugin): record lifecycle architecture"
```

- [ ] **Step 10: Confirm clean completion state**

Run:

```bash
git status --short
git log --oneline --decorate -12
```

Expected: clean worktree and a contiguous set of Feature 014 commits ending in the architecture documentation commit.

---

## Completion Gate

Do not claim completion until all eleven tasks are committed and the final audit proves every `spec.md` acceptance criterion. In particular, a green unit package is not sufficient evidence for three-Agent behavior, recovery, real-environment isolation, documentation, or Changelog requirements.

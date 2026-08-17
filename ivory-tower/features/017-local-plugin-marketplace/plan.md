# Local Plugin Marketplace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 Cloud 的 Agent-specific Plugin 文件组装为不可变本地 marketplace artifact，并让 Codex/Claude 的 marketplace 与 Plugin 版本切换在 manifest-first lifecycle 中可恢复。

**Architecture:** 新增 `internal/pluginresource`，由它管理 Agent-aware 版本缓存、原生 Plugin manifest 校验、Codex/Claude 单条目 marketplace 生成和 Hermes staging；Cloud 只下载原始文件。Lifecycle 只负责 manifest-first 状态表，Adapter 只负责 Agent CLI，Transaction 使用新 marketplace payload/phase 恢复 marketplace 与 Plugin 的联合状态。

**Tech Stack:** Go、Cobra、标准库 `context`/`encoding/json`/`io/fs`/`os`/`path/filepath`、Masterminds SemVer、现有 global lock/config/journal/CLI runner/filesystem 基础设施、Git、Mermaid/C4。

## Global Constraints

- 规格权威来源是 `ivory-tower/features/017-local-plugin-marketplace/spec.md`；实现不得缩减其中任何验收条件。
- Cloud 只托管按 Agent 区分的原始 Plugin 文件，不维护、生成或返回 marketplace 文件/source。
- 每个 `agent + logical plugin` 使用一个 marketplace，名称固定为 `apm-<logical-name>`；每份 marketplace 只包含目标 artifact 的一个版本。
- Artifact 固定保存到 `cache/plugins/<agent>/<logical-name>/<version>`，提交后不可变；cache hit 不连接 Cloud，损坏 cache 不覆盖、不重新下载。
- Codex/Claude marketplace 由目标 Agent 原生 Plugin manifest 生成；`name/version` 必须与请求精确一致，source 固定为 `./plugins/<actual-name>`。
- Install manifest 已记录目标精确版本时直接 `unchanged`，不得访问 Agent、Plugin Resource 或 Cloud。
- 无 manifest 时不得接管任何已有 marketplace/plugin；有旧 manifest 时只有 marketplace/plugin 同时 absent 才清理单条 manifest。
- 有 manifest 的 marketplace/plugin 部分状态、同名异源、cache 缺失或损坏全部 fail closed 并保留 manifest。
- 首装和版本切换的 marketplace/plugin 写操作必须处于 Plugin transaction；Recovery 不连接 Cloud、不生成或修改 artifact。
- 正常 Uninstall 保留 marketplace 注册与 cache；无 manifest 时不检查 Agent；Uninstall 不连接 Cloud。
- Hermes 不生成 marketplace，保持 filesystem 发布与 CLI enable/disable；Skill/MCP、manifest schema、CLI 语法/输出、Search/List 行为不变。
- Plugin journal 继续使用唯一 schema v1，不兼容旧 Plugin payload；所有非法格式返回 `ErrInvalidJournal` 并保留文件。
- 生产代码严格 TDD：先命名错误生产变更，观察聚焦测试因该变更失败，再写最小实现并回归。
- 自动化测试必须使用临时 APM/Agent home 和 fake-bin，逐个断言 Agent executable 位于 fake-bin，禁止真实 Codex、Claude、Hermes CLI。
- 所有重要导出接口和关键一致性约束使用简短、规范的中文注释。
- 实现、逐任务审查、整分支审查、文档和全量验证全部通过前，不修改 `docs/Changelog.md`；最后只增加一条请求级记录。

---

## File Structure

### New production units

| File | Responsibility |
|---|---|
| `internal/pluginresource/artifact.go` | Artifact、Downloader、Manager 契约与稳定字段。 |
| `internal/pluginresource/errors.go` | Miss、损坏、manifest 不匹配和 unsupported Agent 错误。 |
| `internal/pluginresource/manager.go` | Ensure、Lookup、StageHermes 编排。 |
| `internal/pluginresource/filesystem.go` | Agent-aware 路径、临时树、安全遍历、原子提交与 staging。 |
| `internal/pluginresource/marketplace.go` | Builder 接口、原生 manifest 公共解码与校验。 |
| `internal/pluginresource/codex_marketplace.go` | Codex marketplace 生成和复验。 |
| `internal/pluginresource/claude_marketplace.go` | Claude marketplace 生成和复验。 |
| `internal/transaction/plugin_marketplace.go` | Marketplace 首装/版本切换事务。 |
| `internal/transaction/plugin_marketplace_recovery.go` | Marketplace/plugin 联合状态恢复。 |

### Existing production units to change

| File | Responsibility after this feature |
|---|---|
| `internal/domain/plugin.go` | Plugin 实际名、marketplace 名和 canonical version。 |
| `internal/cloud/client.go` | List/Skill/MCP 与 Agent-aware 原始 Plugin 下载。 |
| `internal/cloud/local_plugin.go` | Local Cloud 目录发现和目标目录下载。 |
| `internal/cloud/local_client.go` | 资源根配置，不拥有 Plugin cache。 |
| `internal/cache/cache.go` | Skill staging 接口。 |
| `internal/cache/filesystem.go` | Skill cache 到 staging。 |
| `internal/adapter/plugin.go` | Plugin 与 marketplace facts/targets/mutations 和组合接口。 |
| `internal/adapter/codex_plugin.go` | Codex 本地 marketplace 与 Plugin CLI。 |
| `internal/adapter/claude_plugin.go` | Claude 本地 marketplace 与 Plugin CLI。 |
| `internal/adapter/hermes_plugin.go` | Hermes filesystem Plugin CLI。 |
| `internal/lifecycle/install.go` | 注入 Plugin Resource Manager。 |
| `internal/lifecycle/install_plugin.go` | Manifest-first 安装状态表和 artifact 编排。 |
| `internal/lifecycle/uninstall.go` | 注入 Plugin Resource Manager 的只读能力。 |
| `internal/lifecycle/uninstall_plugin.go` | Manifest-first 联合状态和离线卸载。 |
| `internal/transaction/manager.go` | 显式 Plugin/marketplace transaction 请求。 |
| `internal/transaction/journal.go` | Marketplace payload、phase 和 recovery action。 |
| `internal/transaction/plugin_install.go` | Deployment 分派。 |
| `internal/transaction/plugin_uninstall.go` | 保留 marketplace 的 Plugin 卸载。 |
| `internal/transaction/plugin_recovery.go` | Plugin recovery 分派和 Hermes 恢复。 |
| `internal/transaction/plugin_validation.go` | 新 request/journal 路径和状态校验。 |
| `cmd/apm/main.go` | 构造 Cloud、Plugin Resource、Lifecycle 和 Transaction。 |

### Transition rule

Tasks 1–9 use additive compatibility fields/methods so every task commit builds. Task 10 removes all temporary legacy symbols in one compile-safe cutover. The only transitional names are:

```go
type cloud.PluginPackageDownloader interface {
    DownloadPluginTo(context.Context, domain.AgentID, domain.PowerRef, string) error
}
```

and nullable new marketplace fields beside the old `PluginDefinition`. Task 10 must remove `DownloadPluginTo`, `GetPlugin`, the old `DownloadPlugin(ref)`, `PluginDefinition`, `MarketplaceDefinition`, `PrepareInstall`, and the legacy transaction request/journal branch.

---

### Task 1: Add Plugin Resource Contracts and Marketplace Identity

**Files:**
- Create: `internal/pluginresource/artifact.go`
- Create: `internal/pluginresource/errors.go`
- Create: `internal/pluginresource/artifact_test.go`
- Modify: `internal/domain/plugin.go`
- Modify: `internal/domain/plugin_test.go`

**Interfaces:**
- Consumes: `domain.AgentID`, `domain.PowerRef`, `domain.PluginDeploymentMode`.
- Produces:

```go
func PluginMarketplaceName(ref PowerRef) (string, error)

type pluginresource.Downloader interface {
    DownloadPlugin(context.Context, domain.AgentID, domain.PowerRef, string) error
}

type pluginresource.Manager interface {
    Ensure(context.Context, domain.AgentID, domain.PowerRef) (pluginresource.Artifact, error)
    Lookup(domain.AgentID, domain.PowerRef) (pluginresource.Artifact, error)
    StageHermes(domain.PowerRef) (string, error)
}

type pluginresource.Artifact struct {
    Agent             domain.AgentID
    Power             domain.PowerRef
    ActualName        string
    Root              string
    PluginPath        string
    MarketplaceName   string
    MarketplaceSource string
}
```

- [ ] **Step 1: Write failing marketplace identity tests**

Add literal table cases:

```go
func TestPluginMarketplaceNameUsesLogicalNameOnly(t *testing.T) {
    ref := domain.PowerRef{Name: "demo-plugin", Type: domain.PowerTypePlugin, Version: "v0.1.0"}
    got, err := domain.PluginMarketplaceName(ref)
    if err != nil || got != "apm-demo-plugin" {
        t.Fatalf("PluginMarketplaceName() = %q, %v", got, err)
    }
}

func TestPluginMarketplaceNameRejectsUnsafeIdentity(t *testing.T) {
    for _, ref := range []domain.PowerRef{
        {Name: "../demo", Type: domain.PowerTypePlugin, Version: "v0.1.0"},
        {Name: "demo", Type: domain.PowerTypeSkill, Version: "v0.1.0"},
        {Name: "demo", Type: domain.PowerTypePlugin, Version: "0.1.0"},
    } {
        if _, err := domain.PluginMarketplaceName(ref); err == nil {
            t.Fatalf("PluginMarketplaceName(%#v) error = nil", ref)
        }
    }
}
```

The production mutation these tests catch is deriving marketplace identity from `ActualName`, which would incorrectly include the version.

- [ ] **Step 2: Run the red domain test**

Run: `go test -count=1 ./internal/domain -run 'PluginMarketplaceName'`

Expected: FAIL because `PluginMarketplaceName` is undefined.

- [ ] **Step 3: Implement the exact identity mapping**

Reuse the existing safe-segment and canonical version validation. Return `"apm-" + ref.Name`; do not trim or sanitize invalid input. Keep `PluginDefinition` temporarily for compile compatibility and mark its removal in Task 10.

- [ ] **Step 4: Add the Plugin Resource contracts**

Create the exact interfaces and Artifact above. Add sentinels:

```go
var (
    ErrMiss            = errors.New("plugin artifact cache miss")
    ErrInvalidArtifact = errors.New("invalid plugin artifact")
    ErrUnsupportedAgent = errors.New("unsupported plugin artifact agent")
)
```

Add `Artifact.Validate()` tests proving Codex/Claude require non-empty absolute marketplace source and Hermes requires both marketplace fields empty.

- [ ] **Step 5: Run focused and package tests**

Run: `go test -count=1 ./internal/domain ./internal/pluginresource`

Expected: PASS.

- [ ] **Step 6: Commit the contracts**

```bash
git add internal/domain/plugin.go internal/domain/plugin_test.go internal/pluginresource
git commit -m "feat(plugin): define local artifact contracts"
```

---

### Task 2: Implement Agent-Aware Immutable Artifact Storage

**Files:**
- Create: `internal/pluginresource/manager.go`
- Create: `internal/pluginresource/filesystem.go`
- Create: `internal/pluginresource/manager_test.go`
- Create: `internal/pluginresource/filesystem_test.go`

**Interfaces:**
- Consumes: Task 1 Artifact/Downloader; `paths.CachePath`, `paths.CacheTempPath`, `paths.StagingPath`, `paths.StagingTempPath`, `filesystem.CopyDir`.
- Produces:

```go
type artifactBuilder interface {
    Build(domain.AgentID, domain.PowerRef, string, string) error
    Validate(domain.AgentID, domain.PowerRef, string) error
}

func newService(
    cacheRoot, cacheTempRoot, stagingRoot, stagingTempRoot string,
    downloader Downloader,
    builders map[domain.AgentID]artifactBuilder,
) *Service
```

The public default `New(Downloader)` constructor is added in Task 3 after all built-in
builders exist; this task remains independently buildable through the injected
`newService` constructor used by same-package tests.

- [ ] **Step 1: Write failing immutable-cache tests with an injected builder**

Create a fake downloader that writes `payload.txt` and counts calls, plus a fake builder that validates the final root. Assert:

```go
artifact, err := service.Ensure(ctx, domain.AgentCodex, ref)
// artifact.Root == <cache>/plugins/codex/demo/v0.1.0
// artifact.PluginPath == <root>/plugins/demo-v0.1.0
// downloader.calls == 1

again, err := service.Ensure(ctx, domain.AgentCodex, ref)
// again == artifact and downloader.calls remains 1
```

Also create an existing final root whose builder validation returns `ErrInvalidArtifact`; assert Ensure/Lookup return that error and the downloader remains uncalled.

The mutation these tests catch is treating any existing directory as a valid hit or repopulating a damaged immutable version.

- [ ] **Step 2: Run the red storage tests**

Run: `go test -count=1 ./internal/pluginresource -run 'Ensure|Lookup'`

Expected: FAIL because Service/Ensure/Lookup do not exist.

- [ ] **Step 3: Implement safe paths and atomic Ensure**

Construct only:

```text
<cache>/plugins/<agent>/<logical>/<version>
<cache>/.tmp/plugins/<unique-id>
```

Use `os.MkdirTemp` beneath the configured cache temp root. Download into
`<temporary>/plugins/<actual-name>`, call builder Build then Validate, create the
final parent, and `os.Rename(temporary, final)`. Cleanup only the unique temporary
directory. If final exists, call Validate and never download or replace it.

- [ ] **Step 4: Write and verify symlink/tree safety tests**

Add tests where the plugin root is a symlink, a child is a symlink, or the final
root is a regular file. Assert `errors.Is(err, ErrInvalidArtifact)` and exact outside
sentinel files remain unchanged.

Run: `go test -count=1 ./internal/pluginresource -run 'Symlink|Unsafe|RegularFile'`

Expected before tree validation: FAIL; after adding an `os.Lstat`/`filepath.WalkDir`
physical-tree validator: PASS.

- [ ] **Step 5: Implement and test Hermes staging**

`StageHermes(ref)` must Lookup `domain.AgentHermes`, remove only configured staging
and staging-temp roots, copy the immutable plugin leaf to
`staging/plugins/<logical-name>`, and atomically rename the staging temp root.

Test cache miss, clean rebuild, unrelated old staging removal, and that Codex/Claude
artifact roots are untouched.

- [ ] **Step 6: Run regression tests**

Run: `go test -count=1 ./internal/pluginresource ./internal/paths ./internal/utils/filesystem`

Expected: PASS.

- [ ] **Step 7: Commit immutable storage**

```bash
git add internal/pluginresource/manager.go internal/pluginresource/filesystem.go internal/pluginresource/*_test.go
git commit -m "feat(plugin): persist agent artifacts immutably"
```

---

### Task 3: Generate Codex and Claude Marketplace Documents

**Files:**
- Create: `internal/pluginresource/marketplace.go`
- Create: `internal/pluginresource/codex_marketplace.go`
- Create: `internal/pluginresource/claude_marketplace.go`
- Create: `internal/pluginresource/marketplace_test.go`
- Create: `internal/pluginresource/codex_marketplace_test.go`
- Create: `internal/pluginresource/claude_marketplace_test.go`
- Modify: `internal/pluginresource/manager.go`

**Interfaces:**
- Consumes: Task 2 `artifactBuilder`; target plugin path
  `<artifact>/plugins/<actual-name>`.
- Produces built-in Agent builder resolution and:

```go
func New(Downloader) (*Service, error)
```

- [ ] **Step 1: Write failing Codex generation tests**

Write a `.codex-plugin/plugin.json` fixture with name `demo-v0.1.0`, version
`0.1.0`, description, author and interface fields. Decode the generated document
into an independent test struct and assert the literal values:

```go
wantName := "apm-demo"
wantSourceKind := "local"
wantPath := "./plugins/demo-v0.1.0"
wantInstallation := "AVAILABLE"
wantAuthentication := "ON_INSTALL"
```

Assert exactly one plugin entry and no Claude marketplace file.

- [ ] **Step 2: Run Codex red test and implement its builder**

Run: `go test -count=1 ./internal/pluginresource -run 'CodexMarketplace'`

Expected: FAIL because the builder/file is missing.

Implement JSON structs and `json.Encoder` output at
`.agents/plugins/marketplace.json`. Use manifest displayName/category or literal
defaults `logical-name`/`Coding`.

- [ ] **Step 3: Write failing Claude generation tests**

Use a `.claude-plugin/plugin.json` fixture and assert:

```go
name == "apm-demo"
owner.name == "Example Author"
metadata.version == "0.1.0"
plugins[0].name == "demo-v0.1.0"
plugins[0].source == "./plugins/demo-v0.1.0"
```

Assert one entry and no Codex marketplace file.

- [ ] **Step 4: Run Claude red test and implement its builder**

Run: `go test -count=1 ./internal/pluginresource -run 'ClaudeMarketplace'`

Expected: FAIL before implementation, PASS after writing
`.claude-plugin/marketplace.json` with manifest values or the exact defaults
`APM` and `APM local marketplace for <logical-name>`.

- [ ] **Step 5: Lock down manifest and package rejection**

Add table tests for wrong name, wrong/native noncanonical version, missing manifest,
second JSON value, symlink manifest, Cloud-supplied `.agents/plugins/marketplace.json`
and `.claude-plugin/marketplace.json`. Each must wrap `ErrInvalidArtifact` and leave
the final cache absent.

The mutation check is replacing exact name/version comparison with trimming or
accepting the package's marketplace source.

- [ ] **Step 6: Implement semantic cache-hit validation**

Re-read the Agent manifest, derive the expected document, decode the stored
marketplace and compare its typed semantic value. Reject missing/extra plugin entries,
wrong source, wrong marketplace name and wrong version. Hermes validates only the
physical plugin tree and has empty marketplace fields.

- [ ] **Step 7: Run package tests and commit**

Run: `go test -count=1 ./internal/pluginresource`

Expected: PASS.

```bash
git add internal/pluginresource
git commit -m "feat(plugin): generate local marketplaces"
```

---

### Task 4: Add Raw Agent Plugin Downloads to Local Cloud

**Files:**
- Modify: `internal/cloud/local_plugin.go`
- Modify: `internal/cloud/local_plugin_test.go`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/codex/.codex-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/codex/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/claude-code/.claude-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/claude-code/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/hermes-agent/.claude-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/hermes-agent/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/codex/.codex-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/codex/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/claude-code/.claude-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/claude-code/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/hermes-agent/.claude-plugin/plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/hermes-agent/skills/demo-plugin/SKILL.md`
- Test: `internal/pluginresource/manager_test.go`

**Interfaces:**
- Consumes: Task 1 Downloader destination contract.
- Produces transitional compile-safe interface:

```go
type PluginPackageDownloader interface {
    DownloadPluginTo(context.Context, domain.AgentID, domain.PowerRef, string) error
}
```

Task 10 renames this to the final `Client.DownloadPlugin` after old callers are gone.

- [ ] **Step 1: Write failing Local Cloud raw-download tests**

For every supported Agent, download `demo-plugin=v0.1.0` into a distinct `t.TempDir()`
destination. Assert the target Agent manifest and `skills/demo-plugin/SKILL.md` exist,
and assert neither marketplace path exists anywhere in the copied tree.

Add exact misses for unsupported Agent, missing logical name and missing version.

- [ ] **Step 2: Run the red Cloud tests**

Run: `go test -count=1 ./internal/cloud -run 'DownloadPluginTo'`

Expected: FAIL because `DownloadPluginTo` is undefined.

- [ ] **Step 3: Add Agent-specific raw resources**

Create package roots under the exact logical/version/Agent layout. Codex and Claude
manifests use actual names `demo-plugin-v0.0.1`/`demo-plugin-v0.1.0` and native
versions without `v`. Hermes carries the plugin content needed by its existing
filesystem Adapter. Do not add marketplace files.

- [ ] **Step 4: Implement safe destination copying**

Validate the ref and Agent, require a non-existing or empty caller-controlled
destination beneath no Cloud-owned cache assumption, reject destination symlinks,
and copy the exact resource directory with `filesystem.CopyDir`. Return
`cloud.ErrNotFound` for missing Agent/ref.

- [ ] **Step 5: Prove Cloud does not own cache**

Set `APM_HOME` and `XDG_CACHE_HOME` to sentinel directories, call only
`DownloadPluginTo`, and assert no `cache/plugins` or staging path was created.

- [ ] **Step 6: Integrate Local Cloud with Plugin Resource in tests**

Use a small test adapter implementing `pluginresource.Downloader` by delegating to
`DownloadPluginTo`. Assert Ensure builds correct Codex/Claude marketplace artifacts
and Hermes artifact without touching the legacy marketplace-backed Local Cloud APIs.

- [ ] **Step 7: Run tests and commit**

Run: `go test -count=1 ./internal/cloud ./internal/pluginresource`

Expected: PASS, including unchanged legacy Plugin tests until Task 10.

```bash
git add internal/cloud internal/pluginresource/manager_test.go
git commit -m "feat(cloud): download agent plugin packages"
```

---

### Task 5: Add Explicit Marketplace Adapter Operations

**Files:**
- Modify: `internal/adapter/plugin.go`
- Modify: `internal/adapter/codex_plugin.go`
- Modify: `internal/adapter/claude_plugin.go`
- Modify: `internal/adapter/hermes_plugin.go`
- Modify: `internal/adapter/plugin_test.go`
- Modify: `internal/adapter/codex_plugin_test.go`
- Modify: `internal/adapter/claude_plugin_test.go`
- Modify: `internal/adapter/hermes_plugin_test.go`
- Modify: `internal/adapter/registry_test.go`

**Interfaces:**
- Produces the exact `MarketplacePluginAdapter` composition from the spec while
  temporarily retaining `PrepareInstall` on `PluginAdapter` until Task 10.

```go
type MarketplacePresence string
const (
    MarketplacePresent MarketplacePresence = "present"
    MarketplaceAbsent  MarketplacePresence = "absent"
)

type MarketplaceTarget struct { Name, Source string }
type MarketplaceFact struct { Presence MarketplacePresence; Name, Source string }
```

- [ ] **Step 1: Write failing common validation tests**

Cover safe `apm-demo` names, absolute clean local source, absent facts with empty
source, present facts with non-empty source, and rejection of remote/relative source,
duplicate/unknown presence and unsafe names.

- [ ] **Step 2: Run common red tests and add the types/subinterface**

Run: `go test -count=1 ./internal/adapter -run 'Marketplace(Target|Fact)'`

Expected: FAIL before the types and validators exist.

- [ ] **Step 3: Write exact Codex CLI tests**

Using the recording runner, require:

```text
codex plugin marketplace list --json
codex plugin marketplace add /abs/cache/plugins/codex/demo/v0.1.0 --json
codex plugin marketplace remove apm-demo --json
```

Decode list output and verify absent, one exact local source, duplicate name,
non-local source, missing source and truncated output. Started failures must return
`PluginMutationUnknown`; not-started failures return `PluginMutationNotApplied`.

- [ ] **Step 4: Implement Codex methods and run focused tests**

Run: `go test -count=1 ./internal/adapter -run 'CodexPluginMarketplace'`

Expected: PASS after adding Inspect/Register/Unregister without changing Plugin
Add/Remove behavior.

- [ ] **Step 5: Write and implement exact Claude CLI tests**

Require:

```text
claude plugin marketplace list --json
claude plugin marketplace add /abs/cache/plugins/claude-code/demo/v0.1.0 --scope user
claude plugin marketplace remove apm-demo --scope user
```

Accept only one unambiguous local source from the documented list fields. Apply the
same mutation classification as Codex.

- [ ] **Step 6: Prove interface capabilities**

At compile/runtime, Codex and Claude satisfy `MarketplacePluginAdapter`; Hermes
satisfies only `PluginAdapter`. Preserve the transitional `PrepareInstall` tests until
Task 10, but no new code may call PrepareInstall.

- [ ] **Step 7: Run regression tests and commit**

Run: `go test -count=1 ./internal/adapter`

Expected: PASS.

```bash
git add internal/adapter
git commit -m "feat(adapter): expose marketplace mutations"
```

---

### Task 6: Extend Plugin Requests and Journal Schema for Marketplace Switching

**Files:**
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/plugin_validation.go`
- Modify: `internal/transaction/plugin_validation_test.go`
- Modify: `internal/transaction/journal_schema_test.go`

**Interfaces:**
- Consumes: Task 5 marketplace types.
- Produces additive request/payload fields used by Tasks 7–9:

```go
type PluginMarketplaceInstallRequest struct {
    Target   adapter.MarketplaceTarget
    Previous *adapter.MarketplaceTarget
    Adapter  adapter.MarketplacePluginAdapter
}

type PluginInstallRequest struct {
    Agent          domain.AgentID
    Power          domain.PowerRef
    Previous       *domain.PowerRef
    Definition     domain.PluginDefinition // transitional; Task 10 removes
    StagingRoot    string
    Adapter        adapter.PluginAdapter
    Target         adapter.PluginTarget
    PreviousTarget *adapter.PluginTarget
    Marketplace    *PluginMarketplaceInstallRequest
}

type PluginUninstallRequest struct {
    Agent              domain.AgentID
    Power              domain.PowerRef
    ActualName         string
    Adapter            adapter.PluginAdapter
    Target             adapter.PluginTarget
    Marketplace        *adapter.MarketplaceTarget
    MarketplaceAdapter adapter.MarketplacePluginAdapter
}

type PluginMarketplaceJournalPayload struct {
    Name            string `json:"name"`
    PreviousSource  string `json:"previous_source,omitempty"`
    PreviousPresent bool   `json:"previous_present"`
    TargetSource    string `json:"target_source"`
}
```

- [ ] **Step 1: Write failing valid-envelope tests**

Create literal first-install, upgrade and uninstall journals using phases:

```text
registering-marketplace
removing-old-plugin
unregistering-old-marketplace
registering-new-marketplace
adding-new-plugin
removing-plugin
rolling-back
committing
```

Assert `validateJournal` accepts only phase/operation combinations specified in the
Feature 017 spec.

- [ ] **Step 2: Run red schema tests**

Run: `go test -count=1 ./internal/transaction -run 'PluginMarketplace.*Journal'`

Expected: FAIL because the payload/phases do not exist.

- [ ] **Step 3: Add schema fields and strict phase tables**

Add Chinese comments describing each phase. Marketplace source must be absolute,
clean and within the canonical Plugin cache path:

```text
<cache>/plugins/<journal.agent>/<journal.power.name>/<version>
```

Previous source must match `journal.Previous.Version`; target source must match
`journal.Power.Version`; first install forbids previous source/presence.

- [ ] **Step 4: Add invalid-journal tables**

Reject and assert `errors.Is(err, ErrInvalidJournal)` for relative/remote source,
wrong Agent/name/version path, previous source without previous ref, missing target
source, filesystem payload plus marketplace payload, illegal phase, extra old fields
and old marketplace Plugin journal without the new payload.

During this task only, allow the exact legacy producer shape behind an explicit
private `validLegacyPluginJournal` branch so the current main tests compile; mark the
branch with a Task 10 removal comment and a test named
`TestLegacyPluginJournalAcceptedOnlyDuringFeatureCutover`.

- [ ] **Step 5: Add request validation tests**

First install requires target Plugin and target marketplace. Upgrade additionally
requires previous ref, previous Plugin target and previous marketplace with the same
name but different source. Uninstall marketplace request requires a present target,
marketplace target and marketplace-capable Adapter. Hermes forbids marketplace data.

- [ ] **Step 6: Run transaction tests and commit**

Run: `go test -count=1 ./internal/transaction`

Expected: PASS with both temporary legacy and new schema paths.

```bash
git add internal/transaction/manager.go internal/transaction/journal.go internal/transaction/plugin_validation.go internal/transaction/*validation_test.go internal/transaction/journal_schema_test.go
git commit -m "feat(transaction): model marketplace switching"
```

---

### Task 7: Implement Marketplace Install and Uninstall Transactions

**Files:**
- Create: `internal/transaction/plugin_marketplace.go`
- Create: `internal/transaction/plugin_marketplace_test.go`
- Modify: `internal/transaction/plugin_install.go`
- Modify: `internal/transaction/plugin_uninstall.go`
- Modify: `internal/transaction/plugin_install_test.go`
- Modify: `internal/transaction/plugin_uninstall_test.go`

**Interfaces:**
- Consumes: Task 6 request/payload and Task 5 mutation results.
- Produces new marketplace transaction branch selected when
  `request.Marketplace != nil`; legacy branch remains until Task 10.

- [ ] **Step 1: Write a recording marketplace Adapter and first-install red test**

The fake records one ordered stream for journal Create/Update/Delete,
InspectMarketplace, Inspect Plugin, Register, Add and manifest Set. Assert:

```text
inspect-marketplace, inspect-plugin,
create:registering-marketplace,
register-marketplace,
update:adding-new-plugin,
add-plugin,
update:committing,
set-manifest,
delete-journal
```

The mutation caught is performing any Agent write before journal Create.

- [ ] **Step 2: Run first-install red test and implement minimal forward flow**

Run: `go test -count=1 ./internal/transaction -run 'MarketplacePluginFirstInstall'`

Expected: FAIL before the new branch; PASS after implementing exact phases.

- [ ] **Step 3: Write version-switch red test**

Require this ordered write sequence after preflight facts:

```text
create:removing-old-plugin
remove-old-plugin
update:unregistering-old-marketplace
unregister-old-marketplace
update:registering-new-marketplace
register-new-marketplace
update:adding-new-plugin
add-new-plugin
update:committing
set-new-manifest
delete-journal
```

Assert the marketplace name is unchanged while source changes from previous to target.

- [ ] **Step 4: Implement applied/not-applied/unknown matrices**

For each of four CLI writes, table-test:

- `applied`: advance phase;
- `not-applied`: persist rolling-back and restore the pre-state;
- `unknown`: return `ErrIndeterminateState`, keep journal and stop later writes.

Rollback must remove target Plugin before target marketplace, then register previous
marketplace and restore previous Plugin. First-install rollback removes a marketplace
created by this transaction.

- [ ] **Step 5: Implement marketplace-aware Uninstall**

Transaction preflight must re-inspect the exact marketplace source and Plugin. With
both present, create `removing-plugin`, remove only Plugin, advance `committing`,
remove manifest and delete journal. Never unregister/update marketplace.

Write a test that fails if Register/Unregister or Cloud-like dependencies are called.

- [ ] **Step 6: Preserve deterministic target-missing behavior**

Lifecycle normally handles double-absent before transaction, but transaction recheck
must safely return `TargetMissing=true` and remove only manifest if both become absent.
Partial states return `ErrIndeterminateState` and preserve manifest.

- [ ] **Step 7: Run focused/full transaction tests and commit**

Run:

```bash
go test -count=1 ./internal/transaction -run 'MarketplacePlugin|PluginInstall|PluginUninstall'
go test -count=1 ./internal/transaction
```

Expected: PASS.

```bash
git add internal/transaction
git commit -m "feat(transaction): switch local marketplaces safely"
```

---

### Task 8: Recover Marketplace and Plugin Joint State

**Files:**
- Create: `internal/transaction/plugin_marketplace_recovery.go`
- Create: `internal/transaction/plugin_marketplace_recovery_test.go`
- Modify: `internal/transaction/plugin_recovery.go`
- Modify: `internal/transaction/plugin_recovery_test.go`
- Modify: `internal/transaction/recovery_test.go`

**Interfaces:**
- Consumes: Task 7 journal phases; `PluginAdapterResolver` returning an Adapter that
  must satisfy `MarketplacePluginAdapter` for marketplace payloads.
- Produces Cloud-independent convergence for Fix/Install/Uninstall pre-recovery.

- [ ] **Step 1: Write failing direction-selection tests**

Literal cases:

| Phase/facts | Direction |
|---|---|
| `committing`, target source + target Plugin | commit |
| `adding-new-plugin`, target source + target Plugin | commit |
| `adding-new-plugin`, target source + target absent | rollback |
| any earlier install phase without target Plugin | rollback |
| `rolling-back` | rollback |

Assert direction is persisted before subsequent mutations and never reverses.

- [ ] **Step 2: Run red recovery test**

Run: `go test -count=1 ./internal/transaction -run 'RecoverMarketplacePluginDirection'`

Expected: FAIL because marketplace recovery dispatch is absent.

- [ ] **Step 3: Implement exact fact collection**

Inspect one marketplace name plus old and target actual names. Classify marketplace
only as absent, exact previous source, exact target source or other. Other/duplicate/
unreadable facts return `ErrIndeterminateState` without writes.

- [ ] **Step 4: Implement rollback convergence tests and code**

From every reachable partial state, converge in this order:

```text
remove target Plugin if present
unregister target marketplace if selected
register previous marketplace if required
add previous Plugin if absent
keep previous manifest
delete journal
```

First install omits previous registration/Plugin and converges to marketplace/plugin
absent. Inject interruption after every action and prove the next Recover continues.

- [ ] **Step 5: Implement commit convergence tests and code**

Commit requires target source and target Plugin present, then Set target manifest and
delete journal. If phase is committing but either target fact is absent or previous
source is selected, return `ErrIndeterminateState`; do not invent missing state.

- [ ] **Step 6: Implement Uninstall recovery**

Validate marketplace remains exact. Plugin absent commits manifest removal; Plugin
present fixes rollback direction and keeps manifest. Marketplace absent/changed or
cache source missing retains journal and returns indeterminate.

- [ ] **Step 7: Prove resolver and Cloud boundaries**

Hermes journals continue through existing filesystem recovery. Marketplace payload
with Hermes/non-marketplace Adapter returns `ErrInvalidRequest` or
`ErrIndeterminateState` without panic. Recovery structs expose no Cloud/Downloader;
compile-time tests and fail-fast fakes prove zero Cloud access.

- [ ] **Step 8: Run recovery/race tests and commit**

Run:

```bash
go test -count=1 ./internal/transaction -run 'Recover.*Plugin|PluginRecovery'
go test -race -count=1 ./internal/transaction
```

Expected: PASS.

```bash
git add internal/transaction
git commit -m "feat(transaction): recover marketplace plugin state"
```

---

### Task 9: Apply Manifest-First Plugin Lifecycle and Default Wiring

**Files:**
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_plugin.go`
- Modify: `internal/lifecycle/uninstall.go`
- Modify: `internal/lifecycle/uninstall_plugin.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/uninstall_test_helpers_test.go`
- Modify: `internal/lifecycle/install_plugin_test.go`
- Modify: `internal/lifecycle/uninstall_plugin_test.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `pluginresource.Manager`, Task 5 marketplace facts and Task 7 transaction
  requests.
- Produces final manifest-first business behavior while Cloud still exposes transitional
  old methods until Task 10.

- [ ] **Step 1: Write same-version zero-dependency red test**

Create a manifest with target ref. Every Plugin Resource, Adapter marketplace/plugin,
Cloud Plugin and Transaction method must panic or return `unexpected call`. Assert
Install returns one item with `InstallOutcomeUnchanged` and no calls.

Run: `go test -count=1 ./internal/lifecycle -run 'PluginManifestTargetIsUnchanged'`

Expected against current code: FAIL because GetPlugin/PrepareInstall/Inspect are called.

- [ ] **Step 2: Inject Plugin Resource into lifecycle constructors**

Add `pluginresource.Manager` after the existing Skill cache dependency in NewInstall
and to NewUninstall. Update every constructor call/test in the same commit scope so
`go test ./...` remains buildable. Add Chinese comments distinguishing target Ensure
from previous Lookup.

- [ ] **Step 3: Implement the install manifest-first state table**

Exact order when manifest differs/missing:

```text
Ensure target artifact
resolve Adapter and marketplace capability
if previous manifest: Inspect previous marketplace + previous Plugin
if no manifest: Inspect target marketplace + target Plugin
call Transaction only for double-absent first install or exact-present switch
```

Double-absent previous state removes only that manifest entry, then evaluates strict
no-manifest first install. Partial/source/cache anomalies fail closed and preserve
manifest. Previous artifact uses Lookup only; inject a downloader panic to prove no
old-version download.

- [ ] **Step 4: Implement Hermes manifest-first branching**

Same-version returns unchanged without resource/CLI. Different/missing manifest calls
Ensure for Hermes, uses precise Plugin fact only, clears stale manifest on absent, and
calls StageHermes only when a transaction will publish. Preserve enabled-state
indifference and current filesystem transaction semantics.

- [ ] **Step 5: Implement uninstall manifest-first state table**

No manifest returns unchanged/target-missing without Adapter/Resource/Cloud. With a
manifest:

- Codex/Claude double-absent removes only manifest;
- exact marketplace source + valid Lookup + Plugin present calls UninstallPlugin;
- partial/damaged states fail closed;
- Hermes absent removes manifest, present calls existing transaction.

Add fail-fast Cloud fakes proving all Plugin uninstall paths are offline.

- [ ] **Step 6: Wire the default Plugin Resource Manager**

Keep the transitional Cloud client API compile-safe by defining in `cmd/apm/main.go`:

```go
type pluginDownloadBridge struct { source cloud.PluginPackageDownloader }
func (b pluginDownloadBridge) DownloadPlugin(ctx context.Context, agent domain.AgentID, ref domain.PowerRef, dst string) error {
    return b.source.DownloadPluginTo(ctx, agent, ref, dst)
}
```

Construct one Plugin Resource Manager per lifecycle factory using the LocalClient and
inject it. Task 10 removes this bridge when the final Cloud method is renamed.

- [ ] **Step 7: Run lifecycle and full compile regression**

Run:

```bash
go test -count=1 ./internal/lifecycle -run 'Plugin'
go test -count=1 ./cmd/apm
go test -count=1 ./...
```

Expected: PASS; legacy APIs remain unused in production but still exist for Task 10.

- [ ] **Step 8: Commit lifecycle cutover**

```bash
git add internal/lifecycle cmd/apm
git commit -m "feat(lifecycle): install plugins from local artifacts"
```

---

### Task 10: Remove Remote Marketplace and Legacy Cutover APIs

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_plugin.go`
- Modify: `internal/cloud/local_client.go`
- Modify: `internal/cloud/local_plugin_test.go`
- Modify: `internal/cloud/local_client_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`
- Modify/delete: old `internal/cloud/resources/plugins/.agents/**`, `.claude-plugin/**`, `plugins/**`
- Modify: `internal/domain/plugin.go`
- Modify: `internal/domain/plugin_test.go`
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/filesystem_test.go`
- Modify: `internal/adapter/plugin.go`
- Modify: `internal/adapter/{codex,claude,hermes}_plugin.go`
- Modify: `internal/adapter/*plugin*_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/plugin_install.go`
- Modify: `internal/transaction/plugin_validation.go`
- Modify: `internal/transaction/*plugin*_test.go`
- Modify: Cloud fakes under `internal/lifecycle/*_test.go`
- Modify: `cmd/apm/main.go`

**Interfaces:**
- Produces the final spec API with no transitional symbols:

```go
type cloud.Client interface {
    List() ([]domain.PowerRef, error)
    DownloadSkill(domain.PowerRef) error
    GetMCP(domain.PowerRef) (domain.MCPServer, error)
    DownloadPlugin(context.Context, domain.AgentID, domain.PowerRef, string) error
}
```

- [ ] **Step 1: Add a compile-failing legacy-symbol guard**

First change tests/fakes to the final Cloud method and remove uses of
`PluginDefinition`, `GetPlugin`, `PrepareInstall`, `StagePlugin` and legacy request
fields. Run `go test -count=1 ./...` and observe compile failures in every remaining
production use; record the failure list in the task report.

- [ ] **Step 2: Remove old Cloud authority-index behavior**

Rename `DownloadPluginTo` to `DownloadPlugin`, add it to `cloud.Client`, remove old
`GetPlugin` and cache-writing `DownloadPlugin(ref)`. Rewrite Local `listPlugins` to
scan `<logical>/<version>/<agent>` and emit one logical ref when at least one supported
Agent package exists. Stable sort remains Type/Name/Version.

Delete Cloud marketplace files and the old `plugins/plugins/<actual-name>` layout.
Tests must prove no `.agents/plugins/marketplace.json` or
`.claude-plugin/marketplace.json` exists under Cloud resources.

- [ ] **Step 3: Remove old domain and cache ownership**

Delete `PluginDefinition`, `MarketplaceDefinition`, `PluginRefFromMarketplace`,
`PowerCache.StagePlugin` and the Plugin branch in FilesystemCache. Preserve Skill
tests byte-for-byte where behavior is unchanged.

- [ ] **Step 4: Remove Adapter PrepareInstall**

Delete `PrepareInstall` from the interface and all three implementations/tests.
Codex/Claude keep explicit marketplace subinterface methods; Hermes remains base-only.

- [ ] **Step 5: Remove transaction compatibility branches**

Delete legacy Definition/request fields, legacy marketplace install implementation,
`validLegacyPluginJournal`, and its temporary test. All Plugin marketplace journals
without the new payload must now return `ErrInvalidJournal`. Keep Hermes filesystem
payload valid.

- [ ] **Step 6: Remove wiring bridge and update all fakes**

LocalClient now directly satisfies both `cloud.Client` and `pluginresource.Downloader`.
Delete `pluginDownloadBridge`. Update Search/List/Install/Uninstall fakes with the final
method; fail-fast implementations must report unexpected Plugin downloads where
appropriate.

- [ ] **Step 7: Run symbol scans and full tests**

Run:

```bash
if rg -n 'GetPlugin|PluginDefinition|MarketplaceDefinition|PrepareInstall|StagePlugin|DownloadPluginTo|validLegacyPluginJournal' --glob '*.go' .; then exit 1; fi
go test -count=1 ./internal/domain ./internal/cloud ./internal/cache ./internal/adapter ./internal/transaction ./internal/lifecycle ./cmd/apm
go test -count=1 ./...
```

Expected: scans return no matches and all tests PASS.

- [ ] **Step 8: Commit the final API cutover**

```bash
git add internal cmd
git commit -m "refactor(plugin): remove remote marketplace APIs"
```

---

### Task 11: Add Isolated End-to-End and Interruption Coverage

**Files:**
- Modify: `internal/lifecycle/plugin_integration_test.go`
- Modify: `internal/lifecycle/fix_integration_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`
- Test: `internal/pluginresource/*_test.go`

**Interfaces:**
- Consumes: final production APIs from Task 10.
- Produces acceptance evidence for multi-Agent/multi-version recovery and real-CLI
  isolation.

- [ ] **Step 1: Rebuild fake Agent marketplace state**

Fake Codex/Claude CLIs must persist marketplace `{name, source}` and installed Plugin
selectors in files beneath each temporary Agent home. Implement exact list/add/remove
responses expected by the real adapters. Record the absolute executable path and argv
for every call.

- [ ] **Step 2: Write first-install and manifest-first integration tests**

For Codex and Claude:

```text
install v0.0.1 → local marketplace source points at agent-aware cache
repeat v0.0.1 → unchanged and call log does not grow
uninstall → Plugin absent, manifest removed, marketplace/cache retained
repeat uninstall → no Agent call
```

Decode generated marketplace files and assert exactly one v0.0.1 entry.

- [ ] **Step 3: Write upgrade/downgrade and multi-Plugin tests**

Install v0.0.1, upgrade v0.1.0, then downgrade v0.0.1. Assert both caches remain,
marketplace source switches to the current version, generated document has one entry,
and only one actual Plugin is installed. Install a second logical Plugin and prove its
marketplace source never changes during the first Plugin's version switch.

- [ ] **Step 4: Write manifest/Agent anomaly tests**

Cover same-version manifest with broken Agent state returning unchanged without calls;
different-version manifest with both old facts absent clearing the manifest then
installing; marketplace-only/plugin-only/source-conflict/missing-cache states failing
closed and preserving manifest/Agent state; no-manifest existing facts remaining
unmanaged.

- [ ] **Step 5: Inject interruptions at every marketplace phase**

For first install and version switch, stop after each fake CLI mutation and after each
journal phase write. Run `FixService.Fix` or the next Install, then assert exact
rollback/commit state, manifest, marketplace source, installed selectors and journal
deletion/retention. Unknown CLI results must retain journal until facts become readable.

- [ ] **Step 6: Cover Uninstall recovery and Hermes regression**

Interrupt marketplace Plugin uninstall before/after remove and before manifest commit;
prove marketplace remains. Re-run Hermes install/upgrade/downgrade/uninstall using the
new agent-aware resource path and existing filesystem recovery.

- [ ] **Step 7: Prove fake-bin isolation**

Set PATH to `<temp>/fake-bin:/usr/bin:/bin`, isolate `CODEX_HOME`,
`CLAUDE_CONFIG_DIR`, `HERMES_HOME`, `APM_HOME` and `XDG_CACHE_HOME`, then assert every
recorded Agent executable is inside fake-bin and never equals
`/home/liangff/.local/bin/hermes` or another real Agent path.

- [ ] **Step 8: Run focused, package and race tests**

Run:

```bash
go test -count=1 -v ./internal/lifecycle -run 'TestPlugin|TestFix.*Plugin'
go test -count=1 ./internal/cloud ./internal/pluginresource ./internal/lifecycle
go test -race -count=1 ./internal/pluginresource ./internal/adapter ./internal/transaction ./internal/lifecycle
```

Expected: PASS with no real Agent CLI invocation.

- [ ] **Step 9: Commit integration coverage**

```bash
git add internal/cloud internal/pluginresource internal/lifecycle
git commit -m "test(plugin): cover local marketplace recovery"
```

---

### Task 12: Record ADR-013 and Synchronize Current Documentation

**Files:**
- Create: `ivory-tower/adr/ADR-013-generate-per-plugin-local-marketplaces.md`
- Modify: `ivory-tower/adr/ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md`
- Modify: `ivory-tower/features/014-lifecycle-plugin/spec.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: Plugin lifecycle diagrams under `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`

**Interfaces:**
- Consumes: accepted Feature 017 spec and actual Task 1–11 implementation.
- Produces current-state docs with ADR-013 cited from related documents.

- [ ] **Step 1: Write ADR-013**

Record Context, Decision, Consequences and Alternatives for: raw Agent Cloud files,
APM-generated per-Agent/per-Plugin single-version marketplace, immutable agent-aware
cache, manifest-first joint facts, and transactional marketplace switching. Status is
`Accepted`, date `2026-08-17`.

- [ ] **Step 2: Supersede ADR-012 without rewriting history**

Set ADR-012 status to `Superseded by ADR-013` and add one concise pointer explaining
that remote stable full marketplaces and Codex/Claude cache exclusion no longer
describe current behavior. Preserve its original decision body as historical context.

- [ ] **Step 3: Synchronize feature/current docs**

Feature 014 must identify Feature 017/ADR-013 as the current source for Cloud,
marketplace, cache and transaction semantics. Project Overview and BUILD must show the
exact install/upgrade/uninstall order, manifest-first unchanged path, cache layout and
damaged-state fail-closed boundary.

- [ ] **Step 4: Update C4 dependency direction**

Diagrams must show:

```text
Cloud → raw Plugin destination
Plugin Resource → Cloud + Cache roots + Marketplace Builders
Lifecycle → Plugin Resource + Adapter + Transaction
Transaction → Adapter + Journal + Manifest (never Cloud/Plugin Resource)
```

Codex/Claude builders are parallel components; Hermes has no marketplace builder.

- [ ] **Step 5: Validate docs mechanically**

Run:

```bash
git diff --check
if rg -n 'Cloud 托管.*marketplace|GetPlugin|稳定全量 marketplace' BUILD.md ivory-tower/project-overview.md ivory-tower/c4/components; then exit 1; fi
go test -count=1 ./...
```

Historical ADR/spec files are intentionally excluded from this current-document scan;
their old wording remains as history behind explicit supersession notices.

- [ ] **Step 6: Commit documentation**

Do not edit Changelog in this task.

```bash
git add ivory-tower BUILD.md
git commit -m "docs(plugin): document local marketplace artifacts"
```

---

### Task 13: Completion Audit, Independent Review, Full Verification, and Changelog

**Files:**
- Modify only after all other gates pass: `docs/Changelog.md`
- Inspect: every production, test and documentation file named in this plan.

**Interfaces:**
- Consumes: all Task 1–12 commits.
- Produces a reviewed, fully verified branch and the single request-level Changelog
  record.

- [ ] **Step 1: Audit all fifteen acceptance conditions**

Build a literal checklist from `spec.md` acceptance conditions 1–15. For each item,
cite direct source, focused test, integration fact or documentation evidence. Missing
or indirect evidence is unfinished work and must be fixed before proceeding.

- [ ] **Step 2: Run self-review and request whole-branch review**

Run:

```bash
git diff --check main...HEAD
git diff --stat main...HEAD
git log --oneline main..HEAD
```

Use `superpowers:requesting-code-review` over the complete implementation range. Fix
every verified Critical/Important finding with a focused regression test and re-review
until none remain. Changelog remains untouched during this loop.

- [ ] **Step 3: Run fresh final verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
verify_dir="$(mktemp -d)"
go build -o "$verify_dir/apm" ./cmd/apm
test -x "$verify_dir/apm"
git diff --check
```

Every command must exit 0 on the current HEAD.

- [ ] **Step 4: Re-run isolated Plugin/Fix tests verbosely**

```bash
go test -count=1 -v ./internal/lifecycle -run 'Test(Plugin|Fix.*Plugin)'
git status --short
```

Expected: PASS, clean worktree, all recorded executables inside fake-bin, no real Agent
home/cache changes.

- [ ] **Step 5: Add the one request-level Changelog entry**

Use `/home/liangff/.codex/docs/Changelog.md`. Add a top `2026-08-17` entry with category
`功能变更`, the user requirement, every Feature 017 spec/plan/implementation/test/fix/
documentation commit except the Changelog commit itself, exact verification commands,
fake-Agent isolation evidence, and limitations:

- existing mismatched/damaged marketplace/cache states fail closed;
- same-version manifest returns unchanged without Agent reconciliation;
- cache has no refresh/GC and same-version content is immutable.

- [ ] **Step 6: Commit Changelog and revalidate**

```bash
git add docs/Changelog.md
git commit -m "docs(changelog): record local plugin marketplaces"
git diff --check HEAD^
go test -count=1 ./...
git status --short --branch
```

Expected: PASS and a clean feature branch.

- [ ] **Step 7: Complete the branch**

Use `superpowers:verification-before-completion`, then
`superpowers:finishing-a-development-branch`. Present merge/PR/keep options unless the
user has already selected integration for this feature. Never merge, push or delete
the worktree without the corresponding explicit choice.

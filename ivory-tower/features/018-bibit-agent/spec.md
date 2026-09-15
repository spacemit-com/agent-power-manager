# Bibit Agent 与共享多 Agent Plugin 设计规格

> 状态与取代说明：本文主体保留为 Feature 018 的历史设计记录；其中 Cloud provider 契约已由 [Feature 020](../020-cloud-client-providers/spec.md) 与 [ADR-016](../../adr/ADR-016-select-cloud-clients-with-explicit-providers.md) 取代，Plugin canonical/派生契约已由 Feature 020 与 [ADR-017](../../adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 取代。
>
> 状态：已于 2026-08-20 按 [ADR-014](../../adr/ADR-014-share-multi-agent-plugin-artifacts.md) 实现。
>
> 取代说明（2026-08-26）：[ADR-022](../../adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 与 [Feature 025](../025-unified-lifecycle-state-semantics/spec.md) 定向取代本文的以下旧规则：Plugin 部分状态一律保留 manifest 并 fail closed（现按统一入口状态矩阵收敛，能证明归 APM 管理的缺失/不完整按缺失清理，身份或来源冲突仍 fail closed）；Plugin 正常卸载保留 marketplace（现卸载同时删除 Plugin、注销专属 `apm-<name>` marketplace 并删除 manifest，仅保留共享 artifact cache）；只有 Plugin 支持显式精确版本 manifest 全命中的无 catalog 快速路径（现已扩展到 Skill、Agent、MCP、Plugin 四类 Power）。

## 背景

APM 已为 Codex、Claude Code 与 Hermes 提供 Skill、MCP、Plugin 的统一 lifecycle。内部开发 Agent Bibit 使用 `~/.bianbu-agent` 作为资源根，Skill 与 Plugin 分别位于 `skills/` 和 `plugins/`，MCP 通过 `bianbu-agent mcp add/get/remove` 管理。

当前 Plugin Cloud 与持久缓存仍按 Agent 分包，同一个 Plugin 版本需要维护多份几乎相同的内容。实际 Plugin 可以在一棵目录中同时携带 Codex、Claude Code 与 Hermes 的原生入口；新增 Bibit 应同时完成共享资源治理，而不是增加第四份资源。

本规格实现 [ADR-014](../../adr/ADR-014-share-multi-agent-plugin-artifacts.md)，取代 Feature 017/ADR-013 的 Agent-aware Plugin 包、版本化实际名和 Agent-aware cache 语义。Skill、MCP、Plugin 继续复用既有锁、manifest、唯一 schema v1 journal、批处理和 `fix` 恢复入口。

## 目标

- 新增内置 Agent ID `bibit`，完整适配 Skill、MCP 与 Plugin。
- Bibit 默认根目录为 `~/.bianbu-agent`，允许 `BIANBU_AGENT_HOME` 覆盖。
- Bibit Skill 发布到 `skills/<name>`，不做内容转换。
- Bibit MCP 以独立 Adapter 对齐板端已安装 CLI `bianbu-agent 0.1.17` 的实测契约，便于后续独立演进。
- Bibit Plugin 发布到 `plugins/<name>`，目录存在即表示 Agent 状态，不调用 Plugin enable/disable CLI。
- Cloud 每个 Plugin 版本只维护一个完整多 Agent 原始包。
- APM 每个 Plugin 版本只下载和缓存一份共享、不可变制品，同时生成 Codex 与 Claude 两份 marketplace。
- Codex、Claude Code、Hermes、Bibit 均以逻辑 `<name>` 作为运行时名称与 selector。
- 版本切换、卸载和中断恢复继续由既有 Plugin transaction 与 manifest 管理。

## 不在范围内

- 在当前设备安装或探测真实 `bianbu-agent`；
- 固化 Bibit MCP 未来可能新增的参数或输出字段；
- Cloud 生成、保存或返回 marketplace；
- 主动审计 Cloud 包是否完整适配所有 Agent；
- Cache refresh、TTL、GC、删除命令或旧 cache 自动迁移；
- 自动接管没有 APM manifest 的 Plugin；
- 修改 journal schema v1、跨 Agent 原子回滚或只读 Search/List 锁语义；
- 安装后额外执行 list 验证。

## Agent 契约

### 身份与探测

```text
Agent ID       bibit
CLI            bianbu-agent
环境变量       BIANBU_AGENT_HOME
默认根目录     <有效 HOME>/.bianbu-agent
Skill 根       <bibit-root>/skills
Plugin 根      <bibit-root>/plugins
```

Registry 以根目录存在且为目录判断 Bibit 已安装。CLI 可用性只在 MCP 操作时由公共 `cli_runner` 检查。Bibit 进入通用 Agent 快照；`--agent all` 继续按 Agent ID 字典序执行。

### Skill

Bibit 复用 profile 驱动的 filesystem Skill Adapter，发布目标为 `<bibit-root>/skills/<name>`。它沿用公共 staging、frontmatter 校验、原子 rename、manifest 与 filesystem transaction，不增加 `bibit_skill.go` 或空包装类型。

### MCP

Bibit MCP 使用独立 `bibit_mcp.go`，不与 `codex_mcp.go` 共享命令协议实现，只复用 `cli_runner`、MCP 公共类型、输入校验和 mutation 映射。契约对齐板端已安装 CLI `bianbu-agent 0.1.17`（依据 `bianbu-agent-cli-guide.md` 与实测）：

```text
bianbu-agent mcp get <name>
bianbu-agent mcp add <name> -- <command> [args...]
bianbu-agent mcp install <package-dir>   # 目录名必须是公开 ID mcp.<name>，HTTP 传输走此入口
bianbu-agent mcp remove <name>
```

`get` 接受短名或完整 ID，短名归一化为公开 ID `mcp.<name>`；存在时成功返回一个 `id` 精确匹配 `mcp.<name>` 的 JSON 对象，不存在时输出字面量 `null`（两者 exit 均为 0）。`add` 仅支持 stdio；HTTP 传输由适配器写临时目录包（`mcp.json` 的 `transport` 为 `streamable-http`）后经 `mcp install` 安装。输出截断、JSON 损坏、`id` 不匹配、CLI 不存在或未知失败均返回 unknown/fail closed。

### Plugin

Bibit Plugin Adapter 使用 filesystem deployment：

- `TargetPath(<name>)` 返回 `<bibit-root>/plugins/<name>`；
- `Inspect` 以目标物理目录存在为 present；
- `Add`、`Remove` 是 filesystem transaction 的无 CLI 激活钩子，合法请求返回 applied；
- 版本由 APM manifest 与 APM 在插件目录根派生的 `plugin.yaml` 表达。

Hermes 同样改为 `<hermes-root>/plugins/<name>`，但继续通过 `hermes plugins list/enable/disable` 管理 Agent 启用状态；版本由落盘叶子 `<agent-id>/plugins/<name>` 内的 `plugin.yaml` 表达，该文件携带 `schema_version: 1` 且不含 `author`（[ADR-036](../adr/ADR-036-per-agent-plugin-landing-filtering.md)；原 [ADR-035](../adr/ADR-035-share-derived-filesystem-plugin-manifest.md) 的「Hermes 与 Bibit 共用同一目录内容」已被取代）。

## 共享 Plugin 资源

### Cloud 布局与接口

Cloud 下载接口取消 Agent 参数：

```go
type Downloader interface {
    DownloadPlugin(context.Context, domain.PowerRef, string) error
}
```

Local Cloud 示例布局：

```text
internal/cloud/resources/plugins/
└── demo-plugin/
    └── v0.1.0/
        ├── apc-plugin.json
        ├── skills/
        └── ...
```

canonical 包只含 `apc-plugin.json` 与 `skills/...`；`.codex-plugin/plugin.json`、`.claude-plugin/plugin.json` 与 Hermes/Bibit 的 `plugin.yaml`、Hermes 注册器 `__init__.py` 都由 APM 在 cache miss 时按目标 Agent 派生到 `cache/plugins/<name>/<version>/landing/<agent-id>/plugins/<name>`，不属于 Cloud 内容（[ADR-017](../adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)、[ADR-036](../adr/ADR-036-per-agent-plugin-landing-filtering.md)）。安装目录只包含目标 Agent 落盘叶子的内容。

下载操作将整个版本目录原样复制到调用方目标；不会按 Agent 裁剪，也不会修改或删除其他 Agent 的入口。原始包不得携带 APM 生成的 marketplace。

Cloud 管理方负责包内容规范。APM 不预先要求或交叉核对所有 Agent manifest；某个 marketplace builder 或目标 Agent 实际需要的文件缺失、格式错误时按该操作失败。

### 共享缓存制品

Plugin Resource 接口改为 Agent 无关：

```go
type Manager interface {
    Ensure(context.Context, domain.PowerRef) (Artifact, error)
    Lookup(domain.PowerRef) (Artifact, error)
    StageFilesystem(domain.PowerRef) (string, error)
}
```

缓存键和制品布局：

```text
cache/plugins/<logical-name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<logical-name>/
    └── <完整 Cloud Plugin 包>
```

首次 cache miss 时，APM 在唯一临时目录中下载完整包，放入 `plugins/<name>`，读取 Codex/Claude manifest 生成两份 marketplace，完成本地安全检查后整体 rename 到最终缓存。任一 Agent 首次请求都会生成完整制品；缓存命中不重新下载、生成或修改。并发请求通过同一制品路径锁与发布后 Lookup 收敛为一次持久结果。

`Artifact` 只描述 `Power`、`Root`、`PluginPath` 和两个 marketplace source，不携带 Agent 身份。`MarketplaceFor(agent)` 仅支持 Codex/Claude；Hermes/Bibit 使用 `StageFilesystem` 将同一 `plugins/<name>` 物化到公共 staging。

marketplace 是 APM 派生缓存文件，不是 Cloud Plugin 内容。Codex 与 Claude builder 保持并列，各自使用原生 manifest 所需字段，marketplace 名固定为 `apm-<name>`，条目名和 source 子路径均使用逻辑 `<name>`。

### 校验边界

APM 省略多 Agent Cloud 包完整性审计，但保留：

- 下载与缓存树的路径边界、symlink 和特殊文件拒绝；
- marketplace 生成所必需的 JSON 读取；
- 缓存命中时的物理树和已生成 marketplace 可读性检查；
- Transaction recovery 对当前恢复方向所需 source 的只读检查；
- journal、manifest 和 Adapter fact 的既有 fail-closed 约束。

Validator 接受共享制品同时存在 Codex 与 Claude marketplace，不再执行“每个 Agent 只能出现一份 marketplace”的旧闭集规则。Transaction 继续直接依赖中立只读 validator，不依赖 Cloud 或 Plugin Resource。

## Plugin 命名与生命周期

所有 Agent 的运行时 Plugin 名称和 selector 都是逻辑 `<name>`。版本不再编码进实际名：

```text
PowerRef          demo-plugin@v0.1.0
runtime name      demo-plugin
selector          demo-plugin
marketplace name  apm-demo-plugin
cache root        cache/plugins/demo-plugin/v0.1.0
```

Codex/Claude 在 marketplace absent 的首次安装中依次注册目标版本缓存根、安装 `<name>`、提交 manifest。正常卸载会保留 marketplace；此后重装若发现名称和 source 都与目标共享制品精确一致且 Plugin absent，则直接复用该 retained marketplace，只安装 `<name>` 并提交 manifest。该重装的正常提交、回滚与恢复都不做多余的 unregister/register；仅当恢复方向已固定为 `rolling-back` 且 retained source 缺失时，才离线复验缓存并恢复原 source。版本切换仍依次删除旧 `<name>`、注销旧 source、以同名 marketplace 注册新版本 source、重新安装 `<name>`、提交 manifest。不同版本由 journal 的 `Power`/`Previous` 与 marketplace source 区分。

Hermes/Bibit 首装把 staging 原子发布到 `plugins/<name>`；版本切换把同一目标路径的旧目录隔离，再发布新目录。Hermes 执行 disable/enable，Bibit 激活钩子不调用 CLI。rollback 恢复被隔离的旧目录和 Agent 激活状态。

Install/Uninstall 继续以 APM manifest 为第一事实来源。精确命中返回 unchanged；manifest 指向的 Agent 状态完全缺失时清理对应清单项；部分、冲突、损坏或无 manifest 的受管目标继续 fail closed。Uninstall 与 Fix 不连接 Cloud，marketplace 卸载后仍保留。

Journal 保持 schema v1、`plugin` driver、phase、direction 和 payload 结构；`PluginTarget.ActualName`/`Selector` 的值变为逻辑名称。旧开发期 journal 与新语义不兼容，不增加 schema v2 或兼容分支。

## 模块职责与接口变化

- `domain`：声明 Bibit；把 Plugin 运行时名称收敛为逻辑名称。
- `adapter`：注册 Bibit profile；分别实现 Bibit MCP 与 Plugin；Agent 只决定路径、CLI 和部署方式。
- `cloud`：按 `PowerRef` 下载一份完整 Plugin 包。
- `pluginresource`：独占共享 cache、双 marketplace 生成和通用 filesystem staging。
- `pluginartifact`：提供 Plugin Resource 与 recovery 共享的中立只读安全检查。
- `lifecycle`：选择 marketplace/filesystem 消费方式，不生成 JSON 或直接执行 Agent 写操作。
- `transaction`：管理 marketplace/filesystem、manifest 与离线恢复，不连接 Cloud。

## 错误处理

- Bibit 根不存在时不进入已安装 Agent 快照；显式选择时返回 Agent 未安装。
- Bibit MCP CLI 不存在或输出不确定时保留 manifest/journal 并 fail closed。
- 共享 cache miss 只允许目标版本 Ensure 回源；旧版本、卸载与恢复只允许 Lookup。
- 已发布同版本 cache 不覆盖、不修复；损坏时保留现场并报错。
- 共享缓存 source 与 Agent 已注册同名 marketplace source 不一致时 fail closed。
- filesystem 目标是 symlink、非目录或不受控现有内容时不接管。

## 测试与验收

- Registry：Bibit ID、根目录、环境变量、能力矩阵和 all-agent 顺序。
- Skill：临时 Bibit home 下安装、重复、卸载、冲突与清理。
- MCP：独立 fake `bianbu-agent` 覆盖 stdio/HTTP、present/absent、remove、截断、损坏 JSON、CLI 缺失和未知错误，并精确断言参数。
- Plugin Adapter：Bibit 目录状态与无 CLI 激活；Hermes 逻辑路径与 CLI enable/disable。
- Cloud：共享版本目录下载、目标路径安全、无 Agent 子目录。
- Plugin Resource：一次下载、并发发布、共享 Root、双 marketplace、逻辑条目名、通用 staging、缓存损坏。
- Transaction：同一 filesystem 目标路径的升级/降级/rollback；同名 marketplace Plugin 的 source 切换与离线 recovery。
- Lifecycle：四 Agent 的 Plugin 首装、重复、升级、降级、卸载、manifest-first、无管理状态和 fix；Skill/MCP all-agent fail-fast。
- 隔离：所有 Agent home、APM home、cache、staging 与可执行文件位于 `t.TempDir()`，禁止调用真实 Agent CLI 或修改真实 home。
- 最终门禁：`gofmt`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、临时目录构建、`git diff --check`、旧 Agent-aware Plugin API/资源路径与真实可执行文件扫描。

## 文件布局计划

### 新增

- `internal/adapter/bibit_mcp.go`、`bibit_mcp_test.go`：独立 Bibit MCP 协议。
- `internal/adapter/bibit_plugin.go`、`bibit_plugin_test.go`：Bibit filesystem Plugin。
- `internal/pluginartifact/validation_test.go`：共享制品安全与 marketplace 检查。
- `ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md`：本次重大决策。
- `ivory-tower/features/018-bibit-agent/spec.md`：本规格。

### 修改

- `internal/domain/agent.go`、`plugin.go` 及测试：Bibit 与逻辑 Plugin 名称。
- `internal/adapter/registry.go`、profile/registry/Plugin 测试：Bibit 注册和四 Agent 能力。
- `internal/adapter/codex_plugin_test.go`、`claude_plugin_test.go`、`hermes_plugin_test.go`：纯逻辑 selector 与 Hermes 路径。
- `internal/adapter/plugin.go`：更新 target 和 filesystem 激活钩子约束。
- `internal/cloud/client.go`、`local_plugin.go` 及测试：共享下载接口与布局。
- `internal/cloud/resources/plugins/demo-plugin/*`：合并为一份 canonical 包，不再预置任何 Agent 原生入口。
- `internal/pluginresource/artifact.go`、`manager.go`、`marketplace.go`、两个 builder、filesystem helper 及测试：共享不可变制品。
- `internal/pluginartifact/validation.go`：共享 artifact 的按需只读校验。
- `internal/lifecycle/install_plugin.go`、`uninstall_plugin.go`、Plugin 集成/验收测试及 fake：通用 filesystem 流程与 Agent 无关资源。
- `internal/transaction/plugin_filesystem.go`、`plugin_marketplace_recovery.go`、`plugin_validation.go` 及测试：同名 target 与共享 source 恢复。
- `cmd/apm/main.go`、`main_test.go`：共享 Plugin Resource 注入。
- `BUILD.md`、Project Overview、C4 DSL/图、Feature 017/ADR-013 引用：同步当前架构。
- `docs/Changelog.md`：实现、复审和最终验证完成后追加一个请求级条目。

### 删除或重组

- 删除 Local Cloud Plugin 版本下的 `codex/`、`claude-code/`、`hermes-agent/` 分包目录，以共享内容替代。
- 删除 Plugin Resource 的 Agent builder 映射、Hermes 空 builder 与 Hermes 专用 staging 命名。
- 删除或替代版本化实际名称语义；不保留旧 selector/cache 兼容路径。

## 迁移策略

当前真实 `~/.apm` 没有旧 Plugin cache，不执行环境删除。仓库代码、fixture 和测试完全迁移到新布局。运行时不自动删除旧 cache；若开发环境后续发现旧目录，须先确认没有活动 journal，再人工清理。

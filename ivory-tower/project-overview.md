# Project Overview

> 本文档是项目设计的起点。先完成这里的关键信息，再建立 C4 模型、ADR 和 feature 设计。

## Introduction

- 项目名称：Agent Power Manager，简称 APM
- 交付目标：面向多 Agent，统一管理 Skill、Plugin、MCP 等 Power，提供能力发现、分发、安装和配置治理，并通过 Adapter 适配不同目标 Agent 的本地命令行工具。
- 代码工程：`apm`之下，`ivory-tower`仅作为文档归档目录。
- 当前状态：Search、List、Install、Uninstall 和 Fix 已通过独立 lifecycle 端口接线；Bibit、Codex、Claude Code 与 Hermes Agent 均支持 Skill、MCP 与 Plugin，Bibit 还支持专属 Agent Power（[Feature 019](features/019-agent-power/spec.md) 与 [ADR-015](adr/ADR-015-agent-power-bibit.md)）。Cloud Client 已通过显式 `ProviderRegistry` 支持 Local canonical fixture 与 Remote APC（[ADR-016](adr/ADR-016-select-cloud-clients-with-explicit-providers.md)）；Plugin 以 `apc-plugin.json` 为权威并由 APM 派生 Agent 原生配置（[ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)）。journal 正式格式见 [ADR-011](adr/ADR-011-reset-journal-schema-before-release.md)。
- 最小业务上下文：用户通过APM从云服务发现所需 Power，APM根据目标Agent将通用Power格式适配并安装到对应目录。

Power配置记录、文件锁等治理功能，不替代目标 Agent 的运行时。

## Scope

- 包含：
    - 提供 `search`、`list`、`install`、`uninstall`、`fix` 等 CLI 能力。
    - `search <keyword>` 搜索云端资源；`list` 默认列出云端资源，`list --installed` 列出本地安装清单。
    - `install <power>[=<version>]` 未指定版本时选择云端最新版，相同版本幂等成功，不同版本执行升级或显式降级。
    - 生命周期安全，在任意执行阶段中断，系统都可以识别并纠正。
    - Search、默认 List 和 installed List 在原只读业务前无锁 Inspect 唯一活动 journal：合法 journal Warn 并提示 `apm fix`，损坏或不可读 journal Warn 后继续；三者都不执行 Recover。
    - 默认 `list` 在 Inspect 后列出 Cloud 全部版本；`list --installed` 无锁刷新已安装 Agent 并读取 Config。`--agent=all` 继续输出成功记录和 partial error，但不承诺跨 Agent manifest、journal 与 target 的全局一致快照。
    - `cmd/apm.newCloudClient` 为每次 Search、默认 List、installed List、Install、Uninstall lifecycle 创建独立 `ProviderRegistry`，显式注册 `local|remote` 并只打开配置选中的 provider；默认 Remote，错误不自动 fallback Local。installed List 在构造阶段仍校验 provider，但进入查询后只读 Agent/manifest，不访问网络。Fix 不创建 Cloud Client。
    - installed List、Install 和 Uninstall 在本地操作前扫描已注册 Agent 的根目录，生成当前命令内按 Agent ID 字典序的“已支持且已安装 Agent”快照；installed List 的快照是无锁观察，Install/Uninstall 的快照位于各自锁区间内。
    - Fix 在一次全局锁区间内恰好 Recover 一次并退出；没有 journal 时普通模式静默、verbose 模式 Debug，恢复成功时同步 Warn。损坏 journal 不能自动修复，Fix 会保留现场并非零退出。
    - Install 在一个锁区间内完成 Recover、Warn、快照刷新、Cloud 和串行 Transaction；Skill 与 Agent 由 Cache 的 `PrepareSkill`/`PrepareAgent` 独占版本化 cache 发布与 staging（Agent 为单文件、原样字节、仅 Bibit），MCP 由 `GetMCP` 直接取得定义；Plugin Resource 从 canonical APC package 生成或复验一份共享不可变 artifact，Codex/Claude 使用其中的本地 marketplace，Hermes/Bibit 使用同一 Plugin 叶子的 filesystem staging。成功 Release 后才返回 outcome。
    - Uninstall 在一个锁区间内完成 Recover、Warn、快照刷新、逐 Agent 本地清单解析和串行 Transaction；Plugin 卸载只离线 `Lookup` manifest 指向的精确 artifact，不 `Ensure`、不连接 Cloud、正常路径保留本地 marketplace 与 artifact。MCP 先 Inspect，只有 target present 才按 manifest 精确版本 `GetMCP`，为回滚保留定义。
    - Skill、Agent、MCP 与 Plugin 共用唯一 `transactions/active.json`（Agent 复用 `filesystem` driver，target 为文件路径）。schema v1 shared envelope 以 `filesystem`/`agent-cli`/`plugin` driver 区分互斥载荷；其他格式返回 `ErrInvalidJournal` 并原样保留。
    - 实现类似apt的文件锁，保证软件执行安全。
    - 缓存下载的Power。
    - 可增添的Agent Power Adapters。
    - 已安装Power记录。
- 不包含：
    - 替代目标 Agent 的运行时，或定义目标 Agent 如何执行已安装的 Skill、Plugin、MCP 配置和 Agent 指令。

## Architecture Snapshot

- 系统边界与外部依赖：Agent Power Manager 位于 APC/开发 fixture、能力使用方与目标 Agent 之间；Remote Provider 通过 APC 匿名只读 HTTP API 获取 Published Catalog 与精确版本内容，Local Provider 读取开发 fixture。目标 Agent 自身的运行时位于 Agent Power Manager 边界之外。
- 模块边界与职责：
    - Composition Root：`cmd/apm/cloud.go` 的 `newCloudClient` 直接 import `internal/cloud/local` 与 `internal/cloud/remote`，每次把两者的构造 closure 注册到实例级 registry，再依据环境只 `Open` 一个 provider。Registry 保持中立，只按 ID 选择 factory 并返回 `cloud.Client`，不依赖具体 provider。Install 将同一个 Client 注入 lifecycle、Cache 与 Plugin Resource；Fix composition 不包含 Cloud。
    - Cloud Client：`internal/cloud.Client` 由 `Catalog`、`SkillSource`、`PluginSource`、`MCPSource`、`AgentSource` 五个小端口组成。`List` 返回四类全部精确版本，`Resolve` 把 latest/精确查询收敛为精确 `PowerRef`；provider 只验证和写调用方私有目标，不解释 APM cache、staging、manifest 或 journal。
    - Provider Registry：`internal/cloud.ProviderRegistry` 是显式实例，不使用 package `init()` 或全局可变注册表，也不 import Local/Remote；它只保存 factory、选择 ID、构造并返回 `cloud.Client`。Local 与 Remote 都实现完整 `cloud.Client`，由 composition root 静态链接在单一二进制中；配置、网络或协议错误都不尝试另一 factory。
    - Local Provider：`internal/cloud/local` 从绝对 canonical fixture 根读取 `skills/`、`plugins/`、`mcps/`、`agents/`，复制物理目录/文件或严格解码 MCP；拒绝 symlink、hardlink、特殊节点、路径逃逸、重叠和非空目标。
    - Remote Provider：`internal/cloud/remote` 消费 APC 只读 HTTP API，遍历固定 snapshot 的 cursor 页，执行 latest/exact metadata 查询，验证 HTTP content type、size、MD5 后才解包或解码；Skill/Plugin 使用受限安全归档，Agent 保留原始 YAML 字节，MCP 返回严格领域对象。Linux/macOS 支持完整目录与文件发布；FreeBSD 支持 descriptor-relative staging、Agent 文件发布和 MCP，但 Skill/Plugin 目录发布返回 `ENOTSUP`；Windows/其它平台的 content 操作 fail closed。composition root 注入两分钟整体 HTTP timeout，更短的 context deadline 优先；Remote 不携带 API Key、不自动重试、不跨 origin redirect。
    - 核心生命周期：提供独立的 Search、List、Install、Uninstall 与 Fix 用例。Search/List 返回结构化结果且不渲染业务输出；三种只读查询只无锁检查 journal，Fix/Install/Uninstall 才在 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 定义的边界内锁定并恢复。
    - 配置模型：通过无状态 Repository 维护每个 Agent 按 Skill、Agent、Plugin、MCP 分组的 Power 安装清单。
    - Cache Manager：`internal/cache` 通过包内 `Source` 小接口依赖同一个 Cloud Client；`PrepareSkill`/`PrepareAgent` 负责 cache hit 复验、唯一临时目标、下载、marker 复验、按精确版本原子发布和公共 staging。Cloud provider 不直接写 `apm.powerCache`。
    - Plugin Resource：`internal/pluginresource` 独占 canonical APC Plugin 下载、`cache/plugins/<name>/<version>` 共享不可变 artifact、Codex/Claude 两份单条目本地 marketplace 和通用 filesystem staging。它通过 `internal/pluginartifact` 严格解析 `apc-plugin.json` 的 Skill 闭集，先确定性生成 Codex、Claude、Hermes 原生配置，再让 marketplace builder 读取并复验 Codex/Claude 原生 manifest 后生成两份 marketplace；Transaction recovery 共用同一中立只读 Validator，但不调用 Plugin Resource。
    - Adapter Registry：按 Agent 分别提供 `ResolveSkill`、`ResolveAgent`、`ResolveMCP`、`ResolvePlugin`；其中 `ResolveAgent` 仅 Bibit，对应 `<bibit根>/agents/<agent.id>/agent.yaml` 单文件发布。Codex/Claude Plugin Adapter 管理本地 marketplace；Hermes/Bibit Plugin Adapter 管理逻辑名目录，其中 Hermes 通过 CLI enable/disable，Bibit 不执行 Plugin CLI。Bibit MCP 使用独立 Adapter；MCP/Plugin 都不追加安装后的 list 验证。
    - CLI：提供入口，校验并解析命令参数；Search、List、Install、Uninstall 与 Fix 通过五个独立 factory 创建专属端口。CLI 渲染前四者的结构化结果；Fix 没有 stdout 业务结果。
- 数据与接口：每个 Agent 单独维护一份固定 `skills`、`agents`、`plugins`、`mcps` 分组的 JSON 安装清单，条目统一为精确 `vMAJOR.MINOR.PATCH` 的 `PowerRef`。Config Repository 在 `List/Find/Set/Remove` 内封装完整 manifest 验证、读取和原子读改写；同一 Type/Name 重复合法，Find 取第一条而 Remove 清除全部，非法历史版本不会迁移或清理，详见 [ADR-007](adr/ADR-007-use-stateless-config-repository.md)。用户侧 CLI 语法详见 [ADR-002](adr/ADR-002-cli-agent-and-power-type-as-flags.md)：Search 没有 Agent 维度；默认 List 禁止显式 `--agent`，installed List 必须提供 Agent。本地 Power 操作中，`--agent` 的具体值必须存在于按 [ADR-004](adr/ADR-004-discover-installed-agents-before-local-operations.md) 排序的快照，`all` 对快照中的全部 Agent 执行。Search 与两种 List 都在业务前无锁 Inspect，合法活动 journal 提示 `apm fix`，损坏 journal Warn 后继续；Fix/Install/Uninstall 才锁内 Recover，见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)。Cloud `List(ctx)` 返回四类带版本的全部 `PowerRef`；`Resolve(ctx, ref)` 对 latest 或指定版本返回精确引用。Skill/Plugin destination 是调用方私有目录，Agent destination 是调用方私有 `agent.yaml`，MCP 是无文件 cache 的领域对象。

Install 与 Uninstall 分别以独立 factory 和生命周期端口接线，不复用或耦合 Search/List 的锁边界。Install 显式版本必须为规范 `vMAJOR.MINOR.PATCH`，结果行精确为 `<agent>/<type>/<name>@<version> <outcome>`。Uninstall 拒绝版本，从每个 Agent 的本地清单独立解析 Type/Version，并输出完整 PowerRef 或 Agent-only 的结果行。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制。`APM_CLOUD_PROVIDER` 默认 `remote`，`APC_BASE_URL` 默认 `http://127.0.0.1:8080`；开发 fixture 需显式设置 `APM_CLOUD_PROVIDER=local` 与 clean absolute `APM_CLOUD_LOCAL_ROOT`。Skill/Agent cache 与 staging 归 `internal/cache`，MCP 不进入 Cache；Plugin Resource 独占 `cache/plugins/<logical-name>/<version>` 的共享 artifact 与 filesystem staging。详见 [ADR-010](adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)、[ADR-011](adr/ADR-011-reset-journal-schema-before-release.md)、[ADR-016](adr/ADR-016-select-cloud-clients-with-explicit-providers.md) 与 [ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

### Agent 与 Power 能力

| Agent ID | Profile 根 | Skill | MCP | Plugin | Agent |
| --- | --- | --- | --- | --- | --- |
| `bibit` | `BIANBU_AGENT_HOME`，默认 `~/.bianbu-agent` | `skills/<name>` | 独立 `bianbu-agent mcp` Adapter | `plugins/<name>`；目录即状态，不调用 Plugin CLI | `agents/<agent.id>/agent.yaml`；单文件、原样字节，仅 Bibit |
| `claude-code` | `CLAUDE_CONFIG_DIR`，默认 `~/.claude` | `skills/<name>` | Claude CLI user scope | 共享制品中的 Claude marketplace | 不支持 |
| `codex` | `CODEX_HOME`，默认 `~/.codex` | `skills/<name>` | Codex CLI | 共享制品中的 Codex marketplace | 不支持 |
| `hermes-agent` | `HERMES_HOME`，默认 `~/.hermes` | `skills/<name>` | Hermes CLI | `plugins/<name>`；CLI enable/disable | 不支持 |

Registry 仅以 profile 根存在且为目录判断 Agent 已安装；`--agent=all` 按 Agent ID 字典序使用当前快照。Bibit 的接口与路径约束见 [Feature 018](features/018-bibit-agent/spec.md)，Plugin 的当前 canonical/派生边界见 [ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

Agent Power 仅 Bibit 支持（[Feature 019](features/019-agent-power/spec.md) 与 [ADR-015](adr/ADR-015-agent-power-bibit.md)）：Local canonical fixture 为 `agents/<name>/<version>/agent.yaml`，Remote exact content 为 `application/yaml` 原始字节；Cache 的 `PrepareAgent` 将其按精确版本发布到 `cache/agents/<name>/<version>/agent.yaml` 并物化 staging，最终安装到 `<bibit根>/agents/<agent.id>/agent.yaml`（单文件、原样字节，本地不版本化）。`--type agent` 的 `--agent` 规则：install/uninstall 省略 `--agent` 时默认 `bibit`，`list --installed` 省略 `--agent` 时默认 `bibit`，`search`/`list` 的 Cloud 模式仅过滤结果、不要求 `--agent`；显式 `--agent bibit` 允许，其它值（含 `all`）报错。

### Plugin 当前安装与卸载边界

当前 Plugin 语义以 [Feature 020](features/020-cloud-client-providers/spec.md) 和 [ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 为准；[ADR-014](adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅作为被取代的历史背景。Cloud 的 `DownloadPlugin(ctx, ref, destination)` 只把 canonical `apc-plugin.json + skills/...` 写入 Plugin Resource 提供的目标目录；Cloud 不生成 marketplace 或 Agent 原生配置。每个逻辑 Plugin 版本只有一份共享 artifact：

```text
cache/plugins/<logical-name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<logical-name>/
    ├── apc-plugin.json
    ├── .codex-plugin/plugin.json
    ├── .claude-plugin/plugin.json
    ├── .hermes-plugin/plugin.yaml
    ├── .hermes-plugin/__init__.py
    └── skills/...
```

`apc-plugin.json` 严格定义 name、version、description 和声明 Skill 闭集。Plugin Resource 在唯一 cache-miss 临时根中保留 canonical package，先生成 Codex、Claude、Hermes 原生 manifest，再由 marketplace builder 读取并复验 Codex/Claude manifest 以生成两份 marketplace，经 `internal/pluginartifact` 复验后一次 rename 发布。Plugin 的运行时名称、`PluginTarget` selector 和制品叶子均使用逻辑 `<name>`；版本仅由 `PowerRef`、APM manifest、缓存版本目录与派生原生 manifest 表达。Install 的精确顺序是：锁内 Recover 与 Agent 选择后，显式规范 Plugin ref 会先检查全部所选 Agent manifest；全部精确命中则立即 `unchanged`，不访问 Cloud、Plugin Resource、Adapter 或 Transaction，任一不匹配则回到既有 Cloud 解析路径。普通 Plugin 路径对每个 Agent 先读取 manifest，再统一 `ResolvePlugin(agent)` 并按 deployment 分支。Codex/Claude marketplace 分支在检查 Agent 事实前 `Ensure(ctx, ref)` 并取得目标 marketplace；无 manifest 时只检查目标 marketplace/Plugin，不查询旧 artifact，有 manifest 时依次检查旧 marketplace 与旧 Plugin，只有两者 present 才离线 `Lookup(previous)` 并核对 source/selector，两者 absent 则移除 stale manifest 后转入目标事实检查，部分存在则 fail closed。Hermes/Bibit filesystem 分支先完成旧或目标 Plugin 的本地事实与物理缺失校验，之后才 `Ensure`、`StageFilesystem` 和事务发布。marketplace 首装执行“注册 source、添加逻辑名 Plugin、写 manifest”；正常卸载后若 marketplace 的名称和 source 精确匹配且 Plugin absent，重装则复用 retained source，只执行“添加 Plugin、写 manifest”。retained 重装的正常提交、回滚与恢复不做多余的 unregister/register；仅当恢复方向已固定为 `rolling-back` 且 retained source 缺失时，才离线复验缓存并恢复 source。版本切换仍执行“删除旧 Plugin、切换同名 marketplace source、重新添加逻辑名 Plugin、写 manifest”。

Uninstall 先从 manifest 解析精确版本；没有记录时返回 Agent-only `unexecuted`，不查询 Agent。存在记录时检查该版本的 Adapter 事实；Codex/Claude 要离线 `Lookup` 共享 artifact 并验证 marketplace/source/selector 后，在 transaction 中只移除 Plugin 和 manifest，保留 marketplace 与 artifact；Hermes/Bibit 只处理逻辑名目录与 manifest。marketplace/Plugin 部分存在、身份或 source 不一致、旧 artifact 缺失或损坏、journal 非法或事实不可读时均保留现场和 manifest 并 fail closed。Plugin Resource 与 recovery 共用中立只读 Validator，验证 canonical manifest、派生原生配置、marketplace 和物理树；recovery 只读取 journal、Adapter、manifest 与既有 artifact，绝不连接 Cloud、调用 Plugin Resource 或修改缓存，因此可离线收敛。

## Test Filesystem Isolation

APM 支持仅供测试使用的 `APM_HOME` 环境变量，使测试能在临时目录中模拟 Agent 安装状态和完整的本地 Power 生命周期，不读写用户目录。该路径决策详见 [ADR-005](adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

### 测试与验收

- 路径单元测试覆盖 `APM_HOME` 未设置、空值回退、非空值优先于 HOME，lock 的隔离路径，以及缓存的 `XDG_CACHE_HOME > APM_HOME > 系统默认` 优先级。
- 修改环境变量的测试不得使用 `t.Parallel()`，避免共享进程环境互相污染。
- 在临时 `APM_HOME` 中创建伪 Agent 根目录，可分别模拟 Agent 已安装和未安装状态。后续生命周期测试在其中执行 Power 安装、卸载、清单读写、锁竞争、事务与恢复。
- MCP/Plugin 自动化测试还必须只使用临时 fake-bin 作为 `PATH`，把四个 Agent Home 和 CLI 状态指向同一临时测试根，并逐次断言实际可执行路径位于 fake-bin；禁止调用用户真实 Bibit、Codex、Claude Code 或 Hermes CLI。
- 要求所有变化均位于同一临时根的测试，必须清空 `XDG_CACHE_HOME` 以使缓存回退到 `APM_HOME`，或将 `XDG_CACHE_HOME` 也指向该测试根内；验收时必须证明没有读写预期边界之外的文件。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败由 CLI 统一记录。Search、默认 List 和 installed List 全程无锁，只提供 best-effort 观察，可能出现漏警告、过期警告、跨 Agent 混合 manifest 与 target/manifest 暂时不匹配；全局一致读取需要锁或 MVCC，当前不承诺。Fix、Install 与 Uninstall 的恢复由全局锁串行化，后两者从 Recover 到每个串行 Transaction 持有各自单次锁并在成功 Release 后渲染（[ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）。 |
| 安全与合规 | MCP CLI 不经 shell，动态解析 PATH，单次调用固定 30 秒超时且 stdout/stderr 有界；状态输出不匹配时 fail closed 并保留 journal。MCP 定义不包含密钥字段，HTTP endpoint 只允许 HTTPS。Remote 验证精确 metadata、Content-Type、size、MD5、归档限制与已知归档/可执行签名；Linux/macOS 支持完整 archive publication，FreeBSD 的 Skill/Plugin 目录发布以 `ENOTSUP` fail closed，Windows/其它平台 content 操作 fail closed。受信目录链要求 owner 为 root/当前 euid 且 `(mode & 0022) == 0`；仅生产系统临时根允许 root-owned sticky 例外，其直接条目必须由当前 euid 拥有。同一 euid 主动篡改不在隔离边界内。 |
| 技术与交付 | APM 仅在相关命令中通过 Agent 根目录是否存在来感知安装状态，不承担目标 Agent 的安装、卸载、实时监控或运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置与 Power 格式差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 清单格式和本地文件权限已确定；跨来源内容冲突和更细粒度的权限策略尚未确定。
    - 损坏或不可读 journal 无法安全推断提交方向；只读命令只告警并继续，Fix/Install/Uninstall 保留现场并 fail closed，需要人工诊断。
    - Remote 失败不回退 Local，避免把开发 fixture 误表述为 APC 当前目录；默认 APC 尚不可用时，开发者必须显式选择 Local。
    - Remote content 的 descriptor-relative staging 覆盖 Linux、macOS 与 FreeBSD，但 FreeBSD 缺少目录 no-replace publication，Skill/Plugin 因 `ENOTSUP` fail closed；Windows 和其它平台 content 操作 fail closed。跨平台完整支持需要在保持同等物理目录约束后单独实现。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

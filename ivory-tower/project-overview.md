# Project Overview

> 本文档是项目设计的起点。先完成这里的关键信息，再建立 C4 模型、ADR 和 feature 设计。

## Introduction

- 项目名称：Agent Power Manager，简称 APM
- 交付目标：面向多 Agent，统一管理 Skill、Plugin、MCP 等 Power，提供能力发现、分发、安装和配置治理，并通过 Adapter 适配不同目标 Agent 的本地命令行工具。
- 代码工程：`apm`之下，`ivory-tower`仅作为文档归档目录。
- 当前状态：Search、List、Install、Uninstall 和 Fix 已通过独立 lifecycle 端口接线；Codex、Claude Code 与 Hermes Agent 均支持 Skill、MCP 与 Plugin。Plugin 当前架构见 [Feature 017](features/017-local-plugin-marketplace/spec.md) 和 [ADR-013](adr/ADR-013-generate-per-plugin-local-marketplaces.md)，journal 正式格式见 [ADR-011](adr/ADR-011-reset-journal-schema-before-release.md)。
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
    - installed List、Install 和 Uninstall 在本地操作前扫描已注册 Agent 的根目录，生成当前命令内按 Agent ID 字典序的“已支持且已安装 Agent”快照；installed List 的快照是无锁观察，Install/Uninstall 的快照位于各自锁区间内。
    - Fix 在一次全局锁区间内恰好 Recover 一次并退出；没有 journal 时普通模式静默、verbose 模式 Debug，恢复成功时同步 Warn。损坏 journal 不能自动修复，Fix 会保留现场并非零退出。
    - Install 在一个锁区间内完成 Recover、Warn、快照刷新、Cloud 和串行 Transaction；Skill 使用 Cache/Staging，MCP 由 `GetMCP` 直接取得定义；Plugin 由 Plugin Resource 为目标 Agent 生成或复验不可变 artifact，Codex/Claude 使用本地 marketplace，Hermes 使用 artifact staging。成功 Release 后才返回 outcome。
    - Uninstall 在一个锁区间内完成 Recover、Warn、快照刷新、逐 Agent 本地清单解析和串行 Transaction；Plugin 卸载离线执行，不连接 Cloud，只移除 manifest 指向的精确 Plugin，正常路径保留本地 marketplace 与 artifact。
    - Skill、MCP 与 Plugin 共用唯一 `transactions/active.json`。schema v1 shared envelope 以 `filesystem`/`agent-cli`/`plugin` driver 区分互斥载荷；其他格式返回 `ErrInvalidJournal` 并原样保留。
    - 实现类似apt的文件锁，保证软件执行安全。
    - 缓存下载的Power。
    - 可增添的Agent Power Adapters。
    - 已安装Power记录。
- 不包含：
    - 替代目标 Agent 的运行时，或定义目标 Agent 如何执行已安装的 Skill、Plugin、MCP 配置和 Agent 指令。

## Architecture Snapshot

- 系统边界与外部依赖：Agent Power Manager 位于能力提供方、能力使用方与目标 Agent 之间；对外依赖目标 Agent的Power接入方式。目标 Agent 自身的运行时位于 Agent Power Manager 边界之外。
- 模块边界与职责：
    - 核心生命周期：提供独立的 Search、List、Install、Uninstall 与 Fix 用例。Search/List 返回结构化结果且不渲染业务输出；三种只读查询只无锁检查 journal，Fix/Install/Uninstall 才在 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 定义的边界内锁定并恢复。
    - 配置模型：通过无状态 Repository 维护每个 Agent 按 Skill、Plugin、MCP 分组的 Power 安装清单。
    - Plugin Resource：独占原始 Plugin 下载、agent-aware immutable artifact、Codex/Claude 单条目本地 marketplace 生成与 Hermes staging。它依赖 Cloud、缓存根和并列的 Codex/Claude marketplace builder；Hermes 没有 marketplace builder。
    - Adapter Registry：按 Agent 分别提供 `ResolveSkill`、`ResolveMCP`、`ResolvePlugin`。Codex/Claude Plugin Adapter 管理本地 marketplace 与 selector；Hermes Plugin Adapter 管理版本化目录并通过 CLI enable/disable。MCP/Plugin 都不追加安装后的 list 验证。
    - CLI：提供入口，校验并解析命令参数；Search、List、Install、Uninstall 与 Fix 通过五个独立 factory 创建专属端口。CLI 渲染前四者的结构化结果；Fix 没有 stdout 业务结果。
- 数据与接口：每个 Agent 单独维护一份固定 `skills`、`plugins`、`mcps` 分组的 JSON 安装清单，条目统一为精确 `vMAJOR.MINOR.PATCH` 的 `PowerRef`。Config Repository 在 `List/Find/Set/Remove` 内封装完整 manifest 验证、读取和原子读改写；同一 Type/Name 重复合法，Find 取第一条而 Remove 清除全部，非法历史版本不会迁移或清理，详见 [ADR-007](adr/ADR-007-use-stateless-config-repository.md)。用户侧 CLI 语法详见 [ADR-002](adr/ADR-002-cli-agent-and-power-type-as-flags.md)：Search 没有 Agent 维度；默认 List 禁止显式 `--agent`，installed List 必须提供 Agent。本地 Power 操作中，`--agent` 的具体值必须存在于按 [ADR-004](adr/ADR-004-discover-installed-agents-before-local-operations.md) 排序的快照，`all` 对快照中的全部 Agent 执行。Search 与两种 List 都在业务前无锁 Inspect，合法活动 journal 提示 `apm fix`，损坏 journal Warn 后继续；Fix/Install/Uninstall 才锁内 Recover，见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)。Cloud `list` 返回带版本的全部 `PowerRef`；Search 选择最新版本，List 保留所有版本。

Install 与 Uninstall 分别以独立 factory 和生命周期端口接线，不复用或耦合 Search/List 的锁边界。Install 显式版本必须为规范 `vMAJOR.MINOR.PATCH`，结果行精确为 `<agent>/<type>/<name>@<version> <outcome>`。Uninstall 拒绝版本，从每个 Agent 的本地清单独立解析 Type/Version，并输出完整 PowerRef 或 Agent-only 的结果行。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制；开发阶段以 `internal/cloud/resources` 模拟云端。Skill 使用既有 Cache/Staging，MCP 不进入 Cache；Plugin Resource 独占 `cache/plugins/<agent>/<logical-name>/<version>` 的 artifact 与 Hermes staging。详见 [ADR-010](adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)、[ADR-011](adr/ADR-011-reset-journal-schema-before-release.md) 与 [ADR-013](adr/ADR-013-generate-per-plugin-local-marketplaces.md)。

### Plugin 当前安装与卸载边界

当前 Plugin 语义以 [Feature 017](features/017-local-plugin-marketplace/spec.md) 和 [ADR-013](adr/ADR-013-generate-per-plugin-local-marketplaces.md) 为准。Cloud 只将指定 `agent + PowerRef` 的原始包下载到 Plugin Resource 提供的目标目录；它不提供 marketplace。Plugin artifact 的稳定布局为：

```text
cache/plugins/<agent>/<logical-name>/<version>/
├── .agents/plugins/marketplace.json       # 仅 Codex
├── .claude-plugin/marketplace.json        # 仅 Claude Code
└── plugins/<logical-name>-v<version>/     # Agent 原始包
```

Install 的精确顺序是：先 `Config.Find`；manifest 已含目标精确版本则立即 `unchanged`，不访问 Cloud、Plugin Resource、Adapter 或 Transaction；否则 `Ensure` 目标 artifact（仅 miss 时 Cloud 下载）、解析 Adapter、检查目标及旧版本的 Agent 事实。Codex/Claude 的旧版本还必须离线 `Lookup` 旧 artifact，随后在同一 Plugin transaction 中首装“注册 marketplace、添加 Plugin、写 manifest”，或切换“删除旧 Plugin、注销旧 marketplace、注册新 marketplace、添加新 Plugin、写 manifest”。Hermes 在所有事实校验后才 `StageHermes` 并走 filesystem transaction。

Uninstall 先从 manifest 解析精确版本；没有记录时返回 Agent-only `unexecuted`，不查询 Agent。存在记录时检查该版本的 Adapter 事实；Codex/Claude 要离线 `Lookup` artifact 并验证 marketplace/source/selector 后，在 transaction 中只移除 Plugin 和 manifest，保留 marketplace 与 artifact；Hermes 只处理精确目录与 manifest。marketplace/Plugin 部分存在、身份或 source 不一致、旧 artifact 缺失或损坏、journal 非法或事实不可读时均保留现场和 manifest 并 fail closed；recovery 只使用 journal、Adapter、manifest 与不可变 artifact，绝不连接 Cloud。

## Test Filesystem Isolation

APM 支持仅供测试使用的 `APM_HOME` 环境变量，使测试能在临时目录中模拟 Agent 安装状态和完整的本地 Power 生命周期，不读写用户目录。该路径决策详见 [ADR-005](adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

### 测试与验收

- 路径单元测试覆盖 `APM_HOME` 未设置、空值回退、非空值优先于 HOME，lock 的隔离路径，以及缓存的 `XDG_CACHE_HOME > APM_HOME > 系统默认` 优先级。
- 修改环境变量的测试不得使用 `t.Parallel()`，避免共享进程环境互相污染。
- 在临时 `APM_HOME` 中创建伪 Agent 根目录，可分别模拟 Agent 已安装和未安装状态。后续生命周期测试在其中执行 Power 安装、卸载、清单读写、锁竞争、事务与恢复。
- MCP/Plugin 自动化测试还必须把 `PATH` 前置为临时 fake-bin，把三个 Agent Home 和 CLI 状态指向同一临时测试根，并逐次断言实际可执行路径位于 fake-bin；禁止调用用户真实 Codex、Claude Code 或 Hermes CLI。
- 要求所有变化均位于同一临时根的测试，必须清空 `XDG_CACHE_HOME` 以使缓存回退到 `APM_HOME`，或将 `XDG_CACHE_HOME` 也指向该测试根内；验收时必须证明没有读写预期边界之外的文件。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败由 CLI 统一记录。Search、默认 List 和 installed List 全程无锁，只提供 best-effort 观察，可能出现漏警告、过期警告、跨 Agent 混合 manifest 与 target/manifest 暂时不匹配；全局一致读取需要锁或 MVCC，当前不承诺。Fix、Install 与 Uninstall 的恢复由全局锁串行化，后两者从 Recover 到每个串行 Transaction 持有各自单次锁并在成功 Release 后渲染（[ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）。 |
| 安全与合规 | MCP CLI 不经 shell，动态解析 PATH，单次调用固定 30 秒超时且 stdout/stderr 有界；状态输出不匹配时 fail closed 并保留 journal。MCP 定义不包含密钥字段。 |
| 技术与交付 | APM 仅在相关命令中通过 Agent 根目录是否存在来感知安装状态，不承担目标 Agent 的安装、卸载、实时监控或运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置与 Power 格式差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 清单格式和本地文件权限已确定；跨来源内容冲突和更细粒度的权限策略尚未确定。
    - 损坏或不可读 journal 无法安全推断提交方向；只读命令只告警并继续，Fix/Install/Uninstall 保留现场并 fail closed，需要人工诊断。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

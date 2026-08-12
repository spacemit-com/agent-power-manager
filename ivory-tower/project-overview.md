# Project Overview

> 本文档是项目设计的起点。先完成这里的关键信息，再建立 C4 模型、ADR 和 feature 设计。

## Introduction

- 项目名称：Agent Power Manager，简称 APM
- 交付目标：面向多 Agent，统一管理 Skill、Plugin、MCP 等 Power，提供能力发现、分发、安装和配置治理，并通过 Adapter 适配不同目标 Agent 的本地命令行工具。
- 代码工程：`apm`之下，`ivory-tower`仅作为文档归档目录。
- 当前状态：Search、List、Install 和 Uninstall 已通过独立 lifecycle 端口接线；Codex、Claude Code 与 Hermes Agent 均支持 Skill 文件安装和 MCP CLI 注册，Plugin Adapter 尚未实现。MCP 架构决策见 [ADR-010](adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)。
- 最小业务上下文：用户通过APM从云服务发现所需 Power，APM根据目标Agent将通用Power格式适配并安装到对应目录。

Power配置记录、文件锁等治理功能，不替代目标 Agent 的运行时。

## Scope

- 包含：
    - 提供 `search`、`list`、`install`、`uninstall` 等 CLI 能力。
    - `search <keyword>` 搜索云端资源；`list` 默认列出云端资源，`list --installed` 列出本地安装清单。
    - `install <power>[=<version>]` 未指定版本时选择云端最新版，相同版本幂等成功，不同版本执行升级或显式降级。
    - 生命周期安全，在任意执行阶段中断，系统都可以识别并纠正。
    - Search 在全局锁内检查并恢复唯一活动 journal，恢复成功后同步记录 Warn；释放锁后查询 Cloud、筛选并返回结构化结果。
    - 默认 `list` 无锁、无 Recover 地列出 Cloud 的全部版本；`list --installed` 在同一全局锁区间完成 Recover、实时 Warn、已安装 Agent 刷新和全部 Config 读取，成功 Release 后才计算并渲染。`--agent=all` 仅在成功 Release 后允许输出成功记录和 partial error。
    - installed List、Install 和 Uninstall 在本地操作前扫描已注册 Agent 的根目录，生成当前命令内按 Agent ID 字典序的“已支持且已安装 Agent”快照；Search、List、Install 与 Uninstall 均已接线。
    - Install 在一个锁区间内完成 Recover、Warn、快照刷新、Cloud 和串行 Transaction；Skill 分支使用 Cache/Staging，MCP 分支由 Cloud `GetMCP` 直接取得精确版本启动定义并通过目标 Agent CLI 注册，不创建 Cache/Staging。成功 Release 后才返回 CLI 可渲染的六种 outcome，跨 Type 同名必须在版本选择前显式指定 `--type`。
    - Uninstall 在一个锁区间内完成 Recover、Warn、快照刷新、逐 Agent 本地清单解析和串行 Transaction；Skill 通过文件系统 quarantine，MCP 通过目标 Agent CLI 删除。成功时只删除选定 Type/Name 清单项；清单无记录为幂等 Agent-only `unexecuted`，受管目标缺失由 transaction 返回并由 lifecycle 在锁内同步 Warn。
    - Skill 与 MCP 共用唯一 `transactions/active.json`。schema v2 以 `filesystem`/`agent-cli` driver 区分载荷，仍可只读恢复 schema v1 Skill journal；任意下一条恢复型命令会先串行恢复上一条 Skill 或 MCP 事务。
    - 实现类似apt的文件锁，保证软件执行安全。
    - 缓存下载的Power。
    - 可增添的Agent Power Adapters。
    - 已安装Power记录。
- 不包含：
    - 替代目标 Agent 的运行时，或定义目标 Agent 如何执行已安装的 Skill、Plugin、MCP 配置和 Agent 指令。

## Architecture Snapshot

- 系统边界与外部依赖：Agent Power Manager 位于能力提供方、能力使用方与目标 Agent 之间；对外依赖目标 Agent的Power接入方式。目标 Agent 自身的运行时位于 Agent Power Manager 边界之外。
- 模块边界与职责：
    - 核心生命周期：提供独立的 Search、List、Install 与 Uninstall 用例并返回结构化结果，不渲染业务输出；Cloud List 不访问共享状态，installed List、Install 与 Uninstall 的各自共享状态区间见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)。
    - 配置模型：通过无状态 Repository 维护每个 Agent 按 Skill、Plugin、MCP 分组的 Power 安装清单。
    - Adapter Registry：按 Agent 分别解析 Skill 文件 Adapter 与 MCP CLI Adapter。Skill Adapter 依据 `PowerRef` 从公共 staging 根定位叶子内容并原子发布；MCP Adapter 只通过动态 `PATH` 解析出的目标 Agent CLI 执行 Inspect/Add/Remove，Claude 固定 user scope，Hermes 使用固定 stdin 与明确成功标记，正常写操作后不追加 list 验证。Plugin 不受支持。
    - CLI：提供入口，校验并解析命令参数；Search、List、Install 与 Uninstall 通过四个独立 factory 创建专属端口，并由 CLI 分别渲染结构化结果。
- 数据与接口：每个 Agent 单独维护一份固定 `skills`、`plugins`、`mcps` 分组的 JSON 安装清单，条目统一为精确 `vMAJOR.MINOR.PATCH` 的 `PowerRef`。Config Repository 在 `List/Find/Set/Remove` 内封装完整 manifest 验证、读取和原子读改写；同一 Type/Name 重复合法，Find 取第一条而 Remove 清除全部，非法历史版本不会迁移或清理，详见 [ADR-007](adr/ADR-007-use-stateless-config-repository.md)。用户侧 CLI 语法详见 [ADR-002](adr/ADR-002-cli-agent-and-power-type-as-flags.md)：Search 没有 Agent 维度；默认 List 禁止显式 `--agent`，installed List 必须提供 Agent。本地 Power 操作中，`--agent` 的具体值必须存在于按 [ADR-004](adr/ADR-004-discover-installed-agents-before-local-operations.md) 排序的快照，`all` 对快照中的全部 Agent 执行。Search 在锁内恢复后、锁外查询 Cloud；Cloud List 不锁也不恢复，installed List 在 Recover、Warn、刷新和 Config 读取期间持锁，并在成功 Release 后计算与渲染，见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)。Cloud `list` 返回带版本的全部 `PowerRef`；Search 选择最新版本，List 保留所有版本。

Install 与 Uninstall 分别以独立 factory 和生命周期端口接线，不复用或耦合 Search/List 的锁边界。Install 显式版本必须为规范 `vMAJOR.MINOR.PATCH`，结果行精确为 `<agent>/<type>/<name>@<version> <outcome>`。Uninstall 拒绝版本，从每个 Agent 的本地清单独立解析 Type/Version，并输出完整 PowerRef 或 Agent-only 的结果行。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制；CLI 基于 `cobra`，文件锁基于 `flock`，安装记录为各 Agent 独立的 JSON 清单。开发阶段以 `internal/cloud/resources` 的本地资源模拟云端：Skill 经版本化 Cache 和干净 staging 原子发布；MCP 的 stdio command/args 或 HTTP URL 由 `GetMCP` 直接返回并写入可恢复 agent-cli journal，不进入 Cache。详见 [ADR-001](adr/ADR-001-use-go-as-implementation-language.md)、[ADR-006](adr/ADR-006-use-global-slog-with-level-routing.md) 与 [ADR-010](adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)。

## Test Filesystem Isolation

APM 支持仅供测试使用的 `APM_HOME` 环境变量，使测试能在临时目录中模拟 Agent 安装状态和完整的本地 Power 生命周期，不读写用户目录。该路径决策详见 [ADR-005](adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

### 测试与验收

- 路径单元测试覆盖 `APM_HOME` 未设置、空值回退、非空值优先于 HOME，lock 的隔离路径，以及缓存的 `XDG_CACHE_HOME > APM_HOME > 系统默认` 优先级。
- 修改环境变量的测试不得使用 `t.Parallel()`，避免共享进程环境互相污染。
- 在临时 `APM_HOME` 中创建伪 Agent 根目录，可分别模拟 Agent 已安装和未安装状态。后续生命周期测试在其中执行 Power 安装、卸载、清单读写、锁竞争、事务与恢复。
- MCP 自动化测试还必须把 `PATH` 前置为临时 fake-bin，把三个 Agent Home 和 CLI 状态指向同一临时测试根，并逐次断言实际可执行路径位于 fake-bin；禁止调用用户真实 Codex、Claude Code 或 Hermes CLI。
- 要求所有变化均位于同一临时根的测试，必须清空 `XDG_CACHE_HOME` 以使缓存回退到 `APM_HOME`，或将 `XDG_CACHE_HOME` 也指向该测试根内；验收时必须证明没有读写预期边界之外的文件。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败由 CLI 统一记录。APM 共享状态的恢复与变更由全局锁串行化；Search 的 Cloud 查询和渲染在解锁后执行，Cloud List 不加锁，installed List 仅在成功 Release 后计算和渲染，Install 与 Uninstall 均从 Recover 到每个串行 Transaction 持有各自的单次锁并在成功 Release 后渲染（[ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）。 |
| 安全与合规 | MCP CLI 不经 shell，动态解析 PATH，单次调用固定 30 秒超时且 stdout/stderr 有界；状态输出不匹配时 fail closed 并保留 journal。MCP 定义不包含密钥字段。 |
| 技术与交付 | APM 仅在相关命令中通过 Agent 根目录是否存在来感知安装状态，不承担目标 Agent 的安装、卸载、实时监控或运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置与 Power 格式差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 清单格式和本地文件权限已确定；跨来源内容冲突和更细粒度的权限策略尚未确定。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

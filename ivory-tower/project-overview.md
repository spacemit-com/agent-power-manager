# Project Overview

> 本文档是项目设计的起点。先完成这里的关键信息，再建立 C4 模型、ADR 和 feature 设计。

## Introduction

- 项目名称：Agent Power Manager，简称 APM
- 交付目标：面向多 Agent，统一管理 Skill、Plugin、MCP 等 Power，提供能力发现、分发、安装和配置治理，并通过 Adapter 适配不同目标 Agent 的本地命令行工具。
- 代码工程：`apm`之下，`ivory-tower`仅作为文档归档目录。
- 当前状态：新建。
- 最小业务上下文：用户通过APM从云服务发现所需 Power，APM根据目标Agent将通用Power格式适配并安装到对应目录。

Power配置记录、文件锁等治理功能，不替代目标 Agent 的运行时。

## Scope

- 包含：
    - 提供 `search`、`list`、`install`、`uninstall` 等 CLI 能力。
    - `search <keyword>` 搜索云端资源；`list` 默认列出云端资源，`list --installed` 列出本地安装清单。
    - `install <power>[=<version>]` 未指定版本时选择云端最新版，相同版本幂等成功，不同版本执行升级或显式降级。
    - 生命周期安全，在任意执行阶段中断，系统都可以识别并纠正。
    - 四条命令均在全局锁内先检查唯一活动 journal；提交点前回滚，提交点后完成提交，修复失败则非零退出。
    - `list --installed`、`install`、`uninstall` 在执行本地 Power 操作前扫描已注册 Agent 的根目录，生成当前命令内的“已支持且已安装 Agent”快照。
    - 实现类似apt的文件锁，保证软件执行安全。
    - 缓存下载的Power。
    - 可增添的Agent Power Adapters。
    - 已安装Power记录。
- 不包含：
    - 替代目标 Agent 的运行时，或定义目标 Agent 如何执行已安装的 Skill、Plugin、MCP 配置和 Agent 指令。

## Architecture Snapshot

- 系统边界与外部依赖：Agent Power Manager 位于能力提供方、能力使用方与目标 Agent 之间；对外依赖目标 Agent的Power接入方式。目标 Agent 自身的运行时位于 Agent Power Manager 边界之外。
- 模块边界与职责：
    - 核心生命周期：定义 Power 的发现、安装、卸载、打印、配置读写和版本管理。
    - 配置模型：通过无状态 Repository 维护每个 Agent 按 Skill、Plugin、MCP 分组的 Power 安装清单。
    - Adapter：承接不同 Power 类型和目标 Agent 的格式转换与安装位置差异，并检查已注册 Agent 的根目录是否存在；单个 Power 安装中，Adapter 从公共 staging 原地适配一直负责到目标旁临时复制、原子发布和 staging 清理，详见 [ADR-008](adr/ADR-008-let-adapters-own-staging-to-installation.md)。Adapter 不负责 Agent 安装、实时监控、运行时感知或更新通知；卸载契约另行设计。
    - CLI：提供入口。
- 数据与接口：每个 Agent 单独维护一份固定 `skills`、`plugins`、`mcps` 分组的 JSON 安装清单，条目统一为版本已确定的 `PowerRef`。Config Repository 在 `List/Find/Set/Remove` 内封装读取和原子读改写，lifecycle 不直接管理 JSON Manifest，详见 [ADR-007](adr/ADR-007-use-stateless-config-repository.md)。用户侧通过CLI运行对应功能，CLI 语法详见 [ADR-002](adr/ADR-002-cli-agent-and-power-type-as-flags.md)；生命周期恢复详见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)；Agent 安装检测与命令内快照详见 [ADR-004](adr/ADR-004-discover-installed-agents-before-local-operations.md)。本地 Power 操作中，`--agent` 的具体值必须存在于“已支持且已安装 Agent”快照，`all` 对快照中的全部 Agent 执行。Cloud `list` 返回带版本的全部 `PowerRef`，由 lifecycle 完成列表或搜索筛选；Cloud `get` 将版本已确定的 `PowerRef` 原子写入版本化缓存。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制；CLI 基于 `cobra`，日志在预解析 `--verbose` 后通过分级 Handler 安装为全局 `slog`，文件锁基于 `flock`，安装记录为各 Agent 独立的 JSON 清单。开发阶段以 `internal/cloud/resources` 的本地资源模拟云端服务；安装先将版本已确定的 `PowerRef` 从缓存 fetch 到干净 staging，缓存未命中时执行 Cloud get 后再次 fetch。缓存写入和 staging 物化均通过同文件系统内的临时目录原子 rename，后续安装仍由 staging + journal 保证中断可识别与纠正。详见 [ADR-001](adr/ADR-001-use-go-as-implementation-language.md) 与 [ADR-006](adr/ADR-006-use-global-slog-with-level-routing.md)。

## Test Filesystem Isolation

APM 支持仅供测试使用的 `APM_HOME` 环境变量，使测试能在临时目录中模拟 Agent 安装状态和完整的本地 Power 生命周期，不读写用户目录。该路径决策详见 [ADR-005](adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

### 测试与验收

- 路径单元测试覆盖 `APM_HOME` 未设置、空值回退、非空值优先于 HOME，lock 的隔离路径，以及缓存的 `XDG_CACHE_HOME > APM_HOME > 系统默认` 优先级。
- 修改环境变量的测试不得使用 `t.Parallel()`，避免共享进程环境互相污染。
- 在临时 `APM_HOME` 中创建伪 Agent 根目录，可分别模拟 Agent 已安装和未安装状态。后续生命周期测试在其中执行 Power 安装、卸载、清单读写、锁竞争、事务与恢复。
- 要求所有变化均位于同一临时根的测试，必须清空 `XDG_CACHE_HOME` 以使缓存回退到 `APM_HOME`，或将 `XDG_CACHE_HOME` 也指向该测试根内；验收时必须证明没有读写预期边界之外的文件。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败有相关打印，并且状态能够被识别和修复。APM不支持并发，通过锁机制实现防重入。 |
| 安全与合规 | 暂无。 |
| 技术与交付 | APM 仅在相关命令中通过 Agent 根目录是否存在来感知安装状态，不承担目标 Agent 的安装、卸载、实时监控或运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置与 Power 格式差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 清单格式和本地文件权限已确定；跨来源内容冲突和更细粒度的权限策略尚未确定。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

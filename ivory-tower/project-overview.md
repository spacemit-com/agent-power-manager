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
    - 配置模型：维护各类 Agent 上各类 Power 的安装列表。
    - Adapter：承接不同 Power 类型和目标 Agent 的格式转换、安装位置、注册及配置映射差异。
    - CLI：提供入口。
- 数据与接口：各类Agent单独维护一个Power安装列表，其中按照Power类别分类。用户侧通过CLI运行对应功能，CLI 语法详见 [ADR-002](adr/ADR-002-cli-agent-and-power-type-as-flags.md)；生命周期恢复详见 [ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)。`--agent` 支持 `all` 对所有已注册目标 Agent 执行。应用侧通过`list`获取云端服务Power资源列表、`get`获取特定Power资源。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制；CLI 基于 `cobra`，文件锁基于 `flock`，安装记录为各 Agent 独立的 JSON 清单，安装过程采用 staging 目录 + 原子 rename + journal 实现中断可识别与纠正。详见 [ADR-001](adr/ADR-001-use-go-as-implementation-language.md)。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败有相关打印，并且状态能够被识别和修复。APM不支持并发，通过锁机制实现防重入。 |
| 安全与合规 | 暂无。 |
| 技术与交付 | APM 不承担目标 Agent 的运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置、注册机制和配置能力差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 配置格式、冲突处理和权限控制未确定。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

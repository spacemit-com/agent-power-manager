# Feature Overview

## Goal

- 特性名称：集中式 Lifecycle Transaction
- 要解决的问题：现有 transaction 仅有占位类型，无法保证 Agent 目标目录与安装清单在安装、卸载、中断和收尾失败后的最终一致性；旧 lifecycle 图还把发布动作放在 transaction，与 Adapter 已拥有发布过程的 ADR-008 冲突。
- 可交付结果：实现集中 journal、安装与卸载事务、路径状态判定和跨命令恢复接口；任意 lifecycle 命令后续都可在公共预检中恢复上一次失败，而不需要再次执行相同 Agent 或 Power 的命令。

> **历史 Install 规则已被修订（2026-08-11）：** Feature 010 取代“已纳管 Install target 缺失时直接安装”的自愈规则。当前 Install 只移除对应清单项、清理 canonical staging、返回 `ErrStaleManifest` 并中止，且不创建 journal 或调用 Adapter。Uninstall 的 target 缺失语义不变。

> **Feature 007 的 lifecycle 边界是历史记录：** 本 feature 当时不接线 lifecycle/CLI，并预期四个命令在锁内恢复。该通用预期已被 Feature 008–011 取代：Search 只为 Recover 持锁后查询 Cloud，Cloud List 无锁、installed List 使用自己的锁区间，Install 和 Uninstall 各使用一个完整锁区间并由 CLI 渲染结构化结果。本文的 Uninstall transaction 契约是 Feature 011 接线的历史基础，当前结构化 `TargetMissing` 返回以 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 为准。

## Scope

- 包含：transaction 高层 Manager；集中且原子持久化的唯一活动 journal；同目标文件系统的 backup/quarantine；Install/Uninstall 正常事务；回滚与完成提交；结构化恢复结果；Adapter 显式发布结果；事务路径；错误分类、debug 日志和单元测试。
- 不包含：lifecycle/CLI 实际串联；版本选择；Cloud/Cache 获取；Agent 快照和多 Agent 调度；用户输出；Plugin/MCP Adapter。

## Implementation Context

- 关联模块或服务：`internal/transaction`、`internal/adapter`、`internal/paths`、`internal/config`、`internal/lifecycle` 和 `internal/utils/filesystem`。
- 数据、接口或依赖变化：lifecycle 后续只依赖 transaction Manager；transaction 依赖 Adapter 的目标解析/安装接口和清单写接口；Adapter 安装结果显式报告是否越过发布提交点；Journal 保存恢复所需的新旧引用、阶段和精确路径。
- 关联 C4 文档或 ADR：[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)、[ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

## Acceptance

- 验收条件：正常安装/卸载、提交点前回滚、提交点后完成提交和所有已定义中断窗口均可收敛；恢复只依赖集中 journal，不依赖原命令参数或原 Adapter 实例；非法 journal 或危险路径 fail closed；重要接口具有简要中文用途注释；全量测试、vet、构建和静态检查通过。
- 已知风险或待决问题：外部进程仍可能在原子操作之间修改目标路径；Plugin/MCP 的目标规则和 Adapter 尚未设计；Search、List、Install 和 Uninstall 已分别在 Feature 008–011 中接入各自的恢复/锁/渲染边界。

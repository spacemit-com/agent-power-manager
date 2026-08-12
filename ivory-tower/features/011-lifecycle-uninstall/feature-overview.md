# Feature Overview

## Goal

- 特性名称：Lifecycle Uninstall
- 要解决的问题：`uninstall` 当前只解析参数，尚未把既有 Config、Adapter Registry 与 TransactionManager 串成可执行命令；既有文档也没有完整定义逐 Agent 本地消歧、幂等未安装、目标缺失修复、fail-fast 汇总以及锁外统一渲染的边界。
- 可交付结果：完成 Uninstall 专属 lifecycle 与 CLI 接线；在一次全局锁内恢复、刷新并按 Agent 字典序串行卸载，实时记录未安装与目标缺失 Warn，成功 Release 后返回并渲染结构化 completed/failed/unexecuted 汇总。

## Implementation Status

- 状态：待实现。本 feature 已于 2026-08-12 完成需求澄清和设计确认。

## Scope

- 包含：Uninstall 专属端口、factory、service、结果与 renderer；拒绝版本参数；从各 Agent 本地清单独立解析 Type 和 Version；一次锁内恢复、刷新和串行 Transaction；幂等未安装 Warn；目标缺失结构化事务结果与 Warn；首错停止和完整汇总；默认依赖接线；ADR/C4/概览/构建文档同步。
- 不包含：Search/List/Install lifecycle 重构；通用 lifecycle runner 或共享批处理框架；Cloud、Cache 或版本选择；Adapter `Uninstall` 方法；Plugin/MCP Adapter；并行卸载、失败后继续、非法历史清单迁移或降级；JSON、表格、颜色、进度或交互确认。

## Implementation Context

- 关联模块或服务：`internal/cli`、`internal/lifecycle`、`internal/config`、`internal/adapter`、`internal/transaction`、`internal/lock`、`internal/logging`、`cmd/apm`。
- 数据、接口或依赖变化：CLI 注入 Uninstall 专属 lifecycle factory；Uninstall 返回 Power 可选的逐 Agent 状态；`TransactionManager.Uninstall` 返回结构化 `TargetMissing` 事实并在同时发生错误时保留该事实；默认 lifecycle 的清单读取和 transaction 写入共享 Repository。
- 关联 C4 文档或 ADR：[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)、[ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。`TargetMissing` 返回语义作为重大接口决策修订 ADR-009，并由本 feature 与相关 C4 文档引用。

## Acceptance

- 验收条件：Uninstall 全程只获取一次锁；恢复、未安装和目标缺失 Warn 均在锁内同步输出；Name 区分大小写精确匹配；省略 Type 时按每个 Agent 的清单独立消歧；无记录记为 unexecuted 但命令保持幂等成功；清单存在而目标缺失时修复清单、Warn 并记 completed；任一业务失败立即停止并补齐后续 unexecuted；仅成功 Release 后按精确双形态输出汇总；所有 Error 仍由 CLI 统一记录。
- 已知风险或待决问题：完整卸载事务位于全局锁区间，慢文件系统操作会延长锁持有时间；实时 Warn 在后续 Release 失败时无法撤回；外部进程仍可能在目标检查和原子 rename 之间修改路径，但现有 transaction 会把 rename 竞态缺失纳入幂等提交。

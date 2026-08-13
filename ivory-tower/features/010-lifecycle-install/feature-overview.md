# Feature Overview

> **当前状态说明（2026-08-13）：** 本文保留 Feature 010 的 Skill Install 历史范围。Feature 013 增加 MCP 分支；Feature 014 增加 Plugin 分支，Cloud `GetPlugin` 返回 marketplace 定义，Codex/Claude 不经 Cache，Hermes 条件使用 Cache/Staging；见 [Feature 014](../014-lifecycle-plugin/spec.md) 与 [ADR-012](../../adr/ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md)。

## Goal

- 特性名称：Lifecycle Install
- 要解决的问题：`install` 当前只解析参数，尚未把既有 Cloud、Cache、Adapter、Config 和 TransactionManager 串成可执行命令；旧流程也没有落实锁外统一渲染、规范显式版本、跨类别重名消歧及陈旧清单项修复边界。
- 可交付结果：完成 Install 专属 lifecycle 与 CLI 接线；在一次全局锁内恢复、刷新、解析和串行安装，成功 Release 后返回并渲染结构化结果；支持首次安装、升级、显式降级、同版本幂等和多 Agent fail-fast。

## Implementation Status

- 状态：已于 2026-08-11 实现。实际行为满足本 spec 的已批准要求；当时不在本 feature 范围内的 Uninstall 已由 Feature 011 接线。

## Scope

- 包含：Install 专属端口、factory、service、结果和 renderer；规范 `vMAJOR.MINOR.PATCH` 参数；Cloud 类别推断和最新版选择；Agent 快照；逐 Agent Config/Cache/Cloud/Adapter/Transaction 编排；陈旧清单项修复；Cache Fetch staging-root 契约；精确 stdout、统一错误出口；ADR/C4/概览/构建文档同步。
- 不包含：Uninstall lifecycle；Plugin/MCP Adapter；通用 lifecycle runner；并行或失败后继续；依赖/兼容性求解；真实 HTTP Cloud；非法 Cloud 数据降级；普遍清单迁移或清洗；JSON、表格、颜色或进度输出。

## Implementation Context

- 关联模块或服务：`internal/cli`、`internal/lifecycle`、`internal/version`、`internal/cache`、`internal/cloud`、`internal/adapter`、`internal/config`、`internal/transaction`、`internal/lock`、`cmd/apm`。
- 数据、接口或依赖变化：CLI 注入 Install 专属 lifecycle factory；Install 返回逐 Agent outcome；Cache `Fetch` 直接返回公共 staging root；TransactionManager 在旧清单存在但目标缺失时 Remove 对应清单项并中止；默认 lifecycle 的清单读取和 transaction 写入共享 Repository。
- 关联 C4 文档或 ADR：[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)、[ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

## Acceptance

- 验收条件：Install 全程只获取一次锁；恢复 Warn 在锁内实时输出；版本和类别按规格确定；`all` 按 Agent ID 升序串行并 fail-fast；Cache 为每个实际安装 Agent 重新物化 staging；同版本只信任清单；不同旧版本但目标缺失时仅删除对应清单项、清理 staging 并中止；成功 Release 后精确打印 `<agent>/<type>/<name>@<version> <outcome>`；所有错误仍由 CLI 统一记录。
- 已知风险或待决问题：Cloud/Cache 操作也位于 Install 锁区间，慢 I/O 会延长锁持有时间；外部进程仍可能在目标检查和原子操作之间修改 Agent 目录；当前只有 Skill Adapter 可完成真实安装。

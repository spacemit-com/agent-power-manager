# Feature Overview

## Goal

- 特性名称：Lifecycle List
- 要解决的问题：现有 `list` 只解析参数，聚合 lifecycle 仍以空实现占位；默认 Cloud 列表和 installed 本地列表尚未区分 Agent、恢复与共享状态锁边界，Config 清单也不能保证版本可安全按 SemVer 排序。
- 可交付结果：完成 List 专属 lifecycle 与 CLI 接线；Cloud 模式无锁、无 Recover 地列出全部版本；installed 模式在同一锁内完成 Recover、实时 Warn、Agent 刷新和全部 Config 读取，成功 Release 后才计算并返回结构化结果供 CLI 渲染；同时落实 Agent 排序和规范版本校验。

## Scope

- 包含：List 专属端口、factory 和 service；条件式 `--agent`；Cloud 全版本列表；installed 的恢复、Agent 快照和 Config 读取；精确类型过滤；确定性排序；结构化 partial failure；精确 stdout；统一错误出口；Registry Agent ID 排序；Config `vMAJOR.MINOR.PATCH` 读写校验；聚合 lifecycle 空接口清理；ADR/C4/概览/构建/Changelog 同步。
- 不包含：install/uninstall lifecycle；通用 runner；Cloud 非法数据降级；本地非法清单迁移或清洗；最新版折叠；关键字搜索；JSON、分页或表格输出；真实 HTTP Cloud。Install/Uninstall 未接线是本 feature 的历史边界；Feature 010 已实现 Install，Uninstall 仍为 parse-only。

## Implementation Context

- 关联模块或服务：`internal/cli`、`internal/lifecycle`、`internal/cloud`、`internal/lock`、`internal/transaction`、`internal/adapter`、`internal/config`、`internal/domain`、`cmd/apm`。
- 数据、接口或依赖变化：CLI 注入 List 专属 lifecycle factory；List 返回带可选 Agent 的结构化 Power 记录；只有成功释放锁后的 `--agent=all` 逐 Agent 读取失败可携带可渲染 partial error；Registry 快照和 Config 版本契约收紧。
- 关联 C4 文档或 ADR：[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)、Core Lifecycle/List/Registry/Lock 图。

## Acceptance

- 验收条件：默认 List 不要求 Agent且不访问共享状态；installed List 要求 Agent，在同一锁区间 Recover、实时 Warn、刷新和全部 Config reads，成功 Release 后才过滤排序并渲染；Cloud 输出全部版本为 `<type>/<name>@<version>`，installed 始终输出 `<agent>/<type>/<name>@<version>`；Registry 按 Agent ID 升序；Config 只接受规范 `vMAJOR.MINOR.PATCH` 且不迁移；只有成功 Release 后的 `all` 读取失败可作为 partial 输出，fatal/Release error 不输出业务行；Search 不回归。Install/Uninstall parse-only 是本 feature 的历史边界；Feature 010 已实现 Install，Uninstall 仍为 parse-only。
- 已知风险或待决问题：默认 Cloud List 有意不修复活动 journal；非法旧清单不会自动迁移，升级后需外部修正；开发期 Local Cloud 仍依赖仓库资源目录；List 信任 Cloud 的规范 PowerRef 契约。

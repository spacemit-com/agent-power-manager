# Feature Overview

## Goal

- 特性名称：Lifecycle Search
- 要解决的问题：现有 `search` 只解析参数，lifecycle 仍是占位实现；ADR-003 还要求锁一直持有到结果打印结束，使恢复、共享状态业务和 CLI 展示边界耦合。
- 可交付结果：完成 Search lifecycle 与 CLI 接线，在锁内恢复唯一活动 transaction 并实时记录成功恢复日志，解锁后查询 Cloud、筛选每个 Power 的最新版本，再由 CLI 统一渲染结构化结果。

## Scope

- 包含：Search 专属 lifecycle 端口与依赖注入；锁内 `Recover`；恢复成功 Warn、无 journal Debug；锁外 Cloud `List()`；Unicode 大小写归一化名称搜索；精确类型过滤；每个 `(Type, Name)` 的最大 SemVer 选择；稳定排序；CLI 精确输出；`--agent` 未知 flag；错误、usage 和退出码；ADR、架构图、项目概览与构建文档同步。
- 不包含：list/install/uninstall 的 lifecycle 接线、接口重构或业务实现；通用 lifecycle runner；Cloud 非法数据的校验、跳过或降级排序；真实 HTTP Cloud；搜索描述、标签、内容或版本；模糊匹配和相关性排序。

## Implementation Context

- 关联模块或服务：`internal/cli`、`internal/lifecycle`、`internal/cloud`、`internal/lock`、`internal/transaction`、`internal/domain`、`cmd/apm`。
- 数据、接口或依赖变化：CLI 注入 Search 专属 lifecycle 接口；Search 返回结构化 `SearchResult`，不接收 Renderer；lifecycle 依赖 Locker、TransactionManager 和 CloudClient；CLI renderer 只负责把结果写入 stdout。
- 关联 C4 文档或 ADR：[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)、Core Lifecycle 类图与 Search lifecycle 图。

## Acceptance

- 验收条件：`search` 不接受 `--agent`；恢复严格发生在全局锁内，成功日志先于 Cloud 调用实时写入 stderr，解锁发生在 Cloud 调用前；恢复失败立即返回且 Cloud 不被调用；搜索只匹配规范化后的 Power 名称；每组只输出最大三段 SemVer；结果按 Type、Name 稳定排序并精确打印为 `<type>/<name>@<version>\n`；CLI 对所有错误只记录一条 Error 并打印 usage；全量测试、vet、构建与静态检查通过。
- 已知风险或待决问题：开发期 Local Cloud 仍依赖仓库内资源目录；Search 信任 Cloud 返回规范的类别、名称及 `vMAJOR.MINOR.PATCH`，违反该上游契约的行为不在本 feature 中定义；恢复成功 Warn 为满足实时性而发生在持锁区间。

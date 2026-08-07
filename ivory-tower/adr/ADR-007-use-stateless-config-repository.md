# Architecture Decision Record

- Status：Accepted
- Date：2026-08-07
- Decision Makers：liangjunzhao

## Context

APM 需要为每个 Agent 持久化已安装 Power，并让 lifecycle 在 list、install 和 uninstall 流程中查询或修改清单。现有占位设计向 lifecycle 暴露 `ConfigStore.Read/Write` 和可变 `Manifest`；若 JSONStore 保存当前清单状态，lifecycle 还必须管理先加载、写入后释放、只读释放和跨 Agent 切换，增加与业务编排无关的状态分支。

本决策服务于 [Feature 005](../features/005-agent-power-manifest-config/feature-overview.md)，并细化 [ADR-001](ADR-001-use-go-as-implementation-language.md) 中“每个 Agent 一份 JSON 清单”的技术选择。

## Decision

APM 使用无状态 Config Repository。lifecycle 只调用 `List(agent)`、`Find(agent, type, name)`、`Set(agent, ref)` 和 `Remove(agent, type, name)`；Repository 在每次调用内部完成所需的 JSON 读取，以及 Set/Remove 的单次原子读改写，不暴露 `Read`、`Write`、`Manifest`、加载状态或 `Release`。

每个 Agent 的 JSON 使用固定 `skills`、`plugins`、`mcps` 分组。清单条目统一为版本已确定的 `PowerRef`，磁盘组内不重复保存 type；读取后由 Repository 根据分组补全。系统删除 `InstalledPower` 和 `InstalledAt`，并以共享 `PowerGroups` 表达三类已安装 Power。

Repository 不获取全局锁，也不保证并发写安全。lifecycle 按 ADR-003 在调用本地清单操作时持有全局 APM 锁。Repository 只实施目标状态；版本选择、升级/降级判断、卸载许可、跨类型消歧、筛选和用户输出仍由 lifecycle 负责。

## Consequences

- 正面影响：lifecycle 不需要管理 JSONStore 的隐式状态；只读和多 Agent 流程不需要额外释放；单个 Repository 实例可顺序处理不同 Agent；JSON 格式和原子写入细节完全封装在 config。
- 代价与风险：Find 后 Set、Find 后 Remove 会分别读取文件；Repository 依赖外层全局锁保证读改写串行；固定三分组格式新增 Power 类型时需要扩展 schema；未知字段读取后不会被保留。
- 后续动作：Feature 005 实现 Repository、默认路径、原子写入与测试；后续 lifecycle feature 通过四个业务接口串联清单，不重新引入显式 Read/Write 或 Store 加载状态。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 有状态 JSONStore | 能强制先 Read 再变更，但 lifecycle 必须管理已加载、只读 Release、Write 后清理和跨 Agent 切换，扩大业务编排状态空间。 |
| Read 返回独立 Manifest Editor | 比共享状态安全，但仍把 JSON 文档生命周期暴露给 lifecycle，并增加额外对象层级。 |
| 无状态 Read/Write Repository | 不保存内部状态，但仍要求 lifecycle 读取、持有并回写整个 Manifest，可以绕过 config 的 Set/Remove 约束。 |

# Architecture Decision Record

- Status：Accepted
- Date：2026-08-13
- Decision Makers：APM maintainers

## Context

[Feature 013](../features/013-agent-mcp-adapters/spec.md) 为 Skill 文件系统事务和 MCP Agent CLI 事务引入了带显式 `driver` 与互斥类型载荷的共享 journal 信封。该实现尚处于发布前开发阶段，但最初把旧平铺 Skill journal 编号为 schema v1，把当前共享信封编号为 schema v2，并保留了旧格式的只读解码与归一化路径。

旧平铺格式尚未形成面向用户的发布契约。继续兼容它会让首个正式 schema 从 v2 起步，并长期保留只服务开发期残留文件的解码、转换、测试和文档分支。当前共享信封已经同时表达 `filesystem` 与 `agent-cli` 事务，无需通过旧格式迁移来完成任何正式升级。

本决策局部取代 [ADR-010](ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md) 中“旧 schema v1 作为 legacy filesystem journal 继续可读、新事务写 schema v2”的版本兼容条款。ADR-010 确定的类型专属 driver、共享信封、互斥载荷、唯一活动 journal、phase 与恢复路由继续有效。

## Decision

APM 在开发阶段不兼容旧平铺 journal。当前带显式 driver 和互斥类型载荷的共享信封是唯一正式格式，编号为 schema v1；任意其他格式返回 ErrInvalidJournal 并原样保留。

所有 Skill 与 MCP install/uninstall 事务都写入 `schema_version: 1`。`filesystem` journal 必须且只能包含 filesystem 载荷，`agent-cli` journal 必须且只能包含 agent-cli 载荷；不得根据空字段推断 driver。未知版本、旧平铺字段、未知字段、尾随 JSON、非法 driver、载荷不匹配或非法 phase 均按无效 journal 处理。

读取无效 journal 时，APM 不迁移、覆盖、删除或清理 `transactions/active.json`，当前业务也不得继续。开发者必须检查并人工处理发布前遗留的旧 journal。未来只有在正式信封发生不兼容变更时才引入 schema v2。

## Consequences

- 正面影响：删除旧格式类型、归一化分支和跨版本兼容测试，journal 解码、校验与恢复入口只有一套正式语义。
- 正面影响：首个发布格式从 schema v1 起步，为未来真实的不兼容演进保留清晰的版本编号和迁移边界。
- 代价与风险：仍持有旧平铺开发期 journal 的开发环境会 fail closed，必须由开发者检查现场后人工处理，不能由新版本自动恢复。
- 后续动作：当前态 spec、verification、BUILD、项目概览和 C4 图统一引用本 ADR；Feature 013 的历史实施 plan 保留原文并明确标记相关版本兼容步骤已被取代。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 只保留当前 schema v2 编号 | 首次正式发布从 v2 起步，却不存在已发布的 v1 契约，会永久传递开发期实现历史。 |
| 保留旧平铺 schema v1 兼容 | 增加仅服务开发期残留文件的类型、转换、测试和维护分支，扩大恢复面且不产生用户兼容收益。 |
| 删除版本字段 | 无法在未来正式格式发生不兼容变化时可靠拒绝或迁移，也削弱 fail-closed 校验边界。 |

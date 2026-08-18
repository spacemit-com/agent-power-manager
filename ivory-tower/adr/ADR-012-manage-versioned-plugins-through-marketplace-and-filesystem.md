# Manage Versioned Plugins Through Marketplace and Filesystem

- Status：Superseded by ADR-013
- Date：2026-08-13
- Decision Makers：APM maintainers

> 已由 [ADR-013](ADR-013-generate-per-plugin-local-marketplaces.md) 取代：Cloud 远程稳定全量 marketplace 与 Codex/Claude Plugin 不进入缓存已不符合当前实现；以下正文保留为历史决策上下文。

## Context

APM 已以同一 lifecycle、全局锁、manifest 和唯一 journal 管理 Skill 与 MCP。Plugin 需要保持相同的 `name=version` 用户体验，但 Codex/Claude 原生支持 marketplace，Hermes 只支持本地插件目录及 enable/disable。Cloud 同时托管两套原生 marketplace，APM 还需要一个不会重复解释多套元数据的权威索引。

本决策延续 [ADR-010](ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md) 的类型专属恢复 driver，并遵循 [ADR-011](ADR-011-reset-journal-schema-before-release.md) 的唯一 schema v1。

## Decision

Cloud 维护一个稳定、无版本、包含所有 Plugin 所有版本的 marketplace；实际名采用 `<logical-name>-v<semver>`，用户仍以逻辑 `name=version` 操作。`.claude-plugin/marketplace.json` 是 Cloud 的唯一权威索引；Cloud 返回逻辑 ref、版本化实际名、marketplace 名称和 source，不由 APM 交叉解析 Codex 索引或插件 manifest。

Codex 与 Claude 使用 marketplace deployment：安装前在 transaction 外注册或定向更新 Cloud marketplace，再用 `<actual-name>@<marketplace>` 安装；同名异源 marketplace fail closed。Hermes 使用 filesystem deployment：Plugin 经版本化 Cache/Staging 发布到 `HERMES_HOME/plugins/<actual-name>`，再通过 Hermes CLI enable/disable。APM 不记录 enabled 状态，安装后默认为 enabled。

Plugin 使用独立 `PluginAdapter`、`ResolvePlugin` 和 `plugin` transaction driver。Skill、MCP、Plugin 共用唯一 `transactions/active.json` schema v1 信封，driver payload 严格互斥；任意后续恢复型命令可以串行恢复三类事务。

卸载完全离线：根据 manifest 计算实际名，通过目标 Agent CLI 获取精确 selector，只删除对应 Plugin 清单项；不连接 Cloud、不更新或移除 marketplace、不访问 Cache。marketplace 准备不属于 Plugin transaction，也不回滚。

## Consequences

- 正面影响：三个 Agent 获得一致的 Plugin 搜索、安装、升级、降级、卸载和中断恢复体验，同时保留原生部署方式。
- 正面影响：Cloud 权威索引、Adapter、transaction 与 lifecycle 职责清晰；source 冲突和模糊 selector 均 fail closed。
- 代价与风险：marketplace add/update 位于 Plugin transaction 外，失败后不会自动移除 marketplace；Cloud 管理方必须保证双 marketplace 与插件包规范一致。
- 后续动作：正式 Cloud 实现提供等价的 `GetPlugin`，并负责发布、签名、认证和质量治理。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 每个 Agent 复制一套 Plugin lifecycle | 会重复锁、版本、manifest、批处理与恢复逻辑，并把 Agent 分支扩散到业务层。 |
| 把 Skill、MCP、Plugin 重构为通用 Power Driver | staging、marketplace、selector 与文件隔离没有稳定统一语义，会产生胖接口和可选字段。 |
| 为每个 Plugin 版本建立 marketplace | Agent 需要重复注册来源，无法满足一次注册后长期安装任意版本。 |
| Hermes 使用原生 Git install/remove | 不能提供 APM 所需的版本化本地发布与文件回滚边界。 |

# Architecture Decision Record

- Status：Accepted
- Date：2026-08-12
- Decision Makers：liangjunzhao

## Context

APM 当前通过 [ADR-008](ADR-008-let-adapters-own-staging-to-installation.md) 和 [ADR-009](ADR-009-centralize-lifecycle-transactions.md) 管理 Skill：Adapter 从公共 staging 复制并原子发布目标目录，TransactionManager 以 target、backup/quarantine 和唯一活动 journal 保证 manifest 与文件系统一致。

[Feature 013](../features/013-agent-mcp-adapters/spec.md) 要求复用现有 Search/List/Install/Uninstall 生命周期、Agent 快照、全局锁、版本选择、manifest 和恢复入口，为 Codex、Claude Code 与 Hermes Agent 增加 MCP。MCP 定义由 Cloud 直接返回，实际变更只能通过目标 Agent CLI 执行，不使用 Cache/Staging，也没有可由 `TargetPath` 和原子 rename 表达的文件提交点。

如果把 MCP 强行塞入现有 Skill Adapter，接口会同时出现只适用于文件的 staging/TargetPath 和只适用于 CLI 的 Inspect/Add/Remove；如果 lifecycle 直接执行 Agent CLI，MCP 会绕过集中 journal，进程在升级的 Remove/Add 之间中断时无法由任意后续命令恢复。建立第二套 MCP journal 又会破坏“全局唯一活动事务”和跨类型串行恢复约束。

此外，Codex 可以结构化查询 MCP，但 Claude Code 与 Hermes 首版只能通过严格文本契约确认同名存在性，无法稳定反向提取完整旧定义。恢复所需的新旧定义必须在写操作前从 Cloud 获取并持久化到活动 journal。

## Decision

APM 保留类型专属 Adapter 和执行机制，同时让它们共享同一个生命周期治理框架、TransactionManager、全局锁和唯一活动 journal。

Skill 继续使用现有文件 Adapter，其 `DetectAgent`、`TargetPath`、`Install`、staging、原子 publication 和 transaction backup/quarantine 语义不变。MCP 使用独立 Adapter，通过目标 Agent CLI 提供 `Inspect`、`Add` 和 `Remove`；它不读取 Agent 配置文件，也不访问 Cache、staging、manifest 或 journal。

Agent CLI 变更结果使用 `applied`、`not-applied`、`unknown` 三态。只有明确成功才是 applied；只有能证明无副作用才是 not-applied；超时、异常终止或输出无法解释均为 unknown。Transaction 在每次 Agent CLI 写操作前持久化意图 phase，unknown 时保留 journal，由后续 Recover 通过目标 Agent CLI Inspect 判定提交或回滚方向。

系统仍只允许一个全局活动 journal。新 journal 使用共享信封和显式 driver：`filesystem` 保存 Skill 路径载荷，`agent-cli` 保存 MCP 新旧定义和 CLI phase。新 schema 不通过空字段推断类型；现有 schema v1 明确作为 legacy filesystem journal 继续可读，新事务写入具有显式 driver 的 schema v2。

Recover 先读取唯一 journal，再按 driver 路由到类型专属恢复器。恢复成功并删除 journal 后，当前命令才可继续。因此 MCP 中断可以由后续 Skill 命令恢复，Skill 中断也可以由后续 MCP 命令恢复；系统不建立多个 journal 或恢复队列。

MCP 正常升级/降级在任何 CLI 写操作前，从 Cloud 按旧清单版本和新目标版本获取两个完整定义；卸载获取旧定义。journal 保存恢复所需定义，不保存 CLI 绝对路径或预拼 argv。Recover 根据 Agent ID 从 Registry 重新解析 MCP Adapter，并通过 `PATH` 动态解析目标 CLI。

APM manifest 仍只保存版本已确定的 PowerRef。manifest 有 MCP 记录但 Agent CLI 明确报告同名不存在时，只删除对应 `agent/mcp/name` 条目，不删除 manifest 文件或其他 Power。manifest 无记录但 Agent CLI 报告同名存在时视为清单外冲突，不覆盖或接管。

## Consequences

- 正面影响：现有生命周期、锁、版本、manifest、批处理和恢复入口得到复用；Skill 文件安全边界无需退化；MCP CLI 副作用在中断后可由任意后续共享状态命令恢复；全局仍只有一个活动事务。
- 正面影响：Cloud 定义与 Agent CLI 语法通过 MCP Adapter 解耦；Codex、Claude Code、Hermes 的差异不会进入 lifecycle 或 Config Repository。
- 代价与风险：TransactionManager 和 journal schema 需要支持两种 driver；MCP 没有原子 rename，必须依赖预写 phase、三态 mutation 结果和后续 CLI Inspect。
- 代价与风险：Claude/Hermes 无法稳定反向读取完整定义，恢复只能以同名存在性结合 phase 判断身份；外部进程在 APM 持锁期间修改同名 MCP 不在一致性保证范围内。
- 代价与风险：Agent CLI 调用最长 30 秒且发生在全局锁内，会延长锁持有时间；首版以一致性优先，不并行执行。
- 后续动作：按 Feature 013 实现类型专属 Registry、Cloud GetMCP、MCP Adapter、schema v2 和 MCP 事务；更新 C4、项目概览、BUILD 与相关 feature 文档引用本 ADR。
- 后续动作：未来若 Agent CLI 提供稳定结构化查询或非交互参数，可在 MCP Adapter 内替换实现，不改变 lifecycle 和共享 journal 决策。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 把 Skill 与 MCP 重构成统一胖 Adapter | staging、TargetPath、文件原子发布与 CLI Inspect/Add/Remove 没有共同语义，会制造大量类型分支并扩大现有 Skill 回归面。 |
| 在 lifecycle 中直接调用 MCP CLI，成功后写 manifest | 绕过集中 TransactionManager，无法恢复 Remove/Add、CLI 超时和 manifest 提交之间的中断窗口。 |
| 为 MCP 建立独立 journal 和恢复入口 | 允许 Skill 与 MCP 同时遗留活动事务，破坏全局唯一事务、锁内统一恢复和 fail-closed 边界。 |
| 从 Agent 配置文件读取和修改 MCP | 违反 Adapter 必须通过目标 Agent CLI 的业务要求，并耦合私有配置格式、scope 和迁移规则。 |
| 恢复时从 Cloud 再取旧定义 | 恢复会依赖 Cloud 当前可用性与历史版本保留；写操作前把精确定义持久化到 journal 更可重复。 |
| MCP 不提供中断恢复 | 实现较少，但 Agent 配置和 APM manifest 可能永久不一致，不满足现有生命周期安全承诺。 |

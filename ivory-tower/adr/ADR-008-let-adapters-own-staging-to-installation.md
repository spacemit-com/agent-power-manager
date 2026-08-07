# Architecture Decision Record

- Status：Accepted
- Date：2026-08-07
- Decision Makers：liangjunzhao

## Context

APM 已把版本确定的 Power 从缓存 fetch 到公共无版本 staging，但现有 Adapter 只是占位接口，尚未确定 staging 转换、Agent 路径差异、目标冲突、文件发布和清理分别由谁负责。如果 lifecycle 修改 Agent 格式并拼接目标路径，而另一个通用 installer 负责复制，单个 Agent 的接入规则会分散在多个组件；如果多个 Agent 复用同一份可变 staging，前一个 Adapter 的转换还可能污染后续 Agent。

本决策服务于 [Feature 006](../features/006-agent-skill-adapters/feature-overview.md)，并细化 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 的事务边界与 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md) 的 Agent 检测边界。

## Decision

每个 Agent Adapter 拥有单个 Power 从公共 staging 到目标 Agent 目录的完整安装过程：先防御性检查最终目标冲突，再原地适配 staging，复制到目标旁临时目录，原子 rename 发布，并在成功或失败后清理临时目录和整个公共 staging。

Adapter 暴露只读的目标路径解析能力，使 lifecycle/transaction 可以在调用安装前记录路径，并对已纳管旧目标执行备份或恢复；lifecycle 和 transaction 不重复 Agent 路径规则。Adapter 不读取安装清单，不判断版本或安装许可，也不管理 journal、backup、quarantine 或清单写入。流程到达 Adapter 时最终目标应为空，任何残留文件系统条目都作为清单外冲突处理。

公共 staging 是单次 Agent 安装的独占可变工作区。多 Agent 流程必须串行，并在每个 Agent 调用 Adapter 前重新 fetch staging；不允许多个 Adapter 并行修改或复用同一次 fetch 的内容。

公共 staging 和 Agent Skill 根在物理文件系统中必须不重叠。Adapter 在注册 staging 递归清理或创建目标临时目录之前，解析已经存在的 staging、Agent 根和 Skill 根 symlink；Skill 根缺失时从已解析的 Agent 根推导其物理位置，再用路径分隔符感知的包含判断拒绝“Skill 根等于 staging”或“Skill 根位于 staging 下”的布局。此类重叠返回安装错误，且不清理 staging、不复制、不发布，因为任何递归清理或目标临时目录操作都可能删除输入与目标，或让递归复制进入自身输出。通过该检查后的有效 staging 则继续遵守所有后续成功和失败出口必清理的契约。

首版仅覆盖 Codex、Claude Code 和 Hermes Agent 的全局 Skill。三者共用安装管线，通过 Agent 配置和内部适配 hook 表达差异；首版 hook 均为 no-op。

## Consequences

- 正面影响：Agent 格式与路径差异集中在 Adapter；冲突防护、原子发布和清理只有一份共享实现；transaction 可通过 Adapter 获取目标而不复制路径规则；未来格式转换不会污染其他 Agent 的 staging。
- 代价与风险：多 Agent 安装必须为每个 Agent 重新 fetch，不能并行复用 staging；物理根重叠时会故意保留 staging 供调用方诊断或安全处置；发布成功但 staging 清理失败会形成需 journal 判定的中间结果；Adapter 的单次安装职责比只返回路径的薄 Adapter 更重；外部进程仍可能在物理根检查后改变 symlink，或在最终目标检查与 rename 之间竞争。
- 后续动作：Feature 006 实现三种 Skill Adapter 与 Registry；后续 lifecycle/transaction feature 按“先处理已纳管旧目标，再调用 Adapter”的契约串联，并为发布后清理失败提供恢复路径；Plugin/MCP 和卸载分别设计其 Adapter 契约。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 三个完全独立的 Adapter 安装实现 | 三者首版行为相同，会复制冲突检查、复制、原子发布和清理等安全关键逻辑，后续修复容易不一致。 |
| Adapter 只描述路径和转换，独立 Installer 负责发布 | 将单个 Agent 的接入过程拆散到多个组件，不符合 Adapter 从 staging 适配到目标安装的职责边界。 |
| 多个 Agent 共享同一次 fetch 的 staging | staging 会被 Adapter 原地修改且安装后清理，复用会产生顺序污染，并使并行执行不安全。 |

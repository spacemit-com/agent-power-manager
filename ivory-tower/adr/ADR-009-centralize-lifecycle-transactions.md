# Architecture Decision Record

- Status：Accepted
- Date：2026-08-10
- Decision Makers：liangjunzhao

## Context

[ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 已确定 lifecycle 在选择执行共享状态恢复阶段时恢复唯一活动 journal，但 transaction 仍是占位实现，lifecycle 直接依赖 JournalStore，正常流程与恢复顺序容易散落在两层。[ADR-008](ADR-008-let-adapters-own-staging-to-installation.md) 随后把 staging 适配和原子发布交给 Adapter，使旧设计中“transaction 将 staging rename 到 target”的提交点描述不再符合真实边界。

恢复必须能够由任意选择执行共享状态恢复阶段的 lifecycle 触发，而不是要求用户再次执行相同 Agent 或 Power 的命令。无论当前 lifecycle 的参数为何，Manager 都必须能够修复任意先前 install/uninstall 留下的事务。与此同时，Agent 目标与 APM state 可能位于不同文件系统，把 backup/quarantine 全部集中到 state 会使目标移动无法使用原子 rename。

本决策服务于 [Feature 007](../features/007-central-lifecycle-transactions/feature-overview.md) 与 [Feature 008](../features/008-lifecycle-search/feature-overview.md)，并细化 ADR-003、[ADR-007](ADR-007-use-stateless-config-repository.md) 和 ADR-008。

## Decision

APM 使用 transaction 高层 Manager 封装 `Recover`、`Install` 和 `Uninstall`。每个 lifecycle 自行定义是否以及何时执行共享状态恢复阶段；选择该阶段时负责持有全局锁并调用 Manager。除该边界外，lifecycle 负责业务预检、版本与 Adapter 解析、Cloud/Cache 和多 Agent 调度；它不直接读写 journal、backup 或 quarantine。transaction 在提交点后调用 Config Repository 的 `Set/Remove`，使清单修改、事务清理和 journal 删除保持固定顺序。

唯一活动 journal 集中存放在 APM state 的 `transactions/active.json`。它记录 transaction ID、operation、phase、Agent、新旧 PowerRef、staging 和全部实际事务路径。需要恢复的 lifecycle 在其共享状态锁边界内调用 `Recover`，即可修复上一次事务，无需原命令参数、原 Adapter 实例或相同目标。

[Feature 008](../features/008-lifecycle-search/feature-overview.md) 已将 Search 接入 Manager：Search 只在锁内执行恢复并记录同步 Warn，释放锁后才查询 Cloud 并把结构化结果交给 CLI 渲染，符合修订后的 ADR-003。list/install/uninstall 仍是未接线的命令解析入口；它们未来各自接入 Manager，并选择与自身共享状态业务相符的边界。

backup/quarantine 存放在 target 同一父目录的 `.apm-transactions/<transaction-id>`，集中 journal 记录其绝对路径。该布局同时满足集中发现和同文件系统原子 rename；不使用跨文件系统 copy-delete 代替提交点。

Adapter 的 Install 显式返回是否已经发布。目标 rename 成功后，即使 staging 清理失败也报告 `Published=true`。当前调用依此固定提交方向；进程在 Adapter 返回前中断时，恢复器再结合 journal phase 和 target 状态覆盖提交点窗口。

本 ADR 以“Adapter 在目标同目录创建临时内容并 rename 到 target”取代 ADR-003 中“transaction 将 staging rename 到 target”的旧实现描述；原子 target publication 仍是 Install 提交点。transaction 在写入 `publishing` 前确认 target 为空，Adapter 发布前再次防御性检查，从而使 APM 自身控制的中断窗口可由 target 状态判定；外部进程竞态仍不在保证范围内。

Install 使用 `preparing`、`publishing`、`rolling-back` 和 `committing`；Uninstall 使用 `removing`、`rolling-back` 和 `committing`。回滚前必须先持久化 `rolling-back`，避免恢复后的旧 target 被误判为新发布内容。提交点前失败在当前调用中回滚；提交点后失败保留 journal，由下一条命令完成提交。

清单有记录而 target 已被用户删除时采用自愈：Install 直接安装新版本，Uninstall 完成清单删除。清单无记录但 target 存在仍是清单外冲突，不覆盖或删除。

Journal 和所有待删除路径在使用前严格校验。损坏、未知版本、越界或不可判定状态 fail closed：保留 journal 和现场，不执行用户命令。

## Consequences

- 正面影响：恢复入口集中且与当前命令解耦；正常事务和恢复共享提交/回滚规则；lifecycle 无法绕过 journal 顺序；Adapter 发布边界明确；目标内容仍使用同盘原子移动。
- 代价与风险：集中 journal 仍需记录目标文件系统上的分散事务内容；transaction 依赖清单写接口与 Adapter 最小接口；外部进程仍可能在检查与原子操作之间修改路径；其余命令的 Manager 接入仍需各自定义边界。
- 后续动作：Feature 007 实现 Manager、Journal Store、路径状态和 Adapter 发布结果；Feature 008 已完成 Search 接入与用户恢复日志；list/install/uninstall 在后续 feature 中分别接入 Manager。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| lifecycle 调用细粒度 transaction 原语 | 阶段更新、提交点和 journal 删除顺序暴露给编排层，容易形成不可恢复组合。 |
| transaction 接管版本、Cloud、Cache 和多 Agent 用例 | 依赖面过大，吞并 lifecycle 的业务职责。 |
| backup/quarantine 全部放在集中 state | Agent 目标可能位于另一文件系统，无法用原子 rename 建立提交点。 |
| 只在目标旁保存 journal | 任意选择共享状态恢复阶段的 lifecycle 无法从统一入口发现活动事务。 |
| 仅检查 target 推断当前 Adapter 调用结果 | 清单外冲突和发布后清理失败可能具有相同路径表象，当前进程可以由 Adapter 显式报告。 |

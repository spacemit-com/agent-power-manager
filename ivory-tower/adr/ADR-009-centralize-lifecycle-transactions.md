# Architecture Decision Record

- Status：Accepted
- Date：2026-08-10
- Revised：2026-08-14
- Decision Makers：liangjunzhao

## Context

[ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 已确定 lifecycle 在选择执行共享状态恢复阶段时恢复唯一活动 journal，但 transaction 仍是占位实现，lifecycle 直接依赖 JournalStore，正常流程与恢复顺序容易散落在两层。[ADR-008](ADR-008-let-adapters-own-staging-to-installation.md) 随后把 staging 适配和原子发布交给 Adapter，使旧设计中“transaction 将 staging rename 到 target”的提交点描述不再符合真实边界。

恢复必须能够由任意选择执行共享状态恢复阶段的 lifecycle 触发，而不是要求用户再次执行相同 Agent 或 Power 的命令。无论当前 lifecycle 的参数为何，Manager 都必须能够修复任意先前 install/uninstall 留下的事务。与此同时，Agent 目标与 APM state 可能位于不同文件系统，把 backup/quarantine 全部集中到 state 会使目标移动无法使用原子 rename。

本决策服务于 [Feature 007](../features/007-central-lifecycle-transactions/feature-overview.md) 与 [Feature 008](../features/008-lifecycle-search/feature-overview.md)，并细化 ADR-003、[ADR-007](ADR-007-use-stateless-config-repository.md) 和 ADR-008。

## Decision

APM 使用 transaction 高层 Manager 封装相互分离的 `Inspect`、`Recover`、`Install*` 和 `Uninstall*`。`Inspect` 只读取并校验唯一活动 journal，返回不含 driver payload 和路径的结构化观察结果；它不执行恢复或其他写入。`Recover` 复用同一个私有“一次读取、统一校验”原语，并在同一次读取后继续 driver 路由和收敛，不先调用公开 Inspect，也不重复读取 journal。

每个 lifecycle 自行选择非变更观察或锁内恢复：Search 与两种 List 只依赖 Inspector 并无锁 Inspect；Fix 只依赖 Recoverer，在锁内 Recover 恰好一次后退出；Install/Uninstall 依赖完整 Manager，在各自共享状态锁边界内 Recover 后继续变更业务。除该边界外，lifecycle 负责业务预检、版本与 Adapter 解析、Cloud/Cache 和多 Agent 调度；它不直接读写 journal、backup 或 quarantine。transaction 在提交点后调用 Config Repository 的 `Set/Remove`，同时允许 Install 预检在发现陈旧清单时调用单条 `Remove`；该修复不创建 journal。

唯一活动 journal 集中存放在 APM state 的 `transactions/active.json`。它记录 transaction ID、operation、phase、Agent、新旧 PowerRef、staging 和全部实际事务路径。需要恢复的 Fix/Install/Uninstall 在其共享状态锁边界内调用 `Recover`，即可修复上一次事务，无需原命令参数、原 Adapter 实例或相同目标。Search/List 只观察是否需要恢复，并提示用户运行 `apm fix`。

[Feature 016](../features/016-lifecycle-fix/spec.md) 已修订 Feature 008/009 的恢复边界：Search、默认 List 和 installed List 都在原只读业务前无锁 Inspect，合法活动 journal Warn 并提示 `apm fix`，损坏 journal Warn 后继续；三者均不加锁、不 Recover。Fix 在一次锁区间内 Recover 恰好一次后退出。Feature 010 的 Install 与 [Feature 011](../features/011-lifecycle-uninstall/feature-overview.md) 的 Uninstall 保持原边界：各自在一个锁区间内恢复、刷新和执行串行 Transaction，并只在成功 Release 后交给 CLI 渲染。

backup/quarantine 存放在 target 同一父目录的 `.apm-transactions/<transaction-id>`，集中 journal 记录其绝对路径。该布局同时满足集中发现和同文件系统原子 rename；不使用跨文件系统 copy-delete 代替提交点。

Adapter 的 Install 显式返回是否已经发布。目标 rename 成功后，即使 staging 清理失败也报告 `Published=true`。当前调用依此固定提交方向；进程在 Adapter 返回前中断时，恢复器再结合 journal phase 和 target 状态覆盖提交点窗口。

本 ADR 以“Adapter 在目标同目录创建临时内容并 rename 到 target”取代 ADR-003 中“transaction 将 staging rename 到 target”的旧实现描述；原子 target publication 仍是 Install 提交点。transaction 在写入 `publishing` 前确认 target 为空，Adapter 发布前再次防御性检查，从而使 APM 自身控制的中断窗口可由 target 状态判定；外部进程竞态仍不在保证范围内。

Install 使用 `preparing`、`publishing`、`rolling-back` 和 `committing`；Uninstall 使用 `removing`、`rolling-back` 和 `committing`。回滚前必须先持久化 `rolling-back`，避免恢复后的旧 target 被误判为新发布内容。提交点前失败在当前调用中回滚；提交点后失败保留 journal，由下一条命令完成提交。

`Manager.Uninstall` 返回 `UninstallResult{TargetMissing bool}` 和 error。`TargetMissing=true` 表示 transaction 已确认 target 在初始检查时不存在，或者 target 初查存在但 quarantine rename 失败后复查确认已被外部删除。一旦该事实为 true，后续持久化 `committing`、清单 `Remove`、quarantine/事务目录清理或 journal 删除错误都必须在与 error 同时返回时保留 `TargetMissing=true`；在确认 target 状态前失败则为 false。

Transaction 不记录 Warn。Uninstall lifecycle 在 `Manager.Uninstall` 返回后立即消费该结构化事实：只要 `TargetMissing=true`，即使同时返回 error，也必须在仍持有全局锁时同步记录完整 PowerRef Warn，再将该 Agent 分类为 `completed` 或 `failed`。

Uninstall lifecycle 按 [ADR-007](ADR-007-use-stateless-config-repository.md) 组合清单契约：同一类别内存在重复 Type/Name 时选择清单第一条，只有跨类别匹配才歧义；成功 transaction 的 `Remove(agent,type,name)` 清除该 Type/Name 的全部重复记录。

Install 的清单有记录而 target 已被用户删除时，Transaction 在生成 transaction ID、创建 journal 或调用 Adapter 前执行 `ManifestWriter.Remove(agent,type,name)`，安全清理 canonical staging，返回 `ErrStaleManifest` 并中止本次命令；它不会自动安装新版本。Uninstall 的 target 缺失语义保持既有的完成清单删除。清单无记录但 target 存在仍是清单外冲突，不覆盖或删除。

Journal 和所有待删除路径在使用前严格校验。损坏、未知版本、越界或不可判定状态始终保留 journal 和现场。只读 Inspect 的 lifecycle 记录 damaged Warn 后继续原查询，因为它不会改变共享状态；Fix/Install/Uninstall 的 Recover 则 fail closed，不执行后续修复或写命令业务。

## Consequences

- 正面影响：观察与恢复入口集中但职责分离；只读命令不再隐式写状态；正常事务和恢复共享提交/回滚规则；lifecycle 无法绕过写命令的 journal 顺序；Adapter 发布边界明确；目标内容仍使用同盘原子移动。
- 代价与风险：集中 journal 仍需记录目标文件系统上的分散事务内容；无锁 Inspect 只能提供 best-effort 观察，可能漏掉或过期，installed List 也可能混合跨 Agent manifest 及 target/manifest 窗口；外部进程仍可能在检查与原子操作之间修改路径；Install 与 Uninstall 的完整单锁边界可能延长锁持有时间。
- 后续动作：Feature 007 已实现 Manager、Journal Store、路径状态和 Adapter 发布结果；Feature 016 已分离 Inspect/Recover 并增加显式 Fix；Feature 010/011 的 Install/Uninstall 恢复与事务边界保持不变。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| lifecycle 调用细粒度 transaction 原语 | 阶段更新、提交点和 journal 删除顺序暴露给编排层，容易形成不可恢复组合。 |
| transaction 接管版本、Cloud、Cache 和多 Agent 用例 | 依赖面过大，吞并 lifecycle 的业务职责。 |
| backup/quarantine 全部放在集中 state | Agent 目标可能位于另一文件系统，无法用原子 rename 建立提交点。 |
| 只在目标旁保存 journal | 任意选择共享状态恢复阶段的 lifecycle 无法从统一入口发现活动事务。 |
| 仅检查 target 推断当前 Adapter 调用结果 | 清单外冲突和发布后清理失败可能具有相同路径表象，当前进程可以由 Adapter 显式报告。 |

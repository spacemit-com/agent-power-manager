# Architecture Decision Record

- Status：Accepted
- Date：2026-08-04
- Revised：2026-08-14
- Decision Makers：liangjunzhao

## Context

APM 要求改变共享状态的命令能够识别并纠正上一次 install/uninstall 中断留下的不一致状态。锁必须覆盖恢复和实际共享状态业务，但只读查询不应为了观察 journal 而隐式恢复或阻塞写命令。此前 Search 和 installed List 会获取全局锁并执行 `Recover`，默认 Cloud List 则不检查 journal；三种查询的副作用和用户提示不一致，也缺少一条只执行恢复的显式命令。

本 ADR 补充 [ADR-001](ADR-001-use-go-as-implementation-language.md) 的 staging、原子 rename 与 journal 技术选择，并约束 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 的多 Agent 执行语义；多 Agent 的目标集合由 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md) 定义的命令内快照确定。Transaction 的只读检查与恢复边界见 [ADR-009](ADR-009-centralize-lifecycle-transactions.md)。

## Decision

APM 将“观察恢复需求”与“执行恢复”分离。Search、默认 List 和 installed List 只做无锁检查；Fix、Install 和 Uninstall 才在全局锁内执行恢复。lifecycle 返回结构化结果，CLI 在 lifecycle 返回后渲染；全局锁不覆盖 CLI 输出。

三种只读命令的共同入口固定为：

```text
Search / default List / installed List:
Inspect without lock → Warn/Debug → continue original read-only business.
```

`Inspect` 只读取并严格校验唯一 `transactions/active.json`，不写 journal、不推断恢复方向，也不访问 manifest、Adapter 或目标路径：

- 无活动 journal 时可在 verbose 模式记录 Debug，然后继续原查询。
- 合法活动 journal 时同步记录 Warn，包含原操作、Agent、Power、版本和阶段，并明确提示 `run apm fix`；警告后继续原查询。
- journal 损坏、不可读或校验失败时同步记录 damaged Warn，但不提示 Fix，因为 Fix 同样不能绕过严格校验；警告后继续原查询。
- Inspect error 不成为 Search/List 的业务 error，不改变原查询结果、partial marker 或退出状态。一条只读命令至多记录一条 journal 检查 Warn。

Search 在 Inspect 后查询 Cloud、筛选并返回结构化结果。默认 List 在 Inspect 后查询 Cloud。installed List 在 Inspect 后无锁调用 `RefreshInstalled`，再按 Agent 顺序读取 Config；具体 Agent 的刷新、校验或读取错误仍是 fatal error，省略 `--agent` 或 `--agent=all` 仍遍历全部受支持 Agent、区分未安装与空清单，并收集逐 Agent 失败返回成功记录与 `PartialListError`。三者都不获取全局锁、不调用 `Recover`，也没有 Release gate。

显式 Fix 的锁边界固定为：

```text
Fix: Acquire → Recover exactly once → lock-held Warn/Debug → Release → exit.
```

Fix 不接受 Agent、Power、Type、Version 或 transaction ID，也不重试原命令。Acquire 失败时不调用 Recover；Recover 失败时保留 journal，仍尝试 Release；Release 失败与 Recover 失败同时发生时使用 `errors.Join` 保留两个 cause。恢复成功 Warn 或无 journal Debug 在锁内同步记录，随后 Release；恢复成功但 Release 失败时，已经记录的恢复事实不撤回。一次 Fix 调用恰好执行一次 Recover，不先 Inspect，也不循环处理多个 journal。

Install 的锁边界保持为：

```text
Install: Acquire → Recover → lock-held Warn/Debug → RefreshInstalled → target validation
         → Cloud List/version resolution → serial Config/Cache/Cloud/Transaction work
         → successful Release → CLI render.
```

Uninstall 的锁边界保持为：

```text
Uninstall: Acquire → Recover → lock-held Warn/Debug → RefreshInstalled → target validation
           → serial Config List/local resolution/Adapter Resolve/Transaction Uninstall
           → successful Release → CLI render.
```

Install 与 Uninstall 从 Acquire 到 Release 各只使用一个锁区间。Recover 失败时保留 journal、释放锁后非零退出且不执行原命令业务。两条写命令仍以成功恢复并删除旧 journal 作为创建新事务的前置条件，不先 Inspect 再 Recover，避免双读窗口。

Uninstall 恢复成功、清单无记录、Transaction 确认 target 缺失的 Warn 都由 lifecycle 在当前调用栈中、仍持锁时同步记录。清单无记录记为 `unexecuted` 但不算失败；Config 读取、类别消歧、Adapter Resolve 或 Transaction 的首个错误立即把当前 Agent 记为 `failed`、剩余 Agent 记为 `unexecuted`，且不再访问后续 Agent 的任何依赖。

Install 和 Uninstall 的 batch marker 只能在 Release 成功后构造，结果也只在此后交给 CLI 渲染。Release 失败时返回零结果并抑制所有业务行；若同时存在业务错误，使用 `errors.Join` 保留全部 cause。先前已同步写出的 Warn 不撤回。`--agent=all` 按当前快照中的 Agent ID 升序串行且 fail-fast：任一 Agent 业务错误后立即停止，成功 Release 后再统一渲染 `failed` 与剩余 `unexecuted`。

正常受支持状态至多存在一个未完成 journal。install 在原子 staging-to-target publication 前记录 publishing 意图，该 publication 是提交点；uninstall 在原子 target-to-quarantine move 前记录 removing 意图，该 move 是提交点。Recover 结合 journal 阶段和 staging、target、backup/quarantine 状态覆盖“原子操作完成但阶段尚未更新”的中断窗口。修复成功后必须先删除已恢复 journal，再返回结构化恢复结果；删除或其他恢复步骤失败均保留 journal 并 fail closed。

> [ADR-009](ADR-009-centralize-lifecycle-transactions.md) 修订了 Install 的实现边界：Adapter 把 staging 复制到目标同目录临时目录，并以临时目录到 target 的原子 rename 作为同一提交点；transaction 在调用 Adapter 前持久化 publishing，并通过显式 `Published` 结果或中断后的 target 状态固定恢复方向。

只读命令提供的是无锁、尽力而为的观察，而不是全局一致快照：

- Inspect 后可能立即创建 journal，因此查询可能漏掉刚开始的事务警告。
- Inspect 看到活动 journal 后，Fix 或写命令可能立即完成恢复，因此警告输出时可能已经过期。
- `list --installed --agent=all` 逐个读取 Agent manifest，结果可能混合不同时间点的完整 manifest。
- transaction 的 target、Agent CLI 状态与 manifest 分步提交，installed List 可能观察到 target/manifest 暂时不匹配的窗口。
- `RefreshInstalled` 逐项检测期间，Agent 根目录可能被外部创建或删除，快照只代表各检测时刻。

原子文件替换可保证单个正常写入的 journal 或 manifest 读取不会看到半截 JSON，但不能提供跨 journal、多个 Agent manifest、target 与 Agent CLI 状态的一致时间点。要提供这种全局一致性，必须让读取持有覆盖全部资源的锁，或引入版本化快照/MVCC；这会增加锁竞争、存储和协议复杂度，本决策有意不承诺。

恢复日志至少包含原操作、Agent、Power、版本、原阶段、回滚或完成提交、修复内容和结果。只读检查合法 journal 的 Warn 只报告恢复需求和 `apm fix` 提示；损坏 journal 的 Warn 报告完整 error chain，但不声称可自动修复。

## Consequences

- 正面影响：只读查询不再隐式修改本地状态或阻塞写命令；三种查询以相同方式提示恢复需求；用户可通过 `apm fix` 显式收敛唯一活动事务；Install/Uninstall 仍保持原有串行恢复和事务安全。
- 代价与风险：只读结果与警告是 best-effort，可能出现漏警告、过期警告、跨 Agent 混合 manifest 和 target/manifest 暂时不匹配；损坏 journal 只能告警，Fix 与写命令会 fail closed，不能自动删除或猜测修复。
- 后续动作：在 Core Lifecycle、journal inspection/fix/recovery 流程图、C4 properties、Feature 007/008/009 supersession note、项目概览和构建指南中引用并表达本决策。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Search/List 继续在锁内 Recover | 只读查询会产生隐式写入和额外锁竞争，也让默认 List 与 installed List 的副作用不一致；显式 Fix 更清楚。 |
| 只读 Inspect 获取全局锁 | 单次 journal 读取不需要串行化；持锁也不能让随后锁外的 Cloud、Agent discovery 和多 manifest 读取成为同一快照。 |
| 损坏 journal 时阻止所有只读命令 | Search/List 不修改共享状态，阻断查询不能让损坏 journal 更安全；应 Warn 后保留其原有业务语义。 |
| Fix 删除、忽略或强制解释损坏 journal | 无法安全判断提交方向或路径所有权，可能破坏目标与 manifest；继续严格校验并 fail closed。 |
| 为只读命令承诺全局一致快照 | 需要覆盖 journal、Agent、manifest、target 与 CLI 状态的锁或 MVCC，复杂度与查询阻塞成本超出当前需求。 |
| transactional `install/uninstall --agent=all` 单 Agent 失败后继续 | 未解决 journal 存在时无法安全创建下一事务，破坏单 journal 不变量；只读 installed List 可继续收集 Config 读取失败。 |
| 所有恢复阶段一律回滚或一律继续 | 无法同时保证原子提交点前恢复旧状态和提交点后补全一致性。 |

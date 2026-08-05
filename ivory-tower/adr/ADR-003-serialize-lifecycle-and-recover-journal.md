# Architecture Decision Record

- Status：Accepted
- Date：2026-08-04
- Decision Makers：liangjunzhao

## Context

APM 要求任意命令启动时都能识别并纠正上一次 install/uninstall 中断留下的不一致状态。只让写命令持锁会使 search/list 在 journal 修复期间产生并发写入风险；`--agent=all` 在错误后继续也会与“至多一个未完成 journal”冲突。

本 ADR 补充 [ADR-001](ADR-001-use-go-as-implementation-language.md) 的 staging、原子 rename 与 journal 技术选择，并约束 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 的多 Agent 执行语义。

## Decision

四条生命周期命令 search/list/install/uninstall 在入口获取同一把全局 APM 锁，并持有到 journal 预检、用户命令和结果输出全部结束。

正常受支持状态至多存在一个未完成 journal。每条命令在执行用户请求前检查该 journal：提交点前执行回滚，提交点后完成提交；修复成功后必须先删除已恢复 journal，再打印修复方向与内容并继续原命令。删除失败视为恢复失败：保留 journal、打印删除错误并非零退出，不执行用户原命令。其他修复失败同样保留 journal、打印错误并非零退出。

install 在原子 staging-to-target rename 前记录 publishing 意图，该 rename 是提交点。uninstall 在原子 target-to-quarantine move 前记录 removing 意图，该 move 是提交点。恢复时结合 journal 阶段和 staging、target、backup/quarantine 路径状态覆盖“原子操作完成但阶段尚未更新”的中断窗口。

install/uninstall 的 `--agent=all` 按已支持 Agent ID 升序串行且 fail-fast：任一 Agent 错误后立即把当前 Agent 记为 failed、按相同顺序把全部剩余 Agent 记为 unexecuted，停止执行并非零退出；命令结束前统一打印 completed、failed 和 unexecuted 汇总。`list --installed --agent=all` 是只读汇总，单个 Agent 读取失败不阻止读取其他 Agent，但最终整体非零退出。

install/uninstall 在即时回滚、提交补全或 journal 清理期间再次发生进程中断时，不尝试在中断路径继续修复；内核释放全局锁，现有 journal 保持可恢复，交由下一条任意命令的公共预检重新判断并恢复。

恢复日志至少包含原操作、Agent、Power、版本、原阶段、回滚或完成提交、修复内容和结果。

## Consequences

- 正面影响：所有命令看到一致的本地状态；中断恢复方向确定；只有成功删除已恢复 journal 后才执行新命令，不会在错误 journal 未解决时创建第二个事务。
- 代价与风险：search/list 也被全局锁串行化；`--agent=all` 的 install/uninstall 可能产生“部分完成、其余未执行”的结果。
- 后续动作：在 Core Lifecycle 的四张命令流程图、C4 properties、类图和项目概览中引用并表达本决策。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 仅 install/uninstall 持锁 | search/list 的启动修复会写本地状态，不能在无锁状态安全执行。 |
| 仅在修复时临时加锁 | 锁范围分裂，命令状态可能在预检后被另一进程改变。 |
| `--agent=all` 单 Agent 失败后继续 | 未解决 journal 存在时无法安全创建下一事务，破坏单 journal 不变量。 |
| 所有阶段一律回滚或一律继续 | 无法同时保证原子提交点前恢复旧状态和提交点后补全一致性。 |

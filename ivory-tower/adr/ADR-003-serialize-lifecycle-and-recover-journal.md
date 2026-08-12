# Architecture Decision Record

- Status：Accepted
- Date：2026-08-04
- Revised：2026-08-12
- Decision Makers：liangjunzhao

## Context

APM 要求需要访问 APM 共享状态的命令能够识别并纠正上一次 install/uninstall 中断留下的不一致状态。锁必须覆盖恢复和实际共享状态业务，但不应把 Cloud 查询、结果计算或 CLI 输出无条件串行化；`--agent=all` 在错误后继续也会与“至多一个未完成 journal”冲突。

本 ADR 补充 [ADR-001](ADR-001-use-go-as-implementation-language.md) 的 staging、原子 rename 与 journal 技术选择，并约束 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 的多 Agent 执行语义；多 Agent 的目标集合由 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md) 定义的命令内快照确定。

## Decision

全局 APM 锁只为 journal 恢复或其他访问 APM 共享状态的业务获取。lifecycle 返回结构化结果，CLI 在锁外渲染；没有共享状态业务的生命周期不获取该锁。每个命令分别选择自身、不与其他命令耦合的锁边界。

List 的锁边界固定为：

```text
Cloud List: no lock, no Recover, Cloud query → result computation → CLI render.
Installed List: Acquire → Recover → lock-held Warn → RefreshInstalled → all Config reads
                → successful Release → result computation → CLI render.
```

Install 的锁边界固定为：

```text
Install: Acquire → Recover → lock-held Warn → RefreshInstalled → target validation
         → Cloud List/version resolution → serial Config/Cache/Cloud/Transaction work
         → successful Release → CLI render.
```

Uninstall 的锁边界固定为：

```text
Uninstall: Acquire → Recover/lock-held Warn → RefreshInstalled → target validation
           → serial Config List/local resolution/Adapter Resolve/Transaction Uninstall
           → successful Release → CLI render.
```

Uninstall 从 Acquire 到 Release 只使用一个锁区间。恢复成功、清单无记录、Transaction 确认 target 缺失的 Warn 都由 lifecycle 在当前调用栈中、仍持锁时同步记录。清单无记录记为 `unexecuted` 但不算失败；Config 读取、类别消歧、Adapter Resolve 或 Transaction 的首个错误立即把当前 Agent 记为 `failed`、剩余 Agent 记为 `unexecuted`，且不再访问后续 Agent 的任何依赖。

Uninstall 的 batch marker 只能在 Release 成功后构造，结果也只在此后交给 CLI 渲染。Release 失败时返回零结果并抑制所有业务行，不得保留可被 CLI 识别为可渲染的 Uninstall batch marker；若同时存在业务错误，使用 `errors.Join` 保留全部 cause。先前已同步写出的 Warn 不撤回。

Install 在这一个锁区间内完成每个目标 Agent 的 `Resolve`、`Find`、`Fetch`，以及 cache miss 时的 `Get → Fetch` 和 Transaction。相同版本只信任清单，既不检查 target、Fetch，也不调用 Transaction。`all` 按快照字典序 fail-fast：首个业务错误后记录 `failed` 和剩余 `unexecuted`，不再调用其依赖。预执行错误没有可渲染结果；业务失败的 `InstallBatchError` 采用延迟 marker 构造，只能在 Release 成功后构造。Release 失败返回零结果并抑制 renderer，即使此前 Agent 已完成；同时使用 `errors.Join` 保留业务 cause。

因此默认 Cloud List 不观察或修复活动 journal，也不访问 Registry 或 Config。installed List 的 `all` 模式在锁内继续收集逐 Agent Config 读取失败；只有全部读取完成且 Release 成功后，才返回唯一可渲染的 partial marker 和成功记录。Recover、Refresh、具体 Agent 校验、Config 读取或 Release 的 fatal error 均不输出业务结果；Release 失败还必须抑制原本可能的 partial 输出并保留全部 cause。该 List 契约与命令语法见 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md)，Agent 排序见 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md)，Config 读取契约见 [ADR-007](ADR-007-use-stateless-config-repository.md)。

已实现的 Search 顺序固定为：获取锁 → `Recover` → 锁内同步记录恢复成功 `Warn` → 释放锁 → Cloud 查询。无活动 journal 时可在锁内记录 `Debug` 后释放锁。恢复错误时，无论如何都在返回前尝试释放锁；CLI 在 lifecycle 返回后只记录一次错误并打印 usage。只有释放成功后，Search 才调用 Cloud。

正常受支持状态至多存在一个未完成 journal。每条需要恢复的命令在执行共享状态业务前检查该 journal：提交点前执行回滚，提交点后完成提交；修复成功后必须先删除已恢复 journal，再向调用方返回恢复方向与内容并继续原命令。删除失败视为恢复失败：保留 journal、在尝试释放锁后向 CLI 返回错误并非零退出，不执行用户原命令。其他修复失败同样保留 journal、在尝试释放锁后由 CLI 记录一次错误并非零退出。

install 在原子 staging-to-target rename 前记录 publishing 意图，该 rename 是提交点。uninstall 在原子 target-to-quarantine move 前记录 removing 意图，该 move 是提交点。恢复时结合 journal 阶段和 staging、target、backup/quarantine 路径状态覆盖“原子操作完成但阶段尚未更新”的中断窗口。

> [ADR-009](ADR-009-centralize-lifecycle-transactions.md) 修订了 Install 的实现边界：Adapter 把 staging 复制到目标同目录临时目录，并以临时目录到 target 的原子 rename 作为同一提交点；transaction 在调用 Adapter 前持久化 publishing，并通过显式 `Published` 结果或中断后的 target 状态固定恢复方向。

已实现的 Install 和 Uninstall 在 `--agent=all` 下都按当前快照中已支持且已安装 Agent ID 升序串行且 fail-fast：任一 Agent 业务错误后立即把当前 Agent 记为 `failed`、按相同顺序把全部剩余 Agent 记为 `unexecuted`，停止执行并在成功 Release 后由 CLI 统一渲染结果。`list --installed --agent=all` 是只读汇总，单个 Agent 读取失败不阻止读取其他 Agent；成功 Release 后可先渲染成功记录、再以 partial error 非零退出。

已实现的 Install 与 Uninstall 在当前命令内的回滚、清单更新或清理期间再次发生进程中断时，不尝试在中断路径继续修复；内核释放全局锁，现有 journal 保持可恢复，交由下一条需要共享状态业务的命令重新判断并恢复。提交点后的清单更新或清理失败也不在当前命令中重试：保留 journal、由 CLI 记录失败并非零退出，并由下一条命令完成遗留事务。

恢复日志至少包含原操作、Agent、Power、版本、原阶段、回滚或完成提交、修复内容和结果。

## Consequences

- 正面影响：访问共享状态的命令看到一致的本地状态；中断恢复方向确定；只有成功删除已恢复 journal 后才执行新命令，不会在错误 journal 未解决时创建第二个事务；Search 的 Cloud 延迟与 CLI writer 延迟不阻塞其他共享状态工作。
- 代价与风险：每个未来生命周期都必须明确自己的共享状态边界；`--agent=all` 的 install/uninstall 可能产生“部分完成、其余未执行”的结果。
- 后续动作：在 Core Lifecycle、公共恢复和 Search 流程图、C4 properties 与项目概览中引用并表达本决策。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 仅 install/uninstall 持锁 | 已实现的 installed List 同样读取共享 Config 状态，必须在恢复、刷新和全部读取期间持锁。 |
| 仅在修复时临时加锁 | 锁范围分裂，命令状态可能在预检后被另一进程改变。 |
| 锁一直持有到 CLI 渲染结束 | CLI writer 延迟会无必要地阻塞共享状态业务，且渲染不需要该序列化保证。 |
| transactional `install/uninstall --agent=all` 单 Agent 失败后继续 | 未解决 journal 存在时无法安全创建下一事务，破坏单 journal 不变量；只读的 installed List 是例外，它不创建事务，可在同一锁内继续收集 Config 读取失败。 |
| 所有阶段一律回滚或一律继续 | 无法同时保证原子提交点前恢复旧状态和提交点后补全一致性。 |

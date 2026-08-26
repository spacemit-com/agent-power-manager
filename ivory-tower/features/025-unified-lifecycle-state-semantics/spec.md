# Feature 025：统一 Power Lifecycle 入口状态语义

- Status：Accepted
- Date：2026-08-26
- Related ADRs：[ADR-022](../../adr/ADR-022-unify-lifecycle-entry-state-semantics.md)

## 1. Goal

统一 Skill、Agent、MCP 与 Plugin 在 `install` 和 `uninstall` 中面对相同入口状态时的
宏观行为。入口状态只由 APM 安装清单、目标 Agent 中的实际运行目标以及安装请求版本
决定；四类 Power 保留各自的文件系统、Agent CLI、marketplace 与恢复实现。

本功能统一以下用户可观察契约：

- 命令是否执行、成功或失败；
- `installed`、`unchanged`、`upgraded`、`downgraded`、`completed`、`failed` 与
  `unexecuted` 结果；
- warning 与 Error 的条件、次数和字段；
- 无清单目标冲突的公共错误类型；
- 清单、运行目标、Plugin marketplace 和 journal 的最终状态。

## 2. Scope

### 2.1 Included

- 四类 Power 的统一安装与卸载入口状态矩阵。
- 共享的纯 lifecycle 状态策略和类型专属的 probe/executor。
- 所有 Power 的显式精确版本清单命中快速路径。
- 不同版本安装遇到目标缺失时的两阶段清理与首次安装。
- Plugin 的联合运行状态、残留清理和 marketplace 注销。
- 公共 `ErrTargetConflict`、batch 结果、warning 和 CLI 打印契约。
- 状态矩阵、类型契约、恢复与 CLI 验收测试。
- Feature、ADR、BUILD、项目概览及受影响历史文档的最终实现同步。

### 2.2 Excluded

- CLI 参数或输出行格式变更。
- 多 Agent 并行、失败后继续或跨 Agent 原子回滚。
- Cloud、catalog、content 或 Submission 协议变更。
- 删除 Plugin artifact、Skill cache 或 Agent cache。
- 自动接管没有 APM 清单的现存目标。
- 为同版本 `unchanged` 增加目标审计或修复。
- 把四类 transaction driver 合并为一个通用物理事务实现。

## 3. Terms and State Model

### 3.1 Manifest state

安装使用以下清单状态：

- `absent`：目标 Agent 的清单没有该 Type/Name。
- `same-version`：清单版本与本次确定的目标版本完全相同。
- `different-version`：清单 Type/Name 相同，但版本不同。

卸载不接受版本，只区分清单 `absent` 与 `present`；清单中的精确版本是后续检查和结果
输出的唯一版本来源。

### 3.2 Target state

类型专属 probe 把本地事实归一为：

- `absent`：明确确认运行目标不存在。
- `complete`：明确确认运行目标及必要辅助事实完整。
- `incomplete`：能够证明是清单所管理的目标，但至少一个必要组成部分缺失。
- `conflict`：不能证明现存事实属于清单引用，或 Agent/文件系统无法给出确定、安全的状态。

Skill、Agent 与 MCP 通常只产生 `absent`、`complete` 或 `conflict`。Plugin 可以产生完整的
四态结果。

`same-version` 在任何 target probe 前直接返回，因此目标当时是否存在、完整或损坏都不影响
`unchanged`。这是明确的幂等契约，不是目标健康检查。

### 3.3 Plugin target

Plugin 本体是实际 Power 目标。Codex/Claude 的专属 `apm-<name>` marketplace 是必要辅助
事实；Hermes/Bibit 的 filesystem Plugin 没有 marketplace。

marketplace 部署的 Plugin 状态定义如下：

- `absent`：Plugin 与专属 marketplace 都不存在。
- `complete`：Plugin、marketplace、selector、source 和必要本地 source 路径都存在并与
  清单引用一致。
- `incomplete`：事实能够证明属于该清单引用，但 Plugin、marketplace 或必要 source 至少
  一项缺失。
- `conflict`：selector/source 指向其他资源、同名事实不唯一、身份不匹配或状态不确定。

“不完整按缺失处理”只适用于存在 APM 清单、能够证明残留归 APM 管理的路径。没有清单时
不得据此接管或清理残留；任何现存 Plugin 或 marketplace 事实都按目标冲突拒绝安装。

## 4. Architecture

### 4.1 Shared lifecycle policy

`internal/lifecycle` 增加共享的纯状态策略。概念模型为：

```text
ManifestState = absent | same-version | different-version
TargetState   = absent | complete | incomplete | conflict
Action        = unchanged | install-new | replace |
                clean-and-install | uninstall | clean-only | reject
```

策略不访问文件系统、Agent CLI、Cloud、cache、Plugin Resource 或 config。它只把入口状态
映射为动作、outcome、warning 事实和公共业务错误。类型专属代码继续负责：

- Skill：目录检查、staging 发布和文件系统 transaction。
- Agent：Bibit `agents/<agent.name>` 完整目录检查、发布和文件系统 transaction。
- MCP：Agent CLI Inspect/Add/Remove 和 agent-cli transaction。
- Plugin：Plugin/marketplace 联合检查、artifact/staging 准备和 plugin transaction。

不建立一个统一的物理 `PowerDriver` 来替换现有 transaction driver。共享层只统一宏观决策，
避免把文件 rename、MCP CLI mutation 和 Plugin marketplace recovery 强行抽象为相同操作。

### 4.2 Explicit exact-reference fast path

显式提供规范 `--type`、name 与版本时，Install 在 Agent 选择后构造精确 `PowerRef` 并先读取
所有目标 Agent 的清单。若所有清单均精确命中，则直接为每个 Agent 返回 `unchanged`，不访问
catalog、Cloud、cache、Plugin Resource、Adapter 或 Transaction。

只要有一个 Agent 未命中，命令回到正常 catalog 解析，以同一权威 `CatalogEntry` 完成后续
批处理。每个已经同版本命中的 Agent 仍在自己的执行位置直接返回 `unchanged`，不做 target
probe。未显式给出完整引用时，必须先通过 catalog 确定版本，之后再使用相同策略。

### 4.3 Existing behavior audit

实现前基线按宏观入口状态逐项核实如下。表中的“Plugin 部分”只指 marketplace 部署中 Plugin
本体与专属 marketplace 没有同时完整存在；artifact cache 不作为实际 Power 目标。

#### Install baseline

| Manifest/target entry state | Skill | Agent | MCP | Plugin |
| --- | --- | --- | --- | --- |
| 无清单、目标无 | `installed` | `installed` | `installed` | `installed`；可复用精确 retained marketplace |
| 无清单、目标有 | `ErrTargetConflict`，`failed` | `ErrTargetConflict`，`failed` | `ErrMCPConflict` + `ErrTargetConflict`，`failed` | `ErrPluginConflict` + `ErrTargetConflict`；marketplace 冲突可先返回 `ErrMarketplaceConflict` |
| 同版本清单、目标有 | 不 probe，`unchanged` | 不 probe，`unchanged` | probe 后 `unchanged` | 不 probe，`unchanged` |
| 同版本清单、目标无 | 不 probe，`unchanged` | 不 probe，`unchanged` | 删除清单并以 `ErrStaleManifest` 返回 `failed` | 不 probe，`unchanged` |
| 不同版本清单、目标完整 | `upgraded`/`downgraded` | `upgraded`/`downgraded` | `upgraded`/`downgraded` | `upgraded`/`downgraded` |
| 不同版本清单、目标无 | 删除清单，`ErrStaleManifest`，`failed` | 删除清单，`ErrStaleManifest`，`failed` | 删除清单，`ErrStaleManifest`，`failed` | 完整缺失时清理并同次 `installed` |
| 不同版本清单、Plugin 部分 | 不适用 | 不适用 | 不适用 | `ErrIndeterminateState`，保留清单并 `failed` |

Skill 与 Agent 的同版本快速路径在 lifecycle 层返回，因此不论目标是否存在都不可观察。Agent
Power 仅支持 Bibit，目标是 `<bibit-root>/agents/<agent.name>` 完整目录；除卸载后清理空父目录
外，其基线宏观状态与 Skill 相同。

#### Uninstall baseline

| Manifest/target entry state | Skill | Agent | MCP | Plugin |
| --- | --- | --- | --- | --- |
| 无清单、目标无 | warning，`unexecuted`，不 probe | 相同 | 相同 | 相同 |
| 无清单、目标有 | warning，`unexecuted`，不接管 | 相同 | 相同 | 相同 |
| 有清单、目标完整 | 删除目标与清单，`completed` | 相同 | 删除注册与清单，`completed` | 删除 Plugin 与清单、保留 marketplace；marketplace 模式还要求本地 artifact `Lookup` 成功 |
| 有清单、目标无 | warning，删除清单，`completed` | 相同 | 相同 | Plugin 与 marketplace 都 absent 时相同 |
| 有清单、Plugin 部分 | 不适用 | 不适用 | 不适用 | `ErrIndeterminateState`，保留状态并 `failed` |

无清单卸载和普通成功/失败的 stdout/batch 渲染已经由公共 CLI 路径统一。主要漂移在安装目标
缺失、冲突 sentinel 数量、Plugin 部分状态和卸载后 retained marketplace；本 Feature 只修改
这些宏观差异及其必要恢复流程。

## 5. Install Contract

### 5.1 State matrix

| Manifest | Target | Action | Result | Warning/Error |
| --- | --- | --- | --- | --- |
| absent | absent | 首次安装 | `installed` | 无 |
| absent | complete/incomplete/conflict | 保留现场并拒绝 | `failed` | 公共 `adapter.ErrTargetConflict` |
| same-version | 未检查 | 无操作 | `unchanged` | 无 |
| different-version | complete | 原子版本替换 | `upgraded` 或 `downgraded` | 无 |
| different-version | absent/incomplete | 清理后首次安装 | 成功为 `installed` | 一次目标缺失 warning |
| different-version | conflict | 保留现场并拒绝 | `failed` | 状态检查错误 |

### 5.2 Missing-target repair

不同版本安装确认旧目标 `absent` 或 `incomplete` 时：

1. 同步记录一次 `installed power target is missing` warning。
2. 在可恢复修复阶段删除仍存在且已确认归 APM 管理的残留。
3. 删除旧清单并提交修复，使状态成为“清单无、目标无”。
4. 以首次安装语义准备并安装新版本。
5. 成功只输出 `installed`，不输出 `upgraded` 或 `downgraded`。

warning 至少携带 `operation=install`、agent、type、power 和旧清单 version。修复成功不是错误，
不得返回 `ErrStaleManifest`。后续内容准备或安装失败时，warning 保留，命令返回实际失败，且
不得恢复已经确认陈旧的清单或残留。

### 5.3 Target conflict

无清单但发现任意现存运行目标或 Plugin 辅助事实时，四类 Power 都拒绝自动接管和改写。
对外业务原因只保证 `errors.Is(err, adapter.ErrTargetConflict)`；MCP/Plugin 专属 conflict
sentinel 不再作为额外 `errors.Is` 类型暴露。错误文字可保留 Agent、Power 类型和实际事实
上下文。

## 6. Uninstall Contract

### 6.1 State matrix

| Manifest | Target | Action | Result | Warning/Error |
| --- | --- | --- | --- | --- |
| absent | 未检查 | 无操作 | `unexecuted` | 一次未安装 warning |
| present | complete | 正常卸载 | `completed` | 无 |
| present | absent/incomplete | 清理残留和清单 | `completed` | 一次目标缺失 warning |
| present | conflict | 保留现场并拒绝 | `failed` | 状态检查错误 |

清单不存在时，不调用类型 Adapter 检查目标。即使 Agent 中存在同名目标，也不接管、不删除、
不返回 `ErrTargetConflict`；命令记录一次 `power is not installed` warning，输出 Agent-only
`unexecuted` 并继续后续 Agent。

### 6.2 Present and missing targets

清单存在且目标完整时，各类型删除其运行目标，最后删除清单。清单存在但目标缺失或不完整
时，命令记录一次 `installed power target is missing` warning，清理能够确认归 APM 管理的
残留，删除清单并返回 `completed`。

warning 至少携带 `operation=uninstall`、agent、type、power 和清单 version。已经确认的缺失
事实即使后续清理或 Release 失败也必须保留在 stderr。

### 6.3 Plugin uninstall

Plugin 卸载后的运行状态必须完整缺失：

- 删除仍存在的 Plugin；
- 注销仍存在的专属 `apm-<name>` marketplace；
- 删除 APM manifest；
- 保留共享、不可变 artifact cache。

因此正常卸载不再保留 marketplace 注册。后续安装任意版本都按正常注册流程执行；artifact
缓存命中时不重新下载，缓存缺失时才从 content source 获取。Plugin 部分目标的处理为：

- marketplace 存在、Plugin 不存在：注销 marketplace 后删除清单；
- marketplace 不存在、Plugin 存在：删除 Plugin 后删除清单；
- 两者都不存在：只删除清单；
- 两者都完整存在：删除 Plugin、注销 marketplace，再删除清单。

任何事实不确定、身份冲突或 mutation 结果未知都 fail closed，保留 manifest 和 journal
证据，由 `fix` 按已持久方向恢复。

## 7. Transaction and Recovery

### 7.1 Repair followed by fresh install

`clean-and-install` 是两个顺序执行、分别可恢复的阶段：

1. repair transaction 清理残留并删除旧清单；
2. fresh install transaction 安装新版本并写入新清单。

两阶段复用全局锁和唯一活动 journal，但不同时创建两个 journal。repair 成功后必须删除其
journal，才能开始 fresh install：

- 中断在 repair：`fix` 继续收敛到完全未安装；
- 中断在两阶段之间：已经是合法的完全未安装状态，无需 journal；
- 中断在 fresh install：按该类型既有首次安装规则恢复。

新版本准备或安装失败时不复活旧残留或旧清单。完整目标上的正常升级/降级仍使用既有原子
替换与 rollback 语义，失败时尽量恢复旧目标和旧清单。

### 7.2 Plugin recovery

Plugin journal 必须覆盖本体删除、marketplace 注销和 manifest 删除的确定方向。正常卸载与
不完整目标清理都收敛到三者不存在；恢复不得重新注册已决定卸载的 marketplace，不连接
Cloud，不调用 Plugin Resource `Ensure`，也不重建 artifact。

安装 repair 阶段同样只清理现有受管事实。fresh install 才允许为目标版本调用 `Ensure` 和
staging；其 journal 不依赖已经提交删除的旧清单。

## 8. Result, Logging, and Error Contract

- 每个目标 Agent 恰好产生一条 stdout 结果。
- 缺失修复成功是 warning 加成功结果，不返回错误。
- 每个缺失事实每个 Agent 恰好记录一次 warning，不由 Adapter 或 Transaction 重复记录。
- 一个失败 Agent 只返回一个 `InstallBatchError` 或 `UninstallBatchError`；CLI 只记录一次
  Error，不为运行时失败打印 usage。
- `--agent=all` 保持 Agent ID 字典序、首错停止，后续 Agent 输出 `unexecuted`。
- Release 失败继续抑制全部 stdout 业务结果，但不撤回已经同步写出的事实 warning。
- 参数错误、恢复 warning、stale catalog warning 和 writer error 保持既有命令契约。
- 内部清理失败可以保留底层 cause 或组合恢复错误，但不能产生重复的 CLI Error 行；目标
  冲突的公共业务 sentinel 仍只有 `adapter.ErrTargetConflict`。

统一事实消息为：

```text
installed power target is missing
power is not installed
```

前者用 `operation=install|uninstall` 区分命令；后者只用于卸载清单无记录。

## 9. Testing

### 9.1 Shared policy tests

用表驱动测试穷举 ManifestState、TargetState 和操作，断言 Action、outcome、warning 事实和
公共错误。纯策略测试不得依赖真实文件系统或 Agent CLI。

### 9.2 Four-Power contract tests

对 Skill、Agent、MCP、Plugin 运行同一安装/卸载入口矩阵，至少验证：

- outcome 和 stdout item 数量；
- warning 条件和精确次数；
- `ErrTargetConflict` 的唯一公共业务原因；
- 同版本和无清单卸载的零 target-probe；
- repair 完成后才进入首次安装；
- batch fail-fast、后续 `unexecuted` 和 Release gate。

### 9.3 Plugin and recovery tests

Plugin 专项覆盖两项都存在、两项都缺失、仅 Plugin、仅 marketplace、source/selector 冲突、
artifact 缓存保留，以及卸载后 marketplace 注册消失。Recovery 覆盖 repair 各 mutation、阶段
间隙、fresh install 和正常升级/降级的中断。

自动化测试不得调用真实 Agent CLI。继续使用隔离临时 home 与 fake-bin，并断言所有 CLI
路径位于测试目录。

### 9.4 Verification

实现完成后运行定向 lifecycle/transaction/CLI 测试、全量测试、race、vet、CLI build、
gofmt 和 `git diff --check`。行为实现完成后按仓库模板向 `docs/Changelog.md` 添加本次用户
请求唯一一条记录；设计和测试运行本身不单独记录。

## 10. Documentation and Compatibility

实现时更新 BUILD、项目概览、C4 lifecycle/transaction 描述及被取代的历史 feature 状态说明，
并引用 ADR-022。ADR-022 定向取代以下旧语义：

- 不同版本安装遇到缺失目标时删除清单并返回 `ErrStaleManifest`、要求再次执行；
- Plugin 部分状态一律保留 manifest 并拒绝收敛；
- Plugin 正常卸载保留 marketplace 注册；
- 只有 Plugin 支持显式精确版本清单全命中的无 catalog 快速路径。

不提供旧行为兼容开关。CLI 语法与成功结果行格式保持兼容，但依赖旧 warning/error 或保留
marketplace 行为的调用方必须迁移到本规格。

## 11. Acceptance Criteria

1. 四类 Power 通过同一安装和卸载入口状态契约测试。
2. 显式精确版本清单全命中时四类 Power 都返回 `unchanged` 且不访问任何目标或内容依赖。
3. 不同版本加缺失/不完整目标产生一次 warning，并在同次命令完成 repair 与首次安装。
4. 无清单目标冲突只公开公共 `adapter.ErrTargetConflict`。
5. 无清单卸载不检查或删除 Agent 中的同名目标。
6. Plugin 卸载清除 Plugin、专属 marketplace 和 manifest，但保留 artifact cache。
7. repair 后 fresh install 失败时保持完全未安装，不恢复陈旧状态。
8. 每个 Agent 的 stdout、warning、Error 和 batch fail-fast 数量符合本规格。
9. 中断恢复不连接 Cloud、不重建旧 Plugin artifact，并收敛到已持久方向。
10. 文档、测试、格式、静态检查和构建验证全部通过，行为实现只写一条 Changelog 记录。

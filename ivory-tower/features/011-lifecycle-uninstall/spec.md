# Lifecycle Uninstall 规格

> **实现状态（2026-08-12）：** 本 spec 的已批准要求已实现。`Manager.Uninstall` 的 `TargetMissing` 事实返回与锁内同步消费边界由 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 记录。

## 目标

完成 `uninstall` 的 lifecycle 与 CLI 端到端接线。Uninstall 在同一次全局 APM 锁内完成 transaction 恢复、Agent 快照、逐 Agent 本地清单解析及串行卸载；恢复、未安装和目标缺失事实在锁内同步写入 stderr，只有成功释放锁后才把结构化业务汇总交给 CLI 渲染。

本 feature 复用既有 Config、Adapter 目标路径和集中 transaction 能力，为 Uninstall 建立独立 service、端口、结果和 renderer。它不抽取跨 Search、List、Install、Uninstall 的通用 runner，也不重构前三条命令的生产 lifecycle。

CLI 语法为：

```text
apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

## 范围与非目标

### 包含

- Uninstall 命令专属 lifecycle 端口、factory、service、结构化结果和 renderer；
- Agent、Power 名称、可选 Type 以及禁止版本语法的参数契约；
- 每个目标 Agent 独立从本地清单解析唯一 `PowerRef`；
- 一次锁内的恢复、同步 Warn、Agent 刷新、Config 读取、Adapter Resolve 和 Transaction Uninstall；
- 清单无记录时的幂等 unexecuted 结果；
- 清单存在但目标缺失时的结构化 transaction 事实、清单修复和 completed 结果；
- `all` 字典序串行、首错停止和后续 unexecuted 补齐；
- CLI 精确汇总行、writer error、统一 Error/usage 和退出状态；
- 默认依赖接线、ADR、C4、项目概览、构建指南、相关 feature 文档和实现后的 Changelog 同步。

### 不包含

- Search、List 或 Install 生产 lifecycle、结果接口或业务流程重构；
- 通用 lifecycle runner、跨命令锁模板或共享批处理框架；
- Cloud、Cache、VersionSelector 或任何远端查询；
- 用户指定版本、版本选择、升级、降级或依赖求解；
- Adapter `Uninstall` 方法；Transaction 继续通过既有 `Adapter.TargetPath` 定位目标；
- Plugin/MCP Adapter 或其真实卸载适配；
- 多 Agent 并行、失败后继续或跨 Agent 原子回滚；
- 非法历史清单的跳过、迁移、清洗、降级或部分读取；
- JSON、表格、分页、标题、颜色、进度条或交互确认。

## CLI 参数契约

Uninstall 使用 Cobra `ExactArgs(1)`。全部参数校验在构造 lifecycle 和执行任何共享状态业务前完成。

### Agent

- `--agent` 必填；缺失、空字符串或只含空白返回 `--agent is required`。
- 非空 Agent ID 保留原始值，按区分大小写的精确字符串比较；不 trim、不做 Unicode case folding、不解析别名。
- 字面量 `all` 只按精确值识别。
- 具体 Agent 必须存在于当前命令刷新得到的已安装快照。
- `--agent=all` 使用快照中的全部 Agent；空快照返回“没有已支持且已安装的 Agent”错误，不返回成功空汇总。

### Power 名称与类别

- `<power>` 去除首尾空白后不得为空；非空名称保留用户输入的原始字符串。
- 名称只与清单中的 `PowerRef.Name` 做区分大小写的精确匹配；不 trim 后替换原值，不做 Unicode 归一化、大小写折叠、子串、描述、标签或文件内容匹配。
- `--type` 为空时允许当前 Agent 独立从清单推断；非空时只接受规范小写 `skill`、`plugin` 或 `mcp`，并与 `PowerRef.Type` 精确比较。
- 显式 Type 没有匹配记录时按“该 Agent 未安装此 Power”处理，不回退到其他类别。

### 禁止版本语法

- Uninstall 不接受版本；版本只来自目标 Agent 的本地清单。
- 位置参数中出现 `=`，包括 `name=v1.2.3` 和 `name=`，都明确返回“uninstall does not accept a version”参数错误。
- 显式版本不能参与名称、类别或本地记录消歧，也不能作为“仅当版本相等才卸载”的前置条件。
- 参数错误由现有 CLI 单一错误出口记录一次 Error、打印 Cobra usage 并返回非零状态，不创建 lifecycle。

## CLI 拥有的 Uninstall 契约

Uninstall 使用命令专属端口：

```go
type UninstallLifecycle interface {
    Uninstall(context.Context, Flags) (UninstallResult, error)
}

type UninstallOutcome string

const (
    UninstallOutcomeCompleted  UninstallOutcome = "completed"
    UninstallOutcomeFailed     UninstallOutcome = "failed"
    UninstallOutcomeUnexecuted UninstallOutcome = "unexecuted"
)

type UninstallItem struct {
    Agent   domain.AgentID
    Power   *domain.PowerRef
    Outcome UninstallOutcome
}

type UninstallResult struct {
    Items []UninstallItem
}

type UninstallBatchError struct {
    Agent domain.AgentID
    Err   error
}

type UninstallLifecycleFactory func() (UninstallLifecycle, error)
```

`Power == nil` 表示 lifecycle 尚未解析到唯一清单引用：清单无记录、清单读取或类别消歧失败，以及前序 Agent 失败后未再读取清单都使用该形态。得到唯一 `PowerRef` 后，Adapter Resolve 或 Transaction 失败仍保留完整引用。Lifecycle 创建 item 时复制引用值，不保留 Config 返回 map 或 slice 的引用。

`UninstallResult.Items` 在所有可渲染返回中均为非 nil，并按目标 Agent ID 字典序排列。具体 Agent 模式只包含该 Agent。每个目标 Agent 恰有一条 item，包括未安装和前序失败后未执行的 Agent。

`UninstallBatchError` 是成功 Release 后才允许构造的轻量可渲染 marker。它只说明批处理中首个失败 Agent 及底层原因，不表示 error 已经被实时记录；CLI 仍是唯一 Error 记录者。

不复用旧 `domain.Result` 作为 CLI 契约。该占位结构不能表达可选 Power，也不能承载 Uninstall 专属的渲染与 batch marker 边界；本 feature 不因这一选择清理其他无关领域类型。

所有重要端口、结果、outcome、marker、factory、Transaction 结果、构造函数和执行方法添加简要、规范的中文注释，说明用途与必要约束。

## Lifecycle 组件边界

Uninstall service 只依赖：

```go
type UninstallService struct {
    locker       Locker
    transactions TransactionManager
    registry     AdapterRegistry
    config       ConfigRepository
}
```

构造函数拒绝 nil 和 typed-nil 依赖，并提供 `cli.UninstallLifecycle` 编译期断言。

职责划分：

- CLI：参数语法、factory 创建、结果渲染和统一错误出口。
- Lifecycle：Agent 目标集、逐 Agent Config `List`、本地名称/类别消歧、Adapter Resolve、串行调度、同步业务 Warn 和 outcome 分类。
- Adapter Registry：刷新当前已安装 Agent 快照，并按已解析 Agent/Type 返回 Adapter。
- Adapter：只提供 Agent 检测、目标路径解析和既有安装能力；不读取清单、不执行卸载、不记录 Warn/Error。
- TransactionManager：目标检查、journal、quarantine、清单 Remove、清理、结构化目标缺失事实和跨命令恢复。
- Config Repository：保持无状态；Lifecycle 调用 `List`，TransactionManager 调用 `Remove`。

默认工厂为 lifecycle 的 Config 读取和 TransactionManager 的清单写入传入同一个 Repository 实例。Search、List、Install 和 Uninstall factory 保持相互独立，不创建共享应用容器。

## Transaction Uninstall 结果契约

将高层接口最小调整为：

```go
type Manager interface {
    Recover(context.Context) (RecoveryResult, error)
    Install(context.Context, InstallRequest) error
    Uninstall(context.Context, UninstallRequest) (UninstallResult, error)
}

type UninstallResult struct {
    TargetMissing bool
}
```

这里的 `UninstallResult` 位于 `transaction` package，与 CLI 的逐 Agent `cli.UninstallResult` 是不同层次的类型。

`TargetMissing` 描述 Transaction 已经确认的文件系统事实：

- 初次检查 target 就不存在时为 true；
- 初查存在，但 quarantine rename 失败后复查确认 target 已被外部删除时也为 true；
- target 已成功移动到 quarantine 时为 false；
- 在确认 target 是否存在前失败时为 false。

一旦确认缺失，后续所有返回都保留 `TargetMissing=true`，包括持久化 committing 方向、上下文检查、清单 Remove、quarantine/事务目录清理或 journal Delete 失败。结果与 error 可以同时非零；调用方不能因 `err != nil` 丢弃已确认事实。

目标缺失继续沿用既有幂等提交语义：Transaction 创建并推进 journal、Remove 对应 Agent/Type/Name 的清单项、清理事务证据并完成。它不返回 `ErrStaleManifest`，也不尝试恢复或创建目标。成功意味着用户要求的最终状态“未安装且无对应清单记录”已经达成。

Transaction 不直接调用 `slog.Warn` 或 `slog.Error`。Lifecycle 在 Transaction 返回后、仍持有锁时消费 `TargetMissing`；即使同时收到 error，也先同步记录目标缺失 Warn，再分类失败结果。

该接口决策修订 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。Search、List 和 Install 的生产流程不变。由于三个现有 service 依赖完整 `transaction.Manager` 别名，它们的测试替身需要机械更新 `Uninstall` 方法签名，但不修改构造函数、结果契约、锁边界或业务测试。

本 feature 不借机把 Manager 拆成多个最小接口。接口隔离会修改三个既有 lifecycle 的依赖面，超出实现 Uninstall 所需范围。

## 本地 Power 解析

Lifecycle 对每个 Agent 调用一次 `ConfigRepository.List(agent)`。Repository 继续完整读取并验证该 Agent 的固定分组清单；非法版本、结构或重复条目使整个读取失败，Lifecycle 不跳过、迁移或降级非法数据。

### 显式 Type

只收集 `Name == flags.Name` 且 `Type == flags.Type` 的记录：

- 没有记录：进入幂等未安装分支；即使其他 Type 有同名记录也不回退。
- 有唯一记录：使用清单中的完整 Type、Name 和 Version。

### 省略 Type

先按 `Name == flags.Name` 收集当前 Agent 的所有类别：

- 没有记录：进入幂等未安装分支。
- 只有一个类别：使用该类别中的唯一清单引用。
- 跨类别同名：当前 Agent 失败并要求显式 `--type`。

消歧完全按 Agent 独立进行。同一个 `uninstall foo --agent=all` 可以在一个 Agent 卸载 `skill/foo@v1.0.0`，在另一个 Agent 卸载 `plugin/foo@v2.0.0`；只要每个 Agent 内部都不歧义，就不要求整条命令统一 Type 或 Version。

Config Repository 已保证 Type、Name 和规范 `vMAJOR.MINOR.PATCH` 清单约束。Lifecycle 信任该公共契约，不重新验证或排序版本，也不为同 Type/Name 重复记录定义选择规则。

## 锁、恢复与预执行顺序

固定顺序为：

```text
CLI 解析和校验
  → 构造 Uninstall lifecycle
  → Acquire
  → TransactionManager.Recover(ctx)
  → 锁内同步记录恢复 Warn 或无 journal Debug
  → AdapterRegistry.RefreshInstalled()
  → 解析具体 Agent 或 all 目标集
  → 按 Agent ID 升序逐个读取清单并串行卸载
  → Release
  → CLI 渲染汇总
```

Uninstall 从 Acquire 到 Release 始终持有同一把全局锁。它不在 Recover 后释放并重新获取，也不在没有恢复的情况下创建新事务。

恢复规则与 Install 对齐：

- Recover 只能在 Acquire 成功后调用。
- 恢复成功时，在仍持锁且进入 Refresh 前同步提交一条 `slog.Warn("transaction recovered", ...)`。
- Warn 至少包含 operation、agent、power、version、原 phase、direction 和 actions。
- 无活动 journal 可写命令专属 Debug；普通模式不产生输出。
- Recover 失败立即停止原命令，但仍尝试 Release。
- 恢复日志是已完成恢复的实时审计记录，不因后续失败或 Release error 撤回。

目标集规则：

- 具体 Agent 不在快照中是预执行 fatal error。
- `all` 使用完整快照；空快照是预执行 fatal error。
- 快照由 Registry 按 Agent ID 原始字符串字典序返回，Lifecycle 保持该顺序。
- Agent 校验发生在任何 Config 读取前。

## 单 Agent 卸载与批处理

按目标 Agent 顺序执行：

1. `Config.List(agent)` 并依据本地 Power 解析规则选择唯一引用。
2. 清单读取失败或省略 Type 时跨类别歧义：当前 Agent 添加无 Power 的 `failed` item，停止。
3. 没有匹配记录：同步记录未安装 Warn，添加无 Power 的 `unexecuted` item，继续下一个 Agent。
4. 得到唯一引用后，复制该 `PowerRef` 并调用 `Registry.Resolve(agent, ref.Type)`。
5. Adapter Resolve 失败：当前 Agent 添加完整 Power 的 `failed` item，停止。
6. 调用 `TransactionManager.Uninstall(ctx, request)`。
7. 若返回 `TargetMissing=true`，无论 error 是否为空，都先同步记录目标缺失 Warn。
8. Transaction 成功：添加完整 Power 的 `completed` item，继续。
9. Transaction 失败：添加完整 Power 的 `failed` item，停止。

首个失败后不再读取后续 Agent 的 Config、不 Resolve Adapter、不调用 Transaction。所有剩余 Agent 按快照顺序添加无 Power 的 `unexecuted` item。已 completed 的 Agent 不回滚。

清单无记录的 `unexecuted` 不算失败；Lifecycle 继续处理后续 Agent。即使全部 Agent 都是该状态，命令也在成功 Release 和成功渲染后退出 0。

## 同步 Warn 契约

Warn 使用进程默认 logger，同步发生在 lifecycle 调用栈中，并且在 Release 前完成。它们写 stderr，不进入 `UninstallResult`，也不由 CLI 延迟重放。

### 清单无记录

```text
message="power is not installed"
operation=uninstall
agent=<agent-id>
power=<原始用户名称>
```

仅当用户显式提供 `--type` 时增加 `type=<type>`；省略 Type 时不写空值或 `unknown`。该 Warn 发生后对应 item 为 `<agent> unexecuted`，命令继续。

### 清单有记录但目标缺失

```text
message="installed power target is missing"
operation=uninstall
agent=<agent-id>
power=<manifest-name>
type=<manifest-type>
version=<manifest-version>
```

初次检查缺失和 rename 竞态缺失使用同一 Warn。它在 Transaction 返回后立即记录：成功时对应 item 为完整 completed；同时返回 error 时对应 item 为完整 failed。Warn 不包含 target 路径，不用“not installed”消息混淆清单无记录与受管目标漂移。

Lifecycle、Transaction、Adapter 和 renderer 不调用 `slog.Error`。最终 error 只由 CLI 根层记录一次。

## 结果与精确渲染

Renderer 根据 item 是否携带 Power 写两种精确行。

有唯一引用：

```text
<agent>/<type>/<name>@<version> <completed|failed|unexecuted>\n
```

没有唯一引用：

```text
<agent> <failed|unexecuted>\n
```

示例：

```text
claude-code/skill/demo@v1.0.0 completed
codex/plugin/demo@v2.0.0 failed
hermes-agent unexecuted
```

当前流程中，完整 Power 的常规结果是 completed 或 failed；未安装和首错后的 Agent 使用无 Power unexecuted。Renderer 仍严格依据 item 内容渲染，不自行查询或推断缺失字段。

不输出标题、分组、说明、颜色、占位 Type/Version 或额外空行。Warn 和诊断只写 stderr，汇总只写 stdout。

## 错误分类与 Release

### 预执行 fatal error

包括：

- Acquire 或 Recover 失败；
- Agent 刷新失败；
- 具体 Agent 不在快照；
- `all` 快照为空。

这些错误返回零结果，不输出 failed/unexecuted 汇总。

### 批处理错误

Config List、类别消歧、Adapter Resolve 或 Transaction Uninstall 失败均属于批处理错误。Lifecycle 在内存中保留已完成项、当前 failed 项并补齐剩余 unexecuted，但只有 Release 成功后才返回 `UninstallBatchError` marker。

具体 Agent 和 `all` 使用相同规则；具体 Agent 业务失败同样返回一条 failed item 与 marker。

### Release 规则

- Acquire 成功后的每条返回路径恰好尝试一次 Release。
- 业务错误和 Release 错误同时出现时使用 `errors.Join` 保留全部 cause。
- Release 失败时返回零结果，并且错误不得包含可被 CLI 识别为可渲染的 `UninstallBatchError` marker。
- 即使部分 Agent 实际已完成，Release 失败仍抑制全部业务 stdout。
- 已同步写入的恢复、未安装或目标缺失 Warn 不因 Release 失败撤回。
- Transaction 提交点后错误可能保留活动 journal；Lifecycle fail-fast、Release 并返回，下一条需要共享状态的命令负责恢复。

marker 必须在 Release 成功后构造，避免与 Release error 连接后仍被 `errors.As` 命中并错误渲染。

## CLI 错误出口与 writer failure

Uninstall RunE 行为：

- lifecycle 成功：渲染全部结果，返回 renderer error 或 nil。
- 返回 `UninstallBatchError`：先渲染全部结果，再返回 batch error。
- 返回其他错误：不渲染，直接返回。
- batch error 与 renderer error 同时存在：`errors.Join` 两者。

Writer 失败时保留已经写入的前缀，不尝试回滚 stdout。所有返回错误最终只由 `CLIEntry.Execute` 写一条 `slog.Error`、打印一次 Cobra usage 并返回非零状态。

Uninstall factory 在 CLI 预解析 `--verbose` 并安装默认 logger 后创建。nil factory、typed-nil service 和构造错误进入同一错误出口。每次 `CLIEntry.Execute` 创建新命令和新 lifecycle，不泄漏 Agent、Type、Name、logger 或结果状态。

## 对既有 lifecycle 的影响

本 feature 不修改 Search、List、Install 的生产 lifecycle、CLI 结果接口或业务流程：

- Search 仍只通过 Manager 调用 Recover；
- installed List 仍只通过 Manager 调用 Recover，Cloud List 仍不访问共享状态；
- Install 仍通过 Manager 调用 Recover 和 Install。

`transaction.Manager.Uninstall` 签名改变后，三个既有 lifecycle 测试中的 fake/stub 因实现完整 Manager 接口而需要返回零值 `transaction.UninstallResult`。这是编译兼容性的机械调整，不改变其测试场景或生产代码。

不采用为每条命令拆分 Recovery/Install/Uninstall 最小 transaction 接口的替代方案。该接口隔离可能是未来独立重构，但不是完成本 feature 的必要条件。

CLI 根命令需要增加 Uninstall factory 与默认依赖接线；现有命令树测试可相应补齐依赖，但 Search/List/Install 子命令端口和 factory 不变。

## 测试设计

### Transaction

覆盖：

- 正常 quarantine、清单 Remove 和清理成功返回 `TargetMissing=false`；
- 初次检查缺失成功返回 true；
- rename 竞态复查确认缺失返回 true；
- 已确认缺失后，committing 更新、上下文、清单 Remove、清理或 journal Delete 失败仍同时返回 true 与对应 error；
- 确认目标状态前失败返回 false；
- 既有 journal 阶段、提交、回滚、取消和恢复测试保持通过；
- Transaction 不直接输出 Warn/Error。

### Lifecycle

覆盖：

- nil 与 typed-nil Locker、TransactionManager、Registry 和 Config；
- Acquire、Recover/Warn、Refresh、Config、Resolve、Transaction、Release 的严格顺序；
- 恢复 Warn、未安装 Warn 和目标缺失 Warn 均在 Release 前可观察；
- 无活动 journal 普通模式无输出、verbose 模式只写 Debug；
- Name 区分大小写精确匹配；
- 显式 Type 精确过滤且不回退；
- 省略 Type 的无记录、唯一类别和当前 Agent 内跨类别歧义；
- 不同 Agent 可解析为不同 Type/Version；
- Config 非法或读取失败；
- 具体 Agent、all 字典序与空快照；
- 未安装 Warn、无 Power unexecuted 并继续；全部未安装仍成功；
- Transaction 初查和 rename 竞态缺失在成功/失败返回中都产生完整 Warn；
- Adapter Resolve 和 Transaction 失败保留完整 Power；
- 首错停止，后续 Agent 不读 Config 且补为无 Power unexecuted；
- fatal error 不返回结果；Release error 抑制结果与 batch marker 并连接 cause；
- 输入快照与 Config groups 不被修改。

### CLI 参数、端口与 renderer

覆盖：

- 缺失、空白 Agent；合法具体 Agent 和 `all`；
- 非法 Type、额外位置参数和未知 flag；
- 空白 Power 名称；Name 原值保留；
- `name=v1.2.3`、`name=` 和任何含 `=` 的位置参数均在 lifecycle 创建前拒绝并显示 usage；
- factory 在日志初始化后创建，nil/typed-nil 与构造错误；
- repeated Execute 不泄漏状态；
- 完整 Power 与 Agent-only 行的精确字节输出；
- success、batch、fatal 和 writer error 的渲染边界；
- batch 与 writer error 同时保留全部 cause；
- Search/List/Install 参数、结果和 factory 不回归。

### 默认接线与黑盒

在隔离 `APM_HOME` 和测试 Agent 根中：

1. 使用已实现 Install 安装内置 Skill，确认 target、清单和无活动 journal。
2. 调用 Uninstall，stdout 精确为完整 completed 行，stderr 为空，target 和对应清单项消失且无活动 journal。
3. 再次调用相同 Uninstall，stdout 精确为 `<agent> unexecuted`，stderr 恰有一条结构化 `power is not installed` Warn，退出码为 0。
4. 构造清单存在但 target 缺失的状态，确认 stdout 为完整 completed 行、stderr 恰有一条完整目标缺失 Warn、清单项被删除且无活动 journal。
5. 版本参数在进入 lifecycle 前非零退出，stdout 为空，stderr 恰有一条 Error 并包含 usage。

## 文档与 ADR

实现时同步：

- 修订 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)，记录 `UninstallResult.TargetMissing`、同时返回事实与 error，以及 Uninstall Manager 接线；
- 修订 [ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)，记录禁止版本、本地逐 Agent 消歧和双形态汇总；
- 修订 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)，记录 Uninstall 完整单锁、同步 Warn、Release 后渲染与 batch marker；
- 修订 [ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)，将 Uninstall 从计划语态改为已接线语义；
- 核对 ADR-007/008，不改变其 Repository 严格校验、Adapter 目标路径和职责边界；
- 更新 Uninstall lifecycle 图、Core Lifecycle、CLI Entry、Adapter Registry/Transaction 相关组件图及 C4 properties；
- 更新项目概览、BUILD 和仍称 Uninstall parse-only 的历史/当前文档；历史计划保留时明确标为 historical 或 superseded；
- 源码行为完成后，按项目模板新增一条 Feature 011 Changelog，列出本请求全部相关提交。

## 验收标准

实现完成必须同时满足：

1. Uninstall 只有一个全局锁区间，所有 Config、Adapter Resolve 和 Transaction 操作都在其中；结果仅在成功 Release 后渲染。
2. Recover 成功、清单无记录和目标缺失均按本 spec 在锁内同步 Warn；无普通重复 Error。
3. Name 区分大小写精确匹配，Type 按每个 Agent 本地清单独立解析，版本只能来自清单。
4. 无记录为 Warn + Agent-only unexecuted，并保持幂等成功；目标缺失为 Warn + 完整 completed，且对应清单项被移除。
5. 首个业务失败后不访问后续 Agent，并输出当前 failed 与剩余 Agent-only unexecuted；只有成功 Release 后的 batch error 可先渲染再非零退出。
6. Transaction 在初查与 rename 竞态缺失时都返回 `TargetMissing=true`，且后续错误不丢失该事实。
7. stdout 精确遵守完整 Power 行与 Agent-only 行，不含标题、占位字段或额外空行。
8. Search、List、Install 的生产行为与接口不变；仅完整 Manager 测试替身作机械签名调整。
9. 聚焦、全量、race、vet、build、format、diff、静态日志扫描和隔离 CLI 黑盒全部通过，且 `go mod tidy` 不产生非预期模块变化。

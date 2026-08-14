# Lifecycle Fix 与只读 Journal 检查规格

> 状态：已确认设计，待实施计划
>
> 日期：2026-08-14

## 背景

APM 当前由 `TransactionManager.Recover` 在生命周期入口读取唯一活动 journal，并按持久化阶段回滚或完成提交。Install 与 Uninstall 会改变 APM 清单、Power 目标或 Agent CLI 状态，因此它们在全局锁内先恢复再执行后续业务是必要的。

现有 Search 和 `list --installed` 也会获取全局锁并主动恢复，默认 Cloud List 则完全不检查 journal。这导致只读命令的语义不一致：查询可能隐式修改本地状态，用户也没有一条只执行恢复的明确命令。

本 feature 将“观察恢复需求”与“执行恢复”分离：Search、默认 List 和 Installed List 只做无锁检查和警告；Install、Uninstall 保持锁内恢复；新增 `apm fix` 在全局锁内执行一次恢复后退出。

本设计修订 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)，并补充 [ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md) 与 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 的当前边界。

## 目标

- 新增最小命令 `apm fix`，获取全局锁后调用一次 Recover 并退出。
- Install 与 Uninstall 继续在会改变状态的生命周期开始时执行 Recover。
- Search、默认 List 和 Installed List 不执行 Recover，也不获取全局锁。
- 三种只读查询都检查唯一活动 journal；合法活动 journal 提示运行 `apm fix`，损坏或不可读 journal 发出损坏警告。
- 只读检查失败不阻止原查询，也不改变原查询的业务结果或退出状态。
- Transaction 提供相互分离的 Inspect 与 Recover 公共语义，同时复用一次读取、统一校验的私有实现。
- 明确无锁只读结果只是尽力观测快照，不是跨 journal、Agent、manifest 和目标文件的全局一致快照。
- 保留 journal schema v1、driver、phase、恢复方向、actions、错误和清理语义。

## 非目标

- 不提供 `fix --force`、journal 删除、忽略校验或手工指定恢复方向。
- 不允许 Fix 选择 Agent、Power、类型、版本或 transaction ID。
- 不让 Fix 循环恢复多个 journal；正常受支持状态仍只有一个活动 journal。
- 不让 Fix 重新执行原始 Install/Uninstall 参数或重试用户原命令。
- 不为只读命令提供全局一致性、MVCC、文件系统快照或跨 Agent 原子读取。
- 不改变 Install/Uninstall 的多 Agent 排序、fail-fast、结果渲染和锁边界。
- 不改变 Skill、MCP、Plugin 的 transaction driver 或目标 Agent CLI 行为。
- 不修复无法通过 schema v1 严格校验的损坏 journal。

## CLI 契约

### 命令语法

```text
apm fix
```

Fix 使用 Cobra `ExactArgs(0)`，不定义命令专属 flag，只继承根命令的 `--verbose`。以下调用均为参数错误：

```text
apm fix anything
apm fix --agent=codex
apm fix --type=skill
```

CLI 在日志初始化后通过独立 factory 创建 Fix 生命周期。factory 缺失、构造失败或返回 nil/typed-nil service 时 fail closed。所有 Fix 错误继续经过现有单一 CLI 错误出口：记录一次 Error、打印 usage，并返回非零状态。

Fix 不返回业务结果，也不向 stdout 写行：

```go
type FixLifecycle interface {
    Fix(context.Context) error
}

type FixLifecycleFactory func() (FixLifecycle, error)
```

`CLIEntry` 按 Search、List、Install、Uninstall、Fix 的顺序保存五个独立 factory；根命令以同一顺序注册五个子命令。CLI 只负责语法、factory、调用和统一错误出口，不依赖 transaction 或 journal 存储。

### Fix 正常输出

- 没有活动 journal：退出 0，普通模式静默；verbose 模式记录 Debug。
- 恢复成功：退出 0，Lifecycle 同步记录现有 `transaction recovered` Warn，包括 operation、agent、power、version、原 phase、direction 和 actions。
- stdout 始终为空。

## Transaction 检查与恢复边界

### 公共接口

Transaction 新增只读检查契约，并把 Recover 抽为最小接口：

```go
type Inspector interface {
    Inspect(context.Context) (InspectionResult, error)
}

type Recoverer interface {
    Recover(context.Context) (RecoveryResult, error)
}

type Manager interface {
    Inspector
    Recoverer
    InstallSkill(context.Context, SkillInstallRequest) error
    UninstallSkill(context.Context, SkillUninstallRequest) (UninstallResult, error)
    InstallMCP(context.Context, MCPInstallRequest) error
    UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
    InstallPlugin(context.Context, PluginInstallRequest) error
    UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)
}
```

Search 与 List 的生产 service 只依赖 `Inspector`；Fix 只依赖 `Recoverer`；Install 与 Uninstall 继续依赖完整 Manager。默认 `transaction.Service` 同时实现三个接口。

### InspectionResult

```go
type InspectionResult struct {
    Active         bool
    TransactionID  string
    Operation      Operation
    Agent          domain.AgentID
    Power          domain.PowerRef
    OriginalPhase  Phase
}
```

- 无活动 journal 时返回 `Active=false`、其他字段为零值且 error 为 nil。
- 合法活动 journal 返回 `Active=true` 和已校验的 envelope 元数据。
- 结果不暴露 driver payload、staging、target、backup、quarantine、marketplace 或 Agent CLI 定义。
- journal 读取、解码或校验失败时返回 error，不返回未经完整校验的部分元数据。
- 非法格式继续满足 `errors.Is(err, ErrInvalidJournal)`。
- Inspect 没有执行恢复，因此不包装 `ErrRecoveryFailed`。

### 单一内部检查原语

`Service.Inspect` 与 `Service.Recover` 共用私有 `inspectActiveJournal`：

```text
检查依赖
  → JournalStore.Inspect 一次
  → 判断是否存在
  → 统一边界校验
  → 投影 InspectionResult
```

`Inspect` 到此返回，不写 journal、不解析恢复方向、不访问 manifest、Adapter 或目标文件。`Recover` 使用同一次读取返回的内部 Journal 继续 driver 路由和恢复，不先调用公开 Inspect，也不重新读取 journal。

默认 filesystem Store 继续严格解码 JSON；Service 边界仍校验一次，以防测试 Store 或未来其他 Store 返回违反契约的 Journal。读取和校验逻辑只有一个私有实现，不在 Inspect 与 Recover 中复制。

底层 `os.ReadFile` 不能被 context 强制中断；Recover 继续在任何恢复变更前检查 context。Inspect 本身只执行一次本地同步读取和纯校验。

## 只读命令流程

### 公共检查 helper

Lifecycle 新增无锁 `inspectJournal` helper。它只调用 `Inspector.Inspect` 并同步记录日志，不获取全局锁、不修改状态，也不把检查错误返回给调用方。

无活动 journal 时仅在 verbose 模式记录：

```text
level=DEBUG msg="no active transaction before read-only command" command=<search|list>
```

存在合法活动 journal 时记录：

```text
level=WARN
msg="transaction recovery required"
command=<search|list>
operation=<install|uninstall>
agent=<agent-id>
power=<power-name>
version=<version>
phase=<phase>
hint="run apm fix"
```

任意读取、解码或校验错误时记录：

```text
level=WARN
msg="transaction journal is damaged"
command=<search|list>
error=<完整错误链>
```

损坏警告不附加 `run apm fix`，因为 Fix 必须沿用严格校验，不能安全猜测损坏 journal 的恢复方向。警告不进入 CLI 结构化结果，也不由 CLI 延迟重放。

### Search

```text
CLI 参数校验
  → 构造 Search lifecycle
  → Inspect journal（无锁；警告后继续）
  → Cloud List
  → 筛选、选择最新版和排序
  → CLI 渲染
```

Search 删除 Locker 与 Recover 依赖。Inspect 后 journal 可能被并发创建、更新、恢复或删除；该变化不阻止 Cloud 查询。

### 默认 List

```text
CLI 参数校验
  → 构造 List lifecycle
  → Inspect journal（无锁；警告后继续）
  → Cloud List
  → 过滤和排序
  → CLI 渲染
```

默认 List 不再是“不访问任何本地状态”，而是只读取集中 journal 后访问 Cloud。它仍不刷新 Agent、不读取 manifest、不修改任何本地状态。

### Installed List

```text
CLI 参数校验
  → 构造 List lifecycle
  → Inspect journal（无锁；警告后继续）
  → RefreshInstalled
  → 解析具体 Agent 或 all
  → 逐 Agent Config List
  → 结果计算
  → CLI 渲染
```

Installed List 删除 Locker、Recover 和 Release gate。具体 Agent 的 Refresh、Agent 校验或 Config 失败仍是 fatal error，不返回业务行。`--agent=all` 仍读取所有快照 Agent，按原顺序收集 Config failure，并返回成功记录加 `PartialListError`；因为不再持锁，不存在 Release error 抑制结果的分支。

### 检查错误与业务退出状态

Inspect error 始终只产生 Warn，然后继续原只读业务：

- 后续业务成功时，命令退出 0。
- 后续业务失败时，保持 Search/List 原有错误、partial marker 和退出规则。
- CLI 不把 Inspect error 再记录为 Error。
- 一条命令最多记录一条 journal 检查 Warn。

## 无锁观测与一致性限制

Search、默认 List 和 Installed List 全程不获取全局 APM 锁。现有 journal 与每个 Agent manifest 通过同目录临时文件和原子 rename 替换，因此单次读取只能看到完整旧文件或完整新文件，不会读取 APM 正常写入产生的半截 JSON。

无锁查询不提供跨资源的一致时间点：

- Inspect 之后 Install/Uninstall 可能立即创建 journal，因此查询可能没有警告刚开始的事务。
- Inspect 看到活动 journal 后，Fix 或其他写命令可能立即完成恢复，因此警告可能在输出时已经过期。
- `list --installed --agent=all` 逐个读取 Agent，可以混合不同时间点的完整 manifest。
- transaction 的目标文件、Agent CLI 状态与 manifest 分步提交，Installed List 可能暂时显示提交前或提交后的清单，而实际目标处于另一中间阶段。
- Agent 根目录也可能在 RefreshInstalled 期间被外部创建或删除，快照只代表逐项检测时的观测。

这些结果定义为“无锁尽力观测快照”，不得描述为全局一致快照。只读命令不根据不一致结果执行清理、回滚、提交或清单修复；用户通过合法 journal 警告决定是否运行 `apm fix`。

## Fix 生命周期

`FixService` 只依赖 `lock.Locker` 与 `transaction.Recoverer`：

```text
CLI 参数校验
  → 构造 Fix lifecycle
  → Acquire 全局 APM 锁
  → Recover 一次
  → 同步 Warn 或 Debug
  → Release
  → 退出
```

规则：

- Acquire 失败：不调用 Recover，直接返回错误。
- 无活动 journal：记录可选 Debug，尝试 Release。
- 恢复成功：Transaction 已完成方向固定的幂等动作并删除 journal；Lifecycle 在仍持锁时同步记录恢复 Warn，再尝试 Release。
- Recover 失败：保留 journal，仍尝试 Release，不记录成功恢复 Warn。
- Release 失败：返回非零；若 Recover 也失败，使用 `errors.Join` 保留两个 cause。
- 恢复成功但 Release 失败时，已记录的恢复 Warn 是已发生事实，不撤回，也不改写为恢复失败。
- Fix 每次只调用一次 Recover，不因恢复结果再次 Inspect 或 Recover。

损坏 journal 继续由 Recover 包装 `ErrRecoveryFailed`，并保留 `ErrInvalidJournal` cause；Fix 不删除、改写或隔离该文件。

## Install 与 Uninstall

Install 与 Uninstall 的生产顺序保持不变：

```text
Acquire
  → Recover
  → 锁内同步恢复 Warn/Debug
  → Agent 与 Power 业务
  → 串行 Transaction
  → Release
  → CLI 渲染
```

两条写命令仍以 Recover 成功并删除旧 journal 作为创建新事务的前置条件。Recover 失败时不执行原命令业务，并在释放锁后返回非零。它们不改为先 Inspect 再 Recover，以避免无意义的双读窗口。

## 错误和日志所有权

- Transaction：返回结构化 Inspection/Recovery 结果和错误，不调用 `slog.Warn` 或 `slog.Error`。
- Lifecycle：同步消费 Inspection/Recovery 结果并记录 Debug/Warn；Fix 负责锁与错误合并。
- CLI：只记录最终命令 Error、打印 usage 和设置退出状态；不重复 journal Warn。
- Writer failure 只适用于 Search/List 业务 stdout；Fix 没有 renderer 或 stdout writer 路径。
- Read-only Inspect Warn 已经发生时，后续业务失败不会撤回该 Warn。

## 代码文件布局

### 新增生产文件

| 文件 | 内容 |
| --- | --- |
| `internal/cli/fix.go` | Fix Cobra 命令与零参数调用流程。 |
| `internal/cli/fix_contract.go` | `FixLifecycle` 与 factory。 |
| `internal/transaction/inspection.go` | Inspector、InspectionResult 与共享私有检查原语。 |
| `internal/lifecycle/journal_inspection.go` | Search/List 共用的无锁检查与日志 helper。 |
| `internal/lifecycle/fix.go` | Fix 锁内单次恢复生命周期。 |

### 修改生产文件

| 文件 | 内容 |
| --- | --- |
| `cmd/apm/main.go` | 装配 Fix factory；Search/List 传入只读 Inspector。 |
| `internal/cli/root.go` | CLIEntry、New 和根命令注册 Fix。 |
| `internal/transaction/manager.go` | 增加 Inspector/Recoverer 与 Manager 组合。 |
| `internal/transaction/recovery.go` | 复用共享检查原语，保留恢复编排。 |
| `internal/transaction/journal_store.go` | 明确严格读取契约。 |
| `internal/lifecycle/search.go` | 删除锁内恢复，改为无锁检查。 |
| `internal/lifecycle/list.go` | 两种 List 都先无锁检查。 |
| `internal/lifecycle/list_installed.go` | 删除锁、恢复和 Release gate。 |

重要接口、Fix 执行流程、Inspect/Recover 共享边界和无锁观测约束必须添加简要、规范的中文注释。

## 测试设计

### Transaction

- Inspect 无 journal、合法 journal、损坏 JSON、未知 schema/driver/phase 和 Store 读取错误。
- Inspect 不调用 Create、Update、Delete、manifest、Adapter 或 fileOps。
- Inspect 与 Recover 的每次公开调用只读取 Store 一次。
- fake Store 返回非法 Journal 时，Service 边界仍返回 `ErrInvalidJournal`。
- Recover 的既有 direction、actions、journal 保留/删除和 `ErrRecoveryFailed` 行为不变。

### Search 与 List

- Search、默认 List、Installed List 都先调用 Inspect，但不调用 Recover、Acquire 或 Release。
- 合法活动 journal 记录一次 recovery-required Warn 和 Fix hint，并继续业务。
- 损坏或不可读 journal 记录一次 damaged Warn、无 Fix hint，并继续业务。
- 无 journal 时普通模式无额外输出，verbose 模式有 Debug。
- 默认 List 与 Search 的 Cloud error、筛选、排序和 renderer 行为不变。
- Installed List 的具体 Agent fatal、all partial、空快照、排序和输出行为保持无锁条件下的既定规则。
- 使用临时真实 journal/manifest 验证只读命令不会推进、删除或改写 journal。

### Fix

- CLI `ExactArgs(0)`、未知 flag、factory error、nil/typed-nil service 和统一 usage/Error。
- Lifecycle 顺序严格为 Acquire → Recover → Warn/Debug → Release。
- 覆盖无 journal、恢复成功、Acquire 失败、Recover 失败、Release 失败、Recover 与 Release 双失败。
- 恢复失败保留 journal；成功只调用一次 Recover。
- stdout 始终为空。

### 集成与隔离

- 原本依赖 Search 自动恢复的 MCP/Plugin 集成用例改为：Search/List 只警告且 journal 原样保留，Fix 才执行恢复。
- 所有 APM home、Cache、Agent home 和 CLI 路径继续使用 `t.TempDir()`；MCP/Plugin 测试只允许临时 fake-bin，不调用真实 Codex、Claude Code 或 Hermes CLI。
- 静态扫描保证 Search/List 生产代码不调用 Recover 或全局锁；Install/Uninstall/Fix 的 Recover 仍在锁内。

最终验证至少包括：

```text
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./cmd/apm
git diff --check
```

## 文档同步

- 修订 ADR-003，明确只读无锁检查、Fix 独占恢复和非一致快照风险。
- 修订 ADR-004 的 Installed List Agent 快照锁语义。
- 修订 ADR-009 的 Manager Inspect/Recover 边界和命令选择语义。
- 为 Feature 007、008、009 的 spec 与 overview 增加本 feature 的 supersede 说明；不重写其历史 plan。
- 更新 Project Overview、BUILD 指南和当前 C4 模型。
- C4 新增只读 journal inspection 与 Fix 流程，更新 CLI Entry、Core Lifecycle、Lock Manager、Search、List 和公共恢复图。
- 实现、测试和文档全部完成后，按一次用户请求向 `docs/Changelog.md` 添加一条记录；设计文档提交不提前记录。

## 验收条件

1. `apm fix` 无参数执行一次锁内 Recover；无 journal 静默成功，恢复成功输出 Warn，失败保留 journal 并非零退出。
2. Search、默认 List、Installed List 均调用 Inspect，但生产调用路径没有全局锁和 Recover。
3. 合法活动 journal 只读命令警告并提示 `apm fix`；损坏或不可读 journal 警告损坏且继续查询。
4. Inspect 与 Recover 对外语义分离，内部一次读取与统一校验复用，不形成公开 Inspect 后再次读取的窗口。
5. Install/Uninstall 的锁内 Recover、事务、批处理和输出语义无回归。
6. Installed List 明确允许跨 Agent、manifest 与目标状态的无锁瞬时不一致，且 ADR-003、Project Overview 和 C4 表述一致。
7. Fix 不提供强制删除、跳过校验、选择方向或多 journal 恢复能力。
8. 全量格式、测试、race、vet、构建和 diff 检查通过，隔离测试不访问真实 Agent 环境。

# Lifecycle Install 规格

> **当前状态说明（2026-08-12）：** 本 spec 的外层锁、批处理与 Skill Cache 流程继续有效；Feature 013 在同一 lifecycle 中新增不使用 Cache/Staging 的 MCP CLI 分支，参见 [Feature 013](../013-agent-mcp-adapters/spec.md) 与 [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)。
>
> **取代说明（2026-08-26）：** 本 spec 的“陈旧清单项修复”规则——不同版本安装遇到缺失目标时 Remove 对应清单项并返回 `transaction.ErrStaleManifest`、要求用户再次执行——已被 [ADR-022](../../adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 与 [Feature 025](../025-unified-lifecycle-state-semantics/spec.md) 定向取代：现在不同版本且目标缺失/不完整时记录一次 `installed power target is missing` warning，先 repair 清理残留并删除旧清单，再以首次安装语义安装新版本，成功只输出 `installed`，不再返回 `ErrStaleManifest`。

## 目标

完成 `install` 的 lifecycle 与 CLI 端到端接线。Install 在同一次全局 APM 锁内完成 transaction 恢复、Agent 快照、Cloud 解析、Cache staging 物化及全部目标 Agent 的串行安装；只有成功释放锁后，才把结构化结果交给 CLI 渲染。

本 feature 复用既有 Adapter 发布和集中 transaction 能力，不建立跨 Search、List、Install 的通用 runner。Uninstall 在本 feature 当时继续保持 parse-only，该历史边界已由 Feature 011 supersede。

> **实现状态（2026-08-11）：** 本 spec 的已批准要求已实现；本状态记录不改变下文的需求、边界或验收标准。当时保持 parse-only 的 Uninstall 已由 Feature 011 于 2026-08-12 接线。

CLI 语法保持：

```text
apm install <power>[=<version>] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

## 范围与非目标

### 包含

- Install 命令专属 lifecycle 端口、factory、service、结构化结果和 renderer；
- Agent、Power 名称、类型和显式版本的参数契约；
- Cloud 同名类别推断、显式版本选择和缺省最新版选择；
- 一次锁内的恢复、实时 Warn、Agent 刷新、Cloud/Cache 和串行安装；
- 首次安装、升级、显式降级、同版本幂等和 `all` fail-fast；
- 旧清单存在但目标缺失时的单条清单项修复；
- Cache Fetch 返回公共 staging root 的接口收敛；
- CLI 精确结果行、writer error、统一 Error/usage 和退出状态；
- ADR、C4、项目概览、构建指南、相关 feature 文档和实现后的 Changelog 同步。

### 不包含

- Uninstall lifecycle 接线、Adapter 卸载或 transaction 卸载调整；
- Plugin/MCP Adapter 或其真实安装内容；
- 通用 lifecycle runner、跨命令持锁模板或共享批处理框架；
- 多 Agent 并行、失败后继续或跨 Agent 原子回滚；
- Power 依赖解析、兼容版本求解、校验和或来源信任策略；
- 真实 HTTP Cloud；
- 非法 Cloud 类别、名称或版本的验证、跳过、降级或错误映射；
- 普遍清单迁移、清洗或丢弃；
- JSON、表格、分页、标题、颜色、进度条或交互确认。

## CLI 参数契约

Install 使用 Cobra `ExactArgs(1)`。参数校验在构造 lifecycle 和执行任何业务前完成。

### Agent

- `--agent` 必填；缺失、空字符串或只含空白返回 `--agent is required`。
- 非空 Agent ID 保留原始值，按区分大小写的精确字符串比较；不 trim、不做 Unicode case folding、不解析别名。
- 字面量 `all` 也只按精确值识别。
- 具体 Agent 必须存在于当前命令刷新得到的已安装快照。
- `--agent=all` 使用快照中的全部 Agent；空快照返回“没有已支持且已安装的 Agent”错误，而不是成功空结果。

### Power 名称与类别

- `<power>` 去除首尾空白后不得为空；非空名称保留原值。
- 名称只与 `PowerRef.Name` 做区分大小写的精确匹配；不匹配子串、描述、标签或文件内容。
- `--type` 为空时允许类别推断；非空时只接受规范小写 `skill`、`plugin` 或 `mcp`，并与 `PowerRef.Type` 精确比较。
- 显式 Type 没有候选时直接 not found，不回退到其他类别。

### 显式版本

- 只接受精确 `vMAJOR.MINOR.PATCH`。
- 三个数值分量允许 `0`，其他值不得有前导零。
- 拒绝缺少 `v`、分量不足或过多、空白、正负号、prerelease 和 build metadata。
- 不接受 `1.2.3` 并自动补 `v`。
- 显式版本在类别确定后与 Cloud `PowerRef.Version` 精确比较。

参数错误由现有 CLI 单一错误出口记录一次 Error、打印 Cobra usage 并返回非零状态，不创建 lifecycle。

## CLI 拥有的 Install 契约

Install 使用命令专属端口：

```go
type InstallLifecycle interface {
    Install(context.Context, Flags) (InstallResult, error)
}

type InstallOutcome string

const (
    InstallOutcomeInstalled   InstallOutcome = "installed"
    InstallOutcomeUpgraded    InstallOutcome = "upgraded"
    InstallOutcomeDowngraded  InstallOutcome = "downgraded"
    InstallOutcomeUnchanged   InstallOutcome = "unchanged"
    InstallOutcomeFailed      InstallOutcome = "failed"
    InstallOutcomeUnexecuted  InstallOutcome = "unexecuted"
)

type InstallItem struct {
    Agent   domain.AgentID
    Power   domain.PowerRef
    Outcome InstallOutcome
}

type InstallResult struct {
    Items []InstallItem
}

type InstallBatchError struct {
    Agent domain.AgentID
    Err   error
}

type InstallLifecycleFactory func() (InstallLifecycle, error)
```

`InstallResult.Items` 在所有可渲染返回中均为非 nil，并按目标 Agent ID 字典序排列。每个 item 都携带同一个已解析目标 `PowerRef`；`failed` 和 `unexecuted` 同样显示目标版本。

`InstallBatchError` 是轻量可渲染 marker。它只在目标 Power 已确定、逐 Agent 批处理已结束且 Release 成功时构造；不表示错误已经记录。CLI 仍是唯一 Error 记录者。

不复用 `domain.Result`：Install outcome 比通用 completed/failed 状态更具体，而且结果格式属于 CLI 与用例间的契约。Uninstall 的占位类型不在本 feature 清理。

所有重要端口、结果、outcome、marker、factory、构造函数和执行方法添加简要、规范的中文注释，说明用途和必要约束。

## Lifecycle 组件边界

Install service 依赖：

```go
type InstallService struct {
    locker       Locker
    transactions TransactionManager
    cloud        CloudClient
    cache        PowerCache
    registry     AdapterRegistry
    config       ConfigRepository
    versions     VersionSelector
}
```

构造函数拒绝 nil 和 typed-nil 依赖。Install 独立实现自己的恢复、释放和批处理流程，不复用 Search/List 的控制流 helper。

职责划分：

- CLI：参数语法、factory 创建、结果渲染和统一错误出口。
- Lifecycle：Power 解析、Agent 目标集、Config `Find`、Cache/Get/Fetch、串行调度和 outcome 分类。
- Version Selector：只在已确定名称和类别的版本候选中选择显式版本或最大版本。
- Cache：从版本化缓存物化公共 staging，并返回 staging root。
- Adapter：根据 `PowerRef` 从 staging root 定位叶子内容，完成适配和原子发布。
- TransactionManager：目标状态、journal、backup、Adapter 调用、清单写入、清理和跨命令恢复。
- Config Repository：保持无状态；Lifecycle 调用 `Find`，TransactionManager 调用 `Set/Remove`。

默认工厂为 lifecycle 的 Config 读取和 TransactionManager 的清单写入传入同一个 Repository 实例。Search、List 和 Install factory 仍相互独立，不创建共享应用容器。

## Cache Fetch 契约

统一接口为：

```go
type PowerCache interface {
    Fetch(domain.PowerRef) (stagingRoot string, err error)
}
```

删除 `cache.Content`。成功返回规范公共 staging 根，例如 `$APM_HOME/.apm/staging`，而不是 `staging/skills/<name>` 叶子目录。Adapter 已接收 `PowerRef`，由 Adapter 自行定位 `staging/<type>/<name>`。

每次 Fetch 继续清理旧 staging 和临时 staging，从版本化缓存复制目标内容，并原子发布新的公共 staging。Cache miss 仍清理 staging 后返回 `cache.ErrMiss`。

Lifecycle 的固定获取流程为：

```text
Fetch(ref)
  ├─ success → 使用返回的 staging root
  ├─ ErrMiss → Cloud.Get(ref) → Fetch(ref) 一次
  └─ other error → 当前 Agent 失败
```

第二次 Fetch 的任何错误直接失败，不继续重试。Cloud Get 写入的完整版本缓存保留；Adapter 或 Transaction 只清理 staging。

每个实际需要安装的 Agent 都重新 Fetch。不能跨 Agent 复用 staging，因为 Adapter 可以原地适配并在退出时清理整个公共 staging。

## Version Selector

Lifecycle 使用窄接口：

```go
type VersionSelector interface {
    Select(candidates []string, requested string) (string, error)
}
```

`internal/version.Selector` 的语义：

- `requested != ""`：按原始字符串精确查找并返回；不存在时报错。
- `requested == ""`：按三段 SemVer 数值选择最大值。
- 空候选时报错。
- 不修改候选切片。

Cloud `List()` 的每个引用被视为带规范小写类别、规范名称和合法 `vMAJOR.MINOR.PATCH`，且没有 prerelease/build metadata。Selector 和 Lifecycle 信任该契约，不重新验证候选，也不为非法 Cloud 数据定义跳过、降级排序或容错行为。

## 类别与版本解析

类别推断和版本选择严格分为两个阶段。

### 未提供 Type

1. 按原始名称精确收集所有同名 Cloud 引用。
2. 没有同名引用时 not found。
3. 提取不同 `PowerRef.Type`。
4. 只有一个类别时自动推断该类别。
5. 超过一个类别时立即报歧义并要求 `--type`。

显式版本不能消除类别歧义。例如 Cloud 同时存在 `skill/demo` 与 `plugin/demo` 时，`demo=v1.0.0` 仍然歧义，即使当前只有 Skill 发布了 `v1.0.0`。用户必须显式提供 `--type=skill`。这避免同一脚本因另一类别后来发布相同版本而改变语义。

### 已提供 Type

只保留 Name 和 Type 都精确相等的候选。没有候选时 not found，不检查其他类别。

### 类别确定后

把该类别和名称的版本传给 Version Selector。显式版本精确选择；未指定版本选择 SemVer 最大值。最终形成版本已确定的唯一 `PowerRef`。

解析不修改 Cloud 返回切片或其中的引用。

## 锁、恢复和预执行顺序

Install 的固定顺序是：

```text
CLI 解析和校验
  → 构造 Install lifecycle
  → Acquire
  → TransactionManager.Recover(ctx)
  → 锁内同步记录恢复 Warn 或无 journal Debug
  → AdapterRegistry.RefreshInstalled()
  → 解析具体 Agent 或 all 目标集
  → CloudClient.List()
  → 解析唯一 Type 和目标版本
  → 按 Agent ID 升序串行执行
  → Release
  → CLI 渲染
```

锁覆盖 Cloud List、Cloud Get、Cache Fetch 和全部 Agent 安装。这会延长锁持有时间，但保证同一次命令的 Agent 快照、公共 staging、缓存写入、Config 和唯一 journal 不被另一个 APM 进程交错使用。

恢复规则：

- Recover 只能在 Acquire 成功后调用。
- 恢复成功时，在仍持锁且进入 Refresh 前实时提交一条 `slog.Warn`。
- Warn 至少包含 operation、agent、power、version、原 phase、direction 和 actions。
- 无活动 journal 可写命令专属 Debug；普通模式不产生输出。
- Recover 失败立即停止原命令，但仍尝试 Release。
- 恢复日志不因后续业务失败而撤销或降级；它是已完成恢复的实时审计记录。

目标集规则：

- 具体 Agent 不在快照中是预执行 fatal error。
- `all` 使用完整快照；空快照是预执行 fatal error。
- 快照由 Registry 按 Agent ID 原始字符串字典序返回，Lifecycle 保持该顺序。
- Agent 校验在 Cloud List 前完成；无有效目标时不访问 Cloud。

## 单 Agent 安装流程

对每个目标 Agent 顺序执行：

1. `Registry.Resolve(agent, target.Type)`；没有 Adapter 时当前 Agent 失败。
2. `Config.Find(agent, target.Type, target.Name)`；完整清单非法或读取失败时当前 Agent 失败。
3. 若存在旧引用且 `Previous.Version == target.Version`，记为 `unchanged` 并继续下一个 Agent。
4. 同版本分支只信任清单，不调用 Adapter 目标检查，不 Fetch，也不调用 TransactionManager。
5. 首次安装或版本不同时，按 Cache Fetch 契约物化当前 Agent 独占的 staging。
6. 使用返回的 staging root、已解析 Adapter 和可选 Previous 调用 `TransactionManager.Install`。
7. 成功且 Previous 为空时记为 `installed`。
8. 成功且 Previous 非空时比较规范 SemVer：目标更大为 `upgraded`，目标更小为 `downgraded`。

任何 Agent 失败时立即停止：当前 Agent 添加 `failed` item，所有剩余 Agent 按原顺序添加 `unexecuted` item。已完成的 item 保持原 outcome，不回滚其他 Agent 的成功事务。

## 陈旧清单项修复

本 feature 已以陈旧清单项 Remove 并中止的规则 supersede ADR-009 的旧 Install 自愈语义。

Lifecycle 对同版本只信任清单，因此不会发现或修复缺失目标。只有 Previous 与目标版本不同时才 Fetch 并调用 TransactionManager。

`TransactionManager.Install` 在创建 journal 前检查目标：

- `Previous == nil` 且目标存在：保持既有清单外冲突错误，不覆盖目标。
- `Previous != nil` 且目标存在：继续正常升级或降级事务。
- `Previous != nil` 且目标缺失：不得直接安装新版本。

最后一种情况固定执行：

1. 不创建 journal，不调用 Adapter，不安装目标版本。
2. 调用 `ManifestWriter.Remove(agent, previous.Type, previous.Name)`，只删除对应 Power 清单项；保留清单文件和所有其他条目。
3. 安全清理本次 Fetch 产生的规范 staging；保留版本化缓存。
4. 返回包装 `transaction.ErrStaleManifest` 的错误；该导出 sentinel 表示清单引用的受管目标已经缺失，可由 `errors.Is` 识别。
5. 错误文本只有在 Remove 成功时才说明对应清单项已删除；Remove 失败时明确说明修复失败。
6. Remove 与 staging 清理都必须尝试；同时失败时通过 `errors.Join` 保留全部 cause。

该修复在 Fetch 后发生，这是已接受的取舍：可能先进行 Cache Get/Fetch，但目标缺失的权威判断仍集中在 TransactionManager。修复成功后本地状态为“无目标、无对应清单项”；本次命令仍失败并中止，不自动开始第二次首次安装。

Lifecycle 把该 Agent 标为 `failed`，其余 Agent 标为 `unexecuted`。CLI 不额外写 Warn；唯一 Error 明确呈现目标缺失和清单项修复结果。

## 结果与精确渲染

Renderer 对每个 item 精确写入：

```text
<agent>/<type>/<name>@<target-version> <outcome>\n
```

示例：

```text
claude-code/skill/demo@v2.0.0 upgraded
codex/skill/demo@v2.0.0 failed
hermes-agent/skill/demo@v2.0.0 unexecuted
```

不输出标题、分组、说明、颜色或额外空行。恢复和诊断日志只写 stderr，业务结果只写 stdout。

成功命令渲染全部 item 后返回 nil。无可执行目标不会产生成功空结果，因为 `all` 空快照是错误。

## 错误分类与 Release

### 预执行 fatal error

包括：

- Acquire 或 Recover 失败；
- Agent 刷新失败；
- 具体 Agent 不在快照或 `all` 快照为空；
- Cloud List 失败；
- 名称不存在、类别歧义、显式 Type 无候选；
- Version Selector 失败。

这些错误不返回可渲染结果，不输出 `failed/unexecuted` 行。

### 批处理错误

目标 `PowerRef` 已确定后，Adapter Resolve、Config Find、Cache Fetch、Cloud Get、第二次 Fetch、Transaction Install 或陈旧清单修复失败均属于批处理错误。Lifecycle 在内存中补齐完整结果，但只有 Release 成功后才构造并返回 `InstallBatchError`。

具体 Agent 模式和 `all` 使用相同规则；具体 Agent 失败同样返回一条 `failed` item 和 marker。

### Release 规则

- Acquire 成功后每条正常返回路径恰好尝试一次 Release。
- 业务错误和 Release 错误同时出现时使用 `errors.Join` 保留全部 cause。
- Release 失败时返回零结果，并且不得包含可被 CLI 识别为可渲染的 `InstallBatchError` marker。
- 即使部分 Agent 实际已完成，Release 失败仍抑制全部业务 stdout。
- Transaction 提交点后错误可能保留活动 journal；Lifecycle fail-fast、Release 并返回，下一条需要共享状态的命令负责恢复。

构造 marker 必须发生在 Release 成功之后，避免 `errors.Join` 意外保留 marker 而导致 CLI 在 Release 失败时渲染。

## CLI 错误出口和 writer failure

Install RunE 的行为：

- lifecycle 成功：渲染结果，返回 renderer 错误或 nil。
- 返回 `InstallBatchError`：先渲染结果，再返回 batch error。
- 返回其他错误：不渲染，直接返回。
- batch error 与 renderer error 同时存在：`errors.Join` 两者。

Writer 失败时保留已经写入的前缀，不尝试回滚 stdout。所有返回错误最终只由 `CLIEntry.Execute` 写一条 `slog.Error`、打印一次 Cobra usage 并返回非零状态。

Lifecycle、Version Selector、Cache、Cloud、Adapter、Transaction 和 renderer 不调用 `slog.Error`，不直接打印业务结果，也不使用“已实时记录错误”marker。恢复成功 Warn 是审计事件，不是错误重复记录。

Install factory 在 CLI 预解析 `--verbose` 并安装默认 logger 后创建。nil factory、typed-nil service 和构造错误均进入同一错误出口。每次 `CLIEntry.Execute` 创建新命令和新 lifecycle，不能泄漏 Agent、Type、Name、Version、logger 或结果状态。

## 测试设计

### CLI 参数、端口与 renderer

覆盖：

- 缺失、空白 Agent；合法具体 Agent 和 `all`；
- 非法 Type、额外位置参数、未知 flag；
- 合法 `v0.0.0`、`v1.2.3` 和多位分量；
- 缺少 `v`、前导零、分量错误、prerelease、build metadata、首尾空白和非数字版本；
- Name 保留原值且只以 trim 结果判断空；
- factory 在日志初始化后创建，nil/typed-nil 与构造错误；
- repeated Execute 不泄漏状态；
- 精确六种 outcome 行；
- success、batch、fatal、Release 和 writer error 的渲染边界；
- batch 与 writer error 同时保留全部 cause；
- Search/List 不回归，Uninstall parse-only 是本 feature 的历史验收边界，现已由 Feature 011 supersede。

### Power 解析与 Version Selector

覆盖：

- Name 区分大小写精确匹配；
- 无候选；
- 唯一类别自动推断；
- 跨类别重名在版本选择前报歧义；
- 显式版本不能消除类别歧义；
- 显式 Type 精确过滤且不回退；
- 显式版本精确选择；
- `v1.9.0` 与 `v1.10.0` 的数值最大值；
- 空候选和版本不存在；
- 不修改 Cloud 引用或候选版本切片。

不添加非法 Cloud Type、Name、Version、prerelease 或 build metadata 测试。

### Cache 和 Adapter

更新全部 Cache contract 测试：

- Fetch success 返回公共 staging root；
- 叶子内容仍位于 `staging/skills/<name>`；
- miss 和所有失败保持旧 staging 清理语义；
- Adapter 接收 root 和 PowerRef 后正确定位叶子；
- 不保留或继续使用 `cache.Content.Path`。

### Transaction 陈旧清单修复

覆盖：

- Previous 非空、target 缺失时 Remove 精确 Agent/Type/Name；
- 保留其他类别和名称的清单条目；
- 清理 staging但保留缓存；
- 不创建或更新 journal，不调用 Adapter；
- 返回包装 `transaction.ErrStaleManifest` 的错误且文本说明修复成功；
- Remove 失败不谎称条目已删除；
- Remove 和 staging 清理同时失败时所有 cause 可由 `errors.Is/As` 找到；
- Previous nil + target 存在仍是冲突；
- Previous 非空 + target 存在的正常升级/降级与恢复测试不回归。

### Lifecycle 顺序与失败

使用 fake 依赖记录：

```text
Acquire → Recover → Warn/Debug → Refresh → Cloud List
→ Resolve → Find → Fetch [→ Get → Fetch] → Transaction Install
→ Release
```

覆盖：

- Warn 在 Refresh、Cloud 和 Release 前已同步写入 stderr；
- 无 journal 普通模式无输出，verbose 只有 Install Debug；
- 具体 Agent 与 `all` 的目标验证和 Agent ID 顺序；
- `all` 空快照失败且不调用 Cloud；
- 同版本不检查目录、不 Fetch、不调用 transaction；
- 每个实际安装 Agent 都重新 Fetch；
- installed/upgraded/downgraded 分类；
- first failure 后不调用剩余 Agent 的依赖并补齐 unexecuted；
- 所有 Acquire/Recover/Refresh/Cloud/Resolve/Find/Fetch/Get/Install/Release 失败；
- 每个业务错误与 Release 同时失败的 cause 保留；
- Release 失败返回零结果且无 marker；
- 过滤、结果构造和 renderer 只在成功 Release 后可观察。

### 默认接线与黑盒

在隔离 `APM_HOME` 和 `XDG_CACHE_HOME` 中验证：

- bundled Cloud Power 的首次安装写入目标和 manifest；
- 省略版本选择最新版；
- 重复安装输出 `unchanged` 且不重写目标；
- 精确 stdout，普通成功 stderr 为空；
- verbose 诊断只在 stderr；
- Cache、staging、journal 和目标路径均位于预期测试边界；
- Search、Cloud/installed List 的既有黑盒行为不回归。

最终执行无缓存全量测试、race 测试、`go vet ./...`、构建、go mod tidy diff、gofmt、`git diff --check`、日志/直接打印静态扫描和 CLI 黑盒验收。

## ADR 与文档同步

本 feature 修订现有 ADR，不新建 ADR：

- [ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)：记录规范显式版本、跨类别重名强制 Type、outcome 行和错误汇总。
- [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)：记录 Install 单锁范围、锁外渲染、fail-fast 和 Release 抑制边界。
- [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)：已 supersede “旧清单但 Install target 缺失时直接自愈安装”，改为“Remove 陈旧清单项并中止”。

同步更新：

- CLI Entry、Core Lifecycle、Install lifecycle、Cache Manager、Adapter Registry 及组件 DSL；
- `ivory-tower/project-overview.md`；
- `BUILD.md`；
- Feature 003 的 Cache 返回语义；
- Feature 006 的 Adapter staging 定位说明；
- Feature 007 的 Transaction 缺失目标规则；
- Feature 009 中 Install 仍 parse-only 的历史边界说明；
- 实现完成后的 `docs/Changelog.md` 单条用户请求记录。

Spec 和后续 implementation plan 均保存在 `ivory-tower/features/010-lifecycle-install/`。所有相关文档引用既有 ADR ID，避免创建重复决策来源。

## 验收标准

1. Install CLI 只接受规范显式 `vMAJOR.MINOR.PATCH`，名称精确匹配，跨类别重名必须显式 Type。
2. 缺省版本在唯一类别内选择 SemVer 最大值，Cloud 候选不被重新验证或修改。
3. Install 从 Acquire 到全部 Agent 业务结束只持有一次全局锁，恢复 Warn 在锁内实时输出，结果只在成功 Release 后渲染。
4. 具体 Agent 必须在快照中，`all` 空快照失败；非空目标按 Agent ID 升序串行且 fail-fast。
5. 同版本只信任清单并输出 unchanged，不检查目录、不 Fetch、不创建 transaction。
6. 每个实际安装 Agent 重新 Fetch；Cache miss 只执行一次 Get 和一次二次 Fetch；Fetch 返回公共 staging root。
7. 首次安装、升级和显式降级分别输出 installed、upgraded、downgraded。
8. 不同旧版本但目标缺失时，Transaction 只 Remove 对应清单项、清理 staging并返回错误；不创建 journal、不安装目标版本。
9. 成功 Release 后，批处理失败精确输出 failed/unexecuted 汇总，再由 CLI 记录一次 Error 和 usage；fatal 或 Release error 不输出业务行。
10. Uninstall 保持 parse-only 是本 feature 的历史验收边界，已由 Feature 011 supersede；Search/List 既有锁、结果和 CLI 行为不回归。

# Lifecycle List 规格

> **Feature 016 supersession（2026-08-14）：** 本文记录 Feature 009 当时 installed List 锁内 Recover/Refresh/Config/Release 的设计，该边界已经 supersede。当前默认 List 与 installed List 都先无锁 `Inspect` journal：合法活动 journal Warn 并提示 `run apm fix`，损坏或不可读 journal Warn 后继续；两种模式均不获取锁、不 Recover。installed List 随后无锁 Refresh 并逐 Agent 读取 manifest，结果是 best-effort、可能跨 Agent 混合时间点的观察，不存在 Release gate。当前规格见 [Feature 016](../016-lifecycle-fix/spec.md) 与 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)；下文相关锁内流程与验收仅代表历史 Feature 009 基线。

## 目标

完成 `list` 的 lifecycle 与 CLI 接线，并明确 Cloud 列表与本地已安装列表的不同共享状态边界：默认 Cloud 列表不获取全局锁、不执行 transaction 恢复；`list --installed` 在全局锁内完成恢复、Agent 刷新和清单读取，释放锁后再过滤、排序和渲染。

本 feature 同时把 List 从聚合的 lifecycle 占位接口拆为命令专属端口，清除 Search service 上未实现但静默成功的 List/Install/Uninstall 方法。Install 和 Uninstall CLI 保持 parse-only、不在本 feature 接线，是本 feature 的历史边界；Feature 010/011 已分别实现 Install/Uninstall。

List CLI 语法修订为：

```text
apm list [--type=<skill|plugin|mcp>]
apm list --installed --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

默认 Cloud 模式不需要 Agent。由于同一个 Cobra `list` 命令仍需为 installed 模式注册 `--agent`，默认模式显式提供该 flag 时返回 `--agent requires --installed` 参数组合错误，而不是 unknown flag。

## 范围与非目标

### 包含

- List 命令专属 lifecycle 接口、结构化结果、部分失败和依赖注入；
- 默认 Cloud 列表的全版本查询、精确类型过滤和确定性排序；
- installed 列表的锁内 transaction 恢复、恢复诊断、Agent 刷新和 Config 读取；
- 具体 Agent、`all`、空快照与逐 Agent 部分失败语义；
- CLI 精确输出、writer error、统一错误日志、usage 和退出状态；
- Adapter Registry 按 Agent ID 字典序刷新并返回快照；
- Config Repository 对规范 `vMAJOR.MINOR.PATCH` 的完整读写校验；
- 删除未使用的聚合 lifecycle 接口和 Search service 的空占位业务方法；
- ADR、C4、项目概览、构建指南、feature 文档和 Changelog 同步。

### 不包含

- install/uninstall lifecycle 接线或业务实现（Install/Uninstall 未接线是本 feature 的历史边界；Feature 010/011 已分别实现 Install/Uninstall）；
- Adapter 卸载能力、Power 安装或 transaction 写流程调整；
- 通用命令 runner、跨命令回调流水线或共享持锁模板；
- Cloud 非法类别、名称或版本的校验、跳过、降级排序或错误映射；
- 本地非法清单的自动迁移、清洗、跳过或修复；
- 同名版本折叠、最新版选择、关键字匹配、相关性排序或文件内容搜索；
- JSON、表格、分页、标题分组、颜色或其他输出格式；
- 真实 HTTP Cloud。

## CLI 参数契约

List 不接受位置参数，由 Cobra 使用 `ExactArgs(0)` 拒绝多余参数。参数校验发生在构造 lifecycle 和执行任何业务之前。

### 默认 Cloud 模式

```text
apm list [--type=<skill|plugin|mcp>]
```

- `--installed` 为 false 时不得提供 `--agent`；非空值返回 `--agent requires --installed`。
- `--agent` 即使显式传为空字符串也不产生目标 Agent；CLI 依据 Cobra 的 flag changed 状态拒绝默认模式中显式提供的该 flag，避免 `--agent=` 被静默接受。
- `--type` 为空时不应用类型过滤；非空时只允许规范小写值 `skill`、`plugin` 或 `mcp`。

### Installed 模式

```text
apm list --installed --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

- `--agent` 必填；缺失、空字符串或仅含空白返回 `--agent is required`。
- 非空 Agent ID 按原始值精确比较，不做 trim、Unicode case folding、别名解析或大小写归一化。字面量 `all` 也要求精确匹配。
- `--type` 使用与 Cloud 模式相同的规范值校验和精确过滤。
- 具体 Agent 必须存在于当前命令刷新得到的“已支持且已安装 Agent”快照，否则返回包含该 Agent 的“未受支持或未安装”错误。
- `--agent=all` 以快照中的全部 Agent 为目标；空快照返回成功的空列表。

非法参数由现有 CLI 单一错误出口记录一次 Error、打印 Cobra usage 并返回非零状态。Help 中 `list` 展示 `--agent`、`--installed` 和 `--type`，并明确 `--agent` 只与 `--installed` 一起使用。

## 架构边界

List 使用 CLI 拥有、lifecycle 实现的命令专属端口：

```go
type ListLifecycle interface {
    List(context.Context, Flags) (ListResult, error)
}

type ListedPower struct {
    Agent domain.AgentID
    Power domain.PowerRef
}

type ListResult struct {
    Powers []ListedPower
}

type AgentFailure struct {
    Agent domain.AgentID
    Err   error
}

type PartialListError struct {
    Failures []AgentFailure
}

type ListLifecycleFactory func() (ListLifecycle, error)
```

Cloud 结果的 `ListedPower.Agent` 为空；installed 结果的 Agent 必须非空。成功时 `ListResult.Powers` 始终为非 nil 切片，无结果时为空切片。

`PartialListError` 只表示 `list --installed --agent=all` 已完成全部目标读取尝试、成功释放锁，但至少一个 Agent 读取失败。它是唯一允许 CLI 在 lifecycle 返回 error 时仍渲染同次调用结果的轻量标记；该标记不表示错误已经记录。所有错误仍只由 CLI 统一记录。

`internal/lifecycle` 提供独立的 List service。其依赖为：

```go
type listService struct {
    locker       Locker
    transactions TransactionManager
    cloud        CloudClient
    registry     AdapterRegistry
    config       ConfigRepository
}
```

依赖通过 List 专属构造函数注入；nil 依赖在构造时返回明确错误。默认 Cloud 分支虽然由同一 service 执行，但不得调用 locker、transactions、registry 或 config。

现有 Search service 保持自身行为并使用明确的 Search 专属构造入口。删除未被生产代码消费的聚合 `lifecycle.Lifecycle` 接口，以及 Search service 上静默成功的 List/Install/Uninstall 空实现。Install/Uninstall 的 Cobra 命令仍只解析和校验，不引入新的 lifecycle 端口或工厂。

`CLIEntry` 分别注入 Search 与 List 工厂。工厂在 Cobra 预解析 `--verbose` 并安装默认 slog logger 后调用，使构造错误进入现有统一错误出口。`cmd/apm` 分别构造两个命令的默认依赖；不创建共享应用容器、通用 runner 或跨命令控制流。

重要构造、端口、结果、部分失败和执行接口添加简要、规范的中文注释，说明用途、可渲染错误条件和锁边界。

## 默认 Cloud List 流程

默认模式的顺序固定为：

```text
CLI 解析和校验参数
  → 构造 List lifecycle
  → CloudClient.List()
  → 按可选 Type 精确过滤
  → 按 Type、Name、Version 排序
  → 返回 ListResult
  → CLI 渲染 stdout
```

默认模式不获取全局 APM 锁，不调用 `TransactionManager.Recover`，不刷新 Agent，不读取 Config。即使存在上一次命令留下的活动 journal，Cloud List 也不观察或修复它；该命令不访问 APM 共享状态，因此可独立完成。Cloud 查询失败时返回带 List 阶段上下文并保留底层 cause 的错误，不返回可渲染结果。

Cloud `List()` 继续遵守 Feature 008 使用的上游契约：每个 `PowerRef` 都带规范小写 Type、规范 Name 和合法的 `vMAJOR.MINOR.PATCH`，不存在 prerelease 或 build metadata。List 信任该契约，不重新验证或为非法 Cloud 数据定义行为。

`--type` 非空时要求 `PowerRef.Type` 与指定规范值精确相等。List 不按 Name 合并，不选择最新版；同一个 `(Type, Name)` 的全部版本都保留。

排序规则为：

1. `PowerRef.Type` 原始字符串字典序升序；
2. Type 相同时按 `PowerRef.Name` 原始字符串字典序升序；
3. Type 和 Name 都相同时按 SemVer 数值降序；
4. 三个字段完全相等时使用稳定排序保留 Cloud 输入相对顺序。

过滤和排序不修改 Cloud 返回的原始切片或 `PowerRef` 字段。

## Installed List 流程与锁范围

Installed 模式的顺序固定为：

```text
CLI 解析和校验参数
  → 构造 List lifecycle
  → 获取全局 APM 锁
  → TransactionManager.Recover(ctx)
  → 锁内记录恢复成功 Warn 或无 journal Debug
  → AdapterRegistry.RefreshInstalled()
  → 解析具体 Agent 或 all 目标集合
  → 按 Agent ID 升序调用 ConfigRepository.List(agent)
  → 尝试释放全局 APM 锁
  → 锁外按可选 Type 过滤并排序
  → 返回 ListResult 或 ListResult + PartialListError
  → CLI 渲染 stdout
```

以下顺序是可测试的不变量：

1. `Recover`、Agent 刷新和 Config 读取只能在成功获取锁后调用。
2. 恢复成功 Warn 必须在 `Recover` 返回后、Agent 刷新前同步提交给默认 slog Handler，且此时仍持有锁。
3. 一旦 Acquire 成功，每条正常 error 返回路径都必须尝试一次 Release。
4. 恢复、刷新、具体 Agent 校验或 Config 读取出现 fatal error 时，先尝试 Release，再向 CLI 返回。
5. `--agent=all` 的单 Agent Config 失败只记录到内存并继续下一个 Agent；所有读取尝试结束后再 Release。
6. 只有 Release 成功后，List 才执行结果过滤和排序，并允许 CLI 渲染。
7. Config 返回深拷贝，因此锁外结果计算不再访问 APM 共享状态。
8. CLI renderer 始终在 lifecycle 返回后执行，不处于全局锁内。

List 拥有独立恢复流程，不调用 Search 的 recovery preflight，也不让 Search 调用 List。可以复用领域类型、依赖接口别名和纯数据格式化约定，但不得抽取跨命令 runner 或耦合两条命令的执行顺序。

## Agent 刷新与目标集合

Adapter Registry 按 ADR-004 的既有要求修正为：先按 Agent ID 原始字符串字典序排列已注册 Agent 的副本，再依序调用 `DetectAgent`。成功快照只包含已支持且检测为已安装的 Agent，并保持相同升序。Registry 的内置配置存储顺序不再成为公开执行顺序。

刷新失败返回零快照和带 Agent/原因上下文的错误；List 不读取任何 Config。若此时 Release 也失败，使用 `errors.Join` 保留刷新与释放错误。

具体 Agent 模式只读取一个目标清单。目标不在快照、清单读取或解析失败均为 fatal error，不返回可渲染业务结果。

`--agent=all` 按升序快照遍历：

- 读取成功时保存该 Agent 的 Power；
- 读取失败时保存 `AgentFailure` 并继续；
- 空快照不读取 Config，Release 成功后返回空结果；
- 全部读取成功时返回普通成功结果；
- 存在读取失败且 Release 成功时，返回成功记录和 `PartialListError`；
- 存在读取失败且 Release 失败时，以不带可渲染标记的聚合 error 保留所有 Agent cause 和 Release cause，stdout 为空。

逐 Agent失败集合和对应错误文本都按 Agent ID 升序，保证日志和测试确定性。

## 恢复日志

### 无活动 journal

`Recover` 返回 `Recovered=false` 且 error 为 nil 时继续 installed List。lifecycle 可在锁内写一条 List 专属 `slog.Debug`，例如 `no active transaction before installed list`；普通模式不得产生 stdout 或 stderr 输出。

默认 Cloud List 完全不调用 Recover，因此也不产生该 Debug。

### 恢复成功

`Recover` 返回 `Recovered=true` 时，List lifecycle 在仍持有锁时同步写恰好一条 `slog.Warn`，然后继续 Agent 刷新。Warn 至少包含：

- `operation`；
- `agent`；
- `power`；
- `version`；
- 原 `phase`；
- `direction`；
- `actions`。

字段直接来自 `transaction.RecoveryResult`，消息使用与 Search 一致的稳定语义 `transaction recovered`。Warn 已实时输出后，即使刷新、读取或释放失败，也不撤销、不重复，并且不转换为普通业务输出。

### 恢复失败

恢复失败时不刷新 Agent、不读取 Config。List lifecycle 不调用 `slog.Error`，先尝试 Release，再把错误返回 CLI；恢复和释放同时失败时使用 `errors.Join` 保留两者。

## Config Repository 规范版本保证

Config Repository 的版本契约由“非空、不验证 SemVer”收紧为精确规范格式：

```text
vMAJOR.MINOR.PATCH
```

三个数字分量遵守 SemVer 数值规则：`0` 合法，其他值不得有前导零。不得出现缺失 `v`、缺失分量、正负号、空白、首尾空格、prerelease 或 build metadata。

Repository 使用同一内部校验逻辑覆盖：

- `List`、`Find`、`Remove` 读取的完整清单；
- `Set` 输入的 `PowerRef`，并且校验发生在任何文件 I/O 前；
- 私有 write 接收的完整 `PowerGroups`，防止内部调用或测试绕过；
- 清单中每个分组的每个条目，包括与当前 Find/Remove 目标无关的条目。

读取发现非法版本时返回错误，不返回部分 PowerGroups。错误至少包含 Repository 操作、Agent、Power Type、条目索引、Power Name 和原始 Version，并包装 `config.ErrInvalidPower`，以支持 `errors.Is`。Repository 不跳过、清洗、重排、自动补 `v`、覆盖或迁移非法数据，原文件保持不变。

`Set` 对非法输入返回包装 `ErrInvalidPower` 的错误且不读取或修改文件。有效 `Set` 继续保持既有同名替换、重复归并和原子写语义。名称校验、重复项策略和 JSON schema 的其他规则不因本 feature 改变。

由于 lifecycle 只从成功的 Config `List` 获取数据，installed 排序可以安全使用 SemVer。相同 Agent、Type、Name 的重复合法记录全部输出，按版本数值降序；完全相同记录使用稳定排序保留清单相对顺序。

本变更不提供旧清单迁移。已有无 `v` 版本在升级后会使相关 Repository 读取失败，用户需在本 feature 之外通过重新生成或手工修正清单。

## 结构化结果与渲染

List lifecycle 不接收 renderer、stdout、stderr 或 logger，不直接打印业务结果。

Cloud 记录由 CLI 写为：

```text
<type>/<name>@<version>\n
```

Installed 记录无论具体 Agent 还是 `all` 都写为：

```text
<agent>/<type>/<name>@<version>\n
```

示例：

```text
skill/foo-before-summary@v0.0.1
codex/skill/foo-before-summary@v0.0.1
```

无标题、空行、缩进、状态列或失败摘要业务行。空结果不写 stdout 且命令成功。

CLI 对普通成功结果逐项写入。对 `ListResult + PartialListError`，CLI 先写全部成功记录，再返回同一个 partial error 给统一错误出口。renderer 必须检查每次 writer 返回值；写入失败保留已经完整写出的前缀，不回滚、不重试。

如果渲染 partial result 时 writer 也失败，CLI 使用 `errors.Join` 保留 writer error 和 `PartialListError`，随后只记录一条 Error。任何不带 `PartialListError` 的 lifecycle error 都不得调用 renderer。

## 错误处理

CLI 沿用单一错误出口：

1. 调用一次 `slog.Error("command failed", ...)`；
2. 把 Cobra usage 写到 stderr；
3. 向 main 返回 error，最终非零退出。

Lifecycle、Registry、Config、Cloud 和 renderer 不提前记录 `slog.Error`，也不直接打印。Debug 和恢复成功 Warn 按各自契约保留。

错误必须保留底层 cause 和阶段上下文：

- List lifecycle 构造失败；
- Cloud List 失败；
- installed recovery preflight 的 Acquire、Recover 或 Release 失败；
- Agent 刷新失败；
- 具体 Agent 未受支持或未安装；
- Config 清单路径、读取、解析、Agent 一致性或版本校验失败；
- `all` 的逐 Agent失败集合；
- renderer writer 失败。

只要 Acquire 成功，业务错误与 Release 错误同时出现时使用 `errors.Join`。除成功 Release 后返回的 `PartialListError` 外，任何 lifecycle error 都不产生业务 stdout。恢复成功 Warn 可能先于后续 Error 出现在 stderr，这是已完成恢复的实时审计结果，不属于重复错误记录。

## 测试设计

### CLI 参数与端口测试

覆盖：

- 默认 `list` 和可选合法 `--type` 成功且不要求 Agent；
- 默认模式的 `--agent=all`、`--agent=` 均返回 `--agent requires --installed`、usage 和非零状态；
- installed 缺失、空或纯空白 Agent 返回 `--agent is required`；
- 非法 type、额外位置参数和 Cobra flag 解析错误；
- Help 清楚表达 flag 组合；
- List factory 在日志初始化后创建，nil factory/service 和构造错误进入统一出口；
- repeated `CLIEntry.Execute` 不泄漏 installed、agent、type、logger 或 lifecycle 状态；
- 本 feature 当时的 install/uninstall 只解析且不调用 List factory；该历史边界已由 Feature 010/011 supersede。

### Cloud List 测试

使用会在非预期调用时失败的 fake Locker、TransactionManager、Registry 和 Config，证明默认分支只调用 Cloud。

覆盖：

- Cloud 全部版本均保留；
- `--type` 精确过滤；
- Type、Name 升序和同名 SemVer 降序；
- 完全相等记录稳定；
- 输入引用不被修改；
- 空结果为非 nil 空切片；
- Cloud error 保留 cause 且没有可渲染结果；
- 默认模式在存在 fake 活动 journal 时仍不调用 Recover。

不添加非法 Cloud PowerRef、prerelease、build metadata 或无 `v` 版本测试。

### Installed 顺序与失败测试

使用 fake Locker、Unlock、TransactionManager、Registry 和 Config 记录调用顺序，覆盖：

- `Acquire → Recover → Warn → RefreshInstalled → Config.List → Release`；
- Warn 在 Refresh 和 Release 前已同步写入 stderr；
- 过滤、排序和 CLI renderer 发生在 Release 成功后；
- 无 journal 普通模式无日志、verbose 模式只有 List Debug；
- Acquire 失败不调用后续依赖；
- Recover 失败尝试 Release，不调用 Registry/Config；
- Refresh 失败尝试 Release，不调用 Config；
- 具体 Agent 不在快照中尝试 Release，不调用 Config；
- 具体 Agent Config 失败尝试 Release且无结果；
- Release 失败不返回可渲染结果；
- 各业务错误与 Release 同时失败时 cause 均可由 `errors.Is/As` 找到；
- `all` 按 Agent ID 升序读取；
- 空快照成功返回空列表；
- 单 Agent 读取失败后继续，Release 成功时返回成功记录与有序 `PartialListError`；
- partial failure 加 Release failure 保留所有 cause 但不可渲染；
- type 精确过滤、Agent/Type/Name 升序和重复版本 SemVer 降序；
- 恢复 Warn 字段完整且恰好一条。

### Registry 测试

覆盖 Registry 的内置存储顺序与 Agent ID 排序不同的场景，断言 Detect 调用顺序和成功快照均为 Agent ID 字典序。检测失败仍返回零快照，错误包含发生失败的 Agent 和原因。

### Config 测试

把现有合法 fixture 统一为带 `v` 的三段版本，并新增表驱动覆盖：

- 合法 `v0.0.0`、`v1.2.3` 和多位数字分量；
- 无 `v`、分量不足或过多、前导零、prerelease、build metadata、空值、空白、首尾空格和非数字；
- List/Find/Remove 对任意分组非法条目失败并包装 `ErrInvalidPower`；
- 错误包含 Agent、Type、索引、Name 和 Version；
- Set 在 I/O 前拒绝非法版本且不修改阻塞路径或已有文件；
- 私有 write 拒绝完整 PowerGroups 中的非法条目且不触碰目标；
- 读取失败不自动迁移或重写清单；
- 合法 Set/Remove 和原子写既有行为不回归。

### CLI 渲染与集成测试

覆盖：

- Cloud 精确输出 `<type>/<name>@<version>\n`；
- 具体 Agent 与 `all` 均精确输出 `<agent>/<type>/<name>@<version>\n`；
- 空结果 stdout 为空；
- partial failure 先输出成功记录，stderr 恰有一条 Error 和 usage，整体非零；
- fatal lifecycle error 不输出业务行；
- writer failure 保留已写前缀并只记录一条 Error；
- partial failure 与 writer failure 同时可通过聚合 error 找到；
- 恢复 Warn 只在 stderr，业务结果只在 stdout；
- 默认 `cmd/apm` factory 可列出 bundled Cloud，并在隔离 `APM_HOME` 中列出已安装清单。

最终执行无缓存全量测试、`go vet ./...`、构建、gofmt、`git diff --check`、静态日志/直接打印扫描及 CLI 黑盒验收。

## 文档与 ADR 同步

本 feature 收紧现有决策，不创建新 ADR：

- 修订 [ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)：默认 List 不接受 Agent；installed List 要求 Agent；明确条件组合错误与输出格式。
- 修订 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)：默认 Cloud List 无共享状态业务，因此不锁、不恢复；installed List 的恢复、刷新和清单读取处于同一锁区间，渲染在锁外。
- 修订 [ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)：落实 Registry 按 Agent ID 升序检测和返回快照的公共契约。
- 修订 [ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)：把版本从非空值收紧为规范 `vMAJOR.MINOR.PATCH`，明确完整读取校验和不迁移策略。

同步更新：

- Core Lifecycle、List lifecycle、Adapter Registry 和 Lock Manager C4/Mermaid 文档；
- `ivory-tower/project-overview.md`；
- `BUILD.md`；
- Feature 005 文档中的后续收紧说明，避免其历史“不验证版本”要求被误读为当前契约；
- Feature 008 文档中 List 仍为 parse-only 的历史状态说明；
- 实现完成后的 `docs/Changelog.md` 单条用户请求记录。

Spec 和后续实施计划保存在 `ivory-tower/features/009-lifecycle-list/`。文档、测试和实现必须引用上述 ADR ID，避免另建重复的锁、Agent 或版本决策来源。

## 验收标准

1. 默认 `list` 不要求 Agent，显式 `--agent` 因缺少 `--installed` 失败；installed List 强制要求 Agent。
2. 默认 Cloud List 只调用 Cloud，不获取锁、不恢复、不刷新 Agent、不访问 Config，并输出全部合法版本。
3. Installed List 在同一锁区间依次完成恢复、实时 Warn、Agent 刷新和清单读取；释放成功后才过滤、排序和渲染。
4. Registry 的检测与快照均按 Agent ID 字典序。
5. 具体 Agent、`all`、空快照和逐 Agent继续读取/部分失败语义与本规格一致。
6. Cloud 输出精确为 `<type>/<name>@<version>\n`；installed 输出精确为 `<agent>/<type>/<name>@<version>\n`。
7. Config Repository 不向调用方返回、也不写入任何非规范 `vMAJOR.MINOR.PATCH`；读取发现非法现有清单时返回错误且不自动修改文件。
8. 除可渲染 partial error 外，error 不产生业务 stdout；每次失败只由 CLI 记录一条 Error 并打印 usage。
9. Search 行为不回归；Install/Uninstall parse-only 是本 feature 的历史验收边界，已由 Feature 010/011 分别 supersede 为 Install/Uninstall 已实现；不存在聚合 lifecycle 接口或静默成功的空业务方法。
10. ADR、C4、项目概览、Feature 文档、BUILD 和 Changelog 与最终实现一致，全量测试、vet、构建和静态检查通过。

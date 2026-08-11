# Lifecycle Search 规格

## 目标

完成 `search` 的 lifecycle 与 CLI 接线。Search 在锁内恢复唯一活动 transaction，恢复成功时实时记录结构化 Warn；随后立即解锁，在锁外查询 Cloud、筛选匹配项、为每个 Power 选择最新版本并返回结构化结果。CLI 只在 lifecycle 返回后渲染业务结果。

本 feature 同时修订 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 的锁范围：锁保护恢复和命令自身访问的 APM 共享状态，不保护结果渲染；没有共享状态阶段的命令 lifecycle 不需要获取锁。Search 因执行恢复而需要锁，但 Cloud 查询与结果计算不访问 APM 共享状态，必须在解锁后执行。

Search CLI 语法修订为：

```text
apm search <keyword> [--type=<skill|plugin|mcp>]
```

`search` 不注册 `--agent`。提供该 flag 时由 Cobra 按未知 flag 处理，打印 usage 并非零退出。

## 范围与非目标

### 包含

- Search 命令专属 lifecycle 接口、结果和依赖注入；
- 锁内 transaction 恢复及恢复诊断；
- 锁外 Cloud 列表查询、名称匹配、类别过滤、最新版本选择和稳定排序；
- CLI 结果渲染、错误日志、usage 和退出状态；
- Search 的单元测试、CLI 测试和本地 Cloud 集成验证；
- ADR-002、ADR-003、项目概览、相关 C4 图和 `BUILD.md` 同步。

### 不包含

- 历史边界：本 feature 时 list/install/uninstall 的 lifecycle 接线、接口重构或业务实现不包含。该边界已先由 Feature 009 supersede 为 List 独立 lifecycle，再由 Feature 010 supersede 为 Install 已实现；只有 Uninstall 仍为 parse-only。
- 为四个命令建立通用 runner、公共回调流水线或统一持锁模板；
- Agent 发现、Adapter、Config、Cache、安装或卸载业务；
- Cloud 非法版本、非法类别或非法名称的校验、跳过、错误映射或降级排序；
- 对类别、版本、描述、标签、元数据或文件内容进行搜索；
- 模糊匹配、分词、相关性评分、分页或网络 Cloud。

## 架构边界

Search 使用命令专属端口。CLI 拥有输入和结果契约，lifecycle 提供实现：

```go
type SearchLifecycle interface {
    Search(context.Context, Flags) (SearchResult, error)
}

type SearchResult struct {
    Powers []domain.PowerRef
}
```

`SearchResult.Powers` 在成功时始终为非 nil 切片；无匹配时为空切片。接口不接收 Renderer、stdout、stderr 或日志器。CLI 通过注入的 `SearchLifecycle` 调用业务，不依赖 Locker、TransactionManager 或 CloudClient。

`internal/lifecycle` 的 Search 实现依赖：

```go
type searchService struct {
    locker       Locker
    transactions TransactionManager
    cloud        CloudClient
}
```

重要构造与 Search 接口添加简要、规范的中文注释，说明用途以及锁范围约束。依赖通过构造函数注入；nil 依赖在构造或调用入口返回明确错误，不允许因占位零值产生静默成功。

CLI 的 Search 命令只持有 Search 专属端口。本 feature 时 list/install/uninstall 均保持未接线占位；该历史状态已由 Feature 009 supersede 为 List 独立接线，再由 Feature 010 supersede 为 Install 已接线，只有 Uninstall 仍 parse-only。不得让 Search 调用其他命令 lifecycle，也不得抽象跨命令执行器。

`cmd/apm` 使用工厂构造 Search 默认依赖。工厂在 Cobra 已根据 `--verbose` 安装全局 logger 后执行，使路径或依赖初始化错误进入同一个 CLI 错误出口。默认依赖为全局文件锁、集中 transaction Manager 和开发期 Local Cloud；Local Cloud 资源根按当前构建指南从仓库根的 `internal/cloud/resources` 解析为绝对路径。

## 执行时序与锁范围

Search 的顺序固定为：

```text
CLI 解析和校验参数
  → 构造 Search lifecycle
  → 获取全局 APM 锁
  → TransactionManager.Recover(ctx)
  → 锁内记录恢复成功 Warn 或无 journal Debug
  → 释放全局 APM 锁
  → CloudClient.List()
  → 名称和类型筛选
  → 每个 Type/Name 选择最大 SemVer
  → 按 Type/Name 稳定排序
  → 返回 SearchResult
  → CLI 渲染 stdout
```

以下顺序是可测试的不变量：

1. `Recover` 只能在成功获取锁后调用。
2. 恢复成功 Warn 必须在 `Recover` 返回后、`Release` 和 Cloud 调用前同步提交给默认 slog Handler。
3. 无论 `Recover` 成功或失败，只要获取过锁就必须尝试 `Release`。
4. Cloud `List()` 只能在 `Release` 成功后调用。
5. 获取锁、恢复或释放失败时不得调用 Cloud。
6. CLI renderer 只能在 Search 成功返回后运行，因此不处于生命周期锁内。

Search 不把 Cloud 查询留在锁内。其他命令未来应按自己的共享状态读写决定锁内业务边界；本 feature 不为其实现或复用 Search 流程。

## 恢复日志

### 无活动 journal

`Recover` 返回 `Recovered=false` 且 error 为 nil 时继续 Search。lifecycle 可在锁内写一条 `slog.Debug` 诊断，但普通模式不得产生 stdout 或 stderr 输出。Debug 不得伪装成恢复成功。

### 恢复成功

`Recover` 返回 `Recovered=true` 时，lifecycle 在仍持有锁时同步写恰好一条 `slog.Warn` 到 stderr，然后才尝试解锁并继续。Warn 至少包含：

- `operation`；
- `agent`；
- `power`；
- `version`；
- 原 `phase`；
- `direction`；
- `actions`。

字段值直接来自 `transaction.RecoveryResult`。`actions` 以结构化属性保存全部已完成动作，不另行生成普通输出行。日志消息使用稳定语义，例如 `transaction recovered`；测试断言级别、字段和值，不依赖属性的文本排列顺序。

恢复 Warn 是唯一允许在锁内产生的普通级别输出，因为用户要求恢复完成后立即可见。Warn 已经写出后，即使随后解锁或 Cloud 查询失败，也不撤销或重复该恢复日志。

### 恢复失败

`Recover` 返回 error 时，lifecycle 不调用 `slog.Error`。它先尝试释放锁，再把错误返回 CLI；若释放也失败，使用 `errors.Join` 保留 recovery 与 release 两个错误。CLI 在 lifecycle 返回后统一写一条 `slog.Error`、打印 Cobra usage 并非零退出。不得调用 Cloud，也不产生恢复成功 Warn。

所有其他错误同样留给 CLI 统一记录，不引入“已打印错误”标记或按错误文本去重逻辑。

## CLI 参数契约

Search 要求恰好一个位置参数。参数处理顺序为：

1. 读取 keyword；
2. 去除 Unicode 空白意义下的首尾空白；
3. 若结果为空，返回 `keyword must not be empty`；
4. 使用 `golang.org/x/text/cases.Fold` 对 keyword 进行 Unicode case folding，作为匹配值；
5. 校验可选 `--type` 只能是 `skill`、`plugin` 或 `mcp`。

Search 不调用要求 Agent 的 `validateCommon`，而使用只校验类型的命令专属逻辑。公共 `Flags.Agent` 字段可继续供其他命令使用，但 Search 不绑定该 flag、不读取该字段，verbose 解析诊断也不报告虚假的 Agent 值。

Cobra 原生负责未知 flag、位置参数数量、help 和 flag 值解析。以下调用必须失败并显示 usage：

```text
apm search foo --agent=all
apm search
apm search foo extra
apm search "   "
apm search foo --type=other
```

## 匹配与过滤

Cloud `List()` 返回的每个引用均满足以下上游契约：

- `Type` 是规范的小写类别；
- `Name` 是规范名称；
- `Version` 是带 `v` 的合法三段数字 SemVer，即 `vMAJOR.MINOR.PATCH`；
- 不存在 prerelease 或 build metadata；
- 不存在仅大小写不同而逻辑重名的名称。

Search 信任该契约，不重新验证 Cloud 引用。

每个引用的匹配规则：

1. 使用同一个 `cases.Fold` caser 对 `PowerRef.Name` 做与 keyword 相同的 Unicode case folding；
2. 使用大小写不敏感的子串匹配；
3. 不匹配 `Type`、`Version`、描述、标签、元数据或文件内容；
4. `--type` 非空时，要求 `PowerRef.Type` 与指定规范值精确相等。

原始 `PowerRef` 不因匹配而改写；输出继续使用 Cloud 返回的原始 Type、Name 和 Version。

## 最新版本选择与排序

筛选后的引用按原始 `(PowerRef.Type, PowerRef.Name)` 分组。每组使用项目在 ADR-001 中选定的 SemVer 实现比较版本，只保留数值最大的一个引用。例如 `v1.10.0` 大于 `v1.9.0`，不得使用普通字符串比较决定最新版本。

Cloud 非法数据不属于本 feature 的受支持输入。Search 不为非法版本定义跳过、错误返回、回退到字典序或保留首项等行为，也不新增对应测试。

选出每组唯一引用后，使用稳定排序：

1. `PowerRef.Type` 原始字符串字典序升序；
2. Type 相同时按 `PowerRef.Name` 原始字符串字典序升序。

版本不参与最终组间排序。由于每个 `(Type, Name)` 只剩一个结果，不需要定义同组相等项次序。

## 结构化结果与渲染

Search lifecycle 只返回：

```go
SearchResult{Powers: refs}
```

它不接收 renderer，不写业务 stdout，也不使用 `slog.Info` 代替业务输出。CLI 在 Search 成功返回后逐项写入：

```text
<type>/<name>@<version>\n
```

示例：

```text
skill/foo-before-summary@v0.0.1
```

无结果时 stdout 为空且命令成功。renderer 必须检查 writer 返回的错误；写入失败时返回普通 error，由 CLI 统一记录。已经完整写出的前缀行不回滚、不重试，不触发新的 lifecycle 或共享状态操作。

## 错误处理

CLI 对所有失败使用单一出口：

1. 调用一次 `slog.Error("command failed", ...)`；
2. 把 Cobra usage 写到 stderr；
3. 向 `main` 返回 error，最终非零退出。

不得在 lifecycle、Cloud、transaction 或 renderer 中提前重复记录 Error。恢复失败满足“写 Error”的要求，但实际写入位置是 lifecycle 已尝试解锁后的 CLI 统一出口。

错误保留底层 cause 和阶段上下文：

- 获取锁失败：说明 Search recovery preflight 无法获取锁；
- 恢复失败：保留 `transaction.ErrRecoveryFailed` 及其 cause；
- 释放失败：说明 Search recovery preflight 无法释放锁；
- recovery 与 release 同时失败：`errors.Join` 保留两者；
- Cloud 失败：说明 Search 无法列出 Cloud powers；
- renderer 失败：说明 Search 结果无法写入 stdout。

参数、锁、恢复和 Cloud 错误不得产生业务结果行。renderer 在后续行写入失败时可能保留此前已经完整写出的前缀行，按本规格的渲染约束处理。

## 测试设计

### Lifecycle 顺序测试

使用 fake Locker、Unlock、TransactionManager 和 CloudClient 记录调用顺序，覆盖：

- `Acquire → Recover → Warn → Release → Cloud.List`；
- Warn 在 fake Cloud 被调用前已经同步出现在 stderr；
- 无 journal 时普通模式无日志，verbose 模式只有 Debug；
- Acquire 失败不调用 Recover、Release 或 Cloud；
- Recover 失败会调用 Release，但不调用 Cloud；
- Release 失败不调用 Cloud；
- Recover 与 Release 同时失败时两个错误均可通过 `errors.Is/As` 找到；
- Warn 包含 operation、agent、power、version、原 phase、direction 和 actions；
- Cloud error 原样保留并带 Search 上下文。

### 搜索算法测试

覆盖：

- keyword 首尾空白被移除；
- 空白 keyword 被拒绝；
- Unicode case folding 后的大小写不敏感子串匹配；
- 只匹配 Name，不匹配 Type 或 Version；
- `--type` 精确过滤；
- 同一 Type/Name 的 `v1.10.0` 胜过 `v1.9.0`；
- 不同 Type 下同名 Power 分别保留；
- 最终 Type、Name 字典序稳定；
- 无匹配返回非 nil 空切片；
- 输入引用不被修改。

不添加非法 Cloud 版本、prerelease、build metadata 或大小写重名测试。

### CLI 与集成测试

覆盖：

- Search 不在 help 中展示 `--agent`；
- `--agent` 返回 unknown flag、usage 和非零结果；
- 合法 Search 不要求 Agent；
- 精确输出 `<type>/<name>@<version>\n`；
- 空结果无 stdout；
- 恢复 Warn 在 stderr，业务结果只在 stdout；
- 各错误路径恰好一条 Error 且包含 usage；
- repeated `CLIEntry.Execute` 不泄漏 flag、logger 或 lifecycle 状态；
- 开发期 Local Cloud fixture 输出 `skill/foo-before-summary@v0.0.1`。

最新提交已把 fixture 目录和 marker 版本改为 `v0.0.1`，现有 Cloud 单元及集成测试仍期待 `0.0.1` 并导致基线失败。本 feature 同步更新这些断言为 `v0.0.1`，不增加无 `v` 版本兼容。

## 文档与 ADR 同步

实现必须同步以下设计资产：

- 修订 ADR-002 的 Search 语法，明确 Search 不接受 `--agent`；
- 修订 ADR-003，把“锁持有到结果输出结束”改为“锁覆盖恢复与共享状态业务，CLI 在解锁后渲染”，并记录 Search 在恢复后立即解锁；
- 保持 ADR-009 的 transaction Manager 和集中恢复边界，补充对修订后 ADR-003 的一致引用；
- 更新 Search lifecycle 图，明确 Warn、解锁、Cloud 与 renderer 的顺序；
- 更新 Core Lifecycle 类图和锁说明，展示 Search 结构化结果接口；
- 更新项目概览和 `BUILD.md`，移除 Search 的 `--agent` 示例及“Search 尚未接线”描述；
- 在相关文档中引用 ADR-002、ADR-003 和 ADR-009。

本 feature 不新建 ADR。它直接修订已有 ADR-003 的锁持有决策，并同步 ADR-002 的命令语法。

## 验收与完成条件

实现完成必须同时满足：

1. Search 的正常、空结果、恢复成功、恢复失败、锁失败、Cloud 失败和渲染失败行为符合本规格。
2. 历史验收边界：当时 list/install/uninstall 没有 lifecycle 接线；Feature 009 已使 List 独立接线，Feature 010 已实现 Install，只有 Uninstall 仍为 parse-only。
3. 源码中的重要 Search 接口和构造具有简要中文注释。
4. ADR、C4 图、项目概览和构建文档语义一致。
5. `gofmt`、聚焦无缓存测试、全量无缓存测试、`go vet`、构建和 `git diff --check` 通过。
6. 现有 `v0.0.1` fixture 基线失败已通过更新正确断言解决。
7. 行为实现完成后在 `docs/Changelog.md` 增加本用户请求对应的一条记录；纯设计提交本身不写 Changelog。

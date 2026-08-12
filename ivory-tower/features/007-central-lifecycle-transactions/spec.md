# 集中式 Lifecycle Transaction 规格

## 目标

实现 `internal/transaction` 的真实接口与默认文件系统实现，使安装和卸载在 Agent 目标目录、Config Repository 清单与集中 journal 之间保持可恢复的一致性。

本 feature 只实现 transaction 能力及供 lifecycle 后续调用的边界，不实现 lifecycle 或 CLI 编排。以下“四个命令均在全局锁内 Recover”的描述是 Feature 007 的历史预期，不是当前通用规则；恢复仍不依赖当前命令再次指定原来的 Agent 或 Power。

> **历史 Install 规则已被 Feature 010 supersede（2026-08-11）：** 本文原来允许“已纳管 Install target 缺失时继续安装”的自愈描述不再是当前契约。现在该分支在生成 transaction ID、创建 journal 或调用 Adapter 前，执行 `ManifestWriter.Remove(agent,type,name)`、清理 canonical staging、返回 `ErrStaleManifest` 并中止。本文 Uninstall target 缺失时继续清单删除的语义保持不变。

> **Feature 007 的 lifecycle 边界已被 Feature 008–011 supersede：** Search 只在 Recover 期间持锁，随后释放锁再查询 Cloud；Cloud List 无锁，installed List 使用自己的 Recover/Refresh/Config 锁区间；Install 在一个锁区间内完成 Recover、刷新、Cloud、Cache 与 serial Transaction；Uninstall 在自己的单锁区间内完成 Recover、刷新、本地清单解析与 serial Transaction；二者都在成功 Release 后由 CLI 渲染结构化结果。本 feature 的 Transaction 公开接口是历史基础，当前 Uninstall 结果接口以 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 为准。

本设计由 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 记录，并细化 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md) 和 [ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)。

## 职责边界

### Lifecycle（Feature 007 历史边界）

以下列表记录 Feature 007 当时对后续 lifecycle 的预期，已由 Feature 008–010 的命令专属边界 supersede，不能理解为所有当前命令都采用同一锁或直接输出模型：

- 获取并持有全局 APM 锁；
- 在每个命令入口调用 transaction 恢复；
- 刷新 Agent 快照、解析目标 Agent 和 Adapter；
- 解析 Power 类型和版本；
- 通过 Config Repository 的 `List/Find` 判断未安装、同版本、首次安装、升级或降级；
- 通过 Cloud/Cache 完成 get/fetch，并把公共 staging 根传给 transaction；
- 串行调度多 Agent、汇总结果和输出用户信息。

### Transaction

transaction 负责：

- 保证至多一个活动 journal；
- 在目标文件变化前持久化恢复所需意图；
- 管理已纳管旧目标的 backup、卸载 quarantine 和同盘原子 rename；
- 调用 Adapter 发布安装内容，并依据显式发布结果确定当前调用是否越过提交点；
- 在提交点后调用 Config Repository 的 `Set/Remove`；
- 提交点前立即回滚，提交点后保留 journal 供下一次公共预检完成提交；
- 根据 journal 与路径状态恢复任意上次事务；
- 返回结构化恢复结果，不直接打印用户信息。

### Adapter 与其他组件

Adapter 继续拥有 Agent 目标路径规则、staging 适配、复制、原子发布和常规 staging 清理。transaction 不重复 Agent 路径拼接，不读取清单判断安装许可，也不负责 Cloud/Cache。

Config Repository 的 `List/Find` 由 lifecycle 调用；必须与提交点和 journal 删除严格排序的 `Set/Remove` 由 transaction 调用。Repository 仍保持无状态，且不自行获取全局锁。

## 公开接口

transaction 向 lifecycle 暴露高层接口，而不是可被任意排列的 `Begin/Backup/Commit` 原语：

> **已 supersede 的历史签名：** 以下 Feature 007 接口保留当时的 `Uninstall(...) error`；Feature 011 已将当前签名修订为 `Uninstall(...) (UninstallResult, error)`，其 `TargetMissing` 事实契约见 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

```go
type Manager interface {
    Recover(context.Context) (RecoveryResult, error)
    Install(context.Context, InstallRequest) error
    Uninstall(context.Context, UninstallRequest) error
}

type InstallRequest struct {
    Agent       domain.AgentID
    Power       domain.PowerRef
    Previous    *domain.PowerRef
    StagingRoot string
    Adapter     adapter.Adapter
}

type UninstallRequest struct {
    Agent   domain.AgentID
    Power   domain.PowerRef
    Adapter adapter.Adapter
}

type ManifestWriter interface {
    Set(domain.AgentID, domain.PowerRef) error
    Remove(domain.AgentID, domain.PowerType, string) error
}

func NewManager(ManifestWriter) (*Service, error)
```

`Previous == nil` 表示清单中没有旧版本。非 nil 时必须与 `Power` 同名同类型且版本已确定。transaction 校验请求结构和一致性，但不重新执行版本选择或安装许可判断。

Adapter 安装接口调整为：

```go
type InstallResult struct {
    Published bool
}

Install(domain.PowerRef, string) (InstallResult, error)
```

目标 rename 未成功时 `Published=false`；rename 一旦成功，即使后续 staging 清理失败也必须返回 `Published=true`。`error == nil` 且 `Published=false` 是 Adapter 契约错误。transaction 不解析错误文本，也不在当前调用中仅凭 target 是否存在猜测 Adapter 是否发布成功。

transaction 依赖只含 `Set/Remove` 的清单写接口。JournalStore 是 transaction 内部依赖和测试 seam，不再作为 lifecycle 的依赖，其接口固定为：

```go
type JournalStore interface {
    Inspect() (Journal, bool, error)
    Create(Journal) error
    Update(Journal) error
    Delete(transactionID string) error
}
```

`Inspect` 的 bool 为 false 表示没有活动事务。`Create` 只允许 journal 不存在；`Update/Delete` 必须与当前 transaction ID 一致。

默认 Manager 构造函数接收清单写接口，解析集中 transaction 路径失败时直接返回错误。测试通过 transaction 包内构造 seam 注入 JournalStore 和文件系统失败点，不把底层步骤暴露给 lifecycle。

恢复结果使用以下精确结构：

```go
type RecoveryResult struct {
    Recovered     bool
    TransactionID string
    Operation     Operation
    Agent         domain.AgentID
    Power         domain.PowerRef
    OriginalPhase Phase
    Direction     RecoveryDirection
    Actions       []RecoveryAction
}

type RecoveryAction struct {
    Kind RecoveryActionKind
    Path string
}

type RecoveryDirection string
type RecoveryActionKind string

const (
    RecoveryRollback RecoveryDirection = "rollback"
    RecoveryCommit   RecoveryDirection = "commit"
)

const (
    RecoveryActionRestoreTarget        RecoveryActionKind = "restore-target"
    RecoveryActionSetManifest          RecoveryActionKind = "set-manifest"
    RecoveryActionRemoveManifest       RecoveryActionKind = "remove-manifest"
    RecoveryActionRemoveBackup         RecoveryActionKind = "remove-backup"
    RecoveryActionRemoveQuarantine     RecoveryActionKind = "remove-quarantine"
    RecoveryActionRemoveStaging        RecoveryActionKind = "remove-staging"
    RecoveryActionRemoveTransactionDir RecoveryActionKind = "remove-transaction-dir"
)
```

`RecoveryDirection` 只允许 `rollback` 或 `commit`。文件系统 action 的 `Path` 保存实际路径，manifest action 的 `Path` 为空。无活动 journal 时返回 `Recovered=false`、其他字段为零值且 error 为 nil。恢复成功时 `Recovered=true`；恢复失败时 `Recovered=false`，但已读取的 journal 元数据与已经完成的 actions 仍保留在结果中，并同时返回 error。该结果供选择 Recover 的 lifecycle 记录 ADR-003 要求的恢复日志。

## 集中状态与同盘事务内容

默认集中状态布局：

```text
<state>/transactions/
└── active.json
```

- `paths.TransactionPath() (string, error)` 返回 `<state>/transactions`。
- `paths.JournalPath() (string, error)` 返回 `<state>/transactions/active.json`。
- 集中 journal 是恢复入口；任意后续命令只需检查该文件，不需要重放原命令参数。

backup/quarantine 不能统一放入 APM state：Agent 目标可能与 state 位于不同文件系统，集中存放会破坏原子 rename。每个事务在 target 的同一父目录使用：

```text
<target-parent>/.apm-transactions/<transaction-id>/
├── backup
└── quarantine
```

单个 operation 只使用所需条目。集中 journal 保存这些实际绝对路径。成功完成或回滚后清理事务条目、transaction ID 目录，以及确认已空的 `.apm-transactions` 父目录。

transaction ID 使用 `crypto/rand` 生成 128 位随机值并编码为小写十六进制，既避免同一目标下的名称碰撞，也保证可安全作为单个路径段。`.apm-transactions` 与 transaction ID 目录权限均为 `0700`。

Journal 的持久化字段固定为：

```go
type Journal struct {
    SchemaVersion  int
    TransactionID  string
    Operation      Operation
    Phase          Phase
    Agent          domain.AgentID
    Power          domain.PowerRef
    Previous       *domain.PowerRef
    StagingRoot    string
    TargetPath     string
    TransactionDir string
    BackupPath     string
    QuarantinePath string
}
```

首版 `SchemaVersion` 固定为 1。Install 必须填写 canonical staging、backup 和本地事务目录，quarantine 为空；Uninstall 必须填写 quarantine 和本地事务目录，staging/backup 为空。`Power.Version` 是唯一的新版本字段，不保留重复的顶层 Version。

## Journal 持久化

Journal 根目录权限固定为 `0700`，文件权限固定为 `0600`。Create/Update 在同目录创建临时文件，完成编码、文件 `fsync`、关闭和原子 rename 后同步父目录。Delete 删除匹配 transaction ID 的活动文件后同步父目录。

读取时拒绝：

- 无法解析或未知 schema；
- 未知 operation/phase；
- 空 transaction ID、Agent、Power 名称、类型或版本；
- operation 与 phase 不兼容；
- 非绝对、非 clean、文件系统根或越界路径；
- backup/quarantine/本地事务目录不符合 target 同父目录和 transaction ID 布局；
- Install staging 不等于 `paths.StagingPath()`；
- 字段组合与首次安装、升级或卸载语义冲突。

校验失败时 fail closed：保留 journal，不触碰目标、清单或任何待清理路径，也不执行当前用户命令。

## 阶段与方向

阶段定义：

- `preparing`：Install 已创建 journal，但尚未持久化发布意图；
- `publishing`：Adapter 可能正在发布，恢复时需检查 target；
- `removing`：卸载目标可能正在移入 quarantine；
- `rolling-back`：恢复方向已固定为回滚；
- `committing`：恢复方向已固定为完成提交。

阶段兼容关系：

| Operation | 合法阶段 |
| --- | --- |
| install | `preparing`、`publishing`、`rolling-back`、`committing` |
| uninstall | `removing`、`rolling-back`、`committing` |

## Install 正常流程

1. 检查 context 和 request，通过 Adapter 获取 target，并确认没有活动 journal。
2. 检查事务前 target：`Previous == nil` 且 target 已存在时直接返回清单外冲突；`Previous != nil` 且 target 已缺失时适用上方标记为已被 Feature 010 supersede 的历史 Install 规则，当前不继续安装。
3. 生成 transaction ID、本地事务路径和 backup 路径，写入 `preparing` journal。
4. `Previous != nil` 且 target 存在时，创建本地事务目录并把 target 原子移动到 backup。target 已缺失的旧自愈安装分支已被 Feature 010 supersede，不进入本流程。
5. 在更新 `publishing` 前确认 target 为空，再持久化发布意图并调用 Adapter。Adapter 仍执行自己的目标二次检查，以覆盖检查后的外部变化。
6. Adapter 返回 `Published=false` 时，把 journal 更新为 `rolling-back`。升级场景在 target 为空时原子恢复 backup；首次安装不删除可能由外部创建的 target。回滚成功后删除 journal，再返回原始错误。
7. Adapter 返回 `Published=true` 时，提交点已经越过。将 journal 更新为 `committing`；若 Adapter 同时返回清理错误，停止当前事务、保留 journal 并返回错误。
8. Adapter 成功后幂等调用清单 `Set(Agent, Power)`，删除 backup，重试安全的 canonical staging 清理，清理本地事务目录，最后删除 journal。
9. 提交点后的阶段更新、清单、清理或 journal 删除失败不回滚新 target；保留 journal 并返回带上下文的错误。

在进入回滚动作前必须先持久化 `rolling-back`。否则进程可能在 backup 已恢复为 target、journal 尚未删除时中断，下一次恢复会把旧 target 误判为刚发布的新版本。

## Uninstall 正常流程

1. 检查 context 和 request，通过 Adapter 获取 target，并确认没有活动 journal。
2. 生成 transaction ID、本地事务路径和 quarantine 路径，直接写入 `removing` journal。
3. target 存在时创建本地事务目录，并把 target 原子移动到 quarantine；该 rename 是提交点。
4. target 在事务开始前已经缺失，或 rename 时发现其已被外部删除，均按漂移幂等提交视为文件侧已经提交。
5. 把 journal 更新为 `committing`，幂等调用 `Remove(Agent, Power.Type, Power.Name)`，删除 quarantine，清理本地事务目录，最后删除 journal。
6. rename 前的普通失败先持久化 `rolling-back` 并回滚；提交点后的失败保留 journal，不把 quarantine 恢复为 target。

清单没有记录时 lifecycle 直接完成幂等卸载，不调用 transaction。只有 lifecycle 已确认存在清单记录时才创建 UninstallRequest。

## 恢复判定

`Manager.Recover` 自行 Inspect 集中 journal。没有 journal 时返回 `Recovered=false`；有 journal 时先完整校验，再检查相关路径。

| Operation / Phase | 路径状态 | 恢复方向 |
| --- | --- | --- |
| install / `preparing` | 合法状态 | 回滚 |
| install / `rolling-back` | 合法状态 | 回滚 |
| install / `publishing` | target 存在 | 完成提交 |
| install / `publishing` | target 不存在 | 回滚 |
| install / `committing` | 合法状态 | 完成提交 |
| uninstall / `removing` | quarantine 存在 | 完成提交 |
| uninstall / `removing` | target 不存在 | 完成提交 |
| uninstall / `removing` | target 存在且 quarantine 不存在 | 回滚 |
| uninstall / `rolling-back` | 合法状态 | 回滚 |
| uninstall / `committing` | 合法状态 | 完成提交 |

“合法状态”仍受 operation-specific 不变量约束；例如 target 与 backup/quarantine 同时存在且无法由当前固定方向安全解释时，恢复失败并保留 journal，不能覆盖 target 或猜测内容归属。

恢复动作幂等：

- Install 回滚：backup 存在且 target 为空时恢复 backup；没有 backup 时不删除 target；清单保持旧值；清理安全的事务残留后删除 journal。
- Install 提交：新 target 必须存在；幂等 Set 新引用；删除 backup；仅在 canonical staging 身份和当前物理路径不重叠检查通过后重试 staging 清理；清理本地事务目录后删除 journal。
- Uninstall 回滚：quarantine 存在且 target 为空时恢复；target 已在且 quarantine 不存在时无需动作；清理事务残留后删除 journal。
- Uninstall 提交：幂等 Remove 清单记录；删除 quarantine；清理本地事务目录后删除 journal。

恢复不调用 Adapter，也不重新解析当前环境中的 Agent 根。它只使用 journal 中经过严格验证的精确路径，因此改变 Agent 环境变量或执行不同命令不会使恢复丢失原目标。

## Context、错误与日志

文件系统 rename、remove 和 Repository 调用本身不可由 context 强制中断。transaction 在主要步骤之间检查 context：提交点前取消时立即回滚；提交点后取消时保留 journal，不逆转提交方向。

transaction 定义以下可供 `errors.Is` 判断的稳定错误类别：

```go
var (
    ErrActiveTransaction  = errors.New("active transaction exists")
    ErrInvalidRequest     = errors.New("invalid transaction request")
    ErrInvalidJournal     = errors.New("invalid transaction journal")
    ErrUnsafePath         = errors.New("unsafe transaction path")
    ErrIndeterminateState = errors.New("indeterminate transaction state")
    ErrRecoveryFailed     = errors.New("transaction recovery failed")
)
```

所有 I/O、Adapter 和 Repository 错误保留底层错误，并补充 operation、transaction ID、Agent、Power、阶段和路径上下文。合并错误使用 `errors.Join`，不让清理错误覆盖主错误。

transaction 和 Adapter 不直接打印。内部阶段使用包级 `slog.Debug`，并复用 logging 的 Agent、Power、Version 和 Phase 字段。`RecoveryResult` 为选择 Recover 的 lifecycle 的 stderr 恢复日志提供结构化数据。

## 路径安全

所有递归删除只允许作用于以下经验证路径：

- 与 journal transaction ID 和 target 父目录匹配的本地事务目录；
- Install 提交方向下，与 `paths.StagingPath()` 相等、不是文件系统根，并与 target 当前物理根不重叠的 staging。

Adapter 与 transaction 应复用同一组物理路径解析和分隔符感知包含判断工具，避免两套安全规则漂移。符号链接、缺失后缀、相邻字符串前缀和文件系统根均纳入测试。

已有 `.apm-transactions` 或 transaction ID 路径为 symlink、非目录或不符合预期所有权时必须拒绝使用；清理只删除当前随机 transaction ID 对应的目录，只有父目录确认为空时才删除 `.apm-transactions`。

## 开发规范

新增或修改的生产代码中，重要接口、公开类型、构造函数、事务入口、阶段判定和安全关键辅助函数必须使用与项目既有风格一致的简要中文注释，说明其用途。注释应以被说明的 Go 标识符开头，并避免复述实现步骤或写成长篇设计说明。

普通局部变量、显而易见的短小内部辅助函数和测试代码不要求逐项注释。

## 测试与验收

### Journal Store

- 不存在、Create、Update、Delete 和 transaction ID 不匹配；
- 权限、临时文件清理、文件与目录同步失败；
- 半写、非法 JSON、未知 schema、未知枚举和字段组合；
- 第二个事务不能覆盖活动 journal。

### Install

- 首次安装、升级、降级；“清单记录存在但 Install target 已被手动删除后继续安装”是已被 Feature 010 supersede 的历史测试边界；
- 清单无记录但 target 存在时由 transaction 的发布前检查或 Adapter 的防御性二次检查报告清单外冲突，且不删除该目标；
- backup、阶段更新、Adapter、Set、staging/backup 清理和 journal 删除的失败注入；
- `Published=false` 进入回滚，`Published=true` 固定提交方向；
- Adapter 发布成功但 staging 清理失败时保留 journal，并由全新 Manager 完成提交。

### Uninstall

- 正常 quarantine、target 已手动删除时幂等完成、Remove 和清理；
- rename 失败、外部删除竞态、Remove/清理/journal 删除失败；
- 提交点前回滚和提交点后固定完成提交。

### Recovery 与安全

- 每个原子文件操作和 journal 阶段更新之间模拟进程中断；
- 以全新 Manager、不同当前命令且不持有原 Adapter 实例完成恢复；
- 覆盖所有合法 phase/path-state 组合及矛盾组合；
- 重复 Recover 的幂等性；
- 绝对路径、clean、同父目录、transaction ID、symlink 和物理重叠防护；
- 危险 journal fail closed 且不触碰记录路径。

最终验证：

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 明确不实现

- lifecycle/CLI 实际调用、锁装配、公共预检和用户输出；
- Cloud get、Cache fetch、版本解析、同版本跳过和多 Agent 调度；
- Plugin、MCP、项目级 Power 或其 Adapter；
- 跨文件系统 copy-delete 伪装成原子 backup/quarantine；
- 动态扫描多个 journal、并发事务或自动丢弃损坏 journal；
- 对 APM 全局锁之外的外部进程提供无竞态保证。

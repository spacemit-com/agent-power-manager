# Codex、Claude Code 与 Hermes Agent Skill Adapter 规格

## 目标

APM 为 Codex、Claude Code 和 Hermes Agent 提供首批可用 Adapter。Adapter 负责单个 Agent、单个 Skill 的目标路径差异，并拥有从公共 staging 原地适配、复制安装、原子发布到 staging 清理的完整过程。

本 feature 只实现全局 Skill Adapter，不把 Adapter 串联到 lifecycle 或 CLI。lifecycle 在调用 Adapter 前已经完成版本选择、清单查询和安装许可判断；升级或降级时，transaction 必须先把已纳管旧目标移到 backup，使 Adapter 接收到的最终目标路径为空。详见 [ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)。

## 支持矩阵

| Agent ID | 根目录覆盖变量 | 默认根目录 | 全局 Skill 根目录 |
| --- | --- | --- | --- |
| `codex` | `CODEX_HOME` | `<有效 HOME>/.codex` | `<codex-root>/skills` |
| `claude-code` | `CLAUDE_CONFIG_DIR` | `<有效 HOME>/.claude` | `<claude-root>/skills` |
| `hermes-agent` | `HERMES_HOME` | `<有效 HOME>/.hermes` | `<hermes-root>/skills` |

环境变量去除首尾空白后非空时优先使用；未设置或仅含空白时通过 `paths.HomeDir()` 获取有效 HOME，因此默认路径继续遵守 `APM_HOME` 测试隔离。Codex 不把 `/etc/codex` 作为替代安装根目录：检测结果必须对应 Adapter 实际安装使用的用户根目录。

仅 Agent 根目录决定 Agent 是否已安装。检测使用跟随 symlink 的目录状态：根目录必须存在且最终指向目录。缺失的 `<agent-root>/skills` 不影响检测，安装时可以创建；Adapter 不创建缺失的 Agent 根目录。

## Adapter 接口与职责

`internal/adapter` 对 lifecycle 暴露最小接口：

```go
type Adapter interface {
    DetectAgent() (bool, error)
    TargetPath(domain.PowerRef) (string, error)
    Install(domain.PowerRef, string) (InstallResult, error)
}

type InstallResult struct {
    Published bool
}
```

Feature 007 根据 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md) 为 Install 增加显式发布结果：目标 rename 未成功时为 false；一旦 rename 成功，即使后续 staging 清理失败也为 true。该调整只向 transaction 暴露提交点结果，不改变 Adapter 的安装职责。

第二个 `Install` 参数是当前 Power 的公共 staging 根目录，而不是其中的 Skill 目录。对 `ref`，源目录固定为：

```text
<staging-root>/skills/<ref.Name>
```

各方法职责如下：

- `DetectAgent` 只检查此 Adapter 配置的 Agent 根目录，不创建目录或缓存结果。
- `TargetPath` 校验 Power 类型和名称，返回 `<agent-root>/skills/<ref.Name>`；它不要求路径存在，也不读写文件。lifecycle/transaction 使用它记录、备份和恢复 Agent 特有目标，不能自行重复路径规则。
- `Install` 校验版本已确定的 Skill，在目标为空的前提下完成冲突检查、适配、复制、发布和清理。它不读取 Config Repository，不判断首次安装、相同版本、升级或降级。

删除占位 `domain.Power` 参数和当前没有真实行为的 `Uninstall`。卸载将在其 lifecycle 与事务契约明确后另行扩展，不为未来行为保留胖接口。

## 共享管线与 Agent 配置

三个 Adapter 共用一份 Skill 安装管线。每份 Agent 配置只包含：

- 正式 `AgentID`；
- 根目录环境变量名；
- 有效 HOME 下的默认根目录名；
- Agent 专属的 staging 适配 hook。

首版三个适配 hook 都是原地 no-op。共享管线仍保留该内部边界，使未来 Agent 格式变化可以只修改对应 hook，而不复制冲突防护、发布和清理逻辑。hook 不作为跨包公共扩展 API。

## Registry 与 Agent 快照

`NewRegistry` 注册 `codex`、`claude-code`、`hermes-agent` 三个 Adapter。Registry 行为为：

- `Resolve(agent, type)` 仅在 Agent 已注册且 `type == skill` 时返回 Adapter。
- 未知 Agent 或 `plugin`/`mcp` 返回包装 `ErrUnsupportedAdapter` 的错误，不返回 `(nil, nil)`。
- `RefreshInstalled` 按 `codex`、`claude-code`、`hermes-agent` 顺序调用 `DetectAgent`，只收集已安装 Agent；任一检测发生非“不存在”错误时整体失败，不发布部分快照。
- `AgentSnapshot.Contains` 正确查询快照；`Ordered` 返回保持 Registry 顺序的切片副本，调用方修改结果不影响原快照。

Registry 不缓存跨命令检测结果。lifecycle 仍按 [ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md) 在相关命令中刷新一次快照。

## Skill 引用与路径安全

`Install` 要求：

- `ref.Type == skill`；
- `ref.Name` 和 `ref.Version` 去除首尾空白后均非空；
- `ref.Name` 必须原样是安全的单个文件名，不得是绝对路径、`.`、`..`，不得包含当前平台或兼容平台的路径分隔符，也不得通过清理后改变含义。

Adapter 不静默清洗或重命名 Skill。非法名称返回 `ErrInvalidSkill`，避免 Power 引用、`SKILL.md` 自声明名称、staging 路径、目标目录和安装清单产生不同名称。

公共 staging 与 Agent Skill 根还必须在物理文件系统中不重叠。Adapter 跟随已经存在的 staging、Agent 根和 Skill 根 symlink；Skill 根尚不存在时，先解析其已经存在的 Agent 根父目录，再拼接 `skills`。解析后的 Skill 根不得等于公共 staging，也不得位于公共 staging 之下。包含判断必须按路径分隔符识别真正的父子关系，不能用字符串前缀把 `staging-agent` 误判为 `staging` 的子目录。

## 安装流程

`Install(ref, stagingRoot)` 严格按以下顺序执行：

1. 校验 staging 根路径参数。将调用方路径和 `paths.StagingPath()` 都转换为绝对 clean 路径后，两者必须相等，且不得是文件系统根；无效参数直接返回且不得执行递归删除。
2. 计算 Agent 根和 Skill 根，解析 staging 与 Skill 根的物理路径并执行分隔符感知的包含检查。若物理 Skill 根等于或位于物理 staging 之下，返回包装 `ErrInvalidSkill` 的安全错误；此时不得注册 staging 清理、创建临时目录、复制或发布，也不得改变重叠树中的 staging 和目标内容。
3. 物理根确认不重叠后，为公共 `stagingRoot` 注册延迟清理，使此后的所有成功和失败出口都会递归删除整个 staging；随后校验 `ref`，并计算源和最终目标路径。
4. 检查 Agent 根目录存在且是目录；不存在或不是目录时返回 `ErrAgentNotInstalled`，不得创建 Agent 根目录。
5. 对最终目标执行 `Lstat`。目录、普通文件、symlink 或其他任意已有条目都返回 `ErrTargetConflict`。除“不存在”以外的状态错误保留底层错误。此步骤发生在读取或适配 staging 之前。
6. 进入共享适配流程，读取 `<staging-root>/skills/<name>/SKILL.md` 并校验 YAML frontmatter，然后调用 Agent hook。fetch 已保证 staging 的目录和 marker 结构，Adapter 不在适配前重复做独立的目录/marker `Stat` 合规检查；实际读取失败仍按 I/O 错误返回。
7. 以单层 `Mkdir(0755)` 创建缺失的 `<agent-root>/skills`，不得使用会顺带重建父级的递归创建；若 Agent 根在检测后消失，创建因此失败而不能重建 Agent 根。已有 Skill 根必须最终指向目录。
8. 在 Skill 根内创建唯一的隐藏临时目录，把 staging Skill 完整复制到临时目录。复制包含 dotfiles，并保留源文件与目录权限位；安装模式固定为 copy，不创建指向 staging 的 symlink。
9. 发布前再次 `Lstat` 最终目标。若外部变化使其出现，返回 `ErrTargetConflict`，不覆盖目标。
10. 将同一 Skill 根内的临时目录 rename 为 `<name>`，使正常观察者只看到不存在或完整目录。APM 全局锁保证 APM 自身串行，但本 feature 不承诺消除外部进程在二次检查与 rename 之间的竞态。
11. 延迟清理任何尚存的目标临时目录和整个公共 staging。

物理根确认不重叠后，成功和失败都清理 staging，因此多 Agent 安装必须串行执行：每个 Agent 在调用其 Adapter 前重新 fetch 一份干净的公共 staging，不能让多个 Adapter 共享同一次 fetch 的存续内容。物理重叠是唯一的有效 staging 身份清理例外：因为此时递归清理本身可能删除 Agent 根、目标或 publication 工作区，Adapter 必须在注册清理前拒绝安装并完整保留该重叠树。

## `SKILL.md` frontmatter

适配阶段只接受 YAML frontmatter：

```markdown
---
name: foo-before-summary
description: Run checks before producing the final summary.
---

# Skill body
```

规则如下：

- 文件必须从第一字节开始使用 `---` 行，并包含独立的结束 `---` 行；接受 LF 和 CRLF，结束分隔符后的正文可以为空。
- 分隔符之间必须是一个可解析的 YAML mapping；不支持 JavaScript frontmatter 或任何求值型格式。
- `name` 和 `description` 必须存在、YAML 类型为字符串，且去除首尾空白后非空。
- `name` 的原始字符串值必须与 `ref.Name` 精确相等；不做大小写折叠、trim 后匹配或名称清洗。
- `description` 支持普通字符串和 YAML 多行字符串。
- 未识别字段允许存在并原样留在文件中；Adapter 只验证，不重写 frontmatter。
- 非 mapping、无效 YAML、重复关键字段、缺失字段、非字符串字段、空白字段或名称不一致均返回 `ErrInvalidSkill`。

使用 YAML 官方组织维护的 `go.yaml.in/yaml/v3` 完成 YAML 语义和类型判断，不手写一个只支持键值行的伪 YAML 解析器。

## 原子性与清理语义

目标临时目录必须位于最终 Skill 根内，确保正常情况下 rename 不跨文件系统。发布前失败不得创建最终目标；发布成功后不得因为 staging 清理失败而删除已发布目标。

清理顺序为：

1. 清理尚未 rename 的目标临时目录；
2. 清理整个公共 staging 根目录。

所有清理错误均增加操作和路径上下文，并用 `errors.Join` 与主错误合并。没有主错误但清理失败时，`Install` 仍返回错误。如果目标已经 rename 成功，返回的错误表示“目标可能已发布但收尾失败”；后续 lifecycle/transaction 必须依据 journal 和实际路径状态完成提交或回滚，不能假设安装未发生。只有通过 `paths.StagingPath()` 身份校验且通过物理根不重叠检查的路径才进入这套必清理语义，错误、越界或物理重叠路径永远不会交给 `RemoveAll`。

Adapter 不管理 backup、quarantine、journal 或清单。升级/降级中已纳管旧目录的移走与恢复属于 transaction；当流程到达 Adapter 时，任何残留最终目标都按清单外冲突处理。

## 错误与日志

`internal/adapter` 定义：

```go
var (
    ErrAgentNotInstalled  = errors.New("agent is not installed")
    ErrUnsupportedAdapter = errors.New("unsupported adapter")
    ErrTargetConflict     = errors.New("target path already exists")
    ErrInvalidSkill       = errors.New("invalid skill")
)
```

调用方可用 `errors.Is` 判断错误类别。路径解析、状态检查、读取、YAML 解析、创建目录、复制、rename 和清理错误保留底层错误，并补充 Agent、Power、阶段和路径上下文。

Adapter 不直接打印，不记录 Info、Warn 或 Error，避免 lifecycle/CLI 重复呈现。它只使用包级 `slog.Debug` 记录检测、适配、复制、发布和清理等内部阶段，不创建私有 logger。

## 测试与验收

### 路径、检测与 Registry

- 三个 Agent 的环境变量覆盖默认根目录；空白覆盖值回退到有效 HOME。
- 默认根目录遵守 `APM_HOME`，测试不访问真实 HOME。
- staging 参数只接受与 `paths.StagingPath()` 相同的绝对 clean 路径；空路径、根路径和其他目录被拒绝且内容不变。
- 物理 Skill 根直接位于 staging 下，或通过 staging、Agent 根、Skill 根 symlink 解析到 staging 内时，在复制、发布和清理前拒绝，staging、symlink 与目标内容均保持不变；名称仅共享字符串前缀但物理不包含的相邻目录正常安装并清理 staging。
- 根目录存在且为目录时检测成功；缺失或非目录时不创建路径并按契约返回。
- 缺失的 `skills` 子目录不影响 Agent 检测。
- `TargetPath` 返回 Agent 特有路径且不修改文件系统。
- Registry 只解析三个 Agent 的 Skill；未知 Agent 和非 Skill 返回 `ErrUnsupportedAdapter`。
- 快照顺序、`Contains`、`Ordered` 副本和检测错误不发布部分结果均有覆盖。

### frontmatter 与输入

- 有效 YAML、额外字段、LF/CRLF 和多行 `description` 通过。
- 缺少或错误分隔符、非法 YAML、非 mapping、重复 key、缺字段、非字符串、空白字符串和 `name` 不匹配返回 `ErrInvalidSkill`。
- 非 Skill、空名称、空版本、绝对路径、分隔符和 `.`/`..` 被拒绝。
- frontmatter 校验不改写 staging 内容。

### 冲突、安装与清理

- 最终目标为目录、文件和 symlink 时均返回 `ErrTargetConflict`；Agent hook 未执行、目标未变化、staging 已清理。
- Agent 根存在但 `skills` 缺失时创建 Skill 根并成功安装。
- 完整树、dotfiles、只读目录、可执行位和文件/目录权限得到保留。
- 成功后最终目录完整，不残留目标临时目录或 staging。
- 适配失败、复制失败、二次冲突和 rename 失败均不暴露半成品最终目录，并清理临时目录与 staging。
- 主错误和可构造的清理错误均可由 `errors.Is/As` 观察；清理错误不覆盖主错误。
- 所有真实文件操作位于 `t.TempDir()` 与 `APM_HOME` 隔离范围，不读写用户真实 Agent 目录。

最终验证：

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 明确不实现

- lifecycle、CLI、Config Repository、journal 或 transaction 串联。
- 首次安装、相同版本幂等、升级、降级或清单修改的业务决策。
- 卸载、Plugin、MCP、项目级 Skill 或 symlink 安装。
- 多 Agent 并行安装或复用同一次 fetch 的 staging。
- Agent 根目录安装、`/etc/codex` 检测、Agent 运行时感知或更新通知。
- frontmatter 自动修复、名称清洗、未知字段删除或 Skill 正文转换。
- 针对外部进程的无竞态 no-replace 发布保证。

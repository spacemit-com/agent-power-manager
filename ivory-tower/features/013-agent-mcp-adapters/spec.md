# Agent MCP Adapter 与可恢复 CLI 事务规格

## 目标

在不改变 APM 现有通用命令、Agent 快照、全局锁、版本选择、批处理结果和安装清单模型的前提下，为 Codex、Claude Code 和 Hermes Agent 增加 MCP 安装与卸载支持。

MCP 的启动定义由 Cloud 按精确版本保存和返回。APM 不为 MCP 使用 Cache 或 staging，而是由类型专属 MCP Adapter 调用目标 Agent CLI 完成同名检查、安装和卸载。MCP CLI 副作用纳入现有唯一活动 journal 和集中 TransactionManager，使任意后续选择恢复阶段的命令都能先恢复上一次 Skill 或 MCP 事务。

本 feature 关联并落实 [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)。现有 Skill 文件事务继续遵守 [ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md) 与 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

## 范围与非目标

### 包含

- Codex、Claude Code、Hermes Agent 的用户级 MCP Adapter；
- `stdio` 与 `http` 两种 MCP 启动定义；
- Cloud MCP 资源发现和按精确版本直接读取；
- Agent CLI 的动态发现、非 shell argv 调用、30 秒超时和输出隔离；
- 通过目标 Agent CLI 执行的同名 MCP 存在性检查；
- APM 清单外同名冲突防护和清单内缺失 MCP 的单条漂移修复；
- 首次安装、相同版本幂等、升级、显式降级和卸载；
- MCP 安装与卸载的唯一活动 journal、提交、回滚和跨命令恢复；
- Skill/MCP 类型专属 Adapter 与事务驱动的 Registry 和组合根接线；
- 现有 Search、List、Install、Uninstall 命令对 MCP 的端到端支持；
- ADR、C4、项目概览、构建指南、feature 文档及实现完成后的 Changelog 同步；
- 完全隔离真实 Agent 环境的自动化测试。

### 不包含

- Plugin Adapter；
- 新增 `apm mcp ...` 子命令或改变现有 CLI 输出格式；
- MCP Cache、staging、文件复制或直接读写 Agent 配置文件；
- SSE、环境变量、HTTP Header、Bearer Token、OAuth、登录或密钥治理；
- Claude Code 的 local/project scope；
- Hermes 的认证、预设、环境变量或自定义工具筛选；
- MCP 服务连通性、工具发现结果或运行时健康检查作为 APM 成功条件；
- 从 Agent CLI 反向提取并持久化完整启动定义；
- 校验或清洗 Cloud 返回的 MCP 名称；名称合规性属于 Cloud 契约；
- 多个活动 journal、事务队列、多 Agent 并行或跨 Agent 原子回滚；
- 外部进程与用户在 APM 持锁期间修改同名 MCP 的竞态保证。

## 用户命令与既有生命周期

MCP 继续使用现有通用命令：

```text
apm search <name> --type=mcp
apm list --type=mcp
apm list --installed --agent=<agent|all> --type=mcp
apm install <name>[=<version>] --agent=<agent|all> --type=mcp
apm uninstall <name> --agent=<agent|all> --type=mcp
```

以下现有契约保持不变：

- Search/List 的筛选、排序和精确输出；
- Install 的名称与类别消歧、缺省最新版本和显式规范版本选择；
- Uninstall 从每个 Agent 的本地清单独立解析 Type 与 Version；
- `--agent=all` 的 Agent ID 字典序、串行执行和首错 fail-fast；
- Recover、同步 Warn、Agent 快照和共享状态操作的锁边界；
- 只有成功 Release 后才渲染结构化业务结果；
- Install 的六种 outcome 和 Uninstall 的现有结果行格式；
- CLI 是普通 Error 与 Usage 的唯一记录和渲染出口。

单条 Install 命令最终只安装一个已确定类型的 Power，因此不会在同一批处理中混合 Skill 与 MCP；跨命令恢复可以与当前命令的 Power 类型不同。

## 领域模型

### MCP 启动定义

`domain` 增加 MCP 启动定义，其逻辑结构为：

```go
type MCPTransport string

const (
    MCPTransportStdio MCPTransport = "stdio"
    MCPTransportHTTP  MCPTransport = "http"
)

type MCPServer struct {
    Transport MCPTransport
    Command   string
    Args      []string
    URL       string
}
```

它是严格的判别联合：

- `stdio`：`command` 去除首尾空白后必须非空，`args` 可以为空，`url` 必须为空；
- `http`：`url` 必须是具有 host 的绝对 `http` 或 `https` URL，`command` 必须为空，`args` 必须为空；
- `transport` 必须显式为 `stdio` 或 `http`；
- 不接受额外的认证、Header、环境变量或其他字段；
- `args` 的每个元素按原值作为独立 argv 传递，不进行 shell 拆词、变量展开或转义；
- MCP 名称继续来自 `PowerRef.Name`，APM 不定义额外名称正则、不清洗或改名。

`MCPServer` 只描述启动连接，不包含 Agent、Power 名称、版本、安装状态或秘密。常规 APM manifest 仍只保存版本已确定的 `PowerRef`，不把启动定义复制到长期安装清单。

### Cloud JSON

本地 Cloud 使用以下布局：

```text
internal/cloud/resources/
└── mcps/
    └── <name>/
        └── <version>/
            └── MCP.json
```

stdio 示例：

```json
{
  "transport": "stdio",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/me/project"]
}
```

HTTP 示例：

```json
{
  "transport": "http",
  "url": "https://mcp.company.com/mcp"
}
```

JSON 解码拒绝未知字段、尾随 JSON 值和错误字段类型，并按上述判别联合校验。Cloud 负责保证目录中的 Power 名称与版本合规。

## Cloud 边界

`cloud.Client` 在既有 `List` 和 Skill `Get` 之外增加按精确版本读取 MCP 定义的接口：

```go
GetMCP(domain.PowerRef) (domain.MCPServer, error)
```

契约如下：

- `ref.Type` 必须是 `mcp`，名称和版本必须已确定；
- LocalClient 从 `mcps/<name>/<version>/MCP.json` 直接读取并返回定义；
- 文件缺失、不是普通文件、引用不匹配或定义非法时返回带上下文的错误；
- MCP 定义不写入 Cache，不创建 staging，也不触发 `PowerCache.Fetch`；
- Install 的类别与版本仍先由 `Cloud.List()` 和 VersionSelector 确定，再调用 `GetMCP`；
- 升级或降级在任何 Agent CLI 写操作前，分别按旧清单版本和目标版本获取旧、新两个定义；
- 卸载在任何 Agent CLI 写操作前，按清单中的精确版本获取旧定义；
- 所需定义任一获取失败时，不创建 journal，不改变 Agent 配置或 manifest。

LocalClient 的 `List()` 同时扫描 Skill 与 MCP。普通 `MCP.json` 是 MCP 版本的 marker；List 只负责资源发现，完整 JSON 语义由 `GetMCP` 校验。返回结果继续按 Type、Name、Version 稳定排序。现有 Skill List/Get/Cache 行为不变。

## 类型专属 Adapter 与 Registry

### 接口分离

现有 `adapter.Adapter` 继续只表示 Skill 的文件安装契约，不向其中加入 MCP 方法，也不改变 `DetectAgent`、`TargetPath` 和 `Install` 的语义。

新增独立 MCP Adapter：

```go
type MCPPresence string

const (
    MCPPresent MCPPresence = "present"
    MCPAbsent  MCPPresence = "absent"
)

type MCPMutationState string

const (
    MCPMutationApplied    MCPMutationState = "applied"
    MCPMutationNotApplied MCPMutationState = "not-applied"
    MCPMutationUnknown    MCPMutationState = "unknown"
)

type MCPMutationResult struct {
    State MCPMutationState
}

type MCPAdapter interface {
    Inspect(context.Context, string) (MCPPresence, error)
    Add(context.Context, domain.PowerRef, domain.MCPServer) (MCPMutationResult, error)
    Remove(context.Context, domain.PowerRef) (MCPMutationResult, error)
}
```

重要接口实现时必须添加简要、规范的中文注释，说明用途和 fail-closed 约束。

各方法职责为：

- `Inspect` 只通过目标 Agent CLI 判断指定名称在 APM 管理 scope 中是否明确存在；不确定不是第三种成功值，而是 error；
- `Add` 把已校验定义转换为 Agent CLI argv 并调用 CLI；
- `Remove` 只删除同名 MCP，不读取 APM manifest；
- Adapter 不访问 Cloud、Cache、staging、journal 或 Config Repository；
- Adapter 不直接读取或修改 Codex TOML、Claude 配置或 Hermes YAML；
- Adapter 不通过 shell 执行命令。

`MCPMutationState` 表示可恢复事务所需的副作用事实：

- `applied`：CLI 明确报告目标变更成功；即使同时存在收尾错误，Transaction 仍按已变更处理；
- `not-applied`：CLI 未启动，或 Adapter 能根据目标 CLI 的稳定错误契约明确证明没有发生目标变更；
- `unknown`：超时、异常终止或输出契约不匹配，无法判断副作用是否发生；Transaction 必须保留 journal，交给后续 Recover 查询；
- Adapter 不得仅因无法识别输出就把结果归为 `not-applied`。

### Registry

Registry 继续保存三个 Agent 的根目录配置并生成同一个稳定 Agent 快照。Agent 根存在且为目录仍是 Agent 已安装的判断依据；CLI 是否可用不参与 `RefreshInstalled`，而在具体 MCP 操作时检查。

Registry 对 lifecycle 和 transaction 暴露类型专属解析能力：

```go
ResolveSkill(domain.AgentID) (adapter.Adapter, error)
ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
RefreshInstalled() (domain.AgentSnapshot, error)
```

三个 Agent 均支持 Skill 和 MCP；Plugin 继续返回不支持。内部可以共享 Agent 根与检测配置，但 Skill Adapter 与 MCP Adapter 分开保存和构造。Transaction 的 MCP Recover 只依赖窄的 `ResolveMCP` 能力。

## Agent CLI 契约

### 共同执行规则

每次调用都遵守：

- 通过 `exec.LookPath` 动态解析标准命令名 `codex`、`claude`、`hermes`；
- 不硬编码当前机器的绝对 CLI 路径，也不把解析路径写入 journal；
- CLI 不在 `PATH`、不是可执行文件或启动失败时，在任何 Agent 配置变更前失败；
- 使用 argv 直接调用，不经过 shell；
- 每次 Inspect/Add/Remove 都在调用方 context 下增加固定 30 秒超时；
- Codex 和 Claude 的 stdin 关闭，Hermes 仅接收下文定义的固定回答；
- 捕获 stdout/stderr，不转发到 APM 的业务 stdout；
- 捕获内容有固定上限，错误只携带有界诊断，不无限保存外部输出；
- 取消、超时或退出结果无法解释时 fail closed；
- APM 不把 MCP 服务健康、连接成功或工具数量作为跨 Agent 的统一验收条件。

Codex 与 Claude Code 的 Add/Remove 只有在进程正常退出且满足各自稳定成功契约时返回 `applied`；一般非零退出不自动等于 `not-applied`，无法排除部分写入时返回 `unknown`。Hermes 还必须满足下文的明确成功文本。正常路径得到 `applied` 后不追加 Inspect/List 验证；只有安装前检查和 journal Recover 调用 Inspect。

### CLI 映射

| Agent | stdio Add | HTTP Add | Remove |
| --- | --- | --- | --- |
| Codex | `codex mcp add <name> -- <command> <args...>` | `codex mcp add <name> --url <url>` | `codex mcp remove <name>` |
| Claude Code | `claude mcp add --scope user <name> -- <command> <args...>` | `claude mcp add --scope user --transport http <name> <url>` | `claude mcp remove --scope user <name>` |
| Hermes Agent | `hermes mcp add <name> --command <command> --args <args...>` | `hermes mcp add <name> --url <url>` | `hermes mcp remove <name>` |

Claude Code 固定使用 user scope。Inspect 必须确认 APM 管理的 user scope；如果 CLI 输出只显示 local/project 同名项，或无法确认 scope，则返回错误而不是 absent。APM 不读取、不覆盖、不删除 local/project 配置。

stdio `args` 为空时，Adapter 省略 Hermes 的 `--args`，并在 Codex/Claude 的 `--` 后只传 command。非空 args 始终按切片元素逐项追加，不重新拆词。

### Inspect

- Codex 使用 `codex mcp get --json <name>`，只有成功解析符合预期的 JSON 才返回 present；明确的 not-found 契约返回 absent；
- Claude Code 使用 `claude mcp get <name>`，严格解析支持版本的存在性和 scope 标记；明确不存在时返回 absent；
- Hermes 使用 `hermes mcp list`，去除 ANSI 显示控制后严格解析完整表格或明确的空列表输出；只有精确名称行返回 present；
- 非零退出不自动等价于 absent；只有各 CLI 的明确 not-found 输出才可返回 absent；
- 输出为空、截断、格式变化、重复/歧义匹配、健康检查错误掩盖配置状态或 scope 不清晰时返回 error；
- 所有安装前同名检查和 MCP journal 恢复都必须通过这些目标 Agent CLI，不得回退到配置文件解析。

### Hermes 非交互适配

Hermes 0.16.0 的 Add/Remove 内部仍包含交互提示且部分失败可能返回退出码 0。首版采用受控固定回答：

- stdio Add：对成功发现后的“启用全部工具”或“无工具仍保存”使用默认肯定；连接失败后的“仍然保存”使用默认否定；
- HTTP Add：首先固定回答“不需要认证”，其余保存与工具选择规则同 stdio；
- Remove：固定确认删除；
- 不启用认证、环境变量、preset 或自定义工具筛选；
- Add 只有输出包含该名称的明确 `Saved` 成功标记才返回 `applied`；
- Remove 只有输出包含该名称的明确 `Removed` 成功标记才返回 `applied`；
- 正常 Add/Remove 返回后不追加执行 `hermes mcp list` 验证；
- 首次安装前检查和 journal Recover 仍使用 `hermes mcp list`；
- 提示流程变化、成功标记缺失或超时返回 `unknown`，而不是根据进程退出码猜测成功。

## Manifest 纳管与漂移规则

APM manifest 的 `mcps` 分组继续记录从 APM 成功安装的精确 `PowerRef`。任何“删除清单”均严格指 `Remove(agent, mcp, name)` 删除对应 MCP 条目；不得删除 manifest 文件，也不得影响其他 Skill、Plugin 或 MCP 条目。

同名规则：

- manifest 无记录而 Agent CLI 明确返回 present：清单外冲突，安装失败，不覆盖、不接管；
- manifest 无记录而 Agent CLI 返回 absent：允许首次安装；
- manifest 有记录而 CLI 返回 present：视为 APM 纳管身份，可以幂等、升级、降级或卸载；
- manifest 有记录而 CLI 返回 absent：视为陈旧清单，只删除对应 MCP 条目；
- Inspect 不确定：保持 manifest 和 Agent 配置不变并失败。

Claude/Hermes 没有供 APM 稳定反向读取完整定义的结构化契约，因此 APM 不比较 Agent 当前定义与 Cloud 定义。用户在 Agent 侧手工修改一个已由 APM 纳管的同名 MCP 后，后续 APM 升级或卸载仍会替换或删除这个同名配置。外部修改与 APM 操作并发不在一致性保证范围内。

## Install 业务流程

### 顶层流程

Install 保持 Feature 010 的一个全局锁区间：Recover、同步恢复日志、Agent 刷新、目标解析、Cloud 访问和全部 Agent 串行事务都在同一次 Acquire/Release 内完成。Skill 分支完全保持既有 Cache/Staging 流程；MCP 分支不得调用 Cache。

单 Agent MCP 安装固定执行：

1. `Registry.ResolveMCP(agent)`；
2. `Config.Find(agent, mcp, name)`；
3. `MCPAdapter.Inspect(ctx, name)`；
4. 根据下表决定是否获取 Cloud 定义与调用 MCP Transaction；
5. 成功后按既有规则分类 installed/upgraded/downgraded/unchanged。

| Manifest | CLI 状态 | 行为 |
| --- | --- | --- |
| 无记录 | absent | `GetMCP(目标版本)`，执行首次安装事务 |
| 无记录 | present | 清单外冲突；不读取定义、不覆盖 |
| 同版本 | present | 返回 `unchanged`，不读取定义、不写状态 |
| 同版本 | absent | Transaction 仅删除对应陈旧 MCP 条目，返回 `ErrStaleManifest` |
| 不同版本 | present | 先获取旧、新精确定义，再执行升级或降级事务 |
| 不同版本 | absent | Transaction 仅删除对应陈旧 MCP 条目，返回 `ErrStaleManifest` |
| 任意 | error | fail closed，不修改 Agent 配置或 manifest |

Transaction 在写入 journal 前再次 Inspect，防御生命周期检查后的外部变化：

- 首次安装预期 absent，若复查 present 则返回清单外冲突；
- manifest 有旧记录预期 present，若复查 absent 则只移除对应清单项并返回陈旧清单错误；
- 生命周期判断 absent 后、Transaction 复查又 present 且请求没有完整定义时，Transaction 返回错误且不删除 manifest；
- 所需定义只在实际 CLI 变更路径中强制要求，漂移修复和冲突路径不创建 journal。

陈旧清单修复成功后本次 Install 仍失败，不在同一次命令中自动重装；用户重试后按首次安装处理。`--agent=all` 继续把当前 Agent 标为 failed，并把后续 Agent 标为 unexecuted。

## Uninstall 业务流程

Uninstall 保持 Feature 011 的一个全局锁区间和逐 Agent 本地解析规则。单 Agent MCP 卸载固定执行：

1. 从该 Agent manifest 解析完整 `PowerRef`；
2. 清单无记录时保持现有同步 Warn、Agent-only `unexecuted`，不调用 Inspect 或 Cloud；
3. 有记录时解析 MCP Adapter 并执行 Inspect；
4. present 时按清单精确版本调用 `GetMCP`，再执行 MCP 卸载事务；
5. absent 时 Transaction 只删除对应 MCP 清单项，返回 `TargetMissing=true`；
6. Inspect error 或 present 时 Cloud 定义读取失败，保持状态并失败。

Lifecycle 继续即时消费 `TargetMissing`：只要为 true，即使同时返回后续错误，也在持锁状态同步记录完整 PowerRef Warn。成功移除陈旧条目返回完整引用 `completed`。正常卸载的定义在创建 journal 前已获取；Agent CLI 写操作开始后，恢复只依赖 journal，不再访问 Cloud。

## 集中事务与 Journal

### 唯一活动 journal

Skill 与 MCP 不使用两套独立 journal。系统继续只有一个全局活动文件 `transactions/active.json`，采用“共享信封 + 类型专属载荷”：

```text
active.json
├── 通用字段：schema、transaction_id、agent、operation、phase、power
└── driver
    ├── filesystem：target、staging、backup/quarantine
    └── agent-cli：旧/新 MCP 定义和 CLI 变更阶段
```

兼容规则：

- 现有 schema v1 被明确识别为 legacy filesystem Skill journal，并继续可恢复；
- 新事务写入 schema v2，必须显式包含 `driver=filesystem` 或 `driver=agent-cli`；
- v2 必须且只能包含与 driver 匹配的一份载荷；
- 不通过字段是否为空猜测 driver；
- 未知 schema/driver、矛盾 phase、缺失定义或两份载荷同时存在均 fail closed 并保留 journal；
- 实现完成后新 Skill 与 MCP 事务均写 v2，v1 只读兼容。

任一时刻只能存在一个活动 journal。正常的多 Agent 批处理必须在前一个 Agent 事务完成并删除 journal 后，才可开始下一个 Agent；不建立多个 journal 的队列。

### TransactionManager 接口

现有 Skill `Install/Uninstall` 行为保持不变。Manager 增加 MCP 专属请求与方法，不把 Skill 文件路径和 MCP CLI 定义塞入同一个请求：

```go
type MCPInstallRequest struct {
    Agent          domain.AgentID
    Power          domain.PowerRef
    Previous       *domain.PowerRef
    Server         *domain.MCPServer
    PreviousServer *domain.MCPServer
    Adapter        adapter.MCPAdapter
}

type MCPUninstallRequest struct {
    Agent   domain.AgentID
    Power   domain.PowerRef
    Server  *domain.MCPServer
    Adapter adapter.MCPAdapter
}

InstallMCP(context.Context, MCPInstallRequest) error
UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
```

请求字段约束如下：

- `Power` 始终是版本已确定的 MCP 引用；`Previous` 非 nil 时必须与 Power 同 Type/Name；
- 首次安装正常路径要求 `Server` 非 nil、`Previous` 和 `PreviousServer` 为 nil；
- 升级/降级正常路径要求 `Previous`、`PreviousServer` 和 `Server` 均非 nil，且旧、新版本不同；
- Uninstall 正常 present 路径要求 `Server` 非 nil；
- 漂移或清单外冲突路径允许定义为 nil，以便 Transaction 防御性复查后在创建 journal 前完成单条修复或拒绝；
- 一旦实际状态要求 CLI 写操作，缺少任一所需定义都返回无副作用的无效请求错误；
- Transaction 在写 journal 时复制引用和定义值，不保留 lifecycle 所持指针。

TransactionManager 在组合根中额外接收窄的 MCP Adapter Resolver。正常 lifecycle 传入已解析 Adapter；Recover 根据 journal Agent ID 重新解析 Adapter，不依赖原进程对象，也不从 journal 执行绝对 CLI 路径或预拼 argv。

### MCP 安装阶段

首次安装使用：

```text
preparing → adding-new → committing
                   ↘ rolling-back
```

升级或降级使用：

```text
preparing → removing-old → adding-new → committing
                    ↘         ↘ rolling-back
```

规则如下：

1. 获取和校验所有所需定义、复查 CLI 状态后创建 journal；
2. 每次 Add/Remove 前先持久化对应意图 phase；
3. 首次安装 Add 返回 `applied` 后进入 committing；`not-applied` 结束回滚并保留原 manifest；`unknown` 保留 journal；
4. 升级/降级 Remove 旧配置返回 `applied` 后持久化 `adding-new`；明确未执行则结束回滚并保留旧 manifest；unknown 保留 journal；
5. Add 新配置 applied 后进入 committing；明确未执行则用 journal 中旧定义恢复旧配置；unknown 保留 journal；
6. committing 使用 `Set(agent,newRef)`，随后清理并删除 journal；
7. rolling-back 的目标是恢复旧配置和旧 manifest；恢复 Add 不确定时继续保留 journal；
8. Agent CLI 已明确发生副作用后的 manifest/清理/journal 删除错误不得反向假定 CLI 未执行。

### MCP 卸载阶段

正常卸载使用：

```text
removing → committing
      ↘ rolling-back
```

规则如下：

1. present 且旧定义已获取时，先创建 removing journal，再调用 Remove；
2. Remove applied 后进入 committing，执行 `Remove(agent,mcp,name)`；
3. Remove 明确未执行时结束回滚，保留旧配置与旧 manifest，并返回错误；
4. Remove unknown 时保留 journal；
5. committing 的清单删除、清理或 journal 删除失败保留提交方向，后续 Recover 重试；
6. 写操作前复查 absent 的漂移路径无需创建 journal，只原子删除对应 manifest 条目并返回 `TargetMissing=true`；
7. 所有清单删除都只删除对应 MCP 条目，不删除整个 manifest。

### 跨类型串行恢复

每条选择共享状态恢复阶段的新命令按固定顺序执行：

1. 获取全局锁；
2. Inspect 唯一活动 journal；
3. 根据 driver 路由到 filesystem Skill 恢复器或 agent-cli MCP 恢复器；
4. 恢复成功并删除 journal；
5. 才继续执行当前命令。

因此 MCP 中断后可以由 Skill 命令先恢复，Skill 中断后也可以由 MCP 命令先恢复。恢复失败或不可判定会阻止当前命令，不允许跳过旧 journal 创建新事务。

### MCP 恢复判定

Recover 只通过目标 Agent CLI Inspect 当前同名状态，并结合已持久化 phase 决定：

- 首次安装 `adding-new`：present 表示新配置已添加，转 committing；absent 表示未添加，结束回滚；
- 升级/降级 `removing-old`：present 表示旧配置仍在，结束回滚；absent 表示旧配置已移除，使用 journal 旧定义恢复并回滚；
- 升级/降级 `adding-new`：present 表示新配置已添加，转 committing；absent 表示新配置未添加，使用 journal 旧定义恢复并回滚；
- 卸载 `removing`：absent 表示删除已发生，转 committing；present 表示删除未发生，结束回滚并保留旧 manifest；
- `committing`：只补全对应 `Set/Remove`、清理和 journal 删除；
- `rolling-back`：确保旧配置存在并保留旧 manifest，再删除 journal；
- Inspect error：返回不可判定错误，保留 journal 和现场。

Claude/Hermes 无法稳定返回完整定义，因此恢复阶段以“同名存在”结合 phase 推断旧/新身份。该推断依赖 APM 全局锁串行 APM 自身，并明确排除外部并发修改；出现无法由 phase 解释的状态时 fail closed。

## 错误与日志

新增稳定错误类别至少区分：

- 不支持的 Agent/Power Adapter；
- Agent CLI 不可用；
- MCP 状态不确定；
- 清单外 MCP 冲突；
- 非法 MCP 定义；
- MCP CLI 变更结果不确定；
- journal schema/driver/phase 不合法；
- 不可判定的恢复状态。

错误必须保留 Agent、Power、Version、phase、CLI 操作和底层 cause 上下文，但不得记录未受限的外部输出。Adapter 和 Transaction 不打印业务输出，不记录普通 Error；内部阶段只使用包级 `slog.Debug`。恢复、未安装和目标缺失 Warn 继续由 lifecycle 在既有锁内位置同步记录；普通 Error 仍由 CLI 统一记录一次。

MCP 定义不允许密钥字段。活动 journal 延续现有严格权限和原子写入要求。命令和 args 可能出现在 Debug 或错误时只记录必要摘要，不把完整不可信 stdout/stderr 或潜在敏感参数无界写入日志。

## 测试与验收

### 真实环境隔离是强制条件

自动化测试不得调用真实 Codex、Claude Code 或 Hermes CLI，不得修改用户真实 Agent 配置：

- 在 `t.TempDir()` 创建假的 `codex`、`claude`、`hermes` 可执行文件；
- 通过临时 `PATH` 或可注入 LookPath/Runner 只解析 fake-bin；
- 每次启动前断言解析路径位于测试 fake-bin，越界则在启动前失败；
- 假 CLI 只把 argv、stdin 和调用次数记录到临时目录，并输出测试指定的 stdout/stderr/退出状态；
- 设置临时 `APM_HOME`、`CODEX_HOME`、`CLAUDE_CONFIG_DIR` 和 `HERMES_HOME`；
- Cloud 资源、manifest、journal 和所有记录文件均位于测试临时根；
- 修改环境变量的测试不得使用 `t.Parallel()`；
- 自动 integration test 明确禁止使用本机真实 CLI 作为 target；
- 真实 CLI 仅可由开发者人工执行只读 `--help` 核对，不执行 Add/Remove。

### Cloud 与领域模型

- stdio/http 有效 JSON、空 args 和 Unicode argv；
- 缺失/未知 transport、互斥字段共存、空 command、非法 URL、未知字段、错误类型和尾随 JSON；
- MCP marker 发现、普通文件约束、精确版本 GetMCP、稳定 List 排序；
- MCP GetMCP 不创建 Cache/Staging，不调用 Skill Get；
- Skill 现有 List/Get/Cache 测试全部保持通过。

### Adapter 与 CLI

- 三个 Agent 的 stdio/HTTP/Remove 精确 argv；
- Claude 固定 user scope，并拒绝 scope 不清晰或仅 local/project 的状态；
- LookPath 缺失、不可执行、启动失败、context 取消和 30 秒超时；
- Codex JSON、Claude 文本和 Hermes 表格的 present/absent/歧义/格式变化；
- 非零退出不被笼统映射为 absent；
- stdout/stderr 不进入 APM 业务 stdout，诊断输出有界；
- argv 不经过 shell，特殊 args 保持单独参数；
- Hermes stdio/HTTP/Remove 的固定 stdin、Saved/Removed 标记、连接失败、提示变化和退出码 0 失败；
- Hermes 正常 Add/Remove 后没有额外 list 调用，Inspect/Recover 仍调用 list。

### Lifecycle 与 Manifest

- MCP 首次 installed、同版本 present unchanged、升级、显式降级；
- manifest 无记录但 CLI present 的清单外冲突；
- manifest 有记录但 CLI absent 时只删除对应 MCP 条目，其他条目和文件保留；
- Install 漂移修复返回 failed/ErrStaleManifest，不自动重装；
- Uninstall 漂移修复同步 Warn、TargetMissing 和 completed；
- Inspect error 与 Cloud old/new GetMCP error 均无 Agent/manifest 变更；
- `--agent=all` 字典序、每 Agent 独立 Fetch 定义、首错 failed/unexecuted；
- MCP 分支不调用 Cache/Fetch/Staging；Skill 分支调用序列完全不变；
- 现有 CLI 参数、输出、Release 抑制和 Error 出口回归。

### Transaction 与恢复

- 首次安装、升级、降级、卸载每个 phase 的中断注入；
- applied/not-applied/unknown 三态及 error 同时返回；
- 每次 CLI 写操作前 phase 已持久化；
- 提交只 Set/Remove 对应 MCP 条目，回滚恢复旧配置和旧 manifest；
- 重复 Recover 幂等，失败保留唯一 journal；
- schema v1 Skill journal 继续恢复，v2 filesystem 与 agent-cli 正确路由；
- 未知 driver、双载荷、缺失定义、非法 phase 和矛盾字段 fail closed；
- MCP journal 可由 Skill 命令先恢复，Skill journal 可由 MCP 命令先恢复；
- 前一事务未删除时拒绝任何下一事务；多 Agent 始终串行；
- 外部竞态模拟产生不可解释状态时保留现场。

### 最终验证

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

最终还必须静态或通过测试证明：测试解析的三个 Agent CLI 全部位于临时 fake-bin；测试没有读写真实 HOME、真实 Agent 根或真实 Agent 配置。

## 风险与控制

- Agent CLI 文本输出可能随版本变化：解析器严格绑定支持契约，无法确认时 fail closed；测试固定覆盖格式变化。
- Hermes 缺少正式非交互和结构化状态参数：使用固定输入和明确成功标记，正常操作后按批准范围跳过 list；unknown 结果由 journal 恢复。
- MCP CLI 副作用没有文件 rename 提交点：每次写前持久化 phase，以三态 mutation 结果和后续 Inspect 判定提交/回滚方向。
- Claude/Hermes 无法反向验证完整定义：APM 以 manifest 中的名称身份纳管，恢复依赖 phase；外部并发修改明确不在保证范围内。
- 每次 CLI 最长 30 秒且在全局锁内执行，可能拉长锁持有时间：首版优先一致性，不引入并行或可配置 timeout。
- schema v2 会触及稳定 Skill journal：保留 v1 只读恢复兼容，并以跨版本 fixture 和现有 transaction 全量回归控制风险。
- Cloud 定义中的命令具有执行能力：首版信任 Cloud 契约，始终以 argv 调用且不经 shell；来源信任、签名与审计留待后续设计。

## 文档与决策

本 feature 的重大决策由 [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md) 记录。实现完成时应在项目概览、C4 组件和生命周期流程、BUILD 及相关 Feature 006/007/010/011 文档中引用 ADR-010，并把“当前仅支持 Skill Adapter”的历史状态更新为实际支持矩阵。

实现属于行为变更，完成后按仓库规则在 `docs/Changelog.md` 增加一条覆盖本用户请求的记录；当前设计与文档提交本身不新增 Changelog 条目。

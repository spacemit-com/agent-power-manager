# Lifecycle Plugin 设计规格

> 状态：设计已于 2026-08-13 确认，尚未进入实现。

## 目标

在不改变 APM 现有通用 CLI、Agent 快照、全局锁、版本选择、批处理结果和安装清单外部模型的前提下，为 Codex、Claude Code 和 Hermes Agent 增加 Plugin 的搜索、列出、安装、升级、降级、卸载和中断恢复能力。

Codex 与 Claude Code 通过 Cloud 托管的稳定全量 marketplace 安装插件。marketplace 本身没有版本，包含所有插件的所有版本；同一逻辑插件的不同版本通过实际插件名区分。Hermes 不支持 marketplace，因此由 APM 将同一插件资源经 Cache/Staging 发布到 `~/.hermes/plugins/`，再通过 Hermes CLI 启用或停用。

Plugin 与 Skill、MCP 使用同一套 lifecycle 外壳、Agent 批处理、清单 Repository、唯一活动 journal 和恢复预检，但使用独立的 Plugin Adapter、Plugin transaction driver 和 Plugin journal payload，避免把 marketplace、CLI selector 或 Hermes 文件路径混入既有 Skill/MCP 模型。

本 feature 延续 [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md) 的类型专属 driver 决策和 [ADR-011](../../adr/ADR-011-reset-journal-schema-before-release.md) 的唯一 schema v1 决策。实现阶段应新增 ADR-012，记录稳定全量 marketplace、版本化实际名、Hermes 文件发布和 Plugin 专属 driver 的组合决策。

## 范围

### 包含

- 通过既有 `search/list/install/uninstall --type=plugin` 提供端到端 Plugin lifecycle；
- `.claude-plugin/marketplace.json` 作为 Cloud Plugin 的唯一权威索引；
- `.agents/plugins/marketplace.json` 与 `.claude-plugin/marketplace.json` 共处一个 marketplace 根目录；
- Cloud 返回逻辑 ref、实际插件名、marketplace 名称和安装 source；
- 插件原生 SemVer 与 APM 规范 `vMAJOR.MINOR.PATCH` 版本之间的转换；
- Codex/Claude marketplace 首次注册、后续定向更新和 source 冲突防护；
- Codex/Claude 的用户级插件安装与卸载；
- Hermes 的版本化 Cache/Staging、原子目录发布和 CLI enable/disable；
- 首装、相同版本幂等、目标缺失修复、升级、显式降级和卸载；
- APM 清单外插件冲突防护、清单内目标缺失警告和单条清单项删除；
- Plugin 独立 Adapter、transaction driver、journal payload 和跨命令恢复；
- 完全隔离真实 Agent home 和真实 Agent CLI 的自动化测试；
- ADR、C4、项目概览、构建指南和 feature 文档同步。

### 不包含

- 新增 Plugin 专属 CLI 子命令或改变现有 CLI 输出格式；
- Claude Code 的 project/local scope；
- marketplace 自动移除、垃圾回收或版本管理；
- Hermes `plugins install/remove` 的 Git 安装流程；
- 持久化或恢复用户手工设置的 enabled/disabled 状态；
- 安装成功后的 `plugin list` 二次验证；
- 解析 Codex marketplace，或交叉校验两套 marketplace/manifest；
- APM 参与 Cloud 发布规范检查、资源签名、授权、认证或 marketplace 生成；
- 同一命令内多 Agent 原子回滚、多个活动 journal 或事务队列；
- 外部进程在 APM 持锁期间修改 Agent 配置或插件目录的无竞态保证；
- 复制 NVIDIA 插件内容、品牌或资产到 APM。

## 用户体验与版本映射

用户继续使用与 Skill 相同的逻辑名称和版本体验：

```text
apm search demo-plugin --type=plugin
apm list --type=plugin
apm list --installed --agent=codex --type=plugin
apm install demo-plugin=v0.0.1 --agent=codex --type=plugin
apm uninstall demo-plugin --agent=codex --type=plugin
```

一个版本在不同边界的表示如下：

| 边界 | 名称 | 版本 |
|---|---|---|
| APM CLI / `PowerRef` / manifest | `demo-plugin` | `v0.0.1` |
| Claude marketplace 条目 | `demo-plugin-v0.0.1` | `0.0.1` |
| Codex/Claude plugin manifest | `demo-plugin-v0.0.1` | `0.0.1` |
| Codex/Claude CLI selector | `demo-plugin-v0.0.1@<marketplace>` | 由实际名固定 |
| Hermes 目录和 CLI 名称 | `demo-plugin-v0.0.1` | 由实际名固定 |

映射规则唯一且可逆：

```text
actualName = logicalName + "-" + canonicalAPMVersion
demo-plugin + v0.0.1 = demo-plugin-v0.0.1
```

Cloud 读取 marketplace 条目的原生 `version: "0.0.1"`，规范化为 APM `v0.0.1`，然后要求实际名具有精确后缀 `-v0.0.1`。剥离该后缀后必须得到非空、安全且未经清洗的逻辑名。APM 不接受路径分隔符、`.`、`..`、绝对路径、非规范 SemVer 或经过清洗才合法的名称。

同一 marketplace 通过实际名保存同一逻辑插件的所有版本，例如：

```text
demo-plugin-v0.0.1
demo-plugin-v0.1.0
demo-plugin-v1.0.0
```

Agent 原生命令不需要 `--version`；APM 根据用户的逻辑版本选择唯一实际插件名。

## Cloud 资源模型

### 本地资源布局

开发期 Local Cloud 增加以下资源：

```text
internal/cloud/resources/plugins/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/
    └── demo-plugin-v0.0.1/
        ├── .codex-plugin/plugin.json
        ├── .claude-plugin/plugin.json
        └── skills/
            └── demo-plugin/
                └── SKILL.md
```

目录只参考 `~/Workspace/projects/bianbu-next/reference/source/nvidia-agent-skills` 的 marketplace 和插件包布局。示例插件使用 APM 自有的最小内容，不复制 NVIDIA 品牌、图片、README 或技能内容；插件包内使用真实文件，不使用 symlink。

### 权威索引

APM 只解析 `.claude-plugin/marketplace.json`。Local Cloud 所需的最小字段是：

```json
{
  "name": "apm",
  "plugins": [
    {
      "name": "demo-plugin-v0.0.1",
      "version": "0.0.1",
      "source": "./plugins/demo-plugin-v0.0.1"
    }
  ]
}
```

未知的合法原生字段可以忽略。APM 不读取 `.agents/plugins/marketplace.json`，不比较两个 marketplace，也不比较 `.codex-plugin/plugin.json` 与 `.claude-plugin/plugin.json`。两套文件的一致性、名称合规性和发布质量由 Cloud 管理方保证。

存在权威索引但 JSON 无法解码、关键字段为空、版本不规范、映射不可逆、source 逃逸资源根或出现重复逻辑 ref 时，Local Cloud 返回带路径上下文的错误，不返回部分 Plugin 列表。权威索引不存在时，Plugin 列表为空；精确 `GetPlugin` 返回 `cloud.ErrNotFound`。

### Cloud 接口

`cloud.Client` 在既有接口上增加：

```go
GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
```

`PluginDefinition` 只包含安装编排需要的事实：实际插件名、marketplace 名称和 marketplace source。逻辑 `PowerRef` 仍由调用参数携带，不复制进长期 manifest。

生产 Cloud 返回托管 marketplace 的 Git/HTTPS source；Local Cloud 返回 `internal/cloud/resources/plugins` 的绝对路径。Adapter 不推断或硬编码 Cloud source。

既有 `Get(ref)` 扩展为：

- Skill：保持现有版本化资源写入 Cache 的行为；
- Plugin：按权威条目的 `source` 将插件包原子写入 `cache/plugins/<logical-name>/<version>`；
- MCP：继续返回 `ErrNotFound`，不进入 Cache。

只有 Hermes 的 lifecycle 会为 Plugin 调用 `Get(ref)`。Codex/Claude 只使用 `GetPlugin(ref)` 返回的 marketplace definition。

## Cache 与 Staging

`cache.PowerCache.Fetch(ref)` 支持 Skill 和 Plugin：

```text
cache/skills/<name>/<version>  -> staging/skills/<name>
cache/plugins/<name>/<version> -> staging/plugins/<name>
```

每次 Fetch 继续构建干净 staging，并返回公共 staging 根。Plugin staging 叶子使用逻辑名，Agent 最终目标使用实际名；该差异由 transaction 请求和 Adapter 提供的目标路径显式表达。

Cache 不知道 marketplace、Agent、CLI 或 Hermes。MCP 继续返回 `cache.ErrMiss`。Codex/Claude Plugin 安装不得创建 Plugin Cache 或 staging。

## 领域模型

`internal/domain/plugin.go` 定义以下概念：

```go
type PluginDeploymentMode string

const (
    PluginDeploymentMarketplace PluginDeploymentMode = "marketplace"
    PluginDeploymentFilesystem  PluginDeploymentMode = "filesystem"
)

type MarketplaceDefinition struct {
    Name   string
    Source string
}

type PluginDefinition struct {
    ActualName  string
    Marketplace MarketplaceDefinition
}
```

同一文件提供以下纯规则：

- 规范化 marketplace 原生 SemVer 为 APM 版本；
- 从权威条目解析逻辑 `PowerRef`；
- 从逻辑 `PowerRef` 计算实际插件名；
- 校验 marketplace、actual name 和 source 的必要非空约束；
- clone 需要持久化或跨层传递的值，避免调用方共享可变状态。

领域层不包含 CLI 命令、Agent home、Cache 路径或 Cloud 文件路径。

## Adapter 架构

### 公共契约

新增独立的 `PluginAdapter`，不向 Skill `Adapter` 或 `MCPAdapter` 塞入可选方法：

```go
type PluginAdapter interface {
    DeploymentMode() domain.PluginDeploymentMode
    PrepareInstall(context.Context, domain.PluginDefinition) error
    Inspect(context.Context, string) (PluginFact, error)
    Add(context.Context, PluginTarget) (PluginMutationResult, error)
    Remove(context.Context, PluginTarget) (PluginMutationResult, error)
    TargetPath(string) (string, error)
}
```

其中：

- `PrepareInstall` 只服务安装。Codex/Claude 注册或更新 marketplace；Hermes 为 no-op；
- `Inspect` 输入实际插件名，只返回 `present/absent`。present 时携带 CLI 列表给出的完整 selector；
- `Inspect` 忽略 enabled/status 字段，不把 disabled 当作 absent；
- `Add/Remove` 表示事务中的“使插件存在/使插件不存在”动作：Codex/Claude 为 install/uninstall，Hermes 为 enable/disable；
- `PluginMutationResult` 与 MCP 相同，区分 `applied/not-applied/unknown`；
- `TargetPath` 仅对 filesystem deployment 返回 Hermes 绝对路径；marketplace deployment 返回明确的不支持错误；
- transaction/lifecycle 根据 deployment mode 分支，不根据 Agent ID 分支。

Plugin 注册表与现有类型并列：

```text
ResolveSkill(agent)  -> Skill Adapter
ResolveMCP(agent)    -> MCP Adapter
ResolvePlugin(agent) -> Plugin Adapter
```

`RefreshInstalled` 仍只检查 Agent 根目录，不运行任何 Plugin CLI。

### 通用 Agent CLI Runner

将既有 `mcp_runner.go` 提炼为 `cli_runner.go`，由 MCP 与 Plugin 共用：

- 每次通过 `exec.LookPath` 动态解析 CLI；
- argv 数组直接调用，不经过 shell；
- 30 秒超时；
- 超时时终止整个进程组；
- stdout/stderr 分离并各自限制容量；
- 保留 started、exit code、timeout 和 truncation 事实；
- 未启动映射为 `ErrAgentCLIUnavailable`；
- 已启动但输出截断或结果不可判定映射为类型专属 unknown 错误。

该重命名是内部职责修正，不改变 MCP 命令、超时、错误或测试契约。

### Codex Adapter

安装准备：

```text
codex plugin marketplace list --json
codex plugin marketplace add <source> --json
codex plugin marketplace upgrade <marketplace> --json
```

插件操作：

```text
codex plugin list --json
codex plugin add <actual-name>@<marketplace> --json
codex plugin remove <selector> --json
```

只更新 Cloud definition 指定的 marketplace，不执行无名称的全量 upgrade。

### Claude Code Adapter

安装准备固定为 user scope：

```text
claude plugin marketplace list --json
claude plugin marketplace add <source> --scope user
claude plugin marketplace update <marketplace>
```

插件操作：

```text
claude plugin list --json
claude plugin install <actual-name>@<marketplace> --scope user
claude plugin uninstall <selector> --scope user
```

本 feature 不根据当前工作目录改变 scope。

### Hermes Adapter

Hermes 不执行 marketplace 命令，也不使用原生 Git install/remove：

```text
hermes plugins list --json
hermes plugins enable <actual-name>
hermes plugins disable <actual-name>
```

`TargetPath(actualName)` 返回 `<HERMES_HOME|~/.hermes>/plugins/<actual-name>`。enable/disable 的已满足结果按幂等成功处理；APM 不记录也不恢复用户手工设置的 enabled 状态。

### Marketplace 准备规则

Codex/Claude 每次 Plugin 安装均执行准备，包括最终为 unchanged 的安装：

| 当前状态 | 动作 |
|---|---|
| marketplace 不存在 | add Cloud source |
| 名称存在且规范化 source 相同 | 只更新该 marketplace |
| 名称存在但 source 不同 | `ErrMarketplaceConflict`，停止 |
| CLI 未返回足够 source 事实 | `ErrPluginStateUnknown`，停止 |

source 比较只做无歧义规范化：本地路径转为绝对 clean path；非本地 source 去除首尾空白后按 CLI 返回字符串精确比较。APM 不猜测 `owner/repo`、HTTPS Git URL、SSH Git URL、尾随 `.git` 或不同 Git ref 是否等价。

add/update 失败时，不创建 Plugin transaction journal，不调用插件 install/remove，不修改 APM manifest。marketplace 操作即使已经发生也不回滚；APM 永不自动移除 marketplace。

## Lifecycle 职责

外层 Install/Uninstall 保持现有锁、恢复、快照、版本选择、Agent 顺序、fail-fast、Release 和渲染边界。新增文件只实现类型专属业务，不复制外层 lifecycle。

### 安装顺序

单个 Agent 的 Plugin 安装依次执行：

1. `ResolvePlugin(agent)`；
2. `config.Find(agent, plugin, logicalName)`；
3. `cloud.GetPlugin(targetRef)`；
4. `adapter.PrepareInstall(definition)`；
5. 通过 CLI Inspect 目标实际名；有旧清单且版本不同，再 Inspect 旧实际名；
6. 根据状态表决定 unchanged、conflict 或需要事务；
7. 仅 filesystem deployment 且确实需要新增目标时执行 Cache Fetch；miss 时 Cloud Get 后再次 Fetch；
8. 调用 `InstallPlugin` transaction；
9. 根据逻辑新旧版本返回 installed/upgraded/downgraded/unchanged。

marketplace 准备是共享前置条件，不属于某个插件 transaction。Plugin install 后不再运行 list 验证；命令退出与 mutation result 是本次写操作的成功事实。

### 安装状态表

| APM 清单 | 目标实际插件 | 旧实际插件 | 结果 |
|---|---|---|---|
| 无 | absent | 不适用 | 首次安装 |
| 无 | present | 不适用 | unmanaged conflict，不接管 |
| 同版本 | present | 同一目标 | unchanged；不关心 enabled |
| 同版本 | absent | 同一目标 | 修复安装；清单保持同版本，结果 unchanged |
| 不同版本 | present | 任意 | unmanaged target conflict |
| 不同版本 | absent | present | 先撤旧，再装新 |
| 不同版本 | absent | absent | 直接装新并更新原有逻辑清单项 |

与目标 ref 和清单旧 ref 均无关的其他版本化实际插件不检查、不删除、不接管。

transaction 在创建 journal 前再次使用 Adapter 复核所需 present/absent 事实，以缩小 lifecycle 判断与真实副作用之间的竞态窗口。该复核不是安装后的 list 验证。

### 卸载顺序

Plugin 卸载完全离线：

1. 沿用现有每 Agent manifest 解析，得到精确逻辑 ref；
2. 按领域规则计算实际名；
3. `ResolvePlugin(agent)`；
4. `Inspect(actualName)`；
5. absent 时警告 target missing，并只删除 `Remove(agent, plugin, logicalName)` 对应清单项；
6. present 时把 CLI 返回的完整 selector 交给 `UninstallPlugin` transaction；
7. 成功后只删除对应 Plugin 清单项。

卸载不调用 Cloud、不更新或注册 marketplace、不访问 Cache。Codex/Claude 的完整 selector 从 installed-list 事实获得，而非依赖 Cloud；若同一实际名解析出多个 selector，则返回状态不明确错误，不猜测来源。marketplace 和其他 Plugin/Skill/MCP 清单项始终保留。

## Transaction 与 Journal

### 共享信封与专属 payload

系统继续只有一个活动文件 `transactions/active.json`。新增：

```text
driver = plugin
payload = plugin
schema_version = 1
```

`PluginJournalPayload` 保存：

- deployment mode；
- 新旧实际插件名和完整 selector；
- 事务开始时的新旧 present 事实；
- filesystem deployment 的 staging/source/新旧目标/事务目录/发布临时目录/隔离目录；
- 恢复所需的全部数据。

Plugin payload 不保存 Cloud source、插件内容、enabled 状态或 MCP server。`filesystem`、`agent-cli`、`plugin` 三种 driver 的 payload 严格互斥。

当前项目仍处于开发阶段，不增加旧 Plugin journal 兼容层。所有 producer 写 schema v1；任何未知字段组合、错误 driver/payload、非法 phase、非规范 ref、selector、相对路径、路径逃逸、非受控事务产物或矛盾状态均返回 `ErrInvalidJournal` 并保留原文件。

### 安装 phase

```text
首次安装：adding-new -> committing
版本切换：removing-old -> adding-new -> committing
失败回滚：任意可回滚 phase -> rolling-back
```

marketplace deployment：

- `removing-old`：调用旧 selector 的 Remove；
- `adding-new`：调用新 selector 的 Add；
- `committing`：Set 新逻辑 `PowerRef`，删除 journal。

filesystem deployment：

- 在 `removing-old` 中 disable 旧实际名，并将旧目录 rename 到同盘私有隔离目录；旧目标原本 absent 时跳过；
- 在 `adding-new` 中把 staging 叶子复制到目标同父目录的私有 publish 临时目录，再原子 rename 到新实际名目标，随后 enable 新实际名；
- 在 `committing` 中 Set 新逻辑 `PowerRef`，删除旧隔离目录、staging 和本事务目录，最后删除 journal。

任何 CLI mutation 明确 `not-applied` 时进入回滚；明确 `applied` 后持久化下一 phase；`unknown` 时不猜测、不清 journal，等待下一条恢复型命令。

### 卸载 phase

```text
removing -> committing
失败回滚：removing -> rolling-back
```

marketplace deployment 调用完整 selector 的 Remove。filesystem deployment 先 disable，再将目标目录 rename 到本事务 quarantine。进入 `committing` 后删除单条 manifest、quarantine、事务目录和 journal。

目标在 transaction 复核时已 absent，则不创建 journal，返回 `TargetMissing=true` 并删除单条 manifest。

### 恢复规则

每个 Search、installed List、Install 或 Uninstall 的公共预检继续先恢复唯一 journal。恢复根据 driver 路由：

| Plugin 操作/phase | 事实 | 方向 |
|---|---|---|
| Install `removing-old` | 任意 | rollback，恢复事务前旧状态 |
| Install `adding-new` | 新目标 present | commit |
| Install `adding-new` | 新目标 absent | rollback |
| Install `rolling-back` | 任意 | rollback |
| Install `committing` | 任意 | commit manifest/cleanup |
| Uninstall `removing` | 目标 absent | commit |
| Uninstall `removing` | 目标 present | rollback |
| Uninstall `rolling-back` | 任意 | rollback |
| Uninstall `committing` | 任意 | commit manifest/cleanup |

marketplace recovery 只使用 journal selector 和已注册 marketplace，不连接 Cloud、不更新 marketplace。Hermes recovery 只使用 journal 受控路径和 Adapter：提交新目标前补执行 enable；回滚时移除未提交的新目标、恢复原本 present 的旧目录并 enable。旧目标原本 absent 时不得凭 manifest 虚构目录。

Skill、MCP、Plugin 不建立独立 journal 队列。任意类型遗留事务都由下一条恢复型命令先串行收敛；恢复失败阻止当前命令创建新事务。

## 错误模型

Adapter 增加以下稳定 sentinel：

- `ErrInvalidPlugin`：Plugin ref、actual name、selector 或 definition 不合法；
- `ErrMarketplaceConflict`：同名 marketplace 的 source 与 Cloud 不一致；
- `ErrPluginConflict`：未被 APM manifest 纳管的目标实际插件已存在；
- `ErrPluginStateUnknown`：列表失败、JSON 不完整/截断或 selector 不唯一；
- `ErrPluginMutationUnknown`：CLI 已启动但 mutation 结果无法确定。

未纳管冲突同时包裹现有 `ErrTargetConflict`，与 MCP 的错误组合保持一致。transaction 继续使用 `ErrInvalidRequest`、`ErrActiveTransaction`、`ErrIndeterminateState`、`ErrInvalidJournal` 和 `ErrRecoveryFailed`。

错误必须保留操作、Agent、逻辑 ref、实际名和底层 cause。Adapter/lifecycle/transaction 不直接打印 Error；同步业务 Warn 仍由 lifecycle 记录，最终 Error 与 Usage 仍由 CLI 唯一渲染。日志不输出环境变量、完整插件内容或潜在秘密。

## 文件系统安全

- logical name、actual name 和 transaction ID 都必须是安全的单路径段；
- Cloud `source` 必须在 marketplace 资源根内，且插件 marker 和包目录不得为 symlink；
- Hermes 根、plugins 根、staging、目标和事务产物必须通过物理路径与重叠检查；
- Hermes 事务产物位于 `~/.hermes/plugins/.apm-transactions/<transaction-id>/`，权限沿用私有事务目录约束；
- 只删除当前 journal 精确列出的 staging、publish、quarantine 和 transaction dir；
- 不递归删除 marketplace、Agent root、plugins root、APM home 或 Cache root；
- 外部进程竞态导致状态无法确认时保留 journal 并失败关闭。

## 测试策略

### 单元测试

- Domain：原生/规范版本转换、名称映射、不可逆后缀、路径逃逸和非法 SemVer；
- Cloud：缺失 marketplace、合法最小索引、稳定排序、精确 GetPlugin、重复 ref、损坏 JSON、逃逸/符号链接 source；
- Cache：Skill 行为不变、Plugin cache hit/miss/staging、MCP 继续拒绝；
- Adapter common：definition、fact、selector、mutation 分类和 truncated output；
- Codex/Claude/Hermes：精确 argv、JSON 解析、source conflict、present/absent、disabled 仍为 present、超时和不可判定错误；
- Lifecycle：安装状态表、marketplace 前置顺序、Hermes 条件 Cache、离线卸载、outcome 和 fail-fast；
- Transaction：每个 phase 的失败注入、applied/not-applied/unknown、manifest 失败、journal 失败、Hermes rename/cleanup 失败；
- Recovery：全部方向表、resolver 失败、CLI 不可用、路径矛盾、重复恢复和跨 Skill/MCP/Plugin 串行恢复；
- Journal：schema v1 三 driver 互斥载荷、所有非法 Plugin 组合统一 `ErrInvalidJournal`。

### 隔离集成测试

集成测试在 `t.TempDir()` 中创建 fake `codex`、`claude`、`hermes`，并隔离：

```text
PATH
APM_HOME
XDG_CACHE_HOME
CODEX_HOME
CLAUDE_CONFIG_DIR
HERMES_HOME
```

测试覆盖三个 Agent 的首装、unchanged、升级、降级、卸载、unmanaged conflict、target missing、marketplace source conflict 和中断恢复。测试必须断言所有已执行程序来自 fake-bin，且没有修改真实 `~/.codex`、`~/.claude`、`~/.hermes`。本 feature 不增加真实 Agent 环境 smoke test；真实 CLI 只用于设计阶段的只读 `--help` 契约核对。

### 最终验证

实现完成后至少运行：

```text
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 代码文件布局

### 新增生产代码

| 文件 | 实现内容 |
|---|---|
| `internal/domain/plugin.go` | Plugin definition、marketplace、deployment mode、版本和名称映射规则。 |
| `internal/cloud/local_skill.go` | 从聚合 LocalClient 移出既有 Skill Get 实现，行为不变。 |
| `internal/cloud/local_plugin.go` | 权威 marketplace 解析、Plugin List/GetPlugin 和 Plugin Cache 写入。 |
| `internal/adapter/plugin_adapter.go` | Plugin Adapter 契约、fact、target、mutation 和公共校验。 |
| `internal/adapter/plugin_codex.go` | Codex marketplace/plugin CLI Adapter。 |
| `internal/adapter/plugin_claude.go` | Claude user-scope marketplace/plugin CLI Adapter。 |
| `internal/adapter/plugin_hermes.go` | Hermes list/enable/disable 与目标路径。 |
| `internal/lifecycle/install_plugin.go` | Plugin 安装状态表、Cloud/Cache 编排和 outcome。 |
| `internal/lifecycle/uninstall_plugin.go` | Plugin 离线卸载和 target-missing。 |
| `internal/transaction/plugin_install.go` | Plugin 安装/切换事务。 |
| `internal/transaction/plugin_uninstall.go` | Plugin 卸载事务。 |
| `internal/transaction/plugin_recovery.go` | Plugin journal 恢复。 |
| `internal/transaction/plugin_filesystem.go` | Hermes 受控目录发布、隔离、恢复和清理。 |

### 修改生产代码

| 文件 | 修改内容 |
|---|---|
| `internal/cloud/client.go` | 增加 GetPlugin；明确 Get 支持 Skill/Plugin。 |
| `internal/cloud/local_client.go` | 保留构造、类型聚合、排序和 Get 分派。 |
| `internal/cache/filesystem.go` | 支持 Plugin cache/staging。 |
| `internal/adapter/mcp_runner.go` -> `internal/adapter/cli_runner.go` | 提炼 MCP/Plugin 共用 runner。 |
| `internal/adapter/mcp_adapter.go`、`mcp_codex.go`、`mcp_claude.go`、`mcp_hermes.go` | 仅改用通用 runner 类型。 |
| `internal/adapter/config.go` | Hermes Plugin 安全目标路径。 |
| `internal/adapter/errors.go` | Plugin/marketplace sentinel。 |
| `internal/adapter/registry.go` | 注册并解析三 Agent Plugin Adapter。 |
| `internal/lifecycle/dependencies.go` | 增加 GetPlugin、ResolvePlugin 和 Plugin transaction 接口。 |
| `internal/lifecycle/install_agent.go` | 类型分派加入 installPlugin。 |
| `internal/lifecycle/uninstall_agent.go` | 类型分派加入 uninstallPlugin。 |
| `internal/transaction/journal.go` | DriverPlugin、Plugin payload 和 recovery action。 |
| `internal/transaction/manager.go` | Plugin 请求、Manager 方法和 resolver。 |
| `internal/transaction/recovery.go` | DriverPlugin 路由。 |
| `internal/transaction/validation.go` | Plugin request/journal/path/phase 严格校验。 |

`internal/cli/*`、`internal/config/*` 和 `internal/version/*` 不改变：它们已支持 Plugin 类型、`name=version`、规范 APM 版本和 `plugins` manifest 分组。组合根继续以 `transaction.NewManager(repository, registry)` 构造，不增加 Agent 专属接线。

### 新增测试

```text
internal/domain/plugin_test.go
internal/cloud/local_plugin_test.go
internal/adapter/plugin_adapter_test.go
internal/adapter/plugin_codex_test.go
internal/adapter/plugin_claude_test.go
internal/adapter/plugin_hermes_test.go
internal/lifecycle/install_plugin_test.go
internal/lifecycle/uninstall_plugin_test.go
internal/lifecycle/plugin_integration_test.go
internal/transaction/plugin_install_test.go
internal/transaction/plugin_uninstall_test.go
internal/transaction/plugin_recovery_test.go
```

现有 cache、registry、runner、journal validation、lifecycle fake、transaction fake 和组合根测试只做新增接口所需的同步修改，不复制测试工具。

## 文档与决策记录

设计和计划保存在：

```text
ivory-tower/features/014-lifecycle-plugin/spec.md
ivory-tower/features/014-lifecycle-plugin/plan.md
```

实现阶段新增：

```text
ivory-tower/adr/ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md
```

ADR-012 记录以下重大决策：

- 一个稳定、无版本、包含所有插件所有版本的 Cloud marketplace；
- APM 逻辑名/版本到版本化实际插件名的映射；
- Codex/Claude marketplace-backed 与 Hermes filesystem-backed 的双部署模式；
- marketplace 准备在事务外且永不自动回滚或移除；
- Plugin 使用共享 journal 信封中的独立 driver/payload；
- 卸载按逻辑 ref 离线计算实际名，不连接 Cloud。

同时更新 ADR-010 的当前 driver 列表，并在 `BUILD.md`、`ivory-tower/project-overview.md`、C4 Cloud/Cache/Registry/Core Lifecycle、Install/Uninstall/Recovery 源文件和 Feature 014 文档中引用 ADR-012。历史 feature/plan 保留其当时范围，只在当前状态说明中指向 Feature 014。

实现、文档和最终全量验证全部完成后，按仓库规范只新增一条 `docs/Changelog.md` 记录；设计提交不写 Changelog。

## 验收标准

- Cloud List 将版本化原生插件条目稳定呈现为逻辑 Plugin `PowerRef`；
- 用户通过与 Skill 相同的 `name=version` 体验安装任意 marketplace 内版本；
- Codex/Claude 首次注册 marketplace，后续每次安装只更新该 marketplace；
- marketplace source 冲突不会覆盖用户配置；
- Hermes 只在需要发布时使用 Plugin Cache/Staging，并发布到版本化实际名目录；
- 三 Agent 首装、unchanged、修复、升级、降级和卸载符合状态表；
- APM 不关心 enabled 状态，但 APM 新安装默认启用；
- 卸载不调用 Cloud、不更新 marketplace，只删除对应 Plugin 清单项；
- Plugin 中断可由任意下一条恢复型命令恢复，且与 Skill/MCP 串行共用唯一 journal；
- 任何非法 journal 都统一返回 `ErrInvalidJournal`；
- 自动化测试不执行或修改真实 Agent 环境；
- Skill 与 MCP 的既有行为和测试保持通过；
- 代码文件职责符合本规格的模块边界，无 Agent ID 分支泄漏到 lifecycle/transaction。

## 已确认决策清单

- marketplace 无版本，稳定 source 中包含所有插件的所有版本；
- 同一根目录保存 Codex 与 Claude 两套原生 marketplace；
- `.claude-plugin/marketplace.json` 是唯一权威索引；
- 实际插件名使用 `<logical-name>-v<semver>`；
- 原生 manifest 使用无 `v` 的 SemVer，APM 使用带 `v` 的规范版本；
- marketplace 名称和 source 由 Cloud GetPlugin 返回；
- marketplace 首次 add，之后每次安装 update，永不由 APM remove；
- marketplace 同名异源时失败关闭；
- Codex/Claude 不使用 APM Plugin Cache；Hermes 复用 Cache/Staging；
- 升降级先撤旧再装新；
- enabled 状态不进入 APM 状态模型；
- 安装前通过 Agent CLI Inspect，写操作后不做 list 二次验证；
- 卸载按实际名离线执行，不访问 Cloud 或 marketplace update；
- Plugin 使用 schema v1 共享信封中的独立 driver/payload；
- Skill、MCP、Plugin 遗留事务由下一条命令串行恢复；
- 测试完全隔离真实 Agent 环境；
- 采用独立 Plugin 垂直模块，不复制三套 Agent lifecycle，也不重构为通用 Power Driver。

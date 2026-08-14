# APM 代码结构与接口命名规范化规格

> 状态：设计已确认，等待 implementation plan。

## 目标

在不改变 APM 现有功能和可观察行为的前提下，重新整理 Skill、MCP、Plugin 在 Codex、Claude Code、Hermes Agent 上的代码文件布局、接口名称和声明位置，使 Power 与 Agent 的并列关系清晰，模块职责能够从文件名和接口直接识别，并为后续新增 Power 类型或 Agent 适配提供稳定扩展点。

本 feature 是行为中立的结构重构。它不重新解释 Skill、MCP、Plugin 的业务规则，不修改 CLI 用户体验，不调整 lifecycle 锁与恢复边界，也不改变集中 transaction journal 的 schema 或恢复语义。

## 当前问题

现有顶层 package 边界基本稳定，但 Feature 013 和 Feature 014 迭代后形成了以下命名与布局不一致：

- `adapter.Adapter` 实际只表示 Skill，而 `MCPAdapter`、`PluginAdapter` 已显式包含 Power 名称；
- `transaction.Install`、`transaction.Uninstall` 实际只处理 Skill，MCP 与 Plugin 方法则带类型后缀；
- `cloud.Client.Get` 同时下载 Skill 和 Plugin 文件资源，`GetMCP`、`GetPlugin` 却显式区分定义读取；
- `cache.PowerCache.Fetch` 在 Skill 与 Plugin 间使用类型分支，接口层无法表达 MCP 不使用 Cache；
- lifecycle 中 Skill 实现隐藏在 `install_agent.go`、`uninstall_agent.go`，MCP 与 Plugin 则使用独立文件；
- transaction 中 Skill 使用无类型前缀的 `install.go`、`uninstall.go`、`recovery.go`，MCP 与 Plugin 使用类型前缀；
- Adapter Agent 实现采用 `<power>_<agent>.go`，按 Agent 浏览时不集中；
- Agent 安装探测挂在 Skill Adapter 的 `DetectAgent` 上，使 Agent 发现职责错误依赖 Skill；
- Registry 通过 Agent ID switch 构造实现，新增 Agent 需要继续扩展分支；
- `internal/domain/result.go` 没有生产调用者，仅由孤立测试引用；
- lifecycle 用类型别名转发其他 package 的依赖接口，弱化了真实接口所有权。

## 范围与非目标

### 包含

- 保持现有 Go package 边界，整理 package 内文件布局；
- 显式重命名 Skill 专属接口、请求和方法；
- 统一 Power 与 Agent 文件命名轴；
- 将 Agent 探测从 Skill Adapter 移回 Agent profile/Registry；
- 使用声明式 Agent 注册替代 Registry 中的 Agent ID switch；
- 拆分 Cloud 和 Cache 的类型分支为显式 Power 方法；
- 将 lifecycle 中 Skill 安装和卸载移入独立 Power 文件；
- 将 transaction 中 Skill filesystem driver 显式化，并拆分类型专属 validation；
- 删除未使用的通用 domain Result；
- 同步整理测试文件、fixture 和 helper 归属；
- 为主要接口和业务流程补充简短、规范的中文注释；
- 实现及最终验证完成后，按用户要求新增一条 Changelog 重构记录。

### 不包含

- 新增 Power 子包、Agent 子包或 application package；
- 引入通用 `PowerDriver`、胖 Adapter 或带可选方法的统一接口；
- 合并 Search、List、Install、Uninstall Service；
- 改变 CLI 与 lifecycle 端口的所有权；
- 改变 CLI 参数、输出、usage 或退出码；
- 改变 Cloud 资源格式、Cache/staging/Agent 路径或 manifest；
- 改变 Agent CLI 命令、参数、scope、stdin、超时或输出判定；
- 改变锁区间、恢复顺序、Agent 顺序、fail-fast、日志或错误树；
- 改变 journal schema、driver、payload、phase 或 JSON 字段；
- 修改 `internal/config`、`internal/paths`、`internal/lock`、`internal/logging`、`internal/version`、`internal/utils` 的结构；
- 更新 ADR、C4、BUILD 或 project overview。

## 方案选择

采用“语义对齐型整理”：保持单 package，通过明确接口名、类型专属入口、职责迁移和测试重组建立边界。

未采用以下方案：

| 方案 | 未选择原因 |
|---|---|
| 只重命名文件，不改接口 | `Adapter`、`Get`、`Fetch`、`Install` 等名称仍会隐藏真实 Power 语义，无法解决接口边界问题。 |
| 为 Skill/MCP/Plugin 建立 Adapter 子包 | Power 业务实现耦合低，但共享 Agent profile、CLI runner、Registry 和包内测试；拆包会引入 contracts/shared package、导出构造函数和大量 import 迁移，超出低风险行为中立重构。 |
| 建立完整 Agent × Power 文件矩阵 | Skill 当前没有三套 Agent 专属实现，强制创建 `codex_skill.go` 等文件只会产生空壳和重复。 |
| 引入通用 PowerDriver | Skill filesystem、MCP agent-cli、Plugin marketplace/filesystem 的副作用和恢复模型没有稳定共同接口，会重新制造可选字段和类型分支。 |

## 总体架构与命名规则

保留现有 package：

```text
cmd/apm
internal/
├── cli
├── lifecycle
├── transaction
├── adapter
├── cloud
├── cache
├── config
├── domain
└── ...
```

职责保持：

- `domain`：Agent、Power、MCP、Plugin 的共享值类型和纯规则；
- `cloud`：发现 Power，并按类型获取定义或下载文件资源；
- `cache`：将已缓存的 Skill/Plugin 物化到 staging；
- `adapter`：隔离不同 Agent 对 Skill/MCP/Plugin 的本地操作差异；
- `transaction`：管理 journal、提交点和三类 Power 的可恢复副作用；
- `lifecycle`：按 Search/List/Install/Uninstall 编排业务流程；
- `cli`：解析参数、调用 lifecycle、渲染结果；
- `cmd/apm`：创建默认依赖并完成组合。

声明和文档中的 Power 顺序统一为 Skill、MCP、Plugin；Agent 顺序统一为 Codex、Claude、Hermes。运行时 Agent 快照继续使用既有字典序，不改变执行顺序。

Power 契约文件按 `skill.go`、`mcp.go`、`plugin.go` 并列；Agent 专属实现按 `<agent>_<power>.go` 命名。lifecycle 继续以命令为第一边界，在命令内使用 `install_<power>.go`、`uninstall_<power>.go` 并列。transaction 以 driver/Power 为第一边界，使用 `<power>_<operation>.go`。

所有被替换的内部导出名称一次性迁移，不保留 deprecated alias。项目仍处于开发阶段，双名称会延续歧义且没有外部兼容收益。

## Adapter 设计

### 目标生产布局

```text
internal/adapter/
├── doc.go
├── skill.go
├── skill_filesystem.go
├── skill_frontmatter.go
├── mcp.go
├── plugin.go
├── codex_mcp.go
├── codex_plugin.go
├── claude_mcp.go
├── claude_plugin.go
├── hermes_mcp.go
├── hermes_plugin.go
├── agent_profile.go
├── registry.go
├── cli_runner.go
└── errors.go
```

### 接口与类型命名

```go
type SkillInstallResult struct {
	Published bool
}

type SkillAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (SkillInstallResult, error)
}

type MCPAdapter interface {
	Inspect(context.Context, string) (MCPPresence, error)
	Add(context.Context, domain.PowerRef, domain.MCPServer) (MCPMutationResult, error)
	Remove(context.Context, domain.PowerRef) (MCPMutationResult, error)
}

type PluginAdapter interface {
	DeploymentMode() domain.PluginDeploymentMode
	PrepareInstall(context.Context, domain.PluginDefinition) error
	Inspect(context.Context, string) (PluginFact, error)
	Add(context.Context, PluginTarget) (PluginMutationResult, error)
	Remove(context.Context, PluginTarget) (PluginMutationResult, error)
	TargetPath(string) (string, error)
}
```

具体重命名：

```text
Adapter                -> SkillAdapter
InstallResult          -> SkillInstallResult
skillAdapter           -> filesystemSkillAdapter
newSkillAdapter        -> newFilesystemSkillAdapter
agentConfig            -> agentProfile
builtInAgentConfigs    -> builtInAgentRegistrations
adaptFunc              -> skillAdaptFunc
```

MCP 与 Plugin 虽然都有 presence/mutation 三态，但继续使用类型专属类型。`PluginTarget` 不提升为通用 Power target。CLI runner 只共享进程执行事实，不解释 Power 业务结果。

### Agent profile 与声明式注册

`agent_profile.go` 保存 Agent ID、home 环境变量、默认根目录、安装探测、Skill/Plugin 目标路径和可选 Skill 适配函数。`SkillAdapter` 删除 `DetectAgent`；`Registry.RefreshInstalled` 直接调用 `agentProfile.detectInstalled`，保持根目录存在性判断、错误文本、排序和首错停止行为。

`registry.go` 使用声明式注册：

```go
type agentRegistration struct {
	profile   agentProfile
	newSkill  func(agentProfile) SkillAdapter
	newMCP    func(agentCLIRunner) MCPAdapter
	newPlugin func(agentProfile, agentCLIRunner) PluginAdapter
}
```

`builtInAgentRegistrations` 依次声明 Codex、Claude、Hermes。`NewRegistry` 只遍历注册项，不按 Agent ID switch。factory 为空表示该 Agent 不支持对应 Power，`Resolve*` 继续返回 `ErrUnsupportedAdapter`。

### Adapter 文件迁移

| 目标文件 | 来源与职责 |
|---|---|
| `doc.go` | 接管删除 `adapter.go` 后的 package 中文说明。 |
| `skill.go` | 从 `adapter.go` 迁入 Skill 契约，并保存 Skill 公共校验。 |
| `skill_filesystem.go` | 由 `skill_adapter.go` 重命名，保存 filesystem Skill 发布。 |
| `skill_frontmatter.go` | 由 `frontmatter.go` 重命名。 |
| `mcp.go` | 由 `mcp_adapter.go` 重命名，保存 MCP 契约和公共结果分类。 |
| `plugin.go` | 由 `plugin_adapter.go` 重命名，保存 Plugin 契约和公共结果分类。 |
| `codex_mcp.go` / `codex_plugin.go` | 由 `mcp_codex.go` / `plugin_codex.go` 重命名。 |
| `claude_mcp.go` / `claude_plugin.go` | 由 `mcp_claude.go` / `plugin_claude.go` 重命名。 |
| `hermes_mcp.go` / `hermes_plugin.go` | 由 `mcp_hermes.go` / `plugin_hermes.go` 重命名。 |
| `agent_profile.go` | 由 `config.go` 重命名并解除 Agent 探测与 Skill Adapter 的依赖。 |
| `registry.go` | 改为声明式注册并使用 `SkillAdapter`。 |
| `cli_runner.go` | 保持文件名和私有范围。 |
| `errors.go` | 保持稳定 sentinel。 |

### Adapter 测试布局

```text
skill_test.go
skill_filesystem_test.go
skill_frontmatter_test.go
mcp_test_helpers_test.go
codex_mcp_test.go
claude_mcp_test.go
hermes_mcp_test.go
plugin_test.go
codex_plugin_test.go
claude_plugin_test.go
hermes_plugin_test.go
agent_profile_test.go
registry_test.go
cli_runner_test.go
```

测试用例语义和断言保持，只有随生产符号变化所需的测试名称、fixture 类型和文件归属调整。

## Cloud 设计

### 接口

`cloud.Client.Get` 当前同时下载 Skill 与 Plugin 文件，不应简单改名为 `GetSkill`。改为类型显式接口：

```go
type Client interface {
	List() ([]domain.PowerRef, error)
	DownloadSkill(domain.PowerRef) error
	GetMCP(domain.PowerRef) (domain.MCPServer, error)
	GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
	DownloadPlugin(domain.PowerRef) error
}
```

`DownloadSkill` 和 `DownloadPlugin` 仍把文件资源原子写入配置好的本地 Cache。`GetMCP` 仍直接返回定义且不使用 Cache。`GetPlugin` 仍返回 marketplace 安装定义。删除通用 `Get(ref)` 及其 Power type switch。

### 文件布局

```text
internal/cloud/
├── client.go
├── local_client.go
├── local_skill.go
├── local_mcp.go
├── local_plugin.go
├── errors.go
└── resources/
```

- `local_client.go`：LocalClient、构造、公共 List 和稳定排序；
- `local_skill.go`：Skill 发现辅助和 `DownloadSkill`；
- `local_mcp.go`：MCP 发现辅助、JSON 解码和 `GetMCP`；
- `local_plugin.go`：权威 marketplace 解析、Plugin 发现、`GetPlugin` 和 `DownloadPlugin`。

测试布局：

```text
local_client_test.go
local_skill_test.go
local_mcp_test.go
local_plugin_test.go
local_cache_integration_test.go
```

原子复制、临时目录、Cache 路径、排序和错误文本不变。

## Cache 设计

`PowerCache.Fetch` 改为显式方法：

```go
type PowerCache interface {
	StageSkill(domain.PowerRef) (stagingRoot string, err error)
	StagePlugin(domain.PowerRef) (stagingRoot string, err error)
}
```

`FilesystemCache` 的两个入口内部复用私有 `stage(ref, directory)`，删除公开入口中的 Power type switch。保持：

- `cache/skills`、`cache/plugins` 路径；
- staging 目录布局；
- 清理、复制和 rename 顺序；
- `cache.ErrMiss`；
- MCP 不进入 Cache。

文件保持：

```text
internal/cache/
├── cache.go
├── filesystem.go
└── errors.go
```

`filesystem_test.go` 同步新方法名并保留全部路径和隔离断言。

## Domain 设计

目标布局：

```text
internal/domain/
├── agent.go
├── power.go
├── mcp.go
└── plugin.go
```

删除无生产调用者的 `result.go` 以及 `domain_test.go` 中只验证旧 Result 的部分。保留 Agent、Power、MCP、Plugin 的值、JSON 字段、校验和转换规则。`PowerType` 声明顺序调整为 Skill、MCP、Plugin，常量值不变。

不引入通用 Power definition、deployment、presence 或 mutation 类型。

## Lifecycle 设计

### 目标生产布局

```text
internal/lifecycle/
├── doc.go
├── dependencies.go
├── search.go
├── search_result.go
├── list.go
├── list_installed.go
├── list_result.go
├── install.go
├── install_agent.go
├── install_resolve.go
├── install_skill.go
├── install_mcp.go
├── install_plugin.go
├── uninstall.go
├── uninstall_agent.go
├── uninstall_resolve.go
├── uninstall_skill.go
├── uninstall_mcp.go
└── uninstall_plugin.go
```

### 依赖接口位置

删除以下 lifecycle 类型别名：

```text
ConfigRepository
TransactionManager
Locker
PowerCache
CloudClient
```

Service 字段和构造函数直接使用 `config.Repository`、`transaction.Manager`、`lock.Locker`、`cache.PowerCache`、`cloud.Client`。`dependencies.go` 只保留 lifecycle 自己拥有的窄接口：

```go
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}

type VersionSelector interface {
	Select([]string, string) (string, error)
}
```

构造函数参数顺序、nil/typed-nil 校验顺序和错误文本不变。

### Install/Uninstall 职责

- `install.go`：构造、锁、Recover、Agent 快照、批处理和 Release；
- `install_agent.go`：单 Agent Power 类型分派，`installOne` 改名为 `installForAgent`；
- `install_resolve.go`：Cloud 名称、类型和版本解析；
- `install_skill.go`：Skill Config、Cache、Cloud、Adapter、Transaction；
- `install_mcp.go`：MCP manifest/CLI/Cloud/Transaction；
- `install_plugin.go`：Plugin marketplace、CLI、Hermes 条件 Cache 和 Transaction；
- `uninstall.go`：构造、锁、Recover、Agent 快照、fail-fast 和 Release；
- `uninstall_agent.go`：清单解析、Power 分派、结果和事实 Warn，`uninstallOne` 改名为 `uninstallForAgent`；
- `uninstall_resolve.go`：本地清单名称和类型解析；
- `uninstall_skill.go`、`uninstall_mcp.go`、`uninstall_plugin.go`：类型专属卸载。

删除 `fetchInstallStaging`。Skill 与 Plugin 类型文件分别实现 `stageSkill` 和 `stagePlugin`：

```text
Stage<Type> -> cache miss -> Download<Type> -> Stage<Type>
```

两条流程保持现有调用次数、错误上下文和 Cache miss 语义，不建立新的通用 lifecycle runner。

Transaction 调用同步为：

```text
Install          -> InstallSkill
Uninstall        -> UninstallSkill
InstallMCP       -> 保持
UninstallMCP     -> 保持
InstallPlugin    -> 保持
UninstallPlugin  -> 保持
```

### Lifecycle 测试布局

```text
search_test.go
search_result_test.go
list_test.go
list_installed_test.go
list_result_test.go

install_test.go
install_test_helpers_test.go
install_agent_test.go
install_resolve_test.go
install_skill_test.go
install_mcp_test.go
install_plugin_test.go

uninstall_test.go
uninstall_test_helpers_test.go
uninstall_agent_test.go
uninstall_resolve_test.go
uninstall_skill_test.go
uninstall_mcp_test.go
uninstall_plugin_test.go

mcp_integration_test.go
plugin_integration_test.go
```

helper 文件只保存同一命令多个测试文件共享的 fixture、fake 和 assertion，不保存测试用例，不建立跨命令测试框架。

## Transaction 设计

### 目标生产布局

```text
internal/transaction/
├── doc.go
├── manager.go
├── journal.go
├── journal_store.go
├── filesystem_journal_store.go
├── recovery.go
├── validation.go
├── errors.go
├── skill_install.go
├── skill_uninstall.go
├── skill_recovery.go
├── skill_artifacts.go
├── skill_path_state.go
├── skill_validation.go
├── mcp_install.go
├── mcp_uninstall.go
├── mcp_recovery.go
├── mcp_validation.go
├── plugin_install.go
├── plugin_uninstall.go
├── plugin_recovery.go
├── plugin_filesystem.go
└── plugin_validation.go
```

### Manager 接口

```go
type Manager interface {
	Recover(context.Context) (RecoveryResult, error)
	InstallSkill(context.Context, SkillInstallRequest) error
	UninstallSkill(context.Context, SkillUninstallRequest) (UninstallResult, error)
	InstallMCP(context.Context, MCPInstallRequest) error
	UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
	InstallPlugin(context.Context, PluginInstallRequest) error
	UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)
}
```

`InstallRequest` 改名为 `SkillInstallRequest`，`UninstallRequest` 改名为 `SkillUninstallRequest`。`UninstallResult` 保持公共类型，因为三个 driver 都返回相同的 `TargetMissing` 事实。

`manager.go` 保存 Manager、请求类型、Adapter resolver、ManifestWriter、Service 和构造。公共接口与重要约束使用中文注释。

### 公共恢复与 Power driver

`recovery.go` 只保留：

```text
Inspect active journal
-> validate
-> switch driver
-> recoverSkill / recoverMCP / recoverPlugin
-> delete completed journal
```

当前 `recovery.go` 中属于 filesystem Skill driver 的路径状态、提交/回滚和产物清理移入 `skill_recovery.go`。文件迁移：

```text
install.go       -> skill_install.go
uninstall.go     -> skill_uninstall.go
artifacts.go     -> skill_artifacts.go
path_state.go    -> skill_path_state.go
```

MCP 与 Plugin operation 文件保持类型专属职责。Plugin filesystem 与 Skill filesystem 不合并，因为两者的发布、enable/disable 和恢复模型不同。

### Validation

- `validation.go`：公共 identity、Power、transaction ID、phase 和路径安全原语；
- `skill_validation.go`：Skill 请求与 filesystem driver payload；
- `mcp_validation.go`：MCP 请求与 agent-cli payload；
- `plugin_validation.go`：Plugin 请求、payload 和内嵌 filesystem 路径。

仅移动和重命名现有校验。校验顺序、sentinel、错误包装、严格 JSON 和 fail-closed 行为不变。

### Transaction 测试布局

```text
manager_test.go
journal_schema_test.go
filesystem_journal_store_test.go
recovery_test.go
validation_test.go

skill_install_test.go
skill_uninstall_test.go
skill_recovery_test.go
skill_artifacts_test.go
skill_path_state_test.go
skill_validation_test.go

mcp_install_test.go
mcp_uninstall_test.go
mcp_recovery_test.go
mcp_validation_test.go

plugin_install_test.go
plugin_uninstall_test.go
plugin_recovery_test.go
plugin_filesystem_test.go
plugin_validation_test.go
```

## CLI、组合根与其他模块

`internal/cli` 保持现有 `*_contract.go`、命令和 render 文件布局。lifecycle 继续使用 `cli.Flags` 和 `cli.*Result`，不新增 application DTO 或映射层。CLI 生产代码仅在编译确有需要时同步内部名称，不改变端口和渲染。

`cmd/apm/main.go` 保留 `newSearchLifecycle`、`newListLifecycle`、`newInstallLifecycle`、`newUninstallLifecycle` 四个独立 factory，不引入容器或共享 factory。只同步 Cloud、Cache、Adapter 和 Transaction 新名称。`cmd/apm/main_test.go` 同步调用并保持端到端断言。

`internal/config`、`internal/paths`、`internal/lock`、`internal/logging`、`internal/version` 和 `internal/utils` 不做结构调整；因接口编译依赖产生的机械名称同步除外。

## 行为保持契约

实现必须逐项保持：

1. Search、List、Install、Uninstall 的构造函数、CLI 端口和可渲染结果；
2. nil 与 typed-nil 依赖拒绝顺序及精确错误文本；
3. Acquire、Recover、Registry、Config、Cloud、Cache、Adapter、Transaction、Release 的调用条件、次数和顺序；
4. 各命令现有锁区间、Release 错误处理和结果抑制；
5. Agent 快照字典序、`all` 行为、fail-fast、unexecuted 补齐和部分失败；
6. Skill/MCP/Plugin 的首装、幂等、修复、升级、降级、冲突、漂移和卸载规则；
7. Plugin 卸载不访问 Cloud、不更新 marketplace；
8. MCP 和 Plugin 安装成功后不追加 list 验证；
9. Agent CLI argv、scope、stdin、超时、进程组终止、有界输出和结果判定；
10. slog 级别、消息、字段、值和同步位置；
11. sentinel、错误包装、`errors.Join`、`errors.Is/As` 和结构化结果；
12. Cache、staging、transaction artifacts、Agent target 和 manifest 路径；
13. Cloud JSON、marketplace、MCP 定义和 Plugin 资源解释；
14. 唯一 `transactions/active.json`、schema v1、三个 driver、payload 互斥和 phase；
15. 任何错误 journal 返回 `ErrInvalidJournal` 并保留原文件；
16. CLI stdout/stderr、usage 和退出状态。

## 中文注释规范

重命名或迁移的主要接口必须具有简短、规范的中文注释，说明用途和必要约束。业务流程注释解释原因和边界，不逐行翻译实现。

至少覆盖：

- Skill/MCP/Plugin Adapter 的职责和禁止访问的模块；
- Agent profile 探测与声明式注册的扩展方式；
- Cloud 下载方法会写入本地 Cache，MCP 不使用 Cache；
- Cache Stage 方法只负责 Cache 到 staging；
- 四个 lifecycle 主流程的锁、Recover、Release 和结果边界；
- 三类 Power 的 install/uninstall 类型专属约束；
- Plugin 卸载离线、Hermes 条件 Cache、MCP CLI 检查等非显然规则；
- Transaction Manager 的公共入口；
- recovery driver 分派、phase 意图持久化、commit/rollback 方向与 fail-closed；
- journal validation 对错误格式保留原文件的约束。

## 扩展性

### 新增 Agent

新增 Agent 的稳定步骤是：

1. 新增 `agentProfile`；
2. 新增 `<agent>_mcp.go` 和/或 `<agent>_plugin.go`；
3. 如有 Skill 特殊适配，新增对应 `skillAdaptFunc`；
4. 在 `builtInAgentRegistrations` 增加一项；
5. 添加 Agent 专属 Adapter 和 Registry 测试。

Registry 不需要新增 Agent ID switch。缺失 factory 表示不支持对应 Power，并复用既有不支持错误。

### 新增 Power

新增 Power 不是运行时动态插件，必须显式补齐：

1. domain 类型和纯规则；
2. Cloud 定义读取或文件下载能力；
3. 如为文件资源，Cache staging 能力；
4. Power 专属 Adapter 和 Registry resolver；
5. lifecycle install/uninstall handler；
6. transaction request、driver、journal payload、recovery 和 validation；
7. 单元、隔离集成和恢复测试；
8. feature、ADR（如产生重大决策）和架构文档。

这种扩展有意保持显式和类型安全。不同 Power 的副作用和恢复模型差异较大，不通过通用 PowerDriver 隐藏必要触点。

## 测试与验证

### 重构过程

1. 记录重构前全量测试和导出符号基线；
2. 按 package 小步移动和重命名，每步运行定向测试；
3. 比对重构前后的测试用例映射，防止遗漏；
4. 静态检查旧名称和旧文件全部消失；
5. 检查新增文件只包含规格规定职责；
6. 保持工作树中与本 feature 无关的用户修改。

### 最终验证

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
build_path="$(mktemp -p /tmp apm-feature-015.XXXXXX)"
go build -o "$build_path" ./cmd/apm
rm "$build_path"
git diff --check
```

额外静态验证：

- 不存在旧 `adapter.Adapter`、`transaction.InstallRequest`、`transaction.UninstallRequest`、`cloud.Client.Get` 或 `PowerCache.Fetch`；
- journal schema version 和 JSON tags 未改变；
- Agent CLI 命令字面量、超时和稳定输出契约未改变；
- CLI usage、日志消息和关键错误文本未改变；
- production 文件和测试文件符合目标布局。

MCP/Plugin 集成测试继续使用临时 `APM_HOME`、临时 Agent home、临时 `XDG_CACHE_HOME` 和 fake-bin。必须断言实际可执行路径来自 fake-bin，禁止访问真实 Codex、Claude、Hermes CLI 或用户 Agent 环境。

## 文档与变更记录

本规格保存于 `ivory-tower/features/015-code-structure-normalization/spec.md`。获批后，implementation plan 保存于同目录 `plan.md`。

本 feature 引用 ADR-008 至 ADR-012 的现有 Adapter、Transaction、journal 和 Plugin 决策，但不修改其架构结论。由于 package 关系和运行时行为不变，不新增 ADR，也不更新 C4、BUILD 或 project overview。

尽管本 feature 行为中立，用户已明确要求记录本次重构。所有实现任务和最终验证完成后，按 `~/.codex/docs/Changelog.md` 模板在 `docs/Changelog.md` 新增一条记录，列出本次请求的全部相关提交；不在中间任务提前记录。

## 验收标准

1. 生产代码和测试代码符合本规格的目标文件布局；
2. Skill、MCP、Plugin 接口名称显式并列，Skill 不再使用无类型含义的 `Adapter`、`Install`、`Uninstall`；
3. Codex、Claude、Hermes 专属实现使用 `<agent>_<power>.go` 并列；
4. Agent 探测不再依赖 Skill Adapter，Registry 使用声明式注册且无 Agent ID switch；
5. Cloud 和 Cache 公开接口不再包含 Power type switch；
6. lifecycle 以命令为边界，Install/Uninstall 内三类 Power 文件并列；
7. transaction 公共恢复与三类 driver 边界清晰，Skill filesystem driver 显式命名；
8. 无生产调用者的 domain Result 已删除；
9. 主要接口和业务流程具备准确的中文约束注释；
10. 全部行为保持契约和最终验证成功；
11. 自动化测试未访问真实 Agent 环境；
12. 最终 Changelog 只有一条本次重构记录，并包含全部相关提交。

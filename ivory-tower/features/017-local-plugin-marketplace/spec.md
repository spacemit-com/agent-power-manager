# Local Plugin Marketplace 设计规格

> 状态：设计已于 2026-08-17 确认，尚未进入实现。

## 背景

当前 Codex 与 Claude Plugin Adapter 将 Cloud 返回的 Git/HTTPS marketplace source
直接注册到 Agent，再通过原生 CLI 安装版本化实际插件。实际 Agent Power Cloud
以文件形式保存资源，不能提供可供 Codex/Claude 注册的 Git marketplace。因此 APM
需要先下载指定 Agent 的 Plugin 文件，在本地生成 Agent 原生 marketplace，再通过
CLI 注册该本地 marketplace。

本设计替代 Feature 014 和 ADR-012 中“Cloud 维护稳定全量远程 marketplace、
Codex/Claude Plugin 不进入 APM Cache”的部分。Plugin 的 CLI 语法、逻辑名称与版本、
manifest 外部 schema、全局锁、唯一 journal、`fix` 恢复入口以及 Hermes 文件发布模型
保持不变。

## 目标

- Cloud 只托管按 Agent 区分的原始 Plugin 文件，不维护 marketplace 文件。
- APM 根据目标 Agent 原生 Plugin manifest 生成本地 marketplace。
- 每个 `agent + logical plugin` 独立治理一个 marketplace；每份 marketplace 只包含
  当前准备安装的一个版本。
- Plugin artifact 按 `agent + name + version` 持久化且不可变。
- Codex/Claude 的 marketplace 切换与 Plugin 切换进入同一个可恢复 transaction。
- Install/Uninstall 以 APM manifest 为第一事实来源，不扫描或接管无清单资源。
- Skill、MCP、CLI 输出、Hermes filesystem Plugin 行为和只读 Search/List 行为不受影响。

## 不在范围内

- Cloud 生成、保存或校验 Codex/Claude marketplace 文件；
- Agent 级聚合 marketplace 或包含多个 Plugin/多个版本的 marketplace；
- Cache TTL、强制刷新、校验和、垃圾回收或删除命令；
- 自动迁移指向远程 Git、其他目录或已经损坏的旧 marketplace；
- 无 manifest Plugin 的接管、修复或卸载；
- 同一命令内多 Agent 原子回滚；
- 安装成功后的额外 `plugin list` 验证；
- 记录或恢复 Plugin enabled/disabled 状态；
- 修改 Skill/MCP lifecycle、manifest schema 或 CLI 渲染格式；
- 运行时读取 reference 仓库或复制 NVIDIA 名称、品牌、描述和资产。

## 核心决策

### Marketplace 治理单元

每个目标 Agent、每个逻辑 Plugin 使用一个独立 marketplace 注册：

```text
marketplaceName = "apm-" + logicalPluginName
```

版本不进入 marketplace 名称。版本切换通过删除旧注册并以相同名称注册目标版本缓存
目录完成。Codex 与 Claude 的 marketplace 文件格式不同，因此它们分别拥有自己的
artifact；同一 Agent 中不同逻辑 Plugin 的 marketplace 互不覆盖。

Agent 级单一 marketplace 不采用，因为任意 Plugin 变化都需要重建所有已安装 Plugin
的聚合索引，并使缓存、journal 和回滚跨越无关 Plugin。每版本一个 marketplace 名称
也不采用，因为会积累注册项并绕开已确认的重新注册语义。

### 单版本内容

每个生成的 marketplace 只包含当前 artifact 对应的一个版本化实际插件：

```text
logicalName = demo-plugin
version     = v0.1.0
actualName  = demo-plugin-v0.1.0
marketplace = apm-demo-plugin
```

Agent 不会同时安装同一逻辑 Plugin 的多个版本。历史版本只保留在 APM 持久缓存中，
用于明确降级或 transaction 回滚，不进入当前 marketplace 文件。

### Manifest 优先

APM manifest 是是否已安装的第一事实来源：

- Install 发现 manifest 已记录目标精确版本时，直接返回 `unchanged`；不查询 Agent、
  Cache 或 Cloud。
- Install 仅在 manifest 缺失或记录其他版本时检查 Agent 状态。
- Uninstall 发现 manifest 缺失时直接返回未安装，不检查 Agent。
- Uninstall 发现 manifest 时，只检查 manifest 指向的精确版本，不搜索其他版本。

APM 不通过 Agent list 猜测、接管或清理无 manifest 的 Plugin。

## 模块架构

### `internal/cloud`

Cloud 只负责资源发现和原始文件下载：

```go
type Client interface {
    List() ([]domain.PowerRef, error)
    DownloadSkill(domain.PowerRef) error
    GetMCP(domain.PowerRef) (domain.MCPServer, error)
    DownloadPlugin(
        context.Context,
        domain.AgentID,
        domain.PowerRef,
        string,
    ) error
}
```

最后一个参数是调用方控制的目标目录。Cloud 不知道 APM Cache 最终路径，不生成
marketplace，不返回 marketplace source，也不保留 `GetPlugin`。

Cloud 下载包的根是单个 Plugin 的内容目录：

```text
<download-destination>/
├── .codex-plugin/plugin.json
├── .claude-plugin/plugin.json
├── skills/
└── ...
```

具体 Agent 包只要求包含该 Agent 所需的 manifest 和内容。Cloud 不增加外层
`plugins/<actual-name>`，也不能携带 marketplace 文件。

Local Cloud 示例布局为：

```text
internal/cloud/resources/plugins/
└── demo-plugin/
    └── v0.1.0/
        ├── codex/
        ├── claude-code/
        └── hermes-agent/
```

`List` 对同一逻辑版本只返回一个 `PowerRef`；精确 Agent 资源是否存在由
`DownloadPlugin` 判断。生产 Cloud 的列表 API 继续负责返回已经分离的逻辑名称和版本。

### `internal/pluginresource`

新增专属包，隔离 Cloud 下载、artifact 组装、不可变缓存和 Hermes staging：

```go
type Downloader interface {
    DownloadPlugin(
        context.Context,
        domain.AgentID,
        domain.PowerRef,
        string,
    ) error
}

type Manager interface {
    Ensure(context.Context, domain.AgentID, domain.PowerRef) (Artifact, error)
    Lookup(domain.AgentID, domain.PowerRef) (Artifact, error)
    StageHermes(domain.PowerRef) (string, error)
}

type Artifact struct {
    Agent              domain.AgentID
    Power              domain.PowerRef
    ActualName         string
    Root               string
    PluginPath         string
    MarketplaceName    string
    MarketplaceSource  string
}
```

Hermes Artifact 的 `MarketplaceName` 和 `MarketplaceSource` 为空；Codex/Claude 两个字段
必须是规范 marketplace 名称和 Artifact 根的绝对 clean path。

`Ensure` 允许在 cache miss 时调用 Cloud；`Lookup` 严禁连接 Cloud。Lifecycle 使用这一
差异保证目标版本可下载，而旧版本只能使用已存在的本地恢复材料。

### `internal/pluginartifact`

提供 Plugin Resource 与 Transaction recovery 共用的中立只读 Validator。它不依赖 Cloud
或 `pluginresource.Manager`，不下载、生成或修改 artifact；只负责完整物理后代树、Agent
原生 manifest 身份和 marketplace 闭集/语义校验。Codex 只允许根部生成的 Codex
marketplace，Claude 只允许根部生成的 Claude marketplace，Hermes 不允许 marketplace。

最终缓存布局为：

```text
cache/plugins/<agent>/<logical-name>/<version>/
├── .agents/plugins/marketplace.json       # 仅 Codex
# 或
├── .claude-plugin/marketplace.json        # 仅 Claude
└── plugins/
    └── <actual-name>/
        └── <原始 Plugin 内容>
```

Hermes artifact 使用同一 agent-aware 版本键和 `plugins/<actual-name>` 叶子，但不生成
marketplace；安装前由 `StageHermes` 物化到既有 staging 语义。

### `internal/cache`

现有 `PowerCache` 回归 Skill staging。Plugin 的缓存键、生成、复验和 Hermes staging
全部由 `pluginresource` 拥有，避免 `cache` 与新包共同决定 Plugin 路径。

### `internal/adapter`

基础 Plugin 操作与 marketplace 操作通过接口组合分离：

```go
type PluginAdapter interface {
    DeploymentMode() domain.PluginDeploymentMode
    Inspect(context.Context, string) (PluginFact, error)
    Add(context.Context, PluginTarget) (PluginMutationResult, error)
    Remove(context.Context, PluginTarget) (PluginMutationResult, error)
    TargetPath(string) (string, error)
}

type MarketplacePluginAdapter interface {
    PluginAdapter
    InspectMarketplace(context.Context, string) (MarketplaceFact, error)
    RegisterMarketplace(context.Context, MarketplaceTarget) (PluginMutationResult, error)
    UnregisterMarketplace(context.Context, MarketplaceTarget) (PluginMutationResult, error)
}
```

Codex/Claude 实现组合接口；Hermes 只实现基础接口，不提供无意义的 marketplace 空方法。
旧 `PrepareInstall` 删除。Adapter 只处理目标 Agent CLI、JSON 和 source 规范化，不访问
Cloud、Cache 或 manifest。

### `internal/lifecycle`

Lifecycle 负责 manifest-first 状态表、资源准备顺序、Adapter 事实组合和 transaction
请求映射。它不生成 marketplace JSON，也不直接执行 Agent 写命令。

### `internal/transaction`

Transaction 只接收已经解析的新旧 Plugin target、marketplace target 和绝对缓存路径。
它不依赖 `pluginresource`，不连接 Cloud，也不重新生成 artifact。Marketplace 注册切换、
Plugin 切换、manifest commit 和恢复由 Plugin driver 串行完成。

## Marketplace 生成

reference 文件：

```text
/home/liangff/Workspace/projects/bianbu-next/reference/source/nvidia-agent-skills/
  .agents/plugins/marketplace.json
  .claude-plugin/marketplace.json
```

只作为开发期 schema 与字段默认值参考，不作为运行时模板文件。实现使用 Go 结构体和
`encoding/json`，不做字符串模板替换。

### Plugin 信息权威来源

- Codex：`plugins/<actual-name>/.codex-plugin/plugin.json`
- Claude：`plugins/<actual-name>/.claude-plugin/plugin.json`

APM 必须验证原生 manifest 的 `name` 等于版本化实际名，原生 `version` 与 APM
`vMAJOR.MINOR.PATCH` 精确对应。展示字段来自目标 Agent manifest；缺失时使用不带品牌的
APM 默认值。Cloud 不额外返回 marketplace 元数据。

### Codex marketplace

语义结构为：

```json
{
  "name": "apm-<logical-name>",
  "interface": {
    "displayName": "<plugin displayName 或 logical-name>"
  },
  "plugins": [
    {
      "name": "<actual-name>",
      "source": {
        "source": "local",
        "path": "./plugins/<actual-name>"
      },
      "policy": {
        "installation": "AVAILABLE",
        "authentication": "ON_INSTALL"
      },
      "category": "<plugin category 或 Coding>"
    }
  ]
}
```

### Claude marketplace

语义结构为：

```json
{
  "name": "apm-<logical-name>",
  "owner": {
    "name": "<plugin author.name 或 APM>"
  },
  "metadata": {
    "description": "<plugin description 或 APM 默认描述>",
    "version": "<原生 semver>"
  },
  "plugins": [
    {
      "name": "<actual-name>",
      "source": "./plugins/<actual-name>",
      "description": "<plugin description 或 APM 默认描述>"
    }
  ]
}
```

Marketplace 名称不带版本。Claude document 的 `metadata.version` 使用 Plugin 原生版本，
只描述这一不可变 artifact，不成为 APM marketplace 身份的一部分。

## Artifact 构建与安全

### Cache miss

`Ensure` 按以下顺序构建：

1. 校验 Agent、Plugin ref、实际名和 marketplace 名称；
2. 在 cache 同文件系统临时根创建本次唯一目录；
3. 让 Cloud 把原始包写入临时 `plugins/<actual-name>`；
4. 拒绝 symlink、路径逃逸、非物理目录和非普通 manifest；
5. 拒绝 Cloud 包携带 marketplace 文件；
6. 读取目标 Agent manifest，校验 name/version；
7. Codex/Claude 生成单条目 marketplace；Hermes 跳过生成；
8. 复验完整 artifact；
9. 原子 rename 到最终版本目录。

失败只清理本次临时目录，不覆盖任何既有缓存。

### Cache hit

同一 `<agent>/<name>/<version>` 一旦完整提交即视为不可变。Cache hit 不访问 Cloud，但
必须重新验证：

- 最终根、Plugin 根和 manifest 的物理类型；
- 无 symlink 和路径逃逸；
- manifest name/version；
- 生成 marketplace 的单条目、名称和 source 语义。

已有缓存缺失或损坏时 fail closed，不覆盖、不重新下载。相同版本内容变化必须发布新版本。

本 feature 不提供 TTL、refresh、hash、签名或 GC。

## Install 业务流程

外层继续在一个全局锁区间内 Recover、快照、版本选择和按 Agent 串行执行。

### Manifest 已满足目标

manifest 已记录目标精确版本时：

```text
return unchanged
```

不得调用 Agent CLI、`pluginresource` 或 Cloud。这是 manifest-first 的明确语义；该路径
不承担 Agent 状态对账或修复。

### Manifest 缺失或版本不同

1. 先 `Ensure(agent, targetRef)`，保证目标 artifact 已持久化；
2. 再按 manifest 情况检查 Agent。

有旧 manifest 时，根据旧 ref 推导 marketplace 名、实际插件名和预期缓存路径，然后
查询联合状态：

| Marketplace | 旧 Plugin | 处理 |
|---|---|---|
| absent | absent | 删除陈旧 manifest，转入无 manifest 首装 |
| present，source 精确且目录完整 | present | 执行版本切换 |
| present | absent | 异常，fail closed，保留 manifest |
| absent | present | 异常，fail closed，保留 manifest |
| present，但 source 冲突、目录缺失或损坏 | 任意 | 异常，fail closed，保留 manifest |

旧版本只允许 `Lookup`。旧缓存缺失或损坏时不下载、不迁移、不开始 transaction。

无 manifest（包括已清理陈旧 manifest）时：

| Marketplace | 目标 Plugin | 处理 |
|---|---|---|
| absent | absent | 首次安装 |
| present | 任意 | unmanaged marketplace conflict |
| absent | present | unmanaged plugin conflict |

即使现有 marketplace 的名称和 source 看似符合 APM 规则，只要没有 manifest，也不推断
它属于 APM。

### 首次安装 transaction

```text
journal created
→ register target marketplace
→ install target plugin
→ set manifest
→ delete journal
```

操作前 marketplace/plugin 均 absent。失败回滚到二者 absent；如果本次新增了
marketplace，回滚会删除它。

### 版本升降级 transaction

```text
journal created
→ uninstall old plugin
→ unregister old marketplace
→ register target marketplace
→ install target plugin
→ set target manifest
→ delete journal
```

回滚目标为：

```text
target plugin absent
marketplace 指向 previous source
old plugin present
manifest 保持 previous ref
```

结果根据新旧逻辑 SemVer 返回 `upgraded` 或 `downgraded`。

## Uninstall 业务流程

1. manifest 无记录：直接返回未安装，不检查 Agent、Cache 或 Cloud；
2. manifest 有记录：只推导该精确版本的 marketplace/plugin；
3. marketplace/plugin 都 absent：删除单条陈旧 manifest，返回 target-missing；
4. marketplace source 精确、目录完整且 plugin present：执行卸载 transaction；
5. 部分存在、source 冲突、目录缺失或损坏：fail closed，保留 manifest。

正常卸载只卸载 Plugin 并删除 manifest；marketplace 注册与版本缓存保留。Uninstall 不连接
Cloud，不更新、删除或重新注册 marketplace。

Hermes 没有 marketplace，其 manifest-first 判断只使用精确 Plugin 事实；Plugin absent
时清理陈旧 manifest，present 时沿用现有 filesystem transaction。

## Adapter 事实与 source 规则

`MarketplaceFact` 至少包含：

```go
type MarketplaceFact struct {
    Presence MarketplacePresence
    Name     string
    Source   string
}
```

本地 source 使用绝对 clean path 比较。相同 marketplace 名出现多次、缺失 source、非本地
source、source 不等于预期版本缓存、输出无法解析或截断均为状态不明确或冲突。

Plugin selector 继续使用：

```text
<actual-name>@<marketplace-name>
```

写命令完成后不追加 list 验证。CLI mutation 分类保持：

- 未启动：`not-applied`；
- 明确成功：`applied`；
- 已启动但失败、超时或输出截断：`unknown`。

## Transaction 与 Journal

### Marketplace payload

Marketplace deployment 的 Plugin payload 增加：

```go
type PluginMarketplaceJournal struct {
    Name string

    PreviousSource  string
    PreviousPresent bool

    TargetSource string
}
```

Payload 同时保存新旧 Plugin target、操作开始时的 present 事实和 deployment mode。
不保存 Cloud 地址、下载临时目录、Plugin 内容或 enabled 状态。

合法前置状态下目标 source 在操作开始时必定 absent，因此不保存恒为 false 的
`TargetPresent`。首次安装以 `PreviousPresent=false` 表示 marketplace 原本不存在；版本
切换以 `PreviousPresent=true` 和 `PreviousSource` 保存旧注册。

### Phase

Marketplace Plugin 安装使用足够细粒度的 phase：

```text
registering-marketplace
removing-old-plugin
unregistering-old-marketplace
registering-new-marketplace
adding-new-plugin
committing
rolling-back
```

首次安装从 `registering-marketplace` 开始，版本切换从 `removing-old-plugin` 开始。
每次 CLI mutation 前持久化意图，明确 applied 后推进 phase。

Marketplace Plugin 卸载不修改 marketplace：

```text
removing-plugin → committing
               ↘ rolling-back
```

Journal 仍保存预期 marketplace source，供恢复时确认安装来源未被外部修改。

### Recovery

Recovery 只读取 journal、Agent CLI 事实、manifest 和持久缓存路径，不连接 Cloud、不生成
marketplace。

恢复检查：

- marketplace absent、指向 previous source、指向 target source或指向其他 source；
- old plugin present/absent；
- target plugin present/absent；
- journal 中既定方向实际需要的完整缓存 artifact：安全物理树、原生 manifest 与生成
  marketplace 语义。

方向固定规则为：`committing` 必须继续 commit；`adding-new-plugin` 发现目标 marketplace
和目标 Plugin 都已 present 时固定为 commit；其他尚未完成目标 Plugin 安装的安装 phase
固定为 rollback。方向一旦写入 `rolling-back` 或 `committing` 就不得反转。

首次安装 rollback 收敛到 marketplace/plugin/manifest 都 absent。版本切换 rollback 收敛到
旧 source、旧 Plugin 和旧 manifest；commit 收敛到目标 source、目标 Plugin 和目标 manifest。

指向其他 source、Cache 损坏、CLI 无法检查或状态组合无法由 transaction 产生时，返回
`ErrIndeterminateState` 并保留 journal。

Uninstall recovery 发现 Plugin 已 absent 时提交 manifest 删除；Plugin 仍 present 时回滚并
保留 manifest。Marketplace 缺失、source 改变或损坏时不猜测，保留 journal。

### Schema

项目仍处于开发阶段，继续只使用 `schema_version: 1`，直接重置 Plugin payload 与 phase
校验，不增加旧 Plugin journal 兼容层。任何旧格式、未知字段组合、错误 phase/payload、
非法 source 或矛盾状态统一返回 `ErrInvalidJournal` 并保留原文件。

Skill、MCP、Plugin 仍共用唯一 `transactions/active.json`，任意后续写 lifecycle 或 `apm fix`
继续串行恢复三类事务。

## 错误与副作用边界

- 目标下载、manifest 校验或 marketplace 生成失败发生在 journal 前，不修改 Agent/manifest。
- 目标 artifact cache hit 损坏时不覆盖、不重新下载。
- 旧 artifact 缺失或损坏时不连接 Cloud、不开始版本切换。
- 无 manifest 但 Agent 有 marketplace/plugin 时不接管。
- 有 manifest 但联合状态部分存在或 source 异常时 fail closed。
- Marketplace/Plugin CLI mutation 为 `unknown` 时保留 journal，等待 `fix` 或下一写命令恢复。
- 首次安装失败恢复操作前 marketplace absence；版本切换失败恢复旧 marketplace/plugin。
- 普通成功卸载保留 marketplace 和 Cache。
- Search/List 不获取锁、不恢复，只按 Feature 016 检查并告警；本 feature 不改变该行为。

## 测试策略

### Domain

- 实际名与 `apm-<logical-name>` marketplace 名称；
- 规范 `vMAJOR.MINOR.PATCH`；
- 路径分隔符、绝对路径、`.`、`..` 和非规范名称拒绝。

### Cloud

- 按 Agent/ref 下载到调用方指定目录；
- Cloud 不创建 Cache、staging 或 marketplace；
- Agent/ref 缺失返回 `ErrNotFound`；
- List 对逻辑版本去重并保持稳定排序。

### Plugin Resource

- Codex/Claude 生成 JSON 的语义字段与单条目约束；
- Hermes 不生成 marketplace；
- agent-aware 多版本持久缓存；
- cache hit 不访问 Cloud；
- 临时目录清理和原子提交；
- 已有缓存损坏 fail closed；
- manifest name/version 不匹配、symlink、路径逃逸和 Cloud 携带 marketplace 时拒绝；
- Hermes staging 不污染 Codex/Claude artifact。

### Adapter

- Codex/Claude marketplace list/add/remove 精确 argv；
- source 绝对路径规范化；
- duplicate、同名异源、缺失 source、非本地 source和截断输出；
- Plugin install/uninstall selector；
- Hermes 不满足 marketplace 子接口。

### Lifecycle

- manifest 同版本直接 unchanged，Cloud/Resource/Adapter/Transaction 均未调用；
- manifest-first 安装与卸载完整状态表；
- 目标 `Ensure` 发生在任何 Agent mutation 前；
- 旧版本只 `Lookup`；
- marketplace/plugin 双 absent 才清理 manifest；
- 部分状态和损坏状态保留 manifest；
- 无 manifest 不扫描其他版本、不接管现有资源；
- Uninstall 不连接 Cloud。

### Transaction

- 首装、升级和降级每个 phase；
- marketplace/plugin 每个 mutation 的 applied/not-applied/unknown；
- commit、rollback 和恢复联合状态表；
- Recovery 的 Cloud 零调用；
- 非法 source、phase/payload 和旧 journal 返回 `ErrInvalidJournal`。

### Integration

- Codex/Claude 首装、升级、降级、卸载；
- 同一 Agent 多 Plugin marketplace 互不覆盖；
- Cache 同时保留多版本，Agent 只安装 manifest 版本；
- 每个关键 CLI 边界中断后由下一写命令或 `apm fix` 恢复；
- Hermes 回归；
- 临时 `APM_HOME`、Agent home 和 fake-bin；
- 记录并断言每个 Agent executable 位于 fake-bin，禁止真实 Codex/Claude/Hermes CLI。

最终门禁包括 `gofmt`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、
`go vet ./...`、临时目录 build、`git diff --check` 和聚焦 Plugin/Fix 集成测试。

## 代码文件布局

### 新增生产文件

| 文件 | 责任 |
|---|---|
| `internal/pluginresource/artifact.go` | Artifact、Manager、Downloader 接口和稳定输出。 |
| `internal/pluginresource/errors.go` | Cache miss、artifact 损坏、manifest 不匹配等错误。 |
| `internal/pluginresource/manager.go` | Ensure、Lookup、StageHermes 编排。 |
| `internal/pluginresource/filesystem.go` | 路径、临时目录、物理树校验和原子提交。 |
| `internal/pluginresource/marketplace.go` | Builder 公共接口和 manifest 公共校验。 |
| `internal/pluginresource/codex_marketplace.go` | Codex marketplace 生成与复验。 |
| `internal/pluginresource/claude_marketplace.go` | Claude marketplace 生成与复验。 |
| `internal/pluginartifact/validation.go` | Plugin Resource/Recovery 共用的中立只读完整 artifact 校验。 |
| `internal/transaction/plugin_marketplace.go` | 首装和版本切换 marketplace transaction。 |
| `internal/transaction/plugin_marketplace_recovery.go` | Marketplace/plugin 联合状态恢复。 |

### 修改生产文件

| 文件 | 责任变化 |
|---|---|
| `internal/domain/plugin.go` | 删除 Cloud marketplace definition，新增 marketplace 名称映射。 |
| `internal/cloud/client.go` | Agent-aware 目标目录下载，删除 GetPlugin。 |
| `internal/cloud/local_plugin.go` | 目录型资源发现与原始包下载，不解析 marketplace。 |
| `internal/cloud/local_client.go` | 移除 Plugin cache 路径所有权。 |
| `internal/cache/cache.go` | PowerCache 回归 Skill staging。 |
| `internal/cache/filesystem.go` | 删除 Plugin staging 分支。 |
| `internal/adapter/plugin.go` | Marketplace fact/target/mutation 子接口，删除 PrepareInstall。 |
| `internal/adapter/codex_plugin.go` | 本地 marketplace inspect/register/unregister。 |
| `internal/adapter/claude_plugin.go` | 本地 marketplace inspect/register/unregister。 |
| `internal/adapter/hermes_plugin.go` | 保持 filesystem Plugin，不实现 marketplace 子接口。 |
| `internal/lifecycle/install.go` | 注入 Plugin Resource Manager。 |
| `internal/lifecycle/install_plugin.go` | Manifest-first 状态表与 artifact 编排。 |
| `internal/lifecycle/uninstall.go` | 注入只读 Plugin artifact lookup。 |
| `internal/lifecycle/uninstall_plugin.go` | 联合状态与陈旧 manifest 清理。 |
| `internal/transaction/manager.go` | Plugin 请求接收显式 Plugin/marketplace target。 |
| `internal/transaction/journal.go` | Marketplace payload、phase 和 recovery action。 |
| `internal/transaction/plugin_install.go` | Deployment 分派并调用 marketplace transaction。 |
| `internal/transaction/plugin_uninstall.go` | 卸载时验证但不删除 marketplace。 |
| `internal/transaction/plugin_recovery.go` | 保留分派和 Hermes 恢复。 |
| `internal/transaction/plugin_validation.go` | 新 source、phase、payload 和 request 校验。 |
| `cmd/apm/main.go` | 创建并注入 Plugin Resource Manager。 |

### 测试文件

新增 `internal/pluginresource/*_test.go`，并更新以下现有测试：

- `internal/domain/plugin_test.go`
- `internal/cloud/local_plugin_test.go`
- `internal/cloud/local_client_test.go`
- `internal/cloud/local_cache_integration_test.go`
- `internal/cache/filesystem_test.go`
- `internal/adapter/plugin_test.go`
- `internal/adapter/codex_plugin_test.go`
- `internal/adapter/claude_plugin_test.go`
- `internal/adapter/hermes_plugin_test.go`
- `internal/adapter/registry_test.go`
- `internal/lifecycle/install_plugin_test.go`
- `internal/lifecycle/uninstall_plugin_test.go`
- `internal/lifecycle/plugin_integration_test.go`
- `internal/transaction/plugin_install_test.go`
- `internal/transaction/plugin_uninstall_test.go`
- `internal/transaction/plugin_recovery_test.go`
- `internal/transaction/plugin_validation_test.go`
- `internal/transaction/journal_schema_test.go`
- `cmd/apm/main_test.go`

测试 helper 中旧 Plugin Cloud definition/Cache fake 替换为 Plugin Resource fake。所有集成测试
必须使用临时 home 和 fake CLI。

### 文档文件

- 新增 `ivory-tower/adr/ADR-013-generate-per-plugin-local-marketplaces.md`；
- 将 ADR-012 标记为由 ADR-013 supersede；
- 在 Feature 014 标明远程全量 marketplace 设计已由 Feature 017 替代；
- 更新 Project Overview、BUILD、Cloud/Cache/Adapter/Core Lifecycle C4 和 Plugin lifecycle C4；
- 全部实现、审查和验证完成后，最后添加一条请求级 `docs/Changelog.md` 记录。

## 验收条件

1. Cloud 不含 marketplace 文件，能按 Agent/ref 将原始 Plugin 包下载到指定目录。
2. Codex/Claude artifact 分别生成正确的单条目本地 marketplace；Hermes 不生成。
3. Artifact 按 agent/name/version 持久化；cache hit 不访问 Cloud，损坏缓存不覆盖。
4. Install manifest 已含目标版本时零 Agent/Cache/Cloud 调用并返回 unchanged。
5. 无 manifest 时任何现有 marketplace/plugin 均不被接管。
6. 有旧 manifest 时只有 marketplace/plugin 双 absent 才清理清单；部分或损坏状态 fail closed。
7. 首装和版本切换的 marketplace/plugin 操作全部处于可恢复 Plugin transaction。
8. 版本切换失败或中断可仅凭 journal、旧/新缓存和 Agent CLI 恢复旧状态。
9. 正常 Uninstall 保留 marketplace/cache；无 manifest 时不检查 Agent。
10. Recovery 不连接 Cloud，不生成或修改缓存 artifact。
11. 旧 Plugin journal 和所有非法新 payload 均返回 `ErrInvalidJournal` 并保留文件。
12. Skill、MCP、CLI 输出、manifest schema、Search/List 和 Hermes lifecycle 无行为回归。
13. 多 Plugin、多版本和所有中断集成测试只使用临时 home/fake-bin，不调用真实 Agent CLI。
14. ADR-013、ADR-012 supersession、Feature 014、C4、Project Overview 和 BUILD 与实现一致。
15. 格式、普通测试、race、vet、build、diff 和聚焦集成测试全部通过后才记录 Changelog。

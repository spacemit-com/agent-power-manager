# Cloud Client Provider 与 APC Agent 只读契约设计规格

> 状态：设计已确认，待编写实现计划。重大决策见 [ADR-016](../../adr/ADR-016-select-cloud-clients-with-explicit-providers.md) 与 [ADR-017](../../adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

## 背景

APM 当前通过 `internal/cloud.LocalClient` 读取仓库内 `internal/cloud/resources`。该实现同时承担资源发现、fixture 读取以及 Skill/Agent 全局缓存写入；Plugin 下载却由调用方提供目录，MCP 又直接返回领域对象，接口职责不一致。`cmd/apm` 还在每个 lifecycle 工厂中硬编码创建 Local Client，无法在开发期 fixture 与生产 APC 之间安全切换。

相邻 APC 仓库的 `ivory-tower/project-overview.md` 已定义 Skill、Plugin、MCP 的统一资源读取 API，但尚未包含 APM 已实现的第四种 Power `agent`。APC Plugin v1 同时规定 Cloud 只保存 `apc-plugin.json + skills/...`，而当前 APM Local fixture 仍预置 Codex、Claude 与 Hermes 原生 manifest，两者不能互换。

本 feature 在不实现 APC 服务端、Submission 或本地发布命令的前提下，完成以下同一边界内的工作：补齐 APC Agent 只读契约；为 APM 建立统一虚拟 Cloud Client；提供显式可切换的 Local/Remote provider；把 Local fixture 切换到 APC canonical 内容；并让 Plugin Resource 从 `apc-plugin.json` 派生当前 Agent 所需配置。

## 目标

- `agent` 作为第四种可读取 Power，复用 APC 的 list、series、latest、exact metadata 与 exact content 路径。
- Agent content 是原始 `agent.yaml` 字节，`Content-Type` 为 `application/yaml`，size 与 MD5 均针对原始字节。
- APM 的 `cloud.Client` 由能力小接口组成，Local 与 Remote 完整实现同一契约。
- Local 与 Remote 是同一静态二进制内的 provider 插件，由 composition root 显式注册和选择。
- Cloud provider 只获取、验证并写入调用方临时目标，不解析 APM cache 路径或发布最终缓存。
- Remote provider 完整支持 Skill、Plugin、MCP、Agent 四类 Power。
- Skill/Agent 的缓存命中、临时目标、原子发布与 staging 由 Cache 管理。
- Plugin Cloud/fixture 只维护 `apc-plugin.json + skills/...`；Plugin Resource 确定性生成当前已适配 Agent 的配置与 marketplace。
- `internal/cloud/resources` 全量改为 Local provider 的 canonical APC fixture。
- 保持现有 lifecycle、manifest、transaction、journal、Adapter 和 CLI 用户语义。

## 不在范围内

- APC 服务端、数据库、对象存储、发布管线或目录快照实现。
- Agent Submission、review ticket、yank/restore、后台管理或 Agent 内容上传校验。
- APM `validate`、`submit`、`submission get/list` 或配置管理命令。
- `/resources/version` 目录缓存、ETag 优化、离线目录、自动重试或 provider fallback。
- Go `.so` 动态插件、运行时插件下载或第三方 provider 发现。
- Plugin v2 的 commands、hooks、agents、内嵌 MCP 或其它未在 `apc-plugin.json` schema v1 声明的能力。
- 旧 Local Plugin fixture、旧 Plugin cache 或旧 Cloud Client API 的迁移与兼容层。
- 调用真实 Agent CLI、读取或修改真实 Agent home。

## 上游 APC 只读契约

### 可读取类型与非对称发布边界

Published Catalog 与匿名只读资源 API 支持：

- `skill`
- `plugin`
- `mcp`
- `agent`

本次只有前三种进入既有 Submission、review 与管理流程。`type=agent` 对 Submission 和管理入口返回 `400 UNSUPPORTED_RESOURCE_TYPE`；Agent 资源如何进入 Published Catalog 由 APC 后续发布设计决定，不在本 feature 中推断临时 API。

### 统一路径

Agent 不增加专属 `/agents` 路径，复用：

```http
GET /api/v1/resources?type=agent&view=all&limit=100&cursor=...
GET /api/v1/resources/agent/{name}
GET /api/v1/resources/agent/{name}/latest
GET /api/v1/resources/agent/{name}/versions/{version}
GET /api/v1/resources/agent/{name}/versions/{version}/content
```

list、series、latest 与 exact metadata 使用现有统一字段。Agent metadata 的 `content_type` 固定为 `application/yaml`；`content_size` 与 `content_md5` 对应 content 返回的原始 YAML 字节。下架版本的 exact content 仍返回 `410 Gone`。

### Agent content

- content 是单个 `agent.yaml` 的原始 UTF-8/YAML 字节，不包装为 `.tar.gz`。
- APC 不通过下载接口重写、规范化或重新序列化 YAML。
- APM Remote Client 只验证 HTTP 元数据、长度与 MD5；现有 Agent Adapter 在安装前继续解析 `schema_version`、`agent.id` 和 `agent.version` 并 fail closed。
- `PowerRef.Name`、`PowerRef.Version` 与 YAML 内身份对齐由 APC 资源管理方保证；本次不设计其写入流程。

## APM Cloud 端口

### 能力接口

`internal/cloud/client.go` 定义以下接口。实现重要接口时添加简要中文注释，说明用途、目标约束和精确版本要求。

```go
type Catalog interface {
    List(context.Context) ([]domain.PowerRef, error)
    Resolve(context.Context, domain.PowerRef) (domain.PowerRef, error)
}

type SkillSource interface {
    DownloadSkill(context.Context, domain.PowerRef, string) error
}

type PluginSource interface {
    DownloadPlugin(context.Context, domain.PowerRef, string) error
}

type MCPSource interface {
    GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error)
}

type AgentSource interface {
    DownloadAgent(context.Context, domain.PowerRef, string) error
}

type Client interface {
    Catalog
    SkillSource
    PluginSource
    MCPSource
    AgentSource
}
```

契约如下：

- 所有可能访问文件或网络的方法接收 `context.Context`。
- `List` 返回四类全部可下载精确版本，并按 type、name、SemVer 稳定排序。
- `Resolve` 要求 type/name；version 为空时解析 latest，非空时验证精确版本可用；返回值总是精确 `PowerRef`。
- Skill/Plugin destination 是尚未发布的空目录；成功后目录根就是资源根。
- Agent destination 是调用方提供的临时 `agent.yaml` 文件路径；成功后内容与 APC/fixture 原始字节一致。
- MCP 返回通过严格 JSON 与领域校验的克隆对象，不进入文件缓存。
- Client 不接收 cache 根、staging 根、manifest、journal 或 Agent 目标路径。
- APC description、发布时间、content metadata 与 problem DTO 都是 Remote 私有传输类型，不扩张 lifecycle 使用的 `PowerRef`。

### Provider 插件

`internal/cloud/provider.go` 定义：

```go
type ProviderID string
type Factory func() (Client, error)

const (
    ProviderRemote ProviderID = "remote"
    ProviderLocal  ProviderID = "local"
)
```

`ProviderRegistry` 是显式实例，不使用 package `init()` 或全局可变注册表。它拒绝空/重复 provider、nil factory、未知 provider、nil 或 typed-nil Client。composition root 创建 registry、注册两个 factory，再按配置调用 `Open`。

### 配置

| 配置 | 语义 |
| --- | --- |
| `APM_CLOUD_PROVIDER` | 默认 `remote`；只接受 `remote`、`local`。 |
| `APC_BASE_URL` | 覆盖 Remote 地址；未设置时使用代码常量 `http://127.0.0.1:8080`。 |
| `APM_CLOUD_LOCAL_ROOT` | Local provider 必填的绝对 fixture 根。 |

非 loopback Remote URL 必须为绝对 HTTPS；loopback 开发地址允许 HTTP。配置错误在创建 lifecycle 前失败。Remote 网络错误、协议错误或 APC 不可用时绝不自动回退 Local。本次不增加用户 CLI flag。

## Provider 实现

### Local

`internal/cloud/local` 从一个显式绝对根读取：

```text
skills/<name>/<version>/...
plugins/<name>/<version>/apc-plugin.json
plugins/<name>/<version>/skills/...
mcps/<name>/<version>/MCP.json
agents/<name>/<version>/agent.yaml
```

Local 枚举安全目录与 marker，不解析 cache 路径。Skill/Plugin 复制完整物理目录；Agent 复制单个普通文件；MCP 严格解码。所有复制都拒绝 symlink、hardlink 语义、特殊节点、祖先逃逸、source/destination 重叠和非空 destination，并在错误时清理本次半成品。

### Remote

`internal/cloud/remote` 使用注入的 `http.Client` 和已验证 base URL：

- `List` 遍历全部 cursor 页，要求 `snapshot_updated_at` 不变，拒绝 cursor 环、重复 `PowerRef`、未知类型、非法身份和畸形 DTO。
- `Resolve` 的空版本调用 latest；精确版本调用 exact metadata。
- 下载前读取 exact metadata 并验证返回身份、`published` 状态、Content-Type、size 与 32 位小写 MD5。
- content 写入私有临时文件，同时计算字节数和 MD5；只有二者匹配才允许解包、解码或发布到 destination。
- 拒绝跨 origin redirect，不携带 API Key，不自动重试，保留 context 取消原因。
- problem body 只读取有界片段；错误和日志不打印凭据、任意大响应或服务端内部内容。

composition root 提供带两分钟整体 request timeout 的默认 `http.Client`；测试可注入自定义 Client，但不能传入 nil 或使用无界默认值。调用方更短的 context deadline 优先生效。

所有 content 在读取前受 20 MiB 客户端硬上限约束，metadata 超限直接失败。Skill/Plugin 的 `.tar.gz` 在 MD5 验证后安全解包；解压后总大小上限 100 MiB、单文件 10 MiB、文件总数 1,000、目录深度 16、单一路径 255 字节、总解压倍率 100。解包拒绝绝对路径、`..`、symlink、hardlink、设备、FIFO、socket、嵌套归档和二进制。Agent 原样写入 destination；MCP 使用 `DisallowUnknownFields`、拒绝重复/尾随值，并按公共资源规则只允许 HTTPS URL。

### 共享错误

`internal/cloud/errors.go` 提供可用 `errors.Is` 判断的分类：

- `ErrNotFound`：本地缺失或 HTTP 404；
- `ErrYanked`：HTTP 410；
- `ErrInvalidResponse`：DTO、分页、状态或 Content-Type 不合法；
- `ErrIntegrity`：Content-Length、metadata size 或 MD5 不一致；
- `ErrUnsafeContent`：资源树或归档违反物理安全边界；
- `ErrProviderConfig`：provider、URL、fixture 根或 factory 配置错误。

实现保留底层 cause，并为网络、读取、校验、解包和目标路径增加稳定上下文。

## Cache 与 Lifecycle

`internal/cache.PowerCache` 从只负责 staging 调整为管理 Skill/Agent 的完整版本化缓存准备：

```go
PrepareSkill(context.Context, domain.PowerRef) (stagingRoot string, err error)
PrepareAgent(context.Context, domain.PowerRef) (stagingRoot string, err error)
```

Cache 构造时通过本包定义的小型 source 接口注入 SkillSource 与 AgentSource，不依赖 concrete provider。`Prepare*` 的顺序是：复验精确 cache hit；miss 时创建唯一临时目标；调用 source；复验最小 marker/物理路径；rename 原子发布；再原子物化公共 staging。并发同一版本只能产生一份有效最终缓存，失败不得覆盖既有版本或留下可命中的半成品。

Lifecycle 的变化限于：

- Search/List 把命令 context 传给 `Client.List`；
- Install 使用 `Resolve` 取得精确版本；省略 type 时仍通过完整 List 保留同名跨类型歧义规则；
- Skill/Agent 调用 `Prepare*`，不再执行“Cache → Cloud → Cache”；
- MCP 继续调用 `GetMCP`；
- Plugin 继续调用 `pluginresource.Ensure`；
- Uninstall/Fix 保持离线，不因 Remote provider 引入网络依赖。

Manifest-first unchanged、事务锁、journal phase、恢复顺序、Adapter CLI 和安装 outcome 均不改变。

## Canonical Plugin 与派生 Artifact

### Cloud/fixture 权威内容

每个版本只包含 APC Plugin v1：

```text
apc-plugin.json
skills/
└── <declared-skill>/
    ├── SKILL.md
    └── skill-card.md
```

`apc-plugin.json` 是唯一权威索引：

```json
{
  "schema_version": 1,
  "name": "demo-plugin",
  "version": "v0.1.0",
  "description": "Demo plugin",
  "skills": ["skills/demo-plugin"]
}
```

Local fixture 和 Remote 解包结果具有同一格式。raw package 禁止预置 `.codex-plugin`、`.claude-plugin`、`.hermes-plugin`、marketplace、commands、hooks、agents 或 MCP。

### 构建时派生

`pluginresource.Ensure` 在唯一 cache-miss 临时根内：

1. 下载 canonical package；
2. 严格解析 `apc-plugin.json`，拒绝未知/重复字段、错误 schema、身份不一致、不安全或重复 skill 路径、缺失/额外 Skill；
3. 保留 canonical package；
4. 确定性生成 `.codex-plugin/plugin.json`、`.claude-plugin/plugin.json`、`.hermes-plugin/plugin.yaml` 与 `.hermes-plugin/__init__.py`；
5. 从生成的 manifest 生成 Codex 与 Claude 单 Plugin 本地 marketplace；
6. 使用共享 validator 复验完整 artifact 后一次 rename 发布。

生成覆盖当前全部正式适配 Agent，而不是只覆盖本次安装目标；Bibit filesystem Plugin 复用生成的 Claude 版本身份，Hermes 使用 Hermes manifest。逻辑 Plugin 名和 selector 继续是 `<name>`，版本只存在于 `PowerRef`、APM manifest、cache 版本目录与派生原生 manifest。

最终布局继续是一份共享不可变 artifact：

```text
cache/plugins/<name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<name>/
    ├── apc-plugin.json
    ├── .codex-plugin/plugin.json
    ├── .claude-plugin/plugin.json
    ├── .hermes-plugin/plugin.yaml
    ├── .hermes-plugin/__init__.py
    └── skills/...
```

Recovery 仍只读取 journal、Adapter、manifest 与现有 artifact，不连接 Cloud，不重新生成或修复 cache。缺少 `apc-plugin.json` 的旧 artifact 视为损坏并 fail closed。

## 文件布局

### 新增

- `internal/cloud/provider.go`、`provider_test.go`：provider registry。
- `internal/cloud/local/*.go` 与对应测试：Local provider。
- `internal/cloud/remote/client.go`、`catalog.go`、`metadata.go`、`content.go`、`archive.go`、四类 Power 文件与对应测试：Remote provider。
- `cmd/apm/cloud.go` 与测试：provider 配置和 composition。
- `internal/cache/prepare.go` 与测试：Skill/Agent cache prepare。
- `internal/pluginresource/apc_manifest.go`、`agent_manifests.go` 与测试：canonical manifest 和派生配置。
- `ivory-tower/adr/ADR-016-select-cloud-clients-with-explicit-providers.md`。
- `ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md`。
- `ivory-tower/features/020-cloud-client-providers/spec.md` 与后续 `plan.md`。

### 移动或删除

- 现有 `internal/cloud/local_*.go` 移到 `internal/cloud/local/` 并适配新接口。
- 删除 Local Client 内解析 `paths.CachePath/CacheTempPath` 和直接发布 Skill/Agent cache 的逻辑。
- 删除 `internal/cloud/resources/plugins/*/*` 下预置的 `.codex-plugin`、`.claude-plugin`、`.hermes-plugin`；用 `apc-plugin.json` 替代。

### 修改

- `internal/cloud/client.go`、`errors.go`：组合端口与共享错误。
- `internal/cache/cache.go`、`filesystem.go`：source 注入、Prepare 与 staging。
- `internal/lifecycle/search.go`、`list.go`、`install*.go`、测试 fake：context、Resolve、Prepare。
- `internal/pluginresource/manager.go`、marketplace builder 与 `internal/pluginartifact/*`：canonical raw package 和派生 artifact 复验。
- `internal/domain/mcp.go`：公共 MCP HTTP endpoint 只允许 HTTPS。
- `cmd/apm/main.go`：统一 Client composition。
- `internal/cloud/resources`：canonical Local fixtures。
- `BUILD.md`、`ivory-tower/project-overview.md`、相关 C4：只同步本 feature 已实现行为。
- `../apc/ivory-tower/project-overview.md`、`project-overview-apm.md`：Agent 只读接口与 APM 消费契约。
- `docs/Changelog.md`：仅在行为实现与全量验证完成后增加一条请求级记录。

## 测试与验收

### Provider contract

- 同一套 contract suite 分别运行 Local 与 Remote，覆盖 List、Resolve、Skill、Plugin、MCP、Agent。
- 相同 canonical fixture 经 Local/Remote 获取后，领域值与物理内容语义一致。
- destination 预存、路径逃逸、source/destination 重叠、context 取消和 typed-nil 依赖均 fail closed。

### Remote HTTP

- 使用 `httptest.Server`，不访问真实 APC。
- 覆盖多页、空目录、snapshot 变化、cursor 环、重复 ref、unknown type、latest、exact、404、410、problem body 上限。
- 覆盖错误 Content-Type、Content-Length、metadata size、MD5、截断、超长、跨 origin redirect 和取消。
- 归档攻击矩阵覆盖绝对路径、`..`、symlink、hardlink、特殊文件、嵌套归档、二进制与全部资源上限。

### Cache 与 Plugin

- Skill/Agent 覆盖 hit、miss、并发同版本、下载失败、校验失败、rename 竞争、失败清理和 staging 原子替换。
- Plugin fixture 断言 Cloud 只含 `apc-plugin.json`；Ensure 后才出现四类 Agent 派生配置与双 marketplace。
- Plugin 覆盖 schema、identity、skills 闭集、确定性生成、cache hit 不重建、旧 artifact fail closed 与离线 recovery。
- 四 Agent install/upgrade/downgrade/uninstall/fix 的既有验收继续通过。

### Composition 与隔离

- 覆盖默认 Remote、环境覆盖 Base URL、显式 Local、Local 绝对根、unknown provider、重复注册和禁止 fallback。
- 所有 Agent home、APM home、cache、staging、fixture 与 fake CLI 位于 `t.TempDir()`。
- 不查找或调用真实 Agent CLI，不读取或修改真实 Agent 配置。

### 完成门禁

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -o "$(mktemp -d)/apm" ./cmd/apm
git diff --check
```

APC 文档仓库与 APM 仓库分别要求 clean diff、无占位符和内部链接有效。

## 迁移与兼容

- `APM_CLOUD_PROVIDER` 未设置时从硬编码 Local 切换为 Remote，默认地址为 `http://127.0.0.1:8080`；开发者使用 fixture 必须显式选择 Local 并提供绝对根。
- 旧 Cloud Client 方法签名不保留兼容包装；仓库内调用方一次性迁移。
- `internal/cloud/resources` 一次性迁移为 APC canonical fixture，不保留原生 manifest 副本。
- 已存在且缺少 `apc-plugin.json` 的旧 Plugin artifact 不自动重写、删除或重新下载，读取时 fail closed。
- 旧 Local/Remote MCP definition 若使用明文 HTTP URL 将变为非法，APM 不自动改写 endpoint 或 journal。
- manifest、journal schema、安装目标和 Adapter selector 不迁移。

## 成功标准

- APM 在不改变 lifecycle 业务结果的前提下，可通过配置选择 Local 或 Remote provider。
- Remote provider 能从 APC 统一只读接口获取四类 Power，并在任何完整性失败时阻止内容进入 cache、staging 或 Agent。
- Local 与 Remote 对相同资源产生等价输入，Local fixture 不再包含 Cloud 不应维护的 Agent 原生 Plugin 配置。
- Agent 使用通用 APC 只读路径和原始 YAML content；Submission 与管理面没有被意外扩张。
- Plugin 以 `apc-plugin.json` 为唯一 Cloud 权威，并在 APM cache-miss 构建时一次生成当前全部 Agent 所需派生配置。
- 新增接口及关键 lifecycle 具备简要、规范的中文注释；文档、ADR、C4、BUILD 与实现一致。

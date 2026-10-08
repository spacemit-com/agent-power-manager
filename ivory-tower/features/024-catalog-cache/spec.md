# Feature 024：版本化资源目录缓存

> 局部取代说明：本文保留 Feature 024 落定时的历史设计；其中“Agent 使用原始 YAML”的 content 约定
> 已由 [Feature 026](../026-agent-package-assets/spec.md) 与
> [ADR-023](../../adr/ADR-023-package-agent-with-assets.md) 取代（Agent 改为完整目录资源，Local Provider
> 生成确定性 `.tar.gz`，远程 content 为 `application/gzip`），当前实现与接口以 Feature 026/ADR-023
> 为准；本文的目录缓存、version 检查与 Snapshot 契约中未被修改的部分继续有效。

- Status：Accepted
- Date：2026-08-25
- Related ADRs：[ADR-021](../../adr/ADR-021-cache-versioned-resource-catalog.md)、APC 契约 [../../../APC-API.md](../../../APC-API.md)（原 APC 仓库已停止维护）

## 1. Goal

为 APM 增加类似 apt 索引的完整资源目录缓存。`list`、`search` 和确实需要解析资源的
`install` 在执行前检查 APC 的 catalog SHA-256；只有内容变化时才下载完整 catalog，
其余查询全部基于本地不可变快照完成。这样既避免每次遍历远程资源列表，也不再要求
APC 维护公开资源 cursor 分页、series、latest 和 exact-metadata 接口。

本功能只缓存发布目录及下载校验元数据。Skill、Plugin、Agent 制品仍由既有 Cache
Manager/Plugin Resource 管理；MCP 仍不增加制品缓存。Submission、安装 manifest、
journal 和 Agent CLI 状态不进入 catalog。

## 2. Scope

### 2.1 Included

- 独立 `internal/catalog` Manager、Repository 与不可变 Snapshot。
- Remote 与 Local Provider 统一实现 catalog version 和完整 catalog 来源。
- 全局唯一 `~/.apm/catalog.json`，以文件原始字节 SHA-256 作为同步标记。
- `list/search/install` 的自动 version 检查、必要更新和 stale fallback。
- catalog 中按 Skill、Plugin、MCP、Agent 分组并独立维护类型 schema version。
- install 从 catalog 完成类型歧义、显式版本与 latest 解析，并消费其中的精确 content
  metadata。
- 删除不再需要的 Cloud `List/Resolve`、公开分页、series、latest 和 exact-metadata
  调用及兼容层。
- APM/APC Feature、ADR、Project Overview、BUILD、C4 与 APC HTTP API 规范更新。

### 2.2 Excluded

- APC 服务端、数据库、对象存储、发布事务或队列的具体实现。
- `apm update`、强制刷新、缓存清理或缓存状态命令。
- 多来源 catalog 并存；APM 全局只保留一份 catalog。
- catalog 历史版本、增量 diff、分片、分页或 transport gzip。
- `list --installed`、`uninstall` 和 `fix` 的目录同步。
- 使用 stale catalog 时在线确认下架状态。
- Submission API、内部 review-ticket 分页和 Agent Adapter 行为修改。

## 3. User-visible Command Contract

### 3.1 自动同步范围

以下命令在一次调用内最多检查一次 catalog version：

- `apm list` 的 Cloud 模式；
- `apm search`；
- `apm install` 中确实需要 Cloud 目录解析的路径。

以下路径不检查 catalog version：

- `apm list --installed`；
- `apm uninstall`；
- `apm fix`；
- install 的显式 Plugin 精确 manifest 全命中 `unchanged` 早退。

Uninstall 继续以本地 manifest 和目标 Agent 实际状态为权威。MCP 在 `present` 时
只按名称调用 CLI remove，卸载 journal 不保存 Server definition；`absent` 时只清理
manifest。卸载恢复按 CLI 实际状态收敛到“已卸载”，不重新添加 MCP。因此
`uninstall` 始终不读取 catalog 或 MCP content。MCP 安装和版本切换 journal 仍保留
新旧 definition，用于安装回滚。

### 3.2 stale fallback

当 version 或 catalog 更新失败时：

- 本地存在完整、严格有效的 catalog：继续执行并向 stderr/log 输出一次 warning；
- 本地 catalog 缺失或损坏：命令失败；
- caller 主动取消：立即返回取消错误，不 fallback；
- JSON stdout 不包含 warning，也不因 warning 改变结果 schema。

stale catalog 可用于离线 list/search。Install 若同时命中既有有效制品缓存可以离线成功；
若需要下载 content，则按原网络错误失败。离线期间无法确认版本是否刚被下架，这是本功能
明确接受的 apt 式可用性权衡。

Provider、Remote origin 或 Local root 变化不单独使缓存失效。只要新来源返回的 catalog
SHA-256 与本地有效文件相同就直接复用；hash 不同才更新。

## 4. Architecture

### 4.1 Domain model

新增不可变 catalog 领域值：

```go
type CatalogVersion struct {
    SHA256 string
    Size   int64
}

type CatalogEntry struct {
    Ref          PowerRef
    Description  string
    Message      string
    ContentType  string
    ContentSize  int64
    ContentMD5   string
    PublishedAt  string
    Tags         []string
}
```

具体字段名可按 Go 风格调整，但语义不得减损。构造后不得向调用者暴露可变的内部 slice。
`CatalogEntry.Ref` 永远包含精确 `vMAJOR.MINOR.PATCH`，不存储 `latest`。

### 4.2 Cloud source boundary

Cloud Provider 只提供 catalog 数据源和精确 content：

```go
type CatalogSource interface {
    CatalogVersion(context.Context) (domain.CatalogVersion, error)
    DownloadCatalog(context.Context, string) error
}
```

`DownloadCatalog` 的 destination 必须不存在。Provider 不选择 `~/.apm` 路径、不持有
APM 锁，也不决定 stale fallback。

删除 Cloud `Catalog.List/Resolve`。四类精确 content 接口改为消费完整、已验证的
`CatalogEntry`，不再自行请求 latest 或 exact metadata：

```go
DownloadSkill(ctx, entry, destination)
DownloadPlugin(ctx, entry, destination)
GetMCP(ctx, entry)
DownloadAgent(ctx, entry, destination)
```

Cache Manager、Plugin Resource 和 lifecycle 必须透传同一 entry，禁止重新拼装或只传
`PowerRef` 后私自回源读取 metadata。

### 4.3 Catalog Manager

`internal/catalog.Manager` 只编排缓存同步：

- `Snapshot(ctx)`：为 list/search 获取全局锁后执行同步；
- `SnapshotLocked(ctx)`：为已经持有全局锁的 install 执行同一状态机，禁止二次加锁；
- 两个入口共享一个私有实现，不复制校验、fallback 或替换逻辑；
- 返回 `Snapshot`、`SyncStatus` 和 error；warning 由上层统一渲染。

`SnapshotLocked` 是受约束接口，中文注释和测试必须明确调用方已持有 `apm.lock`。
Manager 不执行 transaction Recover，不读取安装 manifest，也不操作制品缓存。

全局锁继续使用既有非阻塞 `TryLock` 语义：若另一个进程已经持锁，list/search 的
`Snapshot(ctx)` 返回 `lock.ErrLocked`，不等待、不使用 stale fallback。Stale fallback 只处理
Provider/version/catalog 更新失败，不能绕过本地共享状态互斥失败。

### 4.4 Repository and Snapshot

`internal/catalog.Repository` 负责：

- 从唯一缓存路径读取物理普通文件；
- 有界读取、SHA-256 和严格 JSON/schema 校验；
- 以 `0600` 权限原子发布已验证 catalog；
- 拒绝 symlink、hardlink、路径替换和目标竞争；
- 返回不共享内部可变内存的 `Snapshot`。

`Snapshot` 提供：

- `List(type?)`；
- `Resolve(PowerRef)`，支持显式版本或同类型最高 SemVer；
- `Metadata(exact PowerRef)`。

Snapshot 不访问网络或文件系统。跨类型同名歧义仍由 Install lifecycle 按既有错误语义处理。

## 5. Catalog JSON v1

### 5.1 Envelope and groups

```json
{
  "schema_version": 1,
  "skills": {
    "schema_version": 1,
    "items": []
  },
  "plugins": {
    "schema_version": 1,
    "items": []
  },
  "mcps": {
    "schema_version": 1,
    "items": []
  },
  "agents": {
    "schema_version": 1,
    "items": []
  }
}
```

顶层 schema 管理 catalog envelope；四个 Power 类型分组分别维护自己的 schema version，
允许未来独立演进。四组必须全部存在，`items` 必须为数组，空组必须编码为 `[]`，不能为
`null`。未知顶层字段、未知分组字段和未知条目字段在 v1 中全部拒绝。

### 5.2 Item

条目不重复 `type`，类型由所在分组唯一确定：

```json
{
  "name": "code-review-helper",
  "version": "v1.2.0",
  "description": "辅助进行代码评审",
  "message": "增加新的检查规则",
  "content_type": "application/gzip",
  "content_size": 18342,
  "content_md5": "8c7dd922ad47494fc02c388e12c00eac",
  "published_at": "2026-08-18T09:30:12Z",
  "tags": ["code-review", "quality"]
}
```

校验规则：

- name 使用 canonical Power name；
- version 是严格、无 prerelease/build metadata 的 `vMAJOR.MINOR.PATCH`；
- description/message trim 后非空；
- content type 与分组匹配；
- `content_size` 必须为正数（`> 0`），且不超过对应 Power content 的既有类型上限；当前 Skill/Plugin
  指 `.tar.gz` 原始字节、MCP/Agent 指 JSON/YAML 原始字节，四类上限均为 20 MiB；
- MD5 是 32 位小写十六进制；
- published_at 是 UTC RFC 3339；
- tags 非空值、排序、去重并满足 APC tag 规则；
- 同组 name/version 唯一，跨组同名允许；
- 每组按 name 升序、同名 version SemVer 降序排列。

整个 catalog 使用确定性 JSON 字节。相同逻辑目录必须生成完全相同的响应字节和 SHA-256。
APM 接受的 catalog 最大为 100 MiB。

## 6. Synchronization State Machine

在全局锁内：

1. 读取并严格验证 `~/.apm/catalog.json`，同时计算本地 SHA-256。
2. 调用 Provider `CatalogVersion`。
3. 若远端 SHA-256 与本地有效文件相同，直接返回本地 Snapshot。
4. 若不同，下载到 `~/.apm/.tmp/` 下唯一、私有、调用方拥有的临时文件。
5. 下载过程执行 size 上限；完成后严格验证 JSON、group schema、全部条目和排序。
6. 实际 size 与 SHA-256 必须和 version 响应精确一致。
7. fsync 后以 no-replace/原子 rename 设施替换 `catalog.json`，再从同一已验证字节返回 Snapshot。
8. 失败时精确清理本次临时文件，永不删除或覆盖旧有效 catalog。

错误处理：

- 本地损坏但在线更新成功：静默修复；
- version/catalog transport、HTTP、协议、hash 或 schema 失败且旧缓存有效：返回 stale status；
- 无有效旧缓存：返回 `ErrCatalogUnavailable`，同时保留具体 cause；
- `context.Canceled`：保持 identity 并立即终止；
- warning 只包含安全、受控上下文，不包含凭据、响应 body 或服务端不受控 detail。

一次命令只生成一次 stale warning。Manager 不能把失败更新写成新的同步状态。

## 7. Lock and Lifecycle Ordering

List/Search 保留原有 journal 只读 Inspect 行为，不执行恢复。随后 `Snapshot(ctx)` 尝试短时
持有 `apm.lock` 完成 version 检查/更新，释放锁后才做本地过滤和输出；锁已占用时保持既有
`ErrLocked` 失败语义。

Install 保留完整锁区间：

1. Acquire；
2. Recover；
3. RefreshInstalled 与目标 Agent 解析；
4. 显式精确 Plugin manifest 全命中时离线 `unchanged` 早退；
5. 需要目录解析时调用 `SnapshotLocked(ctx)`；
6. 使用 Snapshot 解析精确 CatalogEntry；
7. 继续既有制品、Adapter 与 Transaction lifecycle；
8. Release。

该顺序既避免 Manager 嵌套获取同一非重入锁，也保持 manifest-first 和事务恢复语义。

## 8. Provider Behavior

### 8.1 Remote

- version 和 catalog 请求匿名，不携带 Submission API Key；
- version 响应、catalog Content-Type/Length 和实际字节严格校验；
- content 下载只使用 CatalogEntry metadata，验证 media type、实际 size 和 MD5；
- content 404/410 或完整性错误 fail closed，不切换 Provider；
- 不再调用公开分页、series、latest 或 exact-metadata endpoint。

### 8.2 Local

Local Provider 也实现同一 CatalogSource，而不是绕过持久化缓存：

- 从 fixture 资源生成确定性四分组 catalog；
- Skill/Plugin 使用与下载路径相同的确定性 gzip 内容；
- MCP 使用完整资源 JCS 字节；Agent 使用原始 YAML；
- description 和 content metadata 来自实际内容；
- message 固定为 `local fixture`，published_at 固定为 UTC epoch，tags 为 `[]`；
- fixture 内容不变时 version hash 必须不变；
- 后续下载消费的字节必须和 catalog metadata 完全一致。

Local 仅用于开发与测试，允许 version 检查为生成完整 catalog 付出较高本地计算成本，不以
mtime 或不可靠的目录摘要替代内容 hash。

## 9. APC API Contract

### 9.1 Version

```http
GET /api/v1/resources/version
```

```json
{
  "schema_version": 1,
  "catalog_sha256": "<64 lowercase hex>",
  "catalog_size": 123456
}
```

APC 可返回相同 hash 的 ETag，但 APM 不依赖条件请求。version 必须和当前 catalog 对象来自
同一原子发布状态，不能使用独立、可能漂移的数据库时间戳。`catalog_size` 是完整 Catalog body 的
精确非负字节数（`>= 0`），并继续受 100 MiB Catalog 上限约束；它与 Item 必须为正数的
`content_size` 不是同一个边界。

### 9.2 Full catalog

```http
GET /api/v1/resources
```

响应为上述 Catalog JSON v1，Content-Type 为 `application/json`，Content-Length 与
version `catalog_size` 一致。接口不接受 type/view/limit/cursor 等查询参数。

删除公开接口：

- `GET /api/v1/resources/{type}/{name}`；
- `GET /api/v1/resources/{type}/{name}/latest`；
- `GET /api/v1/resources/{type}/{name}/versions/{version}`。

保留精确 content：

```http
GET /api/v1/resources/{type}/{name}/versions/{version}/content
```

资源发布、下架或恢复必须生成并原子发布新 catalog；Submission 和审核中间状态不改变它。
内网 review-ticket cursor 分页完全不受影响。

## 10. File and Interface Changes

主要新增文件：

- `internal/domain/catalog.go`
- `internal/catalog/{manager,repository,snapshot,errors}.go`
- `internal/cloud/{local,remote}/catalog.go`

主要修改边界：

- `internal/cloud/client.go`
- `internal/cache/*`
- `internal/pluginresource/*`
- `internal/lifecycle/{dependencies,list,search,install,install_resolve}.go`
- `internal/paths/*`
- `cmd/apm/{main,cloud}.go`

删除 Remote latest/series/exact metadata DTO、实现、fake 路由和测试依赖。最终精确文件列表由
实现计划在扫描当前代码后固定，不允许保留无人调用的兼容接口。

## 11. Testing and Acceptance

### 11.1 Repository and schema

- 有效四分组、空 `[]`、各组 schema version；
- 缺组、null、未知字段、错误 group schema；
- 非法身份、版本、metadata、tags、排序和重复 ref；
- 100 MiB 边界、超限、symlink/hardlink/替换竞争；
- 原子替换失败保留旧文件，临时文件精确清理。

### 11.2 Synchronization

- 首次同步、hash 相同不下载、hash 变化更新；
- version/下载/hash/schema 失败的有缓存/无缓存矩阵；
- 损坏缓存在线修复、离线拒绝；
- caller cancellation 不 fallback；
- 两个 list/search/install 进程通过全局锁互斥，后发的非阻塞 Acquire 返回 `ErrLocked`；
- `SnapshotLocked` 不二次加锁且 install 无死锁。

### 11.3 Commands and lifecycle

- list/search/install 每次至多一次 version；
- type group 筛选、跨类型歧义、latest/显式版本解析；
- installed list、四类 uninstall、fix、Plugin unchanged 早退零 catalog 调用；
- MCP present/absent/CLI 删除后崩溃的恢复全程零 definition 和 catalog 调用；
- stale list/search；已有 artifact 离线 install；cache miss 下载失败；
- warning 只出现一次且不污染 JSON stdout；
- transaction Recover、manifest-first 和结果顺序无回归。

### 11.4 Provider contract

- Local/Remote 产生并消费相同 Catalog JSON/domain 契约；
- Remote version/catalog 无凭据，Submission `X-API-Key` 隔离保持；
- content type/size/MD5 与 entry 一致；下架竞争返回 410；
- 严格 fake 证明旧分页/latest/exact-metadata endpoint 零调用；
- 生产扫描不存在已删除接口、cursor 或 DTO。

### 11.5 Full gates

- focused、affected、`go test -count=1 ./...`、race、vet；
- 隔离 build 和相关平台文件交叉编译；
- Mermaid/Structurizr、Markdown link、stale-current-contract 和 diff/status；
- 实现完成后 APM Changelog 只增加一条本用户请求记录。

## 12. Documentation Authority

APM 当前文档需更新 BUILD、Project Overview、Cloud/Cache/Core Lifecycle C4 和 list/search/install
流程图，并引用 ADR-021。Feature 020 中“不增加资源更新时间缓存”和 Feature 023 中“APC
不可用时不使用过期目录”的部分由本 Feature 局部取代，历史正文保留并添加显式取代说明。

APC 当前文档需更新 HTTP API、两个 Project Overview、APM C4，并引用 ADR-004。公开资源
cursor/series/latest/exact-metadata 契约被取代；内部 review-ticket cursor 与 Submission
active-only 无分页契约继续有效。

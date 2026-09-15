# Cloud Client Providers and Canonical APC Plugins Implementation Plan

> 历史计划说明：本文记录 Feature 020 当时的实现步骤，不作为当前目录发现或 MCP 卸载接口
> 权威。公开 `List/Resolve`、cursor/series/latest/exact-metadata、按 Ref 重查 metadata 和
> definition-bearing MCP uninstall 已由 [Feature 024](../024-catalog-cache/spec.md) 与
> [ADR-021](../../adr/ADR-021-cache-versioned-resource-catalog.md) 取代。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 APM 通过显式 provider 在 Local canonical fixture 与 Remote APC 之间切换，安全获取 Skill、Plugin、MCP、Agent，并从 `apc-plugin.json` 构建当前 Agent 所需的共享不可变 Plugin artifact。

**Architecture:** `internal/cloud` 只保留组合 Client 端口、共享错误和显式 Provider Registry，Local/Remote 位于并列子包。Cloud source 只写调用方临时目标；Skill/Agent cache 由 `internal/cache` 发布，Plugin canonical package 由 `internal/pluginresource` 派生原生 manifest/marketplace 后发布，MCP 直接返回领域对象。composition root 是唯一 provider 选择点，默认 Remote、允许环境覆盖、禁止自动 fallback。

**Tech Stack:** Go 1.26.5、标准库 `net/http`/`archive/tar`/`compress/gzip`/`crypto/md5`、`github.com/Masterminds/semver/v3`、`go.yaml.in/yaml/v3`、现有 Cobra/lifecycle/transaction/pluginresource 架构。

**Spec:** `ivory-tower/features/020-cloud-client-providers/spec.md`

## Global Constraints

- 开始执行前使用 `superpowers:using-git-worktrees` 创建隔离 worktree；APM 设计基线提交为 `b2dce66`，APC 接口文档提交为兄弟仓库的 `f470470`，MCP 卸载边界澄清提交为 `f22f2f4`。
- 实现采用 TDD；每个行为先出现可解释的 RED，再写最小实现并运行该包与受影响包测试。
- APM 仍交付单个静态二进制；不得使用 Go `.so`、package `init()` 隐式注册或动态 provider 下载。
- `APM_CLOUD_PROVIDER` 默认 `remote`；`APC_BASE_URL` 默认 `http://127.0.0.1:8080`；`APM_CLOUD_LOCAL_ROOT` 选择 Local 时必须是绝对路径。
- 非 loopback Remote URL 必须为 HTTPS；任何 Remote 错误不得回退 Local。
- Cloud provider 不读取 APM cache/staging/manifest/journal/Agent target 路径；所有输出先进入调用方私有临时目标。
- Remote 默认 `http.Client` 整体 timeout 为两分钟；所有 API 接受并尊重 `context.Context`。
- 所有 content 最大 20 MiB；归档解压后总大小 100 MiB、单文件 10 MiB、文件数 1,000、深度 16、路径 255 字节、解压倍率 100。
- 归档和 Local tree 拒绝路径逃逸、symlink、hardlink、设备、FIFO、socket、嵌套归档和二进制。
- APC 响应可增加未知可选字段；Remote DTO 必须忽略未知响应字段，但拒绝重复键、尾随 JSON、未知资源类型和未知状态。
- MCP 公共 HTTP endpoint 只允许绝对 HTTPS URL；stdio/http 字段继续严格互斥。
- Plugin Cloud/fixture 只含 `apc-plugin.json + skills/...`；Agent 原生 manifest 与 marketplace 只能由 APM 在 cache-miss 临时 artifact 内生成。
- 同一 Plugin 版本一次生成 Codex、Claude、Hermes、Bibit 当前所需配置；cache hit/recovery 不重建、不联网。
- 旧 Cloud Client 签名、旧 Plugin fixture 与旧 cache 不保留兼容分支；旧 artifact 缺少 `apc-plugin.json` 时 fail closed。
- Plugin Uninstall 与 Fix 保持离线；MCP 不新增定义缓存，其目标仍存在时的可恢复卸载继续按 manifest 精确版本调用 `GetMCP`。manifest-first、全局锁、journal schema、phase、Adapter selector、outcome 不变。
- 新增重要接口与主要生命周期函数添加简短、规范的中文注释。
- 测试只使用 `httptest.Server`、`t.TempDir()` 与 fake CLI；不得访问真实 APC、Agent CLI 或真实 Agent home。
- `docs/Changelog.md` 仅在全部实现、最终审查和门禁通过后写一次请求级条目；文档/计划阶段不提前记录。

## Final File Map

```text
internal/cloud/
├── client.go                 # Catalog 与四类 Source 端口
├── errors.go                 # 跨 provider 稳定错误
├── provider.go               # ProviderID、Factory、ProviderRegistry
├── local/
│   ├── client.go             # Local 构造、List、Resolve
│   ├── filesystem.go         # source/destination 物理边界和安全复制
│   ├── skill.go
│   ├── plugin.go
│   ├── mcp.go
│   └── agent.go
└── remote/
    ├── client.go             # Base URL、HTTP Client、request helper
    ├── catalog.go            # 分页 List、latest/exact Resolve
    ├── metadata.go           # APC DTO 与 exact metadata
    ├── content.go            # 有界下载、size/MD5、原子目标
    ├── archive.go            # tar.gz 安全解包
    ├── skill.go
    ├── plugin.go
    ├── mcp.go
    └── agent.go
internal/utils/jsonstrict/    # 重复键、尾随值、可选 unknown-field 策略
internal/cache/prepare.go     # Skill/Agent cache ensure + staging
internal/pluginartifact/apc_manifest.go
internal/pluginresource/agent_manifests.go
cmd/apm/cloud.go              # provider 配置与 composition
```

---

### Task 1: Make Catalog Context-Aware and Add Exact Resolution

**Files:**
- Modify: `internal/cloud/errors.go`
- Create: `internal/cloud/errors_test.go`
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_client.go`
- Modify: `internal/cloud/local_mcp.go`
- Modify: `internal/cloud/local_client_test.go`
- Modify: `internal/cloud/local_mcp_test.go`
- Modify: `internal/lifecycle/search.go`
- Modify: `internal/lifecycle/list.go`
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_resolve.go`
- Modify: `internal/lifecycle/search_test.go`
- Modify: `internal/lifecycle/list_test.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/mcp_integration_test.go`
- Modify: `internal/lifecycle/plugin_acceptance_test.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: existing `domain.PowerRef` and exact `vMAJOR.MINOR.PATCH` version policy.
- Produces: `Catalog.List(context.Context)` and `Catalog.Resolve(context.Context, domain.PowerRef)`; all later providers implement them.

- [ ] **Step 1: Add RED tests for Local Resolve and context propagation**

Add cases equivalent to the following, plus a `v0.10.0`/`v0.2.0` ordering case that requires type/name ascending and SemVer ascending rather than lexical version order:

```go
func TestLocalClientResolveReturnsLatestOrExact(t *testing.T) {
    client := newMCPTestClient(t, writeTwoVersionRoot(t))
    latest, err := client.Resolve(context.Background(), domain.PowerRef{
        Type: domain.PowerTypeMCP, Name: "filesystem",
    })
    if err != nil || latest.Version != "v0.2.0" { t.Fatalf("Resolve latest = %#v, %v", latest, err) }
    exact, err := client.Resolve(context.Background(), domain.PowerRef{
        Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v0.1.0",
    })
    if err != nil || exact.Version != "v0.1.0" { t.Fatalf("Resolve exact = %#v, %v", exact, err) }
}

func TestSearchPassesCancelledContextToCloud(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel()
    _, err := fixture.search.Search(ctx, cli.Flags{Keyword: "demo"})
    if !errors.Is(err, context.Canceled) { t.Fatalf("Search() error = %v", err) }
}
```

- [ ] **Step 2: Run focused tests and capture RED**

Run:

```bash
go test -count=1 ./internal/cloud ./internal/lifecycle -run 'Test(LocalClientResolve|SearchPassesCancelledContext)'
```

Expected: compile failure because `Resolve` and context-aware `List/GetMCP` do not exist.

- [ ] **Step 3: Define Catalog and update current Client incrementally**

Use these exact signatures in `internal/cloud/client.go`:

```go
type Catalog interface {
    List(context.Context) ([]domain.PowerRef, error)
    Resolve(context.Context, domain.PowerRef) (domain.PowerRef, error)
}

type Client interface {
    Catalog
    DownloadSkill(domain.PowerRef) error
    GetMCP(context.Context, domain.PowerRef) (domain.MCPServer, error)
    DownloadPlugin(context.Context, domain.PowerRef, string) error
    DownloadAgent(domain.PowerRef) error
}
```

This is an intermediate compile-safe shape; Task 2 replaces the remaining two cache-owning signatures and decomposes all Source interfaces. Do not add wrappers after Task 2.

- [ ] **Step 4: Implement Local List/Resolve with strict identity and cancellation**

At the top of every method and inside each directory/version enumeration loop, return `ctx.Err()` when cancelled. `List` must preserve the existing type/name ascending presentation order while making version ordering SemVer-ascending. `Resolve` must require non-empty safe type/name, call `List(ctx)`, filter exact type/name, return exact match when `Version` is present, otherwise select the greatest SemVer without mutating the slice:

```go
func (c *LocalClient) Resolve(ctx context.Context, query domain.PowerRef) (domain.PowerRef, error) {
    refs, err := c.List(ctx)
    if err != nil { return domain.PowerRef{}, err }
    // Filter exact Type/Name, select query.Version or SemVer maximum.
    // Return cloud.ErrNotFound when no exact candidate exists.
}
```

Update `GetMCP(ctx, ref)` and check cancellation before any `Lstat/ReadFile`.

- [ ] **Step 5: Make lifecycle use Resolve without changing ambiguity rules**

Add a helper with this behavior:

```go
func (s *InstallService) resolvePower(ctx context.Context, flags cli.Flags) (domain.PowerRef, error) {
    if flags.Type != "" {
        return s.cloud.Resolve(ctx, domain.PowerRef{
            Type: domain.PowerType(flags.Type), Name: flags.Name, Version: flags.Version,
        })
    }
    refs, err := s.cloud.List(ctx)
    if err != nil { return domain.PowerRef{}, fmt.Errorf("list Cloud powers for install: %w", err) }
    return resolveInstallPower(flags, refs, s.versions)
}
```

Keep `explicitPluginManifestHit` before Cloud access. Search/List call `s.cloud.List(ctx)`; MCP install calls `GetMCP(ctx, ref)`.

- [ ] **Step 6: Update all Cloud fakes and prove behavior**

Use this search to find every stale signature and update each result before committing:

```bash
rg -n 'func \([^)]*\) List\(\) \(\[\]domain\.PowerRef|\.cloud\.List\(\)|GetMCP\(domain\.PowerRef|func \([^)]*\) GetMCP\(domain\.PowerRef' cmd internal --glob '*.go'
```

Run:

```bash
go test -count=1 ./internal/cloud ./internal/lifecycle ./internal/cli ./cmd/apm
go test -race -count=1 ./internal/cloud ./internal/lifecycle
```

Expected: PASS and no stale signature matches.

- [ ] **Step 7: Commit**

```bash
git add internal/cloud internal/lifecycle internal/cli cmd/apm
git commit -m "refactor(cloud): add context-aware catalog resolution"
```

---

### Task 2: Move Skill and Agent Cache Ownership out of Cloud

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_skill.go`
- Modify: `internal/cloud/local_agent.go`
- Modify: `internal/cloud/local_skill_test.go`
- Modify: `internal/cloud/local_agent_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Create: `internal/cache/prepare.go`
- Modify: `internal/cache/filesystem_test.go`
- Modify: `internal/cache/agent_test.go`
- Create: `internal/cache/prepare_test.go`
- Modify: `internal/lifecycle/install_skill.go`
- Modify: `internal/lifecycle/install_agent_power.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: Task 1 `Catalog` and context-aware Client.
- Produces: final `SkillSource`, `AgentSource`, `MCPSource`, `PluginSource`, combined `cloud.Client`; shared Cloud error taxonomy; `cache.PowerCache.PrepareSkill/PrepareAgent`.

- [ ] **Step 1: Write RED source-only and cache atomicity tests**

Tests must prove Local writes only the supplied target and Cache owns final paths:

```go
func TestLocalDownloadSkillWritesDestinationWithoutGlobalCache(t *testing.T) {
    cacheHome := filepath.Join(t.TempDir(), "cache-home")
    t.Setenv("XDG_CACHE_HOME", cacheHome)
    destination := filepath.Join(t.TempDir(), "download")
    err := client.DownloadSkill(context.Background(), ref, destination)
    if err != nil { t.Fatal(err) }
    if _, err := os.Stat(filepath.Join(destination, "SKILL.md")); err != nil { t.Fatal(err) }
    if _, err := os.Lstat(filepath.Join(cacheHome, "apm")); !errors.Is(err, os.ErrNotExist) {
        t.Fatalf("Local provider touched global cache: %v", err)
    }
}

func TestPrepareSkillDownloadsOnceThenUsesImmutableCache(t *testing.T) {
    source := &recordingSource{skillContents: map[string][]byte{"SKILL.md": []byte("skill")}}
    store := newTestFilesystemCache(t, source)
    if _, err := store.PrepareSkill(context.Background(), ref); err != nil { t.Fatal(err) }
    if _, err := store.PrepareSkill(context.Background(), ref); err != nil { t.Fatal(err) }
    if source.skillCalls != 1 { t.Fatalf("calls = %d, want 1", source.skillCalls) }
}
```

Add failure cleanup, non-empty destination, same-version concurrency, typed-nil source and Agent single-file cases.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/cloud ./internal/cache -run 'Test(LocalDownload|Prepare)'
```

Expected: compile failure for destination-aware methods and `Prepare*`.

- [ ] **Step 3: Define final Cloud capability interfaces and errors**

Replace the intermediate Client with:

```go
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

Add `ErrYanked`, `ErrInvalidResponse`, `ErrIntegrity`, `ErrUnsafeContent`, and `ErrProviderConfig` beside `ErrNotFound`. Keep them as stable `errors.Is` sentinels and add table tests proving wrapped classification. Add concise Chinese comments to every exported interface and sentinel; describe destination ownership and the exact-version precondition instead of restating method names.

Local Skill/Agent methods validate exact type/name/version, require an absent or empty physical destination, copy into a private sibling temporary target, then rename. They must not import `internal/paths`.

If a destination directory already exists and is physically empty, remove only that exact empty directory immediately before rename; never remove a non-empty destination or any ancestor.

- [ ] **Step 4: Give Cache a narrow injected source and final Prepare API**

Use a cache-owned structural interface:

```go
type Source interface {
    DownloadSkill(context.Context, domain.PowerRef, string) error
    DownloadAgent(context.Context, domain.PowerRef, string) error
}

type PowerCache interface {
    PrepareSkill(context.Context, domain.PowerRef) (string, error)
    PrepareAgent(context.Context, domain.PowerRef) (string, error)
}
```

`NewFilesystem(source Source)` rejects nil/typed-nil. `Prepare*` locks per final cache path and validates a hit. On miss, Skill creates one unique directory below `cache/.tmp` and passes that directory as destination; Agent creates one unique version directory there and passes `<temporary-version>/agent.yaml`. After verifying a physical `SKILL.md` or `agent.yaml`, Cache atomically renames the whole temporary version directory to `cache/<type>/<name>/<version>`, then invokes the existing staging helper. Never remove the shared cache root or another version.

- [ ] **Step 5: Collapse lifecycle cache-cloud-cache calls**

Replace both stage helpers with direct preparation:

```go
staging, err := s.cache.PrepareSkill(ctx, ref)
if err != nil { return cli.InstallItem{}, fmt.Errorf("prepare install power %+v: %w", ref, err) }
```

Use `PrepareAgent` for Agent. Remove lifecycle imports of `cache.ErrMiss`; cache miss is internal to Cache.

- [ ] **Step 6: Update composition and test fakes**

Construct cache with the same Client used by lifecycle:

```go
powerCache, err := cache.NewFilesystem(cloudClient)
```

Update fakes to implement destination methods and assert every destination is below the test temp root.

- [ ] **Step 7: Run focused, package and race gates**

```bash
go test -count=1 ./internal/cloud ./internal/cache ./internal/lifecycle ./cmd/apm
go test -race -count=1 ./internal/cache ./internal/lifecycle
rg -n 'DownloadSkill\([^,)]*\)|DownloadAgent\([^,)]*\)|StageSkill|StageAgent' cmd internal --glob '*.go'
```

Expected: tests PASS; search has no production stale method calls.

- [ ] **Step 8: Commit**

```bash
git add internal/cloud internal/cache internal/lifecycle cmd/apm
git commit -m "refactor(cache): own skill and agent publication"
```

---

### Task 3: Isolate Local Provider and Add Explicit Registry

**Files:**
- Create: `internal/cloud/provider.go`
- Create: `internal/cloud/provider_test.go`
- Create: `internal/cloud/local/client.go`
- Create: `internal/cloud/local/filesystem.go`
- Create: `internal/cloud/local/skill.go`
- Create: `internal/cloud/local/plugin.go`
- Create: `internal/cloud/local/mcp.go`
- Create: `internal/cloud/local/agent.go`
- Create: `internal/cloud/local/client_test.go`
- Create: `internal/cloud/local/filesystem_test.go`
- Create: `internal/cloud/local/skill_test.go`
- Create: `internal/cloud/local/plugin_test.go`
- Create: `internal/cloud/local/mcp_test.go`
- Create: `internal/cloud/local/agent_test.go`
- Create: `internal/cloud/local/cache_integration_test.go`
- Delete: `internal/cloud/local_client.go`
- Delete: `internal/cloud/local_skill.go`
- Delete: `internal/cloud/local_plugin.go`
- Delete: `internal/cloud/local_mcp.go`
- Delete: `internal/cloud/local_agent.go`
- Delete: `internal/cloud/local_client_test.go`
- Delete: `internal/cloud/local_skill_test.go`
- Delete: `internal/cloud/local_plugin_test.go`
- Delete: `internal/cloud/local_mcp_test.go`
- Delete: `internal/cloud/local_agent_test.go`
- Delete: `internal/cloud/local_cache_integration_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: final Task 2 `cloud.Client`.
- Produces: `cloud.ProviderRegistry`, `cloud.ProviderLocal`, `cloud.ProviderRemote`, and `local.New(resourcesRoot string) (cloud.Client, error)`.

- [ ] **Step 1: Write RED registry tests**

Cover successful open and every invalid state:

```go
func TestProviderRegistryOpensExplicitFactory(t *testing.T) {
    registry := cloud.NewProviderRegistry()
    want := &stubClient{}
    if err := registry.Register(cloud.ProviderLocal, func() (cloud.Client, error) { return want, nil }); err != nil { t.Fatal(err) }
    got, err := registry.Open(cloud.ProviderLocal)
    if err != nil || got != want { t.Fatalf("Open() = %v, %v", got, err) }
}
```

Add empty ID, duplicate ID, nil/typed-nil factory result and unknown provider cases; all configuration failures must wrap `cloud.ErrProviderConfig`.

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/cloud -run TestProviderRegistry
```

Expected: undefined registry symbols.

- [ ] **Step 3: Implement an instance registry without global state**

```go
type ProviderID string
type Factory func() (Client, error)

const (
    ProviderRemote ProviderID = "remote"
    ProviderLocal  ProviderID = "local"
)

type ProviderRegistry struct { factories map[ProviderID]Factory }
```

`Register` and `Open` use the existing typed-nil reflection pattern, return contextual errors wrapping `ErrProviderConfig`, and never mutate a registry during `Open`.

- [ ] **Step 4: Move Local implementation into its subpackage**

Use package `local`, constructor:

```go
func New(resourcesRoot string) (*Client, error)
```

Export `Client` only for construction and test type assertions; keep all fields and filesystem helpers private, and make production consumers depend on `cloud.Client`. Import shared errors as `cloud.ErrNotFound`, `cloud.ErrUnsafeContent`; keep all source/destination physical checks in `local/filesystem.go`. Delete cache fields and all `internal/paths` imports.

Add a Local safety matrix in `filesystem_test.go`: source symlink, hardlink (`Stat_t.Nlink > 1`), device/FIFO/socket, overlapping source/destination, pre-existing non-empty destination, binary/archive signature, file >10 MiB, tree >100 MiB, >1,000 nodes, depth >16 and path >255 bytes. Skill/Plugin use these tree limits; MCP/Agent enforce the 20 MiB content limit. Each failure wraps `cloud.ErrUnsafeContent` and leaves no destination or sibling temporary residue.

- [ ] **Step 5: Keep main compile-safe with explicit Local construction**

Before runtime selection is introduced in Task 9, change the existing helper to call `local.New(resourcesRoot)` and return `cloud.Client`. Do not register a half-implemented Remote factory.

- [ ] **Step 6: Run migration checks**

```bash
go test -count=1 ./internal/cloud/... ./cmd/apm
go test -race -count=1 ./internal/cloud/...
rg -n 'NewLocalClient|type LocalClient' internal/cloud cmd --glob '*.go'
```

Expected: tests PASS; stale Local symbols absent outside historical docs.

- [ ] **Step 7: Commit**

```bash
git add internal/cloud cmd/apm
git commit -m "refactor(cloud): isolate local provider plugin"
```

---

### Task 4: Implement Remote Catalog and Resolution

**Files:**
- Create: `internal/cloud/remote/client.go`
- Create: `internal/cloud/remote/client_test.go`
- Create: `internal/cloud/remote/catalog.go`
- Create: `internal/cloud/remote/catalog_test.go`
- Create: `internal/cloud/remote/metadata.go`
- Create: `internal/cloud/remote/metadata_test.go`

**Interfaces:**
- Consumes: `cloud.Catalog`, shared errors, `domain.PowerRef`.
- Produces: `remote.New(baseURL string, httpClient *http.Client) (*Client, error)`, `List`, `Resolve`, private exact metadata DTO.

- [ ] **Step 1: Write RED constructor and pagination tests**

Use `httptest.Server` and cover absolute URL, loopback HTTP, non-loopback HTTP rejection, nil/unbounded HTTP Client rejection, multi-page sort, snapshot change, cursor loop, duplicate ref, unknown type, trailing JSON, 404 and exact/latest identity mismatch.

```go
func TestListTraversesOneSnapshotAndSorts(t *testing.T) {
    server := newCatalogServer(t, pageOne, pageTwo)
    client := newRemoteTestClient(t, server.URL)
    got, err := client.List(context.Background())
    if err != nil { t.Fatal(err) }
    if !reflect.DeepEqual(got, wantSortedRefs) { t.Fatalf("List() = %#v", got) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/cloud/remote -run 'Test(New|List|Resolve)'
```

Expected: package or symbols absent.

- [ ] **Step 3: Implement validated Client construction**

`client.go` stores a copied, normalized `url.URL` and a value-copy of the injected Client. Reject userinfo, fragment, query, non-root path ambiguity, nil Client and `Timeout <= 0`. Permit HTTP only when `Hostname()` is `localhost`, loopback IP, or IPv6 loopback. Wrap the copied Client's redirect callback so Remote itself rejects every cross-origin redirect before optionally delegating a same-origin redirect to the caller callback; do not rely on composition code for this safety property.

```go
type Client struct {
    baseURL *url.URL
    http    *http.Client
}
```

- [ ] **Step 4: Implement bounded JSON request and problem mapping**

Create a request helper that uses `http.NewRequestWithContext`, `Accept: application/json`, rejects redirects via the Client-owned policy, reads JSON under 20 MiB, maps 404 to `ErrNotFound`, 410 to `ErrYanked`, and other non-2xx statuses to contextual errors with at most 8 KiB problem body.

- [ ] **Step 5: Implement Catalog DTOs and strict semantic validation**

Define private DTOs with all APC fields but ignore unknown optional fields. `List` requests `/api/v1/resources?limit=100`, appends only the opaque `cursor` returned by the preceding page, and never sends `view=latest`; after all pages it sorts type/name ascending and version by SemVer ascending. Validate type/name/version, canonical MD5, non-negative size, expected content type and RFC3339 timestamps before converting to `PowerRef`. Track `seenCursor`, `seenRef`, and first `snapshot_updated_at`.

- [ ] **Step 6: Implement Resolve**

Version empty requests `/latest`; exact version requests `/versions/{version}`. Build paths with escaped individual segments, never by string concatenating an untrusted full path. Exact metadata status must be `published`; `yanked` maps to `ErrYanked`.

- [ ] **Step 7: Run package and race tests**

```bash
go test -count=1 ./internal/cloud/remote
go test -race -count=1 ./internal/cloud/remote
```

- [ ] **Step 8: Commit**

```bash
git add internal/cloud/remote
git commit -m "feat(cloud): add remote catalog provider"
```

---

### Task 5: Add Strict JSON, Integrity Downloads, MCP and Agent

**Files:**
- Create: `internal/utils/jsonstrict/jsonstrict.go`
- Create: `internal/utils/jsonstrict/jsonstrict_test.go`
- Create: `internal/cloud/remote/content.go`
- Create: `internal/cloud/remote/content_test.go`
- Create: `internal/cloud/remote/mcp.go`
- Create: `internal/cloud/remote/mcp_test.go`
- Create: `internal/cloud/remote/agent.go`
- Create: `internal/cloud/remote/agent_test.go`
- Modify: `internal/cloud/remote/metadata.go`
- Modify: `internal/domain/mcp.go`
- Modify: `internal/domain/mcp_test.go`
- Modify: `internal/cloud/local/mcp.go`
- Modify: `internal/cloud/local/mcp_test.go`

**Interfaces:**
- Consumes: Remote exact metadata from Task 4.
- Produces: duplicate-key-safe JSON decoding, verified content helper, Remote `GetMCP` and `DownloadAgent`.

- [ ] **Step 1: Write RED strict JSON and integrity tests**

Test duplicate keys at every nesting level, trailing values, optional unknown response DTO fields, MCP unknown fields, content length mismatch, metadata size mismatch, invalid/case-changed MD5, over 20 MiB, cancellation, partial destination cleanup, cross-origin redirect and successful raw Agent byte preservation.

```go
func TestDownloadAgentRejectsMD5MismatchWithoutDestination(t *testing.T) {
    destination := filepath.Join(t.TempDir(), "agent.yaml")
    client := newContentClient(t, agentMetadataWithWrongMD5, []byte("schema_version: 1\n"))
    err := client.DownloadAgent(context.Background(), agentRef, destination)
    if !errors.Is(err, cloud.ErrIntegrity) { t.Fatalf("error = %v", err) }
    if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) { t.Fatalf("destination remains: %v", statErr) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/utils/jsonstrict ./internal/cloud/remote ./internal/domain
```

Expected: missing package/functions or current MCP HTTP acceptance failure.

- [ ] **Step 3: Implement reusable duplicate-key-safe JSON decoding**

Expose:

```go
func Decode(data []byte, target any, disallowUnknown bool) error
```

First walk `json.Decoder.Token()` with an object-key stack to reject duplicate keys; then decode one value into `target`, optionally call `DisallowUnknownFields`, and require the next decode to return `io.EOF`. Response DTOs pass `false`; MCP and canonical Plugin pass `true`.

- [ ] **Step 4: Implement verified content staging**

Use this private signature so the eventual rename never crosses filesystems:

```go
func (c *Client) downloadVerifiedContent(
    ctx context.Context,
    ref domain.PowerRef,
    expectedType string,
    tempParent string,
) (temporaryPath string, resultErr error)
```

The helper must request exact metadata, reject size over 20 MiB, GET exact content, require success and expected Content-Type, create its `0600` temp file under the caller-supplied physical `tempParent`, and stream through `io.LimitReader(size+1)` while hashing with `md5.New`. Close before comparison, then compare actual bytes and lowercase hex digest to metadata. `Content-Length` when present must equal metadata size. On any error remove only the created temp file.

- [ ] **Step 5: Implement Agent atomic file delivery**

Validate the destination is an absolute clean path whose existing ancestors are physical directories; destination must not exist. Rename the verified sibling temp file to destination with mode `0644`. Do not parse or reserialize YAML.

- [ ] **Step 6: Implement MCP decoding and HTTPS tightening**

Remote creates a private directory with `os.MkdirTemp("", "apm-remote-mcp-")`, downloads verified `application/json` below it, calls `jsonstrict.Decode(..., true)`, then `MCPServer.Validate()`, and always removes only that directory. Tests inject temp creation below `t.TempDir()` and assert cleanup. Change the domain HTTP branch to require `parsed.Scheme == "https"`; update Local to use the same strict decoder so both providers reject duplicate keys and plain HTTP.

- [ ] **Step 7: Run focused and affected tests**

```bash
go test -count=1 ./internal/utils/jsonstrict ./internal/domain ./internal/cloud/local ./internal/cloud/remote ./internal/adapter ./internal/transaction ./internal/lifecycle
go test -race -count=1 ./internal/cloud/remote
```

- [ ] **Step 8: Commit**

```bash
git add internal/utils/jsonstrict internal/cloud/remote internal/cloud/local internal/domain
git commit -m "feat(cloud): verify remote mcp and agent content"
```

---

### Task 6: Safely Download Skill and Plugin Archives

**Files:**
- Create: `internal/cloud/remote/archive.go`
- Create: `internal/cloud/remote/archive_test.go`
- Create: `internal/cloud/remote/skill.go`
- Create: `internal/cloud/remote/skill_test.go`
- Create: `internal/cloud/remote/plugin.go`
- Create: `internal/cloud/remote/plugin_test.go`
- Modify: `internal/cloud/remote/client_test.go`

**Interfaces:**
- Consumes: Task 5 verified temporary content.
- Produces: Remote `DownloadSkill` and `DownloadPlugin`; `*remote.Client` satisfies final `cloud.Client`.

- [ ] **Step 1: Write RED archive attack matrix**

Generate tar.gz fixtures in tests and cover regular success plus absolute paths, `../`, duplicate normalized paths, symlink, hardlink, device, FIFO, socket-like unsupported type, nested gzip/zip/tar signatures, ELF/PE/Mach-O/Wasm signatures, compressed >20 MiB, extracted >100 MiB, file >10 MiB, >1,000 nodes, depth >16, path >255 and ratio >100.

```go
func TestExtractArchiveRejectsPathEscapeWithoutDestination(t *testing.T) {
    archive := writeTarGz(t, tarEntry{Name: "../outside", Body: []byte("bad")})
    destination := filepath.Join(t.TempDir(), "resource")
    err := extractArchive(archive, destination)
    if !errors.Is(err, cloud.ErrUnsafeContent) { t.Fatalf("error = %v", err) }
    if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) { t.Fatalf("destination remains: %v", err) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./internal/cloud/remote -run 'Test(ExtractArchive|DownloadSkill|DownloadPlugin)'
```

- [ ] **Step 3: Implement two-phase extraction**

Open gzip/tar only after MD5 verification. Normalize every header with `path.Clean`, reject absolute/empty/dot/parent paths, enforce forward-slash semantics, track duplicate names and all counters before creating a node. Accept only directories and regular files; preserve only regular execute bits, otherwise use `0644`, directories `0755`.

Extract into a private sibling directory. After all limits and signatures pass, validate destination is absent or an empty physical directory, remove only that empty directory, then rename the private directory into place. Cleanup private output on all failures.

- [ ] **Step 4: Add content signature checks**

Read a bounded prefix of each regular file and reject ELF, PE, Mach-O, Wasm and common archive signatures independent of extension. Also reject archive extensions after case-folding. Count uncompressed bytes during `io.CopyN`, never trust tar header size alone.

- [ ] **Step 5: Implement Skill/Plugin methods and markers**

Both request `application/gzip`. Skill requires physical root `SKILL.md` and `skill-card.md`; Plugin requires physical root `apc-plugin.json`. Marker absence maps to `ErrUnsafeContent`, not `ErrNotFound`, because the remote identity existed but content was invalid.

- [ ] **Step 6: Assert full Client implementation and run gates**

```go
var _ cloud.Client = (*Client)(nil)
```

Run:

```bash
go test -count=1 ./internal/cloud/remote
go test -race -count=1 ./internal/cloud/remote
go vet ./internal/cloud/remote
```

- [ ] **Step 7: Commit**

```bash
git add internal/cloud/remote
git commit -m "feat(cloud): safely unpack remote power archives"
```

---

### Task 7: Make apc-plugin.json the Canonical Plugin Authority

**Files:**
- Create: `internal/pluginartifact/apc_manifest.go`
- Create: `internal/pluginartifact/apc_manifest_test.go`
- Modify: `internal/pluginartifact/validation.go`
- Modify: `internal/pluginartifact/validation_test.go`
- Create: `internal/pluginresource/agent_manifests.go`
- Create: `internal/pluginresource/agent_manifests_test.go`
- Modify: `internal/pluginresource/manager.go`
- Modify: `internal/pluginresource/manager_test.go`
- Modify: `internal/pluginresource/marketplace.go`
- Modify: `internal/pluginresource/codex_marketplace.go`
- Modify: `internal/pluginresource/claude_marketplace.go`
- Modify: `internal/pluginresource/marketplace_test.go`
- Modify: `internal/pluginresource/codex_marketplace_test.go`
- Modify: `internal/pluginresource/claude_marketplace_test.go`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/apc-plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/skills/demo-plugin/skill-card.md`
- Verify unchanged: `internal/cloud/resources/plugins/demo-plugin/v0.0.1/skills/demo-plugin/SKILL.md`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/apc-plugin.json`
- Create: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/skills/demo-plugin/skill-card.md`
- Verify unchanged: `internal/cloud/resources/plugins/demo-plugin/v0.1.0/skills/demo-plugin/SKILL.md`
- Modify: `internal/cloud/local/plugin_test.go`
- Modify: `internal/lifecycle/plugin_integration_test.go`
- Modify: `internal/lifecycle/plugin_acceptance_test.go`
- Modify: `internal/transaction/plugin_recovery_test.go`
- Modify: `internal/transaction/plugin_marketplace_recovery_test.go`

**Interfaces:**
- Consumes: provider canonical Plugin root and `jsonstrict.Decode`.
- Produces: `pluginartifact.APCPluginManifest`, shared canonical decoder/validator, deterministic Agent manifest builder, new canonical fixtures.

- [ ] **Step 1: Write RED canonical schema tests**

Use this exact model:

```go
type APCPluginManifest struct {
    SchemaVersion int      `json:"schema_version"`
    Name          string   `json:"name"`
    Version       string   `json:"version"`
    Description   string   `json:"description"`
    Skills        []string `json:"skills"`
}
```

Test schema !=1, identity mismatch, empty description/skills, duplicate or unsafe skill paths, missing declared Skill, extra `skills/*` directory, missing `SKILL.md`/`skill-card.md`, unknown/duplicate JSON fields and raw package containing any generated/native path.

- [ ] **Step 2: Write RED deterministic generation tests**

Build twice in separate temp roots and byte-compare these files:

```text
.codex-plugin/plugin.json
.claude-plugin/plugin.json
plugin.yaml
__init__.py
```

Assert Codex uses `skills: "./skills/"`, Claude uses `skills: ["./skills/"]`, both use native version without `v`, the filesystem `plugin.yaml` uses `schema_version: 1` with only `schema_version/name/version/description` (no `author`, per ADR-036), and `__init__.py` contains exactly a no-op `register(ctx)` function. Per ADR-036 each Agent's derived files live under `landing/<agent-id>/plugins/<name>`, and the two marketplaces reference their own landing leaf.

- [ ] **Step 3: Run RED**

```bash
go test -count=1 ./internal/pluginartifact ./internal/pluginresource -run 'Test(APCPlugin|AgentManifest|EnsureCanonical)'
```

- [ ] **Step 4: Centralize canonical decode in pluginartifact**

Expose a single function used by builder and recovery validator:

```go
func LoadAPCPluginManifest(ref domain.PowerRef, pluginPath string) (APCPluginManifest, error)
```

It reads only a physical regular `apc-plugin.json`, calls `jsonstrict.Decode(..., true)`, validates exact ref identity and the declared Skill closed world, and returns a copy whose `Skills` slice does not alias decoder storage.

- [ ] **Step 5: Generate all Agent manifests before marketplaces**

`sharedMarketplaceBuilder.Build` must execute:

```go
manifest, err := pluginartifact.LoadAPCPluginManifest(ref, pluginPath)
if err != nil { return err }
if err := writeAgentManifests(pluginPath, manifest); err != nil { return err }
if err := builder.codex.Build(ref, root, pluginPath); err != nil { return err }
return builder.claude.Build(ref, root, pluginPath)
```

Write JSON with existing indented encoder and YAML with a typed struct/encoder, never string-substitute unescaped description. Generate into the unpublished temporary artifact only.

- [ ] **Step 6: Strengthen raw and shared validators**

Change the raw validator to `ValidateRawPackage(ref domain.PowerRef, pluginPath string) error`; it delegates canonical identity/Skill validation to `LoadAPCPluginManifest` and forbids native manifest/marketplace directories. `ValidateShared` also delegates canonical validation to the same loader, then requires all four generated files, both marketplaces, a physical tree, exact identity and semantic equivalence. `ValidateMarketplace` continues Agent-specific marketplace validation after `ValidateShared`. Update `pluginresource.Service.Ensure` to pass `ref` to the raw validator; no lifecycle or transaction layer may decode `apc-plugin.json` independently.

- [ ] **Step 7: Migrate both committed Plugin fixture versions**

For each version, delete prebuilt `.codex-plugin`, `.claude-plugin`, `.hermes-plugin`; add `apc-plugin.json`, retain `skills/demo-plugin/SKILL.md`, and add a complete `skill-card.md`. Example identity for v0.1.0:

```json
{"schema_version":1,"name":"demo-plugin","version":"v0.1.0","description":"Second shared Plugin fixture managed by APC","skills":["skills/demo-plugin"]}
```

Do not leave any native manifest under `internal/cloud/resources/plugins`.

- [ ] **Step 8: Update lifecycle/recovery fixtures and run gates**

```bash
go test -count=1 ./internal/pluginartifact ./internal/pluginresource ./internal/cloud/local ./internal/transaction ./internal/lifecycle
go test -race -count=1 ./internal/pluginresource ./internal/transaction ./internal/lifecycle
test -z "$(find internal/cloud/resources/plugins -type f \( -path '*/.codex-plugin/*' -o -path '*/.claude-plugin/*' -o -path '*/.hermes-plugin/*' \) -print)"
```

- [ ] **Step 9: Commit**

```bash
git add internal/pluginartifact internal/pluginresource internal/cloud/resources internal/cloud/local internal/transaction internal/lifecycle
git commit -m "refactor(plugin): derive agent manifests from apc index"
```

---

### Task 8: Prove Local and Remote Provider Equivalence

**Files:**
- Create: `internal/cloud/contract_test.go`
- Create: `internal/cloud/remote_test_server_test.go`
- Modify: `internal/lifecycle/plugin_integration_test.go`
- Modify: `internal/lifecycle/fix_integration_test.go`

**Interfaces:**
- Consumes: complete Local/Remote `cloud.Client`, canonical fixtures, final Plugin builder.
- Produces: shared provider contract and end-to-end evidence that provider choice does not alter lifecycle inputs.

- [ ] **Step 1: Add a shared contract harness**

In external package `cloud_test`, define:

```go
type clientFactory struct {
    name string
    open func(t *testing.T) cloud.Client
}

func TestProviderContract(t *testing.T) {
    for _, factory := range providerFactories(t) {
        t.Run(factory.name, func(t *testing.T) { runProviderContract(t, factory.open(t)) })
    }
}
```

The Remote test server must pack canonical fixture directories deterministically, calculate metadata MD5/size, serve list/latest/exact/content, and keep all sockets inside `httptest.Server`.

- [ ] **Step 2: Compare every Power outcome**

For all committed refs, compare Local and Remote List/Resolve results. Download Skill/Plugin/Agent to separate temp targets and compare normalized tree paths, modes and bytes. Compare MCP structs with `reflect.DeepEqual`. Include cancellation and exact miss classifications.

- [ ] **Step 3: Add Remote-backed lifecycle acceptance**

Reuse isolated fake Agent homes/CLI and instantiate one lifecycle harness with Remote Client. Prove at least one Skill, MCP, Plugin and Agent install reaches the same manifest/cache/target state as Local; after closing the server, prove Fix and Plugin Uninstall remain offline. Add an MCP uninstall case that expects an exact Remote `GetMCP` request only when CLI inspection reports the target present, preserving the existing recoverable-uninstall contract without adding an MCP definition cache.

- [ ] **Step 4: Run contract and integration gates**

```bash
go test -count=1 ./internal/cloud/... -run 'TestProviderContract'
go test -count=1 ./internal/lifecycle -run 'Test(Remote|PluginIntegration|Fix)'
go test -race -count=1 ./internal/cloud/... ./internal/lifecycle
```

- [ ] **Step 5: Commit**

```bash
git add internal/cloud internal/lifecycle
git commit -m "test(cloud): verify local and remote provider parity"
```

---

### Task 9: Select Providers in the Composition Root

**Files:**
- Create: `cmd/apm/cloud.go`
- Create: `cmd/apm/cloud_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`
- Modify: `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: Provider Registry, `local.New`, `remote.New`, final Client.
- Produces: `newCloudClient() (cloud.Client, error)` with production defaults and explicit test/dev overrides.

- [ ] **Step 1: Write RED configuration matrix**

Cover default Remote/base URL, Remote env override, loopback HTTP, non-loopback HTTP rejection, explicit Local absolute root, missing/relative Local root, unknown provider and no-fallback after Remote failure.

```go
func TestNewCloudClientDefaultsToRemote(t *testing.T) {
    t.Setenv("APM_CLOUD_PROVIDER", "")
    t.Setenv("APC_BASE_URL", "")
    got, err := newCloudClient()
    if err != nil { t.Fatal(err) }
    if _, ok := got.(*remote.Client); !ok { t.Fatalf("client = %T, want Remote", got) }
}
```

- [ ] **Step 2: Run RED**

```bash
go test -count=1 ./cmd/apm -run 'TestNewCloudClient'
```

- [ ] **Step 3: Implement constants and explicit registry composition**

```go
const (
    defaultCloudProvider = cloud.ProviderRemote
    defaultAPCBaseURL    = "http://127.0.0.1:8080"
    cloudProviderEnv     = "APM_CLOUD_PROVIDER"
    apcBaseURLEnv        = "APC_BASE_URL"
    localRootEnv         = "APM_CLOUD_LOCAL_ROOT"
)
```

Create a fresh registry per call. Register Local with a closure that validates `localRootEnv`; register Remote with a closure that builds `&http.Client{Timeout: 2 * time.Minute, CheckRedirect: sameOriginOnly}`. Select the environment provider or default. Never attempt the other factory after `Open` returns an error.

- [ ] **Step 4: Inject one selected Client into each lifecycle factory**

Replace `newLocalCloudClient` calls with `newCloudClient`. Install passes the same Client to Cache and Plugin Resource. Uninstall receives the selected Client because present-target MCP uninstall still performs exact `GetMCP`; Skill, Plugin and Agent uninstall do not call it. Fix must not create any Cloud provider.

- [ ] **Step 5: Make bundled-resource tests explicitly Local**

Add one helper:

```go
func useBundledLocalCloud(t *testing.T) {
    root, err := filepath.Abs(filepath.Join("..", "..", "internal", "cloud", "resources"))
    if err != nil { t.Fatal(err) }
    t.Setenv("APM_CLOUD_PROVIDER", "local")
    t.Setenv("APM_CLOUD_LOCAL_ROOT", root)
}
```

Call it only in tests that intentionally use bundled fixtures. Do not set a process-global environment in `TestMain`.

- [ ] **Step 6: Run composition, CLI and no-fallback tests**

```bash
go test -count=1 ./cmd/apm ./internal/cli ./internal/lifecycle
go test -race -count=1 ./cmd/apm ./internal/lifecycle
```

- [ ] **Step 7: Commit**

```bash
git add cmd/apm internal/cli internal/lifecycle
git commit -m "feat(cloud): select local or remote providers"
```

---

### Task 10: Align Runtime Documentation and Architecture Views

**Files:**
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md` only if an additional supersession link is needed; do not rewrite historical decision text
- Verify only: `../apc/ivory-tower/project-overview.md`
- Verify only: `../apc/ivory-tower/project-overview-apm.md`

**Interfaces:**
- Consumes: completed runtime behavior from Tasks 1–9.
- Produces: current operational instructions and architecture references to ADR-016/017.

- [ ] **Step 1: Update BUILD with exact provider commands**

Document:

```bash
# 默认 Remote（当前开发默认 APC 地址）
APC_BASE_URL=http://127.0.0.1:8080 ./apm list

# 显式 Local canonical fixture
APM_CLOUD_PROVIDER=local \
APM_CLOUD_LOCAL_ROOT="$(pwd)/internal/cloud/resources" \
./apm list
```

Replace ADR-014 as the current Plugin authority with ADR-017; retain ADR-014 only as historical context. State Agent/MCP/Skill/Plugin content semantics, that Fix and Plugin Uninstall remain offline, and that present-target MCP uninstall retains its exact Cloud definition lookup for rollback.

- [ ] **Step 2: Update Project Overview and C4**

Project Overview must show `cloud.Client -> ProviderRegistry -> local|remote`, Cache owning Skill/Agent publication, Plugin Resource owning canonical-to-derived artifact, and composition root selection. Mermaid/DSL use the exact file/package names from the final tree.

- [ ] **Step 3: Run documentation consistency scans**

```bash
rg -n 'NewLocalClient|DownloadSkill\(PowerRef\)|DownloadAgent\(PowerRef\)|完整多 Agent 原始包|ADR-014' BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'agent|application/yaml|UNSUPPORTED_RESOURCE_TYPE' ../apc/ivory-tower/project-overview.md ../apc/ivory-tower/project-overview-apm.md
git diff --check
```

Classify historical Feature/ADR matches explicitly; current overview/build/C4 must have no stale architecture matches.

- [ ] **Step 4: Commit**

```bash
git add BUILD.md ivory-tower/project-overview.md ivory-tower/c4 ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md
git commit -m "docs(cloud): document provider runtime and apc plugins"
```

---

### Task 11: Final Audit, Review, Changelog, and Completion Gates

**Files:**
- Modify after all reviews pass: `docs/Changelog.md`
- Verify: all files changed since `b2dce66`

**Interfaces:**
- Consumes: all prior tasks and the already committed design/API documents.
- Produces: reviewed branch, one final request-level Changelog entry, complete verification evidence.

- [ ] **Step 1: Run stale-boundary and real-environment safety scans**

```bash
rg -n 'NewLocalClient|cacheRoot.*Local|cacheTempRoot.*Local|DownloadSkill\([^,)]*\)|DownloadAgent\([^,)]*\)' cmd internal --glob '*.go'
find internal/cloud/resources/plugins -type f \( -path '*/.codex-plugin/*' -o -path '*/.claude-plugin/*' -o -path '*/.hermes-plugin/*' \) -print
rg -n '/home/liangff|\.local/bin/hermes|exec\.Command\("(codex|claude|hermes|bianbu-agent)' internal cmd --glob '*_test.go'
```

Expected: no current production stale Cloud API; no native manifest in raw fixtures; no test path or executable that can touch the real environment.

- [ ] **Step 2: Run full fresh gates**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
build_dir=$(mktemp -d)
go build -o "$build_dir/apm" ./cmd/apm
test -x "$build_dir/apm"
rm -rf -- "$build_dir"
git diff --check
```

Expected: all commands exit 0. The cleanup targets only the directory returned by this step's `mktemp -d`.

- [ ] **Step 3: Request whole-branch review before Changelog**

Use `superpowers:requesting-code-review` against `b2dce66..HEAD`. Review must separately judge spec coverage, provider equivalence, archive safety, cache/recovery boundaries, test isolation, interface names, documentation and maintainability. Process findings with `superpowers:receiving-code-review`; bugs use `superpowers:systematic-debugging` and TDD, then repeat Step 2 and the affected review gate.

- [ ] **Step 4: Add exactly one Changelog entry after review is clean**

Generate the actual APM commit list, including design and plan but excluding the future Changelog commit itself:

```bash
git log --reverse --format='- `%h %s`' b2dce66^..HEAD
```

Use `apply_patch` to insert one newest `2026-08-21` entry following `~/.codex/docs/Changelog.md`, with these exact field values and no placeholder text:

- `分类`：`新增功能`。
- `用户需求`：`基于 APC 统一资源接口新增可切换的 Local/Remote Cloud Client provider，补齐 Agent 只读获取，并将 Plugin Cloud 资源迁移为以 apc-plugin.json 为权威、由 APM 派生 Agent 配置的 canonical 包。`
- `相关提交`：逐行原样写入上述 `git log` 命令的全部输出，再追加 `- \`f470470 docs(agent): define agent power read interfaces\`` 与 `- \`f22f2f4 docs(apm): clarify MCP uninstall cloud dependency\``；不得漏掉设计、计划、实现或修复提交。
- `解决方案或完成结果`：明确记录静态 provider registry、默认 Remote 与环境覆盖、四类资源获取和完整性校验、Skill/Agent cache 归属、canonical Plugin 派生及离线恢复边界。
- `验证结果或限制`：写入 Step 2 的实际 full/race/vet/build/diff 结果，并明确测试只用 `httptest`/fake home、Remote 不 fallback、Agent 不含 Submission、旧 cache/API 不兼容。

- [ ] **Step 5: Commit Changelog only**

```bash
git add docs/Changelog.md
git diff --cached --check
git commit -m "docs(changelog): record cloud provider migration"
```

- [ ] **Step 6: Re-run final immutable evidence**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
final_build_dir=$(mktemp -d)
go build -o "$final_build_dir/apm" ./cmd/apm
test -x "$final_build_dir/apm"
rm -rf -- "$final_build_dir"
git diff --check
git status --short --branch
```

Expected: all gates exit 0; worktree has no tracked or untracked task residue. Report the APC repository's pre-existing untracked `diagrams/` separately and never delete or commit it.

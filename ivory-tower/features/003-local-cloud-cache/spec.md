# 开发期本地 Cloud 与版本化缓存规格

## 目标

在真实 Cloud 服务和完整 install lifecycle 尚未实现时，以 `internal/cloud/resources` 中的本地资源验证 Power 的发现、版本化缓存获取和 staging 物化边界。本 feature 实现 Cloud、Cache、共享目录复制工具及必要路径，不串联 CLI、lifecycle、Adapter 或 Transaction。

## 领域模型

`domain.PowerRef` 统一表示 Power 类别、名称和可选版本：

```go
type PowerRef struct {
	Name    string
	Type    PowerType
	Version string
}
```

- 无版本引用表示选择最新可用版本。
- Cloud `List()` 返回的引用必须带版本。
- lifecycle 在后续 feature 中负责把用户未指定的版本解析为最新版；传入 Cloud `Get` 或 Cache `Fetch` 的 `PowerRef.Version` 必须非空。
- `domain.InstalledPower` 不再单独保存版本，版本来自 `InstalledPower.Ref.Version`。
- `domain.Power` 改为空占位结构，字段由后续 Adapter feature 决定。

## 组件与接口

### Cloud

```go
type Client interface {
	List() ([]domain.PowerRef, error)
	Get(domain.PowerRef) error
}
```

`LocalClient` 是本阶段唯一 Cloud 实现。`NewLocalClient(resourcesRoot string) (*LocalClient, error)` 要求绝对资源根；空值或相对路径立即返回参数错误，但不提前检查目录是否存在或可读。构造函数通过 `internal/paths` 解析并保存缓存及缓存临时路径。

删除 `Query`、`PowerMeta`、无功能 `HTTPClient` 及其构造函数。真实 HTTP Cloud 的协议、下载和完整性校验后续设计。

### Cache

```go
type PowerCache interface {
	Fetch(domain.PowerRef) (Content, error)
}

type Content struct {
	Path string
}
```

`FilesystemCache` 是本阶段唯一 Cache 实现。`NewFilesystem() (*FilesystemCache, error)` 通过 `internal/paths` 解析并保存缓存、staging 和 staging 临时路径。

删除 `Put`、`Checksum` 和 `ContentAddressed`。缓存按 `type/name/version` 分层，不实现 checksum、内容寻址或完整性复验。

### 共享文件系统工具

新增 `internal/utils/filesystem`，提供：

```go
func CopyDir(source, destination string) error
```

该函数递归复制目录内容并保留文件与目录权限位，不保留 owner、时间戳或 xattr。它不负责原子 rename、领域错误映射或失败清理，也不主动校验 Power 名称、版本语义或特殊文件类型；本阶段默认资源目录合规。

Cloud 和 Cache 各自负责路径选择、临时目录清理、原子发布和错误上下文，不互相依赖。

## 路径

`internal/paths` 新增以下入口：

```text
CacheTempPath   = <cache-root>/.tmp
StagingPath     = <state-root>/staging
StagingTempPath = <state-root>/.tmp/staging
```

- `CacheTempPath` 与最终缓存位于同一文件系统。
- `StagingTempPath` 与最终 staging 位于同一文件系统。
- 所有入口沿用现有 `(string, error)` 路径解析方式。
- `XDG_CACHE_HOME > APM_HOME > 系统默认` 的缓存根优先级保持不变。

## Cloud List

`List()` 执行以下步骤：

1. 检查 `resourcesRoot` 存在且可读取；资源根不存在或不可读时返回带路径上下文的错误。
2. 扫描 `resourcesRoot/skills/<name>/<version>`。
3. 仅把版本目录内存在普通文件 `SKILL.md` 的条目视为可用 Skill。
4. 返回 `PowerRef{Type: PowerTypeSkill, Name: name, Version: version}`。
5. 按 type、name、version 字典序稳定排序。

资源根存在但 `skills` 目录不存在或没有有效 Skill 时返回空切片和 `nil`。List 不扫描 Plugin/MCP，不接受 Query，也不负责 keyword/type 筛选或打印；筛选属于后续 lifecycle/search feature。

## Cloud Get

`Get(ref)` 执行以下步骤：

1. `ref.Version` 为空时返回参数错误，不映射为 `ErrNotFound`。
2. 非 Skill 类型返回可通过 `errors.Is(err, cloud.ErrNotFound)` 判断的错误。
3. 清理 `cache/.tmp` 残留。
4. 若最终目录 `cache/skills/<name>/<version>` 已存在，则幂等成功，不复验内容。
5. 若 `resourcesRoot/skills/<name>/<version>` 不存在或不含普通文件 `SKILL.md`，返回 `ErrNotFound`。
6. 将完整版本目录复制到缓存临时目录。
7. 原子 rename 为 `cache/skills/<name>/<version>`。
8. 清理临时目录；成功后不留下临时内容。

资源根不可读、目录复制、删除或 rename 失败时，使用 `%w` 保留底层错误并补充操作及路径上下文。最终缓存目录只通过 rename 出现，复制失败不得发布可见的最终缓存。

## Cache Fetch

`Fetch(ref)` 执行以下步骤：

1. `ref.Version` 为空时返回参数错误，不映射为 `ErrMiss`。
2. 每次有效调用都删除旧 `StagingPath` 和 `StagingTempPath`。
3. 非 Skill 类型或 `cache/skills/<name>/<version>` 不存在时，返回可通过 `errors.Is(err, cache.ErrMiss)` 判断的错误；此时旧 staging 已清理。
4. 将版本化缓存目录复制到临时 staging 的 `skills/<name>`，在此去掉版本层级。
5. 原子 rename 临时 staging 为最终 staging。
6. 返回 `Content{Path: "<state-root>/staging/skills/<name>"}`。

Fetch 不修改缓存源。复制或 rename 失败时不保留可消费的最终 staging；下一次 Fetch 会先清理残留临时目录和旧 staging。

## 后续安装数据流

本 feature 不实现 lifecycle，但接口必须支持后续固定编排：

```text
Fetch(ref)
  ├─ 命中：返回无版本层 staging 内容
  └─ ErrMiss：
       Get(ref)
         → resources/skills/<name>/<version>
         → cache/.tmp/...
         → cache/skills/<name>/<version>
       → 再次 Fetch(ref)
```

用户未指定版本时，后续 lifecycle 先通过 Cloud `List()` 获取候选引用并选择最新版，确保进入上述流程的 `PowerRef.Version` 非空。

## 错误契约

- `cloud.ErrNotFound` 只表示精确 Skill/版本不在开发期资源中，或类型不是当前支持的 Skill。
- `cache.ErrMiss` 只表示精确缓存项不存在或类型不是当前支持的 Skill。
- 空版本和相对资源根是普通参数错误。
- 文件系统错误保留底层错误，便于调用方使用 `errors.Is` 判断 `os` 错误。
- Get 已存在目标时幂等成功；不做 checksum 或内容复验。

## 测试与验收

### 单元测试

- Domain：`PowerRef.Version` 可保存；`Power` 是空占位；`InstalledPower` 仅通过 `Ref.Version` 表示版本。
- Paths：新增三个路径符合约定；缓存与状态根分离时，两个临时目录分别留在对应文件系统。
- Filesystem utility：递归复制文件和子目录，内容及权限位一致。
- Cloud：绝对路径构造、资源列表、无 `SKILL.md` 忽略、稳定排序、空版本、非 Skill、不存在版本、完整复制、版本目录保留、临时目录清理和幂等 Get。
- Cache：空版本、miss、非 Skill、旧 staging 清理、版本层移除、返回路径、缓存源不变和临时目录清理。

修改进程环境的测试不得使用 `t.Parallel()`。

### 集成验收

使用 `internal/cloud/resources/skills/foo-before-summary/0.0.1` 和测试隔离目录验证：

```text
Fetch(skill/foo-before-summary@0.0.1) → ErrMiss
Get(skill/foo-before-summary@0.0.1)   → cache/skills/foo-before-summary/0.0.1
Fetch(skill/foo-before-summary@0.0.1) → staging/skills/foo-before-summary
```

最终缓存与 staging 均包含 `SKILL.md` 和 `skill-card.md`；版本目录只保留在缓存，staging 不含版本层；所有写入位于测试设置的 `APM_HOME`/`XDG_CACHE_HOME` 内。

### 最终验证

```bash
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 明确不实现

- CLI 或 lifecycle 串联、logging 输出和 search 筛选。
- 最新版或 SemVer 选择逻辑。
- Adapter 安装、配置写入、journal 或 Transaction。
- Plugin/MCP 资源与目录映射。
- 真实 HTTP Cloud、checksum、内容寻址和资源完整性验证。
- Power 名称、版本语义或复制节点类型校验。

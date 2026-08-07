# Agent Power 安装清单与无状态 Config Repository 规格

## 目标

APM 为每个 Agent 保存一份已安装 Power 清单。本 feature 确定清单的领域模型、JSON 格式、默认路径和持久化接口，并以无状态 Repository 封装完整的读取、查询、变更和原子写入流程。

lifecycle 后续只调用 `List`、`Find`、`Set` 和 `Remove`，不管理 JSONStore 的加载、写入或释放状态。本 feature 不把 Repository 串联到 lifecycle 或 CLI。

## 领域语言与模型

术语以根目录 [CONTEXT.md](../../../CONTEXT.md) 为准：

- **Agent Power 安装清单**是单个 Agent 已安装的、版本均已确定的 Power 引用集合。
- **Power 引用**统一使用 `domain.PowerRef`，不再定义 `InstalledPower`，也不记录 `InstalledAt`。
- **Power 分组**按 `PowerType` 组织 Power 引用，供 config、lifecycle 结果和后续输出层共享。

`internal/domain` 新增：

```go
type PowerGroups map[PowerType][]PowerRef
```

`PowerGroups` 的有效 key 为 `skill`、`plugin` 和 `mcp`。Repository 返回的分组始终包含这三个 key，且对应切片非 nil。

删除：

```go
type InstalledPower struct {
    Ref         PowerRef
    InstalledAt time.Time
}
```

引用旧类型的占位结果改用 `PowerGroups`，但不实现对应 lifecycle 行为。

## Repository 接口与职责

`internal/config` 暴露：

```go
type Repository interface {
    List(domain.AgentID) (domain.PowerGroups, error)
    Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error)
    Set(domain.AgentID, domain.PowerRef) error
    Remove(domain.AgentID, domain.PowerType, string) error
}
```

唯一实现为 `JSONRepository`。它不保存当前 Agent、清单、dirty flag 或其他跨调用状态，不提供 `Read`、`Write`、`Release`，也不定义加载状态错误。内部未导出的 `read`/`write` 和 JSON DTO 只用于完成单次 Repository 调用。

Repository 不获取或检查全局锁，也不保证并发写安全。调用本地清单的 lifecycle 流程必须已按 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 持有全局 APM 锁。config 不依赖 `internal/lock`。

config 实施调用方给出的目标清单状态，但不决定首次安装、相同版本跳过、升级、降级、卸载、跨类型消歧、筛选或用户输出。这些仍属于 lifecycle。

## JSON 格式

每个 Agent 一份 JSON：

```json
{
  "agent": "codex",
  "skills": [
    {
      "name": "foo-before-summary",
      "version": "0.0.1"
    }
  ],
  "plugins": [],
  "mcps": []
}
```

规则：

- 顶层输出字段顺序固定为 `agent`、`skills`、`plugins`、`mcps`。
- 三个分组始终输出为数组；空分组写为 `[]`，不省略也不写为 `null`。
- 组内条目只写 `name` 和 `version`，不重复写 `type`。
- 读取后根据所在分组补全 `PowerRef.Type`；写入时根据 `PowerRef.Type` 选择分组。
- 输出使用两空格缩进并以一个换行结束。
- 读取时，缺失分组或 `null` 按空数组处理；未知字段由标准 `encoding/json` 行为忽略。
- 非法 JSON、字段类型错误或尾随非空白数据返回解析错误。
- `agent` 必须存在、非空并与 Repository 方法传入的 Agent 相同；否则读取失败。
- 读取不检查已有条目的空名称、空版本、版本语义或重复项。
- 不增加 schema version、迁移协议、checksum 或完整性签名。

## 默认路径与权限

`internal/paths` 提供：

```go
func ManifestPath(domain.AgentID) (string, error)
```

路径为：

```text
<state-root>/manifests/<agent>.json
```

正常环境示例为 `$HOME/.apm/manifests/codex.json`；测试设置 `APM_HOME` 后跟随测试状态根。Agent ID 的合法性由 Adapter Registry/lifecycle 保证，`ManifestPath` 不额外定义字符规则。

只读操作不创建目录。首次有效写入创建父目录，目录权限为 `0700`；清单目标和临时文件权限为 `0600`，替换已有文件后仍统一为 `0600`。

## 操作语义

### List

```text
List(agent) → read → 返回 PowerGroups 深拷贝
```

- 文件不存在时返回含三个空分组的结果，不创建目录。
- 返回 map 和所有切片的副本；调用方修改结果不影响 Repository 或其他调用。
- 不筛选、不排序，保留磁盘数组的原有顺序。

### Find

```text
Find(agent, type, name) → 校验 → read → 查找指定分组
```

- 类型必须是三个已知 `PowerType` 之一，名称必须非空。
- 返回第一个同名 `PowerRef`。
- 未命中返回零值、`false`、`nil`，不定义 `ErrNotFound`。
- 若原文件有重复项，Find 不清洗或报错。

### Set

```text
Set(agent, ref) → 校验 → read → 计算目标分组 → 必要时原子 write
```

- `ref.Type` 必须为已知类型，`Name` 和 `Version` 必须非空。
- 不验证 semver，也不限制名称字符集。
- 同类型同名项不存在时追加到该分组末尾。
- 存在时用新引用替换第一个匹配项并删除其余重复项；其他条目的相对顺序不变。
- 仅有一条匹配项且版本完全相同时幂等成功，不写文件。
- 输入校验失败不得修改文件。

### Remove

```text
Remove(agent, type, name) → 校验 → read → 删除匹配项 → 必要时原子 write
```

- 类型必须已知，名称必须非空；不接受版本参数。
- 删除目标分组内全部同名项，其他条目的相对顺序不变。
- 文件或匹配项不存在时幂等成功，不创建或重写文件。
- 是否允许卸载由 lifecycle 在调用前决定。

## 内部读取与原子写入

`JSONRepository` 内部直接实现文件读写，不保留导出的 `AtomicWrite`，也不创建独立 `atomic_write.go`。

内部读取：

1. 通过 `paths.ManifestPath` 解析目标。
2. 文件不存在时构造指定 Agent 的空三分组清单。
3. 读取并解码 JSON DTO。
4. 检查 Agent 一致性。
5. 把三组条目转换为补全类型的 `PowerGroups`。

内部写入：

1. 将 `PowerGroups` 映射为固定 JSON DTO 并格式化编码。
2. 以 `0700` 创建清单父目录。
3. 在目标同目录创建 `0600` 临时文件。
4. 写入完整内容，执行文件 `Sync` 并关闭。
5. 以 `Rename` 原子替换目标。
6. 同步父目录，确保 rename 持久化。
7. 清理任何尚存的临时文件。

任一步失败都返回带操作、Agent 和路径上下文的错误，不重试。rename 前失败时旧目标保持不变；rename 或目录同步后返回错误时，调用方不得假设旧内容仍在，后续恢复以磁盘实际状态为准。

## 错误与日志

config 仅定义：

```go
var ErrInvalidPower = errors.New("invalid power reference")
```

`Find`/`Remove` 的未知类型或空名称，以及 `Set` 的未知类型、空名称或空版本，包装该错误并指出字段。文件不存在不是错误；Find 未命中也不是错误。

路径、打开、读取、解析、创建目录、写入、同步、关闭和 rename 错误保留底层错误，支持 `errors.Is/As`，同时增加方法、Agent 和路径上下文。

config 不记录 Info、Warn 或 Error，避免 lifecycle 重复输出；只在读取、幂等跳过和成功写入等内部步骤调用包级 `slog.Debug`。不得直接调用 `fmt.Print*`、`log.Print*`，也不得创建私有 logger。`fmt.Errorf` 仅用于构造上下文错误。

## 测试与验收

### 路径与只读行为

- HOME 和 `APM_HOME` 下的清单路径正确。
- 文件不存在时 List 返回完整空分组，Find 正常未命中，且两者不创建状态目录。

### JSON 读取

- 三个分组补全正确的 `PowerRef.Type`。
- 缺失或 `null` 分组归一为空，未知字段忽略。
- 非法 JSON、字段类型错误、Agent 缺失/空值/不匹配返回错误。
- 空名称、空版本和重复项可以读取，证明 read 不做内容审计。

### 查询和变更

- List 返回深拷贝并保留条目顺序。
- Find 返回首个匹配或正常未命中，并校验输入。
- Set 覆盖新增、版本替换、重复归并、顺序保持、输入校验和同版本无写入。
- Remove 覆盖删除全部重复项、顺序保持、输入校验和缺失时无写入。
- 同一个 Repository 实例可顺序操作多个 Agent，调用间没有残留状态。

### 文件安全

- 输出字段、缩进、换行、空数组和 round trip 稳定。
- 目录为 `0700`，目标和临时文件为 `0600`。
- 成功后没有残留临时文件。
- 通过临时目录结构构造失败场景，验证不重试、临时文件清理，以及 rename 前失败不破坏旧目标；不为测试增加生产文件系统抽象。

### 日志与回归

- verbose 下可以观察 config Debug，默认不出现；不存在直接终端打印。
- 删除 `InstalledPower` 后所有占位类型和测试可编译。
- CLI 仅解析命令的现有行为不变。

最终验证：

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 明确不实现

- lifecycle、transaction、Adapter 或 CLI 的 config 串联。
- 安装、升级、降级、卸载、筛选或打印的业务流程。
- Agent ID 字符验证、Power 名称字符验证或 semver 校验。
- schema version、格式迁移、未知字段保留、checksum、签名、加密或审计记录。
- Repository 内部锁、并发写支持、批量事务或失败重试。
- 独立 `AtomicWrite` API 或通用文件持久化工具。

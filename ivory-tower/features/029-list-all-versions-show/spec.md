# Feature 029：List 全版本查询与 Show 详情

- Status：Proposed
- Date：2026-09-04
- Related ADRs：拟新增 [ADR-028](../../adr/ADR-028-list-all-versions-and-show.md)
- Related Features：[Feature 009](../009-lifecycle-list/spec.md)、[Feature 027](../027-installed-power-metadata/spec.md)

## 1. Goal

调整 `apm list` 的 `-a/--all` 语义，使其在指定 Power 名称时能够查看该名称在 Cloud
目录中的全部可安装版本；同时新增 `apm show [name]`，用于查看某个 Cloud Power 的
单个版本详情，包括描述和标签。默认行为继续面向“当前可安装版本”的快速发现，历史版本
与详细元数据分别通过 `list --all` 和 `show` 按需获取。

## 2. Scope

### 2.1 Included

- `list [name] --all`（`-a` 为短选项）参数与 Cloud 生命周期语义。
- `list --all` 缺少 `[name]` 时的 CLI 校验与错误提示。
- Cloud 列表按名称精确筛选后展开全部版本，并按既定类型、名称、SemVer 降序排序。
- 新增 `show [name]` 命令及 `--type`、`--version`、`--json` 选项。
- `show` 默认选择指定名称的最新版本，显式 `--version` 时选择精确版本。
- `show` 的文本与 JSON 单对象输出，至少包含 type、name、version、description、tags。
- Cloud 目录类型歧义、名称/版本不存在、版本格式非法等错误语义。
- CLI、lifecycle、渲染层及根命令 factory 接线的自动化测试。
- BUILD、Project Overview、ADR 与 `docs/Changelog.md` 的行为契约同步。

### 2.2 Excluded

- 不改变 `list` 不带 `--all` 时每个 `(type,name)` 只显示最高 SemVer 版本的语义。
- 不改变 `list --installed` 的本地清单来源、Agent 分组、名称/类型过滤或精确版本记录。
- 不再使用 `list -a/--all` 控制 description/tags 的附加渲染；详情统一由 `show` 提供。
- 不新增 Cloud 分页、远程逐项 metadata 请求或本地缓存之外的数据源。
- 不支持 `show --installed`、`show --agent` 或其它 Agent 本地状态查询。
- 不在 `show` 中展示 content_type、content_size、content_md5 等传输校验字段。

## 3. CLI Contract

### 3.1 List

```text
apm list [name] [--type=<skill|plugin|mcp|agent>] [--all]
apm list [name] --installed [--agent=<agent-id|all>] [--type=<skill|plugin|mcp|agent>]
```

- `name` 仍按区分大小写的原始字符串精确匹配 `PowerRef.Name`，并在进入 lifecycle 前
  去除首尾空白；空名称拒绝。
- Cloud `list` 默认行为不变：省略 `--all` 时每个 `(type,name)` 仅输出最高 SemVer。
- Cloud `list <name> --all` 在名称过滤和可选 `--type` 过滤后输出该名称的全部目录版本，
  不做 latest 去重；输出排序为 type 升序、name 升序、version 按 SemVer 降序。
- `list --all` 或 `list -a` 缺少位置参数 `name` 时在创建 lifecycle 前失败；错误应明确
  表达 `--all` 需要 `name`。
- `--all` 不再打开 description/tags 的文本或 JSON 元数据字段。List 的身份行与既有
  格式保持一致；`--json` 仍输出数组，记录只包含 agent（仅 installed）、type、name、version。
- `list [name] --installed --all` 允许通过统一的 name 校验，并继续返回本地清单中该名称的
  精确记录；installed 清单本来就保留本地记录的具体版本，`--all` 不改变其结果，也不追加
  元数据。省略 name 时同样拒绝 `--all`。
- `--all` 与 `--json` 正交；`--all` 只影响 Cloud 版本集合，`--json` 只影响输出格式。

### 3.2 Show

```text
apm show <name> [--type=<skill|plugin|mcp|agent>] [--version=<vMAJOR.MINOR.PATCH>] [--json]
```

- `name` 必填且按现有 canonical Power 名称规则校验；空白名称拒绝。
- `--type` 省略时按名称在目录中的唯一类型推断；同名跨多个 Power 类型时返回歧义错误，
  要求显式 `--type`。指定类型后只在该类型中查询。
- `--version` 省略时使用该名称的最新 SemVer；提供时只接受规范的 `vMAJOR.MINOR.PATCH`
  并查询精确版本。版本不存在返回 not-found 错误，不回退到其它版本。
- `show` 只查询 Cloud catalog snapshot；不访问 installed Agent 清单，不接受 `--agent`。
- 文本输出为单个对象的稳定字段顺序：

  ```text
  type: skill
  name: demo
  version: v1.2.3
  description: A demo power
  tags: alpha, beta
  ```

  `tags` 为空时输出 `tags:` 后跟空值（不输出 `null`）；描述为空不改变字段位置，目录
  校验保证正常 Cloud 条目描述非空。
- `--json` 输出单个 JSON 对象而非数组，字段为 `type`、`name`、`version`、`description`、
  `tags`；`tags` 始终为数组。JSON 编码失败按 CLI 既有渲染错误语义返回。

## 4. Architecture and Data Flow

### 4.1 List

`ListService.List` 在既有 journal inspect 与单次 `CatalogManager.Snapshot` 后，根据
`flags.Name`、`flags.Type` 和 `flags.All` 选择目录条目：

```text
Snapshot.List(type)
  → exact name/type filter
  → flags.All ? retain every version : keep latest per (type,name)
  → sort type/name/version
  → ListResult → identity-only renderer
```

版本比较继续使用 Masterminds SemVer；目录快照本身保持不可变，List 不发起额外网络请求。
Installed 分支继续读取本地 Repository，`flags.All` 不改变其数据来源。

### 4.2 Show

新增独立 `ShowLifecycle` 端口与 `ShowService`，复用 `CatalogManager` 的 snapshot 边界：

```text
Snapshot.List(type)
  → exact name/type candidate resolution
  → optional exact version / latest selection
  → CatalogEntry metadata
  → ShowResult → text object or JSON object
```

不通过 `ListLifecycle` 复用命令语义，避免 Show 的单对象、版本精确选择与 List 的集合
渲染耦合。`ShowService` 与 Search/List 一样只读检查 journal、告警 stale snapshot，
不执行恢复或修改本地状态。

## 5. Error Handling

- `list --all` 缺少 name：CLI 参数错误，factory/lifecycle 不应被调用。
- `show` 参数数量错误、空 name、非法 type 或非法 version：CLI 参数错误，调用 lifecycle 前返回。
- Show 名称不存在或精确版本不存在：包装 catalog `ErrNotFound`，错误中包含 type/name/version
  上下文（若有）。
- Show 名称跨类型且未指定 type：包装 catalog `ErrAmbiguous`，提示使用 `--type`。
- Snapshot 同步失败沿既有 Cloud List/Search 语义返回；已有有效 stale snapshot 时只写 Warn，
  不改变业务结果。
- 文本/JSON 写入失败保留已写前缀并沿既有 CLI 错误包装；Show 不输出部分数组结果。

## 6. Compatibility and Documentation

- 默认 `apm list`、`apm search`、`apm list --installed` 的非 `--all` 结果保持兼容。
- `-a/--all` 的既有“追加 description/tags”行为被明确取代；调用方若需要元数据应改用
  `apm show <name>`，若需要指定历史版本则使用 `--version`。
- 更新 ADR-002 中 list 语法与 latest 语义的相关条款，并新增 ADR-028 记录“名称限定的全版本
  展开 + 独立 Show 详情”决策。
- BUILD 与 Project Overview 增加 `list <name> --all`、`show <name>` 示例，并移除 list
  `--all` 作为元数据开关的描述。
- 行为实现完成后以一次用户请求更新 `docs/Changelog.md`，列出所有相关提交与验证结果。

## 7. Testing and Acceptance Criteria

### 7.1 Tests

- CLI：`list --all`/`-a` 无 name 被拒绝；带 name 正确传递 flags；Show 参数、type/version
  校验、`--agent` 未知 flag、factory 调用顺序与错误输出。
- Lifecycle List：无 `--all` keep-latest；带 name + `--all` 保留全部版本；type/name 过滤、
  SemVer 降序、重复目录条目与空结果。
- Lifecycle Show：最新版本默认选择、精确版本选择、跨类型歧义、类型过滤、not-found、
  stale snapshot 与 journal inspect 语义。
- Rendering：List identity-only text/JSON；Show 单对象 text/JSON、tags 空数组、写入错误包装。
- Integration：bundled/local catalog 与 Remote fake catalog 覆盖新命令接线，既有 list/search/
  installed 行为不回归。

### 7.2 Acceptance Criteria

1. `apm list <name> --all` 输出该名称全部可安装版本，默认 `apm list <name>` 仍只输出最新版。
2. `apm list --all` 与 `apm list -a` 在缺少 name 时均在 lifecycle 前失败。
3. `apm show <name>` 默认展示最新版的 type/name/version/description/tags；`--version` 展示
   指定版本；`--json` 输出单对象。
4. 同名跨类型、未知名称、未知版本和非法版本均返回可诊断错误，不访问 installed 状态或回退其它版本。
5. 全量测试、race、vet、构建与 `git diff --check` 通过，文档与 ADR 引用新契约。


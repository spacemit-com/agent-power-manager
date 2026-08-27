# Feature 027：Installed Power 元数据持久化与 list/search 展示

- Status：Accepted
- Date：2026-08-27
- Related ADRs：[ADR-024](../../adr/ADR-024-persist-installed-power-description-and-tags.md)

## 1. Goal

把安装时捕获的 `description` 与 `tags` 作为已安装 Power 的一等元数据，在 journal 与 manifest
两处持久化，使离线 recovery 与 `apm list --installed` 不再丢失这些字段；同时让
`apm list` / `apm search` 通过 `-a/--all` 追加 description/tags、通过 `--json` 输出 JSON。
默认文本输出与身份（`PowerRef`）语义保持不变。

## 2. Scope

### 2.1 Included

- domain：新增 `InstalledPower{Ref, Description, Tags}`（含 `Clone`）与
  `InstalledPowerGroups`；`PowerRef` 结构不变。
- config：manifest `powerEntry` 追加 `description`/`tags`（`omitempty`），并补齐读写转换、
  `normalizeTags` 归一化与深拷贝。
- transaction：`Journal` 追加 `description`/`tags`（`omitempty`）；四类 install 事务与三类
  recovery 从 `entry`/`journal` 透传元数据。
- lifecycle：把 `CatalogEntry`（list 云 / search）与 `InstalledPower`（list `--installed`）的
  description/tags 透传进 CLI 结果。
- CLI：`list`/`search` 新增 `-a/--all` 与 `--json`；文本 `-a` 缩进多行，`--json` 输出顶层数组。
- 兼容：旧清单/旧 journal 无损读取，空元数据归一化为非 nil 空切片。

### 2.2 Excluded

- 不改变 `list`/`search` 默认输出（未给 `-a`/`--json` 时逐行 `type/name@version`）。
- 不按 description/tags 筛选、排序或搜索（延续 ADR-021「tags 仅 Catalog Entry metadata」）。
- 不暴露 Catalog 的其它字段（message/content type/size/MD5/published_at）。
- 不升级 journal/manifest schema version（见 ADR-024）。
- 不改 install/uninstall 的身份、幂等、恢复与 conflict 语义。

## 3. Data Model

### 3.1 InstalledPower

```go
type InstalledPower struct {
    Ref         PowerRef // 唯一身份
    Description string
    Tags        []string
}
```

`Ref` 仍是唯一身份，不参与 `==`、map key、排序与缓存键。`Tags` 是 slice，因此
`InstalledPower` 不可直接 `==` 比较；`Clone` 返回不共享 `Tags` 底层数组的副本。清单按
`InstalledPowerGroups map[PowerType][]InstalledPower` 组织。

### 3.2 Manifest JSON shape

```json
{
  "agent": "codex",
  "skills": [
    {
      "name": "demo",
      "version": "v1.0.0",
      "description": "Use when ...",
      "tags": ["alpha", "beta"]
    }
  ]
}
```

`description` 与 `tags` 均为 `omitempty`：空值时省略写出，以保持旧清单的精确 JSON 形状与
round-trip 不变。写出顺序为 `name`、`version`、`description`、`tags`。

### 3.3 Journal fields

`Journal` 在原有字段之外追加 `description` 与 `tags`（`omitempty`）；`Power` 仍为
`domain.PowerRef`（纯身份），只有新版本的 description/tags 被 journal。恢复通过
`journal.installedPower()` 重建完整记录。

### 3.4 Normalization

`normalizeTags` 把 nil 归一化为 `[]string{}` 并复制；读取与写出两侧都调用，保证清单写出时
tags 恒为 `[]` 而非 `null`，且不与调用方共享底层数组。description 不做归一化（空串即空值）。

## 4. Persistence

- install：`entry.Description` / `entry.Tags` 经 request 进入 journal，提交时写入 manifest；
- recovery：filesystem / MCP / plugin 三类恢复从 `journal.installedPower()` 重建并写回清单；
- read-modify-write：`Set` / `Remove` 以 `InstalledPower` 为单位操作，空元数据在往返中保留。

## 5. CLI Output

### 5.1 Text

默认逐行输出 `[agent/]type/name@version`；`agent/` 前缀仅 installed 记录存在。加 `-a/--all` 后
在身份行下追加两空格缩进：

```text
skill/demo@v1.0.0
  description: Use when ...
  tags: alpha, beta
```

description 为空或 tags 为空时不输出对应行（仅剩身份行）。

### 5.2 JSON

`--json` 输出顶层数组，每条为：

```json
[
  {
    "agent": "codex",
    "type": "skill",
    "name": "demo",
    "version": "v1.0.0",
    "description": "Use when ...",
    "tags": ["alpha", "beta"]
  }
]
```

- `agent` 为 `omitempty`，仅 installed 记录出现；
- `description` / `tags` 仅在 `--all` 时填充（`--json` 不带 `-a` 只输出 agent/type/name/version）；
- `tags` 为空时省略。

`-a` 与 `--json` 正交：`-a` 控制是否携带元数据，`--json` 只切换格式，二者不互相隐含。

## 6. Data Flow

```text
list（云）:      Snapshot → CatalogEntry → ListedPower(description, tags) → render
list --installed: Repository.List → InstalledPowerGroups → ListedPower(description, tags) → render
search:          Snapshot → catalogPowers → filterSearchPowers（keep-latest 保留 description/tags）→ render
```

`filterSearchPowers` 按 `(type, name)` 去重保留最新版本时，同步携带最新版本 entry 的
description/tags。渲染由 `internal/cli` 的 `renderPowers` 统一处理，list 与 search 共用。

## 7. Error Handling

- 文本写入失败按 `write power result` / `write power description` / `write power tags` 包装；
- JSON 编码失败按 `encode powers JSON` 包装；
- 渲染错误沿既有 CLI 错误语义返回（渲染失败记录一次 Error 并打印 usage）。

## 8. Testing

- domain/config：manifest 携带 description/tags 的 round-trip；旧清单无字段读取为 `""`/`[]`。
- transaction：install 把 description/tags 持久化进 manifest 与 journal。
- lifecycle：`makeCatalogListResult` / `makeInstalledListResult` 透传元数据；
  `filterSearchPowers` keep-latest 保留最新版本 description/tags。
- cli：文本 `-a` 缩进、`--json` 身份、`--json --all` 全量；`-a`/`--json` flag 绑定与渲染；
  写失败保留已完成前缀并包装底层错误。

## 9. Compatibility

旧 manifest 与旧 journal 缺少 `description`/`tags` 时解码为空描述与 nil tags，读取端归一化为
非 nil 空切片；身份、幂等、恢复与冲突语义不变，无需迁移旧数据（详见 ADR-024）。

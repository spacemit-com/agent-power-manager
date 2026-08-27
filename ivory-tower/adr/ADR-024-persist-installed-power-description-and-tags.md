# Persist Installed Power Description and Tags

- Status：Accepted
- Date：2026-08-27
- Decision Makers：APM maintainers
- Related Feature：[Feature 027](../features/027-installed-power-metadata/spec.md)

## Context

APM 在 install 时已经从 `CatalogEntry` 取得 `description` 与 `tags`，但清单（manifest）此前只
持久化 `name` 与 `version`，安装提交后即丢弃这些发布元数据。与此同时：

- `apm list --installed` 是离线读取，只依赖 manifest 与 Agent 快照，不能回查 Catalog/Cloud；
- journal 是写事务的提交点真相，recovery 也完全离线，无法在恢复后重建元数据；
- 需要在 journal 与 manifest 两处落地 description/tags，才能让 list 与 recovery 一致可见。

需要决策的是：元数据的落地位置、schema 演进策略，以及读取时的校验程度。

## Decision

APM 在 journal 的 `Journal` 与 manifest 的 `powerEntry` 中以可省略字段追加 `description`
（`json:"description,omitempty"`）与 `tags`（`json:"tags,omitempty"`），并引入
`domain.InstalledPower{Ref, Description, Tags}` 承载清单中一条已安装 Power 的完整记录。

- `PowerRef` 仍为唯一身份，不参与 `==`、map key、排序与缓存键；description/tags 是纯元数据。
- 不升级 journal 与 manifest 的 schema version：追加可选字段向后兼容，旧数据解码为 `""`/nil，
  读取端经 `normalizeTags` 归一化为非 nil 空切片。
- `entry.Tags` 原样复制（Catalog 已保证排序与唯一），读取端不重新校验；仅在写出与读取时做
  nil→`[]` 归一化与 Clone 深拷贝，避免与调用方共享底层数组。

本决策只决定元数据的持久化与身份边界，不改变 install/uninstall 的身份、幂等与恢复语义。

## Consequences

- 正面影响：离线 recovery 与 `list --installed` 不再丢 description/tags，元数据在 journal 与
  manifest 间保持一致。
- 正面影响：旧清单与旧 journal 无损读取，空元数据归一化为 `[]`，Set/Remove 读改写时保留。
- 代价与风险：`InstalledPower` 含 slice 不再可直接 `==` 比较，需 `slices.Equal` 或
  `reflect.DeepEqual`；相关比较与缓存键需显式改用 `Ref`。
- 代价与风险：description/tags 不参与身份或幂等判断；若未来要按元数据筛选、排序或搜索，需
  另行演进 schema 与查询路径。
- 后续动作：实现 Feature 027（含 list/search 展示），更新相关文档并在行为验证后写入 Changelog。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 只写 manifest 不写 journal | recovery 离线无法回查，元数据在恢复后丢失。 |
| 升级 journal/manifest schema version | 旧数据需迁移或拒绝，破坏向后兼容；本次仅为追加可选字段。 |
| 读取时重新校验 tags 排序/唯一 | Catalog 已保证，重复校验只增加失败面、无收益。 |
| 以独立文件或表存元数据 | 引入第二个真相源，需额外原子性与一致性维护。 |

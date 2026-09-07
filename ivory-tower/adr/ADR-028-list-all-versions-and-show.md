# Architecture Decision Record

- Status：Accepted
- Date：2026-09-04
- Decision Makers：APM maintainers

## Context

默认 Cloud `list` 仅展示每个 `(type,name)` 的最新版本，但用户仍需要在指定 Power
时检查历史版本及其元数据。此前 `-a/--all` 曾控制描述与标签展示，无法表达版本展开，
也容易让未提供名称的查询产生不可控的大输出。另需一个稳定的详情入口，查看单个版本
的描述、标签等信息。

## Decision

- `apm list [name] --all`（或 `-a`）仅在提供 `name` 时有效，并保留该名称匹配的全部
  Cloud 版本，按 SemVer 降序输出；未提供 name 时在创建 lifecycle 前拒绝并提示
  `--all requires name`。
- `--all` 不再控制描述/标签渲染；list 的文本输出始终是身份记录，installed
  list 的离线语义保持不变。（list 的 JSON 身份记录条款已被
  [ADR-029](ADR-029-list-json-carries-description-and-tags.md) 取代：`list --json`
  恒带 description/tags，文本仍身份精简。）
- 新增 `apm show <name> [--type=<type>] [--version=vMAJOR.MINOR.PATCH] [--json]`。
  默认选择最新版本，指定 `--version` 时选择精确版本；跨类型同名必须提供 `--type`。
  文本输出固定包含 type、name、version、description、tags，JSON 输出单个对象且 tags
  始终为数组。Show 只读取 Cloud catalog，不访问 installed 状态，也不自动 fallback。

该决策落实 Feature 029（`ivory-tower/features/029-list-all-versions-show/spec.md`），
并补充/取代 ADR-027 中“未来另行引入全部版本入口”的预留描述；ADR-002 的 Agent/Type
flag 总体设计仍然有效。

## Consequences

- 默认 list 输出保持精简，历史版本可通过明确的名称查询获得。
- Show 为脚本提供单对象 JSON 契约，详情字段与列表身份字段解耦。
- `list --all` 缺少 name 会产生参数错误；调用方需要在批量浏览时改用默认 list。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 恢复默认 list 平铺全部版本 | 输出噪声大，破坏 keep-latest 语义。 |
| 新增 `--all-versions` | 与既有 `-a/--all` 兼容性差，增加重复 flag。 |
| 仅扩展 list 元数据输出 | 无法提供单版本详情和稳定 JSON 单对象接口。 |

# Architecture Decision Record

- Status：Accepted
- Date：2026-09-07
- Decision Makers：APM maintainers

## Context

前端/集成方通过调用 `apm` 获取本地（`list --installed`）与云端（`list`）资源，需要在批量结果里
拿到 `description` 与 `tags` 用于展示。ADR-028（Feature 029）为让 list 精简，决定 list 的文本与
JSON 恒为身份记录、详情统一走 `show`；但 `show` 是 Cloud-only、单 name、latest/单版本，覆盖不了
本地清单与批量场景，导致前端要么 N+1 逐条查询、要么拿不到元数据。`ListedPower` 与 lifecycle 已经
端到端携带 description/tags，缺的只是 list 的 JSON 渲染没有输出它们。

## Decision

`apm list --json` 的每条记录恒带 `description`（空为 `""`）与 `tags`（空为 `[]`、不为 `null`），
与 `show --json` 的单条形状一致；Cloud 记录含 `type/name/version/description/tags`，installed
记录多 `agent`，`--agent=all` 分组输出的 `{agent,state}` 状态记录不变、listed 记录同样带元数据。
list 文本仍身份精简；`search --json`（description-only、无 tags）与 `show` 不变。实现使用 list
专用 JSON 记录类型与 search 的 `powerJSON` 解耦，避免破坏 search「永远不输出 tags」的契约。

该决策取代 ADR-028 中「list 的 JSON 输出始终是身份记录」的条款；ADR-028 的文本精简与 `show`
详情决策仍然有效。

## Consequences

- 正面影响：前端可一次 `list --json` 批量拿到 description/tags，覆盖云端与本地，无需 N+1 `show`。
- 正面影响：list JSON 与 show JSON 的单条字段形状一致，前端可复用解析；tags 恒为数组、description
  恒在，读取稳定。
- 代价与风险：list JSON 形状向后新增字段，严格断言「JSON 只含身份」的调用方需更新；文本与
  search/show 不受影响。
- 后续动作：如前端仍需本地单条详情或其它粒度，可再评估 `show --installed` 等扩展（不在本次范围）。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 前端改用 `show` 逐条补详情 | Cloud-only、单 name、N+1，覆盖不了本地清单与批量。 |
| 复用 `-a/--all` 控制元数据 | 语义已被 ADR-028 改为「named 版本展开」，复用会冲突。 |
| 直接改共享 `powerJSON` 去 omitempty | 会破坏 search「永远不输出 tags」的契约。 |
| 新增独立 machine 子命令 | 工作量最大，短期收益不如直接补 list JSON。 |

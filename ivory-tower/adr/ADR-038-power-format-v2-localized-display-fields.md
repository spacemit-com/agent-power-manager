# Power 资源格式 V2：本地化展示字段

- Status：Accepted
- Date：2026-09-28
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- 关联既有决策：[ADR-017](ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)（从 canonical 索引派生 Agent 原生配置）、[ADR-020](ADR-020-source-owned-submission-metadata.md)（资源文件是唯一身份来源）、[ADR-033](ADR-033-require-agent-id-to-equal-agent-name.md)（`agent.id` 与 `agent.name` 同值）

> 局部取代说明：本 ADR 关于「不引入 `name_en`」与展示名字段长度规则的两条决定，已被
> [ADR-039](ADR-039-add-name-en-and-display-name-length-limits.md) 取代（新增 `name_en` 并限定
> `name_zh` 2–16 字、`name_en` ≤ 32 字符）。本 ADR 的其余决定——v2 版本门禁、提交侧与安装侧的
> 校验分层、`zh_name` 更名、派生物不携带展示字段、不改 APC 目录契约与 `list`/`show`/`search`——全部继续有效。

## Context

四类 Power（Skill / Plugin / MCP / Agent）的展示信息目前只有 `name` 与 `description`。`name` 是 canonical
小写 kebab 调用名，`description` 是面向 Agent 发现的长句英文说明，两者都不适合作为前端列表的中文展示名与中文
简介。APC 前端需要一个稳定的中文展示名与中文描述，同时不能影响 APM 的调用名、缓存键、安装路径与排序。

现状约束：

- `name` 已被 [ADR-020](ADR-020-source-owned-submission-metadata.md) 固定为唯一身份与调用名，不能承载展示语义。
- Agent 资源已有 `agent.zh_name`，但它位于「APM 不校验、安装时原样透传」的扩展字段区，既无格式校验也无版本门禁；
  该键名与其余三类的命名风格也不一致。Bibit Runtime 的 `/api/v1/agents` 以 `zh_name` 返回展示名，客户端
  `RuntimeAgent` 用 `#[serde(alias = "zh_name")]` 兼容读取，因此这是有真实消费方的字段。
- 四类资源的 `schema_version` 语义此前不统一：`skill-card.md`、`apc-plugin.json`、`MCP.json`、`agent.yaml`
  各自为 `1`，而 `SKILL.md` 根本没有版本字段。
- 提交侧与安装侧复用同一批校验函数（`ValidateAPCPluginManifest`、`MCPResource.Validate`、
  `AgentConfig.Validate`），任意一侧收紧都会同时影响另一侧。

本轮用户明确要求：新增 `name_zh` 与 `description_zh`（仅用于 APC 前端展示，与 APM 功能无关，`name` 保留为调用名）；
`agent.yaml` 的 `zh_name` 更名为 `name_zh`；提交拒绝 v1、全面升级至 v2；不改 APC 目录契约与 `list`/`show`
的现有输出。首版实现按当轮决定「`name` 默认即英文名，不引入 `name_en`」；随后用户追加要求补上 `name_en`
并限定展示名长度，该调整见 [ADR-039](ADR-039-add-name-en-and-display-name-length-limits.md)，本 ADR 相应条款已被取代。

## Decision

四类 Power 的资源文件格式升到 **v2**，新增仅用于展示的 `name_zh` 与 `description_zh`；提交侧只接受 v2，
安装与目录解码路径继续接受 v1 与 v2。

具体条款：

1. **字段与位置**：`name_zh`、`description_zh` 分别写入 `SKILL.md` frontmatter、`apc-plugin.json`、
   `MCP.json` 顶层、`agent.yaml` 的 `agent` 区块。`SKILL.md` 新增整数 `schema_version`；`skill-card.md` 的
   `schema_version` 由 `1` 升到 `2`，使同一份资源的版本取值一致。`name` 仍是唯一身份、调用名与排序键。
   （原决定为「`name` 默认即英文名，不引入 `name_en`」，已由 ADR-039 取代。）
2. **字段规则**：`name_zh` 为自由展示文本（允许空格、中文与标点，**不**套用 canonical name 规则），
   trim 后非空、不含控制字符；`description_zh` trim 后非空、≤ 1024 字节、允许换行。
   （原决定为 `name_zh` ≤ 128 字节，已由 ADR-039 收紧为 2–16 个字符。）
3. **提交侧门禁**：`apm validate` 与 `apm submit` 只接受 `schema_version: 2`，并要求展示字段必填
   （ADR-039 起为 `name_zh`/`name_en`/`description_zh` 三个）；
   v1 资源被拒绝，错误信息给出 `upgrade to 2` 提示。
4. **安装侧兼容**：目录解码、内容下载、缓存复验、派生与事务恢复路径继续接受 `schema_version` 1 或 2；
   v2 若提供展示字段则校验形状，缺失不报错。已发布的 v1 资源因此仍可安装。
5. **`zh_name` 更名**：`agent.yaml` 只识别 `agent.name_zh`；出现旧键 `agent.zh_name` 时显式报错
   `agent.zh_name was renamed to agent.name_zh`，不静默忽略，避免作者以为字段生效却丢失展示名。
6. **派生物不带新字段**：Codex/Claude/Hermes/Bibit 的原生落盘清单与 marketplace 继续只含
   `name`/`version`/`description`/`skills`（或 `plugin.yaml` 四字段）；`apc-plugin.json` 是唯一权威源，
   派生清单由 `reflect.DeepEqual` 严格复验，不得携带本地化字段。
7. **不改的范围**：APC 目录 item 契约与 `../apc` 文档不动；`list --all` 仍只展示全版本；`list --json` / `show`
   输出不变；`search` 仍只匹配 `name`；提交 metadata 不上报这两个字段（它们只在资源内容里，供服务端解析）。

## Consequences

- 正面影响：APC 前端可从资源内容取得稳定的中文展示名与中文描述；`name` 的调用名语义保持零污染。
- 正面影响：`agent.yaml` 的展示字段进入受校验、带版本门的正式 schema，不再是「透传但无人管」的扩展键。
- 正面影响：提交/安装两侧的门禁差异被显式建模为两个函数（`ValidateSubmit*` 与 `Validate*`），
  后续再演进版本时有明确挂点。
- 代价与风险：**v1 资源将无法再提交**（`validate` 与 `submit` 都拒绝），存量作者必须补齐展示字段并把
  `schema_version` 改成 2；这是本轮用户明确要求的破坏性变更。
- 代价与风险：`skill-card.md` 的结构没有变化却升了版本，纯粹为了「一份资源的版本取值一致」；若未来
  两个文件需要独立演进，需要在新的 ADR 中拆分版本语义。
- 代价与风险：APC 若从 content 解析这两个字段，则历史已发布版本没有它们，前端对旧版本只能回退显示
  `name`/`description`；APC 侧如需回填，属于 `../apc` 的独立决策。
- 后续动作：Bibit Runtime 的 `agents/<name>/agent.yaml` 字段由 `zh_name` 变为 `name_zh`，Runtime 侧读取
  与 `/api/v1/agents` 响应需要同步映射（跨仓库依赖，不在本仓库范围内，已在 Feature 031 中记录）。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 在 APC 目录 item 上新增字段承载展示名 | 用户明确要求不改 APC 目录契约与 `../apc` 交付；且目录是内容寻址的字段闭集，扩展需要服务端与客户端同步升级。 |
| 保留 v1 并对新字段做可选扩展 | 用户明确要求提交拒绝 v1、全面升 v2；无版本门的字段无法与存量数据区分。 |
| 提交侧与安装侧使用同一套严格校验 | 会让已发布的 v1 资源无法安装，破坏离线安装与既有缓存；需要服务端重发全部历史资源才可恢复。 |
| 同时新增 `name_en` | 首轮依据「`name` 默认即英文名」判断其与调用名重复而未引入；用户在 ADR-039 中追加要求独立的英文展示名，该取舍随之被取代。 |
| 复用 `description` 承载中文简介 | `description` 是 Agent 发现用的英文触发说明，改成中文会破坏 Skill 的自动发现语义。 |
| 静默接受旧键 `zh_name` | 字段更名后静默忽略会让作者以为展示名已生效而实际丢失；显式报错可在 validate 阶段暴露。 |
| 让派生 Agent 原生清单也带上中文名 | 原生 schema 没有该概念，且派生复验使用 `reflect.DeepEqual` 期望值；带上会破坏复验并污染 Agent 侧配置。 |

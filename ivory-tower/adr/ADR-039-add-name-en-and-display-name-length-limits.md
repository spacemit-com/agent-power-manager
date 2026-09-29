# 新增 name_en 并限定展示名长度

- Status：Accepted
- Date：2026-09-28
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- 局部取代说明：本 ADR 取代 [ADR-038](ADR-038-power-format-v2-localized-display-fields.md) 中
  「不引入 `name_en`，`name` 默认即英文名」与「`name_zh` ≤ 128 字节」两条决定。ADR-038 的其余决定
  （v2 版本门禁、提交侧与安装侧校验分层、`zh_name` → `name_zh` 更名、派生物不携带展示字段、
  不改 APC 目录契约与 `list`/`show`/`search`）继续有效。

## Context

ADR-038 把四类 Power 升到 v2，新增 `name_zh` 与 `description_zh`，并判断 `name` 已经能充当英文展示名，
因此没有引入 `name_en`。随后用户要求：在 v2 基础上补上 `name_en`，并对展示名施加长度约束——
中文 2–16 字、英文 ≤ 32 字符。

需要决策的约束：

- `name` 是 canonical 小写 kebab 名（1–64 字节、仅 `a-z0-9-`），无法承载「中文 2–16 字」这类规则；
  它同时被用作调用名、缓存键、安装路径与排序键，因此不能为展示需要而被收紧。
- 前端需要一个固定的英文展示名：`name` 的 kebab 形态（如 `foo-before-summary`）在英文界面上并不适合直接展示，
  这正是 `name_en` 存在的理由。
- 上一轮 v2 刚落地、尚无已发布的 v2 资源，此时收紧字段规则的迁移成本最低。
- 提交侧与安装侧的门禁在 ADR-038 中已经分层，本轮必须继续沿用：提交强制、安装侧存在才校验。

## Decision

四类 Power 的 v2 资源在 `name_zh` 之外**再新增 `name_en`**，并对两个展示名施加长度与字符集约束；
三个展示字段在提交侧全部必填，安装侧仍只在字段存在时校验形状。

具体条款：

1. **新增 `name_en`**：写入 `SKILL.md` frontmatter、`apc-plugin.json`、`MCP.json` 顶层、`agent.yaml` 的
   `agent` 区块。`name` 仍然是唯一身份、调用名与排序键；`name_en` 只是英文展示文本，不参与身份比较、
   缓存键、安装路径或 `search` 匹配。因此 ADR-038 的「`name` 默认即英文名」不再作为展示依据。
2. **`name_zh` 长度**：trim 后非空，长度按 **Unicode 字符数**计（不按字节），必须落在 2–16 个字符之间；
   允许中文、空格与标点，仍拒绝控制字符。这取代 ADR-038 的「≤ 128 字节」。
3. **`name_en` 形状**：trim 后非空，长度 ≤ 32 个字符，逐字符必须是可打印 ASCII（`0x20`–`0x7e`），
   且至少包含一个 ASCII 字母。允许空格、大小写与常规标点；拒绝中文、纯数字、纯标点等无字母取值。
4. ~~**`description_zh` 不变**：trim 后非空、≤ 1024 字节、允许换行。~~ 本条已被
   [ADR-042](ADR-042-cap-description-length-to-apc-server-limit.md) 取代：提交侧改为 ≤ 500 个 UTF-16 码元，
   并与 `description` 共用同一上限。
5. **提交侧**：`apm validate` 与 `apm submit` 要求 `name_zh`、`name_en`、`description_zh` 三个全部提供并
   通过形状校验；缺失或越界时错误信息点名具体字段。
6. **安装侧**：目录解码、内容下载、缓存复验、派生与事务恢复继续接受 v1 与 v2；v2 若提供展示字段则校验
   形状（含长度），缺失不报错。v1 资源无展示字段，不受新长度规则影响。
7. **不改的范围**：canonical `name` 仍为 1–64 字节小写 kebab，不套用 2–16 / ≤ 32 的新规则；APC 目录契约、
   `list`/`list --all`/`show`/`search` 输出与匹配语义、提交 metadata、本地 manifest 与 journal 均不变。

## Consequences

- 正面影响：前端有独立可控的英文展示名，不再被迫展示 `name` 的 kebab 形式。
- 正面影响：展示名长度受控，前端列表布局与截断逻辑可以依赖固定上界。
- 代价与风险：提交门槛又提高一步——v2 资源必须同时补齐三个展示字段；因为尚无已发布的 v2 资源，
  实际迁移面只有本仓库内置 fixture 与 `../using-apm` 模板。
- 代价与风险：`name_zh` 的 2–16 字上界对少数需要长名称的资源偏紧；若确有需要，应由后续 ADR 明确放宽，
  不得在资源里绕过校验。
- 代价与风险：`name_en` 与 `name` 可能不一致，出现「调用名 `foo-bar`、英文展示名 `Foo Bar`」的双轨语义；
  这是展示与调用分离的正常代价，但文档必须讲清 `name` 才是调用名。
- 后续动作：Bibit Runtime 若同时需要中英文展示名，需要在其 Agent 配置读取侧一并映射 `name_en`
  （跨仓库依赖，不在本仓库范围内）。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 复用 `name` 作为英文展示名 | 用户明确要求独立的 `name_en`；`name` 的 kebab 形态不适合英文界面直接展示。 |
| 把长度规则也套到 canonical `name` | `name` 是调用名、缓存键与安装路径来源，收紧会波及安装侧与既有资源（如 `foo-before-summary` 已超 16 字符）。 |
| `name_zh` 按字节而不是按字符计数 | 一个汉字通常占 3 字节，「2–16 字」按字节解释会退化成约 6 个汉字，与用户表述不符。 |
| `name_en` 允许任意 Unicode | 英文展示名的价值在于稳定的拉丁字形与可预期的宽度，混入中文后与 `name_zh` 职责重叠。 |
| 安装侧也强制三个字段齐备 | 会让已发布或手工生成的 v2 制品无法安装，破坏 ADR-038 已确立的双版本兼容边界。 |
| 只加字段不加长度限制 | 用户明确要求长度约束，且无上界的展示名无法支撑前端布局。 |

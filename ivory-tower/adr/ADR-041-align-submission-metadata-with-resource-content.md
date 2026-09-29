# 提交 metadata 与资源 content 字段对齐

- Status：Accepted
- Date：2026-09-29
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- Related ADR：[ADR-020](ADR-020-source-owned-submission-metadata.md)（资源文件是唯一身份来源）、
  [ADR-038](ADR-038-power-format-v2-localized-display-fields.md)（v2 展示字段）、
  [ADR-039](ADR-039-add-name-en-and-display-name-length-limits.md)（字段形状规则）、
  [ADR-040](ADR-040-catalog-and-cli-json-carry-display-fields.md)（目录与 CLI JSON 携带展示字段）
- 局部取代说明：本 ADR 取代 ADR-038 第 7 条中「提交 metadata 不上报这两个字段」与 ADR-040 Context 中
  「提交 metadata 不上报它们」两处决定。ADR-038 的 v2 版本门禁、提交侧与安装侧校验分层、
  `zh_name` → `name_zh` 更名、`search` 只匹配 `name`、派生清单不携带展示字段等其余决定继续有效。
- 局部被取代（2026-09-29 追加）：第 1 条的字段集中「MCP 另加 `definition`」、第 5 条把 `definition`
  列入两侧逐字段比较、第 6 条把「metadata 携带 definition」当作发布正确的表述，均被
  [ADR-043](ADR-043-mcp-definition-from-content-only.md) 取代。MCP 的 catalog 信息从 metadata 保存，
  实际定义只从 content 读取；本 ADR 的其余条款（展示字段对齐、`schema_version` 用资源格式版本、
  四类一致、传输专有字段保留）继续有效。

## Context

ADR-038 落地时把展示字段刻意留在 metadata 之外，理由是「它们只在资源内容里，供服务端解析」。
2026-09-29 的端到端探测证明这个前提不成立：APC 发布 MCP 时并用的是 metadata，而不是上传的 content。

复现证据（绕过 apm 手工构造 multipart 直投 APC，author-verified）：

- 上传 `metadata`（协议字段，无展示字段）＋ `content`（`schema_version: 2` 且带
  `name_zh`/`name_en`/`description_zh` 的 386 字节两空格缩进 JSON），审批通过后从 CDN 取回
  `resource/mcp/<name>-1-v1.0.0.json` 得到 **319 字节紧凑 JSON**：字段集恰好等于 metadata 的八个键，
  `schema_version` 为 **1**，三个展示字段**全部丢失**；把 metadata 按
  `type,name,version,description,message,definition,schema_version,content_md5` 序列化后与发布件
  **逐字节相同**。
- 用 apm 正常提交的 MCP 同样如此：apm 上传的 JCS 字节 md5 与目录登记的 `content_md5` 不一致，
  后者等于发布件本身的 md5。
- 目录 `/api/v1/resources` 的 26/26 条目字段固定为 11 个，均无展示字段；提交详情与
  `/resources/version` 也没有。

由此产生两个问题：**MCP 的 v2 展示字段提交能过审、发布即丢**；**发布件回退成 v1 骨架后无法原样再提交**，
因为提交侧只接受 v2（ADR-038），从目录下载的 MCP 文件只能拿到 `schema_version: 1`。

同时，metadata 的 `schema_version` 恒为常量 `1`，与资源格式版本（提交侧恒为 2）是两个互不相干的数字，
却被同一个键名承载，容易被误读为「资源是 v1」。

约束：

- 用户已确认 APC 会改成**从 content 重建**目录数据；因此 content 是权威来源，metadata 只需与它对齐，
  不再需要担心「metadata 是闭集、多带字段会失败」。
- 资源文件始终是身份与元数据的唯一来源（ADR-020）；metadata 只是同一次提交的传输副本，不得引入
  资源文件里没有的新信息。
- 四类资源都要改：只对齐 MCP 会让 skill/plugin/agent 与 MCP 的 metadata 语义分叉，前端无法用同一套
  逻辑读取。

## Decision

**提交 metadata 与 content part 中的资源元数据字段对齐：metadata 补齐 `name_zh`/`name_en`/
`description_zh`，`schema_version` 改为上报资源格式版本（提交侧恒为 2），四类资源一致。**

具体条款：

1. **metadata 字段集**：在 `type`/`name`/`version`/`description`/`message`/`content_md5`（MCP 另加
   `definition`）之外，新增 `name_zh`/`name_en`/`description_zh`。四类资源都携带，取值与资源文件
   完全一致，不做翻译或补全。
2. **保留传输专有字段**：`type` 是路由信息、`message` 是本次提交说明、`content_md5` 是传输完整性
   校验。三者不属于资源元数据，只存在于 metadata，不写入内容文件，也不因为「对齐」而删除。
3. **`schema_version` 改为资源格式版本**：`submissionMetadataDTO.SchemaVersion` 取
   `SubmissionRequest.SchemaVersion`，即资源文件的格式版本；提交侧只接受 v2，因此实际恒为 `2`。
   非 v2 或缺失一律 fail closed，错误信息为 `submit remote resource requires resource schema version 2, got N`。
4. **版本来源是资源文件**：`submission.Validation`/`submission.Content` 新增 `SchemaVersion`，四个
   source loader 分别从 `SKILL.md` frontmatter、`apc-plugin.json`、`MCP.json`、`agent.yaml` 取值；
   lifecycle 只做透传，不自行决定版本。
5. **对齐由测试固定**：`internal/cloud/remote/submission_test.go` 的
   `TestSubmitMetadataMirrorsResourceContent` 逐字段比较 metadata 与 content 的资源元数据字段
   （`name`/`version`/`description`/`name_zh`/`name_en`/`description_zh`/`schema_version` 及 `definition`），
   并断言 content 不含传输专有的 `type`；跨层契约测试与 fixture server 同时断言展示字段确实上传。
6. **发布顺序**：APC 先改成从 content 重建，APM 与之同步上报对齐字段。两侧字段集相同后，
   从 metadata 重建与从 content 重建将得到同一结果；`schema_version` 不再有「协议 1 / 资源 2」的歧义。

## Consequences

- 正面影响：APC 从 metadata 或 content 任一侧都能得到含展示字段的完整资源元数据，MCP 的 v2 展示字段
  不再在发布时丢失。
- 正面影响：`schema_version` 在两侧同义同值，消除了「metadata 写 1 但资源是 v2」的误导。
- 正面影响：四类资源行为一致，前端与 APC 不需要按类型分支。
- 代价与风险：**依赖 APC 先完成改造**。若 APM 先发而 APC 仍从 metadata 重建，结果反而更完整
  （展示字段到位、`schema_version` 为 2）；反之若 APC 仍把 metadata 当闭集并拒绝未知字段，新 APM
  提交会整体失败。发布顺序为 APC 先、APM 紧跟。
- 代价与风险：已发布的 MCP 条目（`nws`、`bilibili-search` 及探测件）内容已是 v1 骨架，**不会追溯变化**；
  要让它们带展示字段只能重新发版。
- 代价与风险：`SubmissionRequest` 新增字段属于内部接口变更，`internal/cloud/remote` 与其测试、
  lifecycle 测试需同步；无 wire 兼容负担（metadata 是服务端消费的请求体，不是本地持久化格式）。
- 后续动作：`APC-API.md` 的 metadata 字段表与示例、`docs/power-template/*.md`、`docs/Changelog.md`
  同步更新；`../using-apm/references/submission.md` 中「展示字段只在资源文件里」的说明需要更正。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 只对齐 MCP | skill/plugin/agent 与 MCP 语义分叉，前端与 APC 需要按类型分支；用户明确要求四类一致。 |
| metadata 只带 `schema_version: 2`，不加展示字段 | 仍无法让 APC 从 metadata 重建展示字段，MCP 丢字段的问题不解决。 |
| 删除 `type`/`message`/`content_md5` 做成 content 完全镜像 | 三者有独立语义（路由、提交说明、传输校验），删掉会丢失服务端与审核侧需要的信息。 |
| 让 APC 继续以 content 为唯一来源、APM 不动 | 与「两侧字段对齐」的目标相反；metadata 与 content 会长期保持两份不一致的真相。 |
| 由 APM 在提交前把 content 重写成「metadata 形态」再上传 | 会污染资源内容、破坏 v2 校验与 JCS 规范化，且把服务端缺陷固化到客户端。 |

# MCP 定义改由 content 提供，提交 metadata 不再携带 definition

- Status：Accepted
- Date：2026-09-29
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- Related ADR：[ADR-041](ADR-041-align-submission-metadata-with-resource-content.md)（metadata 与 content 字段对齐）、
  [ADR-020](ADR-020-source-owned-submission-metadata.md)（资源文件是唯一身份来源）
- 局部取代说明：本 ADR 取代 [ADR-041](ADR-041-align-submission-metadata-with-resource-content.md) 中
  「MCP 的 `definition` 必须与 content 逐字段相等地上报 metadata」这一条，以及 ADR-041 第 5 条与
  第 6 条中把 `definition` 列入两侧对齐字段、把「metadata 携带 definition」当作发布正确的表述。
  ADR-041 的其余决定——metadata 补齐 `name_zh`/`name_en`/`description_zh`、`schema_version` 上报资源格式版本、
  四类资源一致、`type`/`message`/`content_md5` 保留为传输专有字段——继续有效。

## Context

ADR-041 让 metadata 与 content 的资源元数据逐字段对齐，其中包含 MCP 的 `definition`，理由是
「两侧同字段后，服务端从任一侧重建都能得到同一结果」。

2026-09-29 验证这套字段集能否真正提交时，发现 APC 的 MCP 通道在两个环境上都不接受这个组合。
对 `schema_version`（1/2）× 展示字段（有/无）× `definition`（有/无）做了 8 组对照，两个环境各跑一遍，
同时用 apm CLI 的真实字节与手工构造的 multipart 各复现一次：

| metadata 的 `definition` | metadata 的展示字段 | 测试环境 | 生产环境 |
| --- | --- | --- | --- |
| 有 | 无 | 500 | 202 接受 |
| 有 | 有 | 500 | 500 |
| 无 | 无 | 202 接受 | 500 |
| 无 | 有 | 202 接受 | 500 |

结论：**「v2 + 展示字段 + definition」这个组合两个环境都 500**；`schema_version` 与是否存在展示字段
都不是测试环境的决定因素，`definition` 的有无才是。用 CLI 原始 JCS 字节重放结果相同，
说明与客户端无关。此外单独验证过：测试环境在 metadata 不带 `definition` 时会把提交收下，
但生成的提交详情里**没有** definition，即它当时并未从 content 重建定义。

由此确认 APC 侧的修订方向：**MCP 的 catalog 信息从 metadata 保存（含 `name`、描述等，但不含定义），
实际定义从 content 读取**。这与 ADR-041 的前提——「metadata 与 content 是同一份元数据的两份副本」——
在 MCP 上不再成立：`definition` 属于「只在 content 里的资源实体」，而不是 metadata 的镜像字段。

## Decision

**MCP 提交的 metadata 不再携带 `definition`；`definition` 只出现在 content part 的资源文件里。**

具体条款：

1. **metadata 字段集**：`schema_version`/`type`/`name`/`version`/`description`/`name_zh`/`name_en`/
   `description_zh`/`message`/`content_md5`，四类资源一致，**不再有 `definition`**。
   删除 `submissionMetadataDTO.Definition` 字段（连带其 `json:"definition,omitempty"` 标签）。
2. **content 仍是定义的唯一来源**：MCP 资源文件继续必须携带 `definition`，提交仍上传其 JCS 字节；
   `content_md5` 仍按该字节计算。服务端按新修订从 content 读取定义。
3. **本地校验不放宽**：`MCPResource.ValidateSubmit` 继续要求 `definition` 存在且形状合法。
   定义是 MCP 资源的实质内容，缺失即资源不完整，仍由 `apm validate` 拦下——本轮只改传输层，
   不改资源格式。
4. **删除 MCP 的提交前 definition 检查**：`remote.Client.Submit` 原有一处
   `submit remote MCP resource requires definition`（要求 `SubmissionRequest.Definition != nil`）
   随字段一起删除。该检查原本是「metadata 必须有 definition」的前置条件；定义已在 content 里，
   metadata 不再需要它，保留检查只会让正确调用被拒。
5. **`SubmissionRequest.Definition` 字段移除**：metadata 不再有该字段，请求结构里保留它只会让人误以为
   它仍会上传。`submission.Content`/`source`/`Validation` 里为传输而保留的 `definition` 透传一并删除，
   `mcpSourceLoader` 不再克隆并携带 `resource.Definition`。`internal/submission` 的 `source.definition`
   字段随之删除，避免留下「无消费者」的死字段。
6. **MCP 定义读取路径不变**：安装、下载、缓存与目录解码继续经 `MCPResource.Definition`
   （`internal/cloud/remote/mcp.go`、`internal/cloud/local/mcp.go`）取值，本 ADR 不影响这些路径。
7. **对齐由测试固定**：`TestSubmitMetadataMirrorsResourceContent` 改为断言 metadata 的
   `name`/`version`/`description`/`name_zh`/`name_en`/`description_zh`/`schema_version` 与 content 相等，
   并**断言 metadata 不含 `definition`**、MCP 的 definition 只出现在 content 里；
   fixture server 与契约测试同步改为「MCP metadata 无 definition、content 有 definition」。

## Consequences

- 正面影响：MCP 的 v2 提交不再被 APC 500 拒绝；「v2 + 展示字段」的资源可以正常发布。
- 正面影响：metadata 只承载 catalog 所需信息（身份、描述、展示字段、版本），资源实体（定义）只从 content 读取，
  职责边界比 ADR-041 的「全字段镜像」更清晰。
- 代价与风险：**依赖 APC 已按新修订上线**。若 APC 仍期望 metadata 带 `definition`（例如仍把 metadata 当闭集
  并要求该键存在），新 APM 的 MCP 提交会被拒。发布顺序仍为 APC 先、APM 紧跟；四类资源里只有 MCP 受影响，
  skill/plugin/agent 的 metadata 不变。
- 代价与风险：`SubmissionRequest` 删除公开字段属于内部接口变更，`internal/cloud/remote` 与其测试、
  `internal/lifecycle`、`internal/domain` 与 `internal/submission` 的测试需同步；APC 的 metadata 契约不是
  本地持久化格式，无 wire 兼容负担。
- 代价与风险：APC 从 content 读取定义意味着**content 必须具备完整定义**；因此客户端不能再把定义只放在
  metadata。这正是本地校验不放宽的原因。
- 后续动作：`APC-API.md` 的 metadata 字段表、示例与说明，`docs/power-template/README.md`、
  `docs/power-template/mcp.md`、`docs/Changelog.md` 同步更新。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保持 ADR-041 不变，等 APC 支持 metadata 带 definition | 已确认 APC 的修订方向是「定义只从 content 读」，继续上报 definition 与服务端契约相反。 |
| metadata 与 content 都不带 definition | 定义是 MCP 的实质内容，不携带就无法安装；且 `apm validate` 明确要求它存在。 |
| 只删除 metadata 的 definition，同时放宽本地对 definition 的校验 | 会允许上传不可安装的 MCP；服务端从 content 读定义，content 缺定义即资源损坏。 |
| 保留 `SubmissionRequest.Definition` 但不再序列化 | 留下无消费者的字段，后续维护者会误判它仍参与上传。 |
| 同时改 skill/plugin/agent 的 metadata 字段集 | 三者没有 definition 语义，且实测它们的 metadata 已被接受；改动只会扩大回归面。 |

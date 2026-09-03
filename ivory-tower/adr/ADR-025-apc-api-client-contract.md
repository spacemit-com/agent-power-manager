# Adopt APC-API.md as the Client Wire Contract

- Status：Superseded（由 [ADR-026](ADR-026-adapt-apc-client-to-test-environment-behavior.md) 取代）
- Date：2026-09-03
- Decision Makers：APM maintainers
- Related Feature：[Feature 028](../features/028-apc-api-adaptation/spec.md)
- Source contract：`APC-API.md`

## Context

APC 对外接口以 `APC-API.md` v1 作为客户端唯一权威协议，覆盖七类接口：公开目录版本
`GET /resources/version`、完整目录 `GET /resources`、四类资源精确内容
`GET /resources/{type}/{name}/versions/{version}/content`，以及需认证的 Submission 三接口
`POST /submissions`、`GET /submissions/{submissionId}`、`GET /submissions`。

对接前的只读探测观察到测试环境实际行为与文档存在偏差：Submission 认证可能仍要求
`Authorization`，且目录可能对 MCP 返回 `application/gzip` 而非文档规定的
`application/json`（该偏差由 Feature 028 的验收探测确认并在验收报告中记录，不作为客户
端降级依据）。现有 APM 实现为 Submission 发送 `Authorization: Bearer`，匿名目录/content
保持无凭据，且 Remote 默认根地址指向 `http://127.0.0.1:8080`。

## Decision

以 `APC-API.md` 为客户端唯一权威契约，APM 客户端只做文档合规的适配，不因测试环境偏差
引入隐式兼容：

1. Submission 三个接口只发送 `X-API-Key: <key>`，不发送 `Authorization`，不实现 Bearer
   回退或双凭据发送。`APC_API_KEY` 仅用于 Submission；匿名 catalog/content 请求不携带任何
   凭据，即使同一 Remote Client 已配置 Key。
2. Submission multipart metadata 必须包含 `type`、`name`、`version`、`schema_version: 1`、
   `description`、`message`；除 MCP 外必须包含 `content_md5`。
3. Remote 默认根地址改为测试环境根 `https://developer.bianbu.xyz`，仍可由 `APC_BASE_URL`
   覆盖；保留既有 base URL 校验（绝对、无凭据/query/fragment、根路径；非 loopback HTTP 拒绝）。
4. Skill/Plugin/Agent 内容类型固定为 `application/gzip`，MCP 为 `application/json`；目录或
   content 返回其它类型时 fail closed，不静默扩大接受范围。
5. 测试环境与文档的认证或内容类型偏差，作为验收限制在报告和 `docs/Changelog.md` 中记录，
   不通过客户端降级伪造成功。

## Consequences

- 正面影响：客户端 wire 与权威协议一致，凭据范围严格收敛到 Submission，匿名请求无凭据泄露。
- 正面影响：`schema_version` 与内容类型映射明确，服务端契约漂移被 fail-closed 暴露而非吞掉。
- 代价与风险：若测试环境确实仍要求 `Authorization` 或返回 MCP `application/gzip`，真实
  Submission 探测或 MCP 安装会失败并被记录为服务端阻塞，客户端不提供绕过路径。
- 代价与风险：默认根地址从 loopback 改为真实测试环境，本地开发 fixture 需显式设置
  `APM_CLOUD_PROVIDER=local` 或 `APC_BASE_URL`。
- 后续动作：完成 Feature 028 验收后，若测试环境长期保持与文档不一致，需推动 APC 侧修正
  或在 `APC-API.md` 中仅做事实标注（不改变客户端目标契约）。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Submission 保留 `Authorization: Bearer` 回退 | 与权威协议冲突，且形成隐式降级，掩盖服务端契约漂移。 |
| 同时发送 `X-API-Key` 与 `Authorization` | 双凭据扩大凭据面，且文档只规定 `X-API-Key`，属于协议违规。 |
| 客户端在观察到测试环境偏差时静默接受其它内容类型 | 违反 fail-closed 原则，削弱目录完整性校验。 |
| 生产环境真实提交 | 超出 Feature 028 范围；验收仅使用批准的临时 Key 做查询与探测。 |

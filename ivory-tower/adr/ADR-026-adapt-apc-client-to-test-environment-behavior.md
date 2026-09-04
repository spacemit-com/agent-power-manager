# Adapt APC Client to Test-Environment Actual Behavior

- Status：Accepted
- Date：2026-09-03
- Decision Makers：APM maintainers
- Related Feature：[Feature 028](../features/028-apc-api-adaptation/spec.md)
- Supersedes：[ADR-025](ADR-025-apc-api-client-contract.md)

## Context

Feature 028 的只读探测（见 [live-probe](../features/028-apc-api-adaptation/live-probe.md)）确认测试环境
`https://developer.bianbu.xyz` 的实际行为与 `APC-API.md` 存在多处不一致：真实基址带网关前缀
`/api-server/apc`、Submission 只认 `Authorization: Bearer`、目录把 MCP 声明为 `application/gzip`、
目录与内容 `302` 到跨源 CDN、目录 schema 形状与文档不符。ADR-025 曾决定以 `APC-API.md` 为唯一
权威契约并 fail-closed 暴露漂移。

本轮用户明确指示：以测试环境实际行为为准（服务端实际行为优先于接口文档），把客户端适配为对
真实服务可用，而非继续 fail-closed 阻塞。

## Decision

客户端以测试环境实际行为为准完成适配，服务端行为覆盖 `APC-API.md`；所有放宽都限定在真实服务
暴露的具体字段与传输特征，不扩大为无条件信任：

1. **网关前缀**：`endpoint()` 在所有 `/api/v1/...` 相对路径前固定拼接 `/api-server/apc`；base URL
   校验仍要求根路径，`APC_BASE_URL` 语义不变。
2. **Submission 认证**：三个 Submission 接口改为只发送 `Authorization: Bearer <key>`，不再发送
   `X-API-Key`；凭据范围仍严格收敛到 Submission，匿名目录/content 请求无凭据。
3. **MCP 内容类型**：目录解码时把 MCP 误声明的 `application/gzip` 归一化为实际内容的
   `application/json`，使后续 `Validate`、`GetMCP` 与 content 类型校验一致；domain 层仍拒绝直接
   声明 `application/gzip` 的 MCP 条目（防御性不变量）。
4. **跨源重定向**：匿名目录/content 请求允许跨源重定向到 HTTPS CDN，拒绝降级到 HTTP、携带
   userinfo 或认证请求的跨源跳转（凭据不扩散）。
5. **目录 schema 容忍**：顶层/分组 `schema_version` 改为可选（缺省视为 v1）；item 容忍
   `schema_version` 与 `download_url`；MCP 条目 `message` 可空；`published_at` 接受 RFC3339 或
   无时区 ISO 8601。
6. **HTTP/2 无 Content-Length**：CDN 经 HTTP/2 返回目录时不带 `Content-Length`，目录下载以
   `MaxCatalogSize` 为上界读取；最终大小与哈希仍由目录版本响应 + sha256/size 校验兜底。
7. **MCP 资源额外字段**：MCP 资源 JSON 容忍服务端额外返回的 `type`/`message`/`content_md5`
   （可能为 null），不参与身份或完整性校验。
8. **gzip 内容媒体类型别名**：目录声明 `application/gzip` 的 Skill/Plugin/Agent 归档，CDN
   实际返回历史别名 `application/x-gzip`；content 校验把该别名归一化为 `application/gzip`
   后再与目录声明比较。归一化只作用于响应侧、只认精确的 `application/x-gzip`，目录元数据
   仍要求 IANA 声明 `application/gzip`（fail-closed 不扩大）。
9. **tar 结束块后的 NUL 填充**：测试环境归档用 GNU tar blocking factor 20 生成，解压后在
   tar 结束块之后还有 8192 字节的 NUL 填充（总长对齐到 10240 字节）。解包校验容忍结束块后的
   纯 NUL 填充，但拒绝任何非零尾随字节，防拼接归档/多归档多态的安全约束不变。

## Consequences

- 正面影响：`apm list` / `apm search` / MCP 安装对真实测试环境可用；客户端 wire 与真实服务对齐。
- 正面影响：凭据收敛与匿名请求无凭据约束保持不变；跨源重定向仅放开匿名 HTTPS，未扩大凭据面。
- 代价与风险：客户端偏离 `APC-API.md`，若服务端后续修正目录/认证/类型，客户端需再次对齐；宽松
  点（MCP gzip→json、目录 schema 可选、无时区时间戳）在服务端改正后仍兼容。
- 代价与风险：`published_at` 无时区值按原样展示，不做时区换算，跨时区比较可能不一致（当前仅展示）。
- 后续动作：推动 APC 侧修正 `APC-API.md` 或客户端契约；测试环境 CA 链（环境问题）由基础设施补齐，
  产品代码保持 TLS 校验开启。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 维持 ADR-025 的 fail-closed | 真实服务不可用，与用户“以服务端实际行为为准”的指示冲突。 |
| 同时发送 `X-API-Key` 与 `Authorization` | 扩大凭据面，且服务端只认 Bearer，双发送无收益。 |
| 关闭 TLS 校验以绕过 CA 缺失 | 安全降级，属环境问题而非客户端适配范围。 |
| 把 `APC_BASE_URL` 改为含前缀的完整路径 | 破坏 base URL 根路径校验与既有覆盖语义，前缀应属于端点构造。 |

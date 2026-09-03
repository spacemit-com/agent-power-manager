# Feature 028：APC-API v1 接口适配与 APM 验收

- Status：Proposed
- Date：2026-09-03
- Related ADRs：拟新增 [ADR-025](../../adr/ADR-025-apc-api-client-contract.md)
- Source contract：[`APC-API.md`](../../../APC-API.md)

## 1. Goal

依据 APC-API.md 将已上线的 APC v1 接口逐一接入 APM，并在对接前完成接口契约探测、对接后完成 APM 功能验收。APC-API.md 是客户端唯一权威协议；测试环境当前实际认证行为若与文档不一致，必须作为服务端阻塞记录，不能通过隐式降级伪造成功。

## 2. Scope

### 2.1 Included

- 公开目录接口：`GET /api/v1/resources/version`、`GET /api/v1/resources`。
- 四类资源精确内容接口：`GET /api/v1/resources/{type}/{name}/versions/{version}/content`。
- Submission 接口：`POST /api/v1/submissions`、`GET /api/v1/submissions/{submissionId}`、`GET /api/v1/submissions`。
- Remote Client 的认证头、metadata、内容类型、响应解析和错误映射。
- APM CLI composition 的测试环境默认地址与 `APC_BASE_URL` 覆盖规则。
- fake APC 契约测试、测试环境只读探测、允许时的真实 POST 探测与 APM 端到端验收。
- BUILD、Project Overview、相关 Feature/ADR 与 `docs/Changelog.md` 的行为契约同步。

### 2.2 Excluded

- APC 服务端、认证服务、数据库、队列或审核流程实现。
- 生产环境真实提交、自动重试、Bearer 回退、双凭据发送或静默协议兼容。
- Submission 取消、删除、重提、历史分页及新的 CLI 命令。
- 将测试环境中发现的服务端契约违规扩大为生产客户端接受范围。

## 3. Contract Mapping

| APC-API.md 接口 | APM 边界 | 验收要点 |
| --- | --- | --- |
| `GET /resources/version` | `cloud.CatalogSource.CatalogVersion` | 匿名；JSON schema v1；SHA-256/size 严格校验 |
| `GET /resources` | `cloud.CatalogSource.DownloadCatalog` | 匿名；302 同源重定向；原始 JSON 字节、长度与安全落盘 |
| `GET /resources/{type}/.../content` | `DownloadSkill/Plugin/Agent`、`GetMCP` | 匿名；精确身份、媒体类型、长度、MD5、404/410 映射 |
| `POST /submissions` | `cloud.Submission.Submit` | `X-API-Key`；multipart metadata+content；202 receipt；文档字段完整 |
| `GET /submissions/{id}` | `GetSubmission` | `X-API-Key`；完整 detail/timeline；身份一致 |
| `GET /submissions` | `ListSubmissions` | `X-API-Key`；仅 status/type/name 过滤；最多 20 条 active、倒序 |

## 4. Authentication and Request Wire

- `APC_API_KEY` 仅用于 Submission 三个接口。
- Submission 请求只发送 `X-API-Key: <key>`，不发送 `Authorization`，不把 key 写入日志、错误或测试 fixture。
- 公开目录、目录内容请求不携带任何认证头，即使同一 Remote Client 已配置 key。
- `POST /submissions` 保留现有 `Idempotency-Key` 以维持 APM 幂等边界；服务端是否支持该扩展不改变文档字段适配。
- multipart 固定两个 part：`metadata`（`application/json`，无 filename）和 `content`（按资源类型声明媒体类型并使用 `<name>-<version>` 文件名）。
- metadata 必须包含：`type`、`name`、`version`、`schema_version: 1`、`description`、`message`；除 MCP 外必须带 `content_md5`，MCP 按文档允许省略但 APM 现有完整性校验可继续发送并验证。
- 内容类型固定为 Skill/Plugin/Agent=`application/gzip`，MCP=`application/json`。Catalog 若返回其它类型，客户端 fail closed。

## 5. Response and Error Semantics

- 成功 JSON 严格限制大小，拒绝重复 key、尾随 JSON 值、缺失必填字段和非法时间线；未知字段可忽略以保持前向兼容。
- `202` 只接受 `status=received` 且非空 `submission_id/status_url`，映射 `SubmissionAccepted`。
- `200` Submission 响应按完整 detail 校验，并要求 replay 的 type/name/version/description 与请求一致，映射 `SubmissionReplayed`。
- `401/403` 映射 `cloud.ErrAuth`；`404` 映射 `ErrNotFound`；`410` 资源内容映射 `ErrYanked`；`503` 映射 `ErrUnavailable`；其它非 2xx 映射 `ErrInvalidResponse`。
- `409` 按稳定 code 映射：`ALREADY_PUBLISHED` 同时匹配 `ErrAlreadyPublished` 与 `ErrSubmissionConflict`；`TOO_MANY_ACTIVE_SUBMISSIONS` 同时匹配 quota 与 conflict sentinel；其它冲突匹配 `ErrSubmissionConflict`。
- `application/problem+json` 只提取稳定 `code`/`trace_id`；忽略 APC 的 `detail/title/type` 等私有或环境相关字段。
- 不因认证、网络或协议错误回退 Local、cache 或 stale snapshot；已有目录 stale 行为仅由 Catalog Manager 既有规则控制。

## 6. Runtime Configuration

- 默认 `APC_BASE_URL` 改为 `https://developer.bianbu.xyz`（测试环境根地址），仍可由环境变量覆盖。
- 保留当前 base URL 校验：绝对、无 credentials/query/fragment、根路径；非 loopback HTTP 拒绝，非回环服务必须 HTTPS。
- 默认 Remote HTTP Client 继续使用有界超时和同源重定向策略。
- `APM_CLOUD_PROVIDER=local` 行为不变；Local Submission 继续明确返回 `cloud.ErrSubmissionUnsupported`。

## 7. Verification Plan

### 7.1 Before integration

使用测试环境做只读探测：目录版本、目录重定向、最终 CDN 下载、无认证目录访问，以及 `X-API-Key` 与旧 `Authorization` 认证行为对比。探测命令临时允许跳过容器缺失的 CA 链，但产品代码不得关闭 TLS 校验。

### 7.2 Contract tests

更新/新增 fake APC 测试，覆盖：

- 所有匿名请求无 `X-API-Key`/`Authorization`；Submission 请求仅有正确 `X-API-Key`。
- metadata 含 `schema_version=1`、`message`、`content_md5`，资源类型与文件名/媒体类型映射正确。
- 202/200 成功形状、完整 detail/timeline、active list 限制、过滤条件及排序。
- 文档错误码、problem 响应、错误状态、重复 key、尾随值、缺字段和身份漂移。
- 四类资源目录/内容下载与现有安全、长度、MD5、yank 语义不回归。

### 7.3 After integration

执行 `go test ./...`、`go test -race ./...`、`go vet ./...`、隔离目录构建与 `git diff --check`。使用 `APC_BASE_URL=https://developer.bianbu.xyz` 验收公开目录和 APM `list/search/install`；使用批准的临时 key 尝试 Submission 查询/POST。若测试环境仍返回“需要 Authorization”或 key 无效，验收报告必须保留 HTTP 状态与安全错误上下文，并将真实 POST 标记为服务端阻塞。

## 8. Documentation and ADR

- 新增 ADR-025，记录“APC-API.md 为客户端权威契约、Submission 仅使用 X-API-Key、拒绝隐式 Bearer 回退”的决策，并在 BUILD、Project Overview、Feature 023/024 相关章节引用。
- 更新 APC-API.md 若发现其自身需要标注测试环境偏差，只做事实记录，不改变客户端目标契约。
- 行为实现完成后，在 `docs/Changelog.md` 以一次用户请求记录全部相关提交、验证结果和测试环境阻塞限制。

## 9. Acceptance Criteria

1. APC-API.md 七类接口均有 APM 对应调用路径和自动化测试。
2. 认证头、metadata schema、内容类型和错误映射符合本文与 APC-API.md。
3. 匿名资源请求无凭据泄露，Submission 不回退或双发认证。
4. APM 全量测试、race、vet、构建和静态差异检查通过。
5. 测试环境真实验收结果可复现；服务端认证或内容类型偏差被明确报告，不被客户端隐式吞掉。

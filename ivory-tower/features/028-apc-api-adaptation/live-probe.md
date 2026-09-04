# Feature 028 测试环境只读探测记录

> 本记录是 2026-09-03 对 `https://developer.bianbu.xyz` 的只读探测证据，只保留状态码、头字段名、内容类型与内容哈希，不含 API Key、Bearer token、私有响应详情或不稳定时间戳。容器缺少 `developer.bianbu.xyz` 的 CA 链，故探测命令使用 `curl -k` 跳过 CA 校验；产品 Go 代码保持 TLS 校验开启，未做任何关闭。

## 探测范围与方法

- 公开接口：`GET /resources/version`、`GET /resources`、`GET /resources/{type}/{name}/versions/{version}/content`。
- 认证接口：`GET /submissions`（无 key、`X-API-Key`、`Authorization: Bearer` 三种方式，均为占位 key，非真实凭据）。
- APM 端到端：用隔离 `APM_HOME`（mode 0700）运行 `apm list` / `apm search`。

## 结论摘要

| # | 发现 | 影响 |
| --- | --- | --- |
| 1 | 真实 APC 基址是 `https://developer.bianbu.xyz/api-server/apc/api/v1`（非根路径），而 APM 默认基址 `https://developer.bianbu.xyz` 加 `/api/v1/...` 会被网关以 `401 Full authentication is required` 拦截 | `apm list/search/install` 无法到达 APC 服务 |
| 2 | 测试环境 Submission 仍要求 `Authorization` 头，忽略 `X-API-Key`（`401 Authorization header is required`） | 真实 Submission GET/POST 被服务端认证阻塞 |
| 3 | 目录把 MCP 的 `content_type` 声明为 `application/gzip`，但 CDN 实际返回 `application/json` 的 JSON 字节 | APM 按 MCP=json 的严格校验会 fail closed |
| 4 | 目录与内容都 `302` 重定向到跨源 CDN `cdn-resource.spacemit.com` | APM 同源重定向策略会拒绝跨源 CDN |
| 5 | 服务端目录无顶层/分组 `schema_version`，含 `download_url` 字段，与 APM 严格 catalog schema 不一致 | `catalog.Decode` 会拒绝真实目录 |
| 6 | 容器缺少目标站点 CA 链 | 产品 TLS 校验正确拒绝（非产品缺陷） |

## 逐项证据

### 1. 路径前缀

- 代码默认路径：`GET /api/v1/resources/version` → `401`，体为 `{"code":50008,"message":"Full authentication is required to access this resource"}`。
- APC-API.md 文档路径：`GET /api-server/apc/api/v1/resources/version` → `200`，体为 `{"schema_version":1,"catalog_size":1686,"catalog_sha256":"9a2de00d913e2a5229b4c3804a528e5af8d50cd20de470ac732f82d47a67bfe6"}`。
- `APC_BASE_URL=https://developer.bianbu.xyz/api-server/apc/api/v1` 被 APM base URL 校验拒绝（非根路径），因此无法用环境变量指向真实服务。

### 2. Submission 认证

- 无 key：`401`，`application/problem+json`，`code=INVALID_API_KEY`，`detail="Authorization header is required"`。
- `X-API-Key: <占位>`：`401`，同样返回 `Authorization header is required`（服务端忽略 `X-API-Key`）。
- `Authorization: Bearer <占位>`：`401`，`detail="API Key不存在"`（服务端认可 Bearer 头格式，进入 key 校验）。
- 结论：测试环境尚未迁移到 `X-API-Key`；真实 Submission 探测按 spec 判定为服务端阻塞，未尝试真实 POST。

### 3. 目录内容类型

- 完整目录（`GET /resources`，302 → CDN）中 `mcps.items[].content_type` 为 `application/gzip`。
- 该 MCP 资源 content 重定向到 `.json` CDN 文件，最终响应 `200 application/json`（原始 JSON 字节）。
- 目录声明与真实内容媒体类型不一致；APM 对 MCP 严格期望 `application/json`，会对 `application/gzip` 声明 fail closed。

### 4. 跨源重定向

- `GET /resources` → `302`，`Location: https://cdn-resource.spacemit.com/file/apc/catalog/catalog-<时间戳>.json`。
- `GET /resources/skill/code-review-helper/versions/v1.0.0/content` → `302`，`Location: https://cdn-resource.spacemit.com/file/apc/resource/code-review-helper-1-v1.0.0.tar.gz`。
- APM Remote 客户端只允许同源重定向，会拒绝跨源 `cdn-resource.spacemit.com`。

### 5. 目录 schema 形状

服务端目录结构（摘录，已去除时间戳）：

```json
{
  "skills": { "items": [ { "name": "...", "schema_version": 1, "content_type": "application/gzip", "download_url": "...", "version": "v1.0.0", ... } ] },
  "plugins": { "items": [] },
  "mcps": { "items": [ { "name": "filesystem", "content_type": "application/gzip", "download_url": "...", "version": "v1.0.0", ... } ] },
  "agents": { "items": [] }
}
```

与 APM 严格 catalog schema 的差异：无顶层 `schema_version`；`schema_version` 位于 item 而非分组；item 额外含 `download_url`。`catalog.Decode` 会因此拒绝。

### 6. 内容哈希（可复现证据）

- skill `code-review-helper` content MD5：`0274020ce87b4b9c067786d8fdf342ef`（与目录 `content_md5` 一致，160 字节，`application/x-gzip`）。
- mcp `filesystem` content MD5：`ac771c6a1392087ccdb299d29820a2b5`（与目录 `content_md5` 一致，269 字节，`application/json`）。

### APM 端到端

- `APM_HOME=<隔离目录> apm list`（默认基址）→ 失败：`tls: failed to verify certificate: x509: certificate signed by unknown authority`（产品正确拒绝未知 CA，未关闭 TLS 校验）。
- `APC_BASE_URL=https://developer.bianbu.xyz/api-server/apc/api/v1 apm list` → 失败：base URL 校验拒绝非根路径。

## 适配过程中补充的发现

在按服务端实际行为适配（见 [ADR-026](../../adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)）并逐项验证时，又确认以下传输与内容形状差异：

| # | 补充发现 | 适配 |
| --- | --- | --- |
| 7 | CDN 经 HTTP/2 返回目录时无 `Content-Length`（`resp.ContentLength=-1`），HTTP/1.1 直连则有 `Content-Length: 1686` | 目录下载容忍无 `Content-Length`，以 `MaxCatalogSize` 为上界，最终大小/哈希由目录版本响应兜底 |
| 8 | 目录 MCP 条目缺少 `message` 字段 | MCP 条目 `message` 允许为空 |
| 9 | 目录条目 `published_at` 无时区（如 `2026-09-03T10:12:04`） | 时间戳校验接受 RFC3339 或无时区 ISO 8601 |
| 10 | MCP 资源 JSON 额外返回 `type`/`message`(null)/`content_md5`(null) | 容忍但不校验这三个字段 |
| 11 | Skill/Plugin/Agent 归档 CDN 返回 `Content-Type: application/x-gzip`，目录却声明 `application/gzip` | content 校验把 `application/x-gzip` 归一化为 `application/gzip` 后再比较，目录元数据仍要求 IANA 声明 |
| 12 | 归档用 GNU tar blocking factor 20 生成，解压后总长 10240 字节，tar 结束块之后还有 8192 字节 NUL 填充 | 解包校验容忍结束块后的纯 NUL 填充，拒绝非零尾随字节 |

MCP 资源文件实际内容（269 字节，与目录 `content_md5` 一致）：

```json
{"type":"mcp","name":"filesystem","version":"v1.0.0","description":"提供受限的文件系统访问能力","message":null,"definition":{"transport":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"]},"schema_version":1,"content_md5":null}
```

## 适配后的验证结果

以服务端证书（`*.bianbu.xyz`，签发者 `dc.com-CA`）作为信任锚设置 `SSL_CERT_FILE`（仅验证环境，产品代码仍开启 TLS 校验）后：

- `apm list` → 成功返回 `mcp/filesystem@v1.0.0`、`skill/code-review-helper@v1.0.0`、`skill/code-test-helper@v1.0.0`。
- `apm list -a` / `apm search filesystem` → 成功展示 description/tags。
- `remote.GetMCP(filesystem)` → 成功下载并校验（`application/json`、269 字节、MD5 一致），解码为 stdio MCP。
- `apm submission list`（占位 key）→ `401 code=INVALID_API_KEY`，证明 `Authorization: Bearer` 已被服务端识别并进入 key 校验。

容器缺少目标站点 CA 链（#6）仍属环境问题，未在客户端关闭 TLS 校验。

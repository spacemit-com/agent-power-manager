# APC (Agent Power Cloud) 对外接口文档

## 概述

APC 是 SpacemiT 的资源分发平台，支持开发者提交和分发 skill、plugin、mcp、agent 等资源。

**Base URL**：

- 测试环境：`https://developer.bianbu.xyz/api-server/apc/api/v1`
- 生产环境：`https://developer.spacemit.com/api-server/apc/api/v1`

> **网关前缀**：以上地址中的 `/api-server/apc` 是网关前缀，请求**必须完整携带**。不带前缀直接访问
> `/api/v1/...` 会被网关以 `401 {"code":50008,"message":"Full authentication is required to
> access this resource"}` 拦截；前缀拼错（如 `/api-server/aapc/`）返回 `50021 Access Token is
> missing or invalid`（2026-09-04 实测）。下文"接口"中的路径均为相对上述 base URL 的相对路径。

### 验证状态（2026-09-03/04 只读探测）

本文件 v1.2 依据对测试环境 `developer.bianbu.xyz` 的只读探测结果修订（探测记录见
`ivory-tower/features/028-apc-api-adaptation/live-probe.md`；客户端适配决策见 ADR-026，口径为
"服务端实际行为优先"）：

- §1 资源查询接口（目录版本、目录、内容下载）：已对测试环境实测，文档按实测修订。
- §2 资源提交接口：仅认证层实测（服务端当前要求 `Authorization: Bearer`，`X-API-Key` 未生效，
  见 §2 认证说明）；成功响应体与业务错误码分支未实测（无真实凭据），保留服务端实现文档口径并逐处标注。
- 生产环境 `developer.spacemit.com` 未探测，行为可能不同，以其自身实测为准。

---

## 一、资源查询接口（公开，无需认证）

### 1.1 获取目录版本信息

获取当前最新资源目录的版本摘要，客户端可用此判断本地缓存是否需要更新。

**接口**：`GET /resources/version`

**响应示例**（值为 2026-09-04 实测快照，仅作格式示例；`catalog_size`/`catalog_sha256` 随资源发布变化）：
```json
{
  "schema_version": 1,
  "catalog_size": 3177,
  "catalog_sha256": "d6d90460cf1f89f890066a36358db4cb7bfe0c6fd491fdf957e6ebc01a721588"
}
```

**字段说明**：
| 字段 | 类型 | 说明 |
|------|------|------|
| `schema_version` | int | 目录 Schema 版本，目前固定为 1 |
| `catalog_sha256` | string | 目录文件内容的 SHA-256 哈希（64 位小写十六进制） |
| `catalog_size` | long | 目录文件大小（字节） |

**异常响应**：
- `500` + ApcException `NOT_FOUND`：当前没有可用的目录版本（未实测，按实现文档）
- `500` + ApcException `INTERNAL_ERROR`：服务器内部错误（未实测，按实现文档）

---

### 1.2 获取完整资源目录

重定向到 CDN 上的最新目录 JSON 文件。

**接口**：`GET /resources`

**响应**：
- 成功：`302 Found`，`Location` 头指向 CDN 目录文件 URL（实测重定向到跨源 HTTPS CDN `cdn-resource.spacemit.com`）
- 无目录：`404 Not Found`（无响应体）（未实测，按实现文档）
- 失败：`500 Internal Server Error`（无响应体）（未实测，按实现文档）

**使用方式**：

客户端应跟随重定向下载 JSON 文件。目录顶层只有 `skills`/`plugins`/`mcps`/`agents` 四个分组
（每组含 `items` 数组），**无顶层 `schema_version`**。文件格式如下（2026-09-04 实测条目摘录，
保留服务端原始字段顺序）：
```json
{
  "skills": {
    "items": [
      {
        "schema_version": 1,
        "content_size": 160,
        "content_type": "application/gzip",
        "name": "code-helper",
        "download_url": "https://cdn-resource.spacemit.com/file/apc/resource/code-helper-1-v1.0.0.tar.gz",
        "description": "代码助手",
        "content_md5": "0274020ce87b4b9c067786d8fdf342ef",
        "message": "首次发布",
        "published_at": "2026-09-04T09:27:52",
        "version": "v1.0.0",
        "tags": ["featured", "official"]
      }
    ]
  },
  "plugins": { "items": [] },
  "mcps": {
    "items": [
      {
        "schema_version": 1,
        "content_size": 269,
        "content_type": "application/json",
        "name": "filesystem",
        "download_url": "https://cdn-resource.spacemit.com/file/apc/resource/mcp/filesystem-1-v1.0.0.json",
        "description": "提供受限的文件系统访问能力",
        "content_md5": "ac771c6a1392087ccdb299d29820a2b5",
        "published_at": "2026-09-03T10:12:04",
        "version": "v1.0.0",
        "tags": ["hot", "official"]
      }
    ]
  },
  "agents": { "items": [] }
}
```

**目录结构字段说明**：
| 字段 | 类型 | 说明 |
|------|------|------|
| `skills/plugins/mcps/agents` | object | 按资源类型分组，每组包含 `items` 数组（目录顶层无 `schema_version`） |
| `items[].name` | string | 资源名称 |
| `items[].schema_version` | int | Schema 版本，目前为 1（位于条目内，非分组/顶层） |
| `items[].version` | string | 最新版本号（格式 `vMAJOR.MINOR.PATCH`） |
| `items[].description` | string | 资源描述 |
| `items[].message` | string | 发布说明；**可选**，实测 MCP 条目缺失该字段（skill 条目含） |
| `items[].content_type` | string | 目录声明的媒体类型：Skill/Plugin/Agent 为 `application/gzip`，MCP 为 `application/json`（2026-09-04 实测；9-03 探测时服务端曾把 MCP 误声明为 `application/gzip`，现已修正）。实际下载响应的媒体类型可能与此不一致，见 1.3 |
| `items[].content_size` | long | 文件大小（字节） |
| `items[].content_md5` | string | 文件 MD5 |
| `items[].published_at` | string | 发布时间，实测为**无时区**本地时间（如 `2026-09-04T09:27:52`，无 `Z`/偏移量，非 RFC3339） |
| `items[].download_url` | string | 文件下载地址（CDN URL，实测条目含该字段） |
| `items[].tags` | string[] | 资源标签（可为空数组，用于客户端筛选） |

> 说明：每种类型只保留每个 `name` 的最新版本，客户端如需历史版本请使用接口 1.3。

> **传输注记**：目录 `302` 重定向到跨源 HTTPS CDN（`cdn-resource.spacemit.com`）；CDN 经
> HTTP/2 返回目录时可能不带 `Content-Length`，客户端不应依赖长度头（2026-09-03 实测）。

---

### 1.3 下载指定版本资源文件

重定向到该版本资源文件的 CDN 地址。

**接口**：`GET /resources/{type}/{name}/versions/{version}/content`

**路径参数**：
| 参数 | 说明 |
|------|------|
| `type` | 资源类型，枚举：`skill`、`plugin`、`mcp`、`agent` |
| `name` | 资源名称 |
| `version` | 版本号，格式 `vMAJOR.MINOR.PATCH`（如 `v1.0.0`） |

**请求示例**：
```
GET /resources/skill/code-review-helper/versions/v1.0.0/content
```
（完整 URL 为 base URL + 上述相对路径，如 `https://developer.bianbu.xyz/api-server/apc/api/v1/resources/skill/code-review-helper/versions/v1.0.0/content`）

**响应**：
- 成功：`302 Found`，`Location` 头指向 CDN 文件下载地址（实测重定向到跨源 HTTPS CDN `cdn-resource.spacemit.com`）
- 类型非法：`400 Bad Request`（无响应体）（未实测，按实现文档）
- 资源不存在：`404 Not Found`（无响应体）（未实测，按实现文档）
- 资源已下架：`410 Gone`（无响应体）（未实测，按实现文档）
- 服务器错误：`500 Internal Server Error`（无响应体）（未实测，按实现文档）

**说明**：
- `status = yanked` 的资源返回 `410 Gone`
- `status = published` 的资源正常重定向

**内容响应媒体类型（2026-09-04 实测）**：
- Skill/Plugin/Agent：跟随重定向后的最终响应为 `application/x-gzip`，与目录声明的
  `application/gzip` **不一致**（服务端已知偏差，2026-09-03/04 探测确认）。
  `application/x-gzip` 是 gzip 的历史别名，内容本身为合法 gzip 归档（size/MD5 与目录一致）。
- MCP：最终响应为 `application/json`，与目录声明一致（内容为 JSON 字节）。

---

## 二、资源提交接口（需要 API Key 认证）

### 认证说明

**认证方式（按测试环境实测，2026-09-03/04）**：提交接口当前通过 `Authorization: Bearer` 携带 API Key：

```
Authorization: Bearer your-api-key-here
```

> **迁移状态**：接口设计目标为 `X-API-Key: <key>`，但测试环境服务端尚未迁移。实测发送
> `X-API-Key` 会被忽略并返回 `401 INVALID_API_KEY`（`detail: "Authorization header is
> required"`）；发送 `Authorization: Bearer <key>` 才会进入 key 校验（key 无效时返回
> `detail: "API Key不存在"`）。调用方应按 Bearer 方式携带凭据；服务端迁移 `X-API-Key` 后本文档再按实测同步。

认证由 `ApcAuthInterceptor` 拦截器处理，成功后会在 request attribute 中设置以下字段，Controller 通过 `@RequestAttribute` 读取：
- `userId`：用户 ID
- `username`：用户名
- `userEmail`：用户邮箱
- `apiKeyId`：API Key ID

---

### 2.1 提交资源

提交资源元数据和文件内容，一次性完成。

**接口**：`POST /submissions`

**请求 Headers**：
```
Content-Type: multipart/form-data
Authorization: Bearer your-api-key-here
```

**请求 Parts**：
| Part 名称 | 类型 | 说明 |
|-----------|------|------|
| `metadata` | JSON 字符串 | 资源元数据（见下方字段说明） |
| `content` | 文件（二进制） | 资源文件 |

**metadata JSON 字段说明**：
| 字段 | 必需 | 类型 | 说明 |
|------|------|------|------|
| `type` | 是 | string | 资源类型：`skill`、`plugin`、`mcp`、`agent` |
| `name` | 是 | string | 资源名称，kebab-case 格式（小写字母、数字、连字符） |
| `version` | 是 | string | 版本号，格式 `vMAJOR.MINOR.PATCH`（如 `v1.0.0`） |
| `description` | 否 | string | 资源描述 |
| `message` | 否 | string | 本次发布说明 |
| `schema_version` | 是 | int | Schema 版本，目前固定为 1 |
| `content_md5` | mcp 可选，其余必需 | string | 文件 MD5 校验值（客户端预计算，服务端校验） |
| `definition` | mcp 必需，其余不携带 | object | MCP 启动定义（`transport` + `url` 或 `command`/`args`），与资源文件内 `definition` 完全一致 |

**metadata 示例**：
```json
{
  "type": "skill",
  "name": "code-review-helper",
  "version": "v1.0.0",
  "description": "代码审查助手",
  "message": "首次发布",
  "schema_version": 1,
  "content_md5": "0274020ce87b4b9c067786d8fdf342ef"
}
```

**MCP metadata 示例**：
```json
{
  "type": "mcp",
  "name": "nws",
  "version": "v1.0.0",
  "description": "通过 HTTPS 连接 nws 远程 MCP 服务",
  "message": "提交 nws v1.0.0",
  "schema_version": 1,
  "content_md5": "d9f294be184422726e05eb6bae66fa8c",
  "definition": {
    "transport": "http",
    "url": "https://nws.caseyjhand.com/mcp"
  }
}
```

> 说明：`definition` 仅 MCP 提交携带，且必须与 `content` part 中资源文件内的 `definition` 一致；
> 服务端据此在发布阶段还原资源文件。skill/plugin/agent 的 metadata 不携带该字段。

**content 文件格式**：
| 资源类型 | 文件格式 |
|----------|----------|
| `skill` | `.tar.gz` 压缩包 |
| `plugin` | `.tar.gz` 压缩包 |
| `mcp` | `.json` 文件 |
| `agent` | `.tar.gz` 压缩包 |

**文件大小限制**：最大 100MB（由 Spring multipart 配置控制）

**成功响应**：`202 Accepted`

```json
{
  "submission_id": "sub_01M1DS53ZE4H059CWQ9J",
  "status": "received",
  "status_url": "/apc/api/v1/submissions/sub_01M1DS53ZE4H059CWQ9J"
}
```

**字段说明**：
| 字段 | 说明 |
|------|------|
| `submission_id` | 提交 ID，格式 `sub_` 前缀 + ULID，用于后续查询 |
| `status` | 初始状态，固定为 `received` |
| `status_url` | 查询该提交状态的 URL 路径 |

**状态流转**：

```
received → validating → validation_failed（终态）
                     ↓
              review_pending → rejected（终态）
                            ↓
                         approved → publishing → published（终态）
                                              ↓
                                       publish_failed（可重试）
```

> 上述成功响应、状态流转与字段说明按服务端实现文档记载，未实测（真实提交需有效凭据；
> 2026-09-03/04 探测仅验证到认证层，未尝试真实 POST）。`status_url` 为服务端返回的查询路径，
> 客户端应直接使用该值拼接请求。

**异常响应**：

业务错误示例（按实现文档，未实测；认证错误为 `application/problem+json`，见第四节）：
```json
{
  "code": "VERSION_NOT_INCREMENTED",
  "message": "新版本 v0.9.0 必须大于已发布的最高版本 v1.0.0"
}
```

**错误码**：
| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| `INVALID_API_KEY` | 401 | API Key 无效或未提供（实测：无凭据或发送 `X-API-Key` 均返回该码，`detail` 分别为 `Authorization header is required` / `API Key不存在`；响应为 `application/problem+json`，见第四节） |
| `INVALID_PARAM` | 400 | 请求参数不合法（类型、名称、版本格式错误）（未实测，按实现文档） |
| `MD5_MISMATCH` | 400 | 文件 MD5 与 metadata 中声明的不一致（未实测，按实现文档） |
| `VERSION_PUBLISHED` | 409 | 该版本已发布，不可重复提交（未实测，按实现文档） |
| `VERSION_NOT_INCREMENTED` | 400 | 新版本号必须大于已发布的最高版本（未实测，按实现文档） |
| `RESOURCE_IN_PROGRESS` | 409 | 该资源已有其他版本正在处理中（处于 active 状态）（未实测，按实现文档） |

---

### 2.2 查询提交详情

查询指定提交的完整详情，包括状态和时间线。

**接口**：`GET /submissions/{submissionId}`

**路径参数**：
| 参数 | 说明 |
|------|------|
| `submissionId` | 提交 ID（从 2.1 接口返回） |

**请求 Headers**：
```
Authorization: Bearer your-api-key-here
```

**成功响应**：`200 OK`

```json
{
  "submission_id": "sub_01M1DS53ZE4H059CWQ9J",
  "type": "skill",
  "name": "code-review-helper",
  "version": "v1.0.0",
  "description": "代码审查助手",
  "message": "首次发布",
  "status": "published",
  "content_available": false,
  "validation_errors": [],
  "review_comment": "",
  "published_version": "v1.0.0",
  "created_at": "2026-09-01T14:05:27",
  "updated_at": "2026-09-01T14:22:32",
  "timeline": [
    {
      "event_id": "log_01M1DS53ZE4H059ABC01",
      "event_type": "received",
      "status_from": "",
      "status_to": "received",
      "timestamp": "2026-09-01T14:05:27",
      "message": "Submission received"
    },
    {
      "event_id": "log_01M1DS53ZE4H059ABC02",
      "event_type": "approved",
      "status_from": "review_pending",
      "status_to": "approved",
      "timestamp": "2026-09-01T14:22:31",
      "message": "Review approved"
    },
    {
      "event_id": "log_01M1DS53ZE4H059ABC03",
      "event_type": "published",
      "status_from": "publishing",
      "status_to": "published",
      "timestamp": "2026-09-01T14:22:32",
      "message": "Resource published successfully"
    }
  ]
}
```

**字段说明**：
| 字段 | 说明 |
|------|------|
| `submission_id` | 提交 ID |
| `type` | 资源类型 |
| `name` | 资源名称 |
| `version` | 版本号 |
| `description` | 资源描述 |
| `message` | 发布说明 |
| `status` | 当前状态（见状态流转图） |
| `content_available` | 原始文件是否仍可获取（终态后为 false） |
| `validation_errors` | 校验错误列表（校验失败时非空） |
| `review_comment` | 审核意见（被拒绝时非空，其余为空字符串） |
| `published_version` | 已发布版本号（发布成功后有值，否则为空字符串） |
| `created_at` | 创建时间（ISO 8601，实测为无时区本地时间，如 `2026-09-07T10:17:58`） |
| `updated_at` | 最后更新时间（ISO 8601，无时区，同上） |
| `timeline` | 状态变更时间线（按时间升序） |
| `timeline[].event_id` | 事件 ID |
| `timeline[].event_type` | 事件类型（received/validated/approved/rejected/published/publish_failed 等） |
| `timeline[].status_from` | 前一状态（初始事件为空字符串） |
| `timeline[].status_to` | 变更后状态 |
| `timeline[].timestamp` | 事件时间（ISO 8601，实测为无时区本地时间，同上） |
| `timeline[].message` | 事件说明（公开信息，不含内网链接或审核者信息） |

**异常响应**：
| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| `INVALID_API_KEY` | 401 | API Key 无效或未提供（实测码） |
| `SUBMISSION_NOT_FOUND` | 404 | 提交不存在（未实测，按实现文档） |
| `ACCESS_DENIED` | 403 | 提交不属于当前用户（未实测，按实现文档） |

---

### 2.3 查询我的提交列表

查询当前用户名下所有处于 **active 状态**的提交记录，按创建时间倒序，最多返回 20 条。

**接口**：`GET /submissions`

**请求 Headers**：
```
Authorization: Bearer your-api-key-here
```

**请求参数**：
| 参数 | 必需 | 说明 |
|------|------|------|
| `type` | 否 | 按资源类型过滤：`skill`、`plugin`、`mcp`、`agent` |
| `status` | 否 | 按状态精确过滤 |
| `name` | 否 | 按资源名称精确过滤 |

> 注意：本接口不支持分页，固定返回最近 20 条 active 记录（`received`、`validating`、`review_pending`、`approved`、`publishing`、`publish_failed` 状态）。终态记录（`published`、`rejected`、`validation_failed`）不在返回范围内。

**成功响应**：`200 OK`

```json
{
  "items": [
    {
      "submission_id": "sub_01M1DS53ZE4H059CWQ9J",
      "type": "skill",
      "name": "code-review-helper",
      "version": "v1.0.0",
      "status": "review_pending",
      "created_at": "2026-09-01T14:05:27"
    }
  ]
}
```

**items 字段说明**：
| 字段 | 说明 |
|------|------|
| `submission_id` | 提交 ID |
| `type` | 资源类型 |
| `name` | 资源名称 |
| `version` | 版本号 |
| `status` | 当前状态 |
| `created_at` | 创建时间（实测为无时区本地时间，如 `2026-09-01T14:05:27`） |

**异常响应**：
| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| `INVALID_API_KEY` | 401 | API Key 无效或未提供（实测码，见第四节） |

---

## 三、常见场景示例

### 场景 1：提交新资源

```bash
# 提交 metadata + 文件
curl -X POST https://developer.bianbu.xyz/api-server/apc/api/v1/submissions \
  -H "Authorization: Bearer your-api-key" \
  -F 'metadata={"type":"skill","name":"code-review-helper","version":"v1.0.0","description":"代码审查助手","message":"首次发布","schema_version":1,"content_md5":"0274020ce87b4b9c067786d8fdf342ef"}' \
  -F "content=@code-review-helper-v1.0.0.tar.gz"

# 响应 202 Accepted
{
  "submission_id": "sub_01M1DS53ZE4H059CWQ9J",
  "status": "received",
  "status_url": "/apc/api/v1/submissions/sub_01M1DS53ZE4H059CWQ9J"
}
```

### 场景 2：轮询提交状态

```bash
curl https://developer.bianbu.xyz/api-server/apc/api/v1/submissions/sub_01M1DS53ZE4H059CWQ9J \
  -H "Authorization: Bearer your-api-key"
```

### 场景 3：检查目录是否更新并下载

```bash
# 1. 获取版本摘要
curl https://developer.bianbu.xyz/api-server/apc/api/v1/resources/version

# 2. 若 catalog_sha256 与本地不同，下载最新目录（跟随重定向）
curl -L https://developer.bianbu.xyz/api-server/apc/api/v1/resources -o catalog.json

# 3. 按目录下载具体资源（跟随重定向）
curl -L "https://developer.bianbu.xyz/api-server/apc/api/v1/resources/skill/code-review-helper/versions/v1.0.0/content" \
  -o code-review-helper-v1.0.0.tar.gz
```

---

## 四、错误响应格式

实际观测到三种错误响应形态（2026-09-03/04 实测），按错误来源区分：

1. **网关层拦截**（前缀缺失/拼错，未到达 APC）：`application/json`，`code` 为数字：
```json
{"code":50008,"message":"Full authentication is required to access this resource"}
```

2. **Submission 认证错误**（实测）：`application/problem+json`（RFC 7807），字段为
   `code`/`detail`/`type`/`title`/`status`，`code` 为字符串（如 `INVALID_API_KEY`）：
```json
{
  "code": "INVALID_API_KEY",
  "detail": "Authorization header is required",
  "type": "https://apc.spacemit.com/problems/invalid-api-key",
  "title": "Authorization header is required",
  "status": 401
}
```

3. **业务错误**（未实测，按实现文档）：由 `ApcExceptionHandler` 统一处理：
```json
{
  "code": "错误码字符串",
  "message": "错误详情描述"
}
```

> 客户端应优先解析 2 的 `code`/`detail`；文档错误码表中 `INVALID_API_KEY` 已实测，
> 其余业务码未实测（见 §2.1/2.2 标注）。

---

**文档版本**：v1.2  
**最后更新**：2026-09-04  
**来源**：`ResourceController.java`、`SubmissionController.java`、`ivory-tower/features/028-apc-api-adaptation/live-probe.md`（2026-09-03/04 测试环境只读探测）、ADR-026

**v1.2 修订说明**：依据 2026-09-03/04 对测试环境 `developer.bianbu.xyz` 的只读探测，将认证方式
（`Authorization: Bearer`）、目录/内容媒体类型（MCP=`application/json`、Skill 下载实际响应
`application/x-gzip`）、`message`/`published_at` 字段格式、网关前缀行为与错误响应形态对齐实测；
§2 成功响应与业务错误码分支未实测，逐处标注为"按实现文档"。客户端适配决策见 ADR-026
（服务端实际行为优先于本文件）。

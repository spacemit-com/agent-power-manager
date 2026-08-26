# Feature 023：Submission 资源元数据与认证查询

> 局部取代说明：本文的 Submission message、完整 MCP v1 content、API Key 认证 get/list 与
> active-only 上限仍然有效；其中对公开资源分页、latest/exact metadata 继续存在的历史假设，
> 已由 [Feature 024](../024-catalog-cache/spec.md) 与
> [ADR-021](../../adr/ADR-021-cache-versioned-resource-catalog.md) 取代。Submission 请求本身仍不
> 使用 catalog stale fallback。

> 局部取代说明：本文保留 Feature 023 落定时的历史设计；其中 Agent 身份来源 `agent.id` 与
> content 为“已验证的原始 YAML”的约定，已由 [Feature 026](../026-agent-package-assets/spec.md) 与
> [ADR-023](../../adr/ADR-023-package-agent-with-assets.md) 取代（Agent 改为按 `agent.name` 身份提交的
> 完整目录资源，权威 content 为确定性 `.tar.gz` 归档，根含 `agent.yaml` 与可选 `assets/`），当前实现
> 与接口以 Feature 026/ADR-023 为准；本文的 Submission 查询、认证与元数据契约中未被修改的部分继续有效。

- Status：Accepted
- Date：2026-08-24
- Related ADRs：[ADR-020](../../adr/ADR-020-source-owned-submission-metadata.md)、APC [ADR-003](../../../../apc/ivory-tower/adr/ADR-003-bound-active-submission-query.md)

## 1. Goal

消除 Submission CLI 参数、Power 资源文件和 APC 元数据之间的重复来源：四类 Power 的身份与描述统一从资源文件读取，提交更新说明统一为 CLI `--message`。同时补齐认证 Submission 查询，将开发者列表收敛为当前 API Key 下最多 20 条 active Submission，并让详情完整呈现状态演进。

## 2. Scope

### 2.1 Included

- Skill、Plugin、MCP、Agent 的统一 Submission Source Loader。
- 删除 validate/submit 的 `--version`，删除 submit 的 `--change-summary`，新增统一必填 `--message`。
- MCP v1 完整资源 schema 以及内置 MCP fixture 迁移。
- Local/Remote MCP 读取完整资源、验证身份并返回 definition。
- APM domain 与 APC wire 的 `ChangeSummary/change_summary` 全链路重命名为 `Message/message`。
- API Key 认证的 active-only Submission list 和完整 get。
- APC 接口规范、APM/APC ADR、Project Overview、BUILD 与 C4 更新。

### 2.2 Excluded

- APC 服务端、数据库、队列或官网历史查询的实现。
- Agent Adapter、Cache、Manifest、Journal、Transaction 与安装 lifecycle 的重构。
- 旧 CLI、旧 MCP JSON 或 `change_summary` 的兼容层和自动迁移。
- Submission 取消、删除、重提、自动等待、历史分页或审核操作。

## 3. Command Contract

```bash
apm validate <path> --type <skill|plugin|mcp|agent> [--json]

apm submit <path> \
  --type <skill|plugin|mcp|agent> \
  --message "本次提交说明"

apm submission list \
  [--status <active-status>] \
  [--type <skill|plugin|mcp|agent>] \
  [--name <canonical-name>] \
  [--output text|json]

apm submission get <submission-id> [--output text|json]
```

规则：

- `validate` 不接收 version 或 message，只验证资源自身。
- `submit --message` 对四类 Power 均必填；trim 后为空即报错。
- `--version` 与 `--change-summary` 均为未知参数，不保留弃用期。
- `--output` 仅接受 `text` 或 `json`，默认 `text`。
- list status 仅接受 active 状态；type/name 在发起网络请求前按 APM canonical 规则校验。
- Submission 命令要求 `APC_API_KEY`；Key 只放在 `Authorization: Bearer`，不得记录或输出。

## 4. Source Loader Architecture

`internal/submission` 新增内部协议：

```go
type sourceLoader interface {
    Load(context.Context, string) (source, error)
}

type source struct {
    Validation  Validation
    ContentType string
    Build       func() ([]byte, error)
}
```

公共入口：

```text
Validate(Type, Path)
  -> 查找 Loader
  -> Load 并完整离线校验
  -> 返回身份、统计和 warnings

Prepare(Type, Path, Message)
  -> 校验 Message
  -> 查找 Loader 并 Load
  -> Build 确定性 content
  -> 校验 content <= 20 MiB
  -> 计算原始 content MD5
  -> 返回 Content
```

每个 Loader 的边界：

| Loader | 权威文件 | 身份来源 | content |
| --- | --- | --- | --- |
| Skill | `SKILL.md` frontmatter | name/version/description | deterministic tar.gz |
| Plugin | `apc-plugin.json` | name/version/description | deterministic tar.gz |
| MCP | `MCP.json` | name/version/description/definition | 完整资源 JCS JSON |
| Agent | `agent.yaml` | agent.id/agent.version/agent.description | 已验证的原始 YAML |

Loader 注册表必须显式覆盖四类 Power；未知类型 fail closed。目录名不得补全或覆盖权威字段。Plugin 与 Agent 必须复用已有权威 parser；MCP 使用新增 domain parser。重要入口添加简短中文注释说明来源和 fail-closed 约束。

## 5. MCP Resource Schema

stdio 示例：

```json
{
  "schema_version": 1,
  "name": "filesystem",
  "version": "v1.2.0",
  "description": "提供受限的文件系统访问能力",
  "definition": {
    "transport": "stdio",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem"]
  }
}
```

HTTP 示例：

```json
{
  "schema_version": 1,
  "name": "company-tools",
  "version": "v1.0.0",
  "description": "公司内部工具",
  "definition": {
    "transport": "http",
    "url": "https://mcp.example.com/mcp"
  }
}
```

`domain.DecodeMCPResourceJSON` 必须拒绝重复 key、未知字段、多个/尾随 JSON 值和非 v1 schema。name 使用 APC canonical name；version 必须为严格 `vMAJOR.MINOR.PATCH`；description trim 后非空；definition 继续执行既有 stdio/http 互斥和秘密字段拒绝规则。

Local/Remote `GetMCP(ref)` 必须验证文件内 type 固定为 MCP 且 name/version 与 ref 完全一致，再返回 `Definition.Clone()`。因此现有 Cloud/Adapter 端口不因 schema 变化而扩散。

## 6. Submission Domain and Wire

请求模型：

```go
type SubmissionRequest struct {
    Type           PowerType
    Name           string
    Version        string
    Description    string
    Message        string
    ContentType    string
    Content        []byte
    ContentMD5     string
    IdempotencyKey string
}
```

multipart 固定包含 metadata 与 content 两个 part；metadata 使用 `message`。Skill/Plugin content 是 gzip，MCP 是完整资源 JSON，Agent 是 YAML。`message` 不写入 content，不参与 MD5。已发布 ResourceMetadata 同样使用 `message`，不再出现 `change_summary`。

幂等与创建顺序由 APC 契约固定为：认证与请求校验、Idempotency-Key 检查、相同活动内容重放、已发布内容/版本冲突、同系列活动申请冲突、账号 active 数量检查、创建。相同活动内容重放不新增名额；已发布同内容返回 `409 ALREADY_PUBLISHED`。

## 7. Authenticated Query Contract

### 7.1 Active status

active 状态只包括：

- `received`
- `validating`
- `review_pending`
- `approved`
- `publishing`

`validation_failed/rejected/publish_failed/published` 不计入 20 条，也不出现在 list；仍可通过已知 ID get。

### 7.2 List

```http
GET /api/v1/submissions?status=review_pending&type=skill&name=demo
Authorization: Bearer <official-api-key>
Accept: application/json
```

响应：

```json
{
  "items": [
    {
      "submission_id": "sub_01J...",
      "type": "skill",
      "name": "demo",
      "version": "v1.0.0",
      "status": "review_pending",
      "created_at": "2026-08-24T10:00:00Z"
    }
  ]
}
```

APC 只返回当前认证 user ID 的记录，最多 20 条，按 created_at 倒序。不定义 limit、cursor、created_before、created_after 或 next_cursor。筛选由 APC 执行；APM 必须验证返回项满足筛选。

### 7.3 Get

详情包含 `submission_id/type/name/version/description/message/status/created_at/updated_at/content_available/validation_errors/review_comment/published_version/timeline`。

timeline 项包含 `event_id/event_type/status_from/status_to/timestamp/message`，按时间升序。APM 原样呈现，不重排、不合并、不推导；允许未来未知 JSON 字段，但拒绝重复 key 和缺失必填字段。读取其他账号 ID 返回 `404 NOT_FOUND`，不得泄露存在性。

### 7.4 Remote validation

Remote Client 必须：

- 为 Submission get/list 单独构造认证 GET；匿名 catalog/metadata/content 请求绝不附带 Key。
- 拒绝缺失 items、超过 20 条、重复 ID、终态 list 项、非法身份/版本/时间。
- 拒绝不满足请求筛选或 created_at 非递增的 list。
- get 校验响应 ID 与请求一致，timeline 时间非递减。
- 使用严格 JSON 单值/重复键检查，同时忽略未知响应字段以保持前向兼容。
- 认证、协议或网络错误不得回退 Local、缓存或快照。

## 8. Quota and Errors

APC 对每个官方 API Key 所属 user ID 事务性限制最多 20 条 active Submission。只在确实创建新记录时计数。达到上限返回：

```http
409 TOO_MANY_ACTIVE_SUBMISSIONS
```

APM 映射为 `cloud.ErrTooManyActiveSubmissions`，并同时匹配 `cloud.ErrSubmissionConflict`。CLI 提示用户先运行 `apm submission list`。缺少或无效 Key 返回 `401 INVALID_API_KEY`；认证服务不可用返回 `503 AUTH_SERVICE_UNAVAILABLE`。

## 9. CLI Rendering

- list 文本每行显示 `id type/name@version status created_at`；空列表输出 `No active submissions.`。
- list JSON 输出 `{"items":[]}` 形状，不虚构分页字段。
- get 文本按身份、description、message、状态、时间、内容可用性、校验/评审结果、timeline 顺序显示。
- get JSON 保留全部 domain 字段并使用 APC snake_case 命名。
- API Key 不进入日志、错误上下文或输出。

## 10. Repository Boundary

APM 实现 domain、Source Loader、CLI/lifecycle、Local/Remote Cloud、fixtures 和测试。APC 仓库本次只更新 Feature spec、ADR、API、Project Overview 与 C4；不创建尚不存在的服务端、数据库或队列实现。Local provider 的 Submission 三方法继续返回 `ErrSubmissionUnsupported`。

## 11. File Layout

主要生产文件：

- `internal/domain/{mcp,submission}.go`
- `internal/submission/{source,result,submission,skill,plugin,mcp,agent}.go`
- `internal/cli/{flags,validate,submit,submission,submission_contract}.go`
- `internal/lifecycle/{validate,submit,submission_query}.go`
- `internal/cloud/{submission,errors}.go`
- `internal/cloud/{local,remote}/mcp.go`
- `internal/cloud/remote/{submission,catalog}.go`
- `internal/cloud/resources/mcps/**/MCP.json`

不修改 Adapter、Cache、Config、Transaction 和 install/uninstall lifecycle 的生产逻辑。

## 12. Testing and Acceptance

- 四类 Loader 的有效、缺字段、非法身份和无 CLI 覆盖测试。
- MCP 新旧 schema、stdio/http、重复/未知字段、身份不匹配测试。
- Local/Remote/Submission 对同一 MCPResource 的一致性测试。
- CLI 旧 flag 拒绝、message 必填、text/json 输出测试。
- query Bearer 发送与匿名资源不泄露 Key 测试。
- list 最大 20、active-only、筛选、顺序、重复 ID 与严格 JSON 测试。
- get 完整字段、timeline、所有权 404 和前向兼容测试。
- TOO_MANY_ACTIVE_SUBMISSIONS 的具体与通用 error matching 测试。
- 测试只使用 `httptest`、`t.TempDir()` 与 fixture，不访问真实 APC、Agent CLI 或用户目录。

完成门禁：`gofmt`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build ./cmd/apm`、`git diff --check`；另外校验 APM/APC Markdown、Mermaid/Structurizr 和过期术语扫描。功能全部完成后只写一条 Changelog。

# Submission 与本地校验设计规格

> 状态：设计已确认，待实现。重大决策将记录在 ADR-018（实现时创建）。上游契约见相邻 APC 仓库的 `project-overview.md` 与 `project-overview-apm.md`。

> 扩展说明：Agent Submission 由 [Feature 022](../022-agent-submission/spec.md) 与 [ADR-019](../../adr/ADR-019-align-agent-submission-lifecycle.md) 扩展，使 `--type agent` 进入与 Skill/Plugin/MCP 相同的统一 Submission 管线。本文主体保留历史的三类范围（Skill、Plugin、MCP），其中的「三类」措辞仅描述本 feature 落定时的范围，不作为当前四类能力的权威描述。

## 背景

APC 定义了资源提交（Submission）工作流，以及 APM/APC 的校验职责边界。本 feature 在 APC 无实体（仅以设计约束 APM）的前提下，实现 APM 侧的提交能力：离线完整校验、确定性打包、统一 Submission 上传、本人 Submission 查询，以及 Cloud Client 的 Submission 接口。

本 feature 同时收敛 APC 的最低复验职责（见「APC 设计变更」），并把「确定性打包后不再解包复核」落实为受信产物约定。

## 目标

- 新增 `apm validate`、`apm submit`、`apm submission get|list` 命令。
- 对 Skill、Plugin、MCP 执行完整、可快速演进的本地格式校验，validate 与 submit 复用同一套规则，submit 在任何网络请求前拦截本地 error。
- 把 Skill/Plugin 原始目录确定性打包为 `.tar.gz`，把 MCP definition 规范化为确定 JSON 字节，并计算 MD5。
- `internal/cloud.Client` 增加 Submission 端口；Remote 完整实现，Local 空实现便于测试。
- 通过 `APC_API_KEY` / `APC_BASE_URL` 环境变量配置凭据与地址，不引入用户级配置文件。
- 复用 APC 的 `application/problem+json` 错误模型，向用户展示稳定 `code` 与 `trace_id`。

## 不在范围内

- APC 服务端、数据库、对象存储、发布管线、工单网站或内网审批接口的实现。
- Agent 的 Submission、审核、发布与管理 API；本次只消费 APC 已发布 Agent 的匿名只读接口。
- 用户级配置文件、API Key 创建/轮换/撤销、账号管理。
- `/resources/version` 目录缓存、离线目录回退、submit 自动重试、`--wait`、批量提交、开发者撤销。
- SHA-256 等强摘要；MVP 继续使用 MD5。

## 命令设计

### validate

```bash
apm validate <path> --type skill|plugin|mcp [--version vMAJOR.MINOR.PATCH] [--json]
```

- 完全离线，不读取 API Key、不访问 APC。
- Skill 未提供 version 时只校验资源内容；version 是提交元数据，不写入 Skill 文件。
- 成功输出解析出的 type、name、description、文件数和预计内容大小；失败输出稳定错误码、相关路径和修复建议。
- `--json` 提供机器输出，供编辑器和 CI 使用。
- 复用与 submit 完全相同的校验规则。

### submit

```bash
apm submit <path> --type skill|plugin|mcp [--version vMAJOR.MINOR.PATCH] --change-summary "..."
```

字段来源：

| 字段 | 来源 |
| --- | --- |
| Skill name/description | `SKILL.md` frontmatter |
| Plugin name/version/description | `apc-plugin.json` |
| MCP 全部字段 | 本地 MCP JSON 文件 |
| Skill version | 必填 `--version` |
| Plugin/MCP version | `apc-plugin.json` / MCP 文件 |
| change_summary | 必填 `--change-summary`（MCP 从文件读取，不接受命令行覆盖） |
| content_md5 | 确定性打包 / 规范化后计算 |

- Skill/Plugin 只接受原始目录，不接受现成 `.tar.gz`；MCP 只接受单个完整 MCP JSON 文件；不接受服务器路径、远程 URL 或 stdin 中的不透明归档。
- 校验通过后：确定性打包（Skill/Plugin）或 JCS 规范化（MCP）→ 计算 MD5 → 调 `Submission.Submit`。
- 收到 `202 Accepted` 后立即输出 `submission_id`、`status: received` 和 `track` 提示，不等待异步验收、不提供 `--wait`。
- 每次逻辑提交生成一个 UUID 作为 `Idempotency-Key`；不自动重试网络错误、超时、429 或 5xx。

### submission get/list

```bash
apm submission get <submission-id>
apm submission list [--status ...] [--type ...] [--name ...] [--limit ...]
```

- 只显示当前官网 user ID 有权读取的 Submission。
- `get` 展示当前状态、公开校验错误、公开评审意见、时间线和 content availability。
- `list` 默认按创建时间倒序，支持服务端筛选与 cursor 分页；渲染时保持服务端状态原样，不自行推断发布结果。

## 完整本地校验

校验逻辑落在新包 `internal/submission`，validate 与 submit 共用，error 与 warning 明确区分，不提供 `--force` 或隐藏绕过方式。

### 通用元数据

- type 只能是 skill、plugin、mcp。
- name 复用 `domain.IsCanonicalPowerName`（1–64 字节小写 kebab，无连续连字符）。
- version 严格为 `vMAJOR.MINOR.PATCH`，复用现有 semver 校验。
- description 与 change_summary 非空；不自动修改、清洗或补全用户字段。
- APM 无法离线判断版本是否严格高于历史最高版本或是否存在活动申请，这些冲突由 APC 返回 `409`。

### 目录与文件安全

在打包前对源目录执行，拒绝：符号/硬链接、设备文件、FIFO、socket 等特殊文件；绝对路径、路径逃逸与资源根外引用；ELF/PE/Mach-O/WebAssembly/共享库/目标文件等可执行二进制（按内容特征检测，不看扩展名）；任意嵌套压缩包；超过 5.6 阈值的大小、文件数、深度、路径长度与解压倍率。允许文本脚本及其执行位，但本地校验不执行任何资源文件。

### Skill

- 根目录直接存在普通文件 `SKILL.md` 与 `skill-card.md`。
- `SKILL.md` 从首字节开始使用 YAML frontmatter，frontmatter 为 mapping，有唯一、非空、字符串类型的 name 与 description。
- `skill-card.md` 一级标题为 `# Skill Card: <显示名称>`，必须填写 Capability、Use Cases、Inputs and Outputs、Dependencies、Limitations、Maintainer 六个章节。
- Maintainer 包含非空 Owner 与联系/支持路径；拒绝 `REPLACE_OWNER`、`REPLACE_CONTACT` 及明显模板占位内容。

### Plugin v1

- 严格解析根目录 `apc-plugin.json`（拒绝未知 schema version、重复键、未知字段与尾随 JSON 值），复用 `pluginartifact` 已有的解析与 Skill 闭集校验，并扩展出「校验 + 打包」用途。
- name/version/description 通过通用规则；skills 为非空、安全相对目录数组；每个路径位于资源根内且含有效 `SKILL.md`/ `skill-card.md`；内嵌 Skill 名称唯一；`skills/` 下无未声明 Skill 目录。
- Plugin v1 拒绝 commands、hooks、agents、内嵌 MCP 与任何 Agent 原生 manifest。

### MCP

- 严格拒绝未知字段、重复键与尾随 JSON 值，复用 `domain.DecodeMCPServerJSON`。
- stdio：非空 command、可选字符串 args、禁止 URL；http：绝对 HTTPS URL、禁止 command/args 与 URL userinfo。
- 禁止 env、headers、token、password、cookie 等秘密字段。
- 提取 `definition`，按 RFC 8785 JCS 生成 UTF-8 content 字节；MD5 与上传针对规范化结果，不是原始本地文件字节。

### 校验结果

本地 error 必须在读取 API Key、认证或上传之前终止。warning 可以继续提交，但完整校验报告不上传 APC，也不把「本地通过」描述为发布保证。

## 确定性打包

Skill/Plugin 校验通过后，从原始目录生成 `.tar.gz`；相同目录内容必须产生相同字节与 MD5。

- 不包含用户目录的外层名称，归档根就是资源根。
- 条目按规范化相对路径稳定排序；固定 uid/gid/mtime 与其它非语义归档字段。
- gzip 头不包含当前时间或本机文件名。
- 保留普通文件必要读写权限与文本脚本执行位；清除 setuid/setgid/sticky 等危险位。
- 不加入归档之外的临时文件。
- 计算原始 `.tar.gz` 字节的 32 位小写十六进制 MD5。

**受信产物约定**：提交方向的 `.tar.gz` 由 APM 确定性生成，属于受信产物；校验在压缩前对源目录完成，打包后不再解包复核。这与下载方向形成对称——下载收到的是不可信压缩包，必须「MD5 复验 + 安全解包」；提交方向的压缩包只需「源目录校验 → 确定性打包 → MD5」。

## MCP JCS 规范化

按 RFC 8785 JSON Canonicalization Scheme 对 `definition` 生成确定字节：对象键按 UTF-16 码元升序、字符串使用最短转义（仅 `\\` 与 `\"`）、数字使用 ECMAScript 数字序列化、无多余空白。采用手写实现并对照 RFC 8785 测试向量验证，不引入第三方依赖。

## cloud.Submission 接口

`internal/cloud` 新增 Submission 端口并嵌入 `Client`：

```go
type Submission interface {
    Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error)
    GetSubmission(context.Context, string) (domain.Submission, error)
    ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error)
}

type Client interface {
    Catalog
    SkillSource
    PluginSource
    MCPSource
    AgentSource
    Submission
}
```

### Remote 实现

- `Submit`：`POST /api/v1/submissions`，multipart 两 part（`metadata`：`application/json`，`content`：`application/gzip` 或 `application/json`），携带 `Authorization: Bearer`、`Idempotency-Key` 与 `Accept`。
- `GetSubmission`：`GET /api/v1/submissions/{id}`。
- `ListSubmissions`：`GET /api/v1/submissions`，按 status/type/name/时间范围筛选，cursor 分页。
- 复用现有 `safeProblemContext` 提取稳定 `code` 与 `trace_id`；把 401/409/413/422/429/503 稳定映射为可读错误，不解析自然语言 detail。
- 响应 DTO 是 remote 私有传输类型，不扩张 lifecycle 使用的领域类型。

### Local 实现

Local 实现同一接口但三个方法均为空实现，仅为让 Local 通过 `cloud.Client` 的编译契约与 contract test；不实现上传与查询，不访问网络。建议返回 `cloud.ErrSubmissionUnsupported` 哨兵错误（fail loud），而非零值成功；实现时按此落定并写注释说明。

## 领域类型

`internal/domain/submission.go` 新增：

- `SubmissionStatus`：received、validating、validation_failed、review_pending、rejected、approved、publishing、publish_failed、published。
- `SubmissionRequest`：Type、Name、Version、Description、ChangeSummary、ContentType、Content（字节）、ContentMD5、IdempotencyKey。
- `SubmissionReceipt`：SubmissionID、Status、StatusURL。
- `Submission`：身份、状态、公开校验错误、公开评审意见、时间线、content availability 与发布结果（字段集对齐 APC §13.3）。
- `SubmissionListQuery` 与 `SubmissionList`：筛选与 cursor 分页。

## 配置

- `APC_API_KEY`：Bearer 凭据；submit 与 submission get/list 需要，资源读取与下载不需要；日志/错误/verbose 不打印完整 Key。
- `APC_BASE_URL`：复用现有默认值 `http://127.0.0.1:8080` 与 remote 的 base URL 校验（非回环必须 HTTPS）。
- 不引入用户级配置文件；未配置 Key 时返回错误并引导用户前往官网获取。

## 错误模型

- 客户端逻辑依赖 HTTP 状态与稳定 problem code，不解析 detail 文本。
- 用户输出包含可执行的修复建议、submission ID 与 trace ID。
- 401 引导检查官网 Key；503 说明官网认证或 APC 依赖不可用；409 区分活动申请、版本、幂等与审批状态冲突；413/422 不重试并说明本地为何未提前发现。
- 忽略未知可选字段，但不忽略未知状态或改变语义的错误码。

## 中断与临时文件语义

- validate/submit 只读遍历源目录，不写任何文件。
- 确定性打包与 MCP JCS 全程流式写入内存 buffer：源文件读入 → gzip/tar 或 JCS 写进内存，同一份字节计算 MD5，再作为 multipart body 上传。因打包前已按 5.6 拦截超限内容，产物必然 ≤ 20 MiB，内存占用有界。
- submit/validate 不进入 `internal/transaction` 的 journal 体系（不修改本地已安装状态，无回滚/恢复需求），也不复用 `internal/cache`（安装下载缓存）。进程被 Ctrl-C 或崩溃中断时无孤儿缓存文件，无需下次启动清扫。
- 上传阶段中断不自动重试、不留本地残留；用户重跑 submit 时由服务端 `Idempotency-Key` + 同资源活动申请规则去重，返回同一 submission ID。
- 兜底约定（当前 20 MiB 范围用不到）：若将来必须落盘，沿用现有 staging 约定——`os.MkdirTemp` 落在 `<state>/.tmp/` 或 `<cache>/.tmp/`，`defer os.RemoveAll` 清理，SIGKILL 孤儿由下次命令启动清扫已知 `.tmp` 根下的过期条目。

## 测试

- 校验规则：Skill 缺 `skill-card.md`、Plugin 引用未声明 Skill、含 ELF/符号链接/嵌套压缩包、MCP 含 env/明文 HTTP/未知字段均本地失败，且不可绕过。
- 确定性打包：同一目录连续打包字节一致；修改任一普通文件后 MD5 改变；跨字段顺序/无意义空白不变（MCP JCS）。
- JCS：对照 RFC 8785 测试向量。
- Submission 客户端：httptest 覆盖 202、401、409、413、422、429、503 与 problem code/trace_id 解析；Idempotency-Key 与 multipart part 断言。
- CLI 接线：沿用 `APM_HOME` / fake-bin 隔离约定；validate/submit 在无网络环境下完成本地阶段。
- contract test：Local 与 Remote 均实现完整 `cloud.Client`，含 Submission 端口。

## 验收场景

- Skill 缺 `skill-card.md` 时 validate 与 submit 在网络前失败。
- 三种类型统一走 `POST /api/v1/submissions` 与相同 multipart part 名称。
- submit 收到 202 后立即返回 submission ID，不等待、不自动重试、不上传校验报告。
- 无效 Key、官网不可用、活动申请、版本冲突分别显示明确错误。
- 中断提交不产生本地缓存/临时文件残留。

## APC 设计变更

本 feature 落地时同步更新 APC 设计文档，记录以下两点：

1. APC 最低复验收敛：保留人工审批流程，APC 仅执行「元数据语法复验 + content MD5 + 文件格式可解析（不解包）+ 上传大小 ≤ 20 MiB」，其余（内容结构、路径/二进制/嵌套、解压阈值）全部收归 APM。
2. 提交方向 `.tar.gz` 由 APM 生成，属受信产物；删除「打包后再次检查归档条目」要求，打包后不解包复核。

# Agent Submission 与 Lifecycle 对齐设计规格

> 状态：设计已确认，待实施计划。APM 的客户端决策将在 ADR-019 记录；APC 的业务治理决策将在相邻 APC 仓库的 ADR-001 记录。
>
> 本文扩展 [Feature 021](../021-submission-validate/spec.md) 与 [ADR-018](../../adr/ADR-018-submission-and-local-validation.md)，不改写其历史设计。APC 权威业务与 HTTP 契约位于相邻 `../apc` 仓库的 `ivory-tower/project-overview.md`、`ivory-tower/project-overview-apm.md` 与 `docs/APC-API.md`。

## 背景

APM 已把 Agent 作为第四种 Power，支持 search、list、install、uninstall 与 fix，并通过 Local/Remote Cloud provider 获取版本化的原始 `agent.yaml`。Feature 021 随后为 Skill、Plugin、MCP 增加 validate、submit 与 Submission 查询，却明确排除了 Agent；APC 文档也只定义了 Agent 的匿名只读入口，把写入、审核、发布和管理留作后续。

本 feature 让 Agent 进入与其它 Power 相同的 Submission 和治理流程，同时保持其 Bibit 专属安装语义。它还修复两个与本次对齐直接相关的既有缺口：Agent 安装内容与 `PowerRef` 尚未形成身份闭环，以及 APM `Submit` 不能正确表达 APC 的 `202` 首次受理与 `200` 幂等重放。

## 目标

- `apm validate` 与 `apm submit` 支持 `--type agent`。
- Agent Submission 输入为单个原始 `agent.yaml`，不打包、不重序列化。
- name、version、description 分别来自 `agent.id`、`agent.version`、`agent.description`；change summary 来自必填 CLI 参数。
- validate、submit、缓存、Bibit Adapter 与 Agent 恢复复用同一套最小稳定 YAML 解析和身份校验规则。
- Agent 纳入 APC 的 Submission、人工审核、不可变发布、版本治理、下架与恢复流程。
- Remote Cloud Client 支持 Agent multipart，并严格区分首次受理与幂等重放；Local Cloud Client 继续只读。
- 同步更新 APM/APC 设计文档、C4 与 APC HTTP 接口规范。

## 不在范围内

- APC 服务端、数据库、对象存储、任务调度、内网网站或邮件实现。
- Submission cursor、创建时间筛选、详情 timeline 或其它查询能力增强。
- Local provider 的本地 Submission、工单状态机或持久化模拟。
- Agent 适配 Codex、Claude Code 或 Hermes；Agent Power 仍只适用于 Bibit。
- 严格解释 `agent.yaml` 的 model、tools、skills、mcps、hooks、metadata 等扩展字段。
- 自动删除、修复或回源覆盖损坏的 Agent 持久缓存。
- Submission 自动重试、`--wait`、批量提交、撤销或强摘要迁移。
- 将全部 Submission 类型重构为注册式 handler 框架。

## 核心决策

### 统一管线而非独立 Agent API

Agent 复用现有 `submission.Validate`、`submission.Prepare`、`cloud.Submission` 与 `/api/v1/submissions`。类型专属规则放在聚焦的 `internal/submission/agent.go`，不新增 `SubmitAgent`、Agent 专属 endpoint 或平行状态机。

该方案保持四类 Power 并列，改动聚焦，也不会为了尚不存在的第五种类型提前重构已稳定的 Skill、Plugin、MCP 管线。

### APM 独占 Agent 业务校验

Agent 的结构、必填字段和内容身份均由 APM 校验。APC 不读取或比较 `agent.yaml` 的业务字段，只保留与其它类型一致的传输级最低验收：metadata 通用语法、Content-Type、非空和大小、原始字节 MD5，以及 YAML 可解析。

APC 原样保存并发布收到的 YAML 字节，不清洗、不规范化、不重序列化。APM 是开发体验和内容一致性的实现边界，但不是服务端安全证明；绕过 APM 的请求仍会经过 APC 最低验收和人工审核。

### Local provider 继续只读

Remote provider 完整实现 Submission；Local provider 只提供 canonical fixture 的 list、resolve 与 download，`Submit`、`GetSubmission`、`ListSubmissions` 对所有类型继续返回 `cloud.ErrSubmissionUnsupported`。离线 validate 不依赖 provider。

### 已发布内容使用稳定冲突

APC 成功语义只有：

- `202 Accepted`：首次接收，响应 `SubmissionReceipt`；
- `200 OK`：相同账号、相同 Idempotency-Key、相同请求的幂等重放，响应当前 `Submission` 详情。

已发布同标识且同 MD5 不再作为 `200` 成功返回，而是 `409 ALREADY_PUBLISHED`。同标识同版本但 MD5 不同返回 `409 RESOURCE_VERSION_CONFLICT`。

## Agent YAML 领域契约

`internal/domain` 是 APM 对 Agent 稳定 schema 的唯一权威。共享解码器必须：

- 只接受单个 YAML document；拒绝第二个 document 或尾随 document；
- 拒绝重复 mapping key；
- 要求根为 mapping、`agent` 为 mapping；
- 要求 `schema_version` 为整数且等于 `1`；
- 要求 `agent.id`、`agent.version`、`agent.description` 为唯一、非空字符串；
- 要求 id 满足 `domain.IsCanonicalPowerName` 和安全单路径段约束；
- 要求 version 精确为 `vMAJOR.MINOR.PATCH`；
- 允许其它未知字段并保持原始字节，不把扩展字段纳入 APM 领域结构。

共享领域 API 固定为：

```go
func DecodeAgentConfigYAML([]byte) (AgentConfig, error)
func (AgentConfig) Validate() error
func (AgentConfig) ValidatePowerRef(PowerRef) error
```

`DecodeAgentConfigYAML` 负责 YAML 形状、重复键与 document 边界，`Validate` 负责最小字段规则，`ValidatePowerRef` 负责 id/version 与精确引用一致。Submission、cache、Adapter 与 recovery 必须使用这些入口，不能各自维护 YAML 解析规则。

`agent.name` 是 Bibit 的显示字段，不参与 APC 资源身份；`agent.description` 是 APC Submission metadata 的 description 来源。

## Validate 与 Prepare

### CLI

```bash
apm validate ./agent.yaml --type agent

apm submit ./agent.yaml \
  --type agent \
  --change-summary "首次发布"
```

- Agent version 只来自 YAML；`--type agent` 携带非空 `--version` 必须报错，禁止覆盖。
- submit 必须携带非空 `--change-summary`。
- validate 完全离线，不读取 API Key、不打开 provider、不访问 APC。

### 输入与安全

Agent 输入必须是单个物理普通文件。校验读取完整原始字节，并应用现有提交安全边界：单文件大小上限、非空、文本内容策略、context 取消。拒绝符号链接、目录、特殊文件、二进制、归档内容与超限输入。

成功的 Validate 返回：

- type：`agent`；
- name/version/description：来自 YAML；
- file count：`1`；
- content size：原始 YAML 字节数；
- warnings：沿用统一模型，本 feature 不新增 Agent 特有 warning。

### 原始内容

Prepare 完整复用 Validate，随后直接使用同一份原始 YAML 字节：

- Content-Type：`application/yaml`；
- MD5：针对原始 YAML 字节；
- 不生成 tar、JCS 或规范化 YAML；
- 不写 cache、staging 或 transaction journal；
- 上传仍受 20 MiB 同步边界约束。

因此 YAML 的空白、注释或字段顺序变化都会改变 content 和 MD5，这是原始字节不可变发布语义的一部分。

## Agent 身份闭环

### Cache

Agent 首次下载到私有临时版本目录后，cache 必须在原子发布精确版本目录前严格解析 YAML，并验证其 id/version 与请求 `PowerRef` 一致。缓存命中时必须重新执行同一校验，之后才能生成 staging。

已存在的缓存若格式损坏或身份不匹配，操作 fail closed：保留精确缓存目录，不删除、不自动回源覆盖。错误必须包含缓存路径与精确 `PowerRef`，便于用户显式处理。

### Bibit Adapter

`bibitAgentAdapter.Install` 在发布最终目标前重新解析 staging 的 `agent.yaml` 并验证 `PowerRef`，防止 cache 校验后到安装前发生替换。发布仍保留原始字节。

`AgentAdapter` 增加 `ValidateArtifact(domain.PowerRef, string) error` 只读校验能力，用于 staging、target、backup 或 quarantine；Adapter 继续独占 Bibit 根、目标路径和本地文件布局知识。

### Manifest-first 不变

当 manifest 已精确记录同名同版本时，install 继续直接返回 `unchanged`，不扫描现有目标。该规则与其它 Power 的 lifecycle 一致，本 feature 不把 install 变成全量状态审计。

### Recovery

现有 `DriverFilesystem` 同时服务 Skill 与 Agent，因此 `skill_recovery.go` 泛化命名为 `filesystem_recovery.go`，不改变 Skill 行为。Agent recovery 在任何 manifest 写入或旧目标恢复前校验将成为本地已安装事实的文件：

- install commit：target 必须匹配新 `PowerRef`；
- install rollback：将恢复的 backup 必须匹配 previous `PowerRef`；
- uninstall rollback：将恢复的 quarantine 必须匹配当前 `PowerRef`；
- uninstall commit：内容将被删除，无需业务身份校验。

校验失败返回 `transaction.ErrIndeterminateState`，保留 journal 与恢复材料，不改 manifest、不继续清理。Agent Adapter resolver 缺失、返回 nil 或部署不匹配同样 fail closed。

## Cloud Submission 接口

### 领域结果

`cloud.Submission.Submit` 返回新的统一结果，而不是只适用于 `202` 的 receipt：

```go
type SubmissionOutcome string

const (
    SubmissionAccepted SubmissionOutcome = "accepted"
    SubmissionReplayed SubmissionOutcome = "replayed"
)

type SubmissionResult struct {
    Outcome      SubmissionOutcome
    SubmissionID string
    Status       SubmissionStatus
    StatusURL    string
}

type Submission interface {
    Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionResult, error)
    GetSubmission(context.Context, string) (domain.Submission, error)
    ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error)
}
```

结果不变量：`accepted` 的 `StatusURL` 必须来自合法 receipt 且非空；`replayed` 的响应没有 `status_url`，其 `StatusURL` 固定为空，调用方使用 `SubmissionID` 生成既有 `apm submission get` 提示。两种结果都必须有非空 SubmissionID 与合法当前状态。

Submission query 类型和 CLI 不在本 feature 扩张；cursor、时间范围与 timeline 缺口明确留后续独立设计。

### Remote

Remote `Submit` 严格按状态解析：

- `202` 只接受合法 `SubmissionReceipt`，映射 `accepted`；
- `200` 只接受合法当前 `Submission` 详情，映射 `replayed`；
- 其它 2xx 返回 `cloud.ErrInvalidResponse`；
- Agent content part 使用 `application/yaml`，原始字节不变；
- `409` problem code 为 `ALREADY_PUBLISHED` 时返回新增 `cloud.ErrAlreadyPublished`，并保持可被 `errors.Is` 识别为通用 `cloud.ErrSubmissionConflict`；
- `RESOURCE_VERSION_CONFLICT` 继续映射通用 conflict；
- 继续拒绝错误 Content-Type、超限/畸形/重复键响应、未知状态和空身份。

Remote 不按字段形状猜测 `200`/`202` 语义，也不把任意 2xx 当成功。

### Local

Local 仅同步新的 `Submit` 方法签名，行为不变。contract test 必须证明 Local 与 Remote 均实现完整 `cloud.Client`，同时明确 Local Submission fail loud。

## APC 业务与 HTTP 契约

### 治理对齐

Agent 纳入与 Skill、Plugin、MCP 相同的规则：

- 任何有效开发者可提交任意 Agent 系列，不建立名称所有权；
- 新版本严格高于该系列历史最高已发布版本；
- 同一 `(type=agent, name)` 同时最多一个活动 Submission；
- 一次 Submission 和 Review Ticket 只对应一个精确版本；
- 进入同一人工审核、发布状态机和追加式审计；
- 已发布字节与 MD5 永不覆盖；已下架版本继续占用版本号；
- review、yank、restore 通用内网接口接受 `type=agent`；
- Agent 仍只适用于 Bibit，不增加 `supported_agents`。

### 最低验收

APC 对 Agent 只执行：

- metadata 的 type/name/version/description/change_summary 通用语法；
- content part 的 Content-Type 精确为 `application/yaml`；
- content 非空且不超过 20 MiB；
- 重算原始 content MD5 并与 metadata 比较；
- 内容可由 YAML parser 解析。

APC 不读取或交叉比较 schema version、id、version、description 或其它 Agent 业务字段，不重序列化 YAML。详细内容审查由 APM 提前完成并由工程师在工单中评审。

### 接口成功与冲突

`POST /api/v1/submissions` 的 metadata.type 接受四类 Power，Agent content 为 `application/yaml`。

- 首次接收：`202 SubmissionReceipt`；
- 幂等重放：`200 Submission`；
- 已发布同标识、同 MD5：`409 ALREADY_PUBLISHED`；
- 已发布同标识、不同 MD5，或版本不高于历史最高：`409 RESOURCE_VERSION_CONFLICT`。

Submission list 的 type filter、Submission detail、内网 review/yank/restore 的类型集合均加入 `agent`，但接口路径和状态机不新增分支。

## CLI 输出

`apm submit` 对两种成功结果都输出：

```text
outcome: accepted|replayed
submission_id: sub_...
status: received|validating|...
track: apm submission get sub_...
```

`accepted` 只表示首次同步接收；`replayed` 表示同一逻辑请求已存在，两者都不代表发布成功。`ALREADY_PUBLISHED` 是错误返回，CLI 不伪造成新的 Submission ID。

## 错误模型

- Agent YAML、schema 或身份错误：底层 `domain.ErrInvalidAgent`。
- validate/submit 对用户包装 `submission.ErrInvalid`，并保留具体路径与原因。
- 持久缓存损坏：cache 上下文错误；不删除、不回源。
- recovery 内容不可信：`transaction.ErrIndeterminateState`；保留 journal。
- 已发布同内容：`cloud.ErrAlreadyPublished`，同时属于 `cloud.ErrSubmissionConflict`。
- Local 提交：`cloud.ErrSubmissionUnsupported`。
- APC 继续以 `application/problem+json` 返回稳定 code 与 trace_id；APM 不解析自然语言 detail。

## 文档变更

APM：

- 新增本文及后续 `plan.md`；
- 新增 `ADR-019-align-agent-submission-lifecycle.md`；
- Feature 021 仅增加由 Feature 022 扩展的取代说明；
- 更新 `ivory-tower/project-overview.md`、`BUILD.md`；
- 更新 `apm-cli.dsl` 以及 cloud client、core lifecycle、cache、adapter、CLI、install、recovery 相关 Mermaid 源，补齐 Submission 端口与 Agent 校验责任；
- 行为实现与全量验证完成后，按仓库约定向 `docs/Changelog.md` 增加一次 Feature 022 条目。

APC：

- 更新 `ivory-tower/project-overview.md`；
- 更新 `ivory-tower/project-overview-apm.md`；
- 更新并显式纳入当前未跟踪的 `docs/APC-API.md`，不触碰其它用户内容；
- 新增 `ivory-tower/adr/ADR-001-unify-agent-submission-governance.md`；
- 本次不新增 APC 服务端代码或服务实现计划。

两仓库相关 overview/API 必须引用各自 ADR ID；APM ADR 同时引用 APC ADR，避免客户端文档成为云端业务规则的唯一权威。

## 预计 APM 生产文件

| 文件 | 实现内容 |
| --- | --- |
| `internal/domain/agent_config.go` | 共享严格 YAML 解码、description、最小校验与 `PowerRef` 身份匹配。 |
| `internal/domain/submission.go` | `SubmissionOutcome` 与 `SubmissionResult`。 |
| `internal/submission/agent.go` | Agent 单文件校验、统计和元数据提取。 |
| `internal/submission/validate.go` | Agent 类型分派。 |
| `internal/submission/submission.go` | Agent 原始 YAML Prepare、MD5 与 change summary 规则。 |
| `internal/cloud/submission.go` | Submit 返回类型签名。 |
| `internal/cloud/errors.go` | `ErrAlreadyPublished`。 |
| `internal/cloud/remote/submission.go` | Agent multipart、202/200 严格分流与 409 code 映射。 |
| `internal/cloud/local/submission.go` | 同步接口签名，保持 unsupported。 |
| `internal/cache/filesystem.go` | 首次下载与缓存命中 Agent 身份校验。 |
| `internal/adapter/agent.go` | Agent Adapter 只读目标校验能力。 |
| `internal/adapter/bibit_agent.go` | 复用共享解码器并实现身份/目标校验。 |
| `internal/transaction/manager.go` | Agent recovery resolver 依赖。 |
| `internal/transaction/filesystem_recovery.go` | 从原 `skill_recovery.go` 泛化命名并增加 Agent 恢复校验。 |
| `internal/cli/validate.go` | Agent 类型与 version override 规则。 |
| `internal/cli/submit.go`、`submit_contract.go` | Agent flag 与 accepted/replayed 输出。 |
| `internal/lifecycle/submit.go` | 新 Cloud 结果到 CLI 结果映射。 |

Cloud 接口签名变化会机械更新 `cmd/apm` 与各包测试 fake；测试与对应生产代码同目录组织，不新建大而全测试包。

## 测试与验收

### Domain

- 有效 Agent YAML 可解码；未知扩展字段不失败。
- 重复键、多 document、错误类型、缺 description、非 canonical id、非法版本均失败。
- id 或 version 与 `PowerRef` 任一不一致均失败。

### Submission

- validate 返回正确身份、`file_count=1` 与原始大小。
- 非普通文件、二进制、归档、超限、version override 与缺 change summary 在 Cloud 调用前失败。
- Prepare 字节与源文件完全相同，Content-Type 为 YAML，MD5 针对原始字节。
- 只改变 YAML 空白会改变 MD5。

### Cache、Adapter 与 Recovery

- 首次下载身份不符时不发布 cache。
- cache hit 损坏时不调用 Cloud、不删除精确缓存。
- staging 被替换时 Adapter 不发布 Bibit 目标。
- install commit、install rollback、uninstall rollback 仅接受匹配的 Agent 文件。
- recovery 校验失败时 manifest/journal/恢复材料保持不变。
- Skill filesystem recovery 全量回归不变。

### Cloud、Lifecycle 与 CLI

- Agent multipart 的 metadata、Content-Type 与原始字节精确。
- `202` 映射 accepted，`200` 映射 replayed，意外 2xx 失败。
- `409 ALREADY_PUBLISHED` 同时支持具体和通用 conflict 判断。
- Local Submission 继续 unsupported，Local/Remote `cloud.Client` contract 通过。
- validate/submit Agent flag、网络前失败和输出稳定。
- Skill、Plugin、MCP 现有行为全量回归。

### 最终门禁

- `gofmt` 无差异；
- `go test -count=1 ./...`；
- `go test -race -count=1 ./...`；
- `go vet ./...`；
- 隔离输出路径构建 `./cmd/apm` 并精确清理；
- APM/APC Markdown 链接、C4/diagram 与接口术语一致性扫描；
- 两仓库 diff scope 和工作树状态审计。

## 验收场景

- 合法 `agent.yaml` 可离线 validate，并可通过统一 Submission endpoint 提交原始字节。
- YAML id/version 与请求身份不一致时，cache、install 与 recovery 均 fail closed。
- 损坏 Agent cache 不被静默删除或回源覆盖。
- Agent Submission 可进入 review、publish、yank、restore，与其它 Power 使用相同状态机和路径。
- APC 只做传输级最低验收，不解释 Agent 业务字段。
- 首次受理、幂等重放、已发布同内容和版本冲突四种结果无歧义。
- Local provider 不伪造 Submission；Remote provider 完整支持 Agent。
- Submission 查询增强没有被本 feature 隐式引入。

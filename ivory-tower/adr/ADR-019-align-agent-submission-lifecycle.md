# 对齐 Agent Submission 生命周期

- Status：Accepted
- Date：2026-08-24
- Decision Makers：APM maintainers

## Context

ADR-015 把 Agent 引入为第四种 Power（仅 Bibit），但只定义了只读的下载、缓存与安装语义，把写入与治理留给后续。ADR-018 为 Skill、Plugin、MCP 建立统一的 `validate`、`submit` 与 Submission 查询，并明确把 Agent 排除在三类之外。相邻 APC 仓库的 ADR-001 已把 `skill`、`plugin`、`mcp`、`agent` 四类 Power 统一纳入同一条 Submission、人工审核、不可变发布与下架恢复治理管线，并把 Agent 的写入入口从「仅支持三类」升级为完整支持。

本决策记录 APM 客户端的对应变化，使 Agent 与其它 Power 共享同一套本地校验、确定性内容与 Cloud Submission 流程，同时保持其 Bibit 专属安装语义（ADR-015）。它同时修复两个与对齐直接相关的既有缺口：

1. Agent 安装内容与 `PowerRef` 尚未形成身份闭环：缓存、安装与恢复此前不交叉校验 `agent.yaml` 的 `agent.id`/`agent.version` 与请求版本一致。
2. `cloud.Submission.Submit` 只返回适用于 `202` 的 `SubmissionReceipt`，无法表达 APC 的 `202 Accepted`（首次受理）与 `200 OK`（幂等重放）两种成功语义，也无法表达「已发布同内容」的 `409 ALREADY_PUBLISHED` 冲突。

本决策关联 [Feature 022](../features/022-agent-submission/spec.md)，继续遵守 ADR-015 的 Bibit 专属边界，并扩展 ADR-018 的 Submission 接口。云端业务与 HTTP 契约的权威在相邻 APC 仓库的 ADR-001（统一 Agent Submission 与发布治理，`ivory-tower/adr/ADR-001-unify-agent-submission-governance.md`）。

## Decision

### 统一管线而非独立 Agent API

Agent 复用现有 `submission.Validate`、`submission.Prepare`、`cloud.Submission` 与 `POST /api/v1/submissions`。类型专属规则集中在 `internal/submission/agent.go`，不新增 `SubmitAgent`、Agent 专属 endpoint 或平行状态机。四类 Power 并列，改动聚焦，也不为尚不存在的第五种类型提前重构已稳定的 Skill、Plugin、MCP 管线。

### 共享 domain Agent parser

`internal/domain` 是 APM 对 Agent 稳定 schema 的唯一权威，固定三个入口：

```go
func DecodeAgentConfigYAML([]byte) (AgentConfig, error)
func (AgentConfig) Validate() error
func (AgentConfig) ValidatePowerRef(PowerRef) error
```

`DecodeAgentConfigYAML` 负责 YAML 形状（单 document、根与 `agent` 为 mapping、`schema_version` 为整数且等于 `1`、`agent.id`/`agent.version`/`agent.description` 为唯一非空字符串）、重复 mapping key 与尾随 document 拒绝，并保留未知字段与原始字节；`Validate` 负责最小字段规则（canonical id、安全单路径段、精确 `vMAJOR.MINOR.PATCH` 版本、非空 description）；`ValidatePowerRef` 负责 id/version 与精确 `PowerRef` 一致。Submission、cache、Adapter 与 recovery 必须复用这些入口，不各自维护 YAML 解析规则。

### Agent 原始 YAML 与身份校验层次

Agent Submission 输入是单个原始 `agent.yaml`，不打包、不重序列化；name/version/description 分别来自 `agent.id`/`agent.version`/`agent.description`，change summary 来自必填 CLI 参数。Prepare 复用 Validate 后直接使用同一份原始字节，Content-Type 为 `application/yaml`，MD5 针对原始字节，上传仍受 20 MiB 同步边界约束。身份校验沿固定层次逐层加固：

1. **domain parser**：`DecodeAgentConfigYAML` + `Validate`/`ValidatePowerRef` 是唯一解析与最小校验入口；
2. **cache**：`PrepareAgent` 在首次下载与缓存命中时都执行同一解析与 `ValidatePowerRef`，确认 id/version 与请求 `PowerRef` 一致后才发布精确版本目录或生成 staging；
3. **adapter**：`AgentAdapter.ValidateArtifact(domain.PowerRef, string) error` 提供只读身份校验，`bibitAgentAdapter.Install` 在发布最终目标前重新校验 staging 的 `agent.yaml`，防止 cache 校验后到安装前被替换；
4. **recovery**：filesystem 事务在任何 manifest 写入或旧目标恢复前，通过 `AgentAdapterResolver.ResolveAgent` 取得 Agent Adapter 并校验即将成为本地已安装事实的文件。

### Cache fail-closed 身份校验

Agent 首次下载到私有临时版本目录后，cache 必须在原子发布精确版本目录前严格解析 YAML 并验证 id/version 与请求 `PowerRef` 一致；缓存命中时必须重新执行同一校验，之后才能生成 staging。已存在的缓存若格式损坏或身份不匹配，操作 fail closed：保留精确缓存目录，不删除、不自动回源覆盖。错误必须包含缓存路径与精确 `PowerRef`，便于用户显式处理。

### Adapter/recovery 身份闭环

`AgentAdapter` 增加 `ValidateArtifact` 只读校验能力，用于 staging、target、backup 或 quarantine；Adapter 继续独占 Bibit 根、目标路径和本地文件布局知识。`bibitAgentAdapter.Install` 先读取并校验 staging 的 `agent.yaml` 身份，再用同一份原始字节原子发布。filesystem recovery 从原 `skill_recovery.go` 泛化命名为 `filesystem_recovery.go`（不改变 Skill 行为），并在以下节点前校验 Agent 身份：

- install commit：target 必须匹配新 `PowerRef`；
- install rollback：将恢复的 backup 必须匹配 previous `PowerRef`；
- uninstall rollback：将恢复的 quarantine 必须匹配当前 `PowerRef`；
- uninstall commit：内容将被删除，无需业务身份校验。

校验失败返回 `transaction.ErrIndeterminateState`，保留 journal 与恢复材料，不改 manifest、不继续清理。Agent Adapter resolver 缺失、返回 nil 或部署不匹配同样 fail closed。

### Local provider 继续只读

Remote provider 完整实现 Submission；Local provider 只提供 canonical fixture 的 list、resolve 与 download，其 `Submit`、`GetSubmission`、`ListSubmissions` 对所有类型继续返回 `cloud.ErrSubmissionUnsupported`（fail loud）。contract test 证明 Local 与 Remote 均实现完整 `cloud.Client`。离线 validate 不依赖 provider。

### Cloud Submission 结果分流

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
```

结果不变量：`accepted` 的 `StatusURL` 必须来自合法 receipt 且非空；`replayed` 的响应没有 `status_url`，其 `StatusURL` 固定为空，调用方使用 `SubmissionID` 生成既有 `apm submission get` 提示。两种结果都必须有非空 SubmissionID 与合法当前状态。Remote `Submit` 严格按状态解析：`202` 只接受合法 receipt 并映射 `accepted`；`200` 只接受合法当前 Submission 详情并映射 `replayed`；其它 2xx 返回 `cloud.ErrInvalidResponse`。Remote 不按字段形状猜测 `200`/`202` 语义，也不把任意 2xx 当成功。

### 已发布内容使用稳定冲突

Agent content part 使用 `application/yaml`，原始字节不变。`409` problem code 为 `ALREADY_PUBLISHED` 时返回新增的 `cloud.ErrAlreadyPublished`，并保持可被 `errors.Is` 识别为通用 `cloud.ErrSubmissionConflict`；`RESOURCE_VERSION_CONFLICT` 继续映射通用 conflict。已发布同标识且同 MD5 不再作为 `200` 成功返回，CLI 不把它伪造成新的 Submission ID。

### CLI 输出

`apm submit` 对两种成功结果都输出 `outcome: accepted|replayed`、`submission_id`、`status` 与 `track: apm submission get <id>`。`accepted` 只表示首次同步接收，`replayed` 表示同一逻辑请求已存在，两者都不代表发布成功。`--type agent` 携带非空 `--version` 必须报错（版本只来自 YAML），submit 必须携带非空 `--change-summary`。

## Consequences

正面影响：

- Agent 与其它三类 Power 共享同一条本地校验、确定性内容与 Submission 管线，避免第二套提交实现与专属 endpoint。
- 缓存、安装与恢复复用同一套 `internal/domain` 解析与身份校验，杜绝各自维护 YAML 规则导致的分叉。
- Agent 安装内容与 `PowerRef` 形成从缓存到安装再到恢复的完整身份闭环，替换或损坏会在发布前被 fail closed 拦截。
- `202`/`200` 分流与 `409 ALREADY_PUBLISHED` 让客户端无歧义地区分首次受理、幂等重放与已发布冲突。

代价与风险：

- Agent 输入只接受单个原始普通文件，YAML 的空白、注释或字段顺序变化都会改变 content 与 MD5，同一逻辑内容可能产生多个冲突版本，需要开发者理解这一原始字节不可变语义。
- Agent 业务校验只由 APM 独占完成，APC 仅做传输级最低验收（metadata 通用语法、`application/yaml`、非空与 20 MiB、原始字节 MD5、YAML 可解析），绕过 APM 的请求仍可能进入 APC 审核工单，内容质量由人工评审兜底。
- 损坏或身份不匹配的 Agent 缓存不会被自动修复或回源覆盖，需要用户显式处理。

后续动作：

- 在 `project-overview.md`、`BUILD.md`、`apm-cli.dsl` 及相关 Mermaid 源中落地本决策，并引用 ADR-019 与 APC ADR-001。
- Feature 021 顶部注明 Agent Submission 由 Feature 022/ADR-019 扩展，其主体保留历史三类范围。
- 行为实现与全量验证完成后，按仓库约定向 `docs/Changelog.md` 增加一次 Feature 022 条目。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 为 Agent 新建 `SubmitAgent`、专属 endpoint 与平行状态机 | 引入第二套提交与治理实现，维护成本高；已由统一管线取代。 |
| 让 cache/Adapter/recovery 各自解析 `agent.yaml` 而不复用 `internal/domain` | 解析与身份规则分叉，替换或损坏无法一致 fail closed；已由共享 parser 取代。 |
| 缓存身份不匹配时删除并回源覆盖损坏 Agent cache | 静默删除用户数据且无法保证覆盖结果正确；已由 fail-closed 保留现场取代。 |
| 继续只让 `Submit` 返回 `SubmissionReceipt` | 无法表达 `200` 幂等重放，客户端会误读成功语义；已由 `SubmissionResult` 取代。 |
| 已发布同标识同 MD5 仍返回 `200` | 与幂等重放的 `200` 不可区分；已由 `409 ALREADY_PUBLISHED` + `ErrAlreadyPublished` 取代。 |
| Local provider 实现真实提交 | 本地 fixture 无真实工单与异步验收语义，空实现更能暴露配置错误；继续返回 `ErrSubmissionUnsupported`。 |

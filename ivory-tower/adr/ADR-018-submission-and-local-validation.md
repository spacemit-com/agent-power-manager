# Submission 与本地完整校验

- Status：Accepted
- Date：2026-08-22
- Decision Makers：APM maintainers

> 局部取代说明：本文保留 Feature 021 落定时的历史决策；其中资源元数据来源、CLI version/message、MCP content 与 Submission 查询部分已由 [ADR-020](ADR-020-source-owned-submission-metadata.md) 和 [Feature 023](../features/023-submission-resource-metadata-query/spec.md) 取代，当前实现与接口以二者为准。

## Context

APC 定义了资源提交（Submission）工作流与校验职责边界，但 APC 无实体，仅以设计约束 APM。APM 需要在提交前离线完成 Skill、Plugin、MCP 的完整格式校验，对 Skill/Plugin 做确定性打包、对 MCP 做确定性 JSON 规范化，再统一上传并查询本人提交。若校验规则只在服务端存在，用户会在上传后很久才看到格式错误，编辑器和 CI 也无法复用。

本决策关联 [Feature 021](../features/021-submission-validate/spec.md)。

## Decision

- `internal/cloud.Client` 在 `Catalog`、`SkillSource`、`PluginSource`、`MCPSource`、`AgentSource` 之外增加 `Submission` 端口（`Submit`/`GetSubmission`/`ListSubmissions`）。Remote 完整实现 multipart 上传与 get/list，Local 空实现返回 `ErrSubmissionUnsupported`。
- 离线完整校验落在新包 `internal/submission`，`validate` 与 `submit` 共用同一套规则：`validate` 不要求 version 与 change_summary，`submit` 经 `Prepare` 强制 version（Skill）与 change_summary（Skill/Plugin）。校验在压缩前执行，确定性打包视为受信产物，打包后不再解包复核。
- Skill/Plugin 只接受原始目录，确定性打包为 `.tar.gz`（gzip 无时间戳、成员名稳定排序）；MCP 只接受单个完整 JSON 文件，definition 经 RFC 8785 JCS 规范化。两者都计算 MD5 摘要，复用 APC 的 multipart 上传与 `application/problem+json` 错误模型。
- 阈值：source 总量 ≤100 MiB、单文件 ≤10 MiB、≤1000 文件、深度 ≤16、路径 ≤255 字节；上传 ≤20 MiB。
- `APC_API_KEY` 与 `APC_BASE_URL` 仅通过环境变量配置，不引入用户级配置文件。submit 与 submission get/list 需要 Key，缺省即报错且错误信息不打印完整 Key；search/list/install/uninstall 的匿名只读接口不携带 Key。
- APC 的最低验收收敛为：metadata 语法校验、MD5 核对、文件格式可解析（不解包）与上传 ≤20 MiB；其余校验全部移入 APM。对 Submission 的审查完全由 APM 负责，APC 仅做必要验收并创建工单。

## Consequences

- 正面影响：validate 与 submit 的规则只维护一份，离线校验错误在发起任何网络请求前被拦截。
- 正面影响：确定性打包与 JCS 规范化使 content_md5 可复现，便于 APC 复核与后续幂等。
- 正面影响：Local 空实现让测试无需网络即可复验生命周期与 CLI 行为。
- 代价与风险：MVP 继续使用 MD5，不提供 SHA-256 强摘要与强完整性证明。
- 代价与风险：submit 不自动重试网络错误、超时、429 或 5xx，不提供 `--wait`，由用户手动跟踪。
- 后续动作：APC 落地时按其设计实现最低验收与工单；若引入强摘要，替换 MD5 并同步改动 content_md5 字段与 APC 契约。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 校验只在 APC 服务端执行 | 错误反馈延迟，编辑器与 CI 无法复用，网络往返成本高。 |
| 打包后解包复核 | 确定性打包已是受信产物，重复解包只增加成本且不增加保证。 |
| 使用 SHA-256 摘要 | APC 契约当前只定义 MD5；强摘要留待后续契约升级。 |
| 为用户级配置文件引入 API Key | 增加凭证轮换、账号与安全治理面，环境变量已满足 MVP。 |
| Local 实现真实提交 | 本地 fixture 无真实工单与异步验收语义，空实现更能暴露配置错误。 |

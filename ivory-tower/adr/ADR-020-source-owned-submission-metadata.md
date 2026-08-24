# Submission 身份元数据以资源文件为权威

- Status：Accepted
- Date：2026-08-24
- Decision Makers：APM maintainers

## Context

APM 已支持 Skill、Plugin、MCP、Agent 的离线校验与 Submission，但四类输入并不一致：Skill 的版本和三类 Power 的更新说明来自命令行，Plugin 与 Agent 的身份来自资源文件，MCP 又使用一份包含身份、更新说明和 definition 的 Submission 专用包装 JSON。相同信息存在多个来源，容易造成 CLI 参数、资源内容和 APC 索引不一致。

同时，现有 Submission 查询虽然领域端口含 cursor 和 limit，CLI 却没有完整传递这些能力，也未消费详情中的 description、更新说明、时间和 timeline。APC 尚未部署，可以在不承担兼容成本的前提下统一命名并收敛查询契约。

本决策关联 [Feature 023](../features/023-submission-resource-metadata-query/spec.md)，并与 APC [ADR-003](../../../apc/ivory-tower/adr/ADR-003-bound-active-submission-query.md) 共同定义客户端与服务端边界。

## Decision

### 资源文件是唯一身份来源

四类 Power 的 `name/version/description` 只从各自权威文件读取：Skill 使用 `SKILL.md` frontmatter，Plugin 使用 `apc-plugin.json`，MCP 使用 `MCP.json`，Agent 使用 `agent.yaml`。CLI 不再接受 `--version`，目录名也不得覆盖资源身份。

`internal/submission` 使用统一的内部 Source Loader 协议组织四类解析器。Loader 只负责读取、校验并提供确定性 content 构造能力；公共编排负责 message、20 MiB 上传上限、MD5 和 Cloud 请求。该抽象仅属于 Submission，不扩展到已经稳定的 install/cache/transaction lifecycle。

### message 是提交上下文

更新说明不属于资源身份，而属于一次 Submission。`apm submit` 对四类 Power 统一要求非空 `--message`；删除 `--change-summary`，并把 APM domain、APC wire 和已发布资源元数据中的 `ChangeSummary/change_summary` 全部改为 `Message/message`，不保留兼容别名。

`message` 不写入 Power 资源文件，不参与 content MD5。重试相同资源时，APC 依据幂等与活动内容规则返回已有 Submission，而不是由资源文件持久化提交上下文。

### MCP 使用完整资源文件

MCP v1 content 统一为包含 `schema_version/name/version/description/definition` 的完整 `MCP.json`。Submission 上传完整文件的 JCS 规范字节；Local/Remote Cloud 校验完整资源与请求 `PowerRef` 一致后，只向 lifecycle 和 Adapter 返回 `definition` 对应的 `MCPServer`。旧 definition-only 文件和旧 Submission wrapper 均不兼容。

### 查询只面向认证账号的 active 集合

`submission list` 只查询当前官方 API Key 所属账号的 active Submission，最多 20 条；筛选仅保留 status/type/name，不使用 limit、cursor 或时间范围。`submission get` 补齐 description、message、时间戳和 timeline。Remote 查询必须使用独立的认证 GET 路径，禁止把 API Key 附加到匿名资源读取请求。

active 状态固定为 `received`、`validating`、`review_pending`、`approved`、`publishing`。其它状态只能通过已知 Submission ID 查询详情。

## Consequences

- 正面影响：资源身份只有一个来源，CLI 无法覆盖或制造与资源内容不同的版本。
- 正面影响：新增 Power 只需增加一个 Submission Loader，不需要修改其他 Loader。
- 正面影响：MCP 发布内容自描述，Local、Remote 与 Submission 复用同一 schema。
- 正面影响：开发者列表规模有固定上界，APM/APC 不需要为该接口维护分页状态。
- 代价与风险：旧 CLI 参数、旧 MCP 文件和 `change_summary` wire 字段立即失效；项目仍在开发阶段，明确不提供迁移兼容。
- 代价与风险：已结束 Submission 不再可从开发者列表发现，用户必须保留 ID 或通过官网历史能力查询。
- 后续动作：ADR-018 与 ADR-019 的相关段落标注为被本 ADR 局部取代；APC 落地时实现 ADR-003 的事务性数量限制与账号归属检查。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保留各 Power 的现有参数和专用解析流程 | 身份继续存在多个来源，新增 Power 容易复制差异。 |
| 把 message 写入每个资源文件 | message 描述一次提交而非资源身份，会改变 content 与 MD5，并污染可复用资源格式。 |
| 建立覆盖安装、缓存和 Submission 的全仓库 Power Resource 接口 | 改动面远超当前问题，会扰动稳定 lifecycle；采用 Submission 内部 Source Loader。 |
| 为 Submission 历史列表保留 cursor 分页 | 当前每个 API Key 的 active 集合有 20 条硬上限，开发者 CLI 不承担历史审计。 |


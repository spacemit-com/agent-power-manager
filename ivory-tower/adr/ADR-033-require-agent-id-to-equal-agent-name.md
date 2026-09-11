# 要求 agent.id 与 agent.name 取相同值

- Status：Accepted
- Date：2026-09-11
- Decision Makers：APM maintainers
- Related ADR：[ADR-023](ADR-023-package-agent-with-assets.md)

## Context

[ADR-023](ADR-023-package-agent-with-assets.md) 定下两件事：Agent 的外部身份使用 `agent.name`，
最终安装到 `<bibit根>/agents/<agent.name>/`（完整目录、不含版本层级）；`agent.id` 只作为 Bibit
内部的必填标识，不再与 PowerRef name 比较。该决策的默认前提是「Bibit 内部标识」与「磁盘上的目录名」
互不相干，因此取值不同也不影响使用。

隔离实测推翻了这一默认。Runtime 在 `agents/` 下按 `agent.id` 定位已安装的 Agent 目录（Runtime 界面
中即「专家」），而 APM 按 `agent.name` 发布目录：`test-agent` 资源的 `id` 为
`test-agent-84a175a0-1223-4e0a-9c70-6f98f2769926`、`name` 为 `test-agent`，结果安装成功、
`apm list --installed --type agent` 也能列出，但 Runtime 按 ID 找不到该 Agent。也就是说，两个取值
是否相同直接决定资源在 Runtime 侧可用与否，而 APM 全程不报错。

APM 侧不存在可以拦住这类错误的检查：`internal/domain/agent_config.go` 的 `Validate` 只要求
`agent.id` trim 后非空，`ValidatePowerRef` 只比较 `name`/`version`。实测四个 Agent 资源根
（`../using-apm` 模板、两个 `test-agent` 副本、内建 local fixture `bianbu-agent-default`）都能通过
`apm validate --type agent --json` 并返回空 `warnings`，其中前者之外的三者或为不等取值、或为
「文档示范不等取值」。内建 fixture 同样如此：`BUILD.md` 记录的
`./apm install bianbu-agent-default=v1.0.0 --type agent` 开发流程在 Runtime 侧不可用。

本决策的依据除上述隔离实测外，只有 Bibit 侧的契约旁证（`RuntimeAgent` 以 `id` 为主键、Runtime 控制台
把 agents 标为「专家」、Runtime 代理以 `/api/v1/agents/<agent_id>` 寻址）。本仓库与开发机、测试设备上
均无可用 Runtime 环境，故无法在 APM 侧复现或自动化该行为；这条规则因此只能由资源作者遵守，并由模板与
文档承载。

## Decision

Agent 资源的 `agent.id` 必须与 `agent.name` 取相同值。

- APM 的校验与安装行为完全不变：仍只要求 `agent.id` 为 trim 后非空字符串，仍按 `agent.name` 定位
  `<bibit根>/agents/<agent.name>/`，仍以 `agent.name`+`agent.version` 构成外部身份，仍不比较
  `agent.id` 与 `agent.name`。`id != name` 不是校验错误。
- 这是资源创作规则，通过提交规则、模板与内建 fixture 固化，不新增校验、不新增告警、不自动改写
  `agent.yaml`，也不改写已安装目录。
- 权威样板为 `outputs/basic-agent/agent.yaml`（两者同为 `basic-agent`）。`test-agent`（提交源与
  `outputs/` 副本）与内建 local fixture `bianbu-agent-default` 改为相同取值。
- 取值方向是「让 `id` 取 `name` 的值」，不是反过来：`agent.name` 与 `agent.id` 都必须同时满足 APM
  canonical power name 规则（`IsCanonicalPowerName`：小写字母/数字/`-`，长度 1..64），而既有资源的
  `id` 往往含大写与十六进制片段，直接沿用会让 `name` 非法。
- 与 ADR-023 不冲突：ADR-023 规定「APM 不比较二者」，本决策规定「取值应当相同」，两者并存。

## Consequences

- 正面影响：按模板创建的 Agent 在 Runtime 中可按 ID 找到，消除「能列出、按 ID 查不到」这一无报错的
  静默失效。
- 正面影响：内建 local fixture 修正后，`BUILD.md` 的 `install bianbu-agent-default=v1.0.0 --type agent`
  开发流程成为端到端可用路径。
- 负面/代价：约束不在 APM 侧强制。作者写错时 `validate` 与 `submit` 仍然全部成功，失效只在 Runtime
  侧静默出现，发现依赖人工检查或运行时行为。
- 代价与风险：已发布的 `test-agent@v1.0.0` 内容带此缺陷，修正需要重新提交；是否提升版本号由提交者
  决定。若下游曾按旧 `id` 记录绑定，重新提交后不会自动迁移。
- 后续动作：可评估在 `apm validate --type agent` 中把 `id != name` 报为 warning，把静默失效变成可
  发现的问题。本决策不包含该行为变更。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保持 ADR-023 现状，仅靠文档提醒 | 模板与内建 fixture 本身仍在示范不等取值，静默失效的代价高于提醒收益。 |
| APM 校验 `id == name` 并拒绝不等的资源 | 会改变已发布资源的可提交性，且 ADR-023 明确该字段不参与身份比较；先以规则、模板与 fixture 落地，再评估 warning。 |
| APM 安装时把目录改名或链接为 `agent.id` | 与 ADR-023 的 `<bibit根>/agents/<agent.name>/` 及「外部身份即 name」冲突，且会让 CLI 所见名称与磁盘目录不一致。 |
| 发布时自动把 `agent.id` 改写为 `agent.name` | 违反 Agent 内容「原样透传、保留原始字节」的既有约定，静默改变作者提交的内容。 |
| 反过来让 `agent.name` 取 `agent.id` 的值 | 旧 `id` 含大写与十六进制片段，不满足 canonical power name，会让资源无法通过校验；`name` 还是用户可见的安装名称。 |

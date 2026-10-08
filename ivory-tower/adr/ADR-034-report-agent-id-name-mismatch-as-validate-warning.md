# 在 validate 中把 agent.id 与 agent.name 不同取值报为 warning

- Status：Accepted
- Date：2026-09-11
- Decision Makers：APM maintainers
- Related ADR：[ADR-033](ADR-033-require-agent-id-to-equal-agent-name.md)（本决策实现其「后续动作」，并部分取代其「不新增告警」条款）

> 拼写说明（2026-10-08）：本文出现的 `--type agent` 是改名前的写法，现在规范拼写为
> `--type expert`（旧值仍被接受，非 `--json` 时会提示弃用）。类别改名与保留清单见
> [ADR-044](ADR-044-rename-agent-power-type-to-expert.md)。

## Context

[ADR-033](ADR-033-require-agent-id-to-equal-agent-name.md) 确定了 Agent 资源的创作规则
`agent.id` 必须等于 `agent.name`：Bibit 按 `agent.id` 定位已安装的 `agents/<agent.name>` 目录，
取值不同时资源仍能安装、也能被 `apm list --installed` 列出，但 Runtime 按 ID 找不到它。该 ADR 同时
明确「不新增校验、不新增告警」，把这条规则完全交给模板与文档承载，并把「在 validate 中报为 warning」
列为后续动作。

结果是该错误在 APM 侧没有任何可发现的信号：`internal/domain/agent_config.go` 的 `Validate` 只要求
`agent.id` trim 后非空，`ValidatePowerRef` 只比较 `name`/`version`，因此一个 `id != name` 的资源
`apm validate --type agent --json` 返回 exit 0 且 `warnings: []`。作者在本地无法自查，失效只在
Runtime 侧静默出现——这正是 ADR-033 记录的、需要人工检查才能发现的问题。

validate 的输出契约已经具备承载能力：`submission.Validation.Warnings` → `cli.ValidateResult.Warnings`
→ CLI 的 `warning: <text>` 行（`--json` 下为 `warnings` 数组），无需新增接口。当前该字段在全部生产
路径上都固定为空切片，本决策会产出它的第一个非空值。

## Decision

`apm validate --type agent` 在 `agent.id != agent.name` 时输出恰好一条 warning，此外不改变任何行为。

- **仅 validate**。`install`/`uninstall`/cache/recovery 不检查该条件，`submit` 的输出与退出码也不变
  （`submission.Prepare` 只消费 `Validation` 中的身份与统计字段，不读取 `Warnings`），因此不新增
  submit 输出契约。
- **仍不是错误**。校验成功、exit code 保持 0，资源仍然可提交、可安装。APM 不比较二者这一 ADR-023
  的边界继续成立。
- **比较为精确字符串比较**，不做 trim、不做大小写归一：Bibit 按字面值定位目录，`id: " demo-agent"`
  与 `name: demo-agent` 在实际使用中确实会查找失败，因此应当告警。
- **warning 文案**在一条消息内给出两个取值、后果与修法：
  `agent.id %q must equal agent.name %q: Bibit locates the installed agents/<name> directory by agent.id, so a mismatch makes the Agent unreachable by ID at runtime`。
- **保持 Agent 路径的 `warnings` 为 JSON 数组**：无警告时仍返回空切片而非 nil。Agent 的 `--json`
  输出在本次变更前就是 `"warnings": []`，返回 nil 会让它退化为 `"warnings": null`。顺带说明一个不
  在本决策范围内的既有不一致：skill/plugin/mcp 三类 loader 从不初始化 `Warnings`，其 `--json` 输出
  本来就是 `null`，本决策不改动它们。
- 判定只覆盖 Agent：其它三种 Power 类型没有该字段对，不新增 warning 来源。

## Consequences

- 正面影响：ADR-033 的静默失效变成离线可发现的问题。作者跑一次 `apm validate` 就能看到取值不一致，
  不需要在 Runtime 里排查。
- 正面影响：exit code 不变、`submit` 不变，已验证与已发布的资源不会因为新增告警而变得不可用或
  不可提交。
- 代价与风险：`Validation.Warnings` 不再是恒空字段，`--json` 的 `warnings` 数组从此可能非空；
  依赖「`warnings` 永远为空」的调用方需要改为按内容判断。
- 代价与风险：warning 只出现在 validate，`submit` 不显示。走「只跑 submit」流程的作者仍可能把它
  带上去；如需覆盖应另行决策，而不是借本决策顺手改 submit 输出。
- 后续动作：可评估让 `install` 在发布前也提示同一条件，以及在 validate 中覆盖其它「不影响 APM 校验、
  但影响 Runtime 可用性」的字段。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保持 ADR-033 原状，不加任何 APM 侧信号 | 错误只在 Runtime 侧静默出现，作者无法在本地自查；ADR-033 已把它列为后续动作。 |
| 直接把 `id != name` 报为校验错误 | 会让已发布资源无法再通过校验与提交，与 ADR-023「该字段不参与身份比较」冲突，也超出「提醒作者」的需要。 |
| 在 `install` 时也输出同一 warning | 安装阶段已无可挽回的动作（目录已按 name 发布），且会把 validate 之外的行为一并改动；留给后续决策。 |
| 同时改 `submit` 输出以显示 warning | 会改动 submit 的输出契约与退出语义，超出本决策范围。 |
| 用 trim 后的值比较，避免空白差异告警 | Bibit 按字面值定位目录，trim 后相等仍会查找失败，放宽会漏报真实故障。 |
| 让 APM 自动把 `agent.id` 改写为 `agent.name` | 违反 Agent 内容原样透传的既有约定，静默改变作者提交的字节。 |

# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Decision Makers：liangjunzhao

## Context

APM 的 `search`/`list`/`install`/`uninstall` 等命令都需要同时确定两个维度：

- **目标 Agent**：通过 Adapter 适配，是开放、可增添的集合，且需要支持 `all`（对所有已注册目标 Agent 执行）。
- **Power 类别**：`skill`/`plugin`/`mcp`，是封闭的小集合，通常可从云端 Power 元数据中获取。

需要决定这两个维度如何映射到 CLI 语法：作为 flag、还是作为子命令命名空间。详见 [project-overview.md](../project-overview.md) 的 Architecture Snapshot（数据与接口）与待决事项「CLI 命令和 Power 类别交织」，以及 [ADR-001](ADR-001-use-go-as-implementation-language.md) 确定的 `cobra` 技术栈。

## Decision

CLI 语法采用：

```text
apm <verb> [name] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

- `--agent`：必填（或由本地配置提供 default-agent 作为缺省值），取值为具体 Agent ID 或字面量 `all`。
- `--type`：可选。缺省时由云端 Power 元数据推断；仅当同名 Power 跨类别产生歧义时，APM 报错并要求显式指定 `--type`。
- `name`：Power 名称，作为 positional 参数；`search`/`list` 可省略（按 `--agent`/`--type`/关键字过滤）。
- `--help` 顶层展示可用的 Agent/Power 类别枚举。

两个维度均以 flag 承载，不作为子命令命名空间；`verb`（`search`/`list`/`install`/`uninstall`）是唯一的子命令层级。

`--agent=all` 的执行语义：对每个已注册目标 Agent 独立执行一次完整的生命周期事务（各自的 staging/rename/journal；全局 APM 文件锁在install/uninstall期间持有，见 ADR-001 锁机制），单个 Agent 失败不影响其他 Agent 继续执行；命令结束后按 Agent 逐条汇总成功/失败结果，整体命令的退出码在任一 Agent 失败时为非零。

## Consequences

- 正面影响：
  - Agent 集合可通过 Adapter 持续增添，不影响命令树结构和 `--help` 输出的稳定性，适合脚本化和自动化调用。
  - `--type` 可选且可推断，减少常见场景下的输入负担，同时保留显式指定以消歧的能力。
  - 四个动词共用同一套 flag 语义，`cobra` 层实现和文档维护成本低，Power 类别新增时无需扩展命令树。
- 代价与风险：
  - `--agent=all` 下多 Agent 部分成功、部分失败的场景，要求错误汇总与非零退出码的实现足够清晰，避免用户误判整体命令是否成功。
- 后续动作：
  - 在 `internal/lifecycle` 中实现「按 Agent 汇总结果」的公共逻辑，供 `install`/`uninstall`（及未来可能受影响的动词）复用。
  - 云端 `get` 接口需要保证返回 Power 的类别元数据，供 `--type` 缺省推断使用；若同名跨类别场景存在，需要在云端协议中明确如何返回歧义信息。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Type 作为子命令命名空间，Agent 作为 Flag（如 `apm skill install <name> --agent=x`） | `--help` 可发现性更好，但 3 类型 × 4 动词需注册 12 个叶子命令，且类别通常可从云端元数据推断，强制用户先选类别造成重复输入。 |
| Agent 作为子命令命名空间，Type 作为 Flag（如 `apm claude-code install <name> --type=x`） | Agent 是通过 Adapter 可持续增添的开放集合，绑定为命令树节点会导致命令树随已编译 Adapter 变化，`--help` 不稳定；`all` 作为子命令名与真实 agent 名混杂，语义别扭，不利于脚本化。 |
| 全 Positional（如 `apm install <type> <agent> <name>`） | 顺序强绑定、无自解释性，缺省值难以表达，未来新增维度（如版本）会破坏既有脚本。 |

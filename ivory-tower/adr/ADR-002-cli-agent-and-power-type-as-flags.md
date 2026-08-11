# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Decision Makers：liangjunzhao

## Context

APM 的本地 Power 命令 `list --installed`/`install`/`uninstall` 需要同时确定两个维度：

- **目标 Agent**：通过 Adapter 适配，是开放、可增添的集合，且需要支持 `all`；本地 Power 操作仅对当前命令检测到的“已支持且已安装 Agent”执行。
- **Power 类别**：`skill`/`plugin`/`mcp`，是封闭的小集合，通常可从云端 Power 元数据中获取。

`search` 只查询 Cloud Power，不访问 Agent 本地状态，因此只使用 Power 类别维度。

需要决定这两个维度如何映射到 CLI 语法：作为 flag、还是作为子命令命名空间。详见 [project-overview.md](../project-overview.md) 的 Architecture Snapshot（数据与接口）与待决事项「CLI 命令和 Power 类别交织」，以及 [ADR-001](ADR-001-use-go-as-implementation-language.md) 确定的 `cobra` 技术栈。

## Decision

CLI 语法采用：

```text
apm search <keyword> [--type=<skill|plugin|mcp>]
apm list [--type=<skill|plugin|mcp>]
apm list --installed --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm install <power>[=<version>] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

- `--agent`：适用于 installed `list`、`install`、`uninstall`；取值为具体 Agent ID 或字面量 `all`。默认 Cloud `list` 显式提供该 flag（包括空值）必须返回 `--agent requires --installed`；installed `list` 必须提供非空 Agent。
- `--type`：可选，但缺省语义按命令的数据来源确定：`search`、默认 `list` 与 `list --installed` 省略时均不应用类别过滤，提供时仅作精确类别过滤；`install` 从 Cloud `List()` 返回的带版本 `PowerRef` 推断；`uninstall` 从目标 Agent 的本地安装清单解析，仅当本地同名 Power 跨类别产生歧义时才报错并要求显式指定 `--type`。
- `--help` 顶层展示可用的 Agent/Power 类别枚举。

- `search` 的 keyword 必填，只查询云端资源；省略 `--type` 时不应用类别过滤。
- 该命令不注册 `--agent`，传入该 flag 时 Cobra 报告 unknown flag。
- `list` 默认列出 Cloud 资源，省略 `--type` 时不应用类别过滤，并保留每个版本；`list --installed` 读取本地安装清单并按本地记录解析类别。Cloud 行精确为 `<type>/<name>@<version>`，installed 行始终精确为 `<agent>/<type>/<name>@<version>`。
- `install` 省略 `--type` 时从 Cloud `List()` 返回的带版本 `PowerRef` 推断类别；未指定版本时从候选引用选择最新版并填充 `PowerRef.Version`；显式 `<power>=<version>` 可安装、升级或降级到指定版本；相同版本视为成功且不重复安装。
- `uninstall` 从本地安装清单解析类别；未找到本地安装记录时视为幂等成功；同名跨类别歧义要求显式 `--type`。

适用的维度均以 flag 承载，不作为子命令命名空间；`verb`（`search`/`list`/`install`/`uninstall`）是唯一的子命令层级。

本地 Power 操作的 Agent 检测与快照语义见 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md)。指定具体 `--agent` 时必须存在于当前快照；`--agent=all` 以快照为目标集合。多 Agent 执行语义见 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md)：install/uninstall 按 Agent 串行且 fail-fast；`list --installed --agent=all` 继续逐 Agent 读取。只有在全部读取尝试后成功 Release 的 partial error 才允许 CLI 渲染已成功记录；其他 fatal 或 Release error 均不输出业务行。

List 的 Config 清单版本必须遵循 [ADR-007](ADR-007-use-stateless-config-repository.md) 的精确 `vMAJOR.MINOR.PATCH` 契约。

## Consequences

- 正面影响：
  - Agent 集合可通过 Adapter 持续增添，不影响命令树结构和 `--help` 输出的稳定性，适合脚本化和自动化调用。
  - 默认 Cloud List 无需选择 Agent；installed List 在执行前刷新已安装 Agent 快照，能拒绝未安装目标并使 `all` 只覆盖当前可用 Agent。
  - `--type` 保持可选，并由各命令依据 Cloud 全量 `PowerRef` 列表或本地清单采用明确的缺省语义，减少常见场景下的输入负担。
  - Search 的 Cloud 查询不需要 Agent 选择；本地 Power 命令仍以稳定的 Agent flag 形式表达目标集合，生命周期层按命令数据来源实现类别过滤、推断或（仅 uninstall 的）本地消歧。
- 代价与风险：
  - `--agent=all` 下多 Agent 部分成功、部分失败的场景要求在成功解锁后才输出已成功记录，并以非零退出码报告汇总错误，避免用户误判整体命令是否成功。
- 后续动作：
  - 在 `internal/lifecycle` 中实现命令特定的 `--type` 缺省语义，以及「按 Agent 汇总结果」的公共逻辑，供 `install`/`uninstall`（及未来可能受影响的动词）复用。
  - Cloud `list` 接口返回带类别、名称与版本的全部 `PowerRef`，供 install 在 `--type` 缺省时推断并选择版本已确定的引用；`list --installed` 始终只按可选 `--type` 精确过滤，本地清单同名跨类别歧义仅由 uninstall 要求显式消歧。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Type 作为子命令命名空间，Agent 作为 Flag（如 `apm skill install <name> --agent=x`） | `--help` 可发现性更好，但 3 类型 × 4 动词需注册 12 个叶子命令，且类别通常可从云端元数据推断，强制用户先选类别造成重复输入。 |
| Agent 作为子命令命名空间，Type 作为 Flag（如 `apm claude-code install <name> --type=x`） | Agent 是通过 Adapter 可持续增添的开放集合，绑定为命令树节点会导致命令树随已编译 Adapter 变化，`--help` 不稳定；`all` 作为子命令名与真实 agent 名混杂，语义别扭，不利于脚本化。 |
| 全 Positional（如 `apm install <type> <agent> <name>`） | 顺序强绑定、无自解释性，缺省值难以表达，未来新增维度（如版本）会破坏既有脚本。 |

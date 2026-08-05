# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Decision Makers：liangjunzhao

## Context

APM 的 `search`/`list`/`install`/`uninstall` 等命令都需要同时确定两个维度：

- **目标 Agent**：通过 Adapter 适配，是开放、可增添的集合，且需要支持 `all`（对所有已支持目标 Agent 执行）。
- **Power 类别**：`skill`/`plugin`/`mcp`，是封闭的小集合，通常可从云端 Power 元数据中获取。

需要决定这两个维度如何映射到 CLI 语法：作为 flag、还是作为子命令命名空间。详见 [project-overview.md](../project-overview.md) 的 Architecture Snapshot（数据与接口）与待决事项「CLI 命令和 Power 类别交织」，以及 [ADR-001](ADR-001-use-go-as-implementation-language.md) 确定的 `cobra` 技术栈。

## Decision

CLI 语法采用：

```text
apm search <keyword> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm list --agent=<agent-id|all> [--type=<skill|plugin|mcp>] [--installed]
apm install <power>[=<version>] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

- `--agent`：必填（或由本地配置提供 default-agent 作为缺省值），取值为具体 Agent ID 或字面量 `all`。
- `--type`：可选，但缺省语义按命令的数据来源确定：`search` 与默认 `list` 不应用类别过滤；`install` 从云端 Power 元数据推断；`list --installed` 与 `uninstall` 从目标 Agent 的本地安装清单解析，仅当本地同名 Power 跨类别产生歧义时才报错并要求显式指定 `--type`。
- `--help` 顶层展示可用的 Agent/Power 类别枚举。

- `search` 的 keyword 必填，只查询云端资源；省略 `--type` 时不应用类别过滤。
- `list` 默认列出云端资源，省略 `--type` 时不应用类别过滤；`--installed` 改为读取本地安装清单并按本地记录解析类别。
- `install` 省略 `--type` 时从云端元数据推断类别；未指定版本时选择云端最新版；显式 `<power>=<version>` 可安装、升级或降级到指定版本；相同版本视为成功且不重复安装。
- `uninstall` 从本地安装清单解析类别；未找到本地安装记录时视为幂等成功；同名跨类别歧义要求显式 `--type`。

两个维度均以 flag 承载，不作为子命令命名空间；`verb`（`search`/`list`/`install`/`uninstall`）是唯一的子命令层级。

`--agent=all` 的执行语义见 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md)：install/uninstall 按 Agent 串行且 fail-fast；`list --installed` 继续逐 Agent 读取并汇总失败。

## Consequences

- 正面影响：
  - Agent 集合可通过 Adapter 持续增添，不影响命令树结构和 `--help` 输出的稳定性，适合脚本化和自动化调用。
  - `--type` 保持可选，并由各命令依据云端查询、云端元数据或本地清单采用明确的缺省语义，减少常见场景下的输入负担。
  - 四个动词共用同一套 flag 形式，命令树和 `--help` 保持稳定；生命周期层按命令数据来源实现类别过滤、推断或本地消歧。
- 代价与风险：
  - `--agent=all` 下多 Agent 部分成功、部分失败的场景，要求错误汇总与非零退出码的实现足够清晰，避免用户误判整体命令是否成功。
- 后续动作：
  - 在 `internal/lifecycle` 中实现命令特定的 `--type` 缺省语义，以及「按 Agent 汇总结果」的公共逻辑，供 `install`/`uninstall`（及未来可能受影响的动词）复用。
  - 云端 `get` 接口需要保证返回 Power 的类别元数据，供 install 在 `--type` 缺省时推断；本地清单查询需要检测同名跨类别歧义，供 `list --installed` 与 `uninstall` 要求显式消歧。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Type 作为子命令命名空间，Agent 作为 Flag（如 `apm skill install <name> --agent=x`） | `--help` 可发现性更好，但 3 类型 × 4 动词需注册 12 个叶子命令，且类别通常可从云端元数据推断，强制用户先选类别造成重复输入。 |
| Agent 作为子命令命名空间，Type 作为 Flag（如 `apm claude-code install <name> --type=x`） | Agent 是通过 Adapter 可持续增添的开放集合，绑定为命令树节点会导致命令树随已编译 Adapter 变化，`--help` 不稳定；`all` 作为子命令名与真实 agent 名混杂，语义别扭，不利于脚本化。 |
| 全 Positional（如 `apm install <type> <agent> <name>`） | 顺序强绑定、无自解释性，缺省值难以表达，未来新增维度（如版本）会破坏既有脚本。 |

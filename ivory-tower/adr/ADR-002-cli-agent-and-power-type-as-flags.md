# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Revised：2026-08-14
- Decision Makers：liangjunzhao

## Context

APM 的本地 Power 命令 `list --installed`/`install`/`uninstall` 需要同时确定两个维度：

- **目标 Agent**：通过 Adapter 适配，是开放、可增添的集合，且需要支持 `all`；本地 Power 操作仅对当前命令检测到的“已支持且已安装 Agent”执行。
- **Power 类别**：`skill`/`plugin`/`mcp`，是封闭的小集合，通常可从云端 Power 元数据中获取。

`search` 只查询 Cloud Power，不访问 Agent 本地状态，因此只使用 Power 类别维度。`fix` 只恢复唯一活动事务，不选择 Agent 或 Power，因此不使用这两个维度。

需要决定这两个维度如何映射到 CLI 语法：作为 flag、还是作为子命令命名空间。详见 [project-overview.md](../project-overview.md) 的 Architecture Snapshot（数据与接口）与待决事项「CLI 命令和 Power 类别交织」，以及 [ADR-001](ADR-001-use-go-as-implementation-language.md) 确定的 `cobra` 技术栈。

## Decision

CLI 语法采用：

```text
apm search <keyword> [--type=<skill|plugin|mcp>]
apm list [name] [--type=<skill|plugin|mcp>]
apm list [name] --all [--type=<skill|plugin|mcp>]
apm list [name] --installed [--agent=<agent-id|all>] [--type=<skill|plugin|mcp>]
apm show <name>[=<version>] [--type=<skill|plugin|mcp>] [--json]
apm install <power>[=<version>] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm fix
```

- `--agent`：适用于 installed `list`、`install`、`uninstall`；取值为具体 Agent ID 或字面量 `all`。默认 Cloud `list` 显式提供该 flag（包括空值）必须返回 `--agent requires --installed`；installed `list` 省略 `--agent` 时等价于 `all`，显式提供但为空/空白则返回 `--agent must not be empty`。
- `--type`：可选，但缺省语义按命令的数据来源确定：`search`、默认 `list` 与 `list --installed` 省略时均不应用类别过滤，提供时仅作精确类别过滤；`install` 从 Cloud `List()` 返回的带版本 `PowerRef` 推断；`uninstall` 从目标 Agent 的本地安装清单解析，仅当本地同名 Power 跨类别产生歧义时才报错并要求显式指定 `--type`。
- `--help` 顶层展示可用的 Agent/Power 类别枚举。
- `fix` 使用零个位置参数，不定义 `--agent`、`--type` 或其他命令专属 flag，只继承根命令的全局 `--verbose`；多余参数和这些未知 flag 都由 Cobra 拒绝。

- `search` 的 keyword 必填，只查询云端资源；省略 `--type` 时不应用类别过滤。
- 该命令不注册 `--agent`，传入该 flag 时 Cobra 报告 unknown flag。
- `list` 默认列出 Cloud 资源，省略 `--type` 时不应用类别过滤，并按 `(type,name)` 只保留最新版本；提供 `name --all`（或 `name -a`）时仅展开该名称的全部版本，缺少 name 使用 `--all` 会被拒绝。`--all` 不改变元数据渲染。`list --installed` 读取本地安装清单并按本地记录解析类别。Cloud 行精确为 `<type>/<name>@<version>`；installed 具体 Agent 行精确为 `<type>/<name>@<version>`（不重复 Agent 前缀），installed 省略 `--agent` 或 `--agent=all` 时按 Agent ID 字典序分组缩进输出，未安装与空清单分别以 `not installed`/`no powers installed` 标记。
- `show <name>[=<version>]` 只查询 Cloud catalog，默认展示最新版本；`=version` 选择精确版本，跨类别同名时必须提供 `--type`。文本输出包含 type、name、version、description、tags，`--json` 输出单对象。
- `list` 的可选位置参数 `name` 按区分大小写的原始字符串精确匹配 `PowerRef.Name`，对默认 Cloud 列表与 `list --installed` 均生效；省略时不应用名字过滤，`name` 不是 flag，且不接受版本。
- `install` 的 Name 与 Cloud `PowerRef.Name` 按区分大小写的原始字符串精确匹配；省略 `--type` 时从 Cloud `List()` 返回的带版本 `PowerRef` 推断类别。若同名候选跨 Type，必须在任何版本选择（包括显式版本）前报歧义并要求 `--type`。未指定版本时从确定类别的候选引用选择最新版并填充 `PowerRef.Version`；显式 `<power>=<version>` 只接受规范 `vMAJOR.MINOR.PATCH`，可安装、升级或降级到指定版本；相同版本视为成功且不重复安装。
- `uninstall` 不接受版本；位置参数中任何 `=` 都在创建 lifecycle 前被拒绝并返回 `uninstall does not accept a version`。它为每个目标 Agent 分别从本地安装清单解析 Type 和 Version：显式 `--type` 只精确匹配该类别并取清单第一条同名记录；省略时按当前 Agent 内匹配到的去重类别消歧，只有一个类别时同样取该类别第一条。不同 Agent 可解析为不同 Type/Version。当前 Agent 无匹配记录时同步 Warn，记为 Agent-only `unexecuted` 并继续，整体仍是幂等成功；同名跨类别歧义则要求显式 `--type`。

适用的维度均以 flag 承载，不作为子命令命名空间；`verb`（`search`/`list`/`install`/`uninstall`/`fix`）是唯一的子命令层级。Fix 没有 Agent/Power 维度，也不为这些维度注册 flag。

本地 Power 操作的 Agent 检测与快照语义见 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md)。指定具体 `--agent` 时必须存在于当前快照；`--agent=all` 以快照为目标集合。多 Agent 执行语义见 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md)：install/uninstall 按 Agent 串行且 fail-fast；`list --installed`（省略 `--agent` 或 `--agent=all`）无锁遍历全部受支持 Agent，区分未安装/空清单并读取已安装 Agent 配置，完成全部读取尝试后即可返回成功记录与 `PartialListError`。installed List 的 fatal error 不返回业务行；它没有 Release error 或成功 Release 前提。

List 的 Config 清单版本必须遵循 [ADR-007](ADR-007-use-stateless-config-repository.md) 的精确 `vMAJOR.MINOR.PATCH` 契约。

Install 的每个 Agent 结果精确渲染为 `<agent>/<type>/<name>@<version> <outcome>`，其中 outcome 为 `installed`、`upgraded`、`downgraded`、`unchanged`、`failed` 或 `unexecuted`。`all` 按 Agent ID 字典序串行并 fail-fast：首个业务失败后补齐当前 `failed` 和剩余 `unexecuted`。只有成功 Release 后的 Install batch error 才允许 CLI 先渲染该完整结果、再以非零状态返回；fatal 或 Release error 均抑制业务行。

Uninstall 的结果根据 lifecycle 是否已确定本地 `PowerRef` 精确渲染为两种行：

```text
<agent>/<type>/<name>@<version> <completed|failed|unexecuted>
<agent> <failed|unexecuted>
```

清单无记录和前序 Agent 失败后未执行使用 Agent-only `unexecuted`；清单读取或类别消歧失败使用 Agent-only `failed`；选定引用后的完成或失败保留完整引用。同类别重复不会产生歧义，成功 transaction 会删除选定 Type/Name 的全部重复清单记录。`all` 按 Agent ID 字典序串行并 fail-fast。只有成功 Release 后的 Uninstall batch error 才允许 CLI 先渲染完整结果、再以非零状态返回；fatal 或 Release error 均抑制业务行。

## Consequences

- 正面影响：
  - Agent 集合可通过 Adapter 持续增添，不影响命令树结构和 `--help` 输出的稳定性，适合脚本化和自动化调用。
  - 默认 Cloud List 无需选择 Agent；installed List 在执行前刷新已安装 Agent 快照，能拒绝未安装的具体目标；省略 `--agent` 或 `--agent=all` 时覆盖全部受支持 Agent 并区分未安装与空清单。
  - `--type` 保持可选，并由各命令依据 Cloud 全量 `PowerRef` 列表或本地清单采用明确的缺省语义，减少常见场景下的输入负担。
  - Search 的 Cloud 查询不需要 Agent 选择；本地 Power 命令仍以稳定的 Agent flag 形式表达目标集合，生命周期层按命令数据来源实现类别过滤、推断或（仅 uninstall 的）本地消歧。
- 代价与风险：
  - `--agent=all` 下 install/uninstall 的业务结果仍以成功解锁为渲染前提；installed List 无锁收集部分失败并以成功记录加非零退出码报告 `PartialListError`。两类边界不同，调用方不能把 partial 结果误认为整体成功。
- 后续动作：
  - 在 `internal/lifecycle` 中实现命令特定的 `--type` 缺省语义，以及「按 Agent 汇总结果」的公共逻辑，供 `install`/`uninstall`（及未来可能受影响的动词）复用。
  - Cloud `list` 接口返回带类别、名称与版本的全部 `PowerRef`，供 install 在 `--type` 缺省时推断并选择版本已确定的引用；`list --installed` 始终只按可选 `--type` 精确过滤，本地清单同名跨类别歧义仅由 uninstall 要求显式消歧。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Type 作为子命令命名空间，Agent 作为 Flag（如 `apm skill install <name> --agent=x`） | `--help` 可发现性更好，但 3 类型 × 4 动词需注册 12 个叶子命令，且类别通常可从云端元数据推断，强制用户先选类别造成重复输入。 |
| Agent 作为子命令命名空间，Type 作为 Flag（如 `apm claude-code install <name> --type=x`） | Agent 是通过 Adapter 可持续增添的开放集合，绑定为命令树节点会导致命令树随已编译 Adapter 变化，`--help` 不稳定；`all` 作为子命令名与真实 agent 名混杂，语义别扭，不利于脚本化。 |
| 全 Positional（如 `apm install <type> <agent> <name>`） | 顺序强绑定、无自解释性，缺省值难以表达，未来新增维度（如版本）会破坏既有脚本。 |

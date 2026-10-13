# Feature 032：按本地已安装清单全量升级（`apm upgrade`）

- Status：Accepted
- Date：2026-10-13
- Related ADRs：[ADR-045](../../adr/ADR-045-add-upgrade-command-for-installed-powers.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)（锁与恢复边界）、[ADR-022](../../adr/ADR-022-unify-lifecycle-entry-state-semantics.md)（安装入口状态语义）、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)（清单 Repository）
- Related Features：[Feature 010](../010-lifecycle-install/spec.md)（Install 生命周期）、[Feature 009](../009-lifecycle-list/spec.md)（installed 清单来源）、[Feature 025](../025-unified-lifecycle-state-semantics/spec.md)（共享状态语义）、[Feature 004](../004-global-slog-logging/spec.md)（warning 通道）

## 1. 目标

新增一等命令 `apm upgrade`：在一次全局锁区间内，以本地已安装清单为唯一输入，对账一次不可变
目录快照，对每条已安装 Power 升级到云端最新版本。

成功标准：

- `apm upgrade` 省略 `--agent` 时覆盖全部**已安装** Agent，按 Agent ID 字典序串行。
- 每条已安装 Power 升到 catalog 最新版；已是最新版输出 `up-to-date`；云端已下架输出
  `skipped` 与一条 warning。
- 目标版本低于本地版本时**不降级**，输出 `skipped` 与一条 warning。
- 重复执行幂等：第二次全部为 `up-to-date` 且零 Cloud 内容下载。
- 单条失败不中断整批（记 `failed` 后继续），但检测到 journal 残留时立即停止，剩余记
  `unexecuted` 并提示 `apm fix`。
- `--dry-run` 不写任何文件、不建事务、不下载内容；`--json` 输出稳定机器契约。
- 既有 `install`/`list`/`uninstall`/`fix` 的行为与测试全部不变。

## 2. 范围

### 2.1 Included

- `upgrade` CLI 端口：位置参数拒绝、`--agent`（默认 all）、`--type`、`-y/--yes`、`--dry-run`、
  `--json` 的解析与校验。
- 执行前确认询问（`apt upgrade` 风格）、非交互/`--json` 的 fail-closed 门禁，以及 isatty 语义的
  终端判定（Unix termios、Windows `GetConsoleMode`）。
- 阶段性进度：lifecycle 上报结构化阶段事件，CLI 渲染到 stderr，`--json` 下静默。
- CLI 契约类型 `UpgradeOutcome`/`UpgradeItem`/`UpgradeResult`/`UpgradeFailure`/`UpgradeBatchError`、
  `UpgradeStage`/`UpgradeProgress`/`UpgradePrompt`/`UpgradeOptions`/`ErrUpgradeDeclined`
  与 `UpgradeLifecycle` 端口。
- 文本渲染 `<agent>/<type>/<name>@<version> <outcome>`、跳过原因、省略逐条 `up-to-date`
  与末尾计数汇总行；`--json` 数组记录保留全部条目。
- 纯函数升级计划器：manifest 分组 × 目录快照 → 有序升级计划。
- `UpgradeService` 生命周期：单锁区间内的 Recover、`RefreshInstalled`、`SnapshotLocked`、
  计划、`--dry-run` 提前返回、逐条执行、journal 残留停止、Release gate。
- 组合根接线：`cmd/apm/main.go` 的 upgrade 生命周期构造与 `cli.New` 注册。
- 复用 install 的 per-type 安装路径、Adapter、Cache、Plugin Resource、Transaction。
- CLI、planner、lifecycle、组合根与端到端的自动化测试。
- ADR-045、BUILD.md、Project Overview 与 `docs/Changelog.md` 的契约同步。

### 2.2 Excluded

- 不支持位置参数 `[name]`；单资源升级/降级/精确版本安装继续由 `install` 承担。
- 不支持 `--allow-downgrade`；目标低于本地时只 warning 并跳过。
- 不支持 `--to`、`--patch`、`--minor` 等版本约束策略。
- 不支持 `--prune`：不清理云端已下架或本地多余的资源（属 uninstall 职责，见 ADR-045）。
- 不新增 `list --outdated`（后续 feature 复用 planner 的接入点）。
- 不修改 install 的 outcome 词表与 fail-fast 语义。
- 不修改 `internal/transaction`、`internal/adapter`、`internal/cache`、`internal/pluginresource`
  的公共契约，也不新增事务或 journal 类型。
- 不修改 config Repository 的四个业务接口，不新增清单迁移。
- 不新增环境变量或 Cloud provider。

## 3. CLI Contract

### 3.1 用法

```text
apm upgrade [--agent=<agent-id|all>] [--type=<skill|plugin|mcp|expert>] [-y|--yes] [--dry-run] [--json]
```

- 零个位置参数；出现任何位置参数由 Cobra 在创建 lifecycle 前以
  `accepts 0 arg(s), received N` 拒绝。
- `--agent` 省略或取空串等价于 `all`；取值必须是已知 Agent ID（`all`、`bibit`、
  `claude-code`、`codex`、`hermes-agent`）之一，否则报 `invalid --agent`。
- `--type` 省略时不过滤类别；取值为 `skill`/`plugin`/`mcp`/`expert`（改名前的 `agent`
  仍是弃用别名，就地改写并 warning，见 [ADR-044](../../adr/ADR-044-rename-agent-power-type-to-expert.md)）；
  非法值报 `invalid --type`。
- `--type=expert` 只支持 `--agent` 省略或 `bibit`，其它值（含 `all`）报错。
- `-y`/`--yes` 跳过执行前的确认询问。
- `--dry-run` 只输出计划，不落盘，也永不要求 `--yes`。
- `--json` 输出机器可读数组；warning 与跳过原因仍走 stderr，不污染 stdout。
  机器模式不做交互询问，因此 `--json` 必须与 `--yes` 同时给出，否则 fail closed。
- `--verbose` 为根命令全局选项，不在 upgrade 单独注册。

### 3.2 确认询问与门禁

筛选出「需要升级」的条目后、执行任何事务前，命令按 `apt upgrade` 的方式列出清单并提问：

```text
The following powers will be upgraded:
  codex/skill/foo-before-summary@v0.0.1 -> v0.1.0
  hermes-agent/plugin/demo-plugin@v0.1.0 -> v0.2.0
2 planned, 3 up-to-date, 1 skipped.
Do you want to continue? [Y/n]
```

- 空回车与 `y`/`yes`（不区分大小写、去空白）同意；`n`/`no` 拒绝；其它输入重新提问。
- EOF 视为拒绝：绝不把「输入结束」当作同意。
- 答 `n` 时不做任何变更、不渲染业务行，并以非零状态退出（脚本据此判断「确实没升级」）。
- 计划中没有待升级条目时不提问，避免对已最新的系统无谓阻塞。
- 确认文本写入 stderr，stdout 仍只承载业务结果。

门禁规则（唯一判定点，位于 CLI 层，在创建 lifecycle 之前）：

| 条件 | 行为 |
| --- | --- |
| 省略 `--yes` 且不是 `--dry-run`，stdin 是真终端 | 提问 |
| 省略 `--yes` 且不是 `--dry-run`，stdin 不是终端 | 报 `upgrade requires --yes when not running interactively` |
| 省略 `--yes` 且给出 `--json` | 报 `upgrade requires --yes when --json is used`（机器模式不提问） |
| `--yes` 或 `--dry-run` | 不提问 |

交互性判定必须用 `isatty` 语义：`os.ModeCharDevice` 对 `/dev/null` 也为真，用它判定会让
`apm upgrade < /dev/null` 与 CI 管道误入确认分支并永远读不到答复。

### 3.3 阶段性进度

长时间操作在 stderr 输出阶段进度，避免命令看起来无响应：

```text
upgrade: reading-installed
upgrade: syncing-catalog
upgrade: planning
upgrade: [1/2] codex/skill/foo-before-summary@v0.0.1 -> v0.1.0
```

- 进度走 stderr（**不能用 `slog.Info`**：info 级按 ADR-004 路由到 stdout，会污染 `--json`）。
- `--json` 下不输出进度。
- `--dry-run` 不输出执行阶段进度。
- 进度是诊断而非业务结果，允许在锁内输出；业务行仍只在成功 Release 后渲染。

### 3.4 目标版本选择

对每条已安装记录 `(agent, type, name, current)`：

1. 在该类型的目录快照中收集同名候选版本。
2. 无候选：`skipped`，`skip=not-in-catalog`，warning。
3. 有候选：按 SemVer 取最高版本为 `target`。
4. `target > current`：进入安装路径；成功结果按 §3.4 如实映射。
5. `target == current`：`up-to-date`，不访问 Adapter/Transaction/内容边界。
6. `target < current`：`skipped`，`skip=newer-local`，warning，不替换（`target` 保持 `current`，
   表示本次执行后本地停留的版本）。
7. 当前版本不在候选（云端已下架旧版本）且类型为 MCP：`skipped`，`skip=not-in-catalog`。
   只有 MCP 的版本切换依赖旧版本目录 metadata 以支持回滚（`snapshot.Metadata(previous.Ref)`），
   因此在计划阶段前置为可读原因；其余类型的替换不依赖旧条目，仍按规则 4 正常升级，避免无谓阻断。

### 3.5 结果映射

批量升级复用安装路径的结果，必须如实映射，不一律记为 `upgraded`：

| 安装结果 | 升级结果 | 含义 |
| --- | --- | --- |
| `upgraded` | `upgraded` | 版本替换成功 |
| `installed` | `installed` | 受管目标此前被删除，按首次安装补齐 |
| `downgraded` | `downgraded` | 执行期事实与计划不一致，仍如实报告 |
| `unchanged` | `up-to-date` | 目标版本已满足 |

### 3.6 输出

文本模式逐行输出，`<version>` 为计划目标版本（跳过时为本地当前版本）：

```text
<agent>/<type>/<name>@<version> <installed|upgraded|downgraded|planned|skipped|failed|unexecuted>
```

**`up-to-date` 不再逐条打印**：批量场景下它们会淹没真正需要关注的条目。其计数由末尾汇总行
承接。`planned` 只在 `--dry-run` 出现，表示计划要升级但未做变更。`skipped` 行在同一行追加
机器可读原因：

```text
codex/skill/foo@v0.0.1 skipped skip=not-in-catalog
hermes-agent/plugin/demo@v0.1.0 skipped skip=newer-local
```

末尾汇总行按固定顺序 `planned、installed、upgraded、downgraded、up-to-date、skipped、failed、
unexecuted` 只列出计数非零的标签；全部为零时输出 `nothing to upgrade`：

```text
2 upgraded, 3 up-to-date, 1 skipped.
```

`--json` 输出顶层数组，**保留全部记录（含 `up-to-date`）**：消费者需要能自行统计「一共装了
多少、多少已最新」，机器契约不因人类可读性而缩水。每条记录字段恒定存在（空值为 `""`），
且不追加文本汇总行：

```json
[
  {
    "agent": "codex",
    "type": "skill",
    "name": "foo-before-summary",
    "current_version": "v0.0.1",
    "target_version": "v0.1.0",
    "outcome": "upgraded",
    "skip": ""
  }
]
```

`--dry-run` 使用同一格式与同一 JSON 形状，只表示计划。

### 3.7 退出状态与失败聚合

- 全部 `upgraded`/`up-to-date`/`skipped`：退出码 0。
- 存在至少一条 `failed`：先渲染完整结果，再以非零状态退出；错误为 `UpgradeBatchError`，
  其 `Unwrap()` 返回全部失败原因，使 `errors.Is` 可匹配任一底层原因。
- fatal error（锁竞争、Recover 失败、快照失败、计划失败）与 Release 失败：不输出业务行。
- journal 残留导致的提前停止：已完成的条目照常渲染，剩余条目记 `unexecuted`，并以非零状态
  退出（属 `failed` 一类的批处理失败）。

## 4. 输出与日志

- 跳过原因与降级提醒使用 `slog.Warn`，与 [ADR-004](../004-global-slog-logging/spec.md) 的
  全局日志约定一致；`--json` 时 stdout 仍只有 JSON。
- journal 残留停止时输出 `transaction recovery required` 类 warning 并提示 `run apm fix`。
- 锁竞争按既有 `lock.ErrLocked` 语义报错，不重试。

## 5. 验收标准

| 编号 | 场景 | 期望 |
| --- | --- | --- |
| UPG-01 | 已装 v0.0.1，云端有 v0.1.0 | `upgraded`，manifest 与磁盘更新为新版本 |
| UPG-02 | 重复执行 | 全部 `up-to-date`，零内容下载，零事务 |
| UPG-03 | 云端无该资源 | `skipped skip=not-in-catalog` + 一条 warning，退出码 0 |
| UPG-04 | 本地版本高于云端最新 | `skipped skip=newer-local` + 一条 warning，不替换 |
| UPG-05 | 省略 `--agent` | 覆盖全部已安装 Agent，按 ID 字典序 |
| UPG-06 | `--type` 过滤 | 只处理该类别，其余不动 |
| UPG-07 | `--dry-run` | 输出计划；无 journal、无 staging、无 manifest 变更、无下载 |
| UPG-08 | `--json` | 稳定数组契约，warning 不进 stdout |
| UPG-09 | 单条失败 | 该条 `failed`，后续条目继续执行 |
| UPG-10 | journal 残留 | 立即停止，剩余 `unexecuted`，提示 `apm fix` |
| UPG-11 | 位置参数 | Cobra 拒绝，退出码非零，无业务输出 |
| UPG-12 | 无已安装 Agent / 无已安装 Power | 成功退出，空结果 |
| UPG-13 | `--type=expert --agent=codex` | 校验失败，不创建 lifecycle |
| UPG-14 | 四类 Power 混合 | 每类各自 upgraded/up-to-date，互不影响 |
| UPG-15 | 交互终端且无 `--yes` | 列出待升级清单并询问；答 `y`/空回车继续，答 `n` 中止且零变更、非零退出 |
| UPG-16 | 非终端 stdin 且无 `--yes` | 报 `upgrade requires --yes when not running interactively`，不创建 lifecycle |
| UPG-17 | `--json` 且无 `--yes` | 报 `upgrade requires --yes when --json is used` |
| UPG-18 | `--yes` | 不提问，直接执行 |
| UPG-19 | 确认答 `n` 或 stdin EOF | 零事务、零变更、不渲染业务行、非零退出 |
| UPG-20 | 无待升级条目 | 不提问，直接给出汇总 |
| UPG-21 | 文本输出 | 不打印 `up-to-date` 行，末尾给出计数汇总 |
| UPG-22 | `--json` 输出 | 仍含 `up-to-date` 记录，且不追加文本汇总行 |
| UPG-23 | 阶段性进度 | 阶段与每条进度写 stderr；`--json` 下不输出 |

## 6. 依赖与影响

- 复用：`installForAgent` 及其四类分支、`catalog.Snapshot`、`VersionSelector`、
  `transaction.Manager`、`config.Repository`、全局锁与 `CatalogManager.SnapshotLocked`。
- 新增：`internal/cli/upgrade.go`、`upgrade_contract.go`、`upgrade_render.go`、
  `internal/lifecycle/upgrade.go`、`upgrade_plan.go`，以及对应测试。
- 修改：`internal/cli/root.go`（variadic factory 接线）、`cmd/apm/main.go`（组合根）、
  `BUILD.md`、`docs/Changelog.md`、`ivory-tower/project-overview.md`。
- 兼容性：`cli.New` 以 variadic 参数扩展，既有调用点无需改动；install/list/uninstall/fix
  的命令面、输出与错误语义保持不变。

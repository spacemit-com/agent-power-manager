# Architecture Decision Record

- Status：Accepted
- Date：2026-10-13
- Decision Makers：liangjunzhao

## Context

APM 已能安装、升级与降级单个 Power（[ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 的
`install <power>[=<version>]`，状态语义见 [ADR-022](ADR-022-unify-lifecycle-entry-state-semantics.md)），
也有只读的已安装清单视图（[ADR-007](ADR-007-use-stateless-config-repository.md) 的
`Repository.List`）。但用户想让「本机已装的一切」保持在云端最新版时，只能用外部脚本读取
`list --installed --json` 与 `list --json` 自行做版本对账，再逐条调用 `apm install`。

这种编排有三个无法在脚本层解决的问题：每条 `install` 各自获取全局锁并各自同步一次目录
（[ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 的单锁区间与
[ADR-021](ADR-021-cache-versioned-resource-catalog.md) 的目录同步都因此被重复触发）；
脚本无法判断「云端已下架本地这个资源」与「云端最新版比本地还旧」这两种需要不同处理的
情况；脚本也无法在一条失败后判断全局 journal 是否仍残留，从而无法安全地继续下一条。

本决策服务于 Feature 032，并遵守 ADR-003 已确定的锁与恢复边界。

## Decision

APM 新增一等命令 `apm upgrade`，把「本机已安装清单」作为唯一输入，对账一次不可变目录快照，
再复用既有 install 状态机逐条执行。

```text
apm upgrade [--agent=<agent-id|all>] [--type=<skill|plugin|mcp|expert>] [--dry-run] [--json]
```

边界固定如下：

- **不接受位置参数**。`upgrade` 不做单资源版本选择；针对单个资源的升级、降级与精确版本
  安装仍由 `install <power>[=<version>]` 承担。两者是「收敛全部」与「指定一个」的分工，
  不在同一命令上叠加。
- **省略 `--agent` 等价于 `all`**，与 `list --installed` 的分组模式一致，覆盖当前快照中全部
  已安装 Agent，按 Agent ID 字典序串行。`--type=expert` 仍只支持 `bibit`。
- **只升不降**。目标版本低于本地版本时不替换，记为 `skipped` 并记录一次 warning。APM 不提供
  `--allow-downgrade`：降级是有意为之的版本决策，必须由用户显式执行 `install <name>=<version>`。
- **云端无该资源时跳过并 warning**，不使整条命令失败。云端一次下架不应让本机的其余升级
  全部不可执行。
- **单条失败继续下一条**，与 install 的 fail-fast 不同。但**一旦检测到 journal 残留
  （`transaction.ErrActiveTransaction` 或 Inspect 报告 Active），立即停止**，剩余条目记为
  `unexecuted` 并提示 `apm fix`。理由见 ADR-003：未收敛的事务存在时无法安全创建下一个事务，
  「继续下一条」在这一点上不可用。
- **计划与执行共用同一个 `SnapshotLocked` 结果**。命令在一次 Acquire/Release 内完成
  Recover、`RefreshInstalled`、目录快照、计划与串行执行；计划中的目标条目就是执行时交给
  Adapter/Transaction 的同一份 `CatalogEntry`，避免计划与执行之间的目录漂移。
- **`--dry-run`** 只输出计划：不创建事务、不下载内容、不写 manifest 或 journal。
- **outcome 词表独立于 install**。`upgrade` 使用 `installed`、`upgraded`、`downgraded`、`planned`、
  `up-to-date`、`skipped`、`failed`、`unexecuted`；install 的 `installed`/`upgraded`/`downgraded`/
  `unchanged`/`failed`/`unexecuted` 契约不变。两者不共用「unchanged」是因为它同时表达「显式请求
  即当前版本」与「计划判定无需动作」，在批量收敛场景下无法区分。`planned` 只用于 `--dry-run`，
  避免计划输出声称变更已发生；复用安装结果时**如实映射**——受管目标此前被删除而按首次安装补齐
  记为 `installed`，不伪装成 `upgraded`。
- **空输入不是错误**。没有已安装 Agent 或没有已安装 Power 时，`upgrade` 成功退出并输出空
  结果。这与 `install --agent=all` 在无已安装 Agent 时报错不同：install 缺少目标即无法完成
  用户请求，而 upgrade 的请求是收敛现状，现状为空即已收敛。
- **`--json` 是长期机器契约**。stdout 只承载结果记录，warning 与跳过原因走 stderr 的
  `slog.Warn`；记录字段与空值策略一旦发布即按 ADR-040 的展示字段惯例稳定。

复用边界：`upgrade` 复用 `InstallService` 的 per-type 安装路径（`installForAgent` 及其
Skill/MCP/Plugin/Expert 分支）、Adapter、Cache、Plugin Resource 与 Transaction。它不新增
事务类型、不放宽 [ADR-009](ADR-009-centralize-lifecycle-transactions.md) 的 journal 不变量、
也不修改 config Repository 的四个业务接口。

## Consequences

- 正面影响：一条命令即可把本机全部已安装 Power 收敛到云端最新版，且全程只同步一次目录、
  只持有一个锁区间、只做一次恢复；跳过语义让云端下架与本地领先变成可读结果而非报错；`--dry-run`
  让批量变更可以先审阅计划。
- 正面影响：升级复用 install 的状态机与事务，不引入第二套安装语义；`up-to-date` 与 `skipped`
  新增词表把「无需动作」与「有意不动」分开，批量结果可读。
- 代价与风险：`upgrade` 是比 `install` 更宽的批量写入面，一次调用可能触及多个 Agent 的多条
  Power；只升不降意味着云端回滚不会自动跟随，用户需要显式 install 才能降级；「失败继续」使
  一批结果可能混合成功与失败，调用方必须读 outcome 而不能只看退出码。
- 代价与风险：MCP 的版本切换依赖旧版本的目录 metadata（`snapshot.Metadata(previous.Ref)`），
  当云端已下架本地当前版本时该条无法完成替换——planner 把这一情况显式分类为 `skipped`
  （`not-in-catalog`），把既有约束前置为可读原因而不是晦涩失败。这条限制**只适用于 MCP**：
  Skill/Plugin/Expert 的替换不依赖旧目录条目，因此旧版本下架不会阻断它们的升级，避免因一条
  与本机无关的限制过度阻断升级。Plugin 的旧版本制品必须仍在本地 Cache（安装路径只
  `Locate`/`Exists`、不回源），缓存被手工清理时该条为 `failed` 且不中断整批。
- 后续动作：Feature 032 实现 CLI 端口、planner、生命周期与组合根接线；BUILD.md、Project
  Overview 与 `docs/Changelog.md` 引用本决策；`--to`/`--patch`/`--minor`/`--prune` 与
  `list --outdated` 留待后续 feature，planner 的纯函数边界即为它们的接入点。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 外部脚本编排现有命令 | 每条 install 重复获取锁并重复同步目录；无法区分云端下架与本地领先；无法在失败后判断 journal 是否残留，因而无法安全续跑。 |
| 给 `install` 增加「省略 power 即全量升级」模式 | 把两种输入语义压进同一 CLI 端口，`Install()` 内部分叉，`Flags` 布尔旗标随策略膨胀，且 install 的 fail-fast 与 upgrade 的「失败继续」在同一入口互相冲突。 |
| 复用 install 的 outcome 词表（含 `unchanged`） | `unchanged` 会同时表示「显式请求即当前版本」与「批量计划判定无需动作」，批量结果无法区分；也让 install 的既有契约承担 upgrade 的语义。 |
| 提供 `--allow-downgrade` 让 upgrade 也能降级 | 降级是用户的版本决策而非收敛默认；把降级并入 upgrade 会让「升级」命令在云端回滚时静默降版本，风险高于便利。 |
| 云端无该资源时整条命令失败 | 云端单次下架会让本机全部待升级项不可用，违背「尽量收敛」的意图；跳过并 warning 保留可执行部分。 |
| 单条失败即停止（沿用 install 的 fail-fast） | 批量升级条目多，一条与本机无关的失败不应阻断其余；真正需要停止的条件是未收敛的全局事务，而非业务失败。 |
| 支持 `apm upgrade <name>` 单资源升级 | 与 `install <name>=<version>` 职责重叠，且会引入「不指定版本时 upgrade 到 latest」与 install 的歧义；单资源路径保留给 install 更清晰。 |
| 同时提供 `--prune` 清理云端已下架资源 | 删除本地资源属于 uninstall 的职责，与「升级」混在一条命令会引入误删风险，需要独立的职责边界与 ADR；留待后续演进。 |

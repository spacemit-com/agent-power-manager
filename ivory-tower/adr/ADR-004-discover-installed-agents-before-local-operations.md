# Architecture Decision Record

- Status：Accepted
- Date：2026-08-05
- Revised：2026-08-12
- Decision Makers：liangjunzhao

## Context

APM 通过编译期 Adapter Registry 确定已支持的 Agent，但仅知道“已支持”不足以安全执行本地 Power 操作：目标 Agent 可能尚未安装，或已被外部卸载。`list --installed`、`install` 和 `uninstall` 需要在读取清单或修改 Agent 安装目录前刷新当前机器上实际可用的目标集合。

本决策补充 [ADR-001](ADR-001-use-go-as-implementation-language.md) 的 Adapter 职责、[ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 的 `--agent` 目标语义，以及 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 的多 Agent 串行执行集合。

## Decision

Adapter Registry 中的 Agent ID 在编译期保证唯一。每个 Adapter 实现 `DetectAgent() (bool, error)`，只检查对应 Agent 根目录（例如 `~/.claude`）是否存在且为目录；目录存在即视为 Agent 已安装，不检查目录是否为空，也不验证配置、可执行文件、Power 子目录或运行状态。

Adapter Registry 提供公共刷新流程：先复制已注册的唯一 Agent ID 列表并按原始字符串字典序排序，再按该顺序逐个调用 `DetectAgent`；成功快照只包含检测为已安装的 Agent，并保持同一字典序。Registry 的内置配置存储顺序不得成为公开检测或返回顺序。快照仅在当前命令内有效、不可变且不持久化。目录不存在是正常的未安装结果；路径存在但不是目录，或发生权限、I/O、路径解析错误时，刷新失败。失败日志必须包含 Agent 和原因，调用方释放全局锁并非零退出，不执行用户原命令。扫描期间进程中断时，内核释放锁，内存快照直接丢弃，下次命令重新扫描。

公共刷新流程在全局锁内按以下位置调用：

- `list --installed` 在 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 定义的锁区间内、Recover 与实时 Warn 后刷新；默认 Cloud `list` 不刷新。
- 已接线的 `install` 和 `uninstall` 都在各自的单锁 lifecycle 内、Recover 与同步 Warn 之后刷新，并在解析或操作 Power 前固定当前命令的目标快照。
- `search` 不刷新。

指定具体 `--agent` 时，该 Agent 必须存在于快照，否则返回“未受支持或未安装”错误，由 CLI 统一记录并非零退出。`list --installed --agent=all` 遍历快照，空快照返回空结果并正常退出；`install/uninstall --agent=all` 在空快照时返回“没有已支持且已安装的 Agent”错误并非零退出，非空时按快照中的 Agent ID 升序串行执行。

单次命令面对的 Power 类型固定，不需要按 Power 类型重复扫描 Agent。APM 的感知边界仅限命令执行时检查已注册 Agent 根目录是否存在；APM 不负责 Agent 的安装、卸载、实时监控、运行时感知或 Power 更新通知，也不因 Agent 未安装而自动删除其历史 Power 清单。List 的条件式 Agent CLI 语法和 partial 输出边界见 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md)，清单内容校验见 [ADR-007](ADR-007-use-stateless-config-repository.md)。

## Consequences

- 正面影响：APM 能在每次本地 Power 操作前响应 Agent 的外部安装或卸载，避免向未安装 Agent 写入 Power；快照不持久化，不会引入第二份易过期状态。
- 代价与风险：相关命令每次都需要扫描全部注册 Agent；根目录存在只代表 Agent 已安装，不保证安装完整或运行正常；快照生成后发生的外部变化要到下一条命令才能感知。
- 后续动作：在 Adapter Registry 与 Core Lifecycle 类图、公共 Agent discovery 流程图、list/install/uninstall 生命周期、C4 properties 和项目概览中引用并表达本决策。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 持久化已安装 Agent 列表 | 外部安装或卸载会使列表过期，还需要额外的一致性与恢复机制；命令内快照已满足需求。 |
| 由 Lifecycle 直接遍历 Adapter | 将注册表遍历和 Agent 检测细节泄漏到命令编排中，三条命令容易形成不一致的扫描行为。 |
| 新增独立 Agent Discovery 顶层组件 | 当前唯一检测来源是已注册 Adapter 的 Agent 根目录，新增组件超出所需复杂度。 |
| 检查目录内容、配置或可执行文件 | 会把“已安装”扩大为安装完整性或运行时健康检查，不符合本决策的感知边界。 |

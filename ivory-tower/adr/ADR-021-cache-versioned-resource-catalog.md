# Cache a Content-addressed Resource Catalog

- Status：Accepted
- Date：2026-08-25
- Decision Makers：APM maintainers
- Related Feature：[Feature 024](../features/024-catalog-cache/spec.md)
- Related APC contract：[APC-API.md](../../APC-API.md)、[ADR-025](ADR-025-apc-api-client-contract.md)（原 APC 仓库已停止维护，其 ADR-004 不再链接）

## Context

APM 的 Cloud list/search 目前每次遍历 APC 公开资源 cursor；未指定类型的 install 也依赖完整
列表，指定类型则调用 latest 或 exact-metadata。资源规模增加后，这些网络读取重复、接口面
过宽，而且 APC 无法在单个分页响应中返回完整目录。

APC 已有整体资源更新时间接口的历史设计，但 APM 尚未消费，也没有持久目录缓存。新增
缓存必须保持 Provider 可替换、全局锁和事务恢复边界，不能把 catalog 与 Skill/Plugin/Agent
制品缓存、MCP definition 或安装 manifest 混为一体。

## Decision

APM 新增独立 `internal/catalog` Manager，以 `~/.apm/catalog.json` 原始字节 SHA-256 作为
唯一同步标记。Remote 和 Local Cloud Provider 只提供 version、完整 catalog 与精确 content；
list/search/install 从 Manager 返回的不可变 Snapshot 完成目录查询，Cloud `List/Resolve`
及公开 latest/series/exact-metadata 调用全部删除。

Catalog 按 Skill、Plugin、MCP、Agent 分组，各组维护独立 schema version。Manager 复用
`apm.lock` 互斥同步：list/search 使用自加锁入口，已持完整生命周期锁的 install 使用显式
locked 入口以避免死锁。锁继续采用非阻塞 Acquire，已占用时返回 `ErrLocked`，不等待或
fallback。同步失败时，有效旧 catalog 可供 stale list/search 和已有制品缓存的离线 install
使用，并输出一次 warning；无有效 catalog 或 caller 主动取消时失败。

Repository 只接受物理普通文件，以 `0600`、100 MiB 上限、严格 schema 和精确 SHA-256/size
校验加载与原子发布 `catalog.json`；symlink、hardlink、可检测的目录项替换和不可信目录链均
fail closed。目录链的“不可信”判定（拒绝对其他用户可写、接受组可写并告警）由
[ADR-032](ADR-032-accept-group-writable-trusted-directories.md) 固定。该 pathname 安全模型明确把 current EUID 视为受信任主体：parent advisory lock
只串行遵守 Repository 协议的发布者，不承诺隔离恶意同 EUID 进程绕过该锁并在 POSIX 最终
`fstatat` 到 `unlinkat` 的 syscall 窄窗替换目录项。检测到的替换仍拒绝删除；若威胁模型需要
覆盖同凭据 Byzantine 写者，必须另行采用进程/目录隔离或具备 fd-bound conditional unlink
能力的可信设施，不能把现有复验描述为该保证。

Repository 的平台契约是决策的一部分，并与 Remote content 的发布能力分开治理：

| 平台 | `Load` | `Replace` |
| --- | --- | --- |
| Linux、macOS | 支持 | 支持 |
| FreeBSD | 支持 | 不支持；返回 `ENOTSUP` 并 fail closed |
| Windows 与其它平台 | 不支持；fail closed | 不支持；fail closed |

因此 FreeBSD 只可消费既有严格有效 catalog；需要首次落盘或 hash 变化时不能更新。Windows 与其它
未实现平台不能加载旧 catalog，也不能发布新 catalog。

缓存不绑定 Provider 来源。来源变化后仍只比较 catalog SHA-256；相同字节直接复用，内容
不同才原子更新。

Uninstall 不读取 catalog。MCP 卸载只需 Agent CLI 中的名称和实际状态：卸载
journal 不再携带 Server definition，恢复遇到 MCP 仍存在时继续删除，已不存在时
提交 manifest 清理。安装和版本切换仍在 journal 保留必要 definition 以支持回滚。

## Consequences

- 正面影响：APM 常规查询只需一次轻量 version 请求；APC 公开资源接口显著收敛；Local 与
  Remote 共用同一缓存和解析契约；install 的选择与下载 metadata 来源唯一。
- 正面影响：hash 同时证明更新状态和下载完整性，不再维护可能与 catalog 漂移的时间戳或
  sidecar marker。
- 代价与风险：完整 catalog 单文件和内存 Snapshot 有 100 MiB 上限；变更时下载整个文件，
  不支持增量更新。
- 代价与风险：stale fallback 提高可用性，但 APC 不可达时可能离线安装随后已被下架、且
  本地已有制品的版本；content 未缓存时仍会失败。
- 代价与风险：list/search 为同步短时获取全局锁；这改变其原先全程无锁的实现，但不改变
  journal 只检查、不恢复的业务规则。
- 代价与风险：Repository 的目录项操作依赖 current-EUID trusted boundary；advisory lock 和
  identity 复验不构成对恶意同 EUID 写者的隔离保证。
- 代价与风险：FreeBSD 只能读取既有有效 catalog，`Replace` 以 `ENOTSUP` fail closed；Windows 与
  其它未实现平台的 `Load`/`Replace` 都 fail closed。扩展平台支持必须保留相同物理文件安全约束。
- 代价与风险：MCP 卸载恢复不再因 `NotApplied` 后的外部漂移而重建原定义；
  它始终收敛到用户请求的“已卸载”结果，与其他 Power 卸载方向一致。
- 后续动作：实现 Feature 024，更新 APM/APC 当前文档并删除旧公开目录接口调用；实现完成
  后在 Changelog 中记录一次。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 把缓存内置于 Remote/Local Cloud Client | Provider 同时承担 HTTP、锁、持久化与 fallback，Local/Remote 容易漂移。 |
| 在 list/search/install 中分别同步 | 重复锁、错误处理、warning 和 schema 解析，模块职责不清。 |
| 保留 cursor 并由 APM 本地组装完整文件 | APC 仍需维护分页快照和游标，未达到简化公开接口的目标。 |
| APC 返回一次性 bulk 响应但不持久 catalog | version 与响应字节难以形成同一原子对象，容易产生标记/内容漂移。 |
| 按 Provider 或 URL 保存多份缓存 | 用户确认全局只保留一份；相同内容应按 hash 复用，不因来源标签重复存储。 |
| version 不可达时始终失败 | 放弃 apt 式离线 list/search 和已有制品安装能力。 |
| 使用独立 catalog.lock | 现有 `apm.lock` 已是跨进程共享状态互斥；新增锁会引入锁顺序和死锁风险。 |

# Cache a Content-addressed Resource Catalog

- Status：Accepted
- Date：2026-08-25
- Decision Makers：APM maintainers
- Related Feature：[Feature 024](../features/024-catalog-cache/spec.md)
- Related APC ADR：[ADR-004](../../../apc/ivory-tower/adr/ADR-004-publish-content-addressed-catalog.md)

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
`apm.lock` 串行同步：list/search 使用自加锁入口，已持完整生命周期锁的 install 使用显式
locked 入口以避免死锁。同步失败时，有效旧 catalog 可供 stale list/search 和已有制品缓存
的离线 install 使用，并输出一次 warning；无有效 catalog 或 caller 主动取消时失败。

缓存不绑定 Provider 来源。来源变化后仍只比较 catalog SHA-256；相同字节直接复用，内容
不同才原子更新。

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

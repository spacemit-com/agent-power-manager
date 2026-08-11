# Feature Overview

## Goal

- 特性名称：Agent Power 安装清单与无状态 Config Repository
- 要解决的问题：现有 config 仅有未实现的 `Read/Write` 占位，清单仍使用扁平 `InstalledPower`，既没有确定 JSON 格式，也会迫使 lifecycle 管理 JSONStore 的加载与写入状态。
- 可交付结果：定义按 Skill、Plugin、MCP 固定分组的 Agent Power 安装清单；以无状态 JSON Repository 封装查询、读改写和原子落盘；lifecycle 后续只需调用 `List/Find/Set/Remove`。

## Scope

- 包含：`PowerGroups`；删除 `InstalledPower` 和安装时间；固定分组 JSON；无状态 `Repository` 与 `JSONRepository`；`List/Find/Set/Remove`；清单默认路径；同目录临时写入、同步和原子替换；输入校验、幂等更新、重复项归并；verbose debug 日志；相关单元测试和设计文档更新。
- 不包含：lifecycle 或 CLI 串联；安装、升级、降级、卸载的业务决策；按关键字或类型筛选输出；Agent ID 字符规则；清单迁移、schema version、checksum、加密、并发写入或内部文件锁。历史上的“semver 校验不包含”已由 [ADR-007](../../adr/ADR-007-use-stateless-config-repository.md) 和 Feature 009 supersede：当前 Repository 完整读写要求精确 `vMAJOR.MINOR.PATCH`，且不迁移或清理非法旧清单。

## Implementation Context

- 关联模块或服务：`internal/domain`、`internal/config`、`internal/paths`，以及引用旧 `InstalledPower`/`ConfigStore` 的占位类型和 C4 图。
- 数据、接口或依赖变化：新增 `domain.PowerGroups`；删除 `domain.InstalledPower`；将 `config.Store.Read/Write` 替换为无状态 `Repository.List/Find/Set/Remove`；`JSONStore` 改为 `JSONRepository`；删除独立 `AtomicWrite`，原子落盘由 Repository 内部完成；不新增第三方依赖。
- 关联 C4 文档或 ADR：[ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-007](../../adr/ADR-007-use-stateless-config-repository.md)。

## Acceptance

- 验收条件：每个 Agent 的清单按固定 JSON 分组读写；`List/Find/Set/Remove` 在缺失文件、重复项、无效输入和读写失败场景下符合规格；写入使用 `0700` 目录、`0600` 文件及同目录原子替换；Repository 不保存跨调用状态；config 不获取全局锁且不直接打印；全量测试、vet、构建和 CLI 解析 smoke test 通过，CLI 功能不变。
- 已知风险或待决问题：未知 JSON 字段在读取时忽略并可能在后续写入时丢失；任何版本不合规的历史条目都会使完整读取失败，用户需在外部修正，Repository 不迁移或清理；Repository 的读改写依赖 lifecycle 已持有全局 APM 锁（当前 installed List 锁范围见 [ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）。

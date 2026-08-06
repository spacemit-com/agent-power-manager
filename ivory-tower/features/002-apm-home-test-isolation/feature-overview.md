# Feature Overview

## Goal

- 特性名称：`APM_HOME` 测试文件系统隔离
- 要解决的问题：本地 Power 生命周期测试需要模拟 Agent 安装状态并读写 APM 状态，但不能触碰用户的真实 HOME 和缓存目录。
- 可交付结果：统一的有效 HOME、APM 状态根和缓存路径解析，以及使用该规则的默认文件锁。

## Scope

- 包含：`APM_HOME` 非空优先级；空值或未设置时回退 HOME；`$APM_HOME/.apm` 状态根；`$APM_HOME/.cache/apm` 隔离缓存；默认 lock 的隔离路径；路径优先级与不越界测试。
- 不包含：CLI flag 或 help 变更；`APM_HOME` 路径校验；尚未实现的 Adapter、Config、Cache、Transaction 或 Lifecycle 业务逻辑；完整 Power 安装卸载集成测试。

## Implementation Context

- 关联模块或服务：`internal/paths`、`internal/lock`；后续 Adapter、Config、Cache 和 Transaction feature 将消费同一路径入口。
- 数据、接口或依赖变化：新增有效 HOME 和 APM 状态根解析；将尚无调用方的 `CachePath` 占位签名改为可返回路径解析错误的实现。不新增第三方依赖。
- 关联 C4 文档或 ADR：[Project Overview](../../project-overview.md#test-filesystem-isolation)、[ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-005](../../adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

## Acceptance

- 验收条件：设置临时 `APM_HOME` 后，有效 HOME、APM 状态、缓存和默认 lock 全部位于临时根目录，并且已设置的 `XDG_CACHE_HOME` 与真实 HOME 未被写入；`APM_HOME` 为空或未设置时保持既有生产语义。
- 已知风险或待决问题：修改进程环境的测试不得并行；完整 Agent 和 Power 生命周期隔离验收需在对应业务组件实现后执行。

# Feature Overview

## Goal

- 特性名称：全局 slog 与分级日志路由
- 要解决的问题：现有 CLI 逐层传递自定义 `logging.Logger`，verbose 依赖可变状态，内部包不能统一直接使用标准库 `slog`，各级别也缺少独立的路由与格式化边界。
- 可交付结果：CLI 在正式解析前预解析 `--verbose` 并安装全局默认 logger；任意包可直接调用 `slog.Debug/Info/Warn/Error`；Debug 由 verbose 控制，Info 写 stdout，Warn/Error 写 stderr。

## Scope

- 包含：按级别分派的 `slog.Handler`；Debug 动态启用检查；无时间戳 TextHandler；全局 `SetupLogging`；CLI writer 与日志初始化回调；现有 CLI 日志调用迁移；重复 Execute 的日志状态隔离。
- 不包含：改写 `fmt.Errorf`；为 Cloud/Cache 等模块新增非必要日志；JSON、颜色、message-only 或用户自定义格式；日志文件、轮转、远端采集；并发执行多个采用不同日志配置的 CLI Entry。

## Implementation Context

- 关联模块或服务：`cmd/apm`、`internal/cli`、`internal/logging`。
- 数据、接口或依赖变化：删除自定义 `logging.Logger` 及其输出/verbose 方法；CLI 不再接收 logger 实例，改为接收 stdout、stderr 和 `func(bool)` 日志初始化回调；不新增第三方依赖。
- 关联 C4 文档或 ADR：[ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md)、[ADR-006](../../adr/ADR-006-use-global-slog-with-level-routing.md)。

## Acceptance

- 验收条件：Debug 仅在 verbose 时写 stderr；Info 仅写 stdout；Warn/Error 仅写 stderr；日志无时间戳且保留属性和 group；同一进程先执行 verbose、再执行普通命令时第二次不输出 Debug；错误只记录一次并保留 Cobra usage；help 仍为 Cobra 原生输出；生产代码不在 logging Handler 之外直接打印或自行创建 logger；全量测试、vet 和构建通过。
- 已知风险或待决问题：全局默认 logger 不支持同一进程并发执行不同日志配置的 CLI Entry；未来自定义格式需要单独 feature 决定具体协议。

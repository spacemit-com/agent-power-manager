# Architecture Decision Record

- Status：Accepted
- Date：2026-08-07
- Decision Makers：liangjunzhao

## Context

[ADR-001](ADR-001-use-go-as-implementation-language.md) 已要求所有用户输出和内部诊断统一经过 `internal/logging`，但首版实现通过 `*logging.Logger` 在 CLI 中逐层传递，并用可变的 `verbose` 字段控制诊断输出。这使任意内部包不能直接使用标准库 `slog`，同一进程多次执行 CLI 时还必须显式重置 verbose 状态。

本决策服务于 [Feature 004](../features/004-global-slog-logging/feature-overview.md)。设计参考 Helm 的预解析 debug flag、配置默认 `slog.Logger` 和在 Handler 中决定 Debug 是否启用的模式，但保留 APM 已确定的 stdout/stderr 路由。

## Decision

APM 在 Cobra 正式执行前预解析全局 `--verbose`，为当前 CLI 执行创建新的分级 `slog.Logger` 并通过 `slog.SetDefault` 安装；内部包直接调用 `slog.Debug`、`slog.Info`、`slog.Warn` 和 `slog.Error`，不再接收自定义 logging 实例。

- `internal/logging.SetupLogging(verbose)` 使用进程 stdout/stderr 安装默认 logger；可注入 writer 的 logger 构造入口供测试和嵌入场景使用。
- 一个路由 Handler 按级别区间分派到四个独立 Handler：Debug 写 stderr，Info 写 stdout，Warn 和 Error 写 stderr。
- Debug Handler 在 `Enabled` 中检查当前 logger 创建时传入的 verbose；其他级别始终启用。
- 各级别初始使用无时间戳 `slog.TextHandler`。本决策不实现 JSON、颜色、message-only 或其他自定义格式，但保留按级别替换 Handler 的边界。
- 每次 `CLIEntry.Execute` 都重新预解析参数并安装新 logger。业务代码不得缓存 `slog.Default()` 的返回值，相关测试不得并行替换默认 logger，多个 CLI Entry 不支持在同一进程并发执行。
- 底层模块返回带上下文错误，不逐层记录 Error；最终未处理错误只在 CLI 外层记录一次。可恢复异常使用 Warn，内部执行细节使用 Debug。
- `fmt.Errorf`、`fmt.Sprintf` 等只构造值的调用不属于直接打印，可以继续使用。Cobra help 和 usage 继续使用 Cobra writer，不进入 slog。

## Consequences

- 正面影响：内部包不再传递 logger；verbose 在每次 CLI 执行时由完整配置决定，不需要 `SetVerbose`；级别路由、输出目标和未来格式化扩展具有独立边界。
- 代价与风险：`slog.SetDefault` 是进程级可变状态，嵌入方不能并发执行多个采用不同日志配置的 CLI Entry；提前缓存的 logger 不会跟随默认 logger 更新；标准 `slog` 调用不向业务层返回 writer 写入错误。
- 后续动作：Feature 004 完成 Handler、CLI 初始化和现有调用迁移；后续模块直接使用包级 `slog` 函数，不自行创建 logger，也不直接使用 `fmt.Print*` 或 `log.Print*`。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 完全照搬 Helm 的单一 stderr Handler | 不能满足 APM 的 Info/stdout 与 Warn、Error/stderr 路由，也没有按级别替换格式的边界。 |
| 继续向所有模块注入 `*slog.Logger` | 可避免全局状态，但仍需逐层传递，违背任意包直接使用标准库 `slog` 的目标。 |
| 保留现有自定义 Logger 门面 | `Output/Warning/Error/Recovery/Verbose` 与标准级别重复，并继续依赖可变 verbose 状态。 |

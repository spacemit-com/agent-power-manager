# 全局 slog 与分级日志路由规格

## 目标

将 APM 从逐层传递自定义 `logging.Logger` 重构为进程级标准库 `slog`。CLI 在 Cobra 正式解析前预解析全局 `--verbose`，安装本次执行使用的默认 logger；任意内部包随后直接使用 `slog.Debug`、`slog.Info`、`slog.Warn` 和 `slog.Error`。

本 feature 只重构日志基础设施和现有 CLI 调用，不为尚未串联的 Cloud、Cache、Lifecycle、Adapter 或 Transaction 增加展示性日志。

## 设计依据与边界

实现参考 Helm 的两项核心模式：在命令执行前预解析 debug flag 并设置 `slog.Default()`；由包装 Handler 的 `Enabled` 在记录发生时决定 Debug 是否启用。APM 不照搬 Helm 的单一 stderr 输出，而是按照 [ADR-006](../../adr/ADR-006-use-global-slog-with-level-routing.md) 保留分级 stdout/stderr 路由。

`fmt.Errorf`、`fmt.Sprintf` 和其他只构造字符串或错误的调用不属于打印，本 feature 不改写它们。Cobra 生成的 help 和 usage 继续使用 Cobra 自身的 writer。

## Logging API

`internal/logging` 提供以下入口：

```go
func SetupLogging(verbose bool)
func NewLogger(stdout, stderr io.Writer, verbose bool) *slog.Logger
```

- `SetupLogging` 调用 `NewLogger(os.Stdout, os.Stderr, verbose)`，再通过 `slog.SetDefault` 安装进程默认 logger。
- `NewLogger` 只负责组装 Handler 并返回 logger，不修改全局状态，供单元测试和嵌入方显式使用。
- 删除现有 `Logger` 类型、`SetVerbose`、`Stdout`、`Stderr`、`Output`、`Warning`、`Error`、`Recovery` 和 `Verbose`。
- `fields.go` 中返回 `slog.Attr` 的领域字段辅助函数保留。

业务模块不得自行调用 `slog.New` 或缓存 `slog.Default()`；它们使用包级 `slog.*` 函数，使每次 `slog.SetDefault` 都能立即生效。

## Handler 结构

### Level Router

路由 Handler 持有 Debug、Info、Warn 和 Error 四个 Handler，并实现完整的 `slog.Handler` 接口：

- `Enabled` 根据当前 level 选择目标 Handler，并委托给目标的 `Enabled`。
- `Handle` 使用相同规则把 Record 交给唯一目标 Handler，不重复输出。
- `WithAttrs` 分别派生四个目标 Handler，再返回新的路由 Handler。
- `WithGroup` 分别派生四个目标 Handler，再返回新的路由 Handler。

级别区间固定为：

| Record level | 目标 Handler | Writer |
| --- | --- | --- |
| `< slog.LevelInfo` | Debug | stderr |
| `>= Info && < Warn` | Info | stdout |
| `>= Warn && < Error` | Warn | stderr |
| `>= Error` | Error | stderr |

因此标准四级和自定义级别都只会路由一次。

### Debug Handler

Debug Handler 包装实际 TextHandler，并保存 `func() bool` 形式的 verbose 检查：

- verbose 检查为空或返回 false 时，`Enabled` 对 Debug 返回 false。
- verbose 返回 true 时继续委托实际 Handler。
- `Handle` 直接委托实际 Handler。
- `WithAttrs` 和 `WithGroup` 必须保留同一个 verbose 检查函数。

`NewLogger` 为每次 CLI 执行传入闭包 `func() bool { return verbose }`。该值在 logger 生命周期内不可变；下一次执行通过创建新 logger 改变配置，不修改旧 logger。

### 初始格式

四个实际 Handler 均使用独立的 `slog.TextHandler`，最低级别允许 Debug，并通过 `ReplaceAttr` 删除 `slog.TimeKey`。当前输出示例：

```text
level=DEBUG msg="command parsed" command=search
level=INFO msg="power listed" power=foo
level=WARN msg="recovery required" phase=commit
level=ERROR msg="command failed" error="invalid flag"
```

本 feature 不实现 JSON、颜色、message-only 或其他自定义格式。四个 Handler 的独立边界为后续格式化设计保留替换点。

## CLI 初始化流程

CLI 构造入口改为接收输出 writer 和日志初始化回调，不再接收 `*logging.Logger`：

```go
type SetupLoggingFunc func(verbose bool)

func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc) *CLIEntry
```

`cmd/apm` 以 `os.Stdout`、`os.Stderr` 和 `logging.SetupLogging` 创建 CLI Entry。

每次 `CLIEntry.Execute(args)` 必须重新创建 Cobra 命令树，并按以下顺序执行：

1. 在根命令的 persistent FlagSet 上允许未知 flag，并预解析 args。
2. 从预解析结果取得 `--verbose`；未提供时为 false。
3. 调用 `setupLogging(verbose)`，替换进程默认 logger。
4. 把原始 args 交给 Cobra 正式解析并执行。

预解析只用于尽早配置日志，不取代 Cobra 校验。未知 flag、缺失参数或非法布尔值在正式解析阶段返回原有 Cobra 错误；若预解析无法取得有效 verbose，则按 false 配置日志，随后仍进入正式解析。

同一进程连续执行时，每次都安装新 logger：第一次 `--verbose` 不会使第二次普通执行输出 Debug。由于 `slog.SetDefault` 是进程级状态，多个 CLI Entry 不支持并发 Execute；修改默认 logger 的测试不得使用 `t.Parallel()`。

## CLI 日志与错误责任

命令成功完成解析后直接调用：

```go
slog.Debug("command parsed", ...)
```

不再检查 verbose，也不再调用 `SetVerbose`；Debug Handler 是唯一开关。

Cobra 返回最终解析或执行错误时，`CLIEntry.Execute`：

1. 调用一次 `slog.Error("command failed", slog.Any("error", err))`。
2. 通过已设置的 Cobra stderr writer 输出 usage。
3. 把原始错误返回给 `main`。

`main` 只以非零状态退出，不重复打印错误。底层模块只返回带上下文错误；只有可继续运行的异常使用 Warn，内部步骤和诊断使用 Debug。

help 和无错误的非 verbose 命令不产生日志。help、usage 的文本和 writer 归 Cobra 管理，不通过 slog 重新格式化。

## 直接打印规则

生产代码遵守以下约束：

- logging Handler 之外不得调用 `fmt.Print*`、`fmt.Fprint*`、`log.Print*` 或 `log.Fatal*`。
- `fmt.Errorf`、`fmt.Sprintf` 和 `errors.New` 可以继续用于返回值构造。
- `internal/logging` 之外不得创建独立 `slog.Logger`。
- 不为了迁移日志系统而给当前没有输出责任的模块新增日志。

## 测试与验收

### Handler 单元测试

- verbose=false 时 Debug 不写 stdout 或 stderr。
- verbose=true 时 Debug 只写 stderr。
- Info 只写 stdout；Warn 和 Error 只写 stderr。
- 每条 Record 只输出一次，无时间戳，并包含 level、message 和属性。
- `WithAttrs` 与 `WithGroup` 在四种路由上保留属性和 group。
- 自定义 level 按区间落入预期 Handler。

### CLI 测试

- verbose 命令在 stderr 产生一条 Debug 解析日志，默认命令不产生该日志。
- 同一 CLI Entry 先执行 verbose、再执行普通命令，第二次不继承 Debug。
- 非法 flag 或参数只产生一条 Error，并由 Cobra 输出 usage；main 不重复记录。
- help 仍写 stdout，stderr 无日志，内容由 Cobra 原生命令树生成。
- 测试通过注入 writer 的 `NewLogger` 安装默认 logger，并在结束后恢复原 logger；相关测试不并行。

### 静态范围检查

验证生产代码只在 `internal/logging` 创建 logger，且不存在越界直接打印。检查只针对打印函数，不误报 `fmt.Errorf` 或 `fmt.Sprintf`。

### 最终验证

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

## 明确不实现

- Cloud、Cache、Lifecycle、Adapter 或 Transaction 的新日志点。
- 日志文件、轮转、远端采集或审计存储。
- JSON、颜色、message-only、用户模板或按命令选择格式。
- 运行时修改 verbose 或重新配置既有 logger。
- 同一进程并发执行采用不同日志配置的 CLI Entry。

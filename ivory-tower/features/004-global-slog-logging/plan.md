# Global slog Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 APM 日志重构为预解析 `--verbose` 后安装的全局 `slog`，并按 Debug、Info、Warn、Error 独立路由和格式化。

**Architecture:** `internal/logging` 用一个级别路由 Handler 组合四个无时间戳 TextHandler，Debug 再由 verbose 检查 Handler 包装；CLI 每次 Execute 都预解析 persistent flag 并安装新的默认 logger。业务包直接调用包级 `slog.*`，Cobra 仍独立负责 help 和 usage。

**Tech Stack:** Go 1.26.5 标准库 `log/slog`、`io`、`context`；Cobra 1.10.2 与其 pflag；Go testing。

## Global Constraints

- 保留 `fmt.Errorf`、`fmt.Sprintf` 和 `errors.New`；只治理实际打印。
- Debug 仅在 verbose 时写 stderr；Info 写 stdout；Warn 和 Error 写 stderr。
- 四级日志初始均使用无时间戳 `slog.TextHandler`，本 feature 不实现 JSON、颜色、message-only 或用户格式模板。
- 业务包不得缓存 `slog.Default()`，不得自行创建 logger，直接调用包级 `slog.Debug/Info/Warn/Error`。
- 底层模块返回错误；最终未处理错误只在 CLI 外层记录一次，main 不重复打印。
- Cobra help 和 usage 继续使用 Cobra writer，不进入 slog。
- 不为 Cloud、Cache、Lifecycle、Adapter 或 Transaction 新增日志点。
- 多个 CLI Entry 不支持在同一进程并发 Execute；修改默认 logger 的测试不得使用 `t.Parallel()`。
- 不新增第三方依赖；重要导出对象和方法使用中文 GoDoc。
- 本请求只在完成全部实现后向 `docs/Changelog.md` 增加一条记录。

## File Map

| File | Responsibility |
| --- | --- |
| `internal/logging/handler.go` | Debug verbose 检查和按级别路由的 `slog.Handler` 实现。 |
| `internal/logging/global.go` | `NewLogger(stdout, stderr, verbose)` 与 `SetupLogging(verbose)` 装配入口。 |
| `internal/logging/logger_test.go` | 级别路由、verbose、格式、属性/group 和默认 logger 替换测试。 |
| `internal/logging/fields.go` | 保持现有领域属性辅助函数，不改变字段名。 |
| `internal/logging/output.go` | 删除旧 Logger 输出门面。 |
| `internal/logging/verbose.go` | 删除旧 Logger verbose 门面。 |
| `internal/cli/root.go` | CLI writer、日志初始化回调、预解析、单次错误记录和直接 Debug 调用。 |
| `internal/cli/{search,list,install,uninstall}.go` | 移除 logger 参数并调用无 logger 参数的解析日志函数。 |
| `internal/cli/cli_test.go` | 注入测试 logger，覆盖重复 Execute、错误单次输出和原生 help/usage。 |
| `cmd/apm/main.go` | 用进程 writer 与 `logging.SetupLogging` 装配 CLI，不创建或打印 logger。 |
| `docs/Changelog.md` | 在所有行为实现和验证完成后记录本请求的一条功能变更。 |

---

### Task 1: 在兼容旧入口的同时实现全局 logger 与分级 Handler

**Files:**
- Create: `internal/logging/handler.go`
- Create: `internal/logging/global.go`
- Modify: `internal/logging/logger_test.go`
- Verify unchanged: `internal/logging/logger.go`
- Verify unchanged: `internal/logging/output.go`
- Verify unchanged: `internal/logging/verbose.go`
- Verify unchanged: `internal/logging/fields.go`

**Interfaces:**
- Consumes: `io.Writer`、`slog.Handler`、`slog.Level`。
- Produces: `func NewLogger(stdout, stderr io.Writer, verbose bool) *slog.Logger`、`func SetupLogging(verbose bool)`。

- [ ] **Step 1: 在旧门面测试旁增加新 API 和行为测试**

Task 1 暂时保留针对 `NewDefault`、writer accessor 和 `Output/Warning/Error/Recovery/Verbose/SetVerbose` 的测试，使尚未迁移的 CLI 继续编译；在 `internal/logging/logger_test.go` 追加以下测试结构：

```go
func TestNewLoggerRoutesLevels(t *testing.T) {
	tests := []struct {
		name       string
		verbose    bool
		level      slog.Level
		wantStdout bool
		wantStderr bool
	}{
		{name: "debug disabled", level: slog.LevelDebug},
		{name: "debug enabled", verbose: true, level: slog.LevelDebug, wantStderr: true},
		{name: "custom debug", verbose: true, level: slog.Level(-2), wantStderr: true},
		{name: "info", level: slog.LevelInfo, wantStdout: true},
		{name: "custom info", level: slog.Level(2), wantStdout: true},
		{name: "warn", level: slog.LevelWarn, wantStderr: true},
		{name: "custom warn", level: slog.Level(6), wantStderr: true},
		{name: "error", level: slog.LevelError, wantStderr: true},
		{name: "custom error", level: slog.Level(12), wantStderr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			logger := logging.NewLogger(&stdout, &stderr, tt.verbose)
			logger.LogAttrs(context.Background(), tt.level, "event", slog.String("key", "value"))

			if got := stdout.Len() > 0; got != tt.wantStdout {
				t.Fatalf("stdout = %q, want output %v", stdout.String(), tt.wantStdout)
			}
			if got := stderr.Len() > 0; got != tt.wantStderr {
				t.Fatalf("stderr = %q, want output %v", stderr.String(), tt.wantStderr)
			}
			combined := stdout.String() + stderr.String()
			if combined != "" {
				if strings.Contains(combined, "time=") {
					t.Fatalf("log = %q, want no timestamp", combined)
				}
				if !strings.Contains(combined, "msg=event") || !strings.Contains(combined, "key=value") {
					t.Fatalf("log = %q, want message and attribute", combined)
				}
			}
		})
	}
}

func TestNewLoggerPreservesAttrsAndGroups(t *testing.T) {
	var stdout, stderr bytes.Buffer
	logger := logging.NewLogger(&stdout, &stderr, true).
		With(slog.String("component", "cli")).
		WithGroup("request")

	logger.Info("parsed", slog.String("command", "search"))
	if got := stdout.String(); !strings.Contains(got, "component=cli") || !strings.Contains(got, "request.command=search") {
		t.Fatalf("stdout = %q, want inherited attr and group", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestSetupLoggingReplacesDefaultLogger(t *testing.T) {
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	logging.SetupLogging(true)
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug disabled after SetupLogging(true)")
	}
	logging.SetupLogging(false)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("debug enabled after SetupLogging(false)")
	}
}
```

为表驱动测试增加 `context` import，继续使用现有 `bytes`、`log/slog`、`strings` 和领域字段测试 import。不得使用 `t.Parallel()`。

- [ ] **Step 2: 运行 logging 测试并确认新 API 缺失**

Run:

```bash
go test ./internal/logging -count=1
```

Expected: FAIL，至少包含 `undefined: logging.NewLogger` 和 `undefined: logging.SetupLogging`。

- [ ] **Step 3: 实现 Debug 检查和级别路由 Handler**

创建 `internal/logging/handler.go`：

```go
package logging

import (
	"context"
	"log/slog"
)

type debugEnabledFunc func() bool

type debugHandler struct {
	handler slog.Handler
	enabled debugEnabledFunc
}

func (h *debugHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.enabled != nil && h.enabled() && h.handler.Enabled(ctx, level)
}

func (h *debugHandler) Handle(ctx context.Context, record slog.Record) error {
	return h.handler.Handle(ctx, record)
}

func (h *debugHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &debugHandler{handler: h.handler.WithAttrs(attrs), enabled: h.enabled}
}

func (h *debugHandler) WithGroup(name string) slog.Handler {
	return &debugHandler{handler: h.handler.WithGroup(name), enabled: h.enabled}
}

type levelRouter struct {
	debug slog.Handler
	info  slog.Handler
	warn  slog.Handler
	error slog.Handler
}

func (h *levelRouter) target(level slog.Level) slog.Handler {
	switch {
	case level < slog.LevelInfo:
		return h.debug
	case level < slog.LevelWarn:
		return h.info
	case level < slog.LevelError:
		return h.warn
	default:
		return h.error
	}
}

func (h *levelRouter) Enabled(ctx context.Context, level slog.Level) bool {
	return h.target(level).Enabled(ctx, level)
}

func (h *levelRouter) Handle(ctx context.Context, record slog.Record) error {
	return h.target(record.Level).Handle(ctx, record)
}

func (h *levelRouter) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelRouter{
		debug: h.debug.WithAttrs(attrs),
		info:  h.info.WithAttrs(attrs),
		warn:  h.warn.WithAttrs(attrs),
		error: h.error.WithAttrs(attrs),
	}
}

func (h *levelRouter) WithGroup(name string) slog.Handler {
	return &levelRouter{
		debug: h.debug.WithGroup(name),
		info:  h.info.WithGroup(name),
		warn:  h.warn.WithGroup(name),
		error: h.error.WithGroup(name),
	}
}
```

不要导出 Handler 实现；本 feature 的稳定接口只有 logger 装配入口。

- [ ] **Step 4: 新增全局 logger 装配并暂时保留旧门面**

创建 `internal/logging/global.go`：

```go
// Package logging 提供 APM 命令行的统一日志配置。
package logging

import (
	"io"
	"log/slog"
	"os"
)

func newTextHandler(writer io.Writer) slog.Handler {
	return slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})
}

// NewLogger 使用指定 writer 和 verbose 配置创建分级日志器。
func NewLogger(stdout, stderr io.Writer, verbose bool) *slog.Logger {
	debug := &debugHandler{
		handler: newTextHandler(stderr),
		enabled: func() bool { return verbose },
	}
	return slog.New(&levelRouter{
		debug: debug,
		info:  newTextHandler(stdout),
		warn:  newTextHandler(stderr),
		error: newTextHandler(stderr),
	})
}

// SetupLogging 使用进程标准输出和标准错误安装默认日志器。
func SetupLogging(verbose bool) {
	slog.SetDefault(NewLogger(os.Stdout, os.Stderr, verbose))
}
```

Task 1 不删除 `logger.go`、`output.go` 或 `verbose.go`；它们仍被当前 CLI 使用，将在 Task 2 完成调用迁移后一起删除。不要修改 `fields.go` 的字段名称。

- [ ] **Step 5: 格式化并运行 logging 测试**

Run:

```bash
gofmt -w internal/logging
go test ./internal/logging -count=1
go test -count=1 ./...
```

Expected: PASS；Debug false 无输出，其他 case 严格落到一个 writer；旧 CLI 在过渡提交上仍可编译和通过测试。

- [ ] **Step 6: 提交 logging 层**

```bash
git add internal/logging/handler.go internal/logging/global.go internal/logging/logger_test.go
git commit -m "refactor(logging): add global level routing"
```

---

### Task 2: 迁移 CLI 和 main 到全局 slog

**Files:**
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/search.go`
- Modify: `internal/cli/list.go`
- Modify: `internal/cli/install.go`
- Modify: `internal/cli/uninstall.go`
- Modify: `internal/cli/cli_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `internal/logging/logger_test.go`
- Delete: `internal/logging/logger.go`
- Delete: `internal/logging/output.go`
- Delete: `internal/logging/verbose.go`

**Interfaces:**
- Consumes: `logging.NewLogger(io.Writer, io.Writer, bool) *slog.Logger`、`logging.SetupLogging(bool)`。
- Produces: `type SetupLoggingFunc func(bool)`、`func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc) *CLIEntry`、无 logger 参数的四个命令构造函数。

- [ ] **Step 1: 把 CLI 测试改为安装注入 writer 的默认 logger**

在 `internal/cli/cli_test.go` 增加 helper，并将全部 `cli.New(logging.New(...))` 改为 `newTestEntry(...)`：

```go
func newTestEntry(t *testing.T, stdout, stderr *bytes.Buffer) *cli.CLIEntry {
	t.Helper()
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })
	return cli.New(stdout, stderr, func(verbose bool) {
		slog.SetDefault(logging.NewLogger(stdout, stderr, verbose))
	})
}
```

修改 verbose 测试，额外断言 `level=DEBUG` 存在且 `time=` 不存在。将重复执行测试改为直接调用包级 Debug：

```go
func TestCLIRepeatedExecutionDoesNotLeakFlagsOrLogging(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newTestEntry(t, &stdout, &stderr)

	if err := entry.Execute([]string{"search", "first", "--agent=all", "--verbose"}); err != nil {
		t.Fatalf("first Execute error = %v", err)
	}
	if !strings.Contains(stderr.String(), "level=DEBUG") {
		t.Fatalf("first stderr = %q, want debug log", stderr.String())
	}
	stdout.Reset()
	stderr.Reset()

	if err := entry.Execute([]string{"search", "second", "--agent=codex"}); err != nil {
		t.Fatalf("second Execute error = %v", err)
	}
	slog.Debug("after non-verbose execution", slog.String("command", "search"))
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("second Execute output = stdout %q, stderr %q; want no leaked verbose state", stdout.String(), stderr.String())
	}

	if err := entry.Execute([]string{"search", "third"}); err == nil {
		t.Fatal("third Execute error = nil, want missing --agent after prior executions")
	}
}
```

在执行错误测试中增加 `--verbose=invalid` case，并对每个 case 断言错误日志只出现一次：

```go
if count := strings.Count(stderr.String(), "level=ERROR"); count != 1 {
	t.Fatalf("stderr = %q, want exactly one error log, got %d", stderr.String(), count)
}
if !strings.Contains(stderr.String(), "Usage:") {
	t.Fatalf("stderr = %q, want Cobra usage", stderr.String())
}
```

help 测试继续断言 stdout 含 Cobra 内容且 stderr 为空。成功的非 verbose 命令继续断言两个 writer 都为空。所有相关测试禁止 `t.Parallel()`。

同时从 `internal/logging/logger_test.go` 删除只验证旧 Logger 门面的 `TestNewDefaultUsesProcessWriters`、`TestRoutesAndVerbose` 和 `TestDiagnosticOutputsAndWriters`；保留 Task 1 新增的全局 logger 测试及 `TestFieldHelpers`。

- [ ] **Step 2: 运行 CLI 测试并确认构造接口不匹配**

Run:

```bash
go test ./internal/cli -count=1
```

Expected: FAIL，包含 `too many arguments in call to cli.New` 或等价的旧构造签名错误。

- [ ] **Step 3: 重写 CLI Entry、预解析和日志调用**

在 `internal/cli/root.go` 中移除 `apm/internal/logging` import，并实现以下结构：

```go
type SetupLoggingFunc func(verbose bool)

// CLIEntry 是 APM 命令行的执行入口。
type CLIEntry struct {
	stdout       io.Writer
	stderr       io.Writer
	setupLogging SetupLoggingFunc
}

// New 使用指定输出和日志初始化函数创建 CLI 入口。
func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc) *CLIEntry {
	return &CLIEntry{stdout: stdout, stderr: stderr, setupLogging: setupLogging}
}

// Execute 解析并校验参数，不执行 Power 生命周期业务。
func (entry *CLIEntry) Execute(args []string) error {
	root := newRootCommand(entry.stdout, entry.stderr)
	preparseLogging(root, args, entry.setupLogging)
	root.SetArgs(args)
	executed, err := root.ExecuteC()
	if err != nil {
		slog.Error("command failed", slog.Any("error", err))
		executed.PrintErr(executed.UsageString())
	}
	return err
}

func preparseLogging(root *cobra.Command, args []string, setupLogging SetupLoggingFunc) {
	flags := root.PersistentFlags()
	flags.ParseErrorsAllowlist.UnknownFlags = true
	_ = flags.Parse(args)
	verbose, _ := flags.GetBool("verbose")
	setupLogging(verbose)
}

func newRootCommand(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "apm",
		Short:         "Agent Power Manager",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.PersistentFlags().Bool("verbose", false, "enable verbose diagnostics")
	root.AddCommand(
		newSearchCommand(),
		newListCommand(),
		newInstallCommand(),
		newUninstallCommand(),
	)
	return root
}

func logParsedCommand(command *cobra.Command, flags Flags, args []string) {
	slog.Debug(
		"command parsed",
		slog.String("command", command.Name()),
		slog.String("agent", flags.Agent),
		slog.String("type", flags.Type),
		slog.Any("args", args),
	)
}
```

imports 必须包含 `io`、`log/slog` 和 Cobra。预解析错误不在此处返回，正式 Cobra 解析负责产生唯一错误。

- [ ] **Step 4: 移除四个命令构造函数的 logger 依赖**

在 `search.go`、`list.go`、`install.go`、`uninstall.go`：

- 删除 `apm/internal/logging` import。
- 将 `newXCommand(logger *logging.Logger)` 改为 `newXCommand()`。
- 将 `logParsedCommand(command, logger, flags, args)` 改为 `logParsedCommand(command, flags, args)`。

不要改动参数校验或命令 help 文本。

- [ ] **Step 5: 修改 main 的依赖装配**

将 `cmd/apm/main.go` 的 main 改为：

```go
func main() {
	entry := cli.New(os.Stdout, os.Stderr, logging.SetupLogging)
	if err := entry.Execute(os.Args[1:]); err != nil {
		os.Exit(1)
	}
}
```

main 不调用 `slog.Error`；错误已由 `CLIEntry.Execute` 记录。

- [ ] **Step 6: 删除已经没有调用者的旧 Logger 门面**

删除：

```text
internal/logging/logger.go
internal/logging/output.go
internal/logging/verbose.go
```

`global.go` 和 `handler.go` 是新的 logging 实现；不得为了兼容保留 `New`、`NewDefault` 或 `SetVerbose` 别名。

- [ ] **Step 7: 格式化并运行聚焦及全量测试**

Run:

```bash
gofmt -w cmd/apm internal/cli
go test ./internal/logging ./internal/cli -count=1
go test -count=1 ./...
```

Expected: PASS；无效命令只出现一条 `level=ERROR`，verbose 重复执行测试不泄漏状态。

- [ ] **Step 8: 确认旧 Logger API 已完全移除**

Run:

```bash
! rg -n 'logging\.New\(|logging\.NewDefault|\*logging\.Logger|SetVerbose|\.Verbose\(|\.Output\(|\.Warning\(|\.Recovery\(' cmd internal --glob '*.go'
```

Expected: 命令退出 0 且无匹配。

- [ ] **Step 9: 提交 CLI、main 与旧门面删除**

```bash
git add cmd/apm internal/cli internal/logging
git commit -m "refactor(cli): install global slog before execution"
```

---

### Task 3: 固化范围约束、记录变更并完成验收

**Files:**
- Modify: `docs/Changelog.md`
- Verify: `cmd/**/*.go`
- Verify: `internal/**/*.go`
- Verify: `ivory-tower/features/004-global-slog-logging/{feature-overview,spec,plan}.md`
- Verify: `ivory-tower/adr/ADR-006-use-global-slog-with-level-routing.md`

**Interfaces:**
- Consumes: Task 1 的 `logging.NewLogger/SetupLogging` 和 Task 2 的新 CLI 构造接口。
- Produces: 已验证且有单条 Changelog 记录的完整 Feature 004。

- [ ] **Step 1: 执行直接打印和独立 logger 静态检查**

Run:

```bash
! rg -n 'fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln)|log\.(Print|Printf|Println|Fatal|Fatalf|Fatalln)' cmd internal --glob '*.go' --glob '!**/*_test.go'
! rg -n 'slog\.(New|SetDefault|NewTextHandler|NewJSONHandler)\(' cmd internal --glob '*.go' --glob '!**/*_test.go' --glob '!internal/logging/**'
```

Expected: 两条命令均退出 0 且无匹配。不得将 `fmt.Errorf` 或 `fmt.Sprintf` 计为违规。

- [ ] **Step 2: 运行日志和 CLI 行为验收**

Run:

```bash
go test ./internal/logging -run 'TestNewLoggerRoutesLevels|TestNewLoggerPreservesAttrsAndGroups|TestSetupLoggingReplacesDefaultLogger' -count=1
go test ./internal/cli -run 'TestCLIVerboseWritesOnlyDiagnosticsToStderr|TestCLIRepeatedExecutionDoesNotLeakFlagsOrLogging|TestCLIExecutionErrorsWriteErrorAndGeneratedUsageToStderr|TestCLIHelp' -count=1
```

Expected: PASS，且不存在跳过测试。

- [ ] **Step 3: 运行完整工程验证**

Run:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: 全部退出 0。

- [ ] **Step 4: 按一次用户请求新增一条 Changelog**

先读取项目模板并生成截至行为实现提交的相关提交列表：

```bash
cat /home/liangff/.codex/docs/Changelog.md
git log --reverse --format='- `%h %s`' 6fa452d^..HEAD
```

在 `docs/Changelog.md` 顶部增加一条 `2026-08-07`、分类为“功能变更”的记录。内容必须说明：

- 用户需求：参考 Helm，把 APM 改为预解析 verbose、安装全局 slog，并为四级日志配置独立 Handler。
- 相关提交：逐行复制上一条 `git log` 的实际输出，包含已提交的 ADR/spec、plan 和 Task 1–2，不写尚不存在的 Changelog 提交自身。
- 完成结果：删除自定义 Logger 门面；Debug 由 verbose Handler 控制；Info/stdout、Warn/Error/stderr；错误只在 CLI 外层记录一次；Cobra help/usage 保持原生。
- 验证结果或限制：记录 Step 1–3 的实际结果；明确不实现自定义格式及并发不同配置的 CLI Entry。

- [ ] **Step 5: 检查并提交 Changelog**

```bash
git diff --check
git add docs/Changelog.md
git commit -m "docs: record global slog migration"
```

- [ ] **Step 6: 在最终 HEAD 上重新验证并确认工作树干净**

Run:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
git status --short
```

Expected: 前五条命令退出 0，`git status --short` 无输出。

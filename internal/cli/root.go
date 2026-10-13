// Package cli 提供解析和校验参数、调用 Search/List/Install/Uninstall/Fix 生命周期并渲染业务结果的 APM Cobra 命令树。
package cli

import (
	"errors"
	"io"
	"log/slog"
	"reflect"

	"github.com/spf13/cobra"
)

// runtimeFailure 标记生命周期运行时失败（如 batch 失败或锁释放失败）。
// Execute 对这类失败只记录一次 Error，不再打印 usage；参数错误与 writer 错误仍保持打印 usage。
type runtimeFailure struct {
	cause error
}

func (e *runtimeFailure) Error() string { return e.cause.Error() }
func (e *runtimeFailure) Unwrap() error { return e.cause }

// SetupLoggingFunc 安装与 verbose 设置匹配的默认日志器。
type SetupLoggingFunc func(verbose bool)

// CLIEntry 是 APM 命令行的执行入口。
type CLIEntry struct {
	stdout                 io.Writer
	stderr                 io.Writer
	stdin                  io.Reader
	setupLogging           SetupLoggingFunc
	newSearchLifecycle     SearchLifecycleFactory
	newListLifecycle       ListLifecycleFactory
	newShowLifecycle       ShowLifecycleFactory
	newInstallLifecycle    InstallLifecycleFactory
	newUpgradeLifecycle    UpgradeLifecycleFactory
	newUninstallLifecycle  UninstallLifecycleFactory
	newFixLifecycle        FixLifecycleFactory
	newValidateLifecycle   ValidateLifecycleFactory
	newSubmitLifecycle     SubmitLifecycleFactory
	newSubmissionLifecycle SubmissionQueryLifecycleFactory
}

// New 使用指定输出、日志初始化函数和相互独立的 Search/List/Install/Uninstall/Fix/Validate/Submit/Submission 生命周期工厂创建 CLI 入口。

func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory, newUninstallLifecycle UninstallLifecycleFactory, newFixLifecycle FixLifecycleFactory, newValidateLifecycle ValidateLifecycleFactory, newSubmitLifecycle SubmitLifecycleFactory, newSubmissionLifecycle SubmissionQueryLifecycleFactory, showFactories ...ShowLifecycleFactory) *CLIEntry {
	return NewWithUpgrade(stdout, stderr, setupLogging, newSearchLifecycle, newListLifecycle, newInstallLifecycle, newUninstallLifecycle, newFixLifecycle, newValidateLifecycle, newSubmitLifecycle, newSubmissionLifecycle, nil, showFactories...)
}

// NewWithUpgrade 在 New 的基础上注册可选的 Upgrade 生命周期工厂。
// upgrade 走单独的 variadic 参数，使既有调用点在新增该命令后无需改动签名。
func NewWithUpgrade(stdout, stderr io.Writer, setupLogging SetupLoggingFunc, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory, newUninstallLifecycle UninstallLifecycleFactory, newFixLifecycle FixLifecycleFactory, newValidateLifecycle ValidateLifecycleFactory, newSubmitLifecycle SubmitLifecycleFactory, newSubmissionLifecycle SubmissionQueryLifecycleFactory, newUpgradeLifecycle UpgradeLifecycleFactory, showFactories ...ShowLifecycleFactory) *CLIEntry {
	var newShowLifecycle ShowLifecycleFactory
	if len(showFactories) > 0 {
		newShowLifecycle = showFactories[0]
	}
	return &CLIEntry{
		stdout:                 stdout,
		stderr:                 stderr,
		setupLogging:           setupLogging,
		newSearchLifecycle:     newSearchLifecycle,
		newListLifecycle:       newListLifecycle,
		newShowLifecycle:       newShowLifecycle,
		newInstallLifecycle:    newInstallLifecycle,
		newUpgradeLifecycle:    newUpgradeLifecycle,
		newUninstallLifecycle:  newUninstallLifecycle,
		newFixLifecycle:        newFixLifecycle,
		newValidateLifecycle:   newValidateLifecycle,
		newSubmitLifecycle:     newSubmitLifecycle,
		newSubmissionLifecycle: newSubmissionLifecycle,
	}
}

// SetStdin 覆盖交互确认读取的输入源；未调用时子命令使用进程标准输入。
// 它让「确认询问」在测试中确定可复现，而不必替换全局 os.Stdin。
func (entry *CLIEntry) SetStdin(reader io.Reader) {
	entry.stdin = reader
}

// Execute 解析参数并执行选定的 CLI 命令。
func (entry *CLIEntry) Execute(args []string) error {
	root := newRootCommand(entry.stdout, entry.stderr, entry.newSearchLifecycle, entry.newListLifecycle, entry.newShowLifecycle, entry.newInstallLifecycle, entry.newUpgradeLifecycle, entry.newUninstallLifecycle, entry.newFixLifecycle, entry.newValidateLifecycle, entry.newSubmitLifecycle, entry.newSubmissionLifecycle)
	if entry.stdin != nil {
		root.SetIn(entry.stdin)
	}
	preparseLogging(root, args, entry.setupLogging)
	root.SetArgs(args)
	executed, err := root.ExecuteC()
	if err != nil {
		slog.Error("command failed", slog.Any("error", err))
		var runtime *runtimeFailure
		if !errors.As(err, &runtime) {
			executed.PrintErr(executed.UsageString())
		}
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

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func newRootCommand(stdout, stderr io.Writer, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newShowLifecycle ShowLifecycleFactory, newInstallLifecycle InstallLifecycleFactory, newUpgradeLifecycle UpgradeLifecycleFactory, newUninstallLifecycle UninstallLifecycleFactory, newFixLifecycle FixLifecycleFactory, newValidateLifecycle ValidateLifecycleFactory, newSubmitLifecycle SubmitLifecycleFactory, newSubmissionLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
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
		newSearchCommand(newSearchLifecycle),
		newListCommand(newListLifecycle),
		newShowCommand(newShowLifecycle),
		newInstallCommand(newInstallLifecycle),
		newUpgradeCommand(newUpgradeLifecycle),
		newUninstallCommand(newUninstallLifecycle),
		newFixCommand(newFixLifecycle),
		newValidateCommand(newValidateLifecycle),
		newSubmitCommand(newSubmitLifecycle),
		newSubmissionCommand(newSubmissionLifecycle),
	)
	return root
}
func bindCommonFlags(command *cobra.Command, flags *Flags) {
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all")
	bindTypeFlag(command, flags)
}

func bindTypeFlag(command *cobra.Command, flags *Flags) {
	command.Flags().StringVar(&flags.Type, "type", "", "power type: skill, plugin, mcp, or expert")
}

func logParsedCommand(command *cobra.Command, flags Flags, args []string) {
	attributes := []slog.Attr{
		slog.String("command", command.Name()),
		slog.String("type", flags.Type),
		slog.Any("args", args),
	}
	if flags.Agent != "" {
		attributes = append(attributes, slog.String("agent", flags.Agent))
	}
	slog.LogAttrs(command.Context(), slog.LevelDebug, "command parsed", attributes...)
}

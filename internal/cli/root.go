// Package cli 提供解析和校验参数、调用 Search/List/Install/Uninstall/Fix 生命周期并渲染业务结果的 APM Cobra 命令树。
package cli

import (
	"io"
	"log/slog"
	"reflect"

	"github.com/spf13/cobra"
)

// SetupLoggingFunc 安装与 verbose 设置匹配的默认日志器。
type SetupLoggingFunc func(verbose bool)

// CLIEntry 是 APM 命令行的执行入口。
type CLIEntry struct {
	stdout                io.Writer
	stderr                io.Writer
	setupLogging          SetupLoggingFunc
	newSearchLifecycle    SearchLifecycleFactory
	newListLifecycle      ListLifecycleFactory
	newInstallLifecycle   InstallLifecycleFactory
	newUninstallLifecycle UninstallLifecycleFactory
	newFixLifecycle       FixLifecycleFactory
}

// New 使用指定输出、日志初始化函数和相互独立的 Search/List/Install/Uninstall/Fix 生命周期工厂创建 CLI 入口。
func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory, newUninstallLifecycle UninstallLifecycleFactory, newFixLifecycle FixLifecycleFactory) *CLIEntry {
	return &CLIEntry{
		stdout:                stdout,
		stderr:                stderr,
		setupLogging:          setupLogging,
		newSearchLifecycle:    newSearchLifecycle,
		newListLifecycle:      newListLifecycle,
		newInstallLifecycle:   newInstallLifecycle,
		newUninstallLifecycle: newUninstallLifecycle,
		newFixLifecycle:       newFixLifecycle,
	}
}

// Execute 解析参数并执行选定的 CLI 命令。
func (entry *CLIEntry) Execute(args []string) error {
	root := newRootCommand(entry.stdout, entry.stderr, entry.newSearchLifecycle, entry.newListLifecycle, entry.newInstallLifecycle, entry.newUninstallLifecycle, entry.newFixLifecycle)
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

func newRootCommand(stdout, stderr io.Writer, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory, newInstallLifecycle InstallLifecycleFactory, newUninstallLifecycle UninstallLifecycleFactory, newFixLifecycle FixLifecycleFactory) *cobra.Command {
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
		newInstallCommand(newInstallLifecycle),
		newUninstallCommand(newUninstallLifecycle),
		newFixCommand(newFixLifecycle),
	)
	return root
}
func bindCommonFlags(command *cobra.Command, flags *Flags) {
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all")
	bindTypeFlag(command, flags)
}

func bindTypeFlag(command *cobra.Command, flags *Flags) {
	command.Flags().StringVar(&flags.Type, "type", "", "power type: skill, plugin, or mcp")
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

// Package cli 提供解析和校验参数、调用 Search/List 生命周期并渲染业务结果的 APM Cobra 命令树。
package cli

import (
	"io"
	"log/slog"

	"github.com/spf13/cobra"
)

// SetupLoggingFunc 安装与 verbose 设置匹配的默认日志器。
type SetupLoggingFunc func(verbose bool)

// CLIEntry 是 APM 命令行的执行入口。
type CLIEntry struct {
	stdout             io.Writer
	stderr             io.Writer
	setupLogging       SetupLoggingFunc
	newSearchLifecycle SearchLifecycleFactory
	newListLifecycle   ListLifecycleFactory
}

// New 使用指定输出、日志初始化函数和相互独立的 Search/List 生命周期工厂创建 CLI 入口。
func New(stdout, stderr io.Writer, setupLogging SetupLoggingFunc, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory) *CLIEntry {
	return &CLIEntry{
		stdout:             stdout,
		stderr:             stderr,
		setupLogging:       setupLogging,
		newSearchLifecycle: newSearchLifecycle,
		newListLifecycle:   newListLifecycle,
	}
}

// Execute 解析参数并执行选定的 CLI 命令。
func (entry *CLIEntry) Execute(args []string) error {
	root := newRootCommand(entry.stdout, entry.stderr, entry.newSearchLifecycle, entry.newListLifecycle)
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

func newRootCommand(stdout, stderr io.Writer, newSearchLifecycle SearchLifecycleFactory, newListLifecycle ListLifecycleFactory) *cobra.Command {
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
		newInstallCommand(),
		newUninstallCommand(),
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

// Package cli 提供只解析和校验参数的 APM Cobra 命令树。
package cli

import (
	"log/slog"

	"apm/internal/logging"

	"github.com/spf13/cobra"
)

// CLIEntry 是 APM 命令行的执行入口。
type CLIEntry struct {
	logger *logging.Logger
}

// New 使用统一日志输出创建只解析命令的 CLI 入口。
func New(logger *logging.Logger) *CLIEntry {
	return &CLIEntry{logger: logger}
}

// Execute 解析并校验参数，不执行 Power 生命周期业务。
func (entry *CLIEntry) Execute(args []string) error {
	// Cobra 的 flag value 会保留上次解析结果；每次重建命令树以隔离重复执行。
	root := newRootCommand(entry.logger)
	root.SetArgs(args)
	executed, err := root.ExecuteC()
	if err != nil {
		executed.PrintErrln(executed.ErrPrefix(), err.Error())
		executed.PrintErr(executed.UsageString())
	}
	return err
}

func newRootCommand(logger *logging.Logger) *cobra.Command {
	root := &cobra.Command{
		Use:           "apm",
		Short:         "Agent Power Manager",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(logger.Stdout())
	root.SetErr(logger.Stderr())
	root.PersistentFlags().Bool("verbose", false, "enable verbose diagnostics")
	root.AddCommand(
		newSearchCommand(logger),
		newListCommand(logger),
		newInstallCommand(logger),
		newUninstallCommand(logger),
	)
	return root
}
func bindCommonFlags(command *cobra.Command, flags *Flags) {
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all")
	command.Flags().StringVar(&flags.Type, "type", "", "power type: skill, plugin, or mcp")
}

func logParsedCommand(command *cobra.Command, logger *logging.Logger, flags Flags, args []string) {
	verbose, _ := command.Flags().GetBool("verbose")
	logger.SetVerbose(verbose)
	if !verbose {
		return
	}
	logger.Verbose(
		"command parsed",
		slog.String("command", command.Name()),
		slog.String("agent", flags.Agent),
		slog.String("type", flags.Type),
		slog.Any("args", args),
	)
}

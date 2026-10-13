package cli

import (
	"errors"
	"fmt"

	"apm/internal/domain"

	"github.com/spf13/cobra"
)

func newUpgradeCommand(newLifecycle UpgradeLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade installed powers to their latest versions",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			// 规范化必须先于任何 flags.Type 读取，否则改名前的 "agent" 会让 Expert 类别静默消失。
			if err := resolvePowerType(&flags); err != nil {
				return err
			}
			if err := validateAgentValue(flags); err != nil {
				return err
			}
			if isExpertType(flags.Type) {
				// expert 只支持 bibit；空 --agent 由 validateExpertTypeFlags 允许。
				if err := validateExpertTypeFlags(&flags); err != nil {
					return err
				}
			}
			// upgrade 省略 --agent 的语义是全部已安装 Agent，与 list --installed 的分组模式一致。
			if flags.Agent == "" {
				flags.Agent = string(domain.AllAgents)
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("upgrade lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create upgrade lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create upgrade lifecycle: nil service")
			}
			result, err := service.Upgrade(command.Context(), flags)
			if err == nil {
				return renderUpgrade(command.OutOrStdout(), result, flags)
			}
			var batch *UpgradeBatchError
			if !errors.As(err, &batch) {
				return &runtimeFailure{cause: err}
			}
			// 批处理失败允许先渲染完整结果，再以非零状态返回。
			if renderErr := renderUpgrade(command.OutOrStdout(), result, flags); renderErr != nil {
				return errors.Join(err, renderErr)
			}
			return &runtimeFailure{cause: err}
		},
	}
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all (defaults to all)")
	bindTypeFlag(command, &flags)
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "print the upgrade plan without making changes")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

package cli

import (
	"errors"
	"fmt"
	"io"

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

			// 需要确认但没有 --yes 时，只有在真终端上才能提问；否则 fail closed，
			// 不获取锁、不同步目录。--dry-run 不产生变更，因此从不要求 --yes。
			stdin := command.InOrStdin()
			if upgradeNeedsConfirmation(flags) {
				switch {
				case flags.JSON:
					// --json 的 stdout 是机器契约：机器模式不做交互询问，要求显式 --yes。
					return errors.New("upgrade requires --yes when --json is used")
				case !isInteractiveInput(stdin):
					return errors.New("upgrade requires --yes when not running interactively")
				}
			}

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

			result, err := service.Upgrade(command.Context(), flags, upgradeOptions(command, flags, upgradeNeedsConfirmation(flags), stdin))
			if err == nil {
				return renderUpgrade(command.OutOrStdout(), result, flags)
			}
			// 用户拒绝不是业务结果：不渲染任何业务行，也不打印 usage。
			if errors.Is(err, ErrUpgradeDeclined) {
				return &runtimeFailure{cause: err}
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
	command.Flags().BoolVarP(&flags.Yes, "yes", "y", false, "assume yes for the confirmation prompt")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

// upgradeNeedsConfirmation 报告本次调用是否需要在执行前确认。
// 判定只看「会不会产生变更」：--yes 显式跳过，--dry-run 只读。
// 确认无法进行时（--json 或非交互）由调用方 fail closed，要求显式 --yes。
func upgradeNeedsConfirmation(flags Flags) bool {
	return !flags.Yes && !flags.DryRun
}

// upgradeOptions 构造 lifecycle 需要的副作用边界。
// 确认回调只在需要提问且确为交互输入时提供；进度只在非 --json 时输出到 stderr。
func upgradeOptions(command *cobra.Command, flags Flags, needsPrompt bool, stdin io.Reader) UpgradeOptions {
	options := UpgradeOptions{}
	if needsPrompt {
		options.Confirm = func(prompt UpgradePrompt) (bool, error) {
			return askUpgradeConfirmation(stdin, command.ErrOrStderr(), prompt)
		}
	}
	if !flags.JSON {
		options.Progress = func(progress UpgradeProgress) {
			_ = renderUpgradeProgress(command.ErrOrStderr(), progress)
		}
	}
	return options
}

// renderUpgradeProgress 把一次阶段进度写成一行 stderr 诊断。
// 进度是诊断而非业务结果，因此允许在锁内输出；业务行仍只在成功 Release 后渲染（ADR-003）。
func renderUpgradeProgress(writer io.Writer, progress UpgradeProgress) error {
	var line string
	switch progress.Stage {
	case UpgradeStageExecuting:
		line = fmt.Sprintf(
			"upgrade: [%d/%d] %s/%s/%s@%s -> %s",
			progress.Index+1, progress.Total,
			progress.Agent, progress.Power.Type, progress.Power.Name,
			progress.Current.Version, progress.Power.Version,
		)
	default:
		line = "upgrade: " + string(progress.Stage)
	}
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		return fmt.Errorf("write upgrade progress: %w", err)
	}
	return nil
}

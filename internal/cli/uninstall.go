package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newUninstallCommand(newLifecycle UninstallLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "uninstall <power>",
		Short: "Uninstall a power",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			if strings.Contains(args[0], "=") {
				return errors.New("uninstall does not accept a version")
			}
			flags.Name = args[0]
			if strings.TrimSpace(flags.Name) == "" {
				return errors.New("power name must not be empty")
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("uninstall lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create uninstall lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create uninstall lifecycle: nil service")
			}
			result, err := service.Uninstall(command.Context(), flags)
			if err == nil {
				return renderUninstall(command.OutOrStdout(), result)
			}
			var batch *UninstallBatchError
			if !errors.As(err, &batch) {
				return &runtimeFailure{cause: err}
			}
			if renderErr := renderUninstall(command.OutOrStdout(), result); renderErr != nil {
				return errors.Join(err, renderErr)
			}
			return &runtimeFailure{cause: err}
		},
	}
	bindCommonFlags(command, &flags)
	return command
}

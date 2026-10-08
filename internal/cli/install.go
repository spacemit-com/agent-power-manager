package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newInstallCommand(newLifecycle InstallLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "install <power>[=<version>]",
		Short: "Install a power",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(&flags); err != nil {
				return err
			}
			name, version, err := parsePower(args[0])
			if err != nil {
				return err
			}
			if err := validateInstallVersion(version); err != nil {
				return err
			}
			flags.Name = name
			flags.Version = version
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("install lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create install lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create install lifecycle: nil service")
			}
			result, err := service.Install(command.Context(), flags)
			if err == nil {
				return renderInstall(command.OutOrStdout(), result)
			}
			var batch *InstallBatchError
			if !errors.As(err, &batch) {
				return &runtimeFailure{cause: err}
			}
			if renderErr := renderInstall(command.OutOrStdout(), result); renderErr != nil {
				return errors.Join(err, renderErr)
			}
			return &runtimeFailure{cause: err}
		},
	}
	bindCommonFlags(command, &flags)
	return command
}

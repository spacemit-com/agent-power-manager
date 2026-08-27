package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newListCommand(newLifecycle ListLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list",
		Short: "List powers",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateListFlags(command, flags); err != nil {
				return err
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("list lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create list lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create list lifecycle: nil service")
			}
			result, err := service.List(command.Context(), flags)
			if err == nil {
				return renderList(command.OutOrStdout(), result, flags)
			}
			var partial *PartialListError
			if !errors.As(err, &partial) {
				return err
			}
			if renderErr := renderList(command.OutOrStdout(), result, flags); renderErr != nil {
				return errors.Join(err, renderErr)
			}
			return err
		},
	}
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all; requires --installed")
	command.Flags().BoolVar(&flags.Installed, "installed", false, "list powers installed for the target agent")
	bindTypeFlag(command, &flags)
	command.Flags().BoolVarP(&flags.All, "all", "a", false, "show description and tags")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

func validateListFlags(command *cobra.Command, flags Flags) error {
	if !flags.Installed {
		if command.Flags().Changed("agent") {
			return errors.New("--agent requires --installed")
		}
		return validatePowerType(flags)
	}
	return validateCommon(flags)
}

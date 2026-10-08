package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newListCommand(newLifecycle ListLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list [name]",
		Short: "List powers",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) == 1 {
				flags.Name = strings.TrimSpace(args[0])
				if flags.Name == "" {
					return errors.New("name must not be empty")
				}
			}
			if flags.All && flags.Name == "" {
				return errors.New("--all requires name")
			}
			if err := validateListFlags(command, &flags); err != nil {
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
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all; only with --installed (defaults to all)")
	command.Flags().BoolVar(&flags.Installed, "installed", false, "list powers installed for the target agent")
	bindTypeFlag(command, &flags)
	command.Flags().BoolVarP(&flags.All, "all", "a", false, "show all versions for a named power (Cloud only)")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

func validateListFlags(command *cobra.Command, flags *Flags) error {
	if !flags.Installed {
		if command.Flags().Changed("agent") {
			return errors.New("--agent requires --installed")
		}
		return resolvePowerType(flags)
	}
	if isExpertType(flags.Type) {
		return validateExpertTypeFlags(flags)
	}
	if strings.TrimSpace(flags.Agent) == "" && command.Flags().Changed("agent") {
		return errors.New("--agent must not be empty")
	}
	if err := validateAgentValue(*flags); err != nil {
		return err
	}
	return resolvePowerType(flags)
}

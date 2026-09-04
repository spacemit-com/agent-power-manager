package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newShowCommand(newLifecycle ShowLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "show [name]",
		Short: "Show power details",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			flags.Name = strings.TrimSpace(args[0])
			if flags.Name == "" {
				return errors.New("name must not be empty")
			}
			if err := validatePowerType(flags); err != nil {
				return err
			}
			if newLifecycle == nil {
				return errors.New("show lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create show lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create show lifecycle: nil service")
			}
			result, err := service.Show(command.Context(), flags)
			if err != nil {
				return err
			}
			return renderShow(command.OutOrStdout(), result, flags.JSON)
		},
	}
	bindTypeFlag(command, &flags)
	command.Flags().StringVar(&flags.Version, "version", "", "specific power version (defaults to latest)")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

package cli

import (
	"errors"
	"fmt"
	"strings"

	"apm/internal/domain"
	"github.com/spf13/cobra"
)

func newShowCommand(newLifecycle ShowLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "show <name>[=<version>]",
		Short: "Show power details",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			name, version, err := parsePower(args[0])
			if err != nil {
				return err
			}
			flags.Name = strings.TrimSpace(name)
			flags.Version = version
			if !domain.IsCanonicalPowerName(flags.Name) {
				return fmt.Errorf("invalid power name %q", flags.Name)
			}
			if flags.Version != "" {
				if _, err := domain.CanonicalPowerVersion(flags.Version); err != nil {
					return fmt.Errorf("invalid power version %q: must be vMAJOR.MINOR.PATCH", flags.Version)
				}
			}
			if err := resolvePowerType(&flags); err != nil {
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
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

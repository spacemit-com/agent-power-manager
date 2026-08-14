package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func newFixCommand(newLifecycle FixLifecycleFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "fix",
		Short: "Recover an interrupted transaction",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			logParsedCommand(command, Flags{}, args)
			if newLifecycle == nil {
				return errors.New("fix lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create fix lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create fix lifecycle: nil service")
			}
			return service.Fix(command.Context())
		},
	}
}

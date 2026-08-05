package cli

import (
	"apm/internal/logging"

	"github.com/spf13/cobra"
)

func newListCommand(logger *logging.Logger) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list",
		Short: "List powers",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			logParsedCommand(command, logger, flags, args)
			return nil
		},
	}
	bindCommonFlags(command, &flags)
	command.Flags().BoolVar(&flags.Installed, "installed", false, "list installed powers")
	return command
}

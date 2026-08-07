package cli

import "github.com/spf13/cobra"

func newListCommand() *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list",
		Short: "List powers",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			logParsedCommand(command, flags, args)
			return nil
		},
	}
	bindCommonFlags(command, &flags)
	command.Flags().BoolVar(&flags.Installed, "installed", false, "list installed powers")
	return command
}

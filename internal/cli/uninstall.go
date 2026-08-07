package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

func newUninstallCommand() *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "uninstall <power>",
		Short: "Uninstall a power",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			flags.Name = args[0]
			if strings.TrimSpace(flags.Name) == "" {
				return errors.New("power name must not be empty")
			}
			logParsedCommand(command, flags, args)
			return nil
		},
	}
	bindCommonFlags(command, &flags)
	return command
}

package cli

import "github.com/spf13/cobra"

func newInstallCommand() *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "install <power>[=<version>]",
		Short: "Install a power",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			name, version, err := parsePower(args[0])
			if err != nil {
				return err
			}
			flags.Name = name
			flags.Version = version
			logParsedCommand(command, flags, args)
			return nil
		},
	}
	bindCommonFlags(command, &flags)
	return command
}

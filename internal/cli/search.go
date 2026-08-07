package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCommand() *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "search <keyword>",
		Short: "Search available powers",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateCommon(flags); err != nil {
				return err
			}
			flags.Keyword = args[0]
			if strings.TrimSpace(flags.Keyword) == "" {
				return errors.New("keyword must not be empty")
			}
			logParsedCommand(command, flags, args)
			return nil
		},
	}
	bindCommonFlags(command, &flags)
	return command
}

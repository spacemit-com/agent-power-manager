package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCommand(newLifecycle SearchLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "search <keyword>",
		Short: "Search available powers",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validatePowerType(flags); err != nil {
				return err
			}
			flags.Keyword = strings.TrimSpace(args[0])
			if flags.Keyword == "" {
				return errors.New("keyword must not be empty")
			}
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("search lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create search lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create search lifecycle: nil service")
			}
			result, err := service.Search(command.Context(), flags)
			if err != nil {
				return err
			}
			return renderSearch(command.OutOrStdout(), result, flags)
		},
	}
	bindTypeFlag(command, &flags)
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}

func renderSearch(writer io.Writer, result SearchResult, flags Flags) error {
	// search 默认展示 description、不展示 tags。
	return renderPowers(writer, result.Powers, powerMetadata{description: true}, flags.JSON)
}

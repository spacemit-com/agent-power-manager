package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"apm/internal/cloud"
	"github.com/spf13/cobra"
)

func newSubmitCommand(newLifecycle SubmitLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "submit <path>",
		Short: "Submit a skill, plugin, MCP, or agent to APC",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateSubmitFlags(flags); err != nil {
				return err
			}
			flags.Message = strings.TrimSpace(flags.Message)
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("submit lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create submit lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create submit lifecycle: nil service")
			}
			result, err := service.Submit(command.Context(), flags, args)
			if err != nil {
				if errors.Is(err, cloud.ErrTooManyActiveSubmissions) {
					return fmt.Errorf("too many active submissions; run `apm submission list` before retrying: %w", err)
				}
				return err
			}
			return renderSubmit(command.OutOrStdout(), result)
		},
	}
	bindTypeFlag(command, &flags)
	command.Flags().StringVar(&flags.Message, "message", "", "describe this submission")
	return command
}

func validateSubmitFlags(flags Flags) error {
	if err := requireSubmissionType(flags); err != nil {
		return err
	}
	if strings.TrimSpace(flags.Message) == "" {
		return errors.New("--message is required")
	}
	return nil
}

func renderSubmit(writer io.Writer, result SubmitResult) error {
	lines := []string{
		"outcome: " + result.Outcome,
		"submission_id: " + result.SubmissionID,
		"status: " + result.Status,
		"track: apm submission get " + result.SubmissionID,
	}
	for _, line := range lines {
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			return fmt.Errorf("write submit result: %w", err)
		}
	}
	return nil
}

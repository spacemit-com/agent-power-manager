package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

func newSubmitCommand(newLifecycle SubmitLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "submit <path>",
		Short: "Submit a skill, plugin, or MCP to APC",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateSubmitFlags(flags); err != nil {
				return err
			}
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
				return err
			}
			return renderSubmit(command.OutOrStdout(), result)
		},
	}
	bindTypeFlag(command, &flags)
	command.Flags().StringVar(&flags.Version, "version", "", "skill version vMAJOR.MINOR.PATCH")
	command.Flags().StringVar(&flags.ChangeSummary, "change-summary", "", "what changed in this submission")
	return command
}

func validateSubmitFlags(flags Flags) error {
	if err := requireSubmissionType(flags); err != nil {
		return err
	}
	switch flags.Type {
	case "skill":
		if strings.TrimSpace(flags.Version) == "" {
			return errors.New("--version is required for skill submit")
		}
		if strings.TrimSpace(flags.ChangeSummary) == "" {
			return errors.New("--change-summary is required for skill submit")
		}
	case "plugin":
		if strings.TrimSpace(flags.ChangeSummary) == "" {
			return errors.New("--change-summary is required for plugin submit")
		}
	case "agent":
		if strings.TrimSpace(flags.Version) != "" {
			return errors.New("--version is not allowed for agent submit; version comes from agent.yaml")
		}
		if strings.TrimSpace(flags.ChangeSummary) == "" {
			return errors.New("--change-summary is required for agent submit")
		}
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

package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func newSubmissionCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	command := &cobra.Command{
		Use:   "submission",
		Short: "Query submissions",
	}
	command.AddCommand(newSubmissionGetCommand(newLifecycle), newSubmissionListCommand(newLifecycle))
	return command
}

func newSubmissionGetCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <submission-id>",
		Short: "Show one submission",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if newLifecycle == nil {
				return errors.New("submission lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create submission query lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create submission query lifecycle: nil service")
			}
			result, err := service.Get(command.Context(), strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			return renderSubmissionGet(command.OutOrStdout(), result)
		},
	}
}

func newSubmissionListCommand(newLifecycle SubmissionQueryLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "list",
		Short: "List submissions",
		Args:  cobra.ExactArgs(0),
		RunE: func(command *cobra.Command, args []string) error {
			logParsedCommand(command, flags, args)
			if newLifecycle == nil {
				return errors.New("submission lifecycle factory is required")
			}
			service, err := newLifecycle()
			if err != nil {
				return fmt.Errorf("create submission query lifecycle: %w", err)
			}
			if isNilInterface(service) {
				return errors.New("create submission query lifecycle: nil service")
			}
			result, err := service.List(command.Context(), flags)
			if err != nil {
				return err
			}
			return renderSubmissionList(command.OutOrStdout(), result)
		},
	}
	command.Flags().StringVar(&flags.Status, "status", "", "filter by submission status")
	command.Flags().StringVar(&flags.Type, "type", "", "filter by power type")
	command.Flags().StringVar(&flags.Name, "name", "", "filter by power name")
	command.Flags().IntVar(&flags.Limit, "limit", 0, "page size")
	return command
}

func renderSubmissionGet(writer io.Writer, result SubmissionQueryResult) error {
	if result.Detail == nil {
		return errors.New("submission get returned no detail")
	}
	detail := result.Detail
	lines := []string{
		"submission_id: " + detail.SubmissionID,
		"type: " + string(detail.Type),
		"name: " + detail.Name,
		"version: " + detail.Version,
		"status: " + string(detail.Status),
		"content_available: " + strconv.FormatBool(detail.ContentAvailable),
	}
	if detail.PublishedVersion != "" {
		lines = append(lines, "published_version: "+detail.PublishedVersion)
	}
	if detail.ReviewComment != "" {
		lines = append(lines, "review_comment: "+detail.ReviewComment)
	}
	for _, validationError := range detail.ValidationErrors {
		lines = append(lines, "validation_error: "+validationError)
	}
	for _, line := range lines {
		if _, err := io.WriteString(writer, line+"\n"); err != nil {
			return fmt.Errorf("write submission detail: %w", err)
		}
	}
	return nil
}

func renderSubmissionList(writer io.Writer, result SubmissionQueryResult) error {
	for _, item := range result.Summaries {
		line := item.SubmissionID + " " + string(item.Type) + "/" + item.Name + "@" + item.Version + " " + string(item.Status)
		if item.CreatedAt != "" {
			line += " " + item.CreatedAt
		}
		line += "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return fmt.Errorf("write submission list: %w", err)
		}
	}
	return nil
}

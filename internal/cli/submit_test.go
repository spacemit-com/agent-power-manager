package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"apm/internal/cli"
)

func TestSubmitRequiresTypeAndMetadata(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing type", args: []string{"submit", "dir"}, want: "--type must be skill, plugin, or mcp"},
		{name: "skill missing version", args: []string{"submit", "dir", "--type=skill", "--change-summary=x"}, want: "--version is required for skill submit"},
		{name: "skill missing summary", args: []string{"submit", "dir", "--type=skill", "--version=v1.0.0"}, want: "--change-summary is required for skill submit"},
		{name: "plugin missing summary", args: []string{"submit", "dir", "--type=plugin"}, want: "--change-summary is required for plugin submit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			lifecycle := &recordingSubmitLifecycle{}
			entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

			err := entry.Execute(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute(%q) error = %v, want containing %q", tt.args, err, tt.want)
			}
			if lifecycle.calls != 0 {
				t.Fatalf("factory calls = %d, want 0 (validation precedes factory)", lifecycle.calls)
			}
		})
	}
}

func TestSubmitInvokesLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmitLifecycle{result: cli.SubmitResult{SubmissionID: "sub_1", Status: "received"}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

	if err := entry.Execute([]string{"submit", "dir", "--type=skill", "--version=v1.0.0", "--change-summary=first release"}); err != nil {
		t.Fatalf("Execute(submit) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", lifecycle.calls)
	}
	if lifecycle.flags.Type != "skill" || lifecycle.flags.Version != "v1.0.0" || lifecycle.flags.ChangeSummary != "first release" {
		t.Fatalf("flags = %#v", lifecycle.flags)
	}
	if out := stdout.String(); !strings.Contains(out, "submission_id: sub_1") || !strings.Contains(out, "status: received") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmitMCPDoesNotRequireMetadataFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmitLifecycle{result: cli.SubmitResult{SubmissionID: "sub_1", Status: "received"}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))

	if err := entry.Execute([]string{"submit", "dir", "--type=mcp"}); err != nil {
		t.Fatalf("Execute(submit mcp) error = %v", err)
	}
	if lifecycle.calls != 1 {
		t.Fatalf("factory calls = %d, want 1", lifecycle.calls)
	}
}

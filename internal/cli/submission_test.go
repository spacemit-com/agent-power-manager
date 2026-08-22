package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestSubmissionGetInvokesLifecycle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Detail: &domain.Submission{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReviewPending}}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"submission", "get", "sub_1"}); err != nil {
		t.Fatalf("Execute(submission get) error = %v", err)
	}
	if lifecycle.getCalls != 1 || len(lifecycle.getIDs) != 1 || lifecycle.getIDs[0] != "sub_1" {
		t.Fatalf("Get calls = %v, want [sub_1]", lifecycle.getIDs)
	}
	if out := stdout.String(); !strings.Contains(out, "submission_id: sub_1") || !strings.Contains(out, "status: review_pending") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmissionListInvokesLifecycleWithFilters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	lifecycle := &recordingSubmissionLifecycle{result: cli.SubmissionQueryResult{Summaries: []domain.SubmissionSummary{{SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Status: domain.SubmissionReceived, CreatedAt: "2024-01-01T00:00:00Z"}}}}
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return lifecycle, nil })

	if err := entry.Execute([]string{"submission", "list", "--status=received", "--type=skill", "--name=demo", "--limit=10"}); err != nil {
		t.Fatalf("Execute(submission list) error = %v", err)
	}
	if lifecycle.listCalls != 1 {
		t.Fatalf("List calls = %d, want 1", lifecycle.listCalls)
	}
	flags := lifecycle.listFlags[0]
	if flags.Status != "received" || flags.Type != "skill" || flags.Name != "demo" || flags.Limit != 10 {
		t.Fatalf("flags = %#v", flags)
	}
	if out := stdout.String(); !strings.Contains(out, "sub_1 skill/demo@v1.0.0 received") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSubmissionGetRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), unexpectedSubmit(t), func() (cli.SubmissionQueryLifecycle, error) { return &recordingSubmissionLifecycle{}, nil })
	if err := entry.Execute([]string{"submission", "get"}); err == nil {
		t.Fatal("Execute(submission get) error = nil, want missing id error")
	}
}

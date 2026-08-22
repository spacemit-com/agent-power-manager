package local

import (
	"context"
	"errors"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalSubmissionUnsupported(t *testing.T) {
	client, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Submit(context.Background(), domain.SubmissionRequest{}); !errors.Is(err, cloud.ErrSubmissionUnsupported) {
		t.Fatalf("Submit() error = %v, want ErrSubmissionUnsupported", err)
	}
	if _, err := client.GetSubmission(context.Background(), "sub_1"); !errors.Is(err, cloud.ErrSubmissionUnsupported) {
		t.Fatalf("GetSubmission() error = %v, want ErrSubmissionUnsupported", err)
	}
	if _, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{}); !errors.Is(err, cloud.ErrSubmissionUnsupported) {
		t.Fatalf("ListSubmissions() error = %v, want ErrSubmissionUnsupported", err)
	}
}

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestLocalAgentReadWorksWhileSubmissionUnsupported(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}

	destination := filepath.Join(t.TempDir(), "agent.yaml")
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DownloadAgent(context.Background(), content.entry, destination); err != nil {
		t.Fatalf("DownloadAgent() error = %v", err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil || len(contents) == 0 {
		t.Fatalf("DownloadAgent() content = %d bytes, %v; want non-empty agent.yaml", len(contents), err)
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

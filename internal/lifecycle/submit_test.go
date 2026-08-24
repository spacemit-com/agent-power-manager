package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestNewSubmitRejectsNilCloud(t *testing.T) {
	if _, err := NewSubmit(nil); err == nil {
		t.Fatal("NewSubmit(nil) error = nil, want error")
	}
	if _, err := NewSubmit((*recordingSubmissionClient)(nil)); err == nil {
		t.Fatal("NewSubmit(typed nil) error = nil, want error")
	}
}

func TestSubmitPreparesAndUploads(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{SubmissionID: "sub_1", Status: domain.SubmissionReceived},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	result, err := service.Submit(context.Background(), cli.Flags{Type: "skill", Version: "v9.9.9", Message: "  first release  "}, []string{dir})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.SubmissionID != "sub_1" || result.Status != string(domain.SubmissionReceived) {
		t.Fatalf("Submit() = %#v", result)
	}
	if len(client.submitCalls) != 1 {
		t.Fatalf("cloud Submit calls = %d, want 1", len(client.submitCalls))
	}
	request := client.submitCalls[0]
	if request.Type != domain.PowerTypeSkill || request.Name != "demo" || request.Version != "v1.0.0" || request.Message != "first release" {
		t.Fatalf("SubmissionRequest = %#v", request)
	}
	if request.ContentType == "" || request.ContentMD5 == "" || request.IdempotencyKey == "" || len(request.Content) == 0 {
		t.Fatalf("SubmissionRequest missing content fields: %#v", request)
	}
}

func TestSubmitForwardsCloudError(t *testing.T) {
	uploadErr := errors.New("upload failed")
	client := &recordingSubmissionClient{submitErr: uploadErr}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	_, err = service.Submit(context.Background(), cli.Flags{Type: "skill", Message: "x"}, []string{dir})
	if !errors.Is(err, uploadErr) {
		t.Fatalf("Submit() error = %v, want %v", err, uploadErr)
	}
}

func TestSubmitRejectsMissingMessageBeforeUpload(t *testing.T) {
	client := &recordingSubmissionClient{}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	_, err = service.Submit(context.Background(), cli.Flags{Type: "skill"}, []string{dir})
	if err == nil {
		t.Fatal("Submit() error = nil, want error for missing message")
	}
	if len(client.submitCalls) != 0 {
		t.Fatalf("cloud Submit calls = %d, want 0 (validation precedes upload)", len(client.submitCalls))
	}
}

func writeVersionedLifecycleSkill(t *testing.T, dir string) {
	t.Helper()
	writeLifecycleSkill(t, dir)
	contents := "---\nname: demo\nversion: \"v1.0.0\"\ndescription: does things\n---\n# Demo Skill\n\nDetails here.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitAgentMapsAcceptedOutcome(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{Outcome: domain.SubmissionAccepted, SubmissionID: "sub_1", Status: domain.SubmissionReceived},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	result, err := service.Submit(context.Background(), cli.Flags{Type: "agent", Message: "first release"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Outcome != string(domain.SubmissionAccepted) || result.SubmissionID != "sub_1" || result.Status != string(domain.SubmissionReceived) {
		t.Fatalf("Submit() = %#v", result)
	}
	if len(client.submitCalls) != 1 {
		t.Fatalf("cloud Submit calls = %d, want 1", len(client.submitCalls))
	}
	request := client.submitCalls[0]
	if request.Type != domain.PowerTypeAgent || request.Name != "demo-agent" || request.Version != "v1.2.3" || request.Message != "first release" || request.ContentType != "application/yaml" {
		t.Fatalf("SubmissionRequest = %#v", request)
	}
	if !bytes.Equal(request.Content, []byte(lifecycleAgentYAML)) || request.ContentMD5 == "" || request.IdempotencyKey == "" {
		t.Fatalf("SubmissionRequest missing content fields: %#v", request)
	}
}

func TestSubmitAgentMapsReplayedOutcome(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{Outcome: domain.SubmissionReplayed, SubmissionID: "sub_2", Status: domain.SubmissionPublished},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	result, err := service.Submit(context.Background(), cli.Flags{Type: "agent", Message: "first release"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Outcome != string(domain.SubmissionReplayed) || result.SubmissionID != "sub_2" || result.Status != string(domain.SubmissionPublished) {
		t.Fatalf("Submit() = %#v", result)
	}
}

func TestSubmitAgentIgnoresVersionOverrideAtLifecycleBoundary(t *testing.T) {
	client := &recordingSubmissionClient{}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	_, err = service.Submit(context.Background(), cli.Flags{Type: "agent", Version: "v9.9.9", Message: "x"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(client.submitCalls) != 1 || client.submitCalls[0].Version != "v1.2.3" {
		t.Fatalf("cloud Submit calls = %#v, want source-owned version v1.2.3", client.submitCalls)
	}
}

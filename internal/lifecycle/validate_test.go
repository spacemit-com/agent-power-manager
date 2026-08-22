package lifecycle

import (
	"context"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestNewValidateReturnsService(t *testing.T) {
	service, err := NewValidate()
	if err != nil {
		t.Fatalf("NewValidate() error = %v", err)
	}
	if service == nil {
		t.Fatal("NewValidate() = nil, want service")
	}
}

func TestValidateOfflineMapsResult(t *testing.T) {
	service, err := NewValidate()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLifecycleSkill(t, dir)

	result, err := service.Validate(context.Background(), cli.Flags{Type: "skill", Version: "v1.0.0"}, []string{dir})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.Type != string(domain.PowerTypeSkill) || result.Name != "demo" || result.Version != "v1.0.0" || result.Description != "does things" {
		t.Fatalf("Validate() = %#v", result)
	}
	if result.FileCount != 2 || result.ContentSize == 0 {
		t.Fatalf("Validate() file count/size = %d/%d", result.FileCount, result.ContentSize)
	}
}

func TestValidateForwardsInvalidContent(t *testing.T) {
	service, err := NewValidate()
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Validate(context.Background(), cli.Flags{Type: "skill"}, []string{t.TempDir()})
	if err == nil {
		t.Fatal("Validate() error = nil, want error for empty source")
	}
}

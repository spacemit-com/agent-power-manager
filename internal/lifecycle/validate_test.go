package lifecycle

import (
	"context"
	"strings"
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
	writeVersionedLifecycleSkill(t, dir)

	result, err := service.Validate(context.Background(), cli.Flags{Type: "skill", Version: "v9.9.9"}, []string{dir})
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

// TestValidateAgentOfflineReportsIDNameMismatch 校验 agent.id 与 agent.name 取值不同时，
// warning 会传达到 CLI 契约且校验仍然成功、外部身份不变（ADR-034）。
func TestValidateAgentOfflineReportsIDNameMismatch(t *testing.T) {
	service, err := NewValidate()
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgentYAML(t, t.TempDir(), lifecycleAgentYAMLMismatchedID)

	result, err := service.Validate(context.Background(), cli.Flags{Type: "agent"}, []string{path})
	if err != nil {
		t.Fatalf("Validate() error = %v, want nil: id 与 name 不同取值不是校验错误", err)
	}
	if result.Name != "demo-agent" || result.Version != "v1.0.0" {
		t.Fatalf("Validate() = %#v", result)
	}
	if len(result.Warnings) != 1 {
		t.Fatalf("Validate() warnings = %v, want exactly 1", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], `agent.id "bibit-internal-demo"`) ||
		!strings.Contains(result.Warnings[0], `agent.name "demo-agent"`) {
		t.Fatalf("Validate() warning = %q, want both id and name", result.Warnings[0])
	}
}

func TestValidateAgentOfflineMapsResult(t *testing.T) {
	service, err := NewValidate()
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	result, err := service.Validate(context.Background(), cli.Flags{Type: "agent"}, []string{path})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if result.Type != string(domain.PowerTypeAgent) || result.Name != "demo-agent" || result.Version != "v1.0.0" || result.Description != "demo" {
		t.Fatalf("Validate() = %#v", result)
	}
	if result.FileCount != 1 || result.ContentSize != int64(len(lifecycleAgentYAML)) {
		t.Fatalf("Validate() file count/size = %d/%d", result.FileCount, result.ContentSize)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Validate() warnings = %v, want none: id 与 name 同值不应告警", result.Warnings)
	}
}

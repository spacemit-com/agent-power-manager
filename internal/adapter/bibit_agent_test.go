package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/paths"
)

func newBibitAgentFixture(t *testing.T, createRoot bool) (root, staging string) {
	t.Helper()
	home := t.TempDir()
	root = filepath.Join(home, ".bianbu-agent")
	t.Setenv("APM_HOME", filepath.Join(home, "apm-home"))
	t.Setenv("BIANBU_AGENT_HOME", root)
	if createRoot {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	staging, err = paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	return root, staging
}

func writeAgentStaging(t *testing.T, staging, name string, raw []byte) {
	t.Helper()
	dir := filepath.Join(staging, "agents", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBibitAgentAdapterTargetPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	target, err := a.TargetPath(ref)
	if err != nil || target != filepath.Join(root, "agents", "bianbu-agent-default", "agent.yaml") {
		t.Fatalf("TargetPath() = %q, %v", target, err)
	}
}

func TestBibitAgentAdapterInstallsYAMLVerbatim(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	raw := []byte("schema_version: 1\nagent:\n  id: bianbu-agent-default\n  version: v1.0.0\n  description: demo\n  name: X\n")
	writeAgentStaging(t, staging, ref.Name, raw)

	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	result, err := a.Install(ref, staging)
	if err != nil || !result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	target := filepath.Join(root, "agents", "bianbu-agent-default", "agent.yaml")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed agent: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("installed bytes = %q, want verbatim %q", got, raw)
	}
}

func TestBibitAgentAdapterRejectsInvalidYAML(t *testing.T) {
	_, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "x", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	writeAgentStaging(t, staging, "x", []byte("schema_version: 1\nagent:\n  id: x\n  version: bad\n"))

	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	if _, err := a.Install(ref, staging); err == nil {
		t.Fatal("Install() error = nil, want validation error")
	}
}

func TestBibitAgentValidateArtifactRequiresExactIdentity(t *testing.T) {
	root, _ := newBibitAgentFixture(t, true)
	path := filepath.Join(root, "candidate.yaml")
	raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	if err := a.ValidateArtifact(ref, path); err != nil {
		t.Fatalf("ValidateArtifact() error = %v", err)
	}
	mismatch := ref
	mismatch.Version = "v2.0.0"
	if err := a.ValidateArtifact(mismatch, path); !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestBibitAgentInstallRejectsStagingIdentityMismatchWithoutPublishing(t *testing.T) {
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
	writeAgentStaging(t, staging, ref.Name, []byte("schema_version: 1\nagent: {id: demo, version: v2.0.0, description: demo}\n"))
	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	if result, err := a.Install(ref, staging); err == nil || result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "agents", "demo", "agent.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists: %v", err)
	}
}

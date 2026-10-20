package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

// subResourceSkillMD 是合法的 v2 SKILL.md 模板，%NAME% 由写入方替换。
const subResourceSkillMD = "---\n" +
	"schema_version: 2\n" +
	"name: %NAME%\n" +
	"version: v1.0.0\n" +
	"description: Probe skill.\n" +
	"name_zh: 探针技能\n" +
	"name_en: Probe Skill\n" +
	"description_zh: 探针技能用途。\n" +
	"---\n" +
	"# Probe\n\nBody.\n"

const subResourceSkillCard = "---\n" +
	"schema_version: 2\n" +
	"---\n" +
	"# Skill Card: Probe Skill\n\n" +
	"## Capability\nDoes a thing.\n\n" +
	"## Use Cases\nProbing.\n\n" +
	"## Inputs and Outputs\nNone.\n\n" +
	"## Dependencies\nNone.\n\n" +
	"## Limitations\nNone.\n\n" +
	"## Maintainer\nOwner: Probe\nContact: probe@example.com\n"

// writeSubResourceAgentDirectory 在 base/agents/<name> 下写入带独有子资源的 Agent 目录。
func writeSubResourceAgentDirectory(t *testing.T, base, name string, raw []byte) {
	t.Helper()
	dir := filepath.Join(base, "agents", name)
	for _, sub := range []string{"assets", "skills/probe-skill", "mcp"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{
		"agent.yaml":                       raw,
		"assets/avatar.png":                testAgentAvatarPNG,
		"skills/probe-skill/SKILL.md":      []byte(strings.ReplaceAll(subResourceSkillMD, "%NAME%", "probe-skill")),
		"skills/probe-skill/skill-card.md": []byte(subResourceSkillCard),
		"mcp/probe-mcp.json": []byte(`{"schema_version":2,"name":"probe-mcp","name_zh":"探针MCP",` +
			`"name_en":"Probe MCP","version":"v1.0.0","description":"Probe MCP server.",` +
			`"description_zh":"探针MCP服务。","definition":{"transport":"stdio","command":"probe"}}`),
	}
	for relative, body := range files {
		target := filepath.Join(dir, filepath.FromSlash(relative))
		if err := os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstallAgentLandsSubResourcesInsideAgentDirectory(t *testing.T) {
	// ADR-046：子资源原样落在 agents/<name>/ 下，APM 不拆到 Bibit 全局目录。
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo-agent", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeSubResourceAgentDirectory(t, staging, ref.Name, []byte(agentFixtureYAML(ref)))

	adp, err := NewRegistry().ResolveAgent(domain.AgentBibit)
	if err != nil {
		t.Fatalf("ResolveAgent() error = %v", err)
	}
	result, err := adp.Install(ref, staging)
	if err != nil {
		t.Fatalf("Install() error = %v, want sub-resources accepted", err)
	}
	if !result.Published {
		t.Fatal("Install() Published = false, want true")
	}

	target := filepath.Join(root, "agents", ref.Name)
	for _, relative := range []string{
		"agent.yaml",
		"skills/probe-skill/SKILL.md",
		"skills/probe-skill/skill-card.md",
		"mcp/probe-mcp.json",
	} {
		if _, err := os.Stat(filepath.Join(target, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("installed agent is missing %q: %v", relative, err)
		}
	}
	// 子资源不得被提升到 agents/<name>/ 之外。
	if _, err := os.Stat(filepath.Join(root, "skills")); !os.IsNotExist(err) {
		t.Fatalf("agent root has a top-level skills directory (err = %v), want sub-resources kept inside the agent", err)
	}
}

func TestInstallAgentRejectsIncompleteSubResource(t *testing.T) {
	// 残缺子资源必须在发布前被拒，且不留下可见目标。
	root, staging := newBibitAgentFixture(t, true)
	ref := domain.PowerRef{Name: "demo-agent", Type: domain.PowerTypeExpert, Version: "v1.0.0"}
	writeSubResourceAgentDirectory(t, staging, ref.Name, []byte(agentFixtureYAML(ref)))
	if err := os.Remove(filepath.Join(staging, "agents", ref.Name, "skills", "probe-skill", "skill-card.md")); err != nil {
		t.Fatal(err)
	}

	adp, err := NewRegistry().ResolveAgent(domain.AgentBibit)
	if err != nil {
		t.Fatalf("ResolveAgent() error = %v", err)
	}
	if _, err := adp.Install(ref, staging); err == nil {
		t.Fatal("Install() error = nil, want a missing skill-card.md rejection")
	}
	if _, err := os.Lstat(filepath.Join(root, "agents", ref.Name)); !os.IsNotExist(err) {
		t.Fatalf("failed install left a visible target (err = %v)", err)
	}
}

package agentartifact_test

import (
	"errors"
	"strings"
	"testing"

	"apm/internal/agentartifact"
	"apm/internal/domain"
)

// subResourceAgentYAML 是带子资源的 Agent 清单；身份取 probe-agent@v1.0.0。
const subResourceAgentYAML = `schema_version: 2
agent:
  id: probe-agent
  name: probe-agent
  version: v1.0.0
  description: Probe agent carrying exclusive sub-resources.
  name_zh: 探针
  name_en: Probe Agent
  description_zh: 探针用途。
  soul: You are a probe.
`

const subSkillMD = `---
schema_version: 2
name: %NAME%
version: v1.0.0
description: Probe skill.
name_zh: 探针技能
name_en: Probe Skill
description_zh: 探针技能用途。
---
# Probe

Body.
`

const subSkillCard = `---
schema_version: 2
---
# Skill Card: Probe Skill

## Capability
Does a thing.

## Use Cases
Probing.

## Inputs and Outputs
None.

## Dependencies
None.

## Limitations
None.

## Maintainer
Owner: Probe
Contact: probe@example.com
`

// subMCPJSON 是合法的 v2 MCP 资源，name 由 %NAME% 替换。
const subMCPJSON = `{"schema_version":2,"name":"%NAME%","name_zh":"探针MCP","name_en":"Probe MCP","version":"v1.0.0","description":"Probe MCP server.","description_zh":"探针MCP服务。","definition":{"transport":"stdio","command":"probe"}}`

// subPluginJSON 是合法的 v2 APC Plugin 清单，声明 skills/probe 一个内嵌 Skill。
const subPluginJSON = `{"schema_version":2,"name":"%NAME%","version":"v1.0.0","description":"Probe plugin.","name_zh":"探针插件","name_en":"Probe Plugin","description_zh":"探针插件。","skills":["skills/probe"]}`

// skillMD 以名称替换占位符。
func skillMD(name string) string { return strings.ReplaceAll(subSkillMD, "%NAME%", name) }

func mcpJSON(name string) string { return strings.ReplaceAll(subMCPJSON, "%NAME%", name) }

func pluginJSON(name string) string { return strings.ReplaceAll(subPluginJSON, "%NAME%", name) }

// newSubResourceEntries 构造一个只含 agent.yaml 的最小快照。
func newSubResourceEntries() []agentartifact.Entry {
	return []agentartifact.Entry{
		{Path: "agent.yaml", Mode: 0o644, Data: []byte(subResourceAgentYAML)},
	}
}

// withDir 追加一个目录条目。
func withDir(entries []agentartifact.Entry, path string) []agentartifact.Entry {
	return append(entries, agentartifact.Entry{Path: path, Directory: true, Mode: 0o755})
}

// withFile 追加一个普通文件条目。
func withFile(entries []agentartifact.Entry, path, body string) []agentartifact.Entry {
	return append(entries, agentartifact.Entry{Path: path, Mode: 0o644, Data: []byte(body)})
}

// addSkill 在 root 下加入一个完整 Skill 子资源。
func addSkill(entries []agentartifact.Entry, root, name string) []agentartifact.Entry {
	entries = withDir(entries, root)
	entries = withFile(entries, root+"/SKILL.md", skillMD(name))
	entries = withFile(entries, root+"/skill-card.md", subSkillCard)
	return entries
}

// addPlugin 在 root 下加入一个完整 APC Plugin 子资源。
func addPlugin(entries []agentartifact.Entry, root, name string) []agentartifact.Entry {
	entries = withDir(entries, root)
	entries = withDir(entries, root+"/skills")
	entries = withFile(entries, root+"/apc-plugin.json", pluginJSON(name))
	entries = addSkill(entries, root+"/skills/probe", name+"-inner")
	return entries
}

func probeRef() domain.PowerRef {
	return domain.PowerRef{Type: domain.PowerTypeExpert, Name: "probe-agent", Version: "v1.0.0"}
}

func TestValidateAcceptsAgentWithSubResources(t *testing.T) {
	entries := newSubResourceEntries()
	entries = addSkill(entries, "skills/probe-skill", "probe-skill")
	entries = addPlugin(entries, "plugins/probe-plugin", "probe-plugin")
	entries = withDir(entries, "mcp")
	entries = withFile(entries, "mcp/probe-mcp.json", mcpJSON("probe-mcp"))

	snapshot, err := agentartifact.Validate(entries, nil)
	if err != nil {
		t.Fatalf("Validate() error = %v, want sub-resources accepted", err)
	}
	if snapshot.Config.Agent.Name != "probe-agent" {
		t.Fatalf("agent name = %q, want probe-agent", snapshot.Config.Agent.Name)
	}
	// 子资源计入文件数：agent.yaml + 2 个 Skill 文件 + 插件清单 + 内嵌 Skill 2 文件 + 1 个 MCP。
	if snapshot.FileCount != 7 {
		t.Fatalf("FileCount = %d, want 7 (sub-resources counted)", snapshot.FileCount)
	}
}

func TestValidateAcceptsAgentWithoutSubResources(t *testing.T) {
	// 回归：不带子资源的既有 Agent 资源必须继续通过。
	if _, err := agentartifact.Validate(newSubResourceEntries(), nil); err != nil {
		t.Fatalf("Validate() error = %v, want a plain agent accepted", err)
	}
}

func TestValidateRejectsSubResourceContainerAsFile(t *testing.T) {
	for _, container := range []string{"skills", "plugins", "mcp"} {
		t.Run(container, func(t *testing.T) {
			entries := withFile(newSubResourceEntries(), container, "not a directory")
			_, err := agentartifact.Validate(entries, nil)
			if err == nil || !strings.Contains(err.Error(), "must be a directory") {
				t.Fatalf("Validate() error = %v, want %q to require a directory", err, container)
			}
			if !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

func TestValidateRejectsIncompleteSubResources(t *testing.T) {
	tests := []struct {
		name  string
		build func() []agentartifact.Entry
		want  string
	}{
		{
			name: "skill without SKILL.md",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "skills/probe")
				return withFile(entries, "skills/probe/skill-card.md", subSkillCard)
			},
			want: "SKILL.md",
		},
		{
			name: "skill without skill-card.md",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "skills/probe")
				return withFile(entries, "skills/probe/SKILL.md", skillMD("probe"))
			},
			want: "skill-card.md",
		},
		{
			name: "skill with invalid frontmatter",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "skills/probe")
				entries = withFile(entries, "skills/probe/SKILL.md", "no frontmatter\n")
				return withFile(entries, "skills/probe/skill-card.md", subSkillCard)
			},
			want: "frontmatter",
		},
		{
			name: "plugin without manifest",
			build: func() []agentartifact.Entry {
				return addSkill(withDir(newSubResourceEntries(), "plugins/probe"), "plugins/probe/skills/probe", "probe")
			},
			want: "apc-plugin.json",
		},
		{
			name: "plugin declaring a missing skill",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "plugins/probe")
				entries = withDir(entries, "plugins/probe/skills")
				return withFile(entries, "plugins/probe/apc-plugin.json", pluginJSON("probe-plugin"))
			},
			want: "skills/probe",
		},
		{
			name: "plugin with undeclared skill directory",
			build: func() []agentartifact.Entry {
				entries := newSubResourceEntries()
				entries = addPlugin(entries, "plugins/probe", "probe-plugin")
				return addSkill(entries, "plugins/probe/skills/extra", "extra")
			},
			want: "outside canonical schema",
		},
		{
			name: "mcp with invalid json",
			build: func() []agentartifact.Entry {
				return withFile(withDir(newSubResourceEntries(), "mcp"), "mcp/bad.json", "{")
			},
			want: "JSON",
		},
		{
			name: "mcp without json extension",
			build: func() []agentartifact.Entry {
				return withFile(withDir(newSubResourceEntries(), "mcp"), "mcp/probe", mcpJSON("probe-mcp"))
			},
			want: ".json",
		},
		{
			name: "mcp as directory",
			build: func() []agentartifact.Entry {
				return withDir(withDir(newSubResourceEntries(), "mcp"), "mcp/probe.json")
			},
			want: "must be a regular file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agentartifact.Validate(test.build(), nil)
			if err == nil {
				t.Fatalf("Validate() error = nil, want a rejection naming %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want a message naming %q", err, test.want)
			}
			if !errors.Is(err, domain.ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}

func TestValidateRejectsDuplicateSubResourceIdentities(t *testing.T) {
	tests := []struct {
		name  string
		build func() []agentartifact.Entry
		want  string
	}{
		{
			name: "two skills with the same frontmatter name",
			build: func() []agentartifact.Entry {
				entries := newSubResourceEntries()
				entries = addSkill(entries, "skills/first", "shared")
				return addSkill(entries, "skills/second", "shared")
			},
			want: "share frontmatter name",
		},
		{
			name: "two plugins with the same manifest name",
			build: func() []agentartifact.Entry {
				entries := newSubResourceEntries()
				entries = addPlugin(entries, "plugins/first", "shared-plugin")
				return addPlugin(entries, "plugins/second", "shared-plugin")
			},
			want: "share name",
		},
		{
			name: "two MCPs with the same name",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "mcp")
				entries = withFile(entries, "mcp/first.json", mcpJSON("shared-mcp"))
				return withFile(entries, "mcp/second.json", mcpJSON("shared-mcp"))
			},
			want: "share name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agentartifact.Validate(test.build(), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want a message naming %q", err, test.want)
			}
		})
	}
}

func TestValidateAllowsSameNameAcrossSubResourceTypes(t *testing.T) {
	// 跨类型同名不冲突：目录布局把它们放在互不重叠的根目录下。
	entries := newSubResourceEntries()
	entries = addSkill(entries, "skills/shared", "shared")
	entries = withDir(entries, "mcp")
	entries = withFile(entries, "mcp/shared.json", mcpJSON("shared"))

	if _, err := agentartifact.Validate(entries, nil); err != nil {
		t.Fatalf("Validate() error = %v, want the same name across types allowed", err)
	}
}

func TestValidateKeepsSafetyRulesForSubResources(t *testing.T) {
	// 子资源不得成为绕过既有安全规则的通道。
	tests := []struct {
		name  string
		build func() []agentartifact.Entry
		want  string
	}{
		{
			name: "executable file inside a skill",
			build: func() []agentartifact.Entry {
				entries := addSkill(newSubResourceEntries(), "skills/probe", "probe")
				return append(entries, agentartifact.Entry{
					Path: "skills/probe/run.sh", Mode: 0o755, Data: []byte("#!/bin/sh\n"),
				})
			},
			want: "is executable",
		},
		{
			name: "nested archive inside a skill",
			build: func() []agentartifact.Entry {
				entries := addSkill(newSubResourceEntries(), "skills/probe", "probe")
				return withFile(entries, "skills/probe/bundle.tar.gz", "PK\x03\x04")
			},
			want: "nested archive",
		},
		{
			name: "non-canonical path inside mcp",
			build: func() []agentartifact.Entry {
				entries := withDir(newSubResourceEntries(), "mcp")
				return withFile(entries, "mcp/./probe.json", mcpJSON("probe-mcp"))
			},
			want: "is not canonical",
		},
		{
			name: "hidden sub-resource name",
			build: func() []agentartifact.Entry {
				return addSkill(newSubResourceEntries(), "skills/.hidden", "probe")
			},
			want: "invalid name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agentartifact.Validate(test.build(), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want a message naming %q", err, test.want)
			}
		})
	}
}

func TestValidateSubResourcesWithPowerRef(t *testing.T) {
	// 身份比较仍只针对 agent.yaml；子资源不影响 PowerRef 校验。
	entries := addSkill(newSubResourceEntries(), "skills/probe", "probe")
	ref := probeRef()
	if _, err := agentartifact.Validate(entries, &ref); err != nil {
		t.Fatalf("Validate(ref) error = %v, want accepted", err)
	}

	wrong := domain.PowerRef{Type: domain.PowerTypeExpert, Name: "other-agent", Version: "v1.0.0"}
	if _, err := agentartifact.Validate(entries, &wrong); err == nil {
		t.Fatal("Validate(wrong ref) error = nil, want an identity mismatch")
	}
}

func TestValidateSubResourcesWithoutDirectoryEntries(t *testing.T) {
	// 外部归档可能只带文件条目、不带显式目录条目；子资源仍必须被发现并校验，
	// 不能因为缺目录条目就被静默跳过。
	entries := []agentartifact.Entry{
		{Path: "agent.yaml", Mode: 0o644, Data: []byte(subResourceAgentYAML)},
		{Path: "skills/probe/SKILL.md", Mode: 0o644, Data: []byte(skillMD("probe"))},
		{Path: "skills/probe/skill-card.md", Mode: 0o644, Data: []byte(subSkillCard)},
	}
	if _, err := agentartifact.Validate(entries, nil); err != nil {
		t.Fatalf("Validate() error = %v, want implicit-directory sub-resource accepted", err)
	}

	// 同样的形态但缺 skill-card.md：必须被拒，证明发现逻辑确实生效。
	incomplete := []agentartifact.Entry{
		{Path: "agent.yaml", Mode: 0o644, Data: []byte(subResourceAgentYAML)},
		{Path: "skills/probe/SKILL.md", Mode: 0o644, Data: []byte(skillMD("probe"))},
	}
	if _, err := agentartifact.Validate(incomplete, nil); err == nil || !strings.Contains(err.Error(), "skill-card.md") {
		t.Fatalf("Validate() error = %v, want the incomplete sub-resource rejected", err)
	}
}

func TestValidateRejectsSubResourceContainerFileWithoutDirectoryEntry(t *testing.T) {
	// skills 位置放一个普通文件（无目录条目）：不得被当作子资源容器。
	entries := []agentartifact.Entry{
		{Path: "agent.yaml", Mode: 0o644, Data: []byte(subResourceAgentYAML)},
		{Path: "skills/probe", Mode: 0o644, Data: []byte("not a directory")},
	}
	if _, err := agentartifact.Validate(entries, nil); err == nil || !strings.Contains(err.Error(), "must be a directory") {
		t.Fatalf("Validate() error = %v, want the file-shaped container rejected", err)
	}
}

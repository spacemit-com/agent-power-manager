package submission

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

// TestSubmitRejectsV1Resources 锁定提交侧版本门禁：四类 v1 资源在 validate 与 submit
// 两侧都必须被拒绝，并给出“升级到 2”的可操作错误（ADR-038）。
func TestSubmitRejectsV1Resources(t *testing.T) {
	skillDir := t.TempDir()
	writeTestFile(t, filepath.Join(skillDir, "SKILL.md"), validSkillMarkdownV1)
	writeTestFile(t, filepath.Join(skillDir, "skill-card.md"), validSkillCardMarkdown)

	pluginDir := t.TempDir()
	writeTestFile(t, filepath.Join(pluginDir, "apc-plugin.json"), validPluginManifestV1)
	writeTestFile(t, filepath.Join(pluginDir, "skills", "reviewer", "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(pluginDir, "skills", "reviewer", "skill-card.md"), validSkillCardMarkdown)

	mcpPath := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, mcpPath, validMCPResourceV1)

	agentDir := t.TempDir()
	writeTestFile(t, filepath.Join(agentDir, "agent.yaml"), validAgentYAMLV1)

	for _, test := range []struct {
		name      string
		powerType domain.PowerType
		path      string
	}{
		{name: "skill", powerType: domain.PowerTypeSkill, path: skillDir},
		{name: "plugin", powerType: domain.PowerTypePlugin, path: pluginDir},
		{name: "mcp", powerType: domain.PowerTypeMCP, path: mcpPath},
		{name: "agent", powerType: domain.PowerTypeAgent, path: agentDir},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Validate(context.Background(), Request{Type: test.powerType, Path: test.path}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate(v1) error = %v, want ErrInvalid", err)
			} else if !strings.Contains(err.Error(), "upgrade to 2") {
				t.Fatalf("Validate(v1) error = %v, want an upgrade-to-2 hint", err)
			}
			if _, err := Prepare(context.Background(), Request{Type: test.powerType, Path: test.path, Message: "v1"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Prepare(v1) error = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestSubmitRequiresLocalizedDisplayFields 锁定提交侧必填展示字段：
// 缺失或留空的 name_zh/description_zh 必须被拒绝，且错误必须点名具体字段。
func TestSubmitRequiresLocalizedDisplayFields(t *testing.T) {
	card := validSkillCardMarkdown
	skillFrontmatter := func(fields string) string {
		return "---\nschema_version: 2\n" + fields + "version: \"v1.0.0\"\n---\n# Demo Skill\n"
	}
	for _, test := range []struct {
		name    string
		fields  string
		wantHas string
	}{
		{
			name:    "missing all display fields",
			fields:  "name: demo\ndescription: does things\n",
			wantHas: "name_zh",
		},
		{
			name:    "missing name_en",
			fields:  "name: demo\nname_zh: 演示\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "name_en",
		},
		{
			name:    "missing description_zh",
			fields:  "name: demo\nname_zh: 演示\nname_en: Demo\ndescription: does things\n",
			wantHas: "description_zh",
		},
		{
			name:    "blank name_zh",
			fields:  "name: demo\nname_zh: \"  \"\nname_en: Demo\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "name_zh",
		},
		{
			name:    "name_zh too short",
			fields:  "name: demo\nname_zh: 演\nname_en: Demo\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "2-16",
		},
		{
			name:    "name_zh too long",
			fields:  "name: demo\nname_zh: 一二三四五六七八九十一二三四五六七\nname_en: Demo\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "2-16",
		},
		{
			name:    "name_en too long",
			fields:  "name: demo\nname_zh: 演示\nname_en: " + strings.Repeat("a", 33) + "\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "name_en",
		},
		{
			name:    "name_en not english",
			fields:  "name: demo\nname_zh: 演示\nname_en: 演示\ndescription: does things\ndescription_zh: 演示描述\n",
			wantHas: "name_en",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "SKILL.md"), skillFrontmatter(test.fields))
			writeTestFile(t, filepath.Join(dir, "skill-card.md"), card)
			_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), test.wantHas) {
				t.Fatalf("Validate() error = %v, want a message naming %q", err, test.wantHas)
			}
		})
	}

	t.Run("mcp missing display fields", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		writeTestFile(t, path, `{"schema_version":2,"name":"demo","version":"v1.0.0","description":"d","definition":{"transport":"stdio","command":"npx"}}`)
		_, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: path})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "name_zh") {
			t.Fatalf("Validate() error = %v, want ErrInvalid naming name_zh", err)
		}
	})

	t.Run("plugin missing display fields", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "apc-plugin.json"), `{"schema_version":2,"name":"code-review","version":"v1.0.0","description":"code review helpers","skills":["skills/reviewer"]}`)
		writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "SKILL.md"), validSkillMarkdown)
		writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "skill-card.md"), validSkillCardMarkdown)
		_, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "name_zh") {
			t.Fatalf("Validate() error = %v, want ErrInvalid naming name_zh", err)
		}
	})

	t.Run("agent missing display fields", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "agent.yaml"), "schema_version: 2\nagent:\n  id: demo-agent\n  name: demo-agent\n  version: v1.0.0\n  description: demo\n  soul: help safely\n")
		_, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: dir})
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "name_zh") {
			t.Fatalf("Validate() error = %v, want ErrInvalid naming name_zh", err)
		}
	})
}

// TestSubmitAcceptsV2ResourcesWithDisplayFields 是拒绝门禁的正向对照：
// 补齐 v2 与展示字段后，四类资源必须通过离线校验。
func TestSubmitAcceptsV2ResourcesWithDisplayFields(t *testing.T) {
	skillDir := t.TempDir()
	writeValidSkill(t, skillDir)
	pluginDir := t.TempDir()
	writeValidPlugin(t, pluginDir)
	mcpPath := filepath.Join(t.TempDir(), "mcp.json")
	writeTestFile(t, mcpPath, validMCPResource)
	agentDir := t.TempDir()
	writeValidAgentDir(t, agentDir)

	for _, test := range []struct {
		name      string
		powerType domain.PowerType
		path      string
	}{
		{name: "skill", powerType: domain.PowerTypeSkill, path: skillDir},
		{name: "plugin", powerType: domain.PowerTypePlugin, path: pluginDir},
		{name: "mcp", powerType: domain.PowerTypeMCP, path: mcpPath},
		{name: "agent", powerType: domain.PowerTypeAgent, path: agentDir},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Validate(context.Background(), Request{Type: test.powerType, Path: test.path}); err != nil {
				t.Fatalf("Validate(v2) error = %v, want nil", err)
			}
		})
	}
}

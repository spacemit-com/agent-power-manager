package submission

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

const validPluginManifest = `{"schema_version":2,"name":"code-review","name_zh":"代码评审","name_en":"Code Review","version":"v1.0.0","description":"code review helpers","description_zh":"代码评审辅助","skills":["skills/reviewer"]}`

// validPluginManifestV1 是历史安装侧 v1 apc-plugin.json；提交侧必须拒绝，用于锁定版本门禁。
const validPluginManifestV1 = `{"schema_version":1,"name":"code-review","version":"v1.0.0","description":"code review helpers","skills":["skills/reviewer"]}`

func writeValidPlugin(t *testing.T, dir string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, "apc-plugin.json"), validPluginManifest)
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "skill-card.md"), validSkillCardMarkdown)
}

func TestValidatePlugin(t *testing.T) {
	dir := t.TempDir()
	writeValidPlugin(t, dir)
	got, err := Validate(context.Background(), Request{
		Type: domain.PowerTypePlugin,
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Type != domain.PowerTypePlugin || got.Name != "code-review" || got.Version != "v1.0.0" || got.Description != "code review helpers" {
		t.Fatalf("Validate() = %#v", got)
	}
	if got.FileCount != 3 {
		t.Fatalf("FileCount = %d, want 3", got.FileCount)
	}
}

func TestValidatePluginDoesNotRequireMessage(t *testing.T) {
	dir := t.TempDir()
	writeValidPlugin(t, dir)
	if _, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidatePluginRejectsMissingManifest(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidatePluginRejectsBadEmbeddedSkill(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "apc-plugin.json"), validPluginManifest)
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "SKILL.md"), validSkillMarkdown)
	// 缺少 skill-card.md。
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidatePluginRejectsPathsOutsideRawSchema(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		content  string
	}{
		{name: "extra root payload", relative: "README.md", content: "unexpected\n"},
		{name: "native agent manifest", relative: ".codex-plugin/plugin.json", content: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeValidPlugin(t, dir)
			writeTestFile(t, filepath.Join(dir, filepath.FromSlash(tt.relative)), tt.content)

			_, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidatePluginRejectsDuplicateEmbeddedSkillNames(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "apc-plugin.json"), `{"schema_version":2,"name":"code-review","name_zh":"代码评审","name_en":"Code Review","version":"v1.0.0","description":"code review helpers","description_zh":"代码评审辅助","skills":["skills/reviewer","skills/writer"]}`)
	for _, relative := range []string{"skills/reviewer", "skills/writer"} {
		writeTestFile(t, filepath.Join(dir, relative, "SKILL.md"), validSkillMarkdown)
		writeTestFile(t, filepath.Join(dir, relative, "skill-card.md"), validSkillCardMarkdown)
	}

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

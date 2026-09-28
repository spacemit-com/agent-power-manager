package submission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/domain"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validSkillMarkdown = `---
schema_version: 2
name: demo
name_zh: 演示
name_en: Demo
description: does things
description_zh: 做一些事情
version: "v1.0.0"
---
# Demo Skill

Details here.
`

// validSkillMarkdownV1 是历史 v1 SKILL.md；提交侧必须拒绝，用于锁定版本门禁。
const validSkillMarkdownV1 = `---
name: demo
version: "v1.0.0"
description: does things
---
# Demo Skill

Details here.
`

const validSkillCardMarkdown = `---
schema_version: 2
---
# Skill Card: Demo

## Capability
does things

## Use Cases
when you need it

## Inputs and Outputs
text in, text out

## Dependencies
none

## Limitations
none

## Maintainer
Owner: platform
Contact: platform@example.com
`

func writeValidSkill(t *testing.T, dir string) {
	t.Helper()
	writeSkillFixture(t, dir, "demo", "v1.0.0", "does things")
}

func writeSkillFixture(t *testing.T, dir, name, version, description string) {
	t.Helper()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nschema_version: 2\nname: "+name+"\nname_zh: 演示\nname_en: Demo\ndescription: "+description+"\ndescription_zh: 演示描述\nversion: \""+version+"\"\n---\n# Demo Skill\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
}

func TestValidateSkillReadsVersionFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "demo", "v1.2.3", "does things")
	got, err := Validate(context.Background(), Request{
		Type: domain.PowerTypeSkill,
		Path: dir,
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Type != domain.PowerTypeSkill || got.Name != "demo" || got.Version != "v1.2.3" || got.Description != "does things" {
		t.Fatalf("Validate() = %#v", got)
	}
	if got.FileCount != 2 {
		t.Fatalf("FileCount = %d, want 2", got.FileCount)
	}
	if got.ContentSize == 0 {
		t.Fatalf("ContentSize = 0, want non-zero")
	}
}

func TestValidateSkillRejectsMissingVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: demo\ndescription: does things\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsNonCanonicalVersion(t *testing.T) {
	dir := t.TempDir()
	writeSkillFixture(t, dir, "demo", "1.0.0", "does things")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillDoesNotRequireMessage(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	if _, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateSkillRequiresSkillMD(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRequiresSkillCard(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsMissingSkillCardSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: platform\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillCardSchemaVersionMustBeTwo(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: 3\n---\n# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: platform\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsNonIntegerSkillCardSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: \"2\"\n---\n# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: platform\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillAcceptsExtraSkillCardFrontmatterKey(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: 2\nfuture: true\n---\n# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: platform\nContact: team@example.com\n")
	if _, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir}); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateSkillRejectsFrontmatterNotAtStart(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "# Title\n---\nname: demo\ndescription: x\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsFrontmatterMarkerWithInlineContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---name: demo\nschema_version: 2\nversion: \"v1.0.0\"\ndescription: does things\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)

	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillAcceptsFrontmatterMarkerWhitespaceAndLineEndings(t *testing.T) {
	for _, opener := range []string{"---\n", "--- \t\n", "---\r\n", "--- \t\r\n"} {
		t.Run(strings.ReplaceAll(fmt.Sprintf("%q", opener), "\\", "_"), func(t *testing.T) {
			dir := t.TempDir()
			writeTestFile(t, filepath.Join(dir, "SKILL.md"), opener+"schema_version: 2\nname: demo\nname_zh: 演示\nname_en: Demo\nversion: \"v1.0.0\"\ndescription: does things\ndescription_zh: 演示\n---\n")
			writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
			if _, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir}); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestValidateSkillRejectsDuplicateFrontmatterKey(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nschema_version: 2\nname: a\nname: b\nname_zh: 演示\nversion: \"v1.0.0\"\ndescription: x\ndescription_zh: 演示\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsNonStringName(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: 123\nversion: \"v1.0.0\"\ndescription: x\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: demo\nversion: \"v1.0.0\"\ndescription: \"\"\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsMissingSection(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: 1\n---\n# Skill Card: Demo\n\n## Capability\nx\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: 1\n---\n# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: REPLACE_OWNER\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsMissingOwner(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "---\nschema_version: 1\n---\n# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

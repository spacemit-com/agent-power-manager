package submission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
name: demo
description: does things
---
# Demo Skill

Details here.
`

const validSkillCardMarkdown = `# Skill Card: Demo

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
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
}

func TestValidateSkill(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	got, err := Validate(context.Background(), Request{
		Type:          domain.PowerTypeSkill,
		Path:          dir,
		Version:       "v1.0.0",
		ChangeSummary: "first release",
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Type != domain.PowerTypeSkill || got.Name != "demo" || got.Version != "v1.0.0" || got.Description != "does things" {
		t.Fatalf("Validate() = %#v", got)
	}
	if got.FileCount != 2 {
		t.Fatalf("FileCount = %d, want 2", got.FileCount)
	}
	if got.ChangeSummary != "first release" {
		t.Fatalf("ChangeSummary = %q", got.ChangeSummary)
	}
	if got.ContentSize == 0 {
		t.Fatalf("ContentSize = 0, want non-zero")
	}
}

func TestValidateSkillAllowsMissingVersion(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	got, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got.Version != "" {
		t.Fatalf("Version = %q, want empty", got.Version)
	}
}

func TestValidateSkillRejectsNonCanonicalVersion(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillAllowsMissingChangeSummary(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	if _, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0"}); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateSkillRequiresSkillMD(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRequiresSkillCard(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsFrontmatterNotAtStart(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "# Title\n---\nname: demo\ndescription: x\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsDuplicateFrontmatterKey(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: a\nname: b\ndescription: x\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsNonStringName(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: 123\ndescription: x\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsEmptyDescription(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: demo\ndescription: \"\"\n---\n")
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown)
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsMissingSection(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "# Skill Card: Demo\n\n## Capability\nx\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nOwner: REPLACE_OWNER\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

func TestValidateSkillRejectsMissingOwner(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "SKILL.md"), validSkillMarkdown)
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), "# Skill Card: Demo\n\n## Capability\nx\n\n## Use Cases\nx\n\n## Inputs and Outputs\nx\n\n## Dependencies\nx\n\n## Limitations\nx\n\n## Maintainer\nContact: team@example.com\n")
	_, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir, Version: "v1.0.0", ChangeSummary: "x"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
}

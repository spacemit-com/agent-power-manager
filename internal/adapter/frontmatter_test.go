package adapter

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSkillFrontmatterAcceptsSupportedDocuments(t *testing.T) {
	for _, tt := range []struct {
		name     string
		contents string
	}{
		{
			name:     "LF with unknown fields and block description",
			contents: "---\nname: demo\ndescription: |\n  first line\n  second line\nmetadata:\n  owner: apm\n---\n# Body\n",
		},
		{
			name:     "CRLF with empty body",
			contents: "---\r\nname: demo\r\ndescription: valid\r\n---\r\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSkillFile(t, tt.contents)

			if err := validateSkillFrontmatter(path, "demo"); err != nil {
				t.Fatalf("validateSkillFrontmatter() error = %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.contents {
				t.Fatalf("validator rewrote file = %q, want %q", got, tt.contents)
			}
		})
	}
}

func TestValidateSkillFrontmatterRejectsInvalidDocuments(t *testing.T) {
	for _, tt := range []struct {
		name     string
		contents string
	}{
		{"missing opening delimiter", "name: demo\ndescription: valid\n---\n"},
		{"leading bytes", "\n---\nname: demo\ndescription: valid\n---\n"},
		{"missing closing delimiter", "---\nname: demo\ndescription: valid\n"},
		{"different delimiter newline", "---\nname: demo\ndescription: valid\n---\r\n"},
		{"malformed YAML", "---\nname: [\ndescription: valid\n---\n"},
		{"sequence root", "---\n- name: demo\n- description: valid\n---\n"},
		{"scalar root", "---\ndemo\n---\n"},
		{"duplicate name", "---\nname: demo\nname: demo\ndescription: valid\n---\n"},
		{"duplicate description", "---\nname: demo\ndescription: valid\ndescription: other\n---\n"},
		{"missing name", "---\ndescription: valid\n---\n"},
		{"missing description", "---\nname: demo\n---\n"},
		{"numeric fields", "---\nname: 42\ndescription: 7\n---\n"},
		{"boolean fields", "---\nname: true\ndescription: false\n---\n"},
		{"null fields", "---\nname: null\ndescription: null\n---\n"},
		{"whitespace name", "---\nname: ' \t'\ndescription: valid\n---\n"},
		{"whitespace description", "---\nname: demo\ndescription: ' \t'\n---\n"},
		{"raw name mismatch", "---\nname: Demo\ndescription: valid\n---\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSkillFrontmatter(writeSkillFile(t, tt.contents), "demo")
			if !errors.Is(err, ErrInvalidSkill) {
				t.Fatalf("validateSkillFrontmatter() error = %v, want ErrInvalidSkill", err)
			}
		})
	}
}

func writeSkillFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

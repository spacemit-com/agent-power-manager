package lifecycle

import (
	"os"
	"path/filepath"
	"testing"
)

const lifecycleSkillMarkdown = "---\nname: demo\ndescription: does things\n---\n# Demo Skill\n\nDetails here.\n"

const lifecycleSkillCard = "# Skill Card: Demo\n\n## Capability\ndoes things\n\n## Use Cases\nwhen needed\n\n## Inputs and Outputs\ntext\n\n## Dependencies\nnone\n\n## Limitations\nnone\n\n## Maintainer\nOwner: platform\nContact: platform@example.com\n"

func writeLifecycleSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(lifecycleSkillMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill-card.md"), []byte(lifecycleSkillCard), 0o644); err != nil {
		t.Fatal(err)
	}
}

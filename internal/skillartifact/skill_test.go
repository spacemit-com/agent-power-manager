package skillartifact_test

import (
	"errors"
	"strings"
	"testing"

	"apm/internal/skillartifact"
)

const validSkillMD = `---
schema_version: 2
name: demo-skill
version: v1.0.0
description: Demonstrates the skill format.
name_zh: 演示技能
name_en: Demo Skill
description_zh: 演示技能格式。
---

# Demo Skill

Body text.
`

const validCard = `---
schema_version: 2
---
# Skill Card: Demo Skill

## Capability
Does a thing.

## Use Cases
Testing.

## Inputs and Outputs
None.

## Dependencies
None.

## Limitations
None.

## Maintainer
Owner: APM Team
Contact: apm@example.com
`

func files(overrides map[string]string) func(string) ([]byte, error) {
	base := map[string]string{"SKILL.md": validSkillMD, "skill-card.md": validCard}
	for name, body := range overrides {
		base[name] = body
	}
	return func(relative string) ([]byte, error) {
		body, ok := base[relative]
		if !ok {
			return nil, errors.New("missing " + relative)
		}
		return []byte(body), nil
	}
}

func TestValidateContentAcceptsCanonicalSkill(t *testing.T) {
	metadata, err := skillartifact.ValidateContent("", files(nil))
	if err != nil {
		t.Fatalf("ValidateContent() error = %v", err)
	}
	if metadata.Name != "demo-skill" || metadata.Version != "v1.0.0" || metadata.SchemaVersion != 2 {
		t.Fatalf("metadata = %#v, want the canonical identity", metadata)
	}
	if metadata.NameZH != "演示技能" || metadata.NameEN != "Demo Skill" || metadata.DescriptionZH != "演示技能格式。" {
		t.Fatalf("metadata display fields = %#v, want the localized values", metadata)
	}
}

func TestValidateContentReadsFilesRelativeToRoot(t *testing.T) {
	// 子资源场景以 "skills/<name>" 为根，读取路径必须带上前缀。
	prefix := func(reader func(string) ([]byte, error)) func(string) ([]byte, error) {
		return func(relative string) ([]byte, error) {
			return reader(strings.TrimPrefix(relative, "skills/demo/"))
		}
	}
	if _, err := skillartifact.ValidateContent("skills/demo", prefix(files(nil))); err != nil {
		t.Fatalf("ValidateContent(root) error = %v", err)
	}
}

func TestValidateContentRejectsMissingMarkers(t *testing.T) {
	for _, missing := range []string{"SKILL.md", "skill-card.md"} {
		t.Run("missing "+missing, func(t *testing.T) {
			reader := files(nil)
			_, err := skillartifact.ValidateContent("", func(relative string) ([]byte, error) {
				if relative == missing {
					return nil, errors.New("required file is missing")
				}
				return reader(relative)
			})
			if err == nil {
				t.Fatalf("ValidateContent() error = nil, want a missing-marker failure for %s", missing)
			}
		})
	}
}

func TestParseMetadataRejectsInvalidFrontmatter(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing schema_version",
			body: "---\nname: demo\nversion: v1.0.0\ndescription: d\nname_zh: 演示\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "schema_version",
		},
		{
			name: "non-integer schema_version",
			body: "---\nschema_version: two\nname: demo\nversion: v1.0.0\ndescription: d\nname_zh: 演示\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "schema_version",
		},
		{
			name: "schema_version 1 is not submittable",
			body: "---\nschema_version: 1\nname: demo\nversion: v1.0.0\ndescription: d\nname_zh: 演示\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "not submittable",
		},
		{
			name: "duplicate key",
			body: "---\nschema_version: 2\nname: demo\nname: other\nversion: v1.0.0\ndescription: d\nname_zh: 演示\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "duplicate key",
		},
		{
			name: "missing name_zh",
			body: "---\nschema_version: 2\nname: demo\nversion: v1.0.0\ndescription: d\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "name_zh",
		},
		{
			name: "non-canonical version",
			body: "---\nschema_version: 2\nname: demo\nversion: 1.0.0\ndescription: d\nname_zh: 演示\nname_en: Demo\ndescription_zh: 描述\n---\n",
			want: "version",
		},
		{
			name: "missing frontmatter opener",
			body: "name: demo\n",
			want: "must start with YAML frontmatter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := skillartifact.ParseMetadata([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseMetadata() error = %v, want a message naming %q", err, test.want)
			}
			if !errors.Is(err, skillartifact.ErrInvalid) {
				t.Fatalf("ParseMetadata() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateCardRejectsIncompleteCards(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "wrong schema_version",
			body: strings.Replace(validCard, "schema_version: 2", "schema_version: 1", 1),
			want: "requires integer schema_version 2",
		},
		{
			name: "missing title",
			body: strings.Replace(validCard, "# Skill Card: Demo Skill", "Demo Skill", 1),
			want: "# Skill Card:",
		},
		{
			name: "missing section",
			body: strings.Replace(validCard, "## Limitations\nNone.\n", "", 1),
			want: "Limitations",
		},
		{
			name: "placeholder maintainer",
			body: strings.Replace(validCard, "Owner: APM Team", "Owner: REPLACE_OWNER", 1),
			want: "placeholder",
		},
		{
			name: "maintainer without contact",
			body: strings.Replace(validCard, "Contact: apm@example.com", "", 1),
			want: "Contact",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := skillartifact.ValidateCard([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateCard() error = %v, want a message naming %q", err, test.want)
			}
			if !errors.Is(err, skillartifact.ErrInvalid) {
				t.Fatalf("ValidateCard() error = %v, want ErrInvalid", err)
			}
		})
	}
}

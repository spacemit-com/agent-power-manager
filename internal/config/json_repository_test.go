package config_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/paths"
)

func manifestPath(t *testing.T, agent domain.AgentID) string {
	t.Helper()
	path, err := paths.ManifestPath(agent)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeManifest(t *testing.T, agent domain.AgentID, content string) string {
	t.Helper()
	path := manifestPath(t, agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertEmptyGroups(t *testing.T, groups domain.PowerGroups) {
	t.Helper()
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP,
	} {
		items, ok := groups[powerType]
		if !ok || items == nil || len(items) != 0 {
			t.Fatalf("group %q = %#v, want present non-nil empty slice", powerType, items)
		}
	}
}

func TestJSONRepositoryListMissingManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	groups, err := config.NewJSONRepository().List("codex")
	if err != nil {
		t.Fatal(err)
	}
	assertEmptyGroups(t, groups)

	if _, err := os.Stat(filepath.Join(home, ".apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only List created state path: %v", err)
	}
}

func TestJSONRepositoryListConvertsGroupsAndReturnsIndependentCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, "codex", `{
		"agent": "codex",
		"unknown_top_level": true,
		"skills": [{"name": "skill-a", "version": "1.2.3", "unknown_entry": "ignored"}],
		"plugins": [{"name": "plugin-a", "version": "2.3.4", "unknown_entry": "ignored"}],
		"mcps": [{"name": "mcp-a", "version": "3.4.5", "unknown_entry": "ignored"}]
	}`)

	repository := config.NewJSONRepository()
	groups, err := repository.List("codex")
	if err != nil {
		t.Fatal(err)
	}
	for powerType, want := range map[domain.PowerType]domain.PowerRef{
		domain.PowerTypeSkill:  {Name: "skill-a", Type: domain.PowerTypeSkill, Version: "1.2.3"},
		domain.PowerTypePlugin: {Name: "plugin-a", Type: domain.PowerTypePlugin, Version: "2.3.4"},
		domain.PowerTypeMCP:    {Name: "mcp-a", Type: domain.PowerTypeMCP, Version: "3.4.5"},
	} {
		if got := groups[powerType]; len(got) != 1 || got[0] != want {
			t.Fatalf("group %q = %#v, want %#v", powerType, got, []domain.PowerRef{want})
		}
	}

	groups[domain.PowerTypeSkill][0].Name = "changed"
	groups[domain.PowerTypeSkill] = append(groups[domain.PowerTypeSkill], domain.PowerRef{Name: "new"})
	groups[domain.PowerTypePlugin] = nil

	again, err := repository.List("codex")
	if err != nil {
		t.Fatal(err)
	}
	if got := again[domain.PowerTypeSkill]; len(got) != 1 || got[0].Name != "skill-a" {
		t.Fatalf("disk-backed skills after caller mutation = %#v, want original skill", got)
	}
	if got := again[domain.PowerTypePlugin]; len(got) != 1 || got[0].Name != "plugin-a" {
		t.Fatalf("disk-backed plugins after caller mutation = %#v, want original plugin", got)
	}
}

func TestJSONRepositoryListNormalizesManifestGroups(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		wantErr bool
		wantLen int
	}{
		{name: "missing groups", content: `{"agent":"codex"}`, wantLen: 0},
		{name: "null groups", content: `{"agent":"codex","skills":null,"plugins":null,"mcps":null}`, wantLen: 0},
		{name: "invalid JSON", content: `{`, wantErr: true},
		{name: "trailing JSON", content: `{"agent":"codex"}{}`, wantErr: true},
		{name: "wrong group type", content: `{"agent":"codex","skills":{}}`, wantErr: true},
		{name: "missing agent", content: `{"skills":[]}`, wantErr: true},
		{name: "mismatched agent", content: `{"agent":"claude","skills":[]}`, wantErr: true},
		{name: "unchecked content", content: `{"agent":"codex","skills":[{"name":"","version":""},{"name":"","version":""}]}`, wantLen: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("APM_HOME", home)
			writeManifest(t, "codex", tt.content)

			groups, err := config.NewJSONRepository().List("codex")
			if tt.wantErr {
				if err == nil {
					t.Fatal("List() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantLen == 0 {
				assertEmptyGroups(t, groups)
				return
			}
			if got := groups[domain.PowerTypeSkill]; len(got) != tt.wantLen {
				t.Fatalf("skills = %#v, want %d entries", got, tt.wantLen)
			}
			for _, power := range groups[domain.PowerTypeSkill] {
				if power.Type != domain.PowerTypeSkill || power.Name != "" || power.Version != "" {
					t.Fatalf("unchecked skill = %#v, want empty skill reference", power)
				}
			}
		})
	}
}

func TestJSONRepositoryFindReturnsFirstMatchingPower(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, "codex", `{"agent":"codex","skills":[{"name":"duplicate","version":"first"},{"name":"duplicate","version":"second"}]}`)

	power, found, err := config.NewJSONRepository().Find("codex", domain.PowerTypeSkill, "duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Find() found = false, want true")
	}
	want := domain.PowerRef{Name: "duplicate", Type: domain.PowerTypeSkill, Version: "first"}
	if power != want {
		t.Fatalf("Find() power = %#v, want %#v", power, want)
	}
}

func TestJSONRepositoryFindMissingNameReturnsNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, "codex", `{"agent":"codex","skills":[{"name":"present","version":"1"}]}`)

	power, found, err := config.NewJSONRepository().Find("codex", domain.PowerTypeSkill, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if found || power != (domain.PowerRef{}) {
		t.Fatalf("Find() = (%#v, %t), want (zero, false)", power, found)
	}
}

func TestJSONRepositoryFindRejectsInvalidPowerBeforeIO(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".apm"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name      string
		powerType domain.PowerType
		nameValue string
	}{
		{name: "unknown type", powerType: "unknown", nameValue: "power"},
		{name: "empty name", powerType: domain.PowerTypeSkill, nameValue: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := config.NewJSONRepository().Find("codex", tt.powerType, tt.nameValue)
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("Find() error = %v, want ErrInvalidPower before I/O", err)
			}
		})
	}
}

func TestJSONRepositoryLogsVerboseRead(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := manifestPath(t, "codex")

	var stderr bytes.Buffer
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(logging.NewLogger(io.Discard, &stderr, true))

	if _, err := config.NewJSONRepository().List("codex"); err != nil {
		t.Fatal(err)
	}
	output := stderr.String()
	for _, want := range []string{"level=DEBUG", "agent=codex", path} {
		if !strings.Contains(output, want) {
			t.Fatalf("verbose read log = %q, want to contain %q", output, want)
		}
	}
}

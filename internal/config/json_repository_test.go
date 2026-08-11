package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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

func readManifest(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
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

func TestJSONRepositoryRejectsNonCanonicalManifestVersions(t *testing.T) {
	tests := []string{
		"", "1.2.3", "v1.2", "v1.2.3.4", "v01.2.3",
		"v1.02.3", "v1.2.03", "v1.2.3-alpha", "v1.2.3+build",
		" v1.2.3", "v1.2.3 ", "v1.two.3", "v-1.2.3",
	}
	for _, version := range tests {
		t.Run(fmt.Sprintf("version_%q", version), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("APM_HOME", home)
			writeManifest(t, domain.AgentCodex, fmt.Sprintf(
				`{"agent":"codex","skills":[{"name":"demo","version":%q}]}`,
				version,
			))
			_, err := config.NewJSONRepository().List(domain.AgentCodex)
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("List() error = %v, want ErrInvalidPower", err)
			}
			for _, want := range []string{"List", "codex", "skill", "entry 0", "demo", strconv.Quote(version)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("List() error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestJSONRepositoryFindAndRemoveRejectInvalidUnrelatedManifestEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, domain.AgentCodex, `{"agent":"codex","skills":[{"name":"demo","version":"v1.2.3"}],"mcps":[{"name":"broken","version":"1.2.3"}]}`)
	for _, operation := range []string{"Find", "Remove"} {
		t.Run(operation, func(t *testing.T) {
			var err error
			if operation == "Find" {
				_, _, err = config.NewJSONRepository().Find(domain.AgentCodex, domain.PowerTypeSkill, "demo")
			} else {
				err = config.NewJSONRepository().Remove(domain.AgentCodex, domain.PowerTypeSkill, "demo")
			}
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("%s() error = %v, want ErrInvalidPower", operation, err)
			}
			for _, want := range []string{operation, "codex", "mcp", "entry 0", "broken", `version "1.2.3"`} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("%s() error = %q, want %q", operation, err, want)
				}
			}
		})
	}
}

func TestJSONRepositorySetRejectsNonCanonicalVersionBeforeIO(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".apm"), []byte("blocking"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := config.NewJSONRepository().Set(domain.AgentCodex, domain.PowerRef{
		Type: domain.PowerTypeSkill, Name: "demo", Version: "1.2.3",
	})
	if !errors.Is(err, config.ErrInvalidPower) || !strings.Contains(err.Error(), `version "1.2.3"`) {
		t.Fatalf("Set() error = %v, want version ErrInvalidPower", err)
	}
}

func TestJSONRepositoryCanonicalVersionsRoundTrip(t *testing.T) {
	for _, version := range []string{"v0.0.0", "v1.2.3", "v123.456.789"} {
		t.Run(version, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("APM_HOME", home)
			if err := config.NewJSONRepository().Set(domain.AgentCodex, domain.PowerRef{
				Type: domain.PowerTypeSkill, Name: "demo", Version: version,
			}); err != nil {
				t.Fatal(err)
			}
			groups, err := config.NewJSONRepository().List(domain.AgentCodex)
			if err != nil {
				t.Fatal(err)
			}
			if got := groups[domain.PowerTypeSkill][0].Version; got != version {
				t.Fatalf("round-trip version = %q, want %q", got, version)
			}
		})
	}
}

func TestJSONRepositoryListConvertsGroupsAndReturnsIndependentCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, "codex", `{
		"agent": "codex",
		"unknown_top_level": true,
		"skills": [{"name": "skill-a", "version": "v1.2.3", "unknown_entry": "ignored"}],
		"plugins": [{"name": "plugin-a", "version": "v2.3.4", "unknown_entry": "ignored"}],
		"mcps": [{"name": "mcp-a", "version": "v3.4.5", "unknown_entry": "ignored"}]
	}`)

	repository := config.NewJSONRepository()
	groups, err := repository.List("codex")
	if err != nil {
		t.Fatal(err)
	}
	for powerType, want := range map[domain.PowerType]domain.PowerRef{
		domain.PowerTypeSkill:  {Name: "skill-a", Type: domain.PowerTypeSkill, Version: "v1.2.3"},
		domain.PowerTypePlugin: {Name: "plugin-a", Type: domain.PowerTypePlugin, Version: "v2.3.4"},
		domain.PowerTypeMCP:    {Name: "mcp-a", Type: domain.PowerTypeMCP, Version: "v3.4.5"},
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
		{name: "unchecked content", content: `{"agent":"codex","skills":[{"name":"","version":""},{"name":"","version":""}]}`, wantErr: true},
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
	writeManifest(t, "codex", `{"agent":"codex","skills":[{"name":"duplicate","version":"v1.0.0"},{"name":"duplicate","version":"v2.0.0"}]}`)

	power, found, err := config.NewJSONRepository().Find("codex", domain.PowerTypeSkill, "duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Find() found = false, want true")
	}
	want := domain.PowerRef{Name: "duplicate", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if power != want {
		t.Fatalf("Find() power = %#v, want %#v", power, want)
	}
}

func TestJSONRepositoryFindMissingNameReturnsNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	writeManifest(t, "codex", `{"agent":"codex","skills":[{"name":"present","version":"v1.0.0"}]}`)

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
		wantField string
	}{
		{name: "unknown type", powerType: "unknown", nameValue: "power", wantField: "type"},
		{name: "empty name", powerType: domain.PowerTypeSkill, nameValue: "", wantField: "name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := config.NewJSONRepository().Find("codex", tt.powerType, tt.nameValue)
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("Find() error = %v, want ErrInvalidPower before I/O", err)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("Find() error = %q, want field context %q", err, tt.wantField)
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

func TestJSONRepositorySetCreatesManifestWithFixedSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	err := config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}

	const want = `{
  "agent": "codex",
  "skills": [
    {
      "name": "foo-before-summary",
      "version": "v0.0.1"
    }
  ],
  "plugins": [],
  "mcps": []
}
`
	path := manifestPath(t, "codex")
	if got := readManifest(t, path); got != want {
		t.Fatalf("manifest = %q, want %q", got, want)
	}
}

func TestJSONRepositorySetAppendsNewNameAndPreservesOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := writeManifest(t, "codex", `{
  "agent": "codex",
  "skills": [
    {"name": "first", "version": "v1.0.0"},
    {"name": "", "version": "v9.0.0"}
  ],
  "plugins": [{"name": "plugin-a", "version": "v2.0.0"}],
  "mcps": [{"name": "mcp-a", "version": "v3.0.0"}]
}`)

	err := config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "last", Type: domain.PowerTypeSkill, Version: "v4.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	const want = `{
  "agent": "codex",
  "skills": [
    {
      "name": "first",
      "version": "v1.0.0"
    },
    {
      "name": "",
      "version": "v9.0.0"
    },
    {
      "name": "last",
      "version": "v4.0.0"
    }
  ],
  "plugins": [
    {
      "name": "plugin-a",
      "version": "v2.0.0"
    }
  ],
  "mcps": [
    {
      "name": "mcp-a",
      "version": "v3.0.0"
    }
  ]
}
`
	if got := readManifest(t, path); got != want {
		t.Fatalf("manifest = %q, want %q", got, want)
	}
}

func TestJSONRepositorySetReplacesFirstMatchAndRemovesDuplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := writeManifest(t, "codex", `{
  "agent": "codex",
  "skills": [
    {"name": "before", "version": "v1.0.0"},
    {"name": "duplicate", "version": "v2.0.0"},
    {"name": "", "version": "v9.0.0"},
    {"name": "duplicate", "version": "v3.0.0"},
    {"name": "after", "version": "v4.0.0"}
  ],
  "plugins": [{"name": "plugin-a", "version": "v3.0.0"}],
  "mcps": []
}`)

	err := config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "duplicate", Type: domain.PowerTypeSkill, Version: "v5.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	const want = `{
  "agent": "codex",
  "skills": [
    {
      "name": "before",
      "version": "v1.0.0"
    },
    {
      "name": "duplicate",
      "version": "v5.0.0"
    },
    {
      "name": "",
      "version": "v9.0.0"
    },
    {
      "name": "after",
      "version": "v4.0.0"
    }
  ],
  "plugins": [
    {
      "name": "plugin-a",
      "version": "v3.0.0"
    }
  ],
  "mcps": []
}
`
	if got := readManifest(t, path); got != want {
		t.Fatalf("manifest = %q, want %q", got, want)
	}
}

func TestJSONRepositorySetIdenticalSingleMatchDoesNotReplaceFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := writeManifest(t, "codex", `{
  "agent": "codex",
  "skills": [{"name": "same", "version": "v1.0.0"}],
  "plugins": [],
  "mcps": []
}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(logging.NewLogger(io.Discard, &stderr, true))

	err = config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "same", Type: domain.PowerTypeSkill, Version: "v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("Set() replaced the manifest for an identical single match")
	}
	for _, want := range []string{"agent=codex", "power=same", "version=v1.0.0"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("Set() no-op log = %q, want field %q", stderr.String(), want)
		}
	}
}

func TestJSONRepositorySetRejectsInvalidPowerBeforeIO(t *testing.T) {
	for _, tt := range []struct {
		name      string
		power     domain.PowerRef
		wantField string
	}{
		{name: "unknown type", power: domain.PowerRef{Name: "power", Type: "unknown", Version: "v1.0.0"}, wantField: "type"},
		{name: "empty name", power: domain.PowerRef{Type: domain.PowerTypeSkill, Version: "v1.0.0"}, wantField: "name"},
		{name: "empty version", power: domain.PowerRef{Name: "power", Type: domain.PowerTypeSkill}, wantField: "version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("APM_HOME", home)
			statePath := filepath.Join(home, ".apm")
			if err := os.WriteFile(statePath, []byte("blocking file"), 0o600); err != nil {
				t.Fatal(err)
			}

			err := config.NewJSONRepository().Set("codex", tt.power)
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("Set() error = %v, want ErrInvalidPower before I/O", err)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("Set() error = %q, want field context %q", err, tt.wantField)
			}
			if got := readManifest(t, statePath); got != "blocking file" {
				t.Fatalf("invalid Set() changed blocking state file to %q", got)
			}
		})
	}
}

func TestJSONRepositoryRemoveDeletesAllMatchesAndPreservesOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := writeManifest(t, "codex", `{
  "agent": "codex",
  "skills": [
    {"name": "before", "version": "v1.0.0"},
    {"name": "remove", "version": "v2.0.0"},
    {"name": "", "version": "v9.0.0"},
    {"name": "remove", "version": "v3.0.0"},
    {"name": "after", "version": "v4.0.0"}
  ],
  "plugins": [{"name": "plugin-a", "version": "v3.0.0"}],
  "mcps": []
}`)

	if err := config.NewJSONRepository().Remove("codex", domain.PowerTypeSkill, "remove"); err != nil {
		t.Fatal(err)
	}

	const want = `{
  "agent": "codex",
  "skills": [
    {
      "name": "before",
      "version": "v1.0.0"
    },
    {
      "name": "",
      "version": "v9.0.0"
    },
    {
      "name": "after",
      "version": "v4.0.0"
    }
  ],
  "plugins": [
    {
      "name": "plugin-a",
      "version": "v3.0.0"
    }
  ],
  "mcps": []
}
`
	if got := readManifest(t, path); got != want {
		t.Fatalf("manifest = %q, want %q", got, want)
	}
}

func TestJSONRepositoryRemoveMissingNameDoesNotReplaceFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := writeManifest(t, "codex", `{"agent":"codex","skills":[{"name":"present","version":"v1.0.0"}]}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(logging.NewLogger(io.Discard, &stderr, true))

	if err := config.NewJSONRepository().Remove("codex", domain.PowerTypeSkill, "missing"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("Remove() replaced the manifest when the name was absent")
	}
	for _, want := range []string{"agent=codex", "power=missing"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("Remove() no-op log = %q, want field %q", stderr.String(), want)
		}
	}
}

func TestJSONRepositoryRemoveMissingManifestDoesNotCreateState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	if err := config.NewJSONRepository().Remove("codex", domain.PowerTypeSkill, "missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Remove() created state for a missing manifest: %v", err)
	}
}

func TestJSONRepositoryRemoveRejectsInvalidPowerBeforeIO(t *testing.T) {
	for _, tt := range []struct {
		name      string
		powerType domain.PowerType
		powerName string
		wantField string
	}{
		{name: "unknown type", powerType: "unknown", powerName: "power", wantField: "type"},
		{name: "empty name", powerType: domain.PowerTypeSkill, wantField: "name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("APM_HOME", home)
			statePath := filepath.Join(home, ".apm")
			if err := os.WriteFile(statePath, []byte("blocking file"), 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(statePath)
			if err != nil {
				t.Fatal(err)
			}

			err = config.NewJSONRepository().Remove("codex", tt.powerType, tt.powerName)
			if !errors.Is(err, config.ErrInvalidPower) {
				t.Fatalf("Remove() error = %v, want ErrInvalidPower before I/O", err)
			}
			if !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("Remove() error = %q, want field context %q", err, tt.wantField)
			}
			after, err := os.Stat(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || readManifest(t, statePath) != "blocking file" {
				t.Fatal("invalid Remove() touched the blocking state file")
			}
		})
	}
}

func TestJSONRepositoryWriteNormalizesPermissionsAndDropsUnknownFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	path := manifestPath(t, "codex")
	manifestDir := filepath.Dir(path)
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
  "agent": "codex",
  "unknown_top_level": true,
  "skills": [{"name": "old", "version": "v1.0.0", "unknown_entry": true}],
  "plugins": [],
  "mcps": []
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	err := config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "new", Type: domain.PowerTypeSkill, Version: "v2.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("manifest directory permissions = %04o, want 0700", got)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("manifest permissions = %04o, want 0600", got)
	}
	contents := readManifest(t, path)
	if strings.Contains(contents, "unknown_") {
		t.Fatalf("rewritten manifest retained unknown fields: %s", contents)
	}
}

func TestJSONRepositoryWriteCleansUpTemporaryFileAfterSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	err := config.NewJSONRepository().Set("codex", domain.PowerRef{
		Name: "power", Type: domain.PowerTypePlugin, Version: "v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Dir(manifestPath(t, "codex"))
	entries, err := os.ReadDir(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "codex.json" {
		t.Fatalf("manifest directory entries = %#v, want only codex.json", entries)
	}
}

func TestJSONRepositoryPublicMethodsWrapManifestReadAndDecodeErrors(t *testing.T) {
	methods := []struct {
		name string
		call func(*config.JSONRepository) error
	}{
		{name: "List", call: func(r *config.JSONRepository) error { _, err := r.List("codex"); return err }},
		{name: "Find", call: func(r *config.JSONRepository) error {
			_, _, err := r.Find("codex", domain.PowerTypeSkill, "power")
			return err
		}},
		{name: "Set", call: func(r *config.JSONRepository) error {
			return r.Set("codex", domain.PowerRef{Name: "power", Type: domain.PowerTypeSkill, Version: "v1.0.0"})
		}},
		{name: "Remove", call: func(r *config.JSONRepository) error { return r.Remove("codex", domain.PowerTypeSkill, "power") }},
	}

	for _, method := range methods {
		for _, failure := range []struct {
			name        string
			prepare     func(*testing.T) string
			assertCause func(*testing.T, error)
		}{
			{
				name: "read",
				prepare: func(t *testing.T) string {
					path := manifestPath(t, "codex")
					if err := os.MkdirAll(path, 0o700); err != nil {
						t.Fatal(err)
					}
					return path
				},
				assertCause: func(t *testing.T, err error) {
					var pathErr *os.PathError
					if !errors.As(err, &pathErr) {
						t.Fatalf("error = %v, want wrapped *os.PathError", err)
					}
				},
			},
			{
				name:    "decode",
				prepare: func(t *testing.T) string { return writeManifest(t, "codex", `{`) },
				assertCause: func(t *testing.T, err error) {
					var syntaxErr *json.SyntaxError
					if !errors.As(err, &syntaxErr) {
						t.Fatalf("error = %v, want wrapped *json.SyntaxError", err)
					}
				},
			},
		} {
			t.Run(method.name+"/"+failure.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("APM_HOME", home)
				path := failure.prepare(t)
				err := method.call(config.NewJSONRepository())
				if err == nil {
					t.Fatal("error = nil, want failure")
				}
				for _, want := range []string{method.name, "codex", path} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error = %q, want context %q", err, want)
					}
				}
				failure.assertCause(t, err)
			})
		}
	}
}

func TestJSONRepositoryIsStatelessAcrossAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	repository := config.NewJSONRepository()

	if err := repository.Set("codex", domain.PowerRef{
		Name: "codex-skill", Type: domain.PowerTypeSkill, Version: "v1.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Set("claude", domain.PowerRef{
		Name: "claude-plugin", Type: domain.PowerTypePlugin, Version: "v2.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	codex, err := repository.List("codex")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := repository.List("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := codex[domain.PowerTypeSkill]; len(got) != 1 || got[0].Name != "codex-skill" {
		t.Fatalf("codex skills = %#v, want only codex-skill", got)
	}
	if got := codex[domain.PowerTypePlugin]; len(got) != 0 {
		t.Fatalf("codex plugins = %#v, want empty", got)
	}
	if got := claude[domain.PowerTypePlugin]; len(got) != 1 || got[0].Name != "claude-plugin" {
		t.Fatalf("claude plugins = %#v, want only claude-plugin", got)
	}
	if got := claude[domain.PowerTypeSkill]; len(got) != 0 {
		t.Fatalf("claude skills = %#v, want empty", got)
	}
}

//go:build darwin

package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
	"golang.org/x/sys/unix"
)

func TestDarwinLocalClientRetrievesEveryResourceKind(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "skills", "demo", "v1.0.0", "SKILL.md"), "skill")
	writeTestFile(t, filepath.Join(root, "skills", "demo", "v1.0.0", "payload.md"), "payload")
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", ".codex-plugin", "plugin.json"), `{"name":"demo","version":"1.0.0"}`)
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", "README.md"), "plugin")
	writeTestFile(t, filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json"), `{"transport":"stdio","command":"npx"}`)
	writeTestFile(t, filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml"), "agent: demo")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	skillDestination := filepath.Join(t.TempDir(), "skill")
	if err := client.DownloadSkill(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}, skillDestination); err != nil {
		t.Fatalf("DownloadSkill() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(skillDestination, "payload.md")); err != nil || string(got) != "payload" {
		t.Fatalf("downloaded Skill payload = %q, %v", got, err)
	}

	pluginDestination := filepath.Join(t.TempDir(), "plugin")
	if err := client.DownloadPlugin(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}, pluginDestination); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(pluginDestination, "README.md")); err != nil || string(got) != "plugin" {
		t.Fatalf("downloaded Plugin payload = %q, %v", got, err)
	}

	server, err := client.GetMCP(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"})
	if err != nil || server.Command != "npx" {
		t.Fatalf("GetMCP() = %+v, %v", server, err)
	}

	agentDestination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, agentDestination); err != nil {
		t.Fatalf("DownloadAgent() error = %v", err)
	}
	if got, err := os.ReadFile(agentDestination); err != nil || string(got) != "agent: demo" {
		t.Fatalf("downloaded Agent = %q, %v", got, err)
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestDarwinSnapshotRejectsFIFOSwapBeforeAnyDataOpen(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	payload := filepath.Join(source, "payload.md")
	writeTestFile(t, payload, "payload")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dataOpened := false
	client.afterSnapshotDataOpen = func(relative string) {
		if relative == "payload.md" {
			dataOpened = true
		}
	}
	client.beforeSnapshotFileOpen = func(relative string) {
		if relative != "payload.md" {
			return
		}
		if err := os.Remove(payload); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "skill")

	err = client.DownloadSkill(context.Background(), testSkillRef, destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
	if dataOpened {
		t.Fatal("swapped FIFO was opened for data access")
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func TestDarwinSnapshotRejectsHardlinkedSingleFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	writeTestFile(t, source, "agent")
	if err := os.Link(source, source+".link"); err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "agent.yaml")

	err = client.DownloadAgent(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, destination)

	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want ErrUnsafeContent", err)
	}
	assertNoLinkSnapshotWorkspaceResidue(t, root)
}

func assertNoLinkSnapshotWorkspaceResidue(t *testing.T, resourcesRoot string) {
	t.Helper()
	for _, directory := range []string{filepath.Dir(resourcesRoot), resourcesRoot} {
		matches, err := filepath.Glob(filepath.Join(directory, linkSnapshotWorkspacePrefix+"*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Fatalf("link snapshot workspace residue = %v", matches)
		}
	}
}

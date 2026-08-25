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
	writeTestFile(t, filepath.Join(root, "skills", "demo", "v1.0.0", "SKILL.md"), "---\nname: demo\nversion: v1.0.0\ndescription: demo\n---\nskill")
	writeTestFile(t, filepath.Join(root, "skills", "demo", "v1.0.0", "skill-card.md"), "card")
	writeTestFile(t, filepath.Join(root, "skills", "demo", "v1.0.0", "payload.md"), "payload")
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", "apc-plugin.json"), `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"demo","skills":["skills/demo"]}`)
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", "skills", "demo", "SKILL.md"), "skill")
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", "skills", "demo", "skill-card.md"), "card")
	writeTestFile(t, filepath.Join(root, "plugins", "demo", "v1.0.0", "skills", "demo", "README.md"), "plugin")
	writeTestFile(t, filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json"), `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"demo","definition":{"transport":"stdio","command":"npx"}}`)
	writeTestFile(t, filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml"), "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	skillRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	pluginRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	mcpRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	agentRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	skillContent, err := client.loadCatalogContent(context.Background(), skillRef)
	if err != nil {
		t.Fatal(err)
	}
	pluginContent, err := client.loadCatalogContent(context.Background(), pluginRef)
	if err != nil {
		t.Fatal(err)
	}
	mcpContent, err := client.loadCatalogContent(context.Background(), mcpRef)
	if err != nil {
		t.Fatal(err)
	}
	agentContent, err := client.loadCatalogContent(context.Background(), agentRef)
	if err != nil {
		t.Fatal(err)
	}

	skillDestination := filepath.Join(t.TempDir(), "skill")
	if err := client.DownloadSkill(context.Background(), skillContent.entry, skillDestination); err != nil {
		t.Fatalf("DownloadSkill() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(skillDestination, "payload.md")); err != nil || string(got) != "payload" {
		t.Fatalf("downloaded Skill payload = %q, %v", got, err)
	}

	pluginDestination := filepath.Join(t.TempDir(), "plugin")
	if err := client.DownloadPlugin(context.Background(), pluginContent.entry, pluginDestination); err != nil {
		t.Fatalf("DownloadPlugin() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(pluginDestination, "skills", "demo", "README.md")); err != nil || string(got) != "plugin" {
		t.Fatalf("downloaded Plugin payload = %q, %v", got, err)
	}

	server, err := client.GetMCP(context.Background(), mcpContent.entry)
	if err != nil || server.Command != "npx" {
		t.Fatalf("GetMCP() = %+v, %v", server, err)
	}

	agentDestination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), agentContent.entry, agentDestination); err != nil {
		t.Fatalf("DownloadAgent() error = %v", err)
	}
	if got, err := os.ReadFile(agentDestination); err != nil || string(got) != "schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n" {
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

	err = client.DownloadSkill(context.Background(), catalogEntryForTest(testSkillRef), destination)

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

	err = client.DownloadAgent(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}), destination)

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

//go:build linux

package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestSkillAcceptsExactFileAndTreeSizeLimits(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	for index := 0; index < 10; index++ {
		name := "payload-" + strconv.Itoa(index) + ".md"
		if index == 0 {
			name = "SKILL.md"
		} else if index == 1 {
			name = "skill-card.md"
		}
		writeRepeatedTextFile(t, filepath.Join(source, name), maxTreeFileSize)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := client.validateSkillSource(context.Background(), source)

	if err != nil {
		t.Fatalf("validate exact limits: %v", err)
	}
	if len(snapshot.nodes) != 10 {
		t.Fatalf("snapshot nodes = %d, want 10", len(snapshot.nodes))
	}
}

func TestAgentAcceptsExactTreeFileLimitAndMCPExactTwentyMiB(t *testing.T) {
	root := t.TempDir()
	agentRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	agentPath := filepath.Join(root, "agents", agentRef.Name, agentRef.Version, "agent.yaml")
	agentPrefix := "schema_version: 1\nagent: {id: demo, name: demo, version: v1.0.0, description: demo, soul: help safely}\n#"
	agentContents := agentPrefix + strings.Repeat("x", int(maxTreeFileSize)-len(agentPrefix)-1) + "\n"
	writeTestFile(t, agentPath, agentContents)
	mcpPath := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	validJSON := []byte(`{"schema_version":1,"name":"demo","version":"v1.0.0","description":"Demo MCP server","definition":{"transport":"stdio","command":"npx"}}`)
	mcpContents := append(validJSON, bytes.Repeat([]byte(" "), int(maxSingleSize)-len(validJSON))...)
	writeTestBytes(t, mcpPath, mcpContents)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	mcpRef := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	mcpContent, err := client.loadCatalogContent(context.Background(), mcpRef)
	if err != nil {
		t.Fatal(err)
	}
	agentContent, err := client.loadCatalogContent(context.Background(), agentRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMCP(context.Background(), mcpContent.entry); err != nil {
		t.Fatalf("GetMCP(exact limit) error = %v", err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	if err := client.DownloadAgent(context.Background(), agentContent.entry, destination); err != nil {
		t.Fatalf("DownloadAgent(exact tree file limit) error = %v", err)
	}
	if info, err := os.Stat(filepath.Join(destination, "agent.yaml")); err != nil || info.Size() != maxTreeFileSize {
		t.Fatalf("Agent agent.yaml = %v, %v, want %d bytes", info, err, maxTreeFileSize)
	}
}

func TestDownloadPluginRejectsSharedTreeFileLimit(t *testing.T) {
	root := t.TempDir()
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
	source := filepath.Join(root, "plugins", ref.Name, ref.Version)
	writeTestFile(t, filepath.Join(source, "payload.md"), "x")
	if err := os.Truncate(filepath.Join(source, "payload.md"), maxTreeFileSize+1); err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "plugin")

	err = client.DownloadPlugin(context.Background(), catalogEntryForTest(ref), destination)

	assertUnsafeNoDownloadResidue(t, err, destination)
}

func TestMCPAndAgentRejectHardlinkBinaryAndArchiveContent(t *testing.T) {
	unsafeCases := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "hardlink",
			mutate: func(t *testing.T, path string) {
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{name: "binary", mutate: func(t *testing.T, path string) { writeTestBytes(t, path, []byte{'a', 0, 'b'}) }},
		{name: "archive", mutate: func(t *testing.T, path string) { writeTestBytes(t, path, []byte("PK\x03\x04payload")) }},
		{name: "ar archive", mutate: func(t *testing.T, path string) { writeTestBytes(t, path, []byte("!<arch>\npayload")) }},
	}
	for _, resource := range []string{"mcp", "agent"} {
		for _, unsafeCase := range unsafeCases {
			t.Run(resource+"/"+unsafeCase.name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
				contents := `{"transport":"stdio","command":"npx"}`
				if resource == "agent" {
					path = filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
					contents = "agent"
				}
				writeTestFile(t, path, contents)
				unsafeCase.mutate(t, path)
				client, err := New(root)
				if err != nil {
					t.Fatal(err)
				}
				if resource == "mcp" {
					_, err = client.GetMCP(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}))
					if !errors.Is(err, cloud.ErrUnsafeContent) {
						t.Fatalf("GetMCP() error = %v, want ErrUnsafeContent", err)
					}
					return
				}
				destination := filepath.Join(t.TempDir(), "agent")
				err = client.DownloadAgent(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}), destination)
				assertUnsafeNoDownloadResidue(t, err, destination)
			})
		}
	}
}

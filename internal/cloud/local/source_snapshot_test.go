package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestDownloadSkillRequiresMarkerInDescriptorSnapshot(t *testing.T) {
	for _, replacement := range []string{"removed", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
			marker := filepath.Join(source, "SKILL.md")
			writeTestFile(t, marker, "skill")
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			client.beforeTreeSnapshot = func() {
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if replacement == "directory" {
					if err := os.Mkdir(marker, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			destination := filepath.Join(t.TempDir(), "download")

			err = client.DownloadSkill(context.Background(), catalogEntryForTest(testSkillRef), destination)

			assertUnsafeNoDownloadResidue(t, err, destination)
		})
	}
}

func TestDownloadPluginRequiresMarkerInDescriptorSnapshot(t *testing.T) {
	for _, replacement := range []string{"removed", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			root := t.TempDir()
			ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"}
			source := filepath.Join(root, "plugins", ref.Name, ref.Version)
			marker := filepath.Join(source, ".codex-plugin", "plugin.json")
			writeTestFile(t, marker, `{"name":"demo","version":"1.0.0"}`)
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			client.beforeTreeSnapshot = func() {
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if replacement == "directory" {
					if err := os.Mkdir(marker, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			destination := filepath.Join(t.TempDir(), "download")

			err = client.DownloadPlugin(context.Background(), catalogEntryForTest(ref), destination)

			assertUnsafeNoDownloadResidue(t, err, destination)
		})
	}
}

func TestGetMCPCancellationAfterCloneReturnsCanceled(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	writeTestFile(t, marker, `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"Demo MCP server","definition":{"transport":"stdio","command":"npx","args":["`+string(bytes.Repeat([]byte("x"), 1<<20))+`"]}}`)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}
	content, err := client.loadCatalogContent(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client.afterMCPClone = cancel

	_, err = client.GetMCP(ctx, content.entry)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetMCP() error = %v, want context.Canceled", err)
	}
}

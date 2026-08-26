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

var testSkillRef = domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}

func catalogEntryForTest(ref domain.PowerRef) domain.CatalogEntry {
	contentType := "application/octet-stream"
	switch ref.Type {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		contentType = "application/gzip"
	case domain.PowerTypeMCP:
		contentType = "application/json"
	case domain.PowerTypeAgent:
		contentType = "application/gzip"
	}
	return domain.CatalogEntry{
		Ref: ref, Description: "test fixture", Message: "test fixture", ContentType: contentType,
		ContentSize: 1, ContentMD5: "00000000000000000000000000000000",
		PublishedAt: "1970-01-01T00:00:00Z", Tags: []string{},
	}
}

func TestLocalTreeDownloadsSanitizePublicationModes(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		marker   string
		ref      domain.PowerRef
		download func(*Client, context.Context, domain.CatalogEntry, string) error
	}{
		{
			name: "Skill", root: "skills", marker: "SKILL.md", ref: testSkillRef,
			download: func(client *Client, ctx context.Context, entry domain.CatalogEntry, destination string) error {
				return client.DownloadSkill(ctx, entry, destination)
			},
		},
		{
			name: "Plugin", root: "plugins", marker: "apc-plugin.json",
			ref: domain.PowerRef{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
			download: func(client *Client, ctx context.Context, entry domain.CatalogEntry, destination string) error {
				return client.DownloadPlugin(ctx, entry, destination)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resources := t.TempDir()
			source := filepath.Join(resources, test.root, test.ref.Name, test.ref.Version)
			nestedRelative := "scripts"
			if test.ref.Type == domain.PowerTypePlugin {
				nestedRelative = filepath.Join("skills", test.ref.Name, "scripts")
			}
			nested := filepath.Join(source, nestedRelative)
			if err := os.MkdirAll(nested, 0o755); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(source, test.marker)
			markerContents := "---\nname: demo\nversion: v1.0.0\ndescription: demo\n---\n"
			if test.ref.Type == domain.PowerTypePlugin {
				markerContents = `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"demo","skills":["skills/demo"]}`
			}
			if err := os.WriteFile(marker, []byte(markerContents), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.ref.Type == domain.PowerTypeSkill {
				writeTestFile(t, filepath.Join(source, "skill-card.md"), "card\n")
			} else {
				writeTestFile(t, filepath.Join(source, "skills", test.ref.Name, "SKILL.md"), "skill\n")
				writeTestFile(t, filepath.Join(source, "skills", test.ref.Name, "skill-card.md"), "card\n")
			}
			executable := filepath.Join(nested, "run.sh")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o710); err != nil {
				t.Fatal(err)
			}
			for path, mode := range map[string]os.FileMode{
				source: 0o700, nested: 0o711, marker: 0o600, executable: 0o710,
			} {
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
			}
			client, err := New(resources)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "download")
			content, err := client.loadCatalogContent(context.Background(), test.ref)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.download(client, context.Background(), content.entry, destination); err != nil {
				t.Fatalf("Download%s() error = %v", test.name, err)
			}
			expectations := map[string]publishedNodeExpectation{
				".":                                     {mode: 0o755, directory: true},
				nestedRelative:                          {mode: 0o755, directory: true},
				test.marker:                             {mode: 0o644},
				filepath.Join(nestedRelative, "run.sh"): {mode: 0o754},
			}
			if test.ref.Type == domain.PowerTypeSkill {
				expectations["skill-card.md"] = publishedNodeExpectation{mode: 0o644}
			} else {
				expectations["skills"] = publishedNodeExpectation{mode: 0o755, directory: true}
				expectations[filepath.Join("skills", test.ref.Name)] = publishedNodeExpectation{mode: 0o755, directory: true}
				expectations[filepath.Join("skills", test.ref.Name, "SKILL.md")] = publishedNodeExpectation{mode: 0o644}
				expectations[filepath.Join("skills", test.ref.Name, "skill-card.md")] = publishedNodeExpectation{mode: 0o644}
			}
			assertPublishedTree(t, destination, expectations)
		})
	}
}

func TestDownloadSkillRejectsUnsafeSourceTree(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "source symlink",
			mutate: func(t *testing.T, root, source string) {
				t.Helper()
				target := t.TempDir()
				writeTestFile(t, filepath.Join(target, "SKILL.md"), "skill")
				if err := os.RemoveAll(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, source); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "hardlink",
			mutate: func(t *testing.T, _, source string) {
				t.Helper()
				if err := os.Link(filepath.Join(source, "SKILL.md"), filepath.Join(source, "duplicate.md")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "binary content",
			mutate: func(t *testing.T, _, source string) {
				writeTestBytes(t, filepath.Join(source, "payload.bin"), []byte{'a', 0, 'b'})
			},
		},
		{
			name: "archive signature",
			mutate: func(t *testing.T, _, source string) {
				writeTestBytes(t, filepath.Join(source, "payload.zip"), []byte("PK\x03\x04payload"))
			},
		},
		{
			name: "file exceeds ten MiB",
			mutate: func(t *testing.T, _, source string) {
				path := filepath.Join(source, "large.md")
				writeTestFile(t, path, "x")
				if err := os.Truncate(path, 10<<20+1); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tree exceeds one hundred MiB",
			mutate: func(t *testing.T, _, source string) {
				for index := 0; index < 10; index++ {
					path := filepath.Join(source, "large-"+string(rune('a'+index))+".md")
					writeRepeatedTextFile(t, path, maxTreeFileSize)
				}
			},
		},
		{
			name: "tree exceeds one thousand nodes",
			mutate: func(t *testing.T, _, source string) {
				for index := 0; index < 1000; index++ {
					if err := os.Mkdir(filepath.Join(source, "node-"+strconv.Itoa(index)), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "depth exceeds sixteen",
			mutate: func(t *testing.T, _, source string) {
				path := source
				for index := 0; index < 17; index++ {
					path = filepath.Join(path, "d")
				}
				writeTestFile(t, filepath.Join(path, "payload.md"), "text")
			},
		},
		{
			name: "path exceeds two hundred fifty five bytes",
			mutate: func(t *testing.T, _, source string) {
				path := source
				for index := 0; index < 5; index++ {
					path = filepath.Join(path, strings.Repeat("p", 60))
				}
				writeTestFile(t, filepath.Join(path, "payload.md"), "text")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
			writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
			writeTestFile(t, filepath.Join(source, "skill-card.md"), "card")
			test.mutate(t, root, source)
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "download")
			err = client.DownloadSkill(context.Background(), catalogEntryForTest(testSkillRef), destination)
			assertUnsafeNoDownloadResidue(t, err, destination)
		})
	}
}

func TestDownloadSkillRejectsOverlappingAndNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	writeTestFile(t, filepath.Join(source, "skill-card.md"), "card")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("destination inside source", func(t *testing.T) {
		destination := filepath.Join(source, "nested", "download")
		err := client.DownloadSkill(context.Background(), catalogEntryForTest(testSkillRef), destination)
		assertUnsafeNoDownloadResidue(t, err, destination)
	})
	t.Run("non-empty destination", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "download")
		writeTestFile(t, filepath.Join(destination, "keep"), "keep")
		err := client.DownloadSkill(context.Background(), catalogEntryForTest(testSkillRef), destination)
		if !errors.Is(err, cloud.ErrUnsafeContent) {
			t.Fatalf("DownloadSkill() error = %v, want ErrUnsafeContent", err)
		}
		if got, readErr := os.ReadFile(filepath.Join(destination, "keep")); readErr != nil || string(got) != "keep" {
			t.Fatalf("pre-existing destination changed: %q, %v", got, readErr)
		}
		assertNoSiblingTemps(t, destination)
	})
}

func TestMCPRejectsContentOverTwentyMiBAndAgentOverTreeFileLimit(t *testing.T) {
	root := t.TempDir()
	mcpPath := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	agentPath := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	writeTestFile(t, mcpPath, "x")
	if err := os.Truncate(mcpPath, 20<<20+1); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, agentPath, "x")
	if err := os.Truncate(agentPath, maxTreeFileSize+1); err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMCP(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"})); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want ErrUnsafeContent", err)
	}
	destination := filepath.Join(t.TempDir(), "agent")
	err = client.DownloadAgent(context.Background(), catalogEntryForTest(domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}), destination)
	assertUnsafeNoDownloadResidue(t, err, destination)
}

func assertUnsafeNoDownloadResidue(t *testing.T, err error, destination string) {
	t.Helper()
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("download error = %v, want ErrUnsafeContent", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after rejection: %v", statErr)
	}
	assertNoSiblingTemps(t, destination)
}

// assertInvalidAgentNoDownloadResidue 断言 Agent 语义校验失败归类为
// domain.ErrInvalidAgent（与 Remote provider 对齐），且不留下任何可见输出。
func assertInvalidAgentNoDownloadResidue(t *testing.T, err error, destination string) {
	t.Helper()
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("download error = %v, want ErrInvalidAgent classification", err)
	}
	if errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("download error = %v, must not classify semantic failure as ErrUnsafeContent", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after semantic rejection: %v", statErr)
	}
	assertNoSiblingTemps(t, destination)
}

func assertNoSiblingTemps(t *testing.T, destination string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary residue = %v, want none", matches)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	writeTestBytes(t, path, []byte(contents))
}

func writeTestBytes(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRepeatedTextFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	remaining := size
	for remaining > 0 {
		write := int64(len(chunk))
		if write > remaining {
			write = remaining
		}
		if _, err := file.Write(chunk[:int(write)]); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		remaining -= write
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

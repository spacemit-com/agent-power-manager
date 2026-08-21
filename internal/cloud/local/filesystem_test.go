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
			test.mutate(t, root, source)
			client, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "download")
			err = client.DownloadSkill(context.Background(), testSkillRef, destination)
			assertUnsafeNoDownloadResidue(t, err, destination)
		})
	}
}

func TestDownloadSkillRejectsOverlappingAndNonEmptyDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "skills", testSkillRef.Name, testSkillRef.Version)
	writeTestFile(t, filepath.Join(source, "SKILL.md"), "skill")
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("destination inside source", func(t *testing.T) {
		destination := filepath.Join(source, "nested", "download")
		err := client.DownloadSkill(context.Background(), testSkillRef, destination)
		assertUnsafeNoDownloadResidue(t, err, destination)
	})
	t.Run("non-empty destination", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "download")
		writeTestFile(t, filepath.Join(destination, "keep"), "keep")
		err := client.DownloadSkill(context.Background(), testSkillRef, destination)
		if !errors.Is(err, cloud.ErrUnsafeContent) {
			t.Fatalf("DownloadSkill() error = %v, want ErrUnsafeContent", err)
		}
		if got, readErr := os.ReadFile(filepath.Join(destination, "keep")); readErr != nil || string(got) != "keep" {
			t.Fatalf("pre-existing destination changed: %q, %v", got, readErr)
		}
		assertNoSiblingTemps(t, destination)
	})
}

func TestMCPAndAgentRejectContentOverTwentyMiB(t *testing.T) {
	root := t.TempDir()
	mcpPath := filepath.Join(root, "mcps", "demo", "v1.0.0", "MCP.json")
	agentPath := filepath.Join(root, "agents", "demo", "v1.0.0", "agent.yaml")
	for _, path := range []string{mcpPath, agentPath} {
		writeTestFile(t, path, "x")
		if err := os.Truncate(path, 20<<20+1); err != nil {
			t.Fatal(err)
		}
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetMCP(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v1.0.0"}); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("GetMCP() error = %v, want ErrUnsafeContent", err)
	}
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	err = client.DownloadAgent(context.Background(), domain.PowerRef{Name: "demo", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, destination)
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

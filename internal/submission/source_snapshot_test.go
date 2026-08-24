package submission

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillSourceBuildUsesLoadedSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeValidSkill(t, dir)
	originalSkill := []byte("---\nname: demo\nversion: \"v1.0.0\"\ndescription: does things\n---\n# Demo Skill\n")
	originalCard := []byte(validSkillCardMarkdown)

	loaded, err := (skillSourceLoader{}).Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	replaceTestFile(t, filepath.Join(dir, "SKILL.md"), []byte("---\nname: shadow\nversion: \"v9.9.9\"\ndescription: changed\n---\n"))
	writeTestFile(t, filepath.Join(dir, "skill-card.md"), validSkillCardMarkdown+"\nchanged after load\n")
	writeTestFile(t, filepath.Join(dir, "late.txt"), "added after load\n")

	packed, err := loaded.build()
	if err != nil {
		t.Fatalf("build() error = %v", err)
	}
	entries := readTarGzFiles(t, packed)
	if !bytes.Equal(entries["SKILL.md"], originalSkill) {
		t.Fatalf("SKILL.md = %q, want loaded snapshot %q", entries["SKILL.md"], originalSkill)
	}
	if !bytes.Equal(entries["skill-card.md"], originalCard) {
		t.Fatalf("skill-card.md = %q, want loaded snapshot", entries["skill-card.md"])
	}
	if _, exists := entries["late.txt"]; exists {
		t.Fatal("archive contains file added after Load")
	}
}

func TestPluginSourceBuildUsesLoadedSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeValidPlugin(t, dir)
	originalManifest := []byte(validPluginManifest)
	originalCard := []byte(validSkillCardMarkdown)

	loaded, err := (pluginSourceLoader{}).Load(context.Background(), dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	replaceTestFile(t, filepath.Join(dir, "apc-plugin.json"), []byte(`{"schema_version":1,"name":"shadow","version":"v9.9.9","description":"changed","skills":["skills/reviewer"]}`))
	writeTestFile(t, filepath.Join(dir, "skills", "reviewer", "skill-card.md"), validSkillCardMarkdown+"\nchanged after load\n")
	writeTestFile(t, filepath.Join(dir, "late.txt"), "added after load\n")

	packed, err := loaded.build()
	if err != nil {
		t.Fatalf("build() error = %v", err)
	}
	entries := readTarGzFiles(t, packed)
	if !bytes.Equal(entries["apc-plugin.json"], originalManifest) {
		t.Fatalf("apc-plugin.json = %q, want loaded snapshot %q", entries["apc-plugin.json"], originalManifest)
	}
	if !bytes.Equal(entries["skills/reviewer/skill-card.md"], originalCard) {
		t.Fatalf("embedded skill-card.md = %q, want loaded snapshot", entries["skills/reviewer/skill-card.md"])
	}
	if _, exists := entries["late.txt"]; exists {
		t.Fatal("archive contains file added after Load")
	}
}

func replaceTestFile(t *testing.T, destination string, contents []byte) {
	t.Helper()
	replacement := filepath.Join(filepath.Dir(destination), ".replacement")
	if err := os.WriteFile(replacement, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, destination); err != nil {
		t.Fatal(err)
	}
}

func readTarGzFiles(t *testing.T, packed []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	files := make(map[string][]byte)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = body
	}
	return files
}

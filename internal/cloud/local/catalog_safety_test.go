package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalCatalogStatFailureNeverRemovesSubstitutedDestination(t *testing.T) {
	client := newCommittedCatalogClient(t)
	parent := t.TempDir()
	destination := filepath.Join(parent, "catalog.json")
	moved := filepath.Join(parent, "opened-catalog.json")
	injected := errors.New("injected destination stat failure")
	client.catalogDestinationAfterOpen = func(path string, _ *os.File) {
		if err := os.Rename(path, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("substitute"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client.catalogDestinationStat = func(*os.File) (fs.FileInfo, error) {
		return nil, injected
	}
	t.Cleanup(func() {
		client.catalogDestinationAfterOpen = nil
		client.catalogDestinationStat = nil
	})

	err := client.DownloadCatalog(context.Background(), destination)
	if !errors.Is(err, cloud.ErrCatalogDestination) || !errors.Is(err, injected) {
		t.Fatalf("DownloadCatalog() error = %v, want ErrCatalogDestination and injected stat failure", err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != "substitute" {
		t.Fatalf("substituted destination = %q, %v, want preserved substitute", got, err)
	}
	if info, err := os.Stat(moved); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("moved opened destination stat = %v, %v, want conservative retained file", info, err)
	}
}

func TestLocalCatalogCancellationDuringArchiveBuildPrecedesDestinationCreation(t *testing.T) {
	root := t.TempDir()
	writeCancelableCatalogSkill(t, root, bytes.Repeat([]byte("x"), 4*sourceCopyChunkSize))
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks := 0
	client.catalogArchiveAfterBodyChunk = func(relative string) {
		if relative == "payload.txt" {
			chunks++
			cancel()
		}
	}
	t.Cleanup(func() { client.catalogArchiveAfterBodyChunk = nil })
	destination := filepath.Join(t.TempDir(), "catalog.json")

	err = client.DownloadCatalog(ctx, destination)
	if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("DownloadCatalog() error = %v, want only context.Canceled", err)
	}
	if chunks != 1 {
		t.Fatalf("archive payload chunks before cancellation = %d, want 1", chunks)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination stat error = %v, want absent before creation", err)
	}
}

func TestLocalCatalogCancellationAfterDestinationOpenCleansOwnedFile(t *testing.T) {
	client := newCommittedCatalogClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	client.catalogDestinationAfterOpen = func(string, *os.File) { cancel() }
	t.Cleanup(func() { client.catalogDestinationAfterOpen = nil })
	destination := filepath.Join(t.TempDir(), "catalog.json")

	err := client.DownloadCatalog(ctx, destination)
	if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("DownloadCatalog() error = %v, want only context.Canceled", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination stat error = %v, want identity-checked cleanup", err)
	}
}

func TestLocalCatalogPluginValidationUsesCapturedSnapshotOnly(t *testing.T) {
	root := t.TempDir()
	source := writeSnapshotCatalogPlugin(t, root)
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	mutated := false
	client.afterSnapshotDataOpen = func(relative string) {
		if mutated || filepath.ToSlash(relative) != "skills/helper/skill-card.md" {
			return
		}
		mutated = true
		if err := os.WriteFile(filepath.Join(source, "apc-plugin.json"), []byte(`{"schema_version":1,"name":"catalog-plugin","version":"v1.0.0","description":"mutated","skills":null}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("late extra"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { client.afterSnapshotDataOpen = nil })
	destination := filepath.Join(t.TempDir(), "catalog.json")

	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatalf("DownloadCatalog() error = %v", err)
	}
	if !mutated {
		t.Fatal("live Plugin fixture was not mutated after snapshot data open")
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := snapshot.Metadata(domain.PowerRef{Type: domain.PowerTypePlugin, Name: "catalog-plugin", Version: "v1.0.0"})
	if err != nil || entry.Description != "captured description" {
		t.Fatalf("Plugin metadata = %+v, %v, want captured description", entry, err)
	}
}

func newCommittedCatalogClient(t *testing.T) *Client {
	t.Helper()
	root, err := filepath.Abs("../resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeCancelableCatalogSkill(t *testing.T, root string, payload []byte) {
	t.Helper()
	source := filepath.Join(root, "skills", "cancel-skill", "v1.0.0")
	files := map[string][]byte{
		"SKILL.md":      []byte("---\nname: cancel-skill\nversion: \"v1.0.0\"\ndescription: cancellation fixture\n---\n# Skill\n"),
		"skill-card.md": []byte("card\n"),
		"payload.txt":   payload,
	}
	for name, contents := range files {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeSnapshotCatalogPlugin(t *testing.T, root string) string {
	t.Helper()
	source := filepath.Join(root, "plugins", "catalog-plugin", "v1.0.0")
	manifest, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"name":           "catalog-plugin",
		"version":        "v1.0.0",
		"description":    "captured description",
		"skills":         []string{"skills/helper"},
	})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"apc-plugin.json":             manifest,
		"skills/helper/SKILL.md":      []byte("skill\n"),
		"skills/helper/skill-card.md": []byte("card\n"),
	}
	for relative, contents := range files {
		path := filepath.Join(source, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return source
}

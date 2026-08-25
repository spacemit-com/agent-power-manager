package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"apm/internal/domain"
)

func TestRepositoryRejectsNonAbsoluteOrUncleanPath(t *testing.T) {
	root := t.TempDir()
	paths := []string{
		"catalog.json",
		filepath.Join(root, ".", "catalog.json")[:len(root)] + string(filepath.Separator) + "." + string(filepath.Separator) + "catalog.json",
		filepath.Join(root, "catalog.json") + string(filepath.Separator),
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			if _, err := NewRepository(path); err == nil {
				t.Fatalf("NewRepository(%q) error = nil, want path validation error", path)
			}
		})
	}
}

func TestRepositoryLoadAbsentFile(t *testing.T) {
	repository := newTestRepository(t, filepath.Join(trustedRepositoryDir(t), "catalog.json"))
	if _, _, err := repository.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want os.ErrNotExist", err)
	}
}

func TestRepositoryLoadValidCatalogAndReturnsImmutableSnapshot(t *testing.T) {
	data := validCatalogJSON()
	path := writeRepositoryFile(t, trustedRepositoryDir(t), "catalog.json", data, 0o600)
	repository := newTestRepository(t, path)

	snapshot, version, err := repository.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(data)
	if version != (domain.CatalogVersion{SHA256: hex.EncodeToString(wantHash[:]), Size: int64(len(data))}) {
		t.Fatalf("Load() version = %#v, want SHA-256 of exact bytes and size %d", version, len(data))
	}
	entries := snapshot.List(domain.PowerTypeSkill)
	if len(entries) == 0 {
		t.Fatal("Load() snapshot has no skills")
	}
	entries[0].Tags[0] = "changed"
	if again := snapshot.List(domain.PowerTypeSkill); again[0].Tags[0] == "changed" {
		t.Fatal("Load() returned snapshot shares mutable Tags with caller")
	}
}

func TestRepositoryLoadRejectsUnsafeModeAndNonRegularFile(t *testing.T) {
	data := validCatalogJSON()
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "mode 0644",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(trustedRepositoryDir(t), "catalog.json")
			tt.setup(t, path)
			repository := newTestRepository(t, path)
			if _, _, err := repository.Load(); err == nil {
				t.Fatal("Load() error = nil, want physical-file validation error")
			}
		})
	}
}

func TestRepositoryLoadCatalogSizeBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("100 MiB physical boundary fixture")
	}
	for _, tt := range []struct {
		name string
		size int64
	}{
		{name: "exact", size: MaxCatalogSize},
		{name: "one byte over", size: MaxCatalogSize + 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := validCatalogJSONWithSize(t, tt.size)
			path := writeRepositoryFile(t, trustedRepositoryDir(t), "catalog.json", data, 0o600)
			repository := newTestRepository(t, path)
			_, version, err := repository.Load()
			if tt.size == MaxCatalogSize {
				if err != nil {
					t.Fatalf("Load(exact boundary) error = %v", err)
				}
				if version.Size != MaxCatalogSize {
					t.Fatalf("Load(exact boundary) size = %d, want %d", version.Size, MaxCatalogSize)
				}
				return
			}
			if err == nil {
				t.Fatal("Load(MaxCatalogSize+1) error = nil, want size failure")
			}
		})
	}
}

func TestRepositoryLoadRejectsInvalidCatalogBytes(t *testing.T) {
	valid := validCatalogJSON()
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{name: "invalid JSON", data: []byte(`{`)},
		{name: "invalid schema", data: replaceJSON(t, valid, `"schema_version":1`, `"schema_version":2`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeRepositoryFile(t, trustedRepositoryDir(t), "catalog.json", tt.data, 0o600)
			repository := newTestRepository(t, path)
			if _, _, err := repository.Load(); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("Load() error = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func newTestRepository(t *testing.T, path string) Repository {
	t.Helper()
	repository, err := NewRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func writeRepositoryFile(t *testing.T, directory, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func trustedRepositoryDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

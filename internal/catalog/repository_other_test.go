//go:build !linux && !darwin && !freebsd

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestRepositoryUnsupportedPlatformFailsClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "catalog.json")
	repository, err := NewRepository(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Load(); err == nil {
		t.Fatal("Load() error = nil, want unsupported fail-closed error")
	}

	data := validCatalogJSON()
	source := filepath.Join(root, "source.json")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Replace(unsupportedRepositoryVersion(data), source); err == nil {
		t.Fatal("Replace() error = nil, want unsupported fail-closed error")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("destination after unsupported Replace: %v", err)
	}
}

func unsupportedRepositoryVersion(data []byte) domain.CatalogVersion {
	digest := sha256.Sum256(data)
	return domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

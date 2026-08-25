//go:build freebsd

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestRepositoryFreeBSDLoadUsesSecureUnixBoundary(t *testing.T) {
	data := validCatalogJSON()
	path := writeRepositoryFile(t, trustedRepositoryDir(t), "catalog.json", data, 0o600)
	repository := newTestRepository(t, path)

	if _, version, err := repository.Load(); err != nil {
		t.Fatal(err)
	} else if version != freeBSDRepositoryVersion(data) {
		t.Fatalf("Load() version = %#v, want %#v", version, freeBSDRepositoryVersion(data))
	}
}

func TestRepositoryFreeBSDReplaceFailsClosed(t *testing.T) {
	data := validCatalogJSON()
	for _, existing := range []bool{false, true} {
		name := "absent"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := filepath.Join(root, "catalog.json")
			if existing {
				writeRepositoryFile(t, root, "catalog.json", data, 0o600)
			}
			source := writeRepositoryFile(t, root, "source.json", data, 0o600)
			if _, err := newTestRepository(t, path).Replace(freeBSDRepositoryVersion(data), source); err == nil {
				t.Fatal("Replace() error = nil, want unsupported fail-closed error")
			}
			if existing {
				assertRepositoryFileBytes(t, path, data)
			} else if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("absent destination after failed Replace: %v", err)
			}
		})
	}
}

func freeBSDRepositoryVersion(data []byte) domain.CatalogVersion {
	digest := sha256.Sum256(data)
	return domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

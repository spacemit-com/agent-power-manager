//go:build linux || darwin || freebsd

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"apm/internal/domain"
	"golang.org/x/sys/unix"
)

func TestRepositoryLoadRejectsSymlinkAndHardlink(t *testing.T) {
	data := validCatalogJSON()
	t.Run("symlink", func(t *testing.T) {
		root := trustedRepositoryDir(t)
		target := writeRepositoryFile(t, root, "target.json", data, 0o600)
		path := filepath.Join(root, "catalog.json")
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, _, err := newTestRepository(t, path).Load(); err == nil {
			t.Fatal("Load(symlink) error = nil, want rejection")
		}
	})

	t.Run("hardlink", func(t *testing.T) {
		root := trustedRepositoryDir(t)
		target := writeRepositoryFile(t, root, "target.json", data, 0o600)
		path := filepath.Join(root, "catalog.json")
		if err := os.Link(target, path); err != nil {
			t.Fatal(err)
		}
		if _, _, err := newTestRepository(t, path).Load(); err == nil {
			t.Fatal("Load(hardlink) error = nil, want rejection")
		}
	})
}

func TestRepositoryLoadRejectsChangedIdentityWhileReading(t *testing.T) {
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", validCatalogJSON(), 0o600)
	replacement := writeRepositoryFile(t, root, "replacement.json", validCatalogJSON(), 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalRead := platform.ops.readBounded
	platform.ops.readBounded = func(reader io.Reader, limit int64) ([]byte, error) {
		data, err := originalRead(reader, limit)
		if err != nil {
			return nil, err
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		return data, nil
	}

	if _, _, err := repository.Load(); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want changed-identity rejection", err)
	}
}

func TestRepositoryReplaceRejectsExpectedMismatchAndInvalidCatalog(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	validVersion := repositoryVersion(newData)
	tests := []struct {
		name     string
		data     []byte
		expected domain.CatalogVersion
	}{
		{name: "hash mismatch", data: newData, expected: domain.CatalogVersion{SHA256: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", Size: int64(len(newData))}},
		{name: "size mismatch", data: newData, expected: domain.CatalogVersion{SHA256: validVersion.SHA256, Size: validVersion.Size + 1}},
		{name: "invalid expected version", data: newData, expected: domain.CatalogVersion{SHA256: "invalid", Size: validVersion.Size}},
		{name: "invalid staged JSON", data: []byte(`{`), expected: repositoryVersion([]byte(`{`))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
			source := writeRepositoryFile(t, root, "source.json", tt.data, 0o600)
			before := repositoryDirectoryEntries(t, root)

			if _, err := newTestRepository(t, path).Replace(tt.expected, source); err == nil {
				t.Fatal("Replace() error = nil, want validation failure")
			}
			assertRepositoryFileBytes(t, path, oldData)
			assertRepositoryFileBytes(t, source, tt.data)
			if after := repositoryDirectoryEntries(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("directory entries after failed Replace = %v, want %v", after, before)
			}
		})
	}
}

func TestRepositoryReplaceRejectsStagedSymlinkAndHardlink(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, string) string
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, root string) string {
				target := writeRepositoryFile(t, root, "provider.json", newData, 0o600)
				source := filepath.Join(root, "source.json")
				if err := os.Symlink(target, source); err != nil {
					t.Fatal(err)
				}
				return source
			},
		},
		{
			name: "hardlink",
			setup: func(t *testing.T, root string) string {
				target := writeRepositoryFile(t, root, "provider.json", newData, 0o600)
				source := filepath.Join(root, "source.json")
				if err := os.Link(target, source); err != nil {
					t.Fatal(err)
				}
				return source
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
			source := tt.setup(t, root)
			before := repositoryDirectoryEntries(t, root)
			if _, err := newTestRepository(t, path).Replace(repositoryVersion(newData), source); err == nil {
				t.Fatal("Replace() error = nil, want staged-file rejection")
			}
			assertRepositoryFileBytes(t, path, oldData)
			if after := repositoryDirectoryEntries(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("directory entries after failed Replace = %v, want %v", after, before)
			}
		})
	}
}

func TestRepositoryReplacePublishesExactPrivateCatalogAndMarker(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path)

	snapshot, err := repository.Replace(repositoryVersion(newData), source)
	if err != nil {
		t.Fatal(err)
	}
	entries := snapshot.List(domain.PowerTypeSkill)
	if len(entries) == 0 || entries[0].Message != "replacement message" {
		t.Fatalf("Replace() snapshot skills = %#v, want replacement message", entries)
	}
	assertRepositoryFileBytes(t, path, newData)
	assertRepositoryFileBytes(t, source, newData)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("published catalog mode = %04o, want 0600", got)
	}
	loaded, version, err := repository.Load()
	if err != nil {
		t.Fatal(err)
	}
	if version != repositoryVersion(newData) {
		t.Fatalf("Load() version = %#v, want %#v", version, repositoryVersion(newData))
	}
	loadedEntries := loaded.List(domain.PowerTypeSkill)
	if len(loadedEntries) == 0 || loadedEntries[0].Message != "replacement message" {
		t.Fatalf("Load() snapshot skills = %#v, want replacement message", loadedEntries)
	}
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after success = %v, want none", got)
	}
}

func TestRepositoryReplacePublishesWhenDestinationIsAbsent(t *testing.T) {
	data := repositoryCatalogVariant(t, "first publication")
	root := trustedRepositoryDir(t)
	path := filepath.Join(root, "catalog.json")
	source := writeRepositoryFile(t, root, "source.json", data, 0o600)

	if _, err := newTestRepository(t, path).Replace(repositoryVersion(data), source); err != nil {
		t.Fatal(err)
	}
	assertRepositoryFileBytes(t, path, data)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after first publication = %v, want none", got)
	}
}

func TestRepositoryReplaceRejectsDestinationSubstitution(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	competitorData := repositoryCatalogVariant(t, "competing message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	competitor := writeRepositoryFile(t, root, "competitor.json", competitorData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalExchange := platform.ops.exchange
	attacked := false
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		if !attacked {
			attacked = true
			if err := os.Rename(competitor, path); err != nil {
				t.Fatal(err)
			}
		}
		return originalExchange(directoryFD, temporary, destination)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want destination substitution rejection")
	}
	assertRepositoryFileBytes(t, path, competitorData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after rejected substitution = %v, want none", got)
	}
}

func TestRepositoryReplaceRejectsCompetingFirstPublication(t *testing.T) {
	newData := repositoryCatalogVariant(t, "replacement message")
	competitorData := repositoryCatalogVariant(t, "competing message")
	root := trustedRepositoryDir(t)
	path := filepath.Join(root, "catalog.json")
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalNoReplace := platform.ops.renameNoReplace
	attacked := false
	platform.ops.renameNoReplace = func(directoryFD int, temporary, destination string) error {
		if !attacked {
			attacked = true
			writeRepositoryFile(t, root, "catalog.json", competitorData, 0o600)
		}
		return originalNoReplace(directoryFD, temporary, destination)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want competing publication rejection")
	}
	assertRepositoryFileBytes(t, path, competitorData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after competing publication = %v, want none", got)
	}
}

func TestRepositoryReplaceFailuresPreservePriorCatalogAndExactCleanup(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	injected := errors.New("injected repository failure")
	tests := []struct {
		name      string
		configure func(*unixRepositoryPlatform)
	}{
		{
			name: "file fsync",
			configure: func(platform *unixRepositoryPlatform) {
				platform.ops.syncFile = func(*os.File) error { return injected }
			},
		},
		{
			name: "atomic exchange",
			configure: func(platform *unixRepositoryPlatform) {
				platform.ops.exchange = func(int, string, string) error { return injected }
			},
		},
		{
			name: "parent fsync",
			configure: func(platform *unixRepositoryPlatform) {
				originalSync := platform.ops.syncDirectory
				calls := 0
				platform.ops.syncDirectory = func(file *os.File) error {
					calls++
					if calls == 1 {
						return injected
					}
					return originalSync(file)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
			source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
			unrelated := writeRepositoryFile(t, root, ".catalog-unrelated.tmp", []byte("unrelated"), 0o600)
			repository := newTestRepository(t, path).(*filesystemRepository)
			tt.configure(repository.platform.(*unixRepositoryPlatform))
			before := repositoryDirectoryEntries(t, root)

			if _, err := repository.Replace(repositoryVersion(newData), source); !errors.Is(err, injected) {
				t.Fatalf("Replace() error = %v, want injected failure", err)
			}
			assertRepositoryFileBytes(t, path, oldData)
			assertRepositoryFileBytes(t, source, newData)
			assertRepositoryFileBytes(t, unrelated, []byte("unrelated"))
			if after := repositoryDirectoryEntries(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("directory entries after failed Replace = %v, want exact %v", after, before)
			}
		})
	}
}

func TestRepositoryReplaceDoesNotRemoveSubstitutedOwnedTemp(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	substituteData := []byte("do not remove")
	var substitutedPath string
	platform.ops.syncFile = func(file *os.File) error {
		substitutedPath = file.Name()
		if err := os.Rename(substitutedPath, substitutedPath+".displaced"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(substitutedPath, substituteData, 0o600); err != nil {
			t.Fatal(err)
		}
		return errors.New("injected sync failure after substitution")
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want substituted-temp rejection")
	}
	assertRepositoryFileBytes(t, path, oldData)
	assertRepositoryFileBytes(t, substitutedPath, substituteData)
}

func TestRepositoryReplaceDoesNotCleanupHardlinkedOwnedTemp(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	var ownedPath string
	platform.ops.syncFile = func(file *os.File) error {
		ownedPath = file.Name()
		if err := os.Link(ownedPath, ownedPath+".linked"); err != nil {
			t.Fatal(err)
		}
		return errors.New("injected sync failure after hardlink")
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want hardlinked-temp rejection")
	}
	assertRepositoryFileBytes(t, path, oldData)
	if _, err := os.Lstat(ownedPath); err != nil {
		t.Fatalf("owned temp was removed despite link count change: %v", err)
	}
	if _, err := os.Lstat(ownedPath + ".linked"); err != nil {
		t.Fatalf("injected hardlink is absent: %v", err)
	}
}

func TestRepositoryReplaceDoesNotRemoveSubstituteDuringOwnedCreation(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalFstatat := platform.ops.fstatat
	substituteData := []byte("do not remove during create")
	var substitutedPath string
	platform.ops.fstatat = func(directoryFD int, name string, stat *unix.Stat_t, flags int) error {
		if substitutedPath == "" && strings.HasSuffix(name, ".owned") {
			substitutedPath = filepath.Join(root, name)
			if err := os.Rename(substitutedPath, substitutedPath+".displaced"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(substitutedPath, substituteData, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return originalFstatat(directoryFD, name, stat, flags)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want creation-time substitution rejection")
	}
	assertRepositoryFileBytes(t, path, oldData)
	assertRepositoryFileBytes(t, substitutedPath, substituteData)
}

func repositoryVersion(data []byte) domain.CatalogVersion {
	digest := sha256.Sum256(data)
	return domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

func repositoryCatalogVariant(t *testing.T, message string) []byte {
	t.Helper()
	return replaceJSON(t, validCatalogJSON(), `"message":"skill message"`, `"message":"`+message+`"`)
}

func repositoryDirectoryEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	sort.Strings(names)
	return names
}

func repositoryOwnedTemps(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".catalog-*.owned"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func assertRepositoryFileBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file %q bytes differ: got %d bytes, want %d", path, len(got), len(want))
	}
}

//go:build linux || darwin

package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
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

func TestRepositoryReplaceReconcilesFailedRollbackAsCommittedPublication(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalExchange := platform.ops.exchange
	exchangeCalls := 0
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			return errors.New("injected rollback exchange failure")
		}
		return originalExchange(directoryFD, temporary, destination)
	}
	originalSync := platform.ops.syncDirectory
	syncCalls := 0
	platform.ops.syncDirectory = func(file *os.File) error {
		syncCalls++
		if syncCalls == 1 {
			return errors.New("injected pre-commit directory sync failure")
		}
		return originalSync(file)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want reconciled committed success", err)
	}
	assertRepositoryFileBytes(t, path, newData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after reconciled commit = %v, want none", got)
	}
}

func TestRepositoryReplaceReconcilesFailedRollbackAndResyncAsCommittedPublication(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalExchange := platform.ops.exchange
	exchangeCalls := 0
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			return errors.New("injected rollback exchange failure")
		}
		return originalExchange(directoryFD, temporary, destination)
	}
	syncCalls := 0
	platform.ops.syncDirectory = func(*os.File) error {
		syncCalls++
		return errors.New("injected directory sync failure")
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want reconciled committed success", err)
	}
	if syncCalls < 2 {
		t.Fatalf("directory sync calls = %d, want publication and reconciliation attempts", syncCalls)
	}
	assertRepositoryFileBytes(t, path, newData)
}

func TestRepositoryReplaceRejectsOwnedBytesModifiedDuringExchange(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalExchange := platform.ops.exchange
	modified := false
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		if err := originalExchange(directoryFD, temporary, destination); err != nil {
			return err
		}
		if !modified {
			modified = true
			if err := os.WriteFile(path, []byte(`{"schema_version":1}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err == nil {
		t.Fatal("Replace() error = nil, want post-exchange bytes/hash/schema rejection")
	}
	assertRepositoryFileBytes(t, path, oldData)
}

func TestRepositoryReplaceReconcilesTransientVerificationAndFailedRollbackAsCommitted(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalExchange := platform.ops.exchange
	published := false
	exchangeCalls := 0
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			return errors.New("injected rollback exchange failure")
		}
		if err := originalExchange(directoryFD, temporary, destination); err != nil {
			return err
		}
		published = true
		return nil
	}
	originalReadBounded := platform.ops.readBounded
	verificationFailed := false
	platform.ops.readBounded = func(reader io.Reader, limit int64) ([]byte, error) {
		if published && !verificationFailed {
			verificationFailed = true
			return nil, errors.New("injected transient post-publication read failure")
		}
		return originalReadBounded(reader, limit)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want reconciled committed success", err)
	}
	assertRepositoryFileBytes(t, path, newData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after reconciled verification = %v, want none", got)
	}
}

func TestRepositoryReplaceRejectsDifferentBytesDespiteMatchingVersionAndFailedRollback(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "collision alpha")
	differentData := repositoryCatalogVariant(t, "collision bravo")
	if len(differentData) != len(newData) {
		t.Fatalf("collision fixture size = %d, want %d", len(differentData), len(newData))
	}
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	expected := repositoryVersion(newData)
	platform.ops.versionForBytes = func([]byte) domain.CatalogVersion { return expected }
	originalExchange := platform.ops.exchange
	exchangeCalls := 0
	platform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		exchangeCalls++
		if exchangeCalls == 2 {
			return errors.New("injected first rollback exchange failure")
		}
		if err := originalExchange(directoryFD, temporary, destination); err != nil {
			return err
		}
		if exchangeCalls == 1 {
			if err := os.WriteFile(path, differentData, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}

	if _, err := repository.Replace(expected, source); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("Replace() error = %v, want exact-byte ErrInvalidCatalog", err)
	}
	assertRepositoryFileBytes(t, path, oldData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after second rollback = %v, want none", got)
	}
}

func TestRepositoryLoadRejectsRootOwnedStickyFinalParent(t *testing.T) {
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", validCatalogJSON(), 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalFstat := platform.ops.fstat
	components := strings.Split(strings.TrimPrefix(filepath.Dir(path), string(filepath.Separator)), string(filepath.Separator))
	finalCall := len(components) + 1
	calls := 0
	platform.ops.fstat = func(fd int, stat *unix.Stat_t) error {
		calls++
		if err := originalFstat(fd, stat); err != nil {
			return err
		}
		if calls == finalCall {
			stat.Uid = 0
			stat.Mode = stat.Mode&^0o7777 | unix.S_ISVTX | 0o777
		}
		return nil
	}

	if _, _, err := repository.Load(); err == nil {
		t.Fatal("Load() error = nil, want root-owned sticky final parent rejected")
	}
}

func TestRepositoryReplaceCommittedCleanupPerformsSecondDirectorySync(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	originalSync := platform.ops.syncDirectory
	syncCalls := 0
	platform.ops.syncDirectory = func(file *os.File) error {
		syncCalls++
		if syncCalls == 2 {
			return errors.New("injected committed cleanup sync failure")
		}
		return originalSync(file)
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want committed success despite cleanup sync failure", err)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want publication and cleanup sync", syncCalls)
	}
	assertRepositoryFileBytes(t, path, newData)
	if got := repositoryOwnedTemps(t, root); len(got) != 0 {
		t.Fatalf("owned temporary entries after cleanup = %v, want none", got)
	}
}

func TestRepositoryReplaceSerializesTwoPublishersOnPinnedParent(t *testing.T) {
	oldData := validCatalogJSON()
	firstData := repositoryCatalogVariant(t, "first publisher")
	secondData := repositoryCatalogVariant(t, "second publisher")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	firstSource := writeRepositoryFile(t, root, "first.json", firstData, 0o600)
	secondSource := writeRepositoryFile(t, root, "second.json", secondData, 0o600)
	firstRepository := newTestRepository(t, path).(*filesystemRepository)
	secondRepository := newTestRepository(t, path).(*filesystemRepository)
	firstPlatform := firstRepository.platform.(*unixRepositoryPlatform)
	originalExchange := firstPlatform.ops.exchange
	firstAtExchange := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstPlatform.ops.exchange = func(directoryFD int, temporary, destination string) error {
		close(firstAtExchange)
		<-releaseFirst
		return originalExchange(directoryFD, temporary, destination)
	}
	type replaceResult struct {
		snapshot Snapshot
		err      error
	}
	firstResult := make(chan replaceResult, 1)
	secondResult := make(chan replaceResult, 1)
	go func() {
		snapshot, err := firstRepository.Replace(repositoryVersion(firstData), firstSource)
		firstResult <- replaceResult{snapshot: snapshot, err: err}
	}()
	<-firstAtExchange
	go func() {
		snapshot, err := secondRepository.Replace(repositoryVersion(secondData), secondSource)
		secondResult <- replaceResult{snapshot: snapshot, err: err}
	}()

	probeFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		close(releaseFirst)
		t.Fatal(err)
	}
	lockErr := unix.Flock(probeFD, unix.LOCK_EX|unix.LOCK_NB)
	if lockErr == nil {
		if err := unix.Flock(probeFD, unix.LOCK_UN); err != nil {
			t.Errorf("unlock probe directory: %v", err)
		}
	}
	if err := unix.Close(probeFD); err != nil {
		t.Errorf("close probe directory: %v", err)
	}
	close(releaseFirst)
	first := <-firstResult
	second := <-secondResult

	if !errors.Is(lockErr, unix.EWOULDBLOCK) && !errors.Is(lockErr, unix.EAGAIN) {
		t.Fatalf("nonblocking parent lock error = %v, want publisher-held lock", lockErr)
	}
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent Replace() errors = first %v, second %v", first.err, second.err)
	}
	assertRepositoryFileBytes(t, path, secondData)
}

func TestRepositoryReplaceIdentityCleanupDoesNotDeleteSubstitutedEntry(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	substituteData := []byte("identity cleanup substitute")
	var substitutedPath string
	platform.ops.beforeIdentityUnlink = func(_ int, name string) {
		if substitutedPath != "" || !strings.HasSuffix(name, ".owned") {
			return
		}
		substitutedPath = filepath.Join(root, name)
		if err := os.Rename(substitutedPath, substitutedPath+".displaced"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(substitutedPath, substituteData, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(substitutedPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want committed success with refused cleanup", err)
	}
	assertRepositoryFileBytes(t, path, newData)
	assertRepositoryFileBytes(t, substitutedPath, substituteData)
}

func TestRepositoryReplaceDoesNotDeleteSubstituteInjectedImmediatelyBeforeUnlink(t *testing.T) {
	oldData := validCatalogJSON()
	newData := repositoryCatalogVariant(t, "replacement message")
	root := trustedRepositoryDir(t)
	path := writeRepositoryFile(t, root, "catalog.json", oldData, 0o600)
	source := writeRepositoryFile(t, root, "source.json", newData, 0o600)
	repository := newTestRepository(t, path).(*filesystemRepository)
	platform := repository.platform.(*unixRepositoryPlatform)
	substituteData := []byte("final identity-check substitute")
	var substitutedPath string
	var preservedPath string
	platform.ops.beforeUnlinkat = func(_ int, name string) {
		if substitutedPath != "" || !strings.HasSuffix(name, ".owned") {
			return
		}
		substitutedPath = filepath.Join(root, name)
		preservedPath = substitutedPath + ".preserved"
		if err := os.Rename(substitutedPath, preservedPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(substitutedPath, substituteData, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := repository.Replace(repositoryVersion(newData), source); err != nil {
		t.Fatalf("Replace() error = %v, want committed success with fail-closed cleanup", err)
	}
	assertRepositoryFileBytes(t, path, newData)
	assertRepositoryFileBytes(t, substitutedPath, substituteData)
	assertRepositoryFileBytes(t, preservedPath, oldData)
}

func TestRepositoryCleanupTrustBoundaryContract(t *testing.T) {
	source, err := os.ReadFile("repository_unix.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"current EUID 是 pathname 操作的受信任主体",
		"advisory parent lock 只串行遵守 Repository 协议的发布者",
		"不承诺隔离恶意同 EUID 在最终 fstatat 到 unlinkat 窄窗的替换",
	} {
		if !strings.Contains(string(source), required) {
			t.Errorf("repository cleanup trust-boundary contract is missing %q", required)
		}
	}
}

func TestRepositoryIdentityCleanupTrustBoundary(t *testing.T) {
	t.Run("normal exact cleanup", func(t *testing.T) {
		root := trustedRepositoryDir(t)
		name := ".catalog-contract.owned"
		path := writeRepositoryFile(t, root, name, []byte("owned catalog"), 0o600)
		platform := newRepositoryPlatform().(*unixRepositoryPlatform)
		parent, err := platform.openParent(root)
		if err != nil {
			t.Fatal(err)
		}
		defer parent.close()
		stat, err := parent.statEntry(name)
		if err != nil {
			t.Fatal(err)
		}

		if err := parent.removeEntryIfIdentity(name, repositoryIdentity(stat)); err != nil {
			t.Fatalf("removeEntryIfIdentity() error = %v", err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("normal cleanup residue = %v, want absent", err)
		}
		if entries := repositoryDirectoryEntries(t, root); len(entries) != 0 {
			t.Fatalf("normal cleanup directory entries = %v, want no residue", entries)
		}
	})

	t.Run("last detectable substitution fails closed", func(t *testing.T) {
		root := trustedRepositoryDir(t)
		name := ".catalog-contract.owned"
		originalData := []byte("owned catalog")
		substituteData := []byte("substitute")
		path := writeRepositoryFile(t, root, name, originalData, 0o600)
		preservedPath := path + ".preserved"
		platform := newRepositoryPlatform().(*unixRepositoryPlatform)
		originalUnlinkat := platform.ops.unlinkat
		unlinkCalls := 0
		platform.ops.unlinkat = func(directoryFD int, entry string, flags int) error {
			unlinkCalls++
			return originalUnlinkat(directoryFD, entry, flags)
		}
		platform.ops.beforeUnlinkat = func(_ int, entry string) {
			if entry != name {
				t.Fatalf("beforeUnlinkat entry = %q, want %q", entry, name)
			}
			if err := os.Rename(path, preservedPath); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, substituteData, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		parent, err := platform.openParent(root)
		if err != nil {
			t.Fatal(err)
		}
		defer parent.close()
		stat, err := parent.statEntry(name)
		if err != nil {
			t.Fatal(err)
		}

		if err := parent.removeEntryIfIdentity(name, repositoryIdentity(stat)); !errors.Is(err, fs.ErrInvalid) {
			t.Fatalf("removeEntryIfIdentity() error = %v, want fail-closed fs.ErrInvalid", err)
		}
		if unlinkCalls != 0 {
			t.Fatalf("unlinkat calls after detectable substitution = %d, want zero", unlinkCalls)
		}
		assertRepositoryFileBytes(t, path, substituteData)
		assertRepositoryFileBytes(t, preservedPath, originalData)
		if entries := repositoryDirectoryEntries(t, root); !reflect.DeepEqual(entries, []string{name, name + ".preserved"}) {
			t.Fatalf("fail-closed cleanup entries = %v, want only substitute and preserved inode", entries)
		}
	})
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

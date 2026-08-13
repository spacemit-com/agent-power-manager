package transaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemJournalStoreLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	journal := validInstallJournal(t)

	if _, found, err := store.Inspect(); err != nil || found {
		t.Fatalf("Inspect() before Create = found %v, error %v", found, err)
	}
	if err := store.Create(journal); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Inspect()
	if err != nil || !found || got.TransactionID != journal.TransactionID || got.Phase != PhasePreparing {
		t.Fatalf("Inspect() = %+v, %v, %v", got, found, err)
	}

	journal.Phase = PhasePublishing
	if err := store.Update(journal); err != nil {
		t.Fatal(err)
	}
	got, found, err = store.Inspect()
	if err != nil || !found || got.Phase != PhasePublishing {
		t.Fatalf("Inspect() after Update = %+v, %v, %v", got, found, err)
	}
	if err := store.Delete(journal.TransactionID); err != nil {
		t.Fatal(err)
	}
	if _, found, err = store.Inspect(); err != nil || found {
		t.Fatalf("Inspect() after Delete = found %v, error %v", found, err)
	}
}

func TestFilesystemJournalStorePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	if err := store.Create(validInstallJournal(t)); err != nil {
		t.Fatal(err)
	}
	assertPermission(t, filepath.Dir(path), 0o700)
	assertPermission(t, path, 0o600)
}

func TestFilesystemJournalStoreRejectsInvalidDocuments(t *testing.T) {
	tests := map[string]string{
		"invalid JSON":   `{`,
		"trailing JSON":  validJournalJSON(t) + ` {}`,
		"unknown field":  strings.Replace(validJournalJSON(t), `"phase"`, `"unknown":true,"phase"`, 1),
		"unknown schema": strings.Replace(validJournalJSON(t), `"schema_version":1`, `"schema_version":3`, 1),
		"legacy flat":    legacyFlatJournalJSON(validInstallJournal(t)),
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "transactions", "active.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			before := []byte(contents)
			_, found, err := newFilesystemJournalStore(path).Inspect()
			if !found || !errors.Is(err, ErrInvalidJournal) {
				t.Fatalf("Inspect() = found %v, error %v; want invalid journal", found, err)
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(after, before) {
				t.Fatalf("invalid journal changed: %v, %q", readErr, after)
			}
		})
	}
}

func TestFilesystemJournalStoreProtectsActiveTransactionAndID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	journal := validInstallJournal(t)
	if err := store.Create(journal); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(journal); !errors.Is(err, ErrActiveTransaction) {
		t.Fatalf("second Create() error = %v, want ErrActiveTransaction", err)
	}
	other := journal
	other.TransactionID = "ffeeddccbbaa99887766554433221100"
	if err := store.Update(other); !errors.Is(err, ErrInvalidJournal) {
		t.Fatalf("Update(other ID) error = %v, want ErrInvalidJournal", err)
	}
	if err := store.Delete(other.TransactionID); !errors.Is(err, ErrInvalidJournal) {
		t.Fatalf("Delete(other ID) error = %v, want ErrInvalidJournal", err)
	}
}

func TestFilesystemJournalStoreRenameFailureCleansTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	renameErr := errors.New("rename failed")
	store.rename = func(string, string) error { return renameErr }

	if err := store.Create(validInstallJournal(t)); !errors.Is(err, renameErr) {
		t.Fatalf("Create() error = %v, want rename error", err)
	}
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".active-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary journals remain: %v", temporary)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active journal after rename failure: %v", err)
	}
}

func TestFilesystemJournalStoreWriteFailureCleansTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	writeErr := errors.New("write failed")
	store.write = func(*os.File, []byte) (int, error) { return 0, writeErr }

	if err := store.Create(validInstallJournal(t)); !errors.Is(err, writeErr) {
		t.Fatalf("Create() error = %v, want write error", err)
	}
	assertNoTemporaryJournals(t, path)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active journal after write failure: %v", err)
	}
}

func TestFilesystemJournalStoreShortWriteFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	store.write = func(_ *os.File, contents []byte) (int, error) { return len(contents) - 1, nil }

	if err := store.Create(validInstallJournal(t)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Create() error = %v, want io.ErrShortWrite", err)
	}
	assertNoTemporaryJournals(t, path)
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active journal after short write: %v", err)
	}
}

func TestFilesystemJournalStoreFileSyncFailureCleansTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	syncErr := errors.New("file sync failed")
	store.syncFile = func(*os.File) error { return syncErr }

	if err := store.Create(validInstallJournal(t)); !errors.Is(err, syncErr) {
		t.Fatalf("Create() error = %v, want sync error", err)
	}
	assertNoTemporaryJournals(t, path)
}

func TestFilesystemJournalStoreCreateDirectorySyncFailureLeavesInspectableJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	syncErr := errors.New("directory sync failed")
	store.syncDir = func(string) error { return syncErr }
	journal := validInstallJournal(t)

	if err := store.Create(journal); !errors.Is(err, syncErr) {
		t.Fatalf("Create() error = %v, want sync error", err)
	}
	got, found, err := store.Inspect()
	if err != nil || !found || got.TransactionID != journal.TransactionID {
		t.Fatalf("Inspect() = %+v, %v, %v", got, found, err)
	}
	assertNoTemporaryJournals(t, path)
}

func TestFilesystemJournalStoreRemoveFailurePreservesJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	journal := validInstallJournal(t)
	if err := store.Create(journal); err != nil {
		t.Fatal(err)
	}
	removeErr := errors.New("remove failed")
	store.remove = func(string) error { return removeErr }

	if err := store.Delete(journal.TransactionID); !errors.Is(err, removeErr) {
		t.Fatalf("Delete() error = %v, want remove error", err)
	}
	if _, found, err := store.Inspect(); err != nil || !found {
		t.Fatalf("Inspect() after failed remove = %v, %v", found, err)
	}
}

func TestFilesystemJournalStoreDeleteSyncFailureRestoresJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	journal := validInstallJournal(t)
	if err := store.Create(journal); err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("directory sync failed")
	failed := false
	store.syncDir = func(string) error {
		if !failed {
			failed = true
			return syncErr
		}
		return nil
	}

	if err := store.Delete(journal.TransactionID); !errors.Is(err, syncErr) {
		t.Fatalf("Delete() error = %v, want sync error", err)
	}
	got, found, err := store.Inspect()
	if err != nil || !found || got.TransactionID != journal.TransactionID {
		t.Fatalf("Inspect() after failed Delete = %+v, %v, %v", got, found, err)
	}
}

func TestFilesystemJournalStoreDeleteSyncAndRestoreFailureReturnsBoth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transactions", "active.json")
	store := newFilesystemJournalStore(path)
	journal := validInstallJournal(t)
	if err := store.Create(journal); err != nil {
		t.Fatal(err)
	}
	syncErr := errors.New("directory sync failed")
	store.syncDir = func(string) error { return syncErr }

	err := store.Delete(journal.TransactionID)
	if !errors.Is(err, syncErr) || !strings.Contains(err.Error(), "restore journal") {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, found, inspectErr := store.Inspect(); inspectErr != nil || !found {
		t.Fatalf("Inspect() after restore failure = %v, %v", found, inspectErr)
	}
}

func assertNoTemporaryJournals(t *testing.T, activePath string) {
	t.Helper()
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(activePath), ".active-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary journals remain: %v", temporary)
	}
}

func assertPermission(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("permission %q = %o, want %o", path, got, want)
	}
}

func validJournalJSON(t *testing.T) string {
	t.Helper()
	contents, err := json.Marshal(validInstallJournal(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

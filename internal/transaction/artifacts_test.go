package transaction

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestNewTransactionIDIsLowercaseHex128Bit(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	first, err := newTransactionID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTransactionID()
	if err != nil {
		t.Fatal(err)
	}
	if !pattern.MatchString(first) || !pattern.MatchString(second) || first == second {
		t.Fatalf("transaction IDs = %q, %q", first, second)
	}
}

func TestArtifactPathsStayBesideTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills", "demo")
	id := "00112233445566778899aabbccddeeff"
	got, err := artifactPaths(target, id)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(filepath.Dir(target), ".apm-transactions", id)
	if got.dir != wantDir || got.backup != filepath.Join(wantDir, "backup") || got.quarantine != filepath.Join(wantDir, "quarantine") {
		t.Fatalf("artifactPaths() = %+v", got)
	}
}

func TestEnsureTransactionDirCreatesPrivateDirectories(t *testing.T) {
	targetParent := filepath.Join(t.TempDir(), "skills")
	if err := os.Mkdir(targetParent, 0o755); err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactPaths(filepath.Join(targetParent, "demo"), "00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTransactionDir(artifacts); err != nil {
		t.Fatal(err)
	}
	assertPermission(t, filepath.Dir(artifacts.dir), 0o700)
	assertPermission(t, artifacts.dir, 0o700)
}

func TestEnsureTransactionDirRejectsSymlinkRoot(t *testing.T) {
	root := t.TempDir()
	targetParent := filepath.Join(root, "skills")
	if err := os.Mkdir(targetParent, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(targetParent, ".apm-transactions")); err != nil {
		t.Fatal(err)
	}
	artifacts, err := artifactPaths(filepath.Join(targetParent, "demo"), "00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureTransactionDir(artifacts); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ensureTransactionDir() error = %v, want ErrUnsafePath", err)
	}
}

func TestCleanupTransactionDirPreservesNonemptySharedRoot(t *testing.T) {
	journal := validInstallJournal(t)
	if err := os.MkdirAll(journal.TransactionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Dir(journal.TransactionDir)
	other := filepath.Join(shared, "ffeeddccbbaa99887766554433221100")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupTransactionDir(journal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other transaction removed: %v", err)
	}
	if _, err := os.Stat(journal.TransactionDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current transaction remains: %v", err)
	}
}

func TestCleanupCommittedStagingRemovesCanonicalDisjointRoot(t *testing.T) {
	journal := validInstallJournal(t)
	if err := os.MkdirAll(journal.TargetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(journal.StagingRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal.StagingRoot, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupCommittedStaging(journal); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(journal.StagingRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging remains: %v", err)
	}
}

func TestCleanupCommittedStagingRejectsPhysicalTargetOverlap(t *testing.T) {
	journal := validInstallJournal(t)
	journal.TargetPath = filepath.Join(journal.StagingRoot, "agent", "skills", "demo")
	dir := filepath.Join(filepath.Dir(journal.TargetPath), ".apm-transactions", journal.TransactionID)
	journal.TransactionDir = dir
	journal.BackupPath = filepath.Join(dir, "backup")
	if err := os.MkdirAll(journal.TargetPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(journal.StagingRoot, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cleanupCommittedStaging(journal); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("cleanupCommittedStaging() error = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("unsafe cleanup changed staging: %v", err)
	}
}

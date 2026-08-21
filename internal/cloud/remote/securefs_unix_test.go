//go:build linux || darwin || freebsd

package remote

import (
	"errors"
	"os"
	"testing"

	"apm/internal/cloud"
	"golang.org/x/sys/unix"
)

func TestCreateStagedCleansExclusiveEntryAfterFstatFailure(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	injected := errors.New("injected staged fstat failure")
	ops := defaultStagedContentCreateOps()
	ops.fstat = func(int, *unix.Stat_t) error {
		return injected
	}

	staged, file, err := parent.createStagedWithOps(".apm-content-", ops)
	if !errors.Is(err, injected) {
		t.Fatalf("createStagedWithOps() error = %v, want injected fstat failure", err)
	}
	if staged != nil || file != nil {
		t.Fatalf("createStagedWithOps() = %#v, %#v, want no returned ownership after failure", staged, file)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestCreateOwnedCleansExclusiveChildAfterOpenatFailure(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	injected := errors.New("injected owned child openat failure")
	ops := defaultOwnedDirectoryCreateOps()
	ops.openat = func(int, string, int, uint32) (int, error) {
		return -1, injected
	}

	owned, err := parent.createOwnedWithOps("apm-remote-mcp-", ops)
	if !errors.Is(err, injected) {
		t.Fatalf("createOwnedWithOps() error = %v, want injected openat failure", err)
	}
	if owned != nil {
		t.Fatalf("createOwnedWithOps() = %#v, want no returned ownership after failure", owned)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestCreateOwnedCleansExclusiveChildAfterFstatFailure(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	injected := errors.New("injected owned child fstat failure")
	ops := defaultOwnedDirectoryCreateOps()
	ops.fstat = func(int, *unix.Stat_t) error {
		return injected
	}

	owned, err := parent.createOwnedWithOps("apm-remote-mcp-", ops)
	if !errors.Is(err, injected) {
		t.Fatalf("createOwnedWithOps() error = %v, want injected fstat failure", err)
	}
	if owned != nil {
		t.Fatalf("createOwnedWithOps() = %#v, want no returned ownership after failure", owned)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestCreateOwnedNamedCleansExclusiveChildAfterSnapshotFailure(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	injected := errors.New("injected named child snapshot failure")
	ops := defaultOwnedDirectoryNamedCreateOps()
	ops.fstatat = func(int, string, *unix.Stat_t, int) error {
		return injected
	}
	unlinkat := ops.unlinkat
	unlinkCalls := 0
	ops.unlinkat = func(directoryFD int, name string, flags int) error {
		unlinkCalls++
		if directoryFD != int(parent.file.Fd()) || name != "child" || flags != unix.AT_REMOVEDIR {
			t.Fatalf("Unlinkat(%d, %q, %d), want pinned parent fd, exact child name, and AT_REMOVEDIR", directoryFD, name, flags)
		}
		return unlinkat(directoryFD, name, flags)
	}

	owned, err := parent.createOwnedNamedWithOps("child", 0o755, ops)
	if !errors.Is(err, injected) || !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("createOwnedNamedWithOps() error = %v, want injected failure classified unsafe", err)
	}
	if owned != nil {
		t.Fatalf("createOwnedNamedWithOps() = %#v, want no returned ownership after failure", owned)
	}
	if unlinkCalls != 1 {
		t.Fatalf("Unlinkat calls = %d, want 1", unlinkCalls)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestCreateOwnedRejectsChildWithoutExactPrivateMode(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	ops := defaultOwnedDirectoryCreateOps()
	mkdirat := ops.mkdirat
	ops.mkdirat = func(directoryFD int, name string, mode uint32) error {
		if err := mkdirat(directoryFD, name, mode); err != nil {
			return err
		}
		childFD, err := unix.Openat(directoryFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		chmodErr := unix.Fchmod(childFD, 0o755)
		return errors.Join(chmodErr, unix.Close(childFD))
	}

	owned, err := parent.createOwnedWithOps("apm-remote-mcp-", ops)
	if owned != nil {
		t.Cleanup(func() { _ = owned.close() })
	}
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("createOwnedWithOps() error = %v, want non-private mode ErrUnsafeContent", err)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestCreateOwnedRejectsChildNotOwnedByCurrentEUID(t *testing.T) {
	parentPath := trustedTestDir(t)
	parent := openTestPinnedDirectory(t, parentPath)
	ops := defaultOwnedDirectoryCreateOps()
	fstat := ops.fstat
	ops.fstat = func(fileFD int, stat *unix.Stat_t) error {
		if err := fstat(fileFD, stat); err != nil {
			return err
		}
		stat.Uid = uint32(os.Geteuid()) + 1
		return nil
	}

	owned, err := parent.createOwnedWithOps("apm-remote-mcp-", ops)
	if owned != nil {
		t.Cleanup(func() { _ = owned.close() })
	}
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("createOwnedWithOps() error = %v, want foreign owner ErrUnsafeContent", err)
	}
	assertDirectoryEmpty(t, parentPath)
}

func TestVerifyDirectoryTrustRejectsForeignOwner(t *testing.T) {
	stat := unix.Stat_t{
		Mode: unix.S_IFDIR | 0o700,
		Uid:  uint32(os.Geteuid()) + 1,
	}

	if _, err := verifyDirectoryTrust(stat, directoryTrustPolicy{}, false); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust() error = %v, want foreign parent owner ErrUnsafeContent", err)
	}
}

func TestOpenPinnedDirectoryAcceptsTestingTempDirBelowRootStickyAncestor(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := openPinnedDirectory(path)
	if err != nil {
		t.Fatalf("openPinnedDirectory(t.TempDir()) error = %v, want current-euid child below root-owned sticky ancestor accepted", err)
	}
	if err := directory.close(); err != nil {
		t.Fatalf("close pinned testing temp directory: %v", err)
	}
}

func TestVerifyDirectoryTrustRequiresCurrentOwnerBelowRootStickyDirectory(t *testing.T) {
	policy := directoryTrustPolicy{allowRootOwnedSticky: true}
	stickyRoot := unix.Stat_t{Mode: unix.S_IFDIR | unix.S_ISVTX | 0o777, Uid: 0}
	sticky, err := verifyDirectoryTrust(stickyRoot, policy, false)
	if err != nil || !sticky {
		t.Fatalf("verifyDirectoryTrust(root sticky) = %t, %v, want true, nil", sticky, err)
	}

	ownedChild := unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: uint32(os.Geteuid())}
	sticky, err = verifyDirectoryTrust(ownedChild, policy, true)
	if err != nil || sticky {
		t.Fatalf("verifyDirectoryTrust(current-euid child) = %t, %v, want false, nil", sticky, err)
	}

	foreignChild := ownedChild
	foreignChild.Uid++
	if _, err := verifyDirectoryTrust(foreignChild, policy, true); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust(foreign child) error = %v, want ErrUnsafeContent", err)
	}
}

func TestVerifyDirectoryTrustRejectsNonStickyWritableDirectory(t *testing.T) {
	stat := unix.Stat_t{Mode: unix.S_IFDIR | 0o777, Uid: 0}
	if _, err := verifyDirectoryTrust(stat, directoryTrustPolicy{allowRootOwnedSticky: true}, false); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust(non-sticky writable root) error = %v, want ErrUnsafeContent", err)
	}
}

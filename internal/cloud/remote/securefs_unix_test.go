//go:build linux || darwin || freebsd

package remote

import (
	"errors"
	"os"
	"path/filepath"
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

	if _, _, err := verifyDirectoryTrust(stat, directoryTrustPolicy{}, false); !errors.Is(err, cloud.ErrUnsafeContent) {
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
	sticky, groupWritable, err := verifyDirectoryTrust(stickyRoot, policy, false)
	if err != nil || !sticky || groupWritable {
		t.Fatalf("verifyDirectoryTrust(root sticky) = %t, %t, %v, want true, false, nil", sticky, groupWritable, err)
	}

	ownedChild := unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: uint32(os.Geteuid())}
	sticky, groupWritable, err = verifyDirectoryTrust(ownedChild, policy, true)
	if err != nil || sticky || groupWritable {
		t.Fatalf("verifyDirectoryTrust(current-euid child) = %t, %t, %v, want false, false, nil", sticky, groupWritable, err)
	}
	// 组可写目录按 ADR-032 放行并上报，由调用方输出一次告警。
	writableOwnedChild := ownedChild
	writableOwnedChild.Mode = unix.S_IFDIR | 0o770
	sticky, groupWritable, err = verifyDirectoryTrust(writableOwnedChild, policy, true)
	if err != nil || sticky || !groupWritable {
		t.Fatalf("verifyDirectoryTrust(group-writable current-euid child) = %t, %t, %v, want false, true, nil", sticky, groupWritable, err)
	}

	foreignChild := ownedChild
	foreignChild.Uid++
	if _, _, err := verifyDirectoryTrust(foreignChild, policy, true); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust(foreign child) error = %v, want ErrUnsafeContent", err)
	}
}

func TestVerifyDirectoryTrustRejectsNonStickyWritableDirectory(t *testing.T) {
	stat := unix.Stat_t{Mode: unix.S_IFDIR | 0o777, Uid: 0}
	if _, _, err := verifyDirectoryTrust(stat, directoryTrustPolicy{allowRootOwnedSticky: true}, false); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust(non-sticky writable root) error = %v, want ErrUnsafeContent", err)
	}
}

// TestVerifyDirectoryTrustModePolicy 固定 ADR-032 的 mode 判定矩阵：仅 other-writable
// 构成拒绝条件，group-writable 放行并上报。
func TestVerifyDirectoryTrustModePolicy(t *testing.T) {
	euid := uint32(os.Geteuid())
	policy := directoryTrustPolicy{allowRootOwnedSticky: true}
	directoryMode := func(mode uint32, owner uint32) unix.Stat_t {
		return unix.Stat_t{Mode: unix.S_IFDIR | mode, Uid: owner}
	}
	tests := []struct {
		name              string
		stat              unix.Stat_t
		wantSticky        bool
		wantGroupWritable bool
		wantUnsafe        bool
	}{
		{name: "current euid private", stat: directoryMode(0o700, euid)},
		{name: "current euid world readable", stat: directoryMode(0o755, euid)},
		{name: "current euid group writable", stat: directoryMode(0o775, euid), wantGroupWritable: true},
		{name: "current euid group only writable", stat: directoryMode(0o770, euid), wantGroupWritable: true},
		{name: "current euid group writable setgid", stat: directoryMode(0o2775, euid), wantGroupWritable: true},
		{name: "current euid other writable", stat: directoryMode(0o777, euid), wantUnsafe: true},
		{name: "current euid other only writable", stat: directoryMode(0o702, euid), wantUnsafe: true},
		{name: "current euid sticky group writable", stat: directoryMode(0o775|unix.S_ISVTX, euid), wantGroupWritable: true},
		{name: "root private", stat: directoryMode(0o700, 0)},
		{name: "root group writable", stat: directoryMode(0o775, 0), wantGroupWritable: true},
		{name: "root other writable", stat: directoryMode(0o777, 0), wantUnsafe: true},
		{name: "root sticky other writable", stat: directoryMode(0o777|unix.S_ISVTX, 0), wantSticky: true},
		{name: "root sticky group writable", stat: directoryMode(0o775|unix.S_ISVTX, 0), wantSticky: true},
		{name: "root sticky without write bits", stat: directoryMode(0o750|unix.S_ISVTX, 0)},
		{name: "foreign owner", stat: directoryMode(0o700, euid+1), wantUnsafe: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sticky, groupWritable, err := verifyDirectoryTrust(tt.stat, policy, false)
			if tt.wantUnsafe {
				if !errors.Is(err, cloud.ErrUnsafeContent) {
					t.Fatalf("verifyDirectoryTrust() error = %v, want ErrUnsafeContent", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("verifyDirectoryTrust() error = %v, want acceptance", err)
			}
			if sticky != tt.wantSticky || groupWritable != tt.wantGroupWritable {
				t.Fatalf("verifyDirectoryTrust() = %t, %t, want %t, %t", sticky, groupWritable, tt.wantSticky, tt.wantGroupWritable)
			}
		})
	}
}

// TestVerifyDirectoryTrustStickyPolicy 校验 root-owned sticky 例外只在 policy 允许时生效：
// openOptionalEmpty 传入零策略，因此既存目标目录本身不得依赖该例外。
func TestVerifyDirectoryTrustStickyPolicy(t *testing.T) {
	stickyWritable := unix.Stat_t{Mode: unix.S_IFDIR | unix.S_ISVTX | 0o777, Uid: 0}
	if _, _, err := verifyDirectoryTrust(stickyWritable, directoryTrustPolicy{}, false); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("verifyDirectoryTrust(sticky without policy) error = %v, want ErrUnsafeContent", err)
	}

	stickyGroupWritable := unix.Stat_t{Mode: unix.S_IFDIR | unix.S_ISVTX | 0o775, Uid: 0}
	sticky, groupWritable, err := verifyDirectoryTrust(stickyGroupWritable, directoryTrustPolicy{}, false)
	if err != nil || sticky || !groupWritable {
		t.Fatalf("verifyDirectoryTrust(group-writable sticky without policy) = %t, %t, %v, want false, true, nil", sticky, groupWritable, err)
	}
}

// TestOpenPinnedDirectoryAcceptsGroupWritableAncestor 校验 0775 祖先目录不再阻断固定，
// 同时 other-writable 目录仍 fail closed。
func TestOpenPinnedDirectoryAcceptsGroupWritableAncestor(t *testing.T) {
	parent := trustedTestDir(t)

	groupWritable := filepath.Join(parent, "cache")
	if err := os.Mkdir(groupWritable, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(groupWritable, 0o775); err != nil {
		t.Fatal(err)
	}
	directory, err := openPinnedDirectory(groupWritable)
	if err != nil {
		t.Fatalf("openPinnedDirectory(0775 ancestor) error = %v, want group-writable directory accepted", err)
	}
	if err := directory.close(); err != nil {
		t.Fatalf("close pinned group-writable directory: %v", err)
	}

	otherWritable := filepath.Join(parent, "world")
	if err := os.Mkdir(otherWritable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(otherWritable, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := openPinnedDirectory(otherWritable); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("openPinnedDirectory(0777 ancestor) error = %v, want ErrUnsafeContent", err)
	}
}

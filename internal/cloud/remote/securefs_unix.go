//go:build linux || darwin || freebsd

package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/cloud"
	"golang.org/x/sys/unix"
)

var errSecureDestinationExists = unix.EEXIST

type fileIdentity struct {
	device uint64
	inode  uint64
}

type pinnedDirectory struct {
	file *os.File
	path string
}

type stagedContent struct {
	directory *pinnedDirectory
	name      string
	identity  fileIdentity
}

type stagedContentCreateOps struct {
	openat   func(int, string, int, uint32) (int, error)
	fstat    func(int, *unix.Stat_t) error
	unlinkat func(int, string, int) error
}

func defaultStagedContentCreateOps() stagedContentCreateOps {
	return stagedContentCreateOps{
		openat:   unix.Openat,
		fstat:    unix.Fstat,
		unlinkat: unix.Unlinkat,
	}
}

type ownedDirectory struct {
	parent    *pinnedDirectory
	directory *pinnedDirectory
	name      string
	identity  fileIdentity
}

type ownedDirectoryCreateOps struct {
	mkdirat  func(int, string, uint32) error
	openat   func(int, string, int, uint32) (int, error)
	fstat    func(int, *unix.Stat_t) error
	fstatat  func(int, string, *unix.Stat_t, int) error
	unlinkat func(int, string, int) error
}

func defaultOwnedDirectoryCreateOps() ownedDirectoryCreateOps {
	return ownedDirectoryCreateOps{
		mkdirat:  unix.Mkdirat,
		openat:   unix.Openat,
		fstat:    unix.Fstat,
		fstatat:  unix.Fstatat,
		unlinkat: unix.Unlinkat,
	}
}

type directoryTrustPolicy struct {
	allowRootSticky bool
}

// openPinnedDirectory 从文件系统根开始逐级固定现有物理目录，每一级都拒绝符号链接。
func openPinnedDirectory(path string) (*pinnedDirectory, error) {
	return openPinnedDirectoryWithPolicy(path, directoryTrustPolicy{})
}

// openSystemTemporaryDirectory 只供已规范化的生产系统临时根使用；root sticky 目录
// 仅可承载当前 euid 直接拥有的安全条目，注入路径不得使用这一例外。
func openSystemTemporaryDirectory(path string) (*pinnedDirectory, error) {
	return openPinnedDirectoryWithPolicy(path, directoryTrustPolicy{allowRootSticky: true})
}

func openPinnedDirectoryWithPolicy(path string, policy directoryTrustPolicy) (*pinnedDirectory, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, unsafeRemoteFilesystem("remote directory %q must be an absolute clean path", path)
	}

	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY
	fd, err := unix.Open(string(filepath.Separator), flags, 0)
	if err != nil {
		return nil, fmt.Errorf("open remote filesystem root for %q: %w", path, errors.Join(cloud.ErrUnsafeContent, err))
	}
	currentPath := string(filepath.Separator)
	current := os.NewFile(uintptr(fd), currentPath)
	requireCurrentOwner := false
	verify := func(file *os.File, displayPath string) (bool, error) {
		stat, statErr := statUnixFile(int(file.Fd()))
		if statErr != nil || unixNodeType(stat) != unix.S_IFDIR {
			if statErr == nil {
				statErr = fs.ErrInvalid
			}
			return false, fmt.Errorf("verify pinned remote directory %q: %w", displayPath, errors.Join(cloud.ErrUnsafeContent, statErr))
		}
		sticky, trustErr := verifyDirectoryTrust(stat, policy, requireCurrentOwner)
		if trustErr != nil {
			return false, fmt.Errorf("verify pinned remote directory %q: %w", displayPath, trustErr)
		}
		return sticky, nil
	}

	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		components = nil
	}
	sticky, err := verify(current, currentPath)
	if err != nil {
		return nil, errors.Join(err, current.Close())
	}
	requireCurrentOwner = sticky

	for _, component := range components {
		nextFD, openErr := unix.Openat(int(current.Fd()), component, flags, 0)
		if openErr != nil {
			return nil, errors.Join(
				fmt.Errorf("open physical remote directory component %q below %q: %w", component, currentPath, errors.Join(cloud.ErrUnsafeContent, openErr)),
				current.Close(),
			)
		}
		nextPath := filepath.Join(currentPath, component)
		next := os.NewFile(uintptr(nextFD), nextPath)
		sticky, verifyErr := verify(next, nextPath)
		closeCurrentErr := current.Close()
		if verifyErr != nil || closeCurrentErr != nil {
			return nil, errors.Join(verifyErr, closeCurrentErr, next.Close())
		}
		current = next
		currentPath = nextPath
		requireCurrentOwner = sticky
	}
	return &pinnedDirectory{file: current, path: path}, nil
}

// verifyDirectoryTrust 建立所有名称型 *at 操作的父目录信任不变量：OS/root 与当前
// 进程受信，目录必须由 root 或当前 euid 拥有且不可被其他主体写入。生产系统临时根
// 可使用 root-owned sticky 语义保护当前 euid 的直接条目；同一 euid 主动篡改不在
// 本隔离边界内，后续 device/inode 复验仅用于发现损坏，不能替代该信任不变量。
func verifyDirectoryTrust(stat unix.Stat_t, policy directoryTrustPolicy, requireCurrentOwner bool) (bool, error) {
	euid := uint32(os.Geteuid())
	owner := stat.Uid
	if owner != 0 && owner != euid {
		return false, unsafeRemoteFilesystem("remote directory owner uid %d is not trusted", owner)
	}
	if requireCurrentOwner && owner != euid {
		return false, unsafeRemoteFilesystem("entry directly below root-owned sticky directory must be owned by current euid %d", euid)
	}

	permissions := uint32(stat.Mode) & 0o777
	if permissions&0o022 == 0 {
		return false, nil
	}
	rootSticky := policy.allowRootSticky && owner == 0 && uint32(stat.Mode)&uint32(unix.S_ISVTX) != 0
	if !rootSticky {
		return false, unsafeRemoteFilesystem("remote directory mode %04o permits untrusted entry mutation", permissions)
	}
	return true, nil
}

func (directory *pinnedDirectory) close() error {
	if directory == nil || directory.file == nil {
		return nil
	}
	err := directory.file.Close()
	directory.file = nil
	return err
}

func (directory *pinnedDirectory) requireAbsent(name string) error {
	if directory == nil || directory.file == nil || !validSecureEntryName(name) {
		return unsafeRemoteFilesystem("invalid remote destination entry %q", name)
	}
	_, err := directory.statEntry(name)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect remote destination entry %q: %w", name, errors.Join(cloud.ErrUnsafeContent, err))
	}
	return fmt.Errorf("remote destination entry %q already exists: %w", name, cloud.ErrUnsafeContent)
}

func (directory *pinnedDirectory) createStaged(prefix string) (*stagedContent, *os.File, error) {
	return directory.createStagedWithOps(prefix, defaultStagedContentCreateOps())
}

func (directory *pinnedDirectory) createStagedWithOps(prefix string, ops stagedContentCreateOps) (*stagedContent, *os.File, error) {
	for attempt := 0; attempt < secureNameAttempts; attempt++ {
		name, err := randomSecureName(prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("generate remote staged content name: %w", err)
		}
		fd, err := ops.openat(
			int(directory.file.Fd()),
			name,
			unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
			0o600,
		)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("create staged remote content in %q: %w", directory.path, err)
		}
		file := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
		var stat unix.Stat_t
		statErr := ops.fstat(fd, &stat)
		if statErr != nil || unixNodeType(stat) != unix.S_IFREG || uint64(stat.Nlink) != 1 {
			closeErr := file.Close()
			cleanupErr := ops.unlinkat(int(directory.file.Fd()), name, 0)
			if errors.Is(cleanupErr, unix.ENOENT) {
				cleanupErr = nil
			}
			if statErr == nil {
				statErr = fs.ErrInvalid
			}
			return nil, nil, fmt.Errorf("verify staged remote content %q: %w", name, errors.Join(cloud.ErrUnsafeContent, statErr, closeErr, cleanupErr))
		}
		return &stagedContent{directory: directory, name: name, identity: unixIdentity(stat)}, file, nil
	}
	return nil, nil, fmt.Errorf("create unique staged remote content: %w", fs.ErrExist)
}

func (directory *pinnedDirectory) createOwned(prefix string) (*ownedDirectory, error) {
	return directory.createOwnedWithOps(prefix, defaultOwnedDirectoryCreateOps())
}

func (directory *pinnedDirectory) createOwnedWithOps(prefix string, ops ownedDirectoryCreateOps) (*ownedDirectory, error) {
	for attempt := 0; attempt < secureNameAttempts; attempt++ {
		name, err := randomSecureName(prefix)
		if err != nil {
			return nil, err
		}
		if err := ops.mkdirat(int(directory.file.Fd()), name, 0o700); errors.Is(err, unix.EEXIST) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("create owned remote temporary directory in %q: %w", directory.path, err)
		}
		fd, openErr := ops.openat(
			int(directory.file.Fd()),
			name,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
			0,
		)
		if openErr != nil {
			cleanupErr := ops.unlinkat(int(directory.file.Fd()), name, unix.AT_REMOVEDIR)
			if errors.Is(cleanupErr, unix.ENOENT) {
				cleanupErr = nil
			}
			return nil, errors.Join(openErr, cleanupErr)
		}
		childFile := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
		var stat unix.Stat_t
		statErr := ops.fstat(fd, &stat)
		var entryStat unix.Stat_t
		entryErr := ops.fstatat(int(directory.file.Fd()), name, &entryStat, unix.AT_SYMLINK_NOFOLLOW)
		if statErr != nil || entryErr != nil || !validOwnedDirectoryStat(stat) || unixIdentity(stat) != unixIdentity(entryStat) {
			closeErr := childFile.Close()
			cleanupErr := ops.unlinkat(int(directory.file.Fd()), name, unix.AT_REMOVEDIR)
			if errors.Is(cleanupErr, unix.ENOENT) {
				cleanupErr = nil
			}
			if statErr == nil && entryErr == nil {
				statErr = fs.ErrInvalid
			}
			return nil, errors.Join(cloud.ErrUnsafeContent, statErr, entryErr, closeErr, cleanupErr)
		}
		child := &pinnedDirectory{file: childFile, path: filepath.Join(directory.path, name)}
		return &ownedDirectory{parent: directory, directory: child, name: name, identity: unixIdentity(stat)}, nil
	}
	return nil, fmt.Errorf("create unique owned remote temporary directory: %w", fs.ErrExist)
}

func validOwnedDirectoryStat(stat unix.Stat_t) bool {
	return unixNodeType(stat) == unix.S_IFDIR &&
		stat.Uid == uint32(os.Geteuid()) &&
		uint32(stat.Mode)&0o7777 == 0o700
}

func (directory *pinnedDirectory) statEntry(name string) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(int(directory.file.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	return stat, err
}

func (staged *stagedContent) openRead() (*os.File, error) {
	if staged == nil || staged.directory == nil || staged.directory.file == nil {
		return nil, unsafeRemoteFilesystem("staged remote content is closed")
	}
	fd, err := unix.Openat(
		int(staged.directory.file.Fd()),
		staged.name,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open staged remote content %q: %w", staged.name, errors.Join(cloud.ErrUnsafeContent, err))
	}
	file := os.NewFile(uintptr(fd), filepath.Join(staged.directory.path, staged.name))
	stat, statErr := statUnixFile(fd)
	if statErr != nil || unixNodeType(stat) != unix.S_IFREG || unixIdentity(stat) != staged.identity {
		closeErr := file.Close()
		if statErr == nil {
			statErr = fs.ErrInvalid
		}
		return nil, fmt.Errorf("verify staged remote content %q: %w", staged.name, errors.Join(cloud.ErrUnsafeContent, statErr, closeErr))
	}
	return file, nil
}

func (staged *stagedContent) prepareForClose(file *os.File, mode fs.FileMode) error {
	if staged == nil || file == nil {
		return unsafeRemoteFilesystem("staged remote content descriptor is unavailable")
	}
	if err := file.Chmod(mode.Perm()); err != nil {
		return fmt.Errorf("set staged remote content mode: %w", err)
	}
	stat, err := statUnixFile(int(file.Fd()))
	if err != nil || unixNodeType(stat) != unix.S_IFREG || unixIdentity(stat) != staged.identity || uint64(stat.Nlink) != 1 || fs.FileMode(stat.Mode&0o777) != mode.Perm() {
		if err == nil {
			err = fs.ErrInvalid
		}
		return fmt.Errorf("verify staged remote content descriptor before close: %w", errors.Join(cloud.ErrUnsafeContent, err))
	}
	return nil
}

func (staged *stagedContent) remove() error {
	if staged == nil || staged.directory == nil || staged.directory.file == nil || staged.name == "" {
		return nil
	}
	stat, err := staged.directory.statEntry(staged.name)
	if errors.Is(err, unix.ENOENT) {
		staged.name = ""
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect staged remote cleanup entry %q: %w", staged.name, errors.Join(cloud.ErrUnsafeContent, err))
	}
	if unixNodeType(stat) != unix.S_IFREG || unixIdentity(stat) != staged.identity {
		return unsafeRemoteFilesystem("refuse cleanup of substituted staged remote entry %q", staged.name)
	}
	if err := unix.Unlinkat(int(staged.directory.file.Fd()), staged.name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove staged remote content %q: %w", staged.name, err)
	}
	staged.name = ""
	return nil
}

func (staged *stagedContent) publishNoReplace(destination string) error {
	if !validSecureEntryName(destination) {
		return unsafeRemoteFilesystem("invalid remote Agent destination entry %q", destination)
	}
	stat, err := staged.directory.statEntry(staged.name)
	if err != nil || unixNodeType(stat) != unix.S_IFREG || unixIdentity(stat) != staged.identity {
		if err == nil {
			err = fs.ErrInvalid
		}
		return fmt.Errorf("verify staged remote Agent before publication: %w", errors.Join(cloud.ErrUnsafeContent, err))
	}
	if err := publishEntryNoReplace(int(staged.directory.file.Fd()), staged.name, destination); err != nil {
		return classifyNoReplaceError(destination, err)
	}
	staged.name = ""
	return nil
}

func (owned *ownedDirectory) close() error {
	if owned == nil {
		return nil
	}
	var resultErr error
	if owned.directory != nil {
		resultErr = errors.Join(resultErr, owned.directory.close())
		owned.directory = nil
	}
	if owned.parent == nil || owned.parent.file == nil || owned.name == "" {
		return resultErr
	}
	stat, err := owned.parent.statEntry(owned.name)
	if errors.Is(err, unix.ENOENT) {
		return errors.Join(resultErr, unsafeRemoteFilesystem("owned remote temporary directory %q was renamed before cleanup", owned.name))
	}
	if err != nil {
		return errors.Join(resultErr, fmt.Errorf("inspect owned remote temporary directory %q: %w", owned.name, errors.Join(cloud.ErrUnsafeContent, err)))
	}
	if unixNodeType(stat) != unix.S_IFDIR || unixIdentity(stat) != owned.identity {
		return errors.Join(resultErr, unsafeRemoteFilesystem("refuse cleanup of substituted owned remote directory %q", owned.name))
	}
	if err := unix.Unlinkat(int(owned.parent.file.Fd()), owned.name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return errors.Join(resultErr, fmt.Errorf("remove owned remote temporary directory %q: %w", owned.name, err))
	}
	owned.name = ""
	return resultErr
}

func statUnixFile(fd int) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := unix.Fstat(fd, &stat)
	return stat, err
}

func unixIdentity(stat unix.Stat_t) fileIdentity {
	return fileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}
}

func unixNodeType(stat unix.Stat_t) uint32 {
	return uint32(stat.Mode) & uint32(unix.S_IFMT)
}

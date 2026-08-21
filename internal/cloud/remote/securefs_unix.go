//go:build linux || darwin || freebsd

package remote

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

type ownedDirectory struct {
	parent    *pinnedDirectory
	directory *pinnedDirectory
	name      string
	identity  fileIdentity
}

// openPinnedDirectory 用一次内核路径解析固定现有目录；祖先别名可被解析，
// 但终端目录必须不是符号链接，后续操作只通过保留的目录描述符进行。
func openPinnedDirectory(path string) (*pinnedDirectory, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, unsafeRemoteFilesystem("remote directory %q must be an absolute clean path", path)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("open pinned remote directory %q: %w", path, errors.Join(cloud.ErrUnsafeContent, err))
	}
	file := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || unixNodeType(stat) != unix.S_IFDIR {
		closeErr := file.Close()
		if err == nil {
			err = fs.ErrInvalid
		}
		return nil, fmt.Errorf("verify pinned remote directory %q: %w", path, errors.Join(cloud.ErrUnsafeContent, err, closeErr))
	}
	return &pinnedDirectory{file: file, path: path}, nil
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
	for attempt := 0; attempt < secureNameAttempts; attempt++ {
		name, err := randomSecureName(prefix)
		if err != nil {
			return nil, nil, fmt.Errorf("generate remote staged content name: %w", err)
		}
		fd, err := unix.Openat(
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
		stat, statErr := statUnixFile(fd)
		if statErr != nil || unixNodeType(stat) != unix.S_IFREG || uint64(stat.Nlink) != 1 {
			closeErr := file.Close()
			var cleanupErr error
			if statErr == nil && unixNodeType(stat) == unix.S_IFREG {
				cleanupErr = (&stagedContent{directory: directory, name: name, identity: unixIdentity(stat)}).remove()
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
	for attempt := 0; attempt < secureNameAttempts; attempt++ {
		name, err := randomSecureName(prefix)
		if err != nil {
			return nil, err
		}
		if err := unix.Mkdirat(int(directory.file.Fd()), name, 0o700); errors.Is(err, unix.EEXIST) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("create owned remote temporary directory in %q: %w", directory.path, err)
		}
		fd, openErr := unix.Openat(
			int(directory.file.Fd()),
			name,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
			0,
		)
		if openErr != nil {
			// 没有固定并确认新目录身份时不按路径回滚，避免删除并发替换项。
			return nil, openErr
		}
		childFile := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
		stat, statErr := statUnixFile(fd)
		entryStat, entryErr := directory.statEntry(name)
		if statErr != nil || entryErr != nil || unixNodeType(stat) != unix.S_IFDIR || unixIdentity(stat) != unixIdentity(entryStat) {
			var cleanupErr error
			if statErr == nil && unixNodeType(stat) == unix.S_IFDIR {
				candidate := &ownedDirectory{
					parent:    directory,
					directory: &pinnedDirectory{file: childFile, path: filepath.Join(directory.path, name)},
					name:      name,
					identity:  unixIdentity(stat),
				}
				cleanupErr = candidate.close()
				childFile = nil
			}
			if childFile != nil {
				cleanupErr = errors.Join(cleanupErr, childFile.Close())
			}
			if statErr == nil && entryErr == nil {
				statErr = fs.ErrInvalid
			}
			return nil, errors.Join(cloud.ErrUnsafeContent, statErr, entryErr, cleanupErr)
		}
		child := &pinnedDirectory{file: childFile, path: filepath.Join(directory.path, name)}
		return &ownedDirectory{parent: directory, directory: child, name: name, identity: unixIdentity(stat)}, nil
	}
	return nil, fmt.Errorf("create unique owned remote temporary directory: %w", fs.ErrExist)
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

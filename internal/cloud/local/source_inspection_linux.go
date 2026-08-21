//go:build linux

package local

import (
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func openInspectionAt(directory *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func openInspectedDirectory(inspection *os.File) (*os.File, error) {
	return reopenLinuxInspection(inspection, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOCTTY)
}

func openInspectedRegular(inspection *os.File) (*os.File, error) {
	return reopenLinuxInspection(inspection, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOCTTY)
}

func reopenLinuxInspection(inspection *os.File, flags int) (*os.File, error) {
	procFD, err := unix.Open("/proc/self/fd", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(procFD)
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(procFD, &filesystem); err != nil {
		return nil, err
	}
	if uint64(filesystem.Type) != uint64(unix.PROC_SUPER_MAGIC) {
		return nil, fmt.Errorf("/proc/self/fd is not procfs: %w", fs.ErrInvalid)
	}
	fd, err := unix.Openat(procFD, strconv.FormatUint(uint64(inspection.Fd()), 10), flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), inspection.Name()), nil
}

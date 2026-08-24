//go:build linux || darwin || freebsd

package submission

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func openPhysicalRegularFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func sourceFileLinkCount(_ *os.File, info os.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, fmt.Errorf("Unix file metadata does not expose Stat_t")
	}
	return uint64(stat.Nlink), nil
}

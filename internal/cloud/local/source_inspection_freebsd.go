//go:build freebsd

package local

import (
	"os"

	"golang.org/x/sys/unix"
)

// FreeBSD 14+ open(2): O_PATH pins a vnode without opening it for I/O, and
// O_EMPTY_PATH upgrades that exact descriptor without another pathname lookup.
const (
	freeBSDOpenEmptyPath = 0x02000000
	freeBSDOpenPath      = 0x00400000
)

func openInspectionAt(directory *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, freeBSDOpenPath|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func openInspectedDirectory(inspection *os.File) (*os.File, error) {
	return reopenFreeBSDInspection(inspection, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOCTTY)
}

func openInspectedRegular(inspection *os.File) (*os.File, error) {
	return reopenFreeBSDInspection(inspection, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|unix.O_NOCTTY)
}

func reopenFreeBSDInspection(inspection *os.File, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(inspection.Fd()), "", flags|freeBSDOpenEmptyPath, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), inspection.Name()), nil
}

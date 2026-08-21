//go:build linux

package remote

import (
	"errors"

	"golang.org/x/sys/unix"
)

func publishEntryNoReplace(directoryFD int, source, destination string) error {
	err := unix.Renameat2(directoryFD, source, directoryFD, destination, unix.RENAME_NOREPLACE)
	if err == nil || errors.Is(err, unix.EEXIST) {
		return err
	}
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	return publishEntryByLink(directoryFD, source, destination)
}

func publishDirectoryNoReplace(directoryFD int, source, destination string) error {
	return unix.Renameat2(directoryFD, source, directoryFD, destination, unix.RENAME_NOREPLACE)
}

func publishEntryByLink(directoryFD int, source, destination string) error {
	return linkThenUnlinkNoReplace(
		source,
		destination,
		func(source, destination string) error {
			return unix.Linkat(directoryFD, source, directoryFD, destination, 0)
		},
		func(source string) error {
			return unix.Unlinkat(directoryFD, source, 0)
		},
	)
}

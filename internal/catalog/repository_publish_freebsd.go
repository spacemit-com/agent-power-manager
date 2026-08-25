//go:build freebsd

package catalog

import (
	"errors"

	"golang.org/x/sys/unix"
)

func renameRepositoryNoReplace(directoryFD int, source, destination string) error {
	if err := unix.Linkat(directoryFD, source, directoryFD, destination, 0); err != nil {
		return err
	}
	if err := unix.Unlinkat(directoryFD, source, 0); err != nil {
		rollbackErr := unix.Unlinkat(directoryFD, destination, 0)
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func exchangeRepositoryEntries(int, string, string) error {
	return unix.ENOTSUP
}

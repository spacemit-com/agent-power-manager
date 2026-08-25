//go:build linux

package catalog

import "golang.org/x/sys/unix"

func renameRepositoryNoReplace(directoryFD int, source, destination string) error {
	return unix.Renameat2(directoryFD, source, directoryFD, destination, unix.RENAME_NOREPLACE)
}

func exchangeRepositoryEntries(directoryFD int, source, destination string) error {
	return unix.Renameat2(directoryFD, source, directoryFD, destination, unix.RENAME_EXCHANGE)
}

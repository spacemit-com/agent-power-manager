//go:build darwin

package catalog

import "golang.org/x/sys/unix"

func renameRepositoryNoReplace(directoryFD int, source, destination string) error {
	return unix.RenameatxNp(directoryFD, source, directoryFD, destination, unix.RENAME_EXCL)
}

func exchangeRepositoryEntries(directoryFD int, source, destination string) error {
	return unix.RenameatxNp(directoryFD, source, directoryFD, destination, unix.RENAME_SWAP)
}

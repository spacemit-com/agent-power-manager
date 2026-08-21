//go:build darwin

package remote

import "golang.org/x/sys/unix"

func publishDirectoryNoReplace(directoryFD int, source, destination string) error {
	return unix.RenameatxNp(directoryFD, source, directoryFD, destination, unix.RENAME_EXCL)
}

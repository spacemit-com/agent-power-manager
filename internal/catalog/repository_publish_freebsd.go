//go:build freebsd

package catalog

import "golang.org/x/sys/unix"

func renameRepositoryNoReplace(int, string, string) error {
	return unix.ENOTSUP
}

func exchangeRepositoryEntries(int, string, string) error {
	return unix.ENOTSUP
}

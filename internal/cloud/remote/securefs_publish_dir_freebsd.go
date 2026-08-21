//go:build freebsd

package remote

import "golang.org/x/sys/unix"

func publishDirectoryNoReplace(int, string, string) error {
	return unix.ENOTSUP
}

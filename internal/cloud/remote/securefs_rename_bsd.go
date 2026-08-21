//go:build darwin || freebsd

package remote

import "golang.org/x/sys/unix"

func publishEntryNoReplace(directoryFD int, source, destination string) error {
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

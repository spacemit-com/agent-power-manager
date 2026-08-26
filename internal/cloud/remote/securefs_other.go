//go:build !linux && !darwin && !freebsd

package remote

import (
	"io/fs"
	"os"
	"runtime"
)

var errSecureDestinationExists = fs.ErrExist

type pinnedDirectory struct {
	path string
}

type stagedContent struct {
	directory *pinnedDirectory
	name      string
}

type ownedDirectory struct {
	parent    *pinnedDirectory
	directory *pinnedDirectory
	name      string
}

func (owned *ownedDirectory) openDirectoryHandle() (*os.File, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote tree validation is unsupported on %s", runtime.GOOS)
}

type pinnedEmptyDirectory struct{}

func openPinnedDirectory(path string) (*pinnedDirectory, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote content staging is unsupported on %s for %q", runtime.GOOS, path)
}

func (directory *pinnedDirectory) close() error { return nil }

func (directory *pinnedDirectory) requireAbsent(string) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote publication is unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) createStaged(string) (*stagedContent, *os.File, error) {
	return nil, nil, unsafeRemoteFilesystem("descriptor-relative Remote content staging is unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) createNamedStaged(string) (*stagedContent, *os.File, error) {
	return nil, nil, unsafeRemoteFilesystem("descriptor-relative Remote content staging is unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) createOwned(string) (*ownedDirectory, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote temporary directories are unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) createOwnedNamed(string, fs.FileMode) (*ownedDirectory, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote temporary directories are unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) openOptionalEmpty(string) (*pinnedEmptyDirectory, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote destination inspection is unsupported on %s", runtime.GOOS)
}

func (directory *pinnedDirectory) requireRegular(string) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote marker inspection is unsupported on %s", runtime.GOOS)
}

func (staged *stagedContent) openRead() (*os.File, error) {
	return nil, unsafeRemoteFilesystem("descriptor-relative Remote content reads are unsupported on %s", runtime.GOOS)
}

func (staged *stagedContent) prepareForClose(*os.File, fs.FileMode) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote content chmod is unsupported on %s", runtime.GOOS)
}

func (staged *stagedContent) remove() error { return nil }

func (staged *stagedContent) publishNoReplace(string) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote publication is unsupported on %s", runtime.GOOS)
}

func (owned *ownedDirectory) close() error { return nil }

func (owned *ownedDirectory) prepareForPublish(fs.FileMode) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote publication is unsupported on %s", runtime.GOOS)
}

func (owned *ownedDirectory) publishNoReplace(string) error {
	return unsafeRemoteFilesystem("descriptor-relative Remote publication is unsupported on %s", runtime.GOOS)
}

func (owned *ownedDirectory) release() error { return nil }

func (existing *pinnedEmptyDirectory) removeIfStillEmpty() error {
	return unsafeRemoteFilesystem("descriptor-relative Remote destination removal is unsupported on %s", runtime.GOOS)
}

func (existing *pinnedEmptyDirectory) close() error { return nil }

//go:build linux || darwin || freebsd

package catalog

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"golang.org/x/sys/unix"
)

type repositoryFileIdentity struct {
	device uint64
	inode  uint64
}

type unixRepositoryOps struct {
	open                 func(string, int, uint32) (int, error)
	openat               func(int, string, int, uint32) (int, error)
	fstat                func(int, *unix.Stat_t) error
	fstatat              func(int, string, *unix.Stat_t, int) error
	readBounded          func(io.Reader, int64) ([]byte, error)
	renameNoReplace      func(int, string, string) error
	exchange             func(int, string, string) error
	unlinkat             func(int, string, int) error
	flock                func(int, int) error
	beforeIdentityUnlink func(int, string)
	beforeUnlinkat       func(int, string)
	versionForBytes      func([]byte) domain.CatalogVersion
	syncFile             func(*os.File) error
	syncDirectory        func(*os.File) error
	close                func(*os.File) error
}

type unixRepositoryPlatform struct {
	ops unixRepositoryOps
}

type pinnedRepositoryDirectory struct {
	file *os.File
	path string
	ops  unixRepositoryOps
}

type repositoryEntryState struct {
	exists   bool
	identity repositoryFileIdentity
}

type ownedRepositoryFile struct {
	directory *pinnedRepositoryDirectory
	name      string
	identity  repositoryFileIdentity
}

func newRepositoryPlatform() repositoryPlatform {
	return &unixRepositoryPlatform{ops: unixRepositoryOps{
		open:    unix.Open,
		openat:  unix.Openat,
		fstat:   unix.Fstat,
		fstatat: unix.Fstatat,
		readBounded: func(reader io.Reader, limit int64) ([]byte, error) {
			return io.ReadAll(io.LimitReader(reader, limit))
		},
		renameNoReplace: renameRepositoryNoReplace,
		exchange:        exchangeRepositoryEntries,
		unlinkat:        unix.Unlinkat,
		flock:           unix.Flock,
		beforeIdentityUnlink: func(int, string) {
		},
		beforeUnlinkat: func(int, string) {
		},
		versionForBytes: versionForCatalogBytes,
		syncFile:        func(file *os.File) error { return file.Sync() },
		syncDirectory:   func(file *os.File) error { return file.Sync() },
		close:           func(file *os.File) error { return file.Close() },
	}}
}

func (platform *unixRepositoryPlatform) load(path string) (snapshot Snapshot, version domain.CatalogVersion, resultErr error) {
	parent, err := platform.openParent(filepath.Dir(path))
	if err != nil {
		return Snapshot{}, domain.CatalogVersion{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, parent.close())
	}()

	data, err := parent.readRegular(filepath.Base(path), true)
	if err != nil {
		return Snapshot{}, domain.CatalogVersion{}, fmt.Errorf("load catalog %q: %w", path, err)
	}
	snapshot, err = Decode(data)
	if err != nil {
		return Snapshot{}, domain.CatalogVersion{}, fmt.Errorf("decode catalog %q: %w", path, err)
	}
	digest := sha256.Sum256(data)
	return snapshot, domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}, nil
}

func (platform *unixRepositoryPlatform) replace(path string, expected domain.CatalogVersion, sourcePath string) (snapshot Snapshot, resultErr error) {
	sourceParent, err := platform.openParent(filepath.Dir(sourcePath))
	if err != nil {
		return Snapshot{}, fmt.Errorf("open staged catalog parent for %q: %w", sourcePath, err)
	}
	data, err := sourceParent.readRegular(filepath.Base(sourcePath), true)
	closeSourceErr := sourceParent.close()
	if err != nil || closeSourceErr != nil {
		readErr := errors.Join(fmt.Errorf("read staged catalog %q: %w", sourcePath, err), closeSourceErr)
		if err != nil && closeSourceErr == nil && errors.Is(err, ErrInvalidCatalog) {
			return Snapshot{}, errors.Join(ErrCatalogSourceInvalid, readErr)
		}
		return Snapshot{}, readErr
	}
	actual := versionForCatalogBytes(data)
	if actual != expected {
		return Snapshot{}, fmt.Errorf("staged catalog version %#v does not match expected %#v: %w", actual, expected, errors.Join(ErrCatalogSourceInvalid, ErrInvalidCatalog))
	}
	if _, err = Decode(data); err != nil {
		return Snapshot{}, fmt.Errorf("decode staged catalog %q: %w", sourcePath, errors.Join(ErrCatalogSourceInvalid, err))
	}

	parent, err := platform.openParent(filepath.Dir(path))
	if err != nil {
		return Snapshot{}, fmt.Errorf("open catalog parent for %q: %w", path, err)
	}
	committed := false
	defer func() {
		closeErr := parent.close()
		if !committed {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	if err := parent.lockExclusive(); err != nil {
		return Snapshot{}, fmt.Errorf("lock catalog parent for %q: %w", path, err)
	}
	defer parent.unlock()
	destination := filepath.Base(path)
	before, err := parent.inspectOptionalRegular(destination)
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspect catalog destination %q: %w", path, err)
	}
	owned, file, err := parent.createOwnedCatalog()
	if err != nil {
		return Snapshot{}, fmt.Errorf("create owned catalog sibling for %q: %w", path, err)
	}
	defer func() {
		cleanupErr := owned.cleanup()
		if !committed {
			resultErr = errors.Join(resultErr, cleanupErr)
		}
	}()
	defer func() {
		closeErr := file.Close()
		if !committed {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	if _, err := parent.prepareOwnedCatalog(owned, file, data, expected); err != nil {
		return Snapshot{}, err
	}

	current, err := parent.inspectOptionalRegular(destination)
	if err != nil {
		return Snapshot{}, fmt.Errorf("reinspect catalog destination %q: %w", path, err)
	}
	if current != before {
		return Snapshot{}, fmt.Errorf("catalog destination %q changed before publication: %w", path, fs.ErrInvalid)
	}
	if !before.exists {
		publishedSnapshot, err := parent.publishFirst(owned, file, destination, expected, data)
		if err != nil {
			return Snapshot{}, fmt.Errorf("publish first catalog %q: %w", path, err)
		}
		committed = true
		return publishedSnapshot, nil
	}
	publishedSnapshot, err := parent.publishReplacement(owned, file, destination, before, expected, data)
	if err != nil {
		return Snapshot{}, fmt.Errorf("replace catalog %q: %w", path, err)
	}
	committed = true
	return publishedSnapshot, nil
}

func versionForCatalogBytes(data []byte) domain.CatalogVersion {
	digest := sha256.Sum256(data)
	return domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

// openParent 从根目录逐级固定并按 owner/mode 验证目录；pathname 操作信任 current EUID。
func (platform *unixRepositoryPlatform) openParent(path string) (*pinnedRepositoryDirectory, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("catalog parent %q must be absolute and clean", path)
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY
	fd, err := platform.ops.open(string(filepath.Separator), flags, 0)
	if err != nil {
		return nil, fmt.Errorf("open catalog filesystem root for %q: %w", path, err)
	}
	currentPath := string(filepath.Separator)
	current := os.NewFile(uintptr(fd), currentPath)
	requireCurrentOwner := false
	verify := func(file *os.File, displayPath string) (bool, error) {
		var stat unix.Stat_t
		if err := platform.ops.fstat(int(file.Fd()), &stat); err != nil {
			return false, err
		}
		if repositoryNodeType(stat) != unix.S_IFDIR {
			return false, fs.ErrInvalid
		}
		return verifyRepositoryDirectoryTrust(stat, requireCurrentOwner)
	}

	sticky, err := verify(current, currentPath)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("verify catalog directory %q: %w", currentPath, err), current.Close())
	}
	requireCurrentOwner = sticky
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		components = nil
	}
	for _, component := range components {
		nextFD, openErr := platform.ops.openat(int(current.Fd()), component, flags, 0)
		if openErr != nil {
			return nil, errors.Join(fmt.Errorf("open physical catalog directory component %q below %q: %w", component, currentPath, openErr), current.Close())
		}
		nextPath := filepath.Join(currentPath, component)
		next := os.NewFile(uintptr(nextFD), nextPath)
		sticky, verifyErr := verify(next, nextPath)
		closeErr := current.Close()
		if verifyErr != nil || closeErr != nil {
			return nil, errors.Join(fmt.Errorf("verify physical catalog directory %q: %w", nextPath, verifyErr), closeErr, next.Close())
		}
		current = next
		currentPath = nextPath
		requireCurrentOwner = sticky
	}
	if requireCurrentOwner {
		return nil, errors.Join(
			fmt.Errorf("catalog parent %q must not itself be a root-owned sticky directory", path),
			current.Close(),
		)
	}
	return &pinnedRepositoryDirectory{file: current, path: path, ops: platform.ops}, nil
}

func verifyRepositoryDirectoryTrust(stat unix.Stat_t, requireCurrentOwner bool) (bool, error) {
	euid := uint32(os.Geteuid())
	owner := stat.Uid
	if owner != 0 && owner != euid {
		return false, fmt.Errorf("catalog directory owner uid %d is not trusted", owner)
	}
	if requireCurrentOwner && owner != euid {
		return false, fmt.Errorf("entry below a root-owned sticky directory must be owned by euid %d", euid)
	}
	permissions := uint32(stat.Mode) & 0o777
	if permissions&0o022 == 0 {
		return false, nil
	}
	if owner == 0 && uint32(stat.Mode)&uint32(unix.S_ISVTX) != 0 {
		return true, nil
	}
	return false, fmt.Errorf("catalog directory mode %04o permits untrusted entry mutation", permissions)
}

func (directory *pinnedRepositoryDirectory) close() error {
	if directory == nil || directory.file == nil {
		return nil
	}
	err := directory.ops.close(directory.file)
	directory.file = nil
	return err
}

// lockExclusive 使用 advisory parent lock 只串行遵守 Repository 协议的发布者。
func (directory *pinnedRepositoryDirectory) lockExclusive() error {
	if directory == nil || directory.file == nil {
		return fs.ErrInvalid
	}
	return directory.ops.flock(int(directory.file.Fd()), unix.LOCK_EX)
}

func (directory *pinnedRepositoryDirectory) unlock() {
	if directory == nil || directory.file == nil {
		return
	}
	_ = directory.ops.flock(int(directory.file.Fd()), unix.LOCK_UN)
}

func (directory *pinnedRepositoryDirectory) readRegular(name string, requirePrivateMode bool) ([]byte, error) {
	if directory == nil || directory.file == nil || !validRepositoryEntryName(name) {
		return nil, fmt.Errorf("invalid catalog entry name %q", name)
	}
	before, err := directory.statEntry(name)
	if err != nil {
		return nil, err
	}
	if err := validateRepositoryRegularStat(before, requirePrivateMode); err != nil {
		return nil, err
	}
	if before.Size > MaxCatalogSize {
		return nil, fmt.Errorf("catalog size %d exceeds %d bytes: %w", before.Size, MaxCatalogSize, ErrInvalidCatalog)
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_NOCTTY
	fd, err := directory.ops.openat(int(directory.file.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
	defer file.Close()

	var opened unix.Stat_t
	if err := directory.ops.fstat(fd, &opened); err != nil {
		return nil, err
	}
	if err := validateRepositoryRegularStat(opened, requirePrivateMode); err != nil {
		return nil, err
	}
	if repositoryIdentity(opened) != repositoryIdentity(before) || opened.Size != before.Size {
		return nil, fmt.Errorf("catalog entry %q changed before open: %w", name, fs.ErrInvalid)
	}
	data, err := directory.ops.readBounded(file, MaxCatalogSize+1)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > MaxCatalogSize {
		return nil, fmt.Errorf("catalog size exceeds %d bytes: %w", MaxCatalogSize, ErrInvalidCatalog)
	}

	var afterDescriptor unix.Stat_t
	if err := directory.ops.fstat(fd, &afterDescriptor); err != nil {
		return nil, err
	}
	afterEntry, err := directory.statEntry(name)
	if err != nil {
		return nil, err
	}
	if err := validateRepositoryRegularStat(afterDescriptor, requirePrivateMode); err != nil {
		return nil, err
	}
	if err := validateRepositoryRegularStat(afterEntry, requirePrivateMode); err != nil {
		return nil, err
	}
	identity := repositoryIdentity(before)
	if repositoryIdentity(afterDescriptor) != identity || repositoryIdentity(afterEntry) != identity ||
		afterDescriptor.Size != before.Size || afterEntry.Size != before.Size || int64(len(data)) != before.Size {
		return nil, fmt.Errorf("catalog entry %q changed while reading: %w", name, fs.ErrInvalid)
	}
	return data, nil
}

func (directory *pinnedRepositoryDirectory) statEntry(name string) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := directory.ops.fstatat(int(directory.file.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	return stat, err
}

func (directory *pinnedRepositoryDirectory) inspectOptionalRegular(name string) (repositoryEntryState, error) {
	stat, err := directory.statEntry(name)
	if errors.Is(err, unix.ENOENT) {
		return repositoryEntryState{}, nil
	}
	if err != nil {
		return repositoryEntryState{}, err
	}
	if err := validateRepositoryRegularStat(stat, true); err != nil {
		return repositoryEntryState{}, err
	}
	return repositoryEntryState{exists: true, identity: repositoryIdentity(stat)}, nil
}

func (directory *pinnedRepositoryDirectory) createOwnedCatalog() (*ownedRepositoryFile, *os.File, error) {
	for attempt := 0; attempt < 128; attempt++ {
		name, err := randomRepositoryOwnedName()
		if err != nil {
			return nil, nil, err
		}
		fd, err := directory.ops.openat(
			int(directory.file.Fd()),
			name,
			unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
			0o600,
		)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		file := os.NewFile(uintptr(fd), filepath.Join(directory.path, name))
		var initial unix.Stat_t
		if err := directory.ops.fstat(fd, &initial); err != nil {
			return nil, nil, errors.Join(err, file.Close())
		}
		owned := &ownedRepositoryFile{directory: directory, name: name, identity: repositoryIdentity(initial)}
		closeAndCleanup := func(operationErr error) error {
			return errors.Join(operationErr, file.Close(), owned.cleanup())
		}
		if err := validateRepositoryRegularStat(initial, false); err != nil {
			return nil, nil, closeAndCleanup(err)
		}
		if err := file.Chmod(0o600); err != nil {
			return nil, nil, closeAndCleanup(err)
		}
		var descriptor unix.Stat_t
		statErr := directory.ops.fstat(fd, &descriptor)
		entry, entryErr := directory.statEntry(name)
		if statErr != nil || entryErr != nil || validateRepositoryRegularStat(descriptor, true) != nil ||
			validateRepositoryRegularStat(entry, true) != nil || repositoryIdentity(descriptor) != owned.identity ||
			repositoryIdentity(entry) != owned.identity {
			if statErr == nil && entryErr == nil {
				statErr = fs.ErrInvalid
			}
			return nil, nil, closeAndCleanup(errors.Join(statErr, entryErr))
		}
		return owned, file, nil
	}
	return nil, nil, fmt.Errorf("create unique owned catalog sibling: %w", fs.ErrExist)
}

func (directory *pinnedRepositoryDirectory) prepareOwnedCatalog(owned *ownedRepositoryFile, file *os.File, data []byte, expected domain.CatalogVersion) (Snapshot, error) {
	if owned == nil || file == nil {
		return Snapshot{}, fs.ErrInvalid
	}
	for written := 0; written < len(data); {
		count, err := file.Write(data[written:])
		if err != nil {
			return Snapshot{}, fmt.Errorf("copy catalog into owned sibling: %w", err)
		}
		if count == 0 {
			return Snapshot{}, fmt.Errorf("copy catalog into owned sibling: %w", io.ErrShortWrite)
		}
		written += count
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Snapshot{}, fmt.Errorf("rewind owned catalog sibling: %w", err)
	}
	copied, err := directory.ops.readBounded(file, MaxCatalogSize+1)
	if err != nil {
		return Snapshot{}, fmt.Errorf("re-read owned catalog sibling: %w", err)
	}
	if !bytes.Equal(copied, data) || versionForCatalogBytes(copied) != expected {
		return Snapshot{}, fmt.Errorf("owned catalog sibling bytes differ from staged catalog: %w", ErrInvalidCatalog)
	}
	snapshot, err := Decode(copied)
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode copied catalog sibling: %w", err)
	}
	if err := directory.ops.syncFile(file); err != nil {
		return Snapshot{}, fmt.Errorf("sync owned catalog sibling: %w", err)
	}
	var descriptor unix.Stat_t
	if err := directory.ops.fstat(int(file.Fd()), &descriptor); err != nil {
		return Snapshot{}, err
	}
	entry, err := directory.statEntry(owned.name)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateRepositoryRegularStat(descriptor, true); err != nil {
		return Snapshot{}, err
	}
	if err := validateRepositoryRegularStat(entry, true); err != nil {
		return Snapshot{}, err
	}
	if repositoryIdentity(descriptor) != owned.identity || repositoryIdentity(entry) != owned.identity ||
		descriptor.Size != expected.Size || entry.Size != expected.Size {
		return Snapshot{}, fmt.Errorf("owned catalog sibling changed before publication: %w", fs.ErrInvalid)
	}
	return snapshot, nil
}

func (directory *pinnedRepositoryDirectory) publishFirst(owned *ownedRepositoryFile, file *os.File, destination string, expected domain.CatalogVersion, staged []byte) (Snapshot, error) {
	if err := directory.ops.renameNoReplace(int(directory.file.Fd()), owned.name, destination); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := directory.verifyPublishedCatalog(owned, file, destination, expected, staged)
	if err != nil {
		rollbackErr := directory.removeEntryIfIdentity(destination, owned.identity)
		return Snapshot{}, errors.Join(fmt.Errorf("verify first catalog publication: %w", err), rollbackErr)
	}
	if err := directory.ops.syncDirectory(directory.file); err != nil {
		rollbackErr := directory.removeEntryIfIdentity(destination, owned.identity)
		rollbackSyncErr := directory.ops.syncDirectory(directory.file)
		return Snapshot{}, errors.Join(err, rollbackErr, rollbackSyncErr)
	}
	owned.name = ""
	return snapshot, nil
}

func (directory *pinnedRepositoryDirectory) publishReplacement(owned *ownedRepositoryFile, file *os.File, destination string, previous repositoryEntryState, expected domain.CatalogVersion, staged []byte) (Snapshot, error) {
	if err := directory.ops.exchange(int(directory.file.Fd()), owned.name, destination); err != nil {
		return Snapshot{}, err
	}
	destinationStat, destinationErr := directory.statEntry(destination)
	temporaryStat, temporaryErr := directory.statEntry(owned.name)
	verifiedSnapshot, verifyErr := directory.verifyPublishedCatalog(owned, file, destination, expected, staged)
	newIsDestination := destinationErr == nil && verifyErr == nil && repositoryIdentity(destinationStat) == owned.identity && validateRepositoryRegularStat(destinationStat, true) == nil
	oldIsTemporary := temporaryErr == nil && repositoryIdentity(temporaryStat) == previous.identity && validateRepositoryRegularStat(temporaryStat, true) == nil
	if !newIsDestination || !oldIsTemporary {
		rollbackErr := directory.rollbackExchange(owned, destination, temporaryStat, temporaryErr)
		if rollbackErr != nil {
			reconciledSnapshot, committed, reconcileErr := directory.reconcileReplacementState(owned, file, destination, previous, expected, staged)
			if committed {
				return reconciledSnapshot, nil
			}
			rollbackErr = errors.Join(rollbackErr, reconcileErr)
		}
		return Snapshot{}, errors.Join(
			fmt.Errorf("catalog publication failed post-exchange verification: %w", fs.ErrInvalid),
			destinationErr,
			temporaryErr,
			verifyErr,
			rollbackErr,
		)
	}
	if err := directory.ops.syncDirectory(directory.file); err != nil {
		rollbackErr := directory.rollbackExchange(owned, destination, temporaryStat, nil)
		if rollbackErr == nil {
			return Snapshot{}, err
		}
		reconciledSnapshot, committed, reconcileErr := directory.reconcileReplacementState(owned, file, destination, previous, expected, staged)
		if !committed {
			return Snapshot{}, errors.Join(err, rollbackErr, reconcileErr)
		}
		return reconciledSnapshot, nil
	}
	directory.cleanupCommittedPrevious(owned, previous.identity)
	return verifiedSnapshot, nil
}

// reconcileReplacementState 在回滚原语失败后重新证明提交或再次回滚，避免返回值与可见目录项矛盾。
func (directory *pinnedRepositoryDirectory) reconcileReplacementState(owned *ownedRepositoryFile, file *os.File, destination string, previous repositoryEntryState, expected domain.CatalogVersion, staged []byte) (Snapshot, bool, error) {
	verifyCommitted := func() (Snapshot, unix.Stat_t, error) {
		snapshot, verifyErr := directory.verifyPublishedCatalog(owned, file, destination, expected, staged)
		temporary, temporaryErr := directory.statEntry(owned.name)
		if temporaryErr == nil && (repositoryIdentity(temporary) != previous.identity || validateRepositoryRegularStat(temporary, true) != nil) {
			temporaryErr = fs.ErrInvalid
		}
		stateErr := verifyErr
		if temporaryErr != nil {
			stateErr = errors.Join(stateErr, fmt.Errorf("verify previous catalog during state reconciliation: %w", temporaryErr))
		}
		if stateErr != nil {
			return Snapshot{}, temporary, stateErr
		}
		return snapshot, temporary, nil
	}
	commit := func(snapshot Snapshot) (Snapshot, bool, error) {
		// 新目录项和旧 inode 已精确复验，发布已经线性化；后续同步或清理失败不得反转成功。
		_ = directory.ops.syncDirectory(directory.file)
		directory.cleanupCommittedPrevious(owned, previous.identity)
		return snapshot, true, nil
	}

	snapshot, temporary, commitErr := verifyCommitted()
	if commitErr == nil {
		return commit(snapshot)
	}
	if repositoryIdentity(temporary) != previous.identity || validateRepositoryRegularStat(temporary, true) != nil {
		return Snapshot{}, false, commitErr
	}
	rollbackErr := directory.rollbackExchange(owned, destination, temporary, nil)
	if rollbackErr == nil {
		return Snapshot{}, false, commitErr
	}
	snapshot, _, recheckErr := verifyCommitted()
	if recheckErr == nil {
		return commit(snapshot)
	}
	return Snapshot{}, false, errors.Join(commitErr, rollbackErr, recheckErr)
}

// cleanupCommittedPrevious 在提交后尽力清理旧 inode；检测到替换时保留 residue，且不反转提交结果。
func (directory *pinnedRepositoryDirectory) cleanupCommittedPrevious(owned *ownedRepositoryFile, previous repositoryFileIdentity) {
	_ = directory.removeEntryIfIdentity(owned.name, previous)
	owned.name = ""
	_ = directory.ops.syncDirectory(directory.file)
}

func (directory *pinnedRepositoryDirectory) verifyPublishedCatalog(owned *ownedRepositoryFile, file *os.File, destination string, expected domain.CatalogVersion, staged []byte) (Snapshot, error) {
	if owned == nil || file == nil {
		return Snapshot{}, fs.ErrInvalid
	}
	var descriptor unix.Stat_t
	if err := directory.ops.fstat(int(file.Fd()), &descriptor); err != nil {
		return Snapshot{}, err
	}
	destinationStat, err := directory.statEntry(destination)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateRepositoryRegularStat(descriptor, true); err != nil {
		return Snapshot{}, err
	}
	if err := validateRepositoryRegularStat(destinationStat, true); err != nil {
		return Snapshot{}, err
	}
	if repositoryIdentity(descriptor) != owned.identity || repositoryIdentity(destinationStat) != owned.identity ||
		descriptor.Size != expected.Size || destinationStat.Size != expected.Size {
		return Snapshot{}, fmt.Errorf("published catalog identity or size changed: %w", fs.ErrInvalid)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Snapshot{}, err
	}
	data, err := directory.ops.readBounded(file, MaxCatalogSize+1)
	if err != nil {
		return Snapshot{}, err
	}
	if !bytes.Equal(data, staged) {
		return Snapshot{}, fmt.Errorf("published catalog bytes differ from staged catalog: %w", ErrInvalidCatalog)
	}
	if directory.ops.versionForBytes(data) != expected {
		return Snapshot{}, fmt.Errorf("published catalog version does not match expected: %w", ErrInvalidCatalog)
	}
	snapshot, err := Decode(data)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (directory *pinnedRepositoryDirectory) rollbackExchange(owned *ownedRepositoryFile, destination string, temporaryStat unix.Stat_t, temporaryErr error) error {
	if temporaryErr != nil {
		return temporaryErr
	}
	destinationStat, err := directory.statEntry(destination)
	if err != nil || repositoryIdentity(destinationStat) != owned.identity {
		if err == nil {
			err = fs.ErrInvalid
		}
		return fmt.Errorf("refuse rollback after destination changed: %w", err)
	}
	if err := directory.ops.exchange(int(directory.file.Fd()), owned.name, destination); err != nil {
		return fmt.Errorf("rollback catalog exchange: %w", err)
	}
	restored, restoredErr := directory.statEntry(destination)
	temporary, ownedErr := directory.statEntry(owned.name)
	if restoredErr != nil || ownedErr != nil || repositoryIdentity(restored) != repositoryIdentity(temporaryStat) || repositoryIdentity(temporary) != owned.identity {
		if restoredErr == nil && ownedErr == nil {
			restoredErr = fs.ErrInvalid
		}
		return errors.Join(fmt.Errorf("verify catalog exchange rollback: %w", restoredErr), ownedErr)
	}
	return directory.ops.syncDirectory(directory.file)
}

// removeEntryIfIdentity 仅删除 pinned parent 下仍匹配预期身份的目录项。
// 调用者须持有 parent advisory lock；current EUID 是 pathname 操作的受信任主体，flock 不隔离无视协议的同 EUID 进程。
// POSIX 不提供 fd-bound conditional unlink，故本函数对可检测替换 fail closed，但不承诺隔离恶意同 EUID 在最终 fstatat 到 unlinkat 窄窗的替换。
func (directory *pinnedRepositoryDirectory) removeEntryIfIdentity(name string, identity repositoryFileIdentity) error {
	stat, err := directory.statEntry(name)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	if validateErr := validateRepositoryRegularStat(stat, true); validateErr != nil || repositoryIdentity(stat) != identity {
		return fmt.Errorf("refuse removal of substituted catalog entry %q: %w", name, fs.ErrInvalid)
	}
	directory.ops.beforeIdentityUnlink(int(directory.file.Fd()), name)
	stat, err = directory.statEntry(name)
	if err != nil {
		return err
	}
	if validateErr := validateRepositoryRegularStat(stat, true); validateErr != nil || repositoryIdentity(stat) != identity {
		return fmt.Errorf("refuse removal of catalog entry %q changed before unlink: %w", name, fs.ErrInvalid)
	}
	directory.ops.beforeUnlinkat(int(directory.file.Fd()), name)
	// 这是 unlink 前最后一个可检测窗口；身份改变时保留 residue，不调用 unlinkat。
	stat, err = directory.statEntry(name)
	if err != nil {
		return err
	}
	if validateErr := validateRepositoryRegularStat(stat, true); validateErr != nil || repositoryIdentity(stat) != identity {
		return fmt.Errorf("refuse removal of catalog entry %q substituted immediately before unlink: %w", name, fs.ErrInvalid)
	}
	if err := directory.ops.unlinkat(int(directory.file.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return nil
}

func (owned *ownedRepositoryFile) cleanup() error {
	if owned == nil || owned.name == "" || owned.directory == nil || owned.directory.file == nil {
		return nil
	}
	err := owned.directory.removeEntryIfIdentity(owned.name, owned.identity)
	if err == nil {
		owned.name = ""
	}
	return err
}

func randomRepositoryOwnedName() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return ".catalog-" + hex.EncodeToString(random) + ".owned", nil
}

func validateRepositoryRegularStat(stat unix.Stat_t, requirePrivateMode bool) error {
	if repositoryNodeType(stat) != unix.S_IFREG {
		return fmt.Errorf("catalog entry is not a physical regular file: %w", fs.ErrInvalid)
	}
	if uint64(stat.Nlink) != 1 {
		return fmt.Errorf("catalog entry link count %d must be one: %w", stat.Nlink, fs.ErrInvalid)
	}
	if stat.Size < 0 {
		return fmt.Errorf("catalog entry size %d is invalid: %w", stat.Size, fs.ErrInvalid)
	}
	if requirePrivateMode && uint32(stat.Mode)&0o7777 != 0o600 {
		return fmt.Errorf("catalog entry mode %04o must be 0600: %w", uint32(stat.Mode)&0o7777, fs.ErrPermission)
	}
	return nil
}

func repositoryIdentity(stat unix.Stat_t) repositoryFileIdentity {
	return repositoryFileIdentity{device: uint64(stat.Dev), inode: uint64(stat.Ino)}
}

func repositoryNodeType(stat unix.Stat_t) uint32 {
	return uint32(stat.Mode) & uint32(unix.S_IFMT)
}

func validRepositoryEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

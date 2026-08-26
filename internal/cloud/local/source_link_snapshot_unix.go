//go:build linux || darwin

package local

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const linkSnapshotWorkspacePrefix = ".apm-local-snapshot-"

var linkSnapshotPinMu sync.Mutex

type linkSnapshotWorkspace struct {
	parent    *os.File
	directory *os.File
	name      string
}

type linkSnapshotTreeCaptureState struct {
	ctx            context.Context
	policy         localTreeContentPolicy
	beforeFileOpen func(string)
	afterDataOpen  func(string)
	workspace      *linkSnapshotWorkspace
	nodes          []treeNodeSnapshot
	totalSize      int64
}

// capturePhysicalTreeLinkSnapshot 通过私有硬链接固定待读取的普通文件，供 Darwin 后端安全快照使用。
func capturePhysicalTreeLinkSnapshot(ctx context.Context, resourcesRoot, source string, beforeSnapshot func(), beforeFileOpen func(string), afterDataOpen func(string)) (snapshot *treeSnapshot, resultErr error) {
	return capturePhysicalTreeLinkSnapshotWithPolicy(ctx, resourcesRoot, source, defaultLocalTreeContentPolicy, beforeSnapshot, beforeFileOpen, afterDataOpen)
}

// capturePhysicalTreeLinkSnapshotWithPolicy 使用调用方内容策略固定物理树快照；
// policy 为 nil 时退化为默认文本-only 策略。
func capturePhysicalTreeLinkSnapshotWithPolicy(ctx context.Context, resourcesRoot, source string, policy localTreeContentPolicy, beforeSnapshot func(), beforeFileOpen func(string), afterDataOpen func(string)) (snapshot *treeSnapshot, resultErr error) {
	root, relative, err := openLinkSnapshotResourcesRoot(resourcesRoot, source)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	if beforeSnapshot != nil {
		beforeSnapshot()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sourceDirectory, err := openLinkSnapshotRelativeDirectory(root, relative)
	if err != nil {
		return nil, unsafeContent("open physical source tree %q: %v", source, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, sourceDirectory.Close())
	}()
	workspace, err := newLinkSnapshotWorkspace(root)
	if err != nil {
		return nil, unsafeContent("create pinned source workspace for %q: %v", source, err)
	}
	defer func() {
		if cleanupErr := workspace.close(); cleanupErr != nil {
			resultErr = errors.Join(resultErr, unsafeContent("remove pinned source workspace: %v", cleanupErr))
		}
	}()
	state := &linkSnapshotTreeCaptureState{
		ctx:            ctx,
		policy:         policy,
		beforeFileOpen: beforeFileOpen,
		afterDataOpen:  afterDataOpen,
		workspace:      workspace,
	}
	if err := state.captureDirectory(sourceDirectory, ""); err != nil {
		return nil, err
	}
	return &treeSnapshot{nodes: state.nodes}, nil
}

// capturePhysicalFileLinkSnapshot 固定并读取单个唯一普通文件，不对未知节点执行数据打开。
func capturePhysicalFileLinkSnapshot(ctx context.Context, resourcesRoot, source string, sizeLimit int64, beforeFileOpen func(string), afterDataOpen func(string)) (contents []byte, mode fs.FileMode, resultErr error) {
	root, relative, err := openLinkSnapshotResourcesRoot(resourcesRoot, source)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	parent, name, err := openLinkSnapshotRelativeParent(root, relative)
	if err != nil {
		return nil, 0, unsafeContent("open source parent for %q: %v", source, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, parent.Close())
	}()
	workspace, err := newLinkSnapshotWorkspace(root)
	if err != nil {
		return nil, 0, unsafeContent("create pinned source workspace for %q: %v", source, err)
	}
	defer func() {
		if cleanupErr := workspace.close(); cleanupErr != nil {
			resultErr = errors.Join(resultErr, unsafeContent("remove pinned source workspace: %v", cleanupErr))
		}
	}()
	expected, err := statLinkSnapshotNodeAt(parent, name)
	if err != nil {
		return nil, 0, unsafeContent("inspect physical source file %q: %v", source, err)
	}
	if linkSnapshotNodeType(expected) != unix.S_IFREG {
		return nil, 0, unsafeContent("source file %q is not a physical regular file", source)
	}
	return captureLinkSnapshotRegular(ctx, workspace, parent, name, relative, source, expected, sizeLimit, beforeFileOpen, afterDataOpen, defaultLocalTreeContentPolicy)
}

func openLinkSnapshotResourcesRoot(resourcesRoot, source string) (*os.File, string, error) {
	rootPath := filepath.Clean(resourcesRoot)
	relative, err := filepath.Rel(rootPath, filepath.Clean(source))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", unsafeContent("source %q escapes resource root %q", source, rootPath)
	}
	root, err := openLinkSnapshotAbsoluteDirectory(rootPath)
	if err != nil {
		return nil, "", unsafeContent("open physical resource root %q: %v", rootPath, err)
	}
	return root, relative, nil
}

func openLinkSnapshotAbsoluteDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fs.ErrInvalid
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openLinkSnapshotRelativeDirectory(root *os.File, relative string) (*os.File, error) {
	components := splitLinkSnapshotRelative(relative)
	if len(components) == 0 {
		return nil, fs.ErrInvalid
	}
	current := root
	owned := false
	for _, component := range components {
		next, err := openLinkSnapshotDirectoryAt(current, component, nil)
		if owned {
			closeErr := current.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			if next != nil {
				_ = next.Close()
			}
			return nil, err
		}
		current = next
		owned = true
	}
	return current, nil
}

func openLinkSnapshotRelativeParent(root *os.File, relative string) (*os.File, string, error) {
	components := splitLinkSnapshotRelative(relative)
	if len(components) == 0 {
		return nil, "", fs.ErrInvalid
	}
	if len(components) == 1 {
		duplicate, err := duplicateLinkSnapshotDirectory(root)
		return duplicate, components[0], err
	}
	parent, err := openLinkSnapshotRelativeDirectory(root, filepath.Join(components[:len(components)-1]...))
	return parent, components[len(components)-1], err
}

func openLinkSnapshotDirectoryAt(directory *os.File, name string, expected *unix.Stat_t) (*os.File, error) {
	var before unix.Stat_t
	if expected == nil {
		stat, err := statLinkSnapshotNodeAt(directory, name)
		if err != nil {
			return nil, err
		}
		before = stat
		expected = &before
	}
	if linkSnapshotNodeType(*expected) != unix.S_IFDIR {
		return nil, fs.ErrInvalid
	}
	fd, err := unix.Openat(
		int(directory.Fd()),
		name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
		0,
	)
	if err != nil {
		return nil, err
	}
	opened := os.NewFile(uintptr(fd), name)
	var actual unix.Stat_t
	if err := unix.Fstat(fd, &actual); err != nil || linkSnapshotNodeType(actual) != unix.S_IFDIR || !sameLinkSnapshotIdentity(*expected, actual) {
		_ = opened.Close()
		if err != nil {
			return nil, err
		}
		return nil, fs.ErrInvalid
	}
	if err := verifyLinkSnapshotNodeIdentity(directory, name, actual); err != nil {
		_ = opened.Close()
		return nil, err
	}
	return opened, nil
}

func duplicateLinkSnapshotDirectory(directory *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(directory.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), directory.Name()), nil
}

func splitLinkSnapshotRelative(path string) []string {
	clean := filepath.Clean(path)
	clean = strings.TrimPrefix(clean, string(filepath.Separator))
	if clean == "" || clean == "." {
		return nil
	}
	return strings.Split(clean, string(filepath.Separator))
}

func statLinkSnapshotNodeAt(directory *os.File, name string) (unix.Stat_t, error) {
	var stat unix.Stat_t
	err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	return stat, err
}

func verifyLinkSnapshotNodeIdentity(directory *os.File, name string, expected unix.Stat_t) error {
	current, err := statLinkSnapshotNodeAt(directory, name)
	if err != nil {
		return err
	}
	if !sameLinkSnapshotIdentity(expected, current) {
		return fs.ErrInvalid
	}
	return nil
}

func sameLinkSnapshotIdentity(left, right unix.Stat_t) bool {
	return uint64(left.Dev) == uint64(right.Dev) && uint64(left.Ino) == uint64(right.Ino)
}

func linkSnapshotNodeType(stat unix.Stat_t) uint32 {
	return uint32(stat.Mode) & uint32(unix.S_IFMT)
}

func newLinkSnapshotWorkspace(root *os.File) (*linkSnapshotWorkspace, error) {
	var attempts []error
	parent, parentErr := openLinkSnapshotDirectoryAt(root, "..", nil)
	if parentErr == nil {
		sameDevice, statErr := sameLinkSnapshotFileDevice(root, parent)
		if statErr == nil && sameDevice {
			workspace, createErr := createLinkSnapshotWorkspaceAt(parent)
			if createErr == nil {
				return workspace, nil
			}
			attempts = append(attempts, createErr)
		} else if statErr != nil {
			attempts = append(attempts, statErr)
		}
		if closeErr := parent.Close(); closeErr != nil {
			attempts = append(attempts, closeErr)
		}
	} else {
		attempts = append(attempts, parentErr)
	}
	rootParent, err := duplicateLinkSnapshotDirectory(root)
	if err != nil {
		return nil, errors.Join(append(attempts, err)...)
	}
	workspace, createErr := createLinkSnapshotWorkspaceAt(rootParent)
	if createErr == nil {
		return workspace, nil
	}
	closeErr := rootParent.Close()
	return nil, errors.Join(append(attempts, createErr, closeErr)...)
}

func sameLinkSnapshotFileDevice(left, right *os.File) (bool, error) {
	var leftStat unix.Stat_t
	if err := unix.Fstat(int(left.Fd()), &leftStat); err != nil {
		return false, err
	}
	var rightStat unix.Stat_t
	if err := unix.Fstat(int(right.Fd()), &rightStat); err != nil {
		return false, err
	}
	return uint64(leftStat.Dev) == uint64(rightStat.Dev), nil
}

func createLinkSnapshotWorkspaceAt(parent *os.File) (*linkSnapshotWorkspace, error) {
	for attempt := 0; attempt < 16; attempt++ {
		name, err := randomLinkSnapshotName(linkSnapshotWorkspacePrefix)
		if err != nil {
			return nil, err
		}
		if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return nil, err
		}
		directory, openErr := openLinkSnapshotDirectoryAt(parent, name, nil)
		if openErr != nil {
			removeErr := unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
			return nil, errors.Join(openErr, removeErr)
		}
		return &linkSnapshotWorkspace{parent: parent, directory: directory, name: name}, nil
	}
	return nil, fs.ErrExist
}

func randomLinkSnapshotName(prefix string) (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(token[:]), nil
}

func (workspace *linkSnapshotWorkspace) link(directory *os.File, name string) (string, error) {
	for attempt := 0; attempt < 16; attempt++ {
		pinnedName, err := randomLinkSnapshotName("node-")
		if err != nil {
			return "", err
		}
		if err := unix.Linkat(int(directory.Fd()), name, int(workspace.directory.Fd()), pinnedName, 0); err != nil {
			if errors.Is(err, unix.EEXIST) {
				continue
			}
			return "", err
		}
		return pinnedName, nil
	}
	return "", fs.ErrExist
}

func (workspace *linkSnapshotWorkspace) unlink(name string) error {
	return unix.Unlinkat(int(workspace.directory.Fd()), name, 0)
}

func (workspace *linkSnapshotWorkspace) close() error {
	var resultErr error
	if workspace.directory != nil {
		resultErr = errors.Join(resultErr, workspace.directory.Close())
		workspace.directory = nil
	}
	if workspace.parent != nil {
		if err := unix.Unlinkat(int(workspace.parent.Fd()), workspace.name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
			resultErr = errors.Join(resultErr, err)
		}
		resultErr = errors.Join(resultErr, workspace.parent.Close())
		workspace.parent = nil
	}
	return resultErr
}

func (state *linkSnapshotTreeCaptureState) captureDirectory(directory *os.File, parent string) error {
	if err := state.ctx.Err(); err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return unsafeContent("read source directory %q: %v", parent, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := state.ctx.Err(); err != nil {
			return err
		}
		relative := entry.Name()
		if parent != "" {
			relative = filepath.Join(parent, entry.Name())
		}
		if err := state.validateNodePath(relative); err != nil {
			return err
		}
		expected, err := statLinkSnapshotNodeAt(directory, entry.Name())
		if err != nil {
			return unsafeContent("inspect source path %q: %v", relative, err)
		}
		switch linkSnapshotNodeType(expected) {
		case unix.S_IFDIR:
			child, err := openLinkSnapshotDirectoryAt(directory, entry.Name(), &expected)
			if err != nil {
				return unsafeContent("open physical source directory %q: %v", relative, err)
			}
			state.nodes = append(state.nodes, treeNodeSnapshot{relative: relative, mode: fs.FileMode(expected.Mode & 0o777), directory: true})
			captureErr := state.captureDirectory(child, relative)
			closeErr := child.Close()
			if captureErr != nil || closeErr != nil {
				return errors.Join(captureErr, closeErr)
			}
		case unix.S_IFREG:
			contents, mode, err := captureLinkSnapshotRegular(
				state.ctx,
				state.workspace,
				directory,
				entry.Name(),
				relative,
				relative,
				expected,
				maxTreeFileSize,
				state.beforeFileOpen,
				state.afterDataOpen,
				state.policy,
			)
			if err != nil {
				return err
			}
			state.totalSize += int64(len(contents))
			if state.totalSize > maxTreeSize {
				return unsafeContent("source tree exceeds %d bytes", maxTreeSize)
			}
			state.nodes = append(state.nodes, treeNodeSnapshot{relative: relative, mode: mode, contents: contents})
		default:
			return unsafeContent("source path %q is not a physical directory or regular file", relative)
		}
	}
	return nil
}

func (state *linkSnapshotTreeCaptureState) validateNodePath(relative string) error {
	if len(state.nodes)+1 > maxTreeNodes {
		return unsafeContent("source tree exceeds %d nodes", maxTreeNodes)
	}
	if len([]byte(relative)) > maxTreePathSize {
		return unsafeContent("source path %q exceeds %d bytes", relative, maxTreePathSize)
	}
	if depth := len(strings.Split(relative, string(filepath.Separator))); depth > maxTreeDepth {
		return unsafeContent("source path %q exceeds depth %d", relative, maxTreeDepth)
	}
	return nil
}

func captureLinkSnapshotRegular(ctx context.Context, workspace *linkSnapshotWorkspace, directory *os.File, name, relative, displayPath string, expected unix.Stat_t, sizeLimit int64, beforeFileOpen func(string), afterDataOpen func(string), policy localTreeContentPolicy) (contents []byte, mode fs.FileMode, resultErr error) {
	linkSnapshotPinMu.Lock()
	pinLocked := true
	defer func() {
		if pinLocked {
			linkSnapshotPinMu.Unlock()
		}
	}()
	current, err := statLinkSnapshotNodeAt(directory, name)
	if err != nil || linkSnapshotNodeType(current) != unix.S_IFREG || !sameLinkSnapshotIdentity(expected, current) {
		return nil, 0, unsafeContent("source file %q changed before pinning", displayPath)
	}
	expected = current
	if uint64(expected.Nlink) != 1 {
		return nil, 0, unsafeContent("source file %q has %d hard links", displayPath, expected.Nlink)
	}
	if beforeFileOpen != nil {
		beforeFileOpen(relative)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	pinnedName, err := workspace.link(directory, name)
	if err != nil {
		return nil, 0, unsafeContent("pin physical source file %q: %v", displayPath, err)
	}
	pinned := true
	defer func() {
		if pinned {
			if cleanupErr := workspace.unlink(pinnedName); cleanupErr != nil && !errors.Is(cleanupErr, unix.ENOENT) {
				resultErr = errors.Join(resultErr, unsafeContent("remove pinned source file %q: %v", displayPath, cleanupErr))
			}
		}
	}()
	pinnedStat, err := statLinkSnapshotNodeAt(workspace.directory, pinnedName)
	if err != nil || linkSnapshotNodeType(pinnedStat) != unix.S_IFREG || !sameLinkSnapshotIdentity(expected, pinnedStat) || uint64(pinnedStat.Nlink) != 2 {
		return nil, 0, unsafeContent("source file %q changed while pinning", displayPath)
	}
	fd, err := unix.Openat(
		int(workspace.directory.Fd()),
		pinnedName,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_NOCTTY,
		0,
	)
	if err != nil {
		return nil, 0, unsafeContent("open pinned source file %q: %v", displayPath, err)
	}
	dataFile := os.NewFile(uintptr(fd), displayPath)
	defer func() {
		resultErr = errors.Join(resultErr, dataFile.Close())
	}()
	if afterDataOpen != nil {
		afterDataOpen(relative)
	}
	var dataStat unix.Stat_t
	if err := unix.Fstat(fd, &dataStat); err != nil || linkSnapshotNodeType(dataStat) != unix.S_IFREG || !sameLinkSnapshotIdentity(pinnedStat, dataStat) || uint64(dataStat.Nlink) != 2 {
		return nil, 0, unsafeContent("source file %q changed while opening pinned data", displayPath)
	}
	if err := verifyLinkSnapshotNodeIdentity(directory, name, dataStat); err != nil {
		return nil, 0, unsafeContent("source file %q changed after data open: %v", displayPath, err)
	}
	if err := workspace.unlink(pinnedName); err != nil {
		return nil, 0, unsafeContent("unlink pinned source file %q before read: %v", displayPath, err)
	}
	pinned = false
	linkSnapshotPinMu.Unlock()
	pinLocked = false
	contents, err = readLinkSnapshotBoundedContext(ctx, dataFile, sizeLimit)
	if err != nil {
		return nil, 0, err
	}
	if policy != nil {
		if policyErr := policy(relative, contents); policyErr != nil {
			return nil, 0, policyErr
		}
	} else if isUnsafePayload(contents) {
		return nil, 0, unsafeContent("source file %q contains binary or archive content", displayPath)
	}
	return contents, fs.FileMode(dataStat.Mode & 0o777), nil
}

func readLinkSnapshotBoundedContext(ctx context.Context, source io.Reader, limit int64) ([]byte, error) {
	contents := make([]byte, 0)
	buffer := make([]byte, sourceCopyChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := source.Read(buffer)
		if count > 0 {
			if int64(len(contents))+int64(count) > limit {
				return nil, unsafeContent("source content exceeds %d bytes", limit)
			}
			contents = append(contents, buffer[:count]...)
		}
		if errors.Is(err, io.EOF) {
			return contents, ctx.Err()
		}
		if err != nil {
			return nil, unsafeContent("read pinned source descriptor: %v", err)
		}
	}
}

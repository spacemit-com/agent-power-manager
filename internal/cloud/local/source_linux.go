//go:build linux

package local

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const sourceResolveFlags = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS

type treeCaptureState struct {
	ctx            context.Context
	beforeFileOpen func(string)
	nodes          []treeNodeSnapshot
	totalSize      int64
}

func capturePhysicalTree(ctx context.Context, resourcesRoot, source string, beforeSnapshot func(), beforeFileOpen func(string)) (*treeSnapshot, error) {
	root, relative, err := openResourcesRoot(resourcesRoot, source)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if beforeSnapshot != nil {
		beforeSnapshot()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sourceDirectory, err := openAt(root, relative, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC)
	if err != nil {
		return nil, unsafeContent("open physical source tree %q: %v", source, err)
	}
	defer sourceDirectory.Close()
	info, err := sourceDirectory.Stat()
	if err != nil || !info.IsDir() {
		return nil, unsafeContent("stat physical source tree %q: %v", source, err)
	}
	state := &treeCaptureState{ctx: ctx, beforeFileOpen: beforeFileOpen}
	if err := state.captureDirectory(sourceDirectory, ""); err != nil {
		return nil, err
	}
	return &treeSnapshot{nodes: state.nodes}, nil
}

func capturePhysicalFile(ctx context.Context, resourcesRoot, source string, sizeLimit int64, beforeFileOpen func(string)) ([]byte, fs.FileMode, error) {
	root, relative, err := openResourcesRoot(resourcesRoot, source)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	pathFile, err := openPathAt(root, relative)
	if err != nil {
		return nil, 0, unsafeContent("open physical source file %q: %v", source, err)
	}
	defer pathFile.Close()
	pathInfo, err := pathFile.Stat()
	if err != nil || !pathInfo.Mode().IsRegular() {
		return nil, 0, unsafeContent("source file %q is not a physical regular file", source)
	}
	if beforeFileOpen != nil {
		beforeFileOpen(relative)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	dataFile, err := openAt(root, relative, unix.O_RDONLY|unix.O_CLOEXEC)
	if err != nil {
		return nil, 0, unsafeContent("open verified source file %q: %v", source, err)
	}
	defer dataFile.Close()
	dataInfo, err := dataFile.Stat()
	if err != nil || !dataInfo.Mode().IsRegular() || !os.SameFile(pathInfo, dataInfo) {
		return nil, 0, unsafeContent("source file %q changed while opening", source)
	}
	if err := rejectMultipleLinks(dataInfo, source); err != nil {
		return nil, 0, err
	}
	contents, err := readBoundedContext(ctx, dataFile, sizeLimit)
	if err != nil {
		return nil, 0, err
	}
	if isUnsafePayload(contents) {
		return nil, 0, unsafeContent("source file %q contains binary or archive content", source)
	}
	return contents, dataInfo.Mode().Perm(), nil
}

func openResourcesRoot(resourcesRoot, source string) (*os.File, string, error) {
	root := filepath.Clean(resourcesRoot)
	relative, err := filepath.Rel(root, filepath.Clean(source))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", unsafeContent("source %q escapes resource root %q", source, root)
	}
	how := &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_NO_MAGICLINKS | unix.RESOLVE_NO_SYMLINKS,
	}
	fd, err := unix.Openat2(unix.AT_FDCWD, root, how)
	if err != nil {
		return nil, "", unsafeContent("open physical resource root %q: %v", root, err)
	}
	return os.NewFile(uintptr(fd), root), filepath.ToSlash(relative), nil
}

func openAt(directory *os.File, relative string, flags int) (*os.File, error) {
	how := &unix.OpenHow{Flags: uint64(flags), Resolve: sourceResolveFlags}
	fd, err := unix.Openat2(int(directory.Fd()), relative, how)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), relative), nil
}

func openPathAt(directory *os.File, relative string) (*os.File, error) {
	return openAt(directory, relative, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW)
}

func (state *treeCaptureState) captureDirectory(directory *os.File, parent string) error {
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
		pathFile, err := openPathAt(directory, entry.Name())
		if err != nil {
			return unsafeContent("open source path %q: %v", relative, err)
		}
		pathInfo, statErr := pathFile.Stat()
		if statErr != nil {
			_ = pathFile.Close()
			return unsafeContent("stat source path %q: %v", relative, statErr)
		}
		switch {
		case pathInfo.IsDir():
			child, openErr := openAt(directory, entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC)
			if openErr != nil {
				_ = pathFile.Close()
				return unsafeContent("open source directory %q: %v", relative, openErr)
			}
			childInfo, infoErr := child.Stat()
			if infoErr != nil || !os.SameFile(pathInfo, childInfo) {
				_ = child.Close()
				_ = pathFile.Close()
				return unsafeContent("source directory %q changed while opening", relative)
			}
			state.nodes = append(state.nodes, treeNodeSnapshot{relative: relative, mode: childInfo.Mode().Perm(), directory: true})
			captureErr := state.captureDirectory(child, relative)
			closeErr := child.Close()
			_ = pathFile.Close()
			if captureErr != nil || closeErr != nil {
				return errors.Join(captureErr, closeErr)
			}
		case pathInfo.Mode().IsRegular():
			if state.beforeFileOpen != nil {
				state.beforeFileOpen(relative)
			}
			dataFile, openErr := openAt(directory, entry.Name(), unix.O_RDONLY|unix.O_CLOEXEC)
			if openErr != nil {
				_ = pathFile.Close()
				return unsafeContent("open verified source file %q: %v", relative, openErr)
			}
			dataInfo, infoErr := dataFile.Stat()
			if infoErr != nil || !dataInfo.Mode().IsRegular() || !os.SameFile(pathInfo, dataInfo) {
				_ = dataFile.Close()
				_ = pathFile.Close()
				return unsafeContent("source file %q changed while opening", relative)
			}
			if linkErr := rejectMultipleLinks(dataInfo, relative); linkErr != nil {
				_ = dataFile.Close()
				_ = pathFile.Close()
				return linkErr
			}
			contents, readErr := readBoundedContext(state.ctx, dataFile, maxTreeFileSize)
			closeErr := dataFile.Close()
			_ = pathFile.Close()
			if readErr != nil || closeErr != nil {
				return errors.Join(readErr, closeErr)
			}
			if isUnsafePayload(contents) {
				return unsafeContent("source file %q contains binary or archive content", relative)
			}
			state.totalSize += int64(len(contents))
			if state.totalSize > maxTreeSize {
				return unsafeContent("source tree exceeds %d bytes", maxTreeSize)
			}
			state.nodes = append(state.nodes, treeNodeSnapshot{relative: relative, mode: dataInfo.Mode().Perm(), contents: contents})
		default:
			_ = pathFile.Close()
			return unsafeContent("source path %q is not a physical directory or regular file", relative)
		}
	}
	return nil
}

func (state *treeCaptureState) validateNodePath(relative string) error {
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

func rejectMultipleLinks(info fs.FileInfo, path string) error {
	count, err := fileLinkCount(info)
	if err != nil {
		return unsafeContent("determine hard-link count for %q: %v", path, err)
	}
	if count > 1 {
		return unsafeContent("source file %q has %d hard links", path, count)
	}
	return nil
}

func readBoundedContext(ctx context.Context, source io.Reader, limit int64) ([]byte, error) {
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
			return nil, unsafeContent("read verified source descriptor: %v", err)
		}
	}
}

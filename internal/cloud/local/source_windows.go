//go:build windows

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

	"golang.org/x/sys/windows"
)

type treeCaptureState struct {
	ctx            context.Context
	beforeFileOpen func(string)
	nodes          []treeNodeSnapshot
	totalSize      int64
}

func capturePhysicalTree(ctx context.Context, resourcesRoot, source string, beforeSnapshot func(), beforeFileOpen func(string)) (*treeSnapshot, error) {
	root, relative, err := openResourcesRootHandle(resourcesRoot, source)
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
	sourceDirectory, err := openRelativeHandle(root, relative)
	if err != nil {
		return nil, unsafeContent("open physical source tree %q: %v", source, err)
	}
	defer sourceDirectory.Close()
	info, err := sourceDirectory.Stat()
	if err != nil || !info.IsDir() {
		return nil, unsafeContent("source tree %q is not a physical directory", source)
	}
	state := &treeCaptureState{ctx: ctx, beforeFileOpen: beforeFileOpen}
	if err := state.captureDirectory(sourceDirectory, ""); err != nil {
		return nil, err
	}
	return &treeSnapshot{nodes: state.nodes}, nil
}

func capturePhysicalFile(ctx context.Context, resourcesRoot, source string, sizeLimit int64, beforeFileOpen func(string)) ([]byte, fs.FileMode, error) {
	root, relative, err := openResourcesRootHandle(resourcesRoot, source)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	pathFile, err := openRelativeHandle(root, relative)
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
	dataFile, err := openRelativeHandle(root, relative)
	if err != nil {
		return nil, 0, unsafeContent("open verified source file %q: %v", source, err)
	}
	defer dataFile.Close()
	dataInfo, err := dataFile.Stat()
	if err != nil || !dataInfo.Mode().IsRegular() || !os.SameFile(pathInfo, dataInfo) {
		return nil, 0, unsafeContent("source file %q changed while opening", source)
	}
	if err := rejectMultipleLinksHandle(dataFile, source); err != nil {
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

func openResourcesRootHandle(resourcesRoot, source string) (*os.File, string, error) {
	rootPath := filepath.Clean(resourcesRoot)
	relative, err := filepath.Rel(rootPath, filepath.Clean(source))
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, "", unsafeContent("source %q escapes resource root %q", source, rootPath)
	}
	root, err := openWindowsHandle(rootPath)
	if err != nil {
		return nil, "", unsafeContent("open physical resource root %q: %v", rootPath, err)
	}
	info, statErr := root.Stat()
	if statErr != nil || !info.IsDir() {
		_ = root.Close()
		return nil, "", unsafeContent("resource root %q is not a physical directory", rootPath)
	}
	final, finalErr := finalHandlePath(root)
	if finalErr != nil || !sameWindowsPath(final, rootPath) {
		_ = root.Close()
		return nil, "", unsafeContent("resource root %q contains a reparse-point ancestor", rootPath)
	}
	return root, relative, nil
}

func openRelativeHandle(root *os.File, relative string) (*os.File, error) {
	components := splitWindowsRelative(relative)
	if len(components) == 0 {
		return nil, fs.ErrInvalid
	}
	currentPath := root.Name()
	var current *os.File
	for _, component := range components {
		currentPath = filepath.Join(currentPath, component)
		next, err := openWindowsHandle(currentPath)
		if current != nil {
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
		final, err := finalHandlePath(next)
		if err != nil || !sameWindowsPath(final, currentPath) {
			_ = next.Close()
			return nil, unsafeContent("source component %q resolves through a reparse point", currentPath)
		}
		current = next
		currentPath = final
	}
	return current, nil
}

func openWindowsHandle(path string) (*os.File, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, unsafeContent("source path %q is a reparse point", path)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func finalHandlePath(file *os.File) (string, error) {
	handle := windows.Handle(file.Fd())
	buffer := make([]uint16, windows.MAX_PATH)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return normalizeWindowsPath(windows.UTF16ToString(buffer[:length])), nil
		}
		buffer = make([]uint16, length+1)
	}
}

func normalizeWindowsPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		path = `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	} else {
		path = strings.TrimPrefix(path, `\\?\`)
	}
	return filepath.Clean(path)
}

func sameWindowsPath(left, right string) bool {
	return strings.EqualFold(normalizeWindowsPath(left), normalizeWindowsPath(right))
}

func splitWindowsRelative(path string) []string {
	clean := filepath.Clean(path)
	if clean == "" || clean == "." {
		return nil
	}
	return strings.Split(clean, string(filepath.Separator))
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
		pathFile, err := openRelativeHandle(directory, entry.Name())
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
			state.nodes = append(state.nodes, treeNodeSnapshot{relative: relative, mode: pathInfo.Mode().Perm(), directory: true})
			captureErr := state.captureDirectory(pathFile, relative)
			closeErr := pathFile.Close()
			if captureErr != nil || closeErr != nil {
				return errors.Join(captureErr, closeErr)
			}
		case pathInfo.Mode().IsRegular():
			if state.beforeFileOpen != nil {
				state.beforeFileOpen(relative)
			}
			dataFile, openErr := openRelativeHandle(directory, entry.Name())
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
			if linkErr := rejectMultipleLinksHandle(dataFile, relative); linkErr != nil {
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

func rejectMultipleLinksHandle(file *os.File, path string) error {
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information); err != nil {
		return unsafeContent("determine hard-link count for %q: %v", path, err)
	}
	if information.NumberOfLinks > 1 {
		return unsafeContent("source file %q has %d hard links", path, information.NumberOfLinks)
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

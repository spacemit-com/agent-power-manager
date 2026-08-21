package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"apm/internal/cloud"
	fsutil "apm/internal/utils/filesystem"
)

const (
	maxTreeFileSize = int64(10 << 20)
	maxTreeSize     = int64(100 << 20)
	maxTreeNodes    = 1000
	maxTreeDepth    = 16
	maxTreePathSize = 255
	maxSingleSize   = int64(20 << 20)
)

func unsafeContent(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), cloud.ErrUnsafeContent)
}

func validatePhysicalTree(ctx context.Context, resourcesRoot, source string) error {
	if err := validateSourceAncestors(resourcesRoot, source); err != nil {
		return err
	}
	files := make([]string, 0)
	var totalSize int64
	nodes := 0
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return unsafeContent("walk source path %q: %v", path, walkErr)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return unsafeContent("lstat source path %q: %v", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return unsafeContent("source path %q is not a physical directory or regular file", path)
		}
		if info.Mode().IsRegular() && hasMultipleLinks(info) {
			return unsafeContent("source file %q has multiple hard links", path)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return unsafeContent("resolve source path %q relative to %q: %v", path, source, err)
		}
		if relative == "." {
			return nil
		}
		nodes++
		if nodes > maxTreeNodes {
			return unsafeContent("source tree %q exceeds %d nodes", source, maxTreeNodes)
		}
		if len([]byte(relative)) > maxTreePathSize {
			return unsafeContent("source path %q exceeds %d bytes", relative, maxTreePathSize)
		}
		if depth := len(strings.Split(relative, string(filepath.Separator))); depth > maxTreeDepth {
			return unsafeContent("source path %q exceeds depth %d", relative, maxTreeDepth)
		}
		if info.Mode().IsRegular() {
			if info.Size() > maxTreeFileSize {
				return unsafeContent("source file %q exceeds %d bytes", path, maxTreeFileSize)
			}
			totalSize += info.Size()
			if totalSize > maxTreeSize {
				return unsafeContent("source tree %q exceeds %d bytes", source, maxTreeSize)
			}
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, cloud.ErrUnsafeContent) {
			return err
		}
		return unsafeContent("walk source tree %q: %v", source, err)
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return unsafeContent("read source file %q: %v", path, err)
		}
		if isUnsafePayload(contents) {
			return unsafeContent("source file %q contains binary or archive content", path)
		}
	}
	return nil
}

func readPhysicalFile(ctx context.Context, resourcesRoot, source string, sizeLimit int64) ([]byte, fs.FileMode, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if err := validateSourceAncestors(resourcesRoot, source); err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return nil, 0, unsafeContent("lstat source file %q: %v", source, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, 0, unsafeContent("source file %q is not a physical regular file", source)
	}
	if hasMultipleLinks(info) {
		return nil, 0, unsafeContent("source file %q has multiple hard links", source)
	}
	if info.Size() > sizeLimit {
		return nil, 0, unsafeContent("source file %q exceeds %d bytes", source, sizeLimit)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return nil, 0, unsafeContent("read source file %q: %v", source, err)
	}
	if int64(len(contents)) > sizeLimit || isUnsafePayload(contents) {
		return nil, 0, unsafeContent("source file %q contains oversized, binary, or archive content", source)
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return contents, info.Mode().Perm(), nil
}

func validateSourceAncestors(resourcesRoot, source string) error {
	root := filepath.Clean(resourcesRoot)
	source = filepath.Clean(source)
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return unsafeContent("source %q escapes resource root %q", source, root)
	}
	current := root
	components := []string{"."}
	if relative != "." {
		components = append(components, strings.Split(relative, string(filepath.Separator))...)
	}
	for _, component := range components {
		if component != "." {
			current = filepath.Join(current, component)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return unsafeContent("lstat source ancestor %q: %v", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return unsafeContent("source ancestor %q is a symbolic link", current)
		}
		if current != source && !info.IsDir() {
			return unsafeContent("source ancestor %q is not a directory", current)
		}
	}
	return nil
}

func hasMultipleLinks(info fs.FileInfo) bool {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	field := value.FieldByName("Nlink")
	return field.IsValid() && field.CanUint() && field.Uint() > 1
}

func isUnsafePayload(contents []byte) bool {
	archiveSignatures := [][]byte{
		{0x50, 0x4b, 0x03, 0x04},
		{0x50, 0x4b, 0x05, 0x06},
		{0x50, 0x4b, 0x07, 0x08},
		{0x1f, 0x8b},
		{'B', 'Z', 'h'},
		{0xfd, '7', 'z', 'X', 'Z', 0x00},
		{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c},
		{'R', 'a', 'r', '!', 0x1a, 0x07},
		{0x7f, 'E', 'L', 'F'},
	}
	for _, signature := range archiveSignatures {
		if bytes.HasPrefix(contents, signature) {
			return true
		}
	}
	if len(contents) >= 262 && bytes.Equal(contents[257:262], []byte("ustar")) {
		return true
	}
	if !utf8.Valid(contents) || bytes.IndexByte(contents, 0) >= 0 {
		return true
	}
	for _, character := range contents {
		if character < 0x20 && character != '\n' && character != '\r' && character != '\t' && character != '\f' {
			return true
		}
	}
	return false
}

func absoluteDestination(destination string) (string, error) {
	if destination == "" {
		return "", unsafeContent("destination is empty")
	}
	resolved, err := filepath.Abs(destination)
	if err != nil {
		return "", unsafeContent("resolve destination %q: %v", destination, err)
	}
	return filepath.Clean(resolved), nil
}

func validateTreeDestination(destination string) error {
	if err := validateDestinationAncestors(destination); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return unsafeContent("stat destination %q: %v", destination, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return unsafeContent("destination %q is not a physical directory", destination)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return unsafeContent("read destination %q: %v", destination, err)
	}
	if len(entries) != 0 {
		return unsafeContent("destination %q is not empty", destination)
	}
	return nil
}

func validateFileDestination(destination string) error {
	if err := validateDestinationAncestors(filepath.Dir(destination)); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return unsafeContent("stat destination %q: %v", destination, err)
	}
	return unsafeContent("destination %q must not exist", destination)
}

func validateDestinationAncestors(destination string) error {
	volume := filepath.VolumeName(destination)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(destination, current)
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return unsafeContent("stat destination ancestor %q: %v", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return unsafeContent("destination ancestor %q is not a physical directory", current)
		}
	}
	return nil
}

func validateCopyBoundary(source, destination string) error {
	physicalSource, err := fsutil.ResolvePhysicalPath(source)
	if err != nil {
		return unsafeContent("resolve physical source %q: %v", source, err)
	}
	physicalDestination, err := fsutil.ResolvePhysicalPath(destination)
	if err != nil {
		return unsafeContent("resolve physical destination %q: %v", destination, err)
	}
	for _, pair := range [][2]string{{physicalSource, physicalDestination}, {physicalDestination, physicalSource}} {
		overlaps, err := fsutil.PathEqualOrWithin(pair[0], pair[1])
		if err != nil {
			return unsafeContent("compare source %q and destination %q: %v", source, destination, err)
		}
		if overlaps {
			return unsafeContent("source %q and destination %q overlap", source, destination)
		}
	}
	return nil
}

func copyTreeAtomically(ctx context.Context, source, destination, label string) (resultErr error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return fmt.Errorf("create %s destination parent %q: %w", label, filepath.Dir(destination), err)
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), "."+filepath.Base(destination)+".tmp-")
	if err != nil {
		return fmt.Errorf("create %s temporary destination beside %q: %w", label, destination, err)
	}
	defer func() {
		if err := os.RemoveAll(temporary); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove %s temporary destination %q: %w", label, temporary, err))
		}
	}()
	if err := copyTree(ctx, source, temporary); err != nil {
		return fmt.Errorf("copy source %s %q: %w", label, source, err)
	}
	if err := validateTreeDestination(destination); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("remove empty %s destination %q: %w", label, destination, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unsafeContent("recheck %s destination %q: %v", label, destination, err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish %s destination %q: %w", label, destination, err)
	}
	return nil
}

func copyTree(ctx context.Context, source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.Mkdir(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || hasMultipleLinks(info) {
			return unsafeContent("source path %q changed after validation", path)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		return errors.Join(copyErr, closeOutputErr, closeInputErr)
	})
}

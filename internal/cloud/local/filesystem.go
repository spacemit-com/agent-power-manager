package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		{'!', '<', 'a', 'r', 'c', 'h', '>', '\n'},
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

func copyTreeAtomically(ctx context.Context, snapshot *treeSnapshot, destination, label string, beforePublish func()) (resultErr error) {
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
	if err := copyTreeSnapshot(ctx, snapshot, temporary); err != nil {
		return fmt.Errorf("copy source %s snapshot: %w", label, err)
	}
	if beforePublish != nil {
		beforePublish()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateTreeDestination(destination); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("remove empty %s destination %q: %w", label, destination, err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unsafeContent("recheck %s destination %q: %v", label, destination, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish %s destination %q: %w", label, destination, err)
	}
	return nil
}

func copyTreeSnapshot(ctx context.Context, snapshot *treeSnapshot, destination string) error {
	for _, node := range snapshot.nodes {
		if err := ctx.Err(); err != nil {
			return err
		}
		target := filepath.Join(destination, node.relative)
		if node.directory {
			if err := os.Mkdir(target, node.mode.Perm()); err != nil {
				return err
			}
			continue
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, node.mode.Perm())
		if err != nil {
			return err
		}
		copyErr := writeAllContext(ctx, output, node.contents)
		closeOutputErr := output.Close()
		if copyErr != nil || closeOutputErr != nil {
			return errors.Join(copyErr, closeOutputErr)
		}
	}
	return ctx.Err()
}

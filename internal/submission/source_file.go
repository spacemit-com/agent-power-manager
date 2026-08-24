package submission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

const sourceReadChunkSize = 32 << 10

// readPhysicalRegularFile 以 no-follow 方式固定单个文件，并在读取前后校验身份、链接数和大小。
func readPhysicalRegularFile(ctx context.Context, path string, sizeLimit int64) (contents []byte, mode os.FileMode, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	file, err := openPhysicalRegularFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open physical source file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()

	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("source file %q is not a physical regular file: %w", path, errors.Join(ErrInvalid, err))
	}
	if before.Size() < 0 || before.Size() > sizeLimit {
		return nil, 0, fmt.Errorf("source file %q size %d exceeds %d bytes: %w", path, before.Size(), sizeLimit, ErrInvalid)
	}
	if err := requireSinglePhysicalLink(file, before, path); err != nil {
		return nil, 0, err
	}
	if err := verifyPhysicalPathIdentity(path, before); err != nil {
		return nil, 0, err
	}

	contents, err = readPhysicalFileContext(ctx, file, sizeLimit)
	if err != nil {
		return nil, 0, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != before.Size() || int64(len(contents)) != after.Size() {
		return nil, 0, fmt.Errorf("source file %q changed while reading: %w", path, errors.Join(ErrInvalid, err))
	}
	if err := requireSinglePhysicalLink(file, after, path); err != nil {
		return nil, 0, err
	}
	if err := verifyPhysicalPathIdentity(path, after); err != nil {
		return nil, 0, err
	}
	return contents, after.Mode().Perm(), nil
}

func verifyPhysicalPathIdentity(path string, expected os.FileInfo) error {
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return fmt.Errorf("source file %q changed or resolves through a link: %w", path, errors.Join(ErrInvalid, err))
	}
	return nil
}

func requireSinglePhysicalLink(file *os.File, info os.FileInfo, path string) error {
	count, err := sourceFileLinkCount(file, info)
	if err != nil {
		return fmt.Errorf("determine hard-link count for source file %q: %w", path, errors.Join(ErrInvalid, err))
	}
	if count != 1 {
		return fmt.Errorf("source file %q has %d hard links: %w", path, count, ErrInvalid)
	}
	return nil
}

func readPhysicalFileContext(ctx context.Context, source io.Reader, limit int64) ([]byte, error) {
	contents := make([]byte, 0)
	buffer := make([]byte, sourceReadChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := source.Read(buffer)
		if count > 0 {
			if int64(len(contents))+int64(count) > limit {
				return nil, fmt.Errorf("source content exceeds %d bytes: %w", limit, ErrInvalid)
			}
			contents = append(contents, buffer[:count]...)
		}
		if errors.Is(err, io.EOF) {
			return contents, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("read physical source file: %w", err)
		}
	}
}

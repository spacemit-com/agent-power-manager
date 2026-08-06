// Package filesystem 提供内部文件系统工具。
package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyDir 递归复制目录内容并保留权限位。
func CopyDir(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat source directory %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a directory", source)
	}
	if err = os.MkdirAll(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination directory %q: %w", destination, err)
	}

	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read source directory %q: %w", source, err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			if err = CopyDir(sourcePath, destinationPath); err != nil {
				return err
			}
			continue
		}
		if err = copyFile(sourcePath, destinationPath); err != nil {
			return err
		}
	}
	if err = os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set destination directory mode %q: %w", destination, err)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", source, err)
	}
	defer input.Close()

	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %q: %w", source, err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", destination, err)
	}
	if _, err = io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err = output.Close(); err != nil {
		return fmt.Errorf("close destination file %q: %w", destination, err)
	}
	if err = os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set destination file mode %q: %w", destination, err)
	}
	return nil
}

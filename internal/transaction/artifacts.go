package transaction

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	filesystemutil "apm/internal/utils/filesystem"
)

type transactionArtifacts struct {
	dir        string
	backup     string
	quarantine string
}

// newTransactionID 生成可安全用于单个路径段的随机事务 ID。
func newTransactionID() (string, error) {
	contents := make([]byte, 16)
	if _, err := rand.Read(contents); err != nil {
		return "", fmt.Errorf("generate transaction id: %w", err)
	}
	return hex.EncodeToString(contents), nil
}

// artifactPaths 返回与目标同一父目录的事务产物路径。
func artifactPaths(target, transactionID string) (transactionArtifacts, error) {
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return transactionArtifacts{}, err
	}
	if !validTransactionID(transactionID) {
		return transactionArtifacts{}, fmt.Errorf("transaction id %q: %w", transactionID, ErrUnsafePath)
	}
	dir := filepath.Join(filepath.Dir(target), ".apm-transactions", transactionID)
	return transactionArtifacts{
		dir:        dir,
		backup:     filepath.Join(dir, "backup"),
		quarantine: filepath.Join(dir, "quarantine"),
	}, nil
}

// ensureTransactionDir 创建或校验当前事务的私有同盘目录。
func ensureTransactionDir(artifacts transactionArtifacts) error {
	return ensureTransactionDirWithOps(artifacts, defaultFileOps())
}

func ensureTransactionDirWithOps(artifacts transactionArtifacts, files fileOps) error {
	root := filepath.Dir(artifacts.dir)
	if err := ensurePrivateDirectory(root, files); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(artifacts.dir, files); err != nil {
		return err
	}
	return nil
}

func ensurePrivateDirectory(path string, files fileOps) error {
	info, err := files.lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := files.mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create transaction directory %q: %w", path, err)
		}
	case err != nil:
		return fmt.Errorf("inspect transaction directory %q: %w", path, err)
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return fmt.Errorf("transaction directory %q is not a physical directory: %w", path, ErrUnsafePath)
	}
	if err := files.chmod(path, 0o700); err != nil {
		return fmt.Errorf("set transaction directory permissions %q: %w", path, err)
	}
	return nil
}

// cleanupTransactionDir 删除当前事务目录，并仅在共享根为空时删除共享根。
func cleanupTransactionDir(journal Journal) error {
	return cleanupTransactionDirWithOps(journal, defaultFileOps())
}

func cleanupTransactionDirWithOps(journal Journal, files fileOps) error {
	if err := validateJournal(journal); err != nil {
		return err
	}
	artifacts, err := artifactPaths(journal.TargetPath, journal.TransactionID)
	if err != nil {
		return err
	}
	root := filepath.Dir(artifacts.dir)
	if err := validateExistingPhysicalDirectory(root, files); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := validateExistingPhysicalDirectory(artifacts.dir, files); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := files.removeAll(artifacts.dir); err != nil {
		return fmt.Errorf("remove transaction directory %q: %w", artifacts.dir, err)
	}
	entries, err := files.readDir(root)
	if err != nil {
		return fmt.Errorf("read transaction root %q: %w", root, err)
	}
	if len(entries) == 0 {
		if err := files.remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty transaction root %q: %w", root, err)
		}
	}
	return nil
}

func validateExistingPhysicalDirectory(path string, files fileOps) error {
	info, err := files.lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("transaction directory %q is not a physical directory: %w", path, ErrUnsafePath)
	}
	return nil
}

func validateSafeAbsolutePath(name, path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == filepath.VolumeName(path)+string(filepath.Separator) {
		return fmt.Errorf("%s path %q: %w", name, path, ErrUnsafePath)
	}
	return nil
}

// cleanupCommittedStaging 安全清理已越过发布提交点的 canonical staging。
func cleanupCommittedStaging(journal Journal) error {
	return cleanupCommittedStagingWithOps(journal, defaultFileOps())
}

func cleanupCommittedStagingWithOps(journal Journal, files fileOps) error {
	if journal.Operation != OperationInstall {
		return fmt.Errorf("cleanup staging for operation %q: %w", journal.Operation, ErrUnsafePath)
	}
	if err := validateJournal(journal); err != nil {
		return err
	}
	physicalStaging, err := filesystemutil.ResolvePhysicalPath(journal.StagingRoot)
	if err != nil {
		return fmt.Errorf("resolve physical staging %q: %w", journal.StagingRoot, err)
	}
	physicalTarget, err := filesystemutil.ResolvePhysicalPath(journal.TargetPath)
	if err != nil {
		return fmt.Errorf("resolve physical target %q: %w", journal.TargetPath, err)
	}
	targetWithinStaging, err := filesystemutil.PathEqualOrWithin(physicalStaging, physicalTarget)
	if err != nil {
		return fmt.Errorf("compare staging and target: %w", err)
	}
	stagingWithinTarget, err := filesystemutil.PathEqualOrWithin(physicalTarget, physicalStaging)
	if err != nil {
		return fmt.Errorf("compare target and staging: %w", err)
	}
	if targetWithinStaging || stagingWithinTarget {
		return fmt.Errorf("physical staging %q overlaps target %q: %w", physicalStaging, physicalTarget, ErrUnsafePath)
	}
	if err := files.removeAll(journal.StagingRoot); err != nil {
		return fmt.Errorf("remove committed staging %q: %w", journal.StagingRoot, err)
	}
	return nil
}

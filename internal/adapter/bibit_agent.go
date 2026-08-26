package adapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"apm/internal/agentartifact"
	"apm/internal/domain"
)

// bibitAgentAdapter 实现从公共 staging 到 Bibit agents 目录的完整目录 Agent 安装流程。
type bibitAgentAdapter struct {
	profile   agentProfile
	copyDir   func(string, string) error
	mkdirAll  func(string, os.FileMode) error
	rename    func(string, string) error
	removeAll func(string) error
}

func newBibitAgentAdapter(profile agentProfile) *bibitAgentAdapter {
	return &bibitAgentAdapter{
		profile:   profile,
		copyDir:   copyAgentDirNormalized,
		mkdirAll:  os.MkdirAll,
		rename:    os.Rename,
		removeAll: os.RemoveAll,
	}
}

// TargetPath 返回指定 Agent 的最终安装目录路径 root/agents/<ref.Name>；
// 目录名使用 ref.Name（外部身份），不含版本目录，亦无 agent.yaml 后缀。
func (a *bibitAgentAdapter) TargetPath(ref domain.PowerRef) (string, error) {
	if ref.Type != domain.PowerTypeAgent || !isSafeSkillName(ref.Name) {
		return "", fmt.Errorf("target path for agent %q power %q: %w", a.profile.id, ref.Name, ErrInvalidAgent)
	}
	root, err := a.profile.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agents", ref.Name), nil
}

// ValidateArtifact 只读校验完整 Agent 目录：物理安全、根闭集、avatar PNG 以及
// agent.name/version 身份与 ref 完全一致，可作用于 staging/target/backup/
// quarantine 目录，不发布或改写任何文件。
func (a *bibitAgentAdapter) ValidateArtifact(ref domain.PowerRef, path string) error {
	if _, err := agentartifact.ValidateDirectory(context.Background(), path, &ref); err != nil {
		return err
	}
	return nil
}

// Install 从公共 staging 校验完整 Agent 目录（staging/agents/<ref.Name>），
// 复制到 Bibit agents 目录下的私有目录并再次校验，最后以不覆盖语义原子发布
// 到 root/agents/<ref.Name>；任何发布前失败都清理私有与 staging 路径。
func (a *bibitAgentAdapter) Install(ref domain.PowerRef, staging string) (result AgentInstallResult, resultErr error) {
	stagingRoot, err := validateStagingIdentity(staging)
	if err != nil {
		return result, a.installError(ref, "staging", staging, err)
	}
	defer func() {
		if err := a.removeAll(stagingRoot); err != nil {
			resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", stagingRoot, err))
		}
	}()

	if ref.Type != domain.PowerTypeAgent || !isSafeSkillName(ref.Name) || ref.Version == "" {
		return result, a.installError(ref, "validation", stagingRoot, ErrInvalidAgent)
	}

	root, err := a.profile.rootPath()
	if err != nil {
		return result, a.installError(ref, "detection", stagingRoot, err)
	}
	if err := ensureBibitAgentRoot(root); err != nil {
		return result, a.installError(ref, "detection", root, err)
	}

	source := filepath.Join(stagingRoot, "agents", ref.Name)
	if _, err := agentartifact.ValidateDirectory(context.Background(), source, &ref); err != nil {
		return result, a.installError(ref, "validation", source, err)
	}

	target := filepath.Join(root, "agents", ref.Name)
	if err := ensureAgentTargetAbsent(target); err != nil {
		return result, a.installError(ref, "detection", target, err)
	}
	agentsRoot := filepath.Dir(target)
	if err := a.mkdirAll(agentsRoot, 0o755); err != nil {
		return result, a.installError(ref, "directory", agentsRoot, err)
	}
	tempDir, err := os.MkdirTemp(agentsRoot, ".apm-"+ref.Name+"-*")
	if err != nil {
		return result, a.installError(ref, "temporary", agentsRoot, err)
	}
	defer func() {
		if tempDir != "" {
			if err := a.removeAll(tempDir); err != nil {
				resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", tempDir, err))
			}
		}
	}()

	if err := a.copyDir(source, tempDir); err != nil {
		return result, a.installError(ref, "copy", source, fmt.Errorf("copy to %q: %w", tempDir, err))
	}
	if _, err := agentartifact.ValidateDirectory(context.Background(), tempDir, &ref); err != nil {
		return result, a.installError(ref, "validation", tempDir, err)
	}
	if err := ensureAgentTargetAbsent(target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	if err := a.rename(tempDir, target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	tempDir = ""
	result.Published = true
	return result, nil
}

func (a *bibitAgentAdapter) installError(ref domain.PowerRef, phase, path string, err error) error {
	return fmt.Errorf("install agent %q power %q phase %q path %q: %w", a.profile.id, ref.Name, phase, path, err)
}

// ensureBibitAgentRoot 确保 Bibit 根目录存在且为目录。
func ensureBibitAgentRoot(root string) error {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return ErrAgentNotInstalled
	}
	if err != nil {
		return fmt.Errorf("stat agent root %q: %w", root, err)
	}
	if !info.IsDir() {
		return ErrAgentNotInstalled
	}
	return nil
}

// ensureAgentTargetAbsent 确保最终目标目录不存在任何文件系统条目。
func ensureAgentTargetAbsent(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Errorf("agent target %q: %w", target, ErrTargetConflict)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("lstat agent target %q: %w", target, err)
}

// copyAgentDirNormalized 递归复制目录树，文件统一 0644、目录统一 0755。
func copyAgentDirNormalized(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat source directory %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a directory", source)
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
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
			if err := copyAgentDirNormalized(sourcePath, destinationPath); err != nil {
				return err
			}
			continue
		}
		if err := copyAgentFileNormalized(sourcePath, destinationPath); err != nil {
			return err
		}
	}
	return nil
}

// copyAgentFileNormalized 以 0644 模式复制单个普通文件。
func copyAgentFileNormalized(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", source, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", destination, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close destination file %q: %w", destination, err)
	}
	return nil
}

var _ AgentAdapter = (*bibitAgentAdapter)(nil)

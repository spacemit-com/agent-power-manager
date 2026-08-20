package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"apm/internal/domain"
)

// bibitAgentAdapter 实现从公共 staging 到 Bibit agents 目录的单文件 Agent 安装流程。
type bibitAgentAdapter struct {
	profile   agentProfile
	readFile  func(string) ([]byte, error)
	mkdirAll  func(string, os.FileMode) error
	rename    func(string, string) error
	removeAll func(string) error
}

func newBibitAgentAdapter(profile agentProfile) *bibitAgentAdapter {
	return &bibitAgentAdapter{
		profile:   profile,
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		rename:    os.Rename,
		removeAll: os.RemoveAll,
	}
}

// TargetPath 返回指定 Agent 的最终安装文件路径；目录名使用 ref.Name（Cloud 保证等于 agent.id）。
func (a *bibitAgentAdapter) TargetPath(ref domain.PowerRef) (string, error) {
	if ref.Type != domain.PowerTypeAgent || !isSafeSkillName(ref.Name) {
		return "", fmt.Errorf("target path for agent %q power %q: %w", a.profile.id, ref.Name, ErrInvalidAgent)
	}
	root, err := a.profile.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agents", ref.Name, "agent.yaml"), nil
}

// Install 从公共 staging 读取并校验 agent.yaml，再原子发布单文件到 Bibit agents 目录。
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

	source := filepath.Join(stagingRoot, "agents", ref.Name, "agent.yaml")
	raw, err := a.readFile(source)
	if err != nil {
		return result, a.installError(ref, "read", source, err)
	}
	var config domain.AgentConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return result, a.installError(ref, "parse", source, fmt.Errorf("%w: %v", ErrInvalidAgent, err))
	}
	if err := config.Validate(); err != nil {
		return result, a.installError(ref, "validation", source, err)
	}

	target := filepath.Join(root, "agents", ref.Name, "agent.yaml")
	if err := ensureAgentTargetAbsent(target); err != nil {
		return result, a.installError(ref, "detection", target, err)
	}
	if err := a.mkdirAll(filepath.Dir(target), 0o755); err != nil {
		return result, a.installError(ref, "directory", filepath.Dir(target), err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".apm-"+ref.Name+"-*")
	if err != nil {
		return result, a.installError(ref, "temporary", filepath.Dir(target), err)
	}
	tempName := temp.Name()
	cleanupTemp := func() {
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}
	defer cleanupTemp()
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return result, a.installError(ref, "write", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return result, a.installError(ref, "write", tempName, err)
	}
	if err := a.rename(tempName, target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	tempName = ""
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

// ensureAgentTargetAbsent 确保最终目标文件不存在任何文件系统条目。
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

var _ AgentAdapter = (*bibitAgentAdapter)(nil)

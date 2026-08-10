package adapter

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"
)

// skillAdapter 实现从公共 staging 到 Agent 全局目录的 Skill 安装流程。
type skillAdapter struct {
	config    agentConfig
	copyDir   func(string, string) error
	rename    func(string, string) error
	removeAll func(string) error
}

// newSkillAdapter 使用指定 Agent 配置创建 Skill Adapter。
func newSkillAdapter(config agentConfig) *skillAdapter {
	return &skillAdapter{
		config:    config,
		copyDir:   filesystem.CopyDir,
		rename:    os.Rename,
		removeAll: os.RemoveAll,
	}
}

// DetectAgent 报告目标 Agent 是否已安装。
func (a *skillAdapter) DetectAgent() (bool, error) {
	return a.config.detectAgent()
}

// TargetPath 返回指定 Skill 的最终安装路径。
func (a *skillAdapter) TargetPath(ref domain.PowerRef) (string, error) {
	return a.config.targetPath(ref)
}

// Install 从公共 staging 复制并原子发布指定 Skill，随后清理 staging。
func (a *skillAdapter) Install(ref domain.PowerRef, staging string) (result InstallResult, resultErr error) {
	stagingRoot, err := validateStagingIdentity(staging)
	if err != nil {
		return result, a.installError(ref, "staging", staging, err)
	}
	root, err := a.config.rootPath()
	if err != nil {
		return result, a.installError(ref, "safety", stagingRoot, err)
	}
	skillsRoot := filepath.Join(root, "skills")
	if err := ensurePhysicalInstallRootsDisjoint(stagingRoot, skillsRoot); err != nil {
		return result, a.installError(ref, "safety", skillsRoot, err)
	}
	defer func() {
		a.debugInstall(ref, "cleanup", stagingRoot)
		if err := a.removeAll(stagingRoot); err != nil {
			resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", stagingRoot, err))
		}
	}()

	if err := validateVersionedSkillRef(ref); err != nil {
		return result, a.installError(ref, "validation", stagingRoot, err)
	}
	sourceDir := filepath.Join(stagingRoot, "skills", ref.Name)

	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, a.installError(ref, "detection", root, ErrAgentNotInstalled)
	}
	if err != nil {
		return result, a.installError(ref, "detection", root, err)
	}
	if !info.IsDir() {
		return result, a.installError(ref, "detection", root, ErrAgentNotInstalled)
	}

	target := filepath.Join(root, "skills", ref.Name)
	a.debugInstall(ref, "detection", target)
	if err := ensureTargetAbsent(target); err != nil {
		return result, a.installError(ref, "detection", target, err)
	}
	frontmatterPath := filepath.Join(sourceDir, "SKILL.md")
	if err := validateSkillFrontmatter(frontmatterPath, ref.Name); err != nil {
		return result, a.installError(ref, "frontmatter", frontmatterPath, err)
	}
	a.debugInstall(ref, "adaptation", sourceDir)
	if err := a.config.adapt(ref, sourceDir); err != nil {
		return result, a.installError(ref, "adaptation", sourceDir, err)
	}

	if err := ensureSkillsRoot(skillsRoot); err != nil {
		return result, a.installError(ref, "skills-root", skillsRoot, err)
	}
	tempDir, err := os.MkdirTemp(skillsRoot, ".apm-"+ref.Name+"-*")
	if err != nil {
		return result, a.installError(ref, "temporary", skillsRoot, err)
	}
	defer func() {
		if tempDir != "" {
			a.debugInstall(ref, "cleanup", tempDir)
			if err := a.removeAll(tempDir); err != nil {
				resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", tempDir, err))
			}
		}
	}()

	a.debugInstall(ref, "copy", tempDir)
	if err := a.copyDir(sourceDir, tempDir); err != nil {
		return result, a.installError(ref, "copy", sourceDir, fmt.Errorf("copy to %q: %w", tempDir, err))
	}
	a.debugInstall(ref, "publication", target)
	if err := ensureTargetAbsent(target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	if err := a.rename(tempDir, target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	result.Published = true
	tempDir = ""
	return result, nil
}

func (a *skillAdapter) installError(ref domain.PowerRef, phase, path string, err error) error {
	return fmt.Errorf("install agent %q power %q phase %q path %q: %w", a.config.id, ref.Name, phase, path, err)
}

func (a *skillAdapter) debugInstall(ref domain.PowerRef, phase, path string) {
	slog.Debug(
		"install skill",
		logging.Agent(a.config.id),
		logging.Power(ref.Name),
		logging.Version(ref.Version),
		logging.Phase(phase),
		slog.String("path", path),
	)
}

// validateStagingIdentity 校验并返回允许清理的公共 staging 绝对路径。
func validateStagingIdentity(staging string) (string, error) {
	if staging == "" {
		return "", fmt.Errorf("staging path is empty: %w", ErrInvalidSkill)
	}
	expected, err := paths.StagingPath()
	if err != nil {
		return "", fmt.Errorf("resolve staging path: %w", err)
	}
	expected, err = filepath.Abs(filepath.Clean(expected))
	if err != nil {
		return "", fmt.Errorf("resolve expected staging path: %w", err)
	}
	actual, err := filepath.Abs(filepath.Clean(staging))
	if err != nil {
		return "", fmt.Errorf("resolve staging path %q: %w", staging, err)
	}
	if actual == filesystemVolumeRoot(actual) || actual != expected {
		return "", fmt.Errorf("staging path %q: %w", staging, ErrInvalidSkill)
	}
	return actual, nil
}

func filesystemVolumeRoot(path string) string {
	return filepath.VolumeName(path) + string(filepath.Separator)
}

// ensurePhysicalInstallRootsDisjoint 拒绝物理 Skill 根等于或位于 staging 内的布局。
func ensurePhysicalInstallRootsDisjoint(stagingRoot, skillsRoot string) error {
	physicalStaging, err := filesystem.ResolvePhysicalPath(stagingRoot)
	if err != nil {
		return fmt.Errorf("resolve physical staging root %q: %w", stagingRoot, err)
	}
	physicalSkills, err := filesystem.ResolvePhysicalPath(skillsRoot)
	if err != nil {
		return fmt.Errorf("resolve physical skill root %q: %w", skillsRoot, err)
	}

	contained, err := filesystem.PathEqualOrWithin(physicalStaging, physicalSkills)
	if err != nil {
		return fmt.Errorf("compare physical staging root %q and skill root %q: %w", physicalStaging, physicalSkills, err)
	}
	if contained {
		return fmt.Errorf(
			"physical skill root %q overlaps staging root %q: %w",
			physicalSkills,
			physicalStaging,
			ErrInvalidSkill,
		)
	}
	return nil
}

// validateVersionedSkillRef 校验版本已确定且名称安全的 Skill 引用。
func validateVersionedSkillRef(ref domain.PowerRef) error {
	if ref.Type != domain.PowerTypeSkill || !isSafeSkillName(ref.Name) || strings.TrimSpace(ref.Version) == "" {
		return fmt.Errorf("skill reference %+v: %w", ref, ErrInvalidSkill)
	}
	return nil
}

// ensureTargetAbsent 确保最终目标不存在任何文件系统条目。
func ensureTargetAbsent(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Errorf("skill target %q: %w", target, ErrTargetConflict)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("lstat skill target %q: %w", target, err)
}

// ensureSkillsRoot 创建缺失的单层 Skill 根目录，或校验已有目录。
func ensureSkillsRoot(skillsRoot string) error {
	if err := os.Mkdir(skillsRoot, 0o755); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create skills root %q: %w", skillsRoot, err)
	}

	info, err := os.Stat(skillsRoot)
	if err != nil {
		return fmt.Errorf("stat skills root %q: %w", skillsRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("skills root %q is not a directory", skillsRoot)
	}
	return nil
}

var _ Adapter = (*skillAdapter)(nil)

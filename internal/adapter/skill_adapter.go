package adapter

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"apm/internal/domain"
	"apm/internal/logging"
	"apm/internal/paths"
	"apm/internal/utils/filesystem"
)

type skillAdapter struct {
	config    agentConfig
	copyDir   func(string, string) error
	rename    func(string, string) error
	removeAll func(string) error
}

func newSkillAdapter(config agentConfig) *skillAdapter {
	return &skillAdapter{
		config:    config,
		copyDir:   filesystem.CopyDir,
		rename:    os.Rename,
		removeAll: os.RemoveAll,
	}
}

func (a *skillAdapter) DetectAgent() (bool, error) {
	return a.config.detectAgent()
}

func (a *skillAdapter) TargetPath(ref domain.PowerRef) (string, error) {
	return a.config.targetPath(ref)
}

func (a *skillAdapter) Install(ref domain.PowerRef, staging string) (resultErr error) {
	stagingRoot, err := validateStagingIdentity(staging)
	if err != nil {
		return a.installError(ref, "staging", staging, err)
	}
	root, err := a.config.rootPath()
	if err != nil {
		return a.installError(ref, "safety", stagingRoot, err)
	}
	skillsRoot := filepath.Join(root, "skills")
	if err := ensurePhysicalInstallRootsDisjoint(stagingRoot, skillsRoot); err != nil {
		return a.installError(ref, "safety", skillsRoot, err)
	}
	defer func() {
		a.debugInstall(ref, "cleanup", stagingRoot)
		if err := a.removeAll(stagingRoot); err != nil {
			resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", stagingRoot, err))
		}
	}()

	if err := validateVersionedSkillRef(ref); err != nil {
		return a.installError(ref, "validation", stagingRoot, err)
	}
	sourceDir := filepath.Join(stagingRoot, "skills", ref.Name)

	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return a.installError(ref, "detection", root, ErrAgentNotInstalled)
	}
	if err != nil {
		return a.installError(ref, "detection", root, err)
	}
	if !info.IsDir() {
		return a.installError(ref, "detection", root, ErrAgentNotInstalled)
	}

	target := filepath.Join(root, "skills", ref.Name)
	a.debugInstall(ref, "detection", target)
	if err := ensureTargetAbsent(target); err != nil {
		return a.installError(ref, "detection", target, err)
	}
	frontmatterPath := filepath.Join(sourceDir, "SKILL.md")
	if err := validateSkillFrontmatter(frontmatterPath, ref.Name); err != nil {
		return a.installError(ref, "frontmatter", frontmatterPath, err)
	}
	a.debugInstall(ref, "adaptation", sourceDir)
	if err := a.config.adapt(ref, sourceDir); err != nil {
		return a.installError(ref, "adaptation", sourceDir, err)
	}

	if err := ensureSkillsRoot(skillsRoot); err != nil {
		return a.installError(ref, "skills-root", skillsRoot, err)
	}
	tempDir, err := os.MkdirTemp(skillsRoot, ".apm-"+ref.Name+"-*")
	if err != nil {
		return a.installError(ref, "temporary", skillsRoot, err)
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
		return a.installError(ref, "copy", sourceDir, fmt.Errorf("copy to %q: %w", tempDir, err))
	}
	a.debugInstall(ref, "publication", target)
	if err := ensureTargetAbsent(target); err != nil {
		return a.installError(ref, "publication", target, err)
	}
	if err := a.rename(tempDir, target); err != nil {
		return a.installError(ref, "publication", target, err)
	}
	tempDir = ""
	return nil
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

func ensurePhysicalInstallRootsDisjoint(stagingRoot, skillsRoot string) error {
	physicalStaging, err := resolvePhysicalPath(stagingRoot)
	if err != nil {
		return fmt.Errorf("resolve physical staging root %q: %w", stagingRoot, err)
	}
	physicalSkills, err := resolvePhysicalPath(skillsRoot)
	if err != nil {
		return fmt.Errorf("resolve physical skill root %q: %w", skillsRoot, err)
	}

	contained, err := pathEqualOrWithin(physicalStaging, physicalSkills)
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

func resolvePhysicalPath(path string) (string, error) {
	return resolvePhysicalPathDepth(path, 0)
}

func resolvePhysicalPathDepth(path string, depth int) (string, error) {
	if depth > 255 {
		return "", fmt.Errorf("resolve path %q: too many symbolic links", path)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}

	current := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", fmt.Errorf("evaluate symlinks for %q: %w", current, err)
		}

		info, lstatErr := os.Lstat(current)
		if lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readlinkErr := os.Readlink(current)
			if readlinkErr != nil {
				return "", fmt.Errorf("read symlink %q: %w", current, readlinkErr)
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			resolved, resolveErr := resolvePhysicalPathDepth(target, depth+1)
			if resolveErr != nil {
				return "", resolveErr
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) && !errors.Is(lstatErr, syscall.ENOTDIR) {
			return "", fmt.Errorf("inspect path %q: %w", current, lstatErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("resolve existing path prefix for %q: %w", absolute, err)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathEqualOrWithin(parent, path string) (bool, error) {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}

func validateVersionedSkillRef(ref domain.PowerRef) error {
	if ref.Type != domain.PowerTypeSkill || !isSafeSkillName(ref.Name) || strings.TrimSpace(ref.Version) == "" {
		return fmt.Errorf("skill reference %+v: %w", ref, ErrInvalidSkill)
	}
	return nil
}

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

package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/domain"
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
	stagingPath, err := validateStagingIdentity(ref, staging)
	if err != nil {
		return err
	}
	defer func() {
		if err := a.removeAll(stagingPath); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove staging path %q: %w", stagingPath, err))
		}
	}()

	if err := validateVersionedSkillRef(ref); err != nil {
		return err
	}

	root, err := a.config.rootPath()
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agent %q root %q: %w", a.config.id, root, ErrAgentNotInstalled)
	}
	if err != nil {
		return fmt.Errorf("stat agent %q root %q: %w", a.config.id, root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("agent %q root %q: %w", a.config.id, root, ErrAgentNotInstalled)
	}

	target := filepath.Join(root, "skills", ref.Name)
	if err := ensureTargetAbsent(target); err != nil {
		return err
	}
	if err := validateSkillFrontmatter(filepath.Join(stagingPath, "SKILL.md"), ref.Name); err != nil {
		return err
	}
	if err := a.config.adapt(ref, stagingPath); err != nil {
		return fmt.Errorf("adapt agent %q skill %q: %w", a.config.id, ref.Name, err)
	}

	skillsRoot := filepath.Join(root, "skills")
	if err := ensureSkillsRoot(skillsRoot); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(skillsRoot, ".apm-"+ref.Name+"-*")
	if err != nil {
		return fmt.Errorf("create temporary skill directory in %q: %w", skillsRoot, err)
	}
	published := false
	defer func() {
		if !published {
			if err := a.removeAll(temporary); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary skill directory %q: %w", temporary, err))
			}
		}
	}()

	if err := a.copyDir(stagingPath, temporary); err != nil {
		return fmt.Errorf("copy skill staging path %q to %q: %w", stagingPath, temporary, err)
	}
	if err := ensureTargetAbsent(target); err != nil {
		return err
	}
	if err := a.rename(temporary, target); err != nil {
		return fmt.Errorf("publish skill %q to %q: %w", ref.Name, target, err)
	}
	published = true
	return nil
}

func validateStagingIdentity(ref domain.PowerRef, staging string) (string, error) {
	if staging == "" {
		return "", fmt.Errorf("staging path is empty: %w", ErrInvalidSkill)
	}
	stagingRoot, err := paths.StagingPath()
	if err != nil {
		return "", fmt.Errorf("resolve staging path: %w", err)
	}
	skillsRoot, err := filepath.Abs(filepath.Clean(filepath.Join(stagingRoot, "skills")))
	if err != nil {
		return "", fmt.Errorf("resolve staging skills path: %w", err)
	}
	expected, err := filepath.Abs(filepath.Clean(filepath.Join(skillsRoot, ref.Name)))
	if err != nil {
		return "", fmt.Errorf("resolve expected staging path: %w", err)
	}
	relative, err := filepath.Rel(skillsRoot, expected)
	if err != nil {
		return "", fmt.Errorf("relativize expected staging path: %w", err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || filepath.Dir(relative) != "." {
		return "", fmt.Errorf("staging path %q: %w", staging, ErrInvalidSkill)
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

package pluginartifact

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
)

const apcPluginManifestPath = "apc-plugin.json"

// APCPluginManifest 定义 Cloud Plugin v1 的唯一权威索引；Skills 只允许一层相对目录。
type APCPluginManifest struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	Skills        []string `json:"skills"`
}

// LoadAPCPluginManifest 严格读取并复验精确 Plugin 身份及声明 Skill 闭集。
// 调用方必须传入尚未发布或受 lifecycle 全局锁保护的物理 Plugin 根。
func LoadAPCPluginManifest(ref domain.PowerRef, pluginPath string) (APCPluginManifest, error) {
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return APCPluginManifest{}, errors.Join(ErrInvalid, err)
	}
	if err := ValidatePhysicalTree(pluginPath); err != nil {
		return APCPluginManifest{}, err
	}

	manifestPath := filepath.Join(pluginPath, apcPluginManifestPath)
	if err := requirePhysicalRegularFile(manifestPath); err != nil {
		return APCPluginManifest{}, err
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return APCPluginManifest{}, fmt.Errorf("read APC Plugin manifest %q: %w", manifestPath, errors.Join(ErrInvalid, err))
	}
	var manifest APCPluginManifest
	if err := jsonstrict.Decode(contents, &manifest, true); err != nil {
		return APCPluginManifest{}, fmt.Errorf("decode APC Plugin manifest %q: %w", manifestPath, errors.Join(ErrInvalid, err))
	}
	if manifest.SchemaVersion != 1 {
		return APCPluginManifest{}, fmt.Errorf("APC Plugin schema_version %d, want 1: %w", manifest.SchemaVersion, ErrInvalid)
	}
	if manifest.Name != logicalName || manifest.Version != ref.Version {
		return APCPluginManifest{}, fmt.Errorf("APC Plugin identity %q@%q does not match %q@%q: %w", manifest.Name, manifest.Version, logicalName, ref.Version, ErrInvalid)
	}
	if strings.TrimSpace(manifest.Description) == "" {
		return APCPluginManifest{}, fmt.Errorf("APC Plugin description is empty: %w", ErrInvalid)
	}
	if len(manifest.Skills) == 0 {
		return APCPluginManifest{}, fmt.Errorf("APC Plugin skills is empty: %w", ErrInvalid)
	}

	declared := make(map[string]struct{}, len(manifest.Skills))
	for _, skillPath := range manifest.Skills {
		skillName, ok := canonicalAPCSkillName(skillPath)
		if !ok {
			return APCPluginManifest{}, fmt.Errorf("APC Plugin skill path %q is not canonical skills/<name>: %w", skillPath, ErrInvalid)
		}
		if _, duplicate := declared[skillName]; duplicate {
			return APCPluginManifest{}, fmt.Errorf("APC Plugin skill path %q is duplicated: %w", skillPath, ErrInvalid)
		}
		declared[skillName] = struct{}{}
	}
	if err := validateDeclaredSkillWorld(pluginPath, declared); err != nil {
		return APCPluginManifest{}, err
	}

	manifest.Skills = append([]string(nil), manifest.Skills...)
	return manifest, nil
}

func canonicalAPCSkillName(skillPath string) (string, bool) {
	if skillPath == "" || path.IsAbs(skillPath) || strings.Contains(skillPath, `\`) || path.Clean(skillPath) != skillPath {
		return "", false
	}
	parts := strings.Split(skillPath, "/")
	if len(parts) != 2 || parts[0] != "skills" {
		return "", false
	}
	name := parts[1]
	if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name {
		return "", false
	}
	return name, true
}

func validateDeclaredSkillWorld(pluginPath string, declared map[string]struct{}) error {
	skillsRoot := filepath.Join(pluginPath, "skills")
	info, err := os.Lstat(skillsRoot)
	if err != nil {
		return fmt.Errorf("lstat APC Plugin skills root %q: %w", skillsRoot, errors.Join(ErrInvalid, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("APC Plugin skills root %q is not a physical directory: %w", skillsRoot, ErrInvalid)
	}
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return fmt.Errorf("read APC Plugin skills root %q: %w", skillsRoot, errors.Join(ErrInvalid, err))
	}
	if len(entries) != len(declared) {
		return fmt.Errorf("APC Plugin skills root contains %d entries, want %d: %w", len(entries), len(declared), ErrInvalid)
	}
	for _, entry := range entries {
		if _, ok := declared[entry.Name()]; !ok {
			return fmt.Errorf("APC Plugin skills root contains undeclared entry %q: %w", entry.Name(), ErrInvalid)
		}
		skillRoot := filepath.Join(skillsRoot, entry.Name())
		entryInfo, err := os.Lstat(skillRoot)
		if err != nil {
			return fmt.Errorf("lstat APC Plugin skill root %q: %w", skillRoot, errors.Join(ErrInvalid, err))
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.IsDir() {
			return fmt.Errorf("APC Plugin skill root %q is not a physical directory: %w", skillRoot, ErrInvalid)
		}
		for _, marker := range []string{"SKILL.md", "skill-card.md"} {
			if err := requirePhysicalRegularFile(filepath.Join(skillRoot, marker)); err != nil {
				return err
			}
		}
	}
	return nil
}

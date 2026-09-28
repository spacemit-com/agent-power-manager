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

// APCPluginManifest 定义 Cloud Plugin 的唯一权威索引；Skills 只允许一层相对目录。
// v2 起新增仅用于展示的 NameZH/NameEN/DescriptionZH，name 仍是唯一调用名。
type APCPluginManifest struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Description   string   `json:"description"`
	NameZH        string   `json:"name_zh,omitempty"`
	NameEN        string   `json:"name_en,omitempty"`
	DescriptionZH string   `json:"description_zh,omitempty"`
	Skills        []string `json:"skills"`
}

// ValidateAPCPluginManifest 校验内存中的 APC Plugin 权威索引；
// 本函数不访问文件系统，供已固定的 immutable snapshot 复用完整身份与声明语义。
// 安装、下载与目录解码路径复用本函数，因此兼容 v1 与 v2；v2 若提供本地化展示
// 字段则校验形状，缺失不报错（ADR-038）。
func ValidateAPCPluginManifest(ref domain.PowerRef, manifest APCPluginManifest) error {
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return errors.Join(ErrInvalid, err)
	}
	if err := domain.ValidateInstallSchemaVersion(manifest.SchemaVersion); err != nil {
		return fmt.Errorf("APC Plugin %w", errors.Join(ErrInvalid, err))
	}
	if manifest.Name != logicalName || manifest.Version != ref.Version {
		return fmt.Errorf("APC Plugin identity %q@%q does not match %q@%q: %w", manifest.Name, manifest.Version, logicalName, ref.Version, ErrInvalid)
	}
	if strings.TrimSpace(manifest.Description) == "" {
		return fmt.Errorf("APC Plugin description is empty: %w", ErrInvalid)
	}
	if err := domain.ValidateOptionalDisplayMetadata(manifest.NameZH, manifest.NameEN, manifest.DescriptionZH); err != nil {
		return fmt.Errorf("apc-plugin.json %w", errors.Join(ErrInvalid, err))
	}
	if len(manifest.Skills) == 0 {
		return fmt.Errorf("APC Plugin skills is empty: %w", ErrInvalid)
	}

	declared := make(map[string]struct{}, len(manifest.Skills))
	for _, skillPath := range manifest.Skills {
		skillName, ok := canonicalAPCSkillName(skillPath)
		if !ok {
			return fmt.Errorf("APC Plugin skill path %q is not canonical skills/<name>: %w", skillPath, ErrInvalid)
		}
		if _, duplicate := declared[skillName]; duplicate {
			return fmt.Errorf("APC Plugin skill path %q is duplicated: %w", skillPath, ErrInvalid)
		}
		declared[skillName] = struct{}{}
	}
	return nil
}

// ValidateSubmitAPCPluginManifest 校验提交侧 APC Plugin 权威索引：只接受 schema_version 2，
// 且本地化展示字段必填。安装与目录路径不得调用本函数，否则已发布的 v1 资源将无法安装。
func ValidateSubmitAPCPluginManifest(ref domain.PowerRef, manifest APCPluginManifest) error {
	if err := domain.ValidateSubmitSchemaVersion(manifest.SchemaVersion); err != nil {
		return fmt.Errorf("apc-plugin.json %w", errors.Join(ErrInvalid, err))
	}
	if err := ValidateAPCPluginManifest(ref, manifest); err != nil {
		return err
	}
	if err := domain.ValidateRequiredDisplayMetadata(manifest.NameZH, manifest.NameEN, manifest.DescriptionZH); err != nil {
		return fmt.Errorf("apc-plugin.json %w", errors.Join(ErrInvalid, err))
	}
	return nil
}

// LoadAPCPluginManifest 严格读取并复验精确 Plugin 身份及声明 Skill 闭集。
// 调用方必须传入尚未发布或受 lifecycle 全局锁保护的物理 Plugin 根。
func LoadAPCPluginManifest(ref domain.PowerRef, pluginPath string) (APCPluginManifest, error) {
	if err := ValidatePhysicalTree(pluginPath); err != nil {
		return APCPluginManifest{}, err
	}

	manifestPath := filepath.Join(pluginPath, apcPluginManifestPath)
	contents, err := readExactRequiredFile(pluginPath, apcPluginManifestPath)
	if err != nil {
		return APCPluginManifest{}, fmt.Errorf("read APC Plugin manifest %q: %w", manifestPath, errors.Join(ErrInvalid, err))
	}
	var manifest APCPluginManifest
	if err := jsonstrict.Decode(contents, &manifest, true); err != nil {
		return APCPluginManifest{}, fmt.Errorf("decode APC Plugin manifest %q: %w", manifestPath, errors.Join(ErrInvalid, err))
	}
	if err := ValidateAPCPluginManifest(ref, manifest); err != nil {
		return APCPluginManifest{}, err
	}
	declared := make(map[string]struct{}, len(manifest.Skills))
	for _, skillPath := range manifest.Skills {
		skillName, _ := canonicalAPCSkillName(skillPath)
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
	if !domain.IsCanonicalPowerName(name) {
		return "", false
	}
	return name, true
}

func validateDeclaredSkillWorld(pluginPath string, declared map[string]struct{}) error {
	skillsRoot, err := requireExactPhysicalDirectory(pluginPath, "skills")
	if err != nil {
		return fmt.Errorf("validate APC Plugin skills root: %w", err)
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
		skillRelative := path.Join("skills", entry.Name())
		if _, err := requireExactPhysicalDirectory(pluginPath, skillRelative); err != nil {
			return fmt.Errorf("validate APC Plugin skill root %q: %w", filepath.Join(pluginPath, filepath.FromSlash(skillRelative)), err)
		}
		for _, marker := range []string{"SKILL.md", "skill-card.md"} {
			if _, err := requireExactPhysicalRegularFile(pluginPath, path.Join(skillRelative, marker)); err != nil {
				return err
			}
		}
	}
	return nil
}

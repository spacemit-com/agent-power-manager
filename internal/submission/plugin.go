package submission

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/jsonstrict"
)

func validatePlugin(ctx context.Context, request Request) (Validation, error) {
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	stats, err := validateSourceTree(request.Path)
	if err != nil {
		return Validation{}, err
	}
	identity, err := readPluginIdentity(request.Path)
	if err != nil {
		return Validation{}, err
	}
	ref := domain.PowerRef{Type: domain.PowerTypePlugin, Name: identity.Name, Version: identity.Version}
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, request.Path)
	if err != nil {
		return Validation{}, fmt.Errorf("validate plugin artifact: %w", errors.Join(ErrInvalid, err))
	}
	for _, skillPath := range manifest.Skills {
		skillRoot := filepath.Join(request.Path, filepath.FromSlash(skillPath))
		if _, _, err := validateSkillContent(skillRoot); err != nil {
			return Validation{}, fmt.Errorf("validate plugin skill %q: %w", skillPath, err)
		}
	}
	return Validation{
		Type:          domain.PowerTypePlugin,
		Name:          identity.Name,
		Version:       identity.Version,
		Description:   identity.Description,
		ChangeSummary: request.ChangeSummary,
		FileCount:     stats.fileCount,
		ContentSize:   stats.totalSize,
	}, nil
}

// pluginIdentity 的 JSON 字段与 Go 字段大小写无关匹配，无需标签。
type pluginIdentity struct {
	Name        string
	Version     string
	Description string
}

// readPluginIdentity 读取 apc-plugin.json 的身份字段以构造 PowerRef；
// 完整严格复验交给 pluginartifact.LoadAPCPluginManifest。
func readPluginIdentity(root string) (pluginIdentity, error) {
	contents, err := readRootRegularFile(root, "apc-plugin.json")
	if err != nil {
		return pluginIdentity{}, err
	}
	var identity pluginIdentity
	if err := jsonstrict.Decode(contents, &identity, false); err != nil {
		return pluginIdentity{}, fmt.Errorf("decode apc-plugin.json: %w", errors.Join(ErrInvalid, err))
	}
	return identity, nil
}

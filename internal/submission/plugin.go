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

type pluginSourceLoader struct{}

func (pluginSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	stats, err := validateSourceTree(path)
	if err != nil {
		return source{}, err
	}
	identity, err := readPluginIdentity(path)
	if err != nil {
		return source{}, err
	}
	ref := domain.PowerRef{Type: domain.PowerTypePlugin, Name: identity.Name, Version: identity.Version}
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, path)
	if err != nil {
		return source{}, fmt.Errorf("validate plugin artifact: %w", errors.Join(ErrInvalid, err))
	}
	for _, skillPath := range manifest.Skills {
		skillRoot := filepath.Join(path, filepath.FromSlash(skillPath))
		if _, _, _, err := validateSkillContent(skillRoot); err != nil {
			return source{}, fmt.Errorf("validate plugin skill %q: %w", skillPath, err)
		}
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypePlugin, Name: identity.Name, Version: identity.Version,
			Description: identity.Description, FileCount: stats.fileCount, ContentSize: stats.totalSize,
		},
		contentType: "application/gzip",
		build:       func() ([]byte, error) { return BuildTarGz(path) },
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

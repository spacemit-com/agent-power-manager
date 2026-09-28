package submission

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/jsonstrict"
)

type pluginSourceLoader struct{}

func (pluginSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	snapshot, err := captureSourceTree(ctx, path)
	if err != nil {
		return source{}, err
	}
	snapshotManifest, err := readPluginManifest(snapshot)
	if err != nil {
		return source{}, err
	}
	if err := validateCommonMetadata(snapshotManifest.Name, snapshotManifest.Version, snapshotManifest.Description); err != nil {
		return source{}, err
	}
	ref := domain.PowerRef{Type: domain.PowerTypePlugin, Name: snapshotManifest.Name, Version: snapshotManifest.Version}
	// 提交侧只接受 schema_version 2 且本地化展示字段必填；安装与恢复路径继续使用
	// ValidateAPCPluginManifest 的 v1+v2 宽松语义（ADR-038）。
	if err := pluginartifact.ValidateSubmitAPCPluginManifest(ref, snapshotManifest); err != nil {
		return source{}, fmt.Errorf("apc-plugin.json %w", errors.Join(ErrInvalid, err))
	}
	if err := pluginartifact.ValidateRawPackage(ref, path); err != nil {
		return source{}, fmt.Errorf("validate raw plugin package: %w", errors.Join(ErrInvalid, err))
	}
	manifest, err := pluginartifact.LoadAPCPluginManifest(ref, path)
	if err != nil {
		return source{}, fmt.Errorf("validate plugin artifact: %w", errors.Join(ErrInvalid, err))
	}
	if !reflect.DeepEqual(snapshotManifest, manifest) {
		return source{}, fmt.Errorf("plugin manifest changed while loading: %w", ErrInvalid)
	}
	entries := make([]pluginartifact.RawPackageEntry, 0, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		entries = append(entries, pluginartifact.RawPackageEntry{
			Path: strings.TrimSuffix(entry.name, "/"), Directory: entry.typeflag == tar.TypeDir,
		})
	}
	if err := pluginartifact.ValidateRawPackageEntries(snapshotManifest, entries); err != nil {
		return source{}, fmt.Errorf("validate raw plugin snapshot: %w", errors.Join(ErrInvalid, err))
	}
	embeddedNames := make(map[string]string, len(manifest.Skills))
	for _, skillPath := range manifest.Skills {
		embedded, err := validateSkillContent(snapshot, skillPath)
		if err != nil {
			return source{}, fmt.Errorf("validate plugin skill %q: %w", skillPath, err)
		}
		if previousPath, duplicate := embeddedNames[embedded.Name]; duplicate {
			return source{}, fmt.Errorf("plugin skills %q and %q share frontmatter name %q: %w", previousPath, skillPath, embedded.Name, ErrInvalid)
		}
		embeddedNames[embedded.Name] = skillPath
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypePlugin, Name: snapshotManifest.Name, Version: snapshotManifest.Version,
			Description: snapshotManifest.Description, FileCount: snapshot.stats.fileCount, ContentSize: snapshot.stats.totalSize,
		},
		contentType: "application/gzip",
		build:       snapshot.build,
	}, nil
}

// readPluginManifest 从已固定快照严格读取权威索引；路径版 ValidateRawPackage 负责复用完整闭集规则。
func readPluginManifest(snapshot sourceTreeSnapshot) (pluginartifact.APCPluginManifest, error) {
	contents, err := snapshot.regularFile("apc-plugin.json")
	if err != nil {
		return pluginartifact.APCPluginManifest{}, err
	}
	var manifest pluginartifact.APCPluginManifest
	if err := jsonstrict.Decode(contents, &manifest, true); err != nil {
		return pluginartifact.APCPluginManifest{}, fmt.Errorf("decode apc-plugin.json: %w", errors.Join(ErrInvalid, err))
	}
	return manifest, nil
}

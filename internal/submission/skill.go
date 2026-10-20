package submission

import (
	"context"
	"errors"

	"apm/internal/domain"
	"apm/internal/skillartifact"
)

// skillMetadata 是 skillartifact.Metadata 的别名：Skill 格式的权威校验已下沉到
// internal/skillartifact，使 Plugin 内嵌 Skill 与 Agent 子资源共用同一套规则；
// 本包只做错误翻译（ErrInvalid）与快照读取。
type skillMetadata = skillartifact.Metadata

type skillSourceLoader struct{}

func (skillSourceLoader) Load(ctx context.Context, path string) (source, error) {
	if err := ctx.Err(); err != nil {
		return source{}, err
	}
	snapshot, err := captureSourceTree(ctx, path)
	if err != nil {
		return source{}, err
	}
	metadata, err := validateSkillContent(snapshot, "")
	if err != nil {
		return source{}, err
	}
	if err := validateCommonMetadata(metadata.Name, metadata.Version, metadata.Description); err != nil {
		return source{}, err
	}
	return source{
		validation: Validation{
			Type: domain.PowerTypeSkill, SchemaVersion: metadata.SchemaVersion,
			Name: metadata.Name, Version: metadata.Version, Description: metadata.Description,
			NameZH: metadata.NameZH, NameEN: metadata.NameEN, DescriptionZH: metadata.DescriptionZH,
			FileCount: snapshot.stats.fileCount, ContentSize: snapshot.stats.totalSize,
		},
		contentType: "application/gzip",
		build:       snapshot.build,
	}, nil
}

// validateSkillContent 校验资源根下 SKILL.md 与 skill-card.md 的完整 v2 格式，
// 返回 SKILL.md frontmatter 的稳定字段。独立 Skill 与内嵌 Plugin Skill 共用，
// 规则由 skillartifact 统一实现。
func validateSkillContent(snapshot sourceTreeSnapshot, root string) (skillMetadata, error) {
	metadata, err := skillartifact.ValidateContent(root, func(relative string) ([]byte, error) {
		return snapshot.regularFile(relative)
	})
	return metadata, wrapSkillError(err)
}

// wrapSkillError 把 skillartifact 的哨兵错误桥接到本包的 ErrInvalid。
// 两个包各自持有 ErrInvalid 以便独立演化，但调用方（与既有测试）依赖
// errors.Is(err, submission.ErrInvalid)，因此边界处必须显式并接。
func wrapSkillError(err error) error {
	if err == nil || !errors.Is(err, skillartifact.ErrInvalid) {
		return err
	}
	return errors.Join(ErrInvalid, err)
}

// validateSkillCard 校验 skill-card.md 的 schema_version frontmatter、标题、六个已填章节与 Maintainer 字段。
func validateSkillCard(contents []byte) error {
	return wrapSkillError(skillartifact.ValidateCard(contents))
}

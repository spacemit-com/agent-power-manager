package adapter

import "apm/internal/domain"

// SkillInstallResult 报告 Skill Adapter 安装是否已经越过原子发布提交点。
type SkillInstallResult struct {
	Published bool
}

// SkillAdapter 定义智能体的文件型 Skill 安装操作。
type SkillAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (SkillInstallResult, error)
}

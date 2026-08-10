// Package adapter 提供智能体适配器接口。
package adapter

import "apm/internal/domain"

// InstallResult 报告 Adapter 安装是否已经越过原子发布提交点。
type InstallResult struct {
	Published bool
}

// Adapter 定义智能体 Skill 安装操作。
type Adapter interface {
	DetectAgent() (bool, error)
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (InstallResult, error)
}

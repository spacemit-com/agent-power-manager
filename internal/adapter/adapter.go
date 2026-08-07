// Package adapter 提供智能体适配器接口。
package adapter

import "apm/internal/domain"

// Adapter 定义智能体 Skill 安装操作。
type Adapter interface {
	DetectAgent() (bool, error)
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) error
}

// Package adapter 提供智能体适配器的占位接口。
package adapter

import "apm/internal/domain"

// Adapter 定义智能体能力安装和卸载操作。
type Adapter interface {
	DetectAgent() (bool, error)
	InstallPath(domain.AgentID) string
	Install(domain.Power, string) error
	Uninstall(domain.PowerRef) error
}

// Package config 提供 APM 配置清单的占位类型。
package config

import "apm/internal/domain"

// Manifest 表示一个智能体的已安装能力清单。
type Manifest struct {
	Agent  domain.AgentID
	Powers []domain.InstalledPower
}

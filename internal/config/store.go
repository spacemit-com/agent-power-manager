package config

import "apm/internal/domain"

// Store 定义配置清单的读写操作。
type Store interface {
	Read(domain.AgentID) (Manifest, error)
	Write(domain.AgentID, Manifest) error
}

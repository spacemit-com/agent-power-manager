package config

import "apm/internal/domain"

// JSONStore 是 JSON 配置清单存储的零值占位实现。
type JSONStore struct{}

// NewJSONStore 创建零值 JSON 配置清单存储。
func NewJSONStore() *JSONStore {
	return &JSONStore{}
}

// Read 读取指定智能体的配置清单。
func (s *JSONStore) Read(domain.AgentID) (Manifest, error) {
	return Manifest{}, nil
}

// Write 写入指定智能体的配置清单。
func (s *JSONStore) Write(domain.AgentID, Manifest) error {
	return nil
}

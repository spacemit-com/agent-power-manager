package adapter

import "apm/internal/domain"

// Registry 是适配器注册表的零值占位实现。
type Registry struct{}

// NewRegistry 创建零值适配器注册表。
func NewRegistry() *Registry {
	return &Registry{}
}

// Resolve 解析指定智能体和能力类别的适配器。
func (r *Registry) Resolve(domain.AgentID, domain.PowerType) (Adapter, error) {
	return nil, nil
}

// RefreshInstalled 刷新已安装智能体的快照。
func (r *Registry) RefreshInstalled() (domain.AgentSnapshot, error) {
	return domain.AgentSnapshot{}, nil
}

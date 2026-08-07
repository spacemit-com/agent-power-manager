package config

import "apm/internal/domain"

// Repository 定义能力清单的查询和修改操作。
type Repository interface {
	List(domain.AgentID) (domain.PowerGroups, error)
	Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error)
	Set(domain.AgentID, domain.PowerRef) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

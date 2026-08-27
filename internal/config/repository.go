package config

import "apm/internal/domain"

// Repository 定义能力清单的查询和修改操作。
type Repository interface {
	List(domain.AgentID) (domain.InstalledPowerGroups, error)
	Find(domain.AgentID, domain.PowerType, string) (domain.InstalledPower, bool, error)
	Set(domain.AgentID, domain.InstalledPower) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

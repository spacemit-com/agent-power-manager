package lifecycle

import (
	"apm/internal/adapter"
	"apm/internal/cache"
	"apm/internal/cloud"
	"apm/internal/config"
	"apm/internal/domain"
	"apm/internal/lock"
	"apm/internal/transaction"
)

// AdapterRegistry 定义智能体适配器注册表所需的操作。
type AdapterRegistry interface {
	Resolve(domain.AgentID, domain.PowerType) (adapter.Adapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}

// ConfigRepository 是配置清单 Repository 的别名。
type ConfigRepository = config.Repository

// JournalStore 定义事务日志存储所需的操作。
type JournalStore interface {
	Inspect() (transaction.Journal, error)
	Write(transaction.Journal) error
	Delete() error
}

// Locker 是进程间锁的别名。
type Locker = lock.Locker

// PowerCache 是能力缓存的别名。
type PowerCache = cache.PowerCache

// CloudClient 是云端能力客户端的别名。
type CloudClient = cloud.Client

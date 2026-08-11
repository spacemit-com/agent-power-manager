// Package cache 提供能力缓存接口。
package cache

import "apm/internal/domain"

// PowerCache 定义将版本化能力物化到公共 staging 根的操作。
type PowerCache interface {
	Fetch(domain.PowerRef) (stagingRoot string, err error)
}

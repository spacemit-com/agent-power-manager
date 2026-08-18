// Package cache 提供能力缓存接口。
package cache

import "apm/internal/domain"

// PowerCache 只负责 Skill 从版本化缓存物化到公共 staging；Plugin 缓存归 pluginresource 管理。
type PowerCache interface {
	StageSkill(domain.PowerRef) (stagingRoot string, err error)
}

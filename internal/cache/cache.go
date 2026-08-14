// Package cache 提供能力缓存接口。
package cache

import "apm/internal/domain"

// PowerCache 定义文件型 Power 从版本化缓存物化到公共 staging 根的操作。
type PowerCache interface {
	StageSkill(domain.PowerRef) (stagingRoot string, err error)
	StagePlugin(domain.PowerRef) (stagingRoot string, err error)
}

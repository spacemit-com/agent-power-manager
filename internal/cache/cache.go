// Package cache 提供能力缓存接口。
package cache

import (
	"context"

	"apm/internal/domain"
)

// Source 定义 Cache 获取精确版本 Skill 和 Agent 到私有目标的最小边界。
type Source interface {
	DownloadSkill(context.Context, domain.PowerRef, string) error
	DownloadAgent(context.Context, domain.PowerRef, string) error
}

// PowerCache 定义准备可安装 Skill 与 Agent staging 的边界；缓存命中及下载发布均由实现管理。
type PowerCache interface {
	PrepareSkill(context.Context, domain.PowerRef) (stagingRoot string, err error)
	PrepareAgent(context.Context, domain.PowerRef) (stagingRoot string, err error)
}

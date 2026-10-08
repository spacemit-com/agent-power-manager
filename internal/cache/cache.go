// Package cache 提供能力缓存接口。
package cache

import (
	"context"

	"apm/internal/domain"
)

// Source 定义 Cache 获取精确版本 Skill 和 Agent 到私有目标的最小边界。
// CatalogEntry 已由目录快照验证且是内容元数据的唯一权威，调用链不得按 Ref 重建或回源查询 metadata。
// DownloadSkill 的 destination 是单个 SKILL.md 文件路径；DownloadAgent 的 destination
// 是物理空目录，实现必须在其下写入完整的 agent.yaml + assets/ 目录树。
type Source interface {
	DownloadSkill(context.Context, domain.CatalogEntry, string) error
	DownloadAgent(context.Context, domain.CatalogEntry, string) error
}

// PowerCache 定义准备可安装 Skill 与 Agent staging 的边界；缓存键只使用 entry.Ref，
// miss 必须把同一权威 CatalogEntry 完整透传给 Source，禁止重建 metadata。
type PowerCache interface {
	PrepareSkill(context.Context, domain.CatalogEntry) (stagingRoot string, err error)
	PrepareExpert(context.Context, domain.CatalogEntry) (stagingRoot string, err error)
}

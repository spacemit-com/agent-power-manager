package lifecycle

import (
	"apm/internal/cli"
	"apm/internal/domain"
)

// makeShowResult 将目录条目复制为不与快照共享 Tags 的 CLI 结果；复制方式与 list 共用，
// 因此 show 与 list 的发布元数据字段集（含 v2 展示字段）始终一致。
func makeShowResult(entry domain.CatalogEntry) cli.ShowResult {
	return cli.ShowResult{Power: listedPowerFromCatalogEntry(entry)}
}

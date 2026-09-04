package lifecycle

import (
	"apm/internal/cli"
	"apm/internal/domain"
)

// makeShowResult 将目录条目复制为不与快照共享 Tags 的 CLI 结果。
func makeShowResult(entry domain.CatalogEntry) cli.ShowResult {
	return cli.ShowResult{Power: cli.ListedPower{Power: entry.Ref, Description: entry.Description, Tags: entry.Tags}}
}

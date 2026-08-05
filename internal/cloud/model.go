package cloud

import "apm/internal/domain"

// Query 表示云端能力查询条件。
type Query struct {
	Keyword string
	Type    domain.PowerType
}

// PowerMeta 表示云端能力的元数据。
type PowerMeta struct {
	Ref         domain.PowerRef
	Version     string
	DownloadURL string
}

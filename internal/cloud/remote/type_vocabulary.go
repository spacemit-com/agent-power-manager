package remote

import "apm/internal/domain"

// APC 至今把 Expert 类别叫 agent：它的 metadata.type 是 agent，目录分组叫 agents，
// 内容与提交路径里也写 agent（见 APC-API.md 与 ADR-044）。apm 本地从这次改名起统一
// 叫 expert，两者只能在这条 HTTP 边界上对齐。
//
// 这三个函数就是唯一的翻译层，任何跨边界的类型取值都必须经过它们，否则会以不同的
// 方式坏掉：提交被判成未知类型、内容下载 404、响应里的 agent 被判成非法资源标识。
// 等 APC 也改名后，连同 ADR-044 一起删除这一层。

// apcWireType 把本地类型翻成 APC 的取值。只有 Expert 有差异，其余原样返回。
func apcWireType(powerType domain.PowerType) domain.PowerType {
	if powerType == domain.PowerTypeExpert {
		return domain.PowerType("agent")
	}
	return powerType
}

// powerTypeFromAPC 把 APC 的取值翻回本地类型，同样是 Expert 一处差异。
func powerTypeFromAPC(raw domain.PowerType) domain.PowerType {
	if raw == domain.PowerType("agent") {
		return domain.PowerTypeExpert
	}
	return raw
}

// apcPowerType 是 powerTypeFromAPC 的指针版本，供 presence-sensitive DTO 使用：
// nil 表示响应里缺字段，必须原样透传，不能变成一个「有值」的零值。
func apcPowerType(raw *domain.PowerType) *domain.PowerType {
	if raw == nil {
		return nil
	}
	translated := powerTypeFromAPC(*raw)
	return &translated
}

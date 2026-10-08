package remote

import (
	"testing"

	"apm/internal/domain"
)

// APC 仍把 Expert 类别叫 agent，本地叫 expert，两者只在 HTTP 边界上对齐。这层翻译是
// 过渡期唯一的脏点，所以单独钉住它的两个方向与 nil 透传：漏掉任一方向都会以不同的
// 方式坏掉（提交被判未知类型、内容 404、响应标识非法），而单元测试之外很难一眼看出。
func TestAPCWireTypeVocabulary(t *testing.T) {
	if got := apcWireType(domain.PowerTypeExpert); got != domain.PowerType("agent") {
		t.Fatalf("apcWireType(expert) = %q, want agent", got)
	}
	for _, unchanged := range []domain.PowerType{domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP} {
		if got := apcWireType(unchanged); got != unchanged {
			t.Fatalf("apcWireType(%q) = %q, want it unchanged", unchanged, got)
		}
	}

	if got := powerTypeFromAPC(domain.PowerType("agent")); got != domain.PowerTypeExpert {
		t.Fatalf("powerTypeFromAPC(agent) = %q, want expert", got)
	}
	for _, unchanged := range []domain.PowerType{domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP} {
		if got := powerTypeFromAPC(unchanged); got != unchanged {
			t.Fatalf("powerTypeFromAPC(%q) = %q, want it unchanged", unchanged, got)
		}
	}

	// The DTOs are presence-sensitive: nil means "the response omitted the field", and
	// turning that into a value would silently satisfy a validator that must reject it.
	if got := apcPowerType(nil); got != nil {
		t.Fatalf("apcPowerType(nil) = %v, want nil", *got)
	}
	raw := domain.PowerType("agent")
	if got := apcPowerType(&raw); got == nil || *got != domain.PowerTypeExpert {
		t.Fatalf("apcPowerType(agent) = %v, want expert", got)
	}
}

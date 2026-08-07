package domain_test

import (
	"testing"

	"apm/internal/domain"
)

func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{domain.PowerTypeSkill: {ref}}
	_ = domain.Power{}
	result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
	if groups[domain.PowerTypeSkill][0] != ref || len(result.Items) != 1 {
		t.Fatal("共享类型未保存输入")
	}
}

func TestPowerGroupsKeepTypedVersionedReferences(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{
		domain.PowerTypeSkill:  {ref},
		domain.PowerTypePlugin: {},
		domain.PowerTypeMCP:    {},
	}
	if got := groups[domain.PowerTypeSkill][0]; got != ref {
		t.Fatalf("skill group = %#v, want %#v", got, ref)
	}
}

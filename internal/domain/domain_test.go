package domain_test

import (
	"testing"
	"time"

	"apm/internal/domain"
)

func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	installed := domain.InstalledPower{Ref: ref, InstalledAt: time.Unix(1, 0)}
	_ = domain.Power{}
	result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
	if installed.Ref != ref || installed.Ref.Version != "1.0.0" || len(result.Items) != 1 {
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

package domain_test

import (
	"testing"

	"apm/internal/domain"
)

func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}
	installed := domain.InstalledPower{Ref: ref, Version: "1.0.0"}
	result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
	if installed.Ref != ref || len(result.Items) != 1 {
		t.Fatal("共享类型未保存输入")
	}
}

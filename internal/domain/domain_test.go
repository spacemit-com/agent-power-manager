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

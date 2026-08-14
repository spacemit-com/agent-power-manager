package adapter_test

import (
	"testing"

	"apm/internal/adapter"
	"apm/internal/domain"
)

type contractAdapter struct{}

func (contractAdapter) DetectAgent() (bool, error) { return true, nil }

func (contractAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }

func (contractAdapter) Install(domain.PowerRef, string) (adapter.SkillInstallResult, error) {
	return adapter.SkillInstallResult{Published: true}, nil
}

func TestAdapterContractAndErrors(t *testing.T) {
	var _ adapter.SkillAdapter = contractAdapter{}
	for _, err := range []error{
		adapter.ErrAgentNotInstalled,
		adapter.ErrUnsupportedAdapter,
		adapter.ErrTargetConflict,
		adapter.ErrInvalidSkill,
	} {
		if err == nil {
			t.Fatal("adapter sentinel is nil")
		}
	}
}

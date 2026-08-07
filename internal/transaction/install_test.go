package transaction

import (
	"context"
	"testing"

	"apm/internal/domain"
)

func TestInstallerAcceptsPowerReference(t *testing.T) {
	err := (&Installer{}).Install(context.Background(), domain.PowerRef{
		Name:    "demo",
		Type:    domain.PowerTypeSkill,
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
}

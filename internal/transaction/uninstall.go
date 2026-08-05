package transaction

import (
	"context"

	"apm/internal/domain"
)

// Uninstaller 表示卸载事务执行器。
type Uninstaller struct{}

// Uninstall 卸载能力。
func (u *Uninstaller) Uninstall(context.Context, domain.PowerRef) error {
	return nil
}

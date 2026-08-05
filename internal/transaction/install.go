package transaction

import (
	"context"

	"apm/internal/domain"
)

// Installer 表示安装事务执行器。
type Installer struct{}

// Install 安装能力。
func (i *Installer) Install(context.Context, domain.Power) error {
	return nil
}

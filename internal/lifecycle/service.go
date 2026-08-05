// Package lifecycle 提供能力生命周期操作的占位接口和服务。
package lifecycle

import (
	"apm/internal/cli"
	"apm/internal/domain"
)

// ListResult 表示能力列表查询的聚合结果。
type ListResult struct {
	CloudPowers      []domain.PowerRef
	InstalledByAgent map[domain.AgentID][]domain.InstalledPower
	Failures         map[domain.AgentID]error
}

// Lifecycle 定义能力生命周期操作。
type Lifecycle interface {
	Search(cli.Flags) ([]domain.PowerRef, error)
	List(cli.Flags) (ListResult, error)
	Install(cli.Flags) (domain.Result, error)
	Uninstall(cli.Flags) (domain.Result, error)
}

// Service 是 Lifecycle 的零值占位实现。
type Service struct{}

// New 创建零值生命周期服务。
func New() *Service {
	return &Service{}
}

// Search 搜索能力。
func (s *Service) Search(cli.Flags) ([]domain.PowerRef, error) {
	return nil, nil
}

// List 列出能力。
func (s *Service) List(cli.Flags) (ListResult, error) {
	return ListResult{}, nil
}

// Install 安装能力。
func (s *Service) Install(cli.Flags) (domain.Result, error) {
	return domain.Result{}, nil
}

// Uninstall 卸载能力。
func (s *Service) Uninstall(cli.Flags) (domain.Result, error) {
	return domain.Result{}, nil
}

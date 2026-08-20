package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"

	"apm/internal/domain"
)

// bibitPluginAdapter 通过 Bibit 的受控 Plugin 目录读取安装事实。
type bibitPluginAdapter struct {
	profile agentProfile
}

func newBibitPluginAdapter(profile agentProfile) *bibitPluginAdapter {
	return &bibitPluginAdapter{profile: profile}
}

func (adapter *bibitPluginAdapter) DeploymentMode() domain.PluginDeploymentMode {
	return domain.PluginDeploymentFilesystem
}

// Inspect 仅将物理目录视为已安装，避免把符号链接或普通文件误认为安全 Plugin。
func (adapter *bibitPluginAdapter) Inspect(_ context.Context, name string) (PluginFact, error) {
	if !isSafePluginActualName(name) {
		return PluginFact{}, fmt.Errorf("inspect plugin name %q: %w", name, ErrInvalidPlugin)
	}
	target, err := adapter.TargetPath(name)
	if err != nil {
		return PluginFact{}, err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		fact := PluginFact{Presence: PluginAbsent, ActualName: name}
		return fact, validatePluginFact(fact)
	}
	if err != nil {
		return PluginFact{}, fmt.Errorf("inspect Bibit plugin target %q: %w", target, errors.Join(ErrPluginStateUnknown, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return PluginFact{}, fmt.Errorf("Bibit plugin target %q is not a physical directory: %w", target, ErrPluginStateUnknown)
	}
	fact := PluginFact{Presence: PluginPresent, ActualName: name, Selector: name}
	return fact, validatePluginFact(fact)
}

// Add 是事务完成目录发布后的 Bibit 激活钩子；Bibit 自动发现目录，因此不执行 CLI 或写文件。
func (adapter *bibitPluginAdapter) Add(_ context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validateLogicalFilesystemPluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	return PluginMutationResult{State: PluginMutationApplied}, nil
}

// Remove 是事务完成目录移除前的 Bibit 停用钩子；物理删除始终由事务负责。
func (adapter *bibitPluginAdapter) Remove(_ context.Context, target PluginTarget) (PluginMutationResult, error) {
	if err := validateLogicalFilesystemPluginTarget(target); err != nil {
		return PluginMutationResult{State: PluginMutationNotApplied}, err
	}
	return PluginMutationResult{State: PluginMutationApplied}, nil
}

func (adapter *bibitPluginAdapter) TargetPath(name string) (string, error) {
	return adapter.profile.pluginTargetPath(name)
}

// validateLogicalFilesystemPluginTarget 约束目录名与 Agent 激活 selector 都是同一个逻辑 Plugin 名称。
func validateLogicalFilesystemPluginTarget(target PluginTarget) error {
	if err := validatePluginTarget(target); err != nil {
		return err
	}
	if target.Selector != target.ActualName {
		return fmt.Errorf("filesystem plugin selector %q differs from logical name %q: %w", target.Selector, target.ActualName, ErrInvalidPlugin)
	}
	return nil
}

var _ PluginAdapter = (*bibitPluginAdapter)(nil)

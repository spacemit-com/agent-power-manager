package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
	"apm/internal/domain"
)

// installLocalMarketplacePlugin 编排本地 marketplace 与 Plugin 的联合首装；每个 Agent 写操作前必须先持久化对应意图阶段。
func (s *Service) installLocalMarketplacePlugin(ctx context.Context, request PluginInstallRequest) error {
	if request.Previous != nil {
		return s.switchLocalMarketplacePlugin(ctx, request)
	}
	marketplaceAdapter := request.Marketplace.Adapter
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, request.Marketplace.Target.Name)
	if err != nil {
		return fmt.Errorf("inspect target marketplace before Plugin install: %w", err)
	}
	pluginFact, err := request.Adapter.Inspect(ctx, request.Target.ActualName)
	if err != nil {
		return fmt.Errorf("inspect target Plugin before install: %w", err)
	}
	// 重装可复用卸载后保留的精确 marketplace，但 journal 必须记录其回滚后仍需存在。
	retainedMarketplace := marketplaceFact.Presence == adapter.MarketplacePresent &&
		marketplaceFact.Name == request.Marketplace.Target.Name && marketplaceFact.Source == request.Marketplace.Target.Source
	if (marketplaceFact.Presence != adapter.MarketplaceAbsent && !retainedMarketplace) || pluginFact.Presence != adapter.PluginAbsent {
		return fmt.Errorf("marketplace Plugin first-install pre-state is not absent: %w", ErrIndeterminateState)
	}

	phase := PhaseRegisteringMarketplace
	if retainedMarketplace {
		phase = PhaseAddingNewPlugin
	}
	journal, err := s.newLocalMarketplacePluginInstallJournal(request, phase)
	if err != nil {
		return err
	}
	if retainedMarketplace {
		journal.Plugin.Marketplace.PreviousSource = request.Marketplace.Target.Source
		journal.Plugin.Marketplace.PreviousPresent = true
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create marketplace Plugin install journal: %w", err)
	}
	progress := marketplacePluginProgress{}
	if !retainedMarketplace {
		mutation, mutationErr := marketplaceAdapter.RegisterMarketplace(ctx, request.Marketplace.Target)
		switch mutation.State {
		case adapter.PluginMutationApplied:
			progress.targetMarketplaceRegistered = true
		case adapter.PluginMutationNotApplied:
			return s.rollbackLocalMarketplacePlugin(ctx, journal, marketplaceAdapter, progress, pluginMutationError("register target marketplace", mutationErr))
		case adapter.PluginMutationUnknown:
			return indeterminatePluginMutation("register target marketplace", mutationErr)
		default:
			return indeterminatePluginMutation(fmt.Sprintf("register target marketplace returned state %q", mutation.State), mutationErr)
		}
		journal.Phase = PhaseAddingNewPlugin
		if err := s.journals.Update(journal); err != nil {
			return errors.Join(mutationErr, fmt.Errorf("persist Plugin add intent: %w", err))
		}
		if mutationErr != nil {
			return mutationErr
		}
	}

	mutation, mutationErr := request.Adapter.Add(ctx, request.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		progress.targetPluginAdded = true
	case adapter.PluginMutationNotApplied:
		return s.rollbackLocalMarketplacePlugin(ctx, journal, marketplaceAdapter, progress, pluginMutationError("add target Plugin", mutationErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("add target Plugin", mutationErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("add target Plugin returned state %q", mutation.State), mutationErr)
	}
	return s.commitLocalMarketplacePluginInstall(ctx, journal, mutationErr)
}

// commitLocalMarketplacePluginInstall 先固定 committing 方向再写 manifest，确保中断恢复不会反向回滚已安装的目标 Plugin。
func (s *Service) commitLocalMarketplacePluginInstall(ctx context.Context, journal Journal, mutationErr error) error {
	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(mutationErr, fmt.Errorf("persist marketplace Plugin committing direction: %w", err))
	}
	if mutationErr != nil {
		return mutationErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(journal.Agent, journal.Power); err != nil {
		return fmt.Errorf("set marketplace Plugin manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return fmt.Errorf("delete completed marketplace Plugin journal: %w", err)
	}
	return nil
}

// switchLocalMarketplacePlugin 以固定写入顺序切换同名 marketplace；旧 Plugin 未移除前不得改写 marketplace。
func (s *Service) switchLocalMarketplacePlugin(ctx context.Context, request PluginInstallRequest) error {
	marketplaceAdapter := request.Marketplace.Adapter
	previousMarketplace := *request.Marketplace.Previous
	previousTarget := *request.PreviousTarget
	marketplaceFact, err := marketplaceAdapter.InspectMarketplace(ctx, previousMarketplace.Name)
	if err != nil {
		return fmt.Errorf("inspect previous marketplace before Plugin switch: %w", err)
	}
	previousFact, err := request.Adapter.Inspect(ctx, previousTarget.ActualName)
	if err != nil {
		return fmt.Errorf("inspect previous Plugin before switch: %w", err)
	}
	// 版本切换前只有一个同名 Plugin 事实；版本差异由 marketplace source 与 manifest 表达。
	if marketplaceFact.Presence != adapter.MarketplacePresent || marketplaceFact.Name != previousMarketplace.Name || marketplaceFact.Source != previousMarketplace.Source ||
		previousFact.Presence != adapter.PluginPresent || previousFact.ActualName != previousTarget.ActualName || previousFact.Selector != previousTarget.Selector {
		return fmt.Errorf("marketplace Plugin version-switch pre-state is not exact: %w", ErrIndeterminateState)
	}

	journal, err := s.newLocalMarketplacePluginInstallJournal(request, PhaseRemovingOldPlugin)
	if err != nil {
		return err
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create marketplace Plugin switch journal: %w", err)
	}
	progress := marketplacePluginProgress{}
	mutation, mutationErr := request.Adapter.Remove(ctx, previousTarget)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		progress.previousPluginRemoved = true
	case adapter.PluginMutationNotApplied:
		return s.rollbackLocalMarketplacePlugin(ctx, journal, request.Marketplace.Adapter, progress, pluginMutationError("remove previous Plugin", mutationErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("remove previous Plugin", mutationErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("remove previous Plugin returned state %q", mutation.State), mutationErr)
	}
	journal.Phase = PhaseUnregisteringOldMarketplace
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(mutationErr, fmt.Errorf("persist old marketplace unregister intent: %w", err))
	}
	if mutationErr != nil {
		return mutationErr
	}

	mutation, mutationErr = marketplaceAdapter.UnregisterMarketplace(ctx, previousMarketplace)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		progress.previousMarketplaceRemoved = true
	case adapter.PluginMutationNotApplied:
		return s.rollbackLocalMarketplacePlugin(ctx, journal, marketplaceAdapter, progress, pluginMutationError("unregister previous marketplace", mutationErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("unregister previous marketplace", mutationErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("unregister previous marketplace returned state %q", mutation.State), mutationErr)
	}
	journal.Phase = PhaseRegisteringNewMarketplace
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(mutationErr, fmt.Errorf("persist new marketplace register intent: %w", err))
	}
	if mutationErr != nil {
		return mutationErr
	}

	mutation, mutationErr = marketplaceAdapter.RegisterMarketplace(ctx, request.Marketplace.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		progress.targetMarketplaceRegistered = true
	case adapter.PluginMutationNotApplied:
		return s.rollbackLocalMarketplacePlugin(ctx, journal, marketplaceAdapter, progress, pluginMutationError("register target marketplace", mutationErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("register target marketplace", mutationErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("register target marketplace returned state %q", mutation.State), mutationErr)
	}
	journal.Phase = PhaseAddingNewPlugin
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(mutationErr, fmt.Errorf("persist target Plugin add intent: %w", err))
	}
	if mutationErr != nil {
		return mutationErr
	}

	mutation, mutationErr = request.Adapter.Add(ctx, request.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		progress.targetPluginAdded = true
	case adapter.PluginMutationNotApplied:
		return s.rollbackLocalMarketplacePlugin(ctx, journal, marketplaceAdapter, progress, pluginMutationError("add target Plugin", mutationErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("add target Plugin", mutationErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("add target Plugin returned state %q", mutation.State), mutationErr)
	}
	return s.commitLocalMarketplacePluginInstall(ctx, journal, mutationErr)
}

type marketplacePluginProgress struct {
	previousPluginRemoved       bool
	previousMarketplaceRemoved  bool
	targetMarketplaceRegistered bool
	targetPluginAdded           bool
}

// rollbackLocalMarketplacePlugin 固定按新资源逆序、旧资源正序恢复；任一回滚结果不明确时保留 rolling-back journal。
func (s *Service) rollbackLocalMarketplacePlugin(ctx context.Context, journal Journal, marketplaceAdapter adapter.MarketplacePluginAdapter, progress marketplacePluginProgress, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist marketplace Plugin rollback direction: %w", err))
	}
	if progress.targetPluginAdded {
		mutation, err := marketplaceAdapter.Remove(ctx, journal.Plugin.Target)
		if err := requireAppliedPluginMutation("remove target Plugin during rollback", mutation, err); err != nil {
			return errors.Join(originalErr, err)
		}
	}
	marketplace := journal.Plugin.Marketplace
	if progress.targetMarketplaceRegistered {
		mutation, err := marketplaceAdapter.UnregisterMarketplace(ctx, adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.TargetSource})
		if err := requireAppliedPluginMutation("unregister target marketplace during rollback", mutation, err); err != nil {
			return errors.Join(originalErr, err)
		}
	}
	if progress.previousMarketplaceRemoved {
		mutation, err := marketplaceAdapter.RegisterMarketplace(ctx, adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.PreviousSource})
		if err := requireAppliedPluginMutation("register previous marketplace during rollback", mutation, err); err != nil {
			return errors.Join(originalErr, err)
		}
	}
	if progress.previousPluginRemoved {
		mutation, err := marketplaceAdapter.Add(ctx, *journal.Plugin.Previous)
		if err := requireAppliedPluginMutation("restore previous Plugin", mutation, err); err != nil {
			return errors.Join(originalErr, err)
		}
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, fmt.Errorf("delete rolled-back marketplace Plugin journal: %w", err))
	}
	return originalErr
}

func (s *Service) newLocalMarketplacePluginInstallJournal(request PluginInstallRequest, phase Phase) (Journal, error) {
	id, err := s.newID()
	if err != nil {
		return Journal{}, err
	}
	payload := &PluginJournalPayload{
		Deployment: domain.PluginDeploymentMarketplace,
		Target:     request.Target,
		Marketplace: &PluginMarketplaceJournalPayload{
			Name:         request.Marketplace.Target.Name,
			TargetSource: request.Marketplace.Target.Source,
		},
	}
	if request.Previous != nil {
		payload.Previous = clonePluginTarget(request.PreviousTarget)
		payload.PreviousPresent = true
		payload.Marketplace.PreviousSource = request.Marketplace.Previous.Source
		payload.Marketplace.PreviousPresent = true
	}
	return Journal{
		SchemaVersion: 1,
		TransactionID: id,
		Driver:        DriverPlugin,
		Operation:     OperationInstall,
		Phase:         phase,
		Agent:         request.Agent,
		Power:         request.Power,
		Previous:      clonePowerRef(request.Previous),
		Plugin:        payload,
	}, nil
}

// uninstallLocalMarketplacePlugin 以提交为导向移除 Plugin 与专用 marketplace：先复核两个事实，
// 双缺省直接清理 manifest；否则按观察到的 pre-state 记录 presence bit 后幂等移除残留目标，绝不回滚或重新注册。
func (s *Service) uninstallLocalMarketplacePlugin(ctx context.Context, request PluginUninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	marketplaceFact, err := request.MarketplaceAdapter.InspectMarketplace(ctx, request.Marketplace.Name)
	if err != nil {
		return result, fmt.Errorf("inspect marketplace before Plugin uninstall: %w", err)
	}
	pluginFact, err := request.Adapter.Inspect(ctx, request.Target.ActualName)
	if err != nil {
		return result, fmt.Errorf("inspect Plugin before uninstall: %w", err)
	}
	marketplacePresent := marketplaceFact.Presence == adapter.MarketplacePresent
	marketplaceAbsent := marketplaceFact.Presence == adapter.MarketplaceAbsent
	pluginPresent := pluginFact.Presence == adapter.PluginPresent
	pluginAbsent := pluginFact.Presence == adapter.PluginAbsent

	// 双缺省无需 journal：只清理残留 manifest，且不产生任何 Agent 写操作。
	if marketplaceAbsent && pluginAbsent {
		result.TargetMissing = true
		if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
			return result, fmt.Errorf("remove missing marketplace Plugin manifest entry: %w", err)
		}
		return result, nil
	}
	// 任何现存事实必须精确匹配，否则在 journal 创建前失败闭合，保证零写入。
	if marketplacePresent && (marketplaceFact.Name != request.Marketplace.Name || marketplaceFact.Source != request.Marketplace.Source) {
		return result, fmt.Errorf("marketplace Plugin uninstall pre-state is partial or conflicting: %w", ErrIndeterminateState)
	}
	if pluginPresent && (pluginFact.ActualName != request.Target.ActualName || pluginFact.Selector != request.Target.Selector) {
		return result, fmt.Errorf("marketplace Plugin uninstall pre-state is partial or conflicting: %w", ErrIndeterminateState)
	}
	if (!marketplaceAbsent && !marketplacePresent) || (!pluginAbsent && !pluginPresent) {
		return result, fmt.Errorf("marketplace Plugin uninstall pre-state is indeterminate: %w", ErrIndeterminateState)
	}
	result.TargetMissing = !pluginPresent || !marketplacePresent

	journal, err := s.newLocalMarketplacePluginUninstallJournal(request, pluginPresent, marketplacePresent)
	if err != nil {
		return result, err
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create marketplace Plugin uninstall journal: %w", err)
	}

	if pluginPresent {
		mutation, mutationErr := request.Adapter.Remove(ctx, request.Target)
		switch mutation.State {
		case adapter.PluginMutationApplied:
		case adapter.PluginMutationNotApplied:
			return result, pluginMutationError("remove Plugin", mutationErr)
		case adapter.PluginMutationUnknown:
			return result, indeterminatePluginMutation("remove Plugin", mutationErr)
		default:
			return result, indeterminatePluginMutation(fmt.Sprintf("remove Plugin returned state %q", mutation.State), mutationErr)
		}
		journal.Phase = PhaseUnregisteringMarketplace
		if err := s.journals.Update(journal); err != nil {
			return result, errors.Join(mutationErr, fmt.Errorf("persist marketplace Plugin uninstall unregister intent: %w", err))
		}
		if mutationErr != nil {
			return result, mutationErr
		}
	} else {
		journal.Phase = PhaseUnregisteringMarketplace
		if err := s.journals.Update(journal); err != nil {
			return result, fmt.Errorf("persist marketplace Plugin uninstall unregister intent: %w", err)
		}
	}

	if marketplacePresent {
		mutation, mutationErr := request.MarketplaceAdapter.UnregisterMarketplace(ctx, *request.Marketplace)
		switch mutation.State {
		case adapter.PluginMutationApplied:
		case adapter.PluginMutationNotApplied:
			return result, pluginMutationError("unregister marketplace", mutationErr)
		case adapter.PluginMutationUnknown:
			return result, indeterminatePluginMutation("unregister marketplace", mutationErr)
		default:
			return result, indeterminatePluginMutation(fmt.Sprintf("unregister marketplace returned state %q", mutation.State), mutationErr)
		}
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return result, errors.Join(mutationErr, fmt.Errorf("persist marketplace Plugin uninstall committing: %w", err))
		}
		if mutationErr != nil {
			return result, mutationErr
		}
	} else {
		journal.Phase = PhaseCommitting
		if err := s.journals.Update(journal); err != nil {
			return result, fmt.Errorf("persist marketplace Plugin uninstall committing: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove marketplace Plugin manifest: %w", err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return result, fmt.Errorf("delete completed marketplace Plugin uninstall journal: %w", err)
	}
	return result, nil
}

func (s *Service) newLocalMarketplacePluginUninstallJournal(request PluginUninstallRequest, pluginPresent, marketplacePresent bool) (Journal, error) {
	id, err := s.newID()
	if err != nil {
		return Journal{}, err
	}
	return Journal{
		SchemaVersion: 1,
		TransactionID: id,
		Driver:        DriverPlugin,
		Operation:     OperationUninstall,
		Phase:         PhaseRemovingPlugin,
		Agent:         request.Agent,
		Power:         request.Power,
		Plugin: &PluginJournalPayload{
			Deployment:    domain.PluginDeploymentMarketplace,
			Target:        request.Target,
			TargetPresent: pluginPresent,
			Marketplace: &PluginMarketplaceJournalPayload{
				Name:          request.Marketplace.Name,
				TargetSource:  request.Marketplace.Source,
				TargetPresent: marketplacePresent,
			},
		},
	}, nil
}

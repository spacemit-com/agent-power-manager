package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/adapter"
	"apm/internal/domain"
	"apm/internal/paths"
)

type marketplaceRecoverySelection uint8

const (
	marketplaceRecoveryAbsent marketplaceRecoverySelection = iota
	marketplaceRecoveryPrevious
	marketplaceRecoveryTarget
)

type marketplaceRecoveryFacts struct {
	marketplace     marketplaceRecoverySelection
	targetPresent   bool
	previousPresent bool
}

// recoverLocalMarketplacePlugin 先固定恢复方向，再按 journal 中的持久来源收敛 marketplace 与 Plugin；该过程不得下载或生成 artifact。
func (s *Service) recoverLocalMarketplacePlugin(ctx context.Context, journal Journal, result *RecoveryResult) error {
	if isNilInterface(s.pluginAdapters) {
		return fmt.Errorf("marketplace Plugin recovery resolver is nil: %w", ErrInvalidRequest)
	}
	pluginAdapter, err := s.pluginAdapters.ResolvePlugin(journal.Agent)
	if err != nil {
		return errors.Join(fmt.Errorf("resolve marketplace Plugin adapter for %q: %w", journal.Agent, err), ErrIndeterminateState)
	}
	marketplaceAdapter, ok := pluginAdapter.(adapter.MarketplacePluginAdapter)
	if isNilInterface(pluginAdapter) || !ok || isNilInterface(marketplaceAdapter) ||
		pluginAdapter.DeploymentMode() != domain.PluginDeploymentMarketplace {
		return fmt.Errorf("resolved Plugin adapter has no marketplace capability for %q: %w", journal.Agent, ErrInvalidRequest)
	}
	facts, err := inspectMarketplaceRecoveryFacts(ctx, journal, marketplaceAdapter)
	if err != nil {
		return err
	}
	switch journal.Operation {
	case OperationInstall:
		return s.recoverLocalMarketplacePluginInstall(ctx, journal, facts, marketplaceAdapter, result)
	case OperationUninstall:
		return s.recoverLocalMarketplacePluginUninstall(journal, facts, result)
	default:
		return fmt.Errorf("marketplace Plugin recovery operation %q: %w", journal.Operation, ErrIndeterminateState)
	}
}

func inspectMarketplaceRecoveryFacts(ctx context.Context, journal Journal, marketplaceAdapter adapter.MarketplacePluginAdapter) (marketplaceRecoveryFacts, error) {
	payload := journal.Plugin
	marketplace := payload.Marketplace
	fact, err := marketplaceAdapter.InspectMarketplace(ctx, marketplace.Name)
	if err != nil {
		return marketplaceRecoveryFacts{}, errors.Join(fmt.Errorf("inspect marketplace %q during recovery: %w", marketplace.Name, err), ErrIndeterminateState)
	}
	selection, err := classifyRecoveryMarketplaceFact(fact, *marketplace)
	if err != nil {
		return marketplaceRecoveryFacts{}, err
	}
	targetPresent, err := inspectExactRecoveryPlugin(ctx, marketplaceAdapter, payload.Target)
	if err != nil {
		return marketplaceRecoveryFacts{}, err
	}
	facts := marketplaceRecoveryFacts{marketplace: selection, targetPresent: targetPresent}
	if payload.Previous != nil {
		facts.previousPresent, err = inspectExactRecoveryPlugin(ctx, marketplaceAdapter, *payload.Previous)
		if err != nil {
			return marketplaceRecoveryFacts{}, err
		}
	}
	return facts, nil
}

func classifyRecoveryMarketplaceFact(fact adapter.MarketplaceFact, journal PluginMarketplaceJournalPayload) (marketplaceRecoverySelection, error) {
	if fact.Name != journal.Name {
		return 0, fmt.Errorf("marketplace recovery returned name %q, want %q: %w", fact.Name, journal.Name, ErrIndeterminateState)
	}
	switch fact.Presence {
	case adapter.MarketplaceAbsent:
		if fact.Source != "" {
			return 0, fmt.Errorf("absent marketplace %q returned source %q: %w", fact.Name, fact.Source, ErrIndeterminateState)
		}
		return marketplaceRecoveryAbsent, nil
	case adapter.MarketplacePresent:
		switch fact.Source {
		case journal.TargetSource:
			return marketplaceRecoveryTarget, nil
		case journal.PreviousSource:
			if journal.PreviousPresent && journal.PreviousSource != "" {
				return marketplaceRecoveryPrevious, nil
			}
		}
		return 0, fmt.Errorf("marketplace %q points to unrecognized source %q: %w", fact.Name, fact.Source, ErrIndeterminateState)
	default:
		return 0, fmt.Errorf("marketplace %q has presence %q: %w", fact.Name, fact.Presence, ErrIndeterminateState)
	}
}

func inspectExactRecoveryPlugin(ctx context.Context, pluginAdapter adapter.PluginAdapter, target adapter.PluginTarget) (bool, error) {
	fact, err := pluginAdapter.Inspect(ctx, target.ActualName)
	if err != nil {
		return false, errors.Join(fmt.Errorf("inspect Plugin %q during recovery: %w", target.ActualName, err), ErrIndeterminateState)
	}
	if fact.ActualName != target.ActualName {
		return false, fmt.Errorf("Plugin recovery returned actual name %q, want %q: %w", fact.ActualName, target.ActualName, ErrIndeterminateState)
	}
	switch fact.Presence {
	case adapter.PluginAbsent:
		if fact.Selector != "" {
			return false, fmt.Errorf("absent Plugin %q returned selector %q: %w", fact.ActualName, fact.Selector, ErrIndeterminateState)
		}
		return false, nil
	case adapter.PluginPresent:
		if fact.Selector != target.Selector {
			return false, fmt.Errorf("Plugin %q selector %q does not match %q: %w", fact.ActualName, fact.Selector, target.Selector, ErrIndeterminateState)
		}
		return true, nil
	default:
		return false, fmt.Errorf("Plugin %q has presence %q: %w", fact.ActualName, fact.Presence, ErrIndeterminateState)
	}
}

// recoverLocalMarketplacePluginInstall 按持久化 phase 选择一次 commit 或 rollback；已固定的方向永不反转。
func (s *Service) recoverLocalMarketplacePluginInstall(ctx context.Context, journal Journal, facts marketplaceRecoveryFacts, marketplaceAdapter adapter.MarketplacePluginAdapter, result *RecoveryResult) error {
	direction, err := selectMarketplaceInstallRecoveryDirection(journal, facts)
	if err != nil {
		return err
	}
	result.Direction = direction
	if direction == RecoveryCommit {
		if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
			return err
		}
		if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.TargetSource); err != nil {
			return err
		}
		return s.commitRecoveredPlugin(journal, result)
	}
	if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
		return err
	}
	if journal.Plugin.PreviousPresent && (facts.marketplace != marketplaceRecoveryPrevious || !facts.previousPresent) {
		if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.PreviousSource); err != nil {
			return err
		}
	}
	return s.rollbackRecoveredMarketplacePlugin(ctx, journal, facts, marketplaceAdapter, result)
}

func selectMarketplaceInstallRecoveryDirection(journal Journal, facts marketplaceRecoveryFacts) (RecoveryDirection, error) {
	previousExpected := journal.Plugin.PreviousPresent
	if journal.Phase == PhaseCommitting {
		if facts.marketplace != marketplaceRecoveryTarget || !facts.targetPresent || previousExpected && facts.previousPresent {
			return "", fmt.Errorf("committing marketplace Plugin facts are not exact target state: %w", ErrIndeterminateState)
		}
		return RecoveryCommit, nil
	}
	if journal.Phase == PhaseRollingBack {
		if err := validateMarketplaceRollbackFacts(journal, facts); err != nil {
			return "", err
		}
		return RecoveryRollback, nil
	}
	if journal.Phase == PhaseAddingNewPlugin {
		if facts.marketplace != marketplaceRecoveryTarget || previousExpected && facts.previousPresent {
			return "", fmt.Errorf("adding marketplace Plugin facts are not a reachable target state: %w", ErrIndeterminateState)
		}
		if facts.targetPresent {
			return RecoveryCommit, nil
		}
		return RecoveryRollback, nil
	}
	if facts.targetPresent {
		return "", fmt.Errorf("target Plugin is present before add phase: %w", ErrIndeterminateState)
	}
	switch journal.Phase {
	case PhaseRegisteringMarketplace:
		if previousExpected || facts.marketplace == marketplaceRecoveryPrevious {
			return "", fmt.Errorf("first-install marketplace facts contain previous state: %w", ErrIndeterminateState)
		}
	case PhaseRemovingOldPlugin:
		if !previousExpected || facts.marketplace != marketplaceRecoveryPrevious {
			return "", fmt.Errorf("old-Plugin removal facts lost previous marketplace: %w", ErrIndeterminateState)
		}
	case PhaseUnregisteringOldMarketplace:
		if !previousExpected || facts.previousPresent || facts.marketplace == marketplaceRecoveryTarget {
			return "", fmt.Errorf("old-marketplace removal facts are unreachable: %w", ErrIndeterminateState)
		}
	case PhaseRegisteringNewMarketplace:
		if !previousExpected || facts.previousPresent || facts.marketplace == marketplaceRecoveryPrevious {
			return "", fmt.Errorf("new-marketplace registration facts are unreachable: %w", ErrIndeterminateState)
		}
	default:
		return "", fmt.Errorf("marketplace Plugin install phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
	return RecoveryRollback, nil
}

func validateMarketplaceRollbackFacts(journal Journal, facts marketplaceRecoveryFacts) error {
	if !journal.Plugin.PreviousPresent {
		if facts.marketplace == marketplaceRecoveryPrevious ||
			(facts.marketplace == marketplaceRecoveryAbsent && facts.targetPresent) || facts.previousPresent {
			return fmt.Errorf("first-install rollback facts are unreachable: %w", ErrIndeterminateState)
		}
		return nil
	}
	switch facts.marketplace {
	case marketplaceRecoveryTarget:
		if facts.previousPresent {
			return fmt.Errorf("target marketplace coexists with previous Plugin during rollback: %w", ErrIndeterminateState)
		}
	case marketplaceRecoveryAbsent:
		if facts.targetPresent || facts.previousPresent {
			return fmt.Errorf("absent marketplace coexists with a Plugin during rollback: %w", ErrIndeterminateState)
		}
	case marketplaceRecoveryPrevious:
		if facts.targetPresent {
			return fmt.Errorf("previous marketplace coexists with target Plugin during rollback: %w", ErrIndeterminateState)
		}
	default:
		return fmt.Errorf("unknown marketplace rollback fact %d: %w", facts.marketplace, ErrIndeterminateState)
	}
	return nil
}

// rollbackRecoveredMarketplacePlugin 采用固定逆序清理新状态、正序恢复旧状态，重复执行可继续收敛。
func (s *Service) rollbackRecoveredMarketplacePlugin(ctx context.Context, journal Journal, facts marketplaceRecoveryFacts, marketplaceAdapter adapter.MarketplacePluginAdapter, result *RecoveryResult) error {
	if facts.targetPresent {
		mutation, err := marketplaceAdapter.Remove(ctx, journal.Plugin.Target)
		if err := requireAppliedPluginMutation("remove target Plugin during recovery rollback", mutation, err); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRemovePlugin})
	}
	marketplace := journal.Plugin.Marketplace
	if facts.marketplace == marketplaceRecoveryTarget {
		mutation, err := marketplaceAdapter.UnregisterMarketplace(ctx, adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.TargetSource})
		if err := requireAppliedPluginMutation("unregister target marketplace during recovery rollback", mutation, err); err != nil {
			return err
		}
		facts.marketplace = marketplaceRecoveryAbsent
	}
	if marketplace.PreviousPresent && facts.marketplace == marketplaceRecoveryAbsent {
		mutation, err := marketplaceAdapter.RegisterMarketplace(ctx, adapter.MarketplaceTarget{Name: marketplace.Name, Source: marketplace.PreviousSource})
		if err := requireAppliedPluginMutation("register previous marketplace during recovery rollback", mutation, err); err != nil {
			return err
		}
	}
	if journal.Plugin.PreviousPresent && !facts.previousPresent {
		mutation, err := marketplaceAdapter.Add(ctx, *journal.Plugin.Previous)
		if err := requireAppliedPluginMutation("restore previous Plugin during recovery rollback", mutation, err); err != nil {
			return err
		}
		result.Actions = append(result.Actions, RecoveryAction{Kind: RecoveryActionRestorePlugin})
	}
	return s.deleteRecoveredJournal(journal)
}

func (s *Service) recoverLocalMarketplacePluginUninstall(journal Journal, facts marketplaceRecoveryFacts, result *RecoveryResult) error {
	if facts.marketplace != marketplaceRecoveryTarget {
		return fmt.Errorf("uninstall marketplace source changed or disappeared: %w", ErrIndeterminateState)
	}
	switch journal.Phase {
	case PhaseCommitting:
		if facts.targetPresent {
			return fmt.Errorf("committing Plugin uninstall target is still present: %w", ErrIndeterminateState)
		}
		result.Direction = RecoveryCommit
		if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.TargetSource); err != nil {
			return err
		}
		return s.commitRecoveredPlugin(journal, result)
	case PhaseRollingBack:
		if !facts.targetPresent {
			return fmt.Errorf("rolled-back Plugin uninstall target is absent: %w", ErrIndeterminateState)
		}
		result.Direction = RecoveryRollback
		if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.TargetSource); err != nil {
			return err
		}
		return s.deleteRecoveredJournal(journal)
	case PhaseRemovingPlugin:
		if facts.targetPresent {
			result.Direction = RecoveryRollback
			if err := s.persistRecoveryDirection(&journal, PhaseRollingBack); err != nil {
				return err
			}
			if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.TargetSource); err != nil {
				return err
			}
			return s.deleteRecoveredJournal(journal)
		}
		result.Direction = RecoveryCommit
		if err := s.persistRecoveryDirection(&journal, PhaseCommitting); err != nil {
			return err
		}
		if err := s.validateMarketplaceRecoverySource(journal.Plugin.Marketplace.TargetSource); err != nil {
			return err
		}
		return s.commitRecoveredPlugin(journal, result)
	default:
		return fmt.Errorf("marketplace Plugin uninstall phase %q: %w", journal.Phase, ErrIndeterminateState)
	}
}

// validateMarketplaceRecoverySource 只校验当前收敛动作实际依赖的持久缓存，避免无关历史版本阻断既定方向。
func (s *Service) validateMarketplaceRecoverySource(source string) error {
	cacheRoot, err := paths.CachePath()
	if err != nil {
		return errors.Join(fmt.Errorf("resolve Plugin recovery cache root: %w", err), ErrIndeterminateState)
	}
	return validateMarketplaceRecoverySourceTree(cacheRoot, source, s.files)
}

func validateMarketplaceRecoverySourceTree(cacheRoot, source string, files fileOps) error {
	relative, err := filepath.Rel(cacheRoot, source)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.Join(fmt.Errorf("marketplace recovery source %q escapes cache root %q", source, cacheRoot), err, ErrIndeterminateState)
	}
	current := cacheRoot
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := files.lstat(current)
		if err != nil {
			return errors.Join(fmt.Errorf("inspect marketplace recovery source %q: %w", current, err), ErrIndeterminateState)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.Join(fmt.Errorf("marketplace recovery source %q is not a physical directory", current), ErrUnsafePath, ErrIndeterminateState)
		}
	}
	return nil
}

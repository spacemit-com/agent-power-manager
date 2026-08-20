package transaction

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/adapter"
	"apm/internal/domain"
	filesystemutil "apm/internal/utils/filesystem"
)

type pluginFilesystemArtifacts struct {
	dir      string
	publish  string
	previous string
	target   string
}

func filesystemPluginArtifacts(target, transactionID string) (pluginFilesystemArtifacts, error) {
	if err := validateSafeAbsolutePath("Plugin target", target); err != nil {
		return pluginFilesystemArtifacts{}, err
	}
	if !validTransactionID(transactionID) {
		return pluginFilesystemArtifacts{}, fmt.Errorf("transaction id %q: %w", transactionID, ErrUnsafePath)
	}
	dir := filepath.Join(filepath.Dir(target), ".apm-transactions", transactionID)
	return pluginFilesystemArtifacts{dir: dir, publish: filepath.Join(dir, "publish"), previous: filepath.Join(dir, "previous"), target: filepath.Join(dir, "target")}, nil
}

func (s *Service) installFilesystemPlugin(ctx context.Context, request PluginInstallRequest) error {
	if request.Adapter.DeploymentMode() != domain.PluginDeploymentFilesystem {
		return fmt.Errorf("invalid filesystem Plugin deployment: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}
	targetFact, err := request.Adapter.Inspect(ctx, request.Target.ActualName)
	if err != nil {
		return fmt.Errorf("inspect filesystem Plugin target: %w", err)
	}
	targetPath, err := request.Adapter.TargetPath(request.Target.ActualName)
	if err != nil {
		return fmt.Errorf("resolve filesystem Plugin target: %w", err)
	}
	if err := validateSafeAbsolutePath("Plugin target", targetPath); err != nil {
		return err
	}
	var previousTarget *adapter.PluginTarget
	var previousPath string
	previousPresent := false
	if request.Previous != nil && request.Previous.Version != request.Power.Version {
		previousName, err := domain.PluginRuntimeName(*request.Previous)
		if err != nil {
			return err
		}
		previousPath, err = request.Adapter.TargetPath(previousName)
		if err != nil {
			return fmt.Errorf("resolve previous filesystem Plugin target: %w", err)
		}
		if previousPath != targetPath {
			return fmt.Errorf("filesystem Plugin version switch changed logical target path from %q to %q: %w", previousPath, targetPath, ErrInvalidRequest)
		}
		if targetFact.Presence != adapter.PluginPresent || targetFact.ActualName != previousName || targetFact.Selector != previousName {
			return fmt.Errorf("previous filesystem Plugin state does not match logical target: %w", ErrIndeterminateState)
		}
		previousPresent = true
		previousTarget = &adapter.PluginTarget{ActualName: previousName, Selector: previousName}
	} else {
		request.Previous = nil
		if targetFact.Presence == adapter.PluginPresent {
			return fmt.Errorf("filesystem Plugin target is already registered: %w", adapter.ErrPluginConflict)
		}
		if targetFact.Presence != adapter.PluginAbsent || targetFact.ActualName != request.Target.ActualName {
			return fmt.Errorf("filesystem Plugin target fact is not absent: %w", ErrIndeterminateState)
		}
		if exists, err := s.pathExists(targetPath); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("filesystem Plugin target %q exists: %w", targetPath, adapter.ErrTargetConflict)
		}
	}
	id, err := s.newID()
	if err != nil {
		return err
	}
	artifacts, err := filesystemPluginArtifacts(targetPath, id)
	if err != nil {
		return err
	}
	source := filepath.Join(request.StagingRoot, "plugins", request.Power.Name)
	phase := PhaseAddingNew
	if previousPresent {
		phase = PhaseRemovingOld
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationInstall,
		Phase: phase, Agent: request.Agent, Power: request.Power, Previous: clonePowerRef(request.Previous),
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentFilesystem,
			Target:     request.Target,
			Previous:   clonePluginTarget(previousTarget), PreviousPresent: previousPresent,
			Filesystem: &PluginFilesystemJournalPayload{
				StagingRoot: request.StagingRoot, SourcePath: source, TargetPath: targetPath,
				TransactionDir: artifacts.dir, PublishPath: artifacts.publish,
				PreviousTargetPath: previousPath,
			},
		},
	}
	if request.Previous != nil {
		journal.Plugin.Filesystem.PreviousQuarantinePath = artifacts.previous
	}
	if err := validateJournal(journal); err != nil {
		return err
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create filesystem Plugin journal: %w", err)
	}
	if err := s.ensurePluginTransactionDirectories(targetPath, artifacts); err != nil {
		return err
	}
	if previousPresent {
		// 同名版本切换必须先隔离旧目录，之后 targetPath 才能用于原子发布新版本。
		mutation, removeErr := request.Adapter.Remove(ctx, *previousTarget)
		if err := requireAppliedPluginMutation("disable previous filesystem Plugin", mutation, removeErr); err != nil {
			return err
		}
		if exists, err := s.pathExists(previousPath); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("previous filesystem Plugin target %q is missing: %w", previousPath, ErrIndeterminateState)
		}
		if err := s.files.rename(previousPath, artifacts.previous); err != nil {
			return fmt.Errorf("quarantine previous Plugin %q: %w", previousPath, err)
		}
		journal.Phase = PhaseAddingNew
		if err := s.journals.Update(journal); err != nil {
			return fmt.Errorf("persist filesystem Plugin adding-new: %w", err)
		}
	}
	if err := filesystemutil.CopyDir(source, artifacts.publish); err != nil {
		return fmt.Errorf("copy Plugin source %q to %q: %w", source, artifacts.publish, err)
	}
	if exists, err := s.pathExists(targetPath); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("Plugin target appeared before publication: %w", adapter.ErrTargetConflict)
	}
	if err := s.files.rename(artifacts.publish, targetPath); err != nil {
		return fmt.Errorf("publish Plugin target %q: %w", targetPath, err)
	}
	mutation, addErr := request.Adapter.Add(ctx, journal.Plugin.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		if addErr != nil {
			return addErr
		}
	case adapter.PluginMutationNotApplied:
		return s.rollbackFilesystemPluginInstall(ctx, journal, request.Adapter, pluginMutationError("enable filesystem Plugin", addErr))
	case adapter.PluginMutationUnknown:
		return indeterminatePluginMutation("enable filesystem Plugin", addErr)
	default:
		return indeterminatePluginMutation(fmt.Sprintf("enable filesystem Plugin returned state %q", mutation.State), addErr)
	}
	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return fmt.Errorf("persist filesystem Plugin committing: %w", err)
	}
	if err := s.manifests.Set(request.Agent, request.Power); err != nil {
		return fmt.Errorf("set filesystem Plugin manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.previous); err != nil {
		return fmt.Errorf("remove previous Plugin quarantine %q: %w", artifacts.previous, err)
	}
	if err := cleanupInstallStagingWithOps(request.StagingRoot, targetPath, s.files); err != nil {
		return err
	}
	if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
		return err
	}
	if err := s.journals.Delete(id); err != nil {
		return fmt.Errorf("delete completed filesystem Plugin journal: %w", err)
	}
	return nil
}

func (s *Service) rollbackFilesystemPluginInstall(ctx context.Context, journal Journal, pluginAdapter adapter.PluginAdapter, originalErr error) error {
	journal.Phase = PhaseRollingBack
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(originalErr, fmt.Errorf("persist filesystem Plugin rollback: %w", err))
	}
	filesystem := journal.Plugin.Filesystem
	if exists, err := s.pathExists(filesystem.TargetPath); err != nil {
		return errors.Join(originalErr, err)
	} else if exists {
		if err := s.files.removeAll(filesystem.TargetPath); err != nil {
			return errors.Join(originalErr, err)
		}
	}
	if journal.Plugin.PreviousPresent && journal.Plugin.Previous != nil {
		quarantineExists, err := s.pathExists(filesystem.PreviousQuarantinePath)
		if err != nil {
			return errors.Join(originalErr, err)
		}
		if quarantineExists {
			if err := s.files.rename(filesystem.PreviousQuarantinePath, filesystem.PreviousTargetPath); err != nil {
				return errors.Join(originalErr, err)
			}
			mutation, err := pluginAdapter.Add(ctx, *journal.Plugin.Previous)
			if appliedErr := requireAppliedPluginMutation("restore previous filesystem Plugin", mutation, err); appliedErr != nil {
				return errors.Join(originalErr, appliedErr)
			}
		}
	}
	artifacts, _ := filesystemPluginArtifacts(filesystem.TargetPath, journal.TransactionID)
	if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
		return errors.Join(originalErr, err)
	}
	if err := s.journals.Delete(journal.TransactionID); err != nil {
		return errors.Join(originalErr, err)
	}
	return originalErr
}

func (s *Service) ensurePluginTransactionDirectories(target string, artifacts pluginFilesystemArtifacts) error {
	pluginsRoot := filepath.Dir(target)
	info, err := s.files.lstat(pluginsRoot)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := s.files.mkdir(pluginsRoot, 0o755); err != nil {
			return fmt.Errorf("create Plugin root %q: %w", pluginsRoot, err)
		}
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return fmt.Errorf("Plugin root %q is not a physical directory: %w", pluginsRoot, ErrUnsafePath)
	}
	return ensureTransactionDirWithOps(transactionArtifacts{dir: artifacts.dir}, s.files)
}

func (s *Service) cleanupPluginTransactionDir(artifacts pluginFilesystemArtifacts) error {
	root := filepath.Dir(artifacts.dir)
	if err := s.files.removeAll(artifacts.dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Plugin transaction directory %q: %w", artifacts.dir, err)
	}
	entries, err := s.files.readDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		if err := s.files.remove(root); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Service) uninstallFilesystemPlugin(ctx context.Context, request PluginUninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}
	fact, err := request.Adapter.Inspect(ctx, request.Target.ActualName)
	if err != nil {
		return result, fmt.Errorf("inspect filesystem Plugin before uninstall: %w", err)
	}
	if fact.Presence == adapter.PluginAbsent {
		result.TargetMissing = true
		if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
			return result, err
		}
		return result, nil
	}
	if fact.Presence != adapter.PluginPresent {
		return result, fmt.Errorf("unexpected filesystem Plugin presence %q: %w", fact.Presence, ErrIndeterminateState)
	}
	targetPath, err := request.Adapter.TargetPath(request.Target.ActualName)
	if err != nil {
		return result, err
	}
	id, err := s.newID()
	if err != nil {
		return result, err
	}
	artifacts, err := filesystemPluginArtifacts(targetPath, id)
	if err != nil {
		return result, err
	}
	journal := Journal{
		SchemaVersion: 1, TransactionID: id, Driver: DriverPlugin, Operation: OperationUninstall,
		Phase: PhaseRemoving, Agent: request.Agent, Power: request.Power,
		Plugin: &PluginJournalPayload{
			Deployment: domain.PluginDeploymentFilesystem, Target: adapter.PluginTarget{ActualName: fact.ActualName, Selector: fact.Selector}, TargetPresent: true,
			Filesystem: &PluginFilesystemJournalPayload{TargetPath: targetPath, TransactionDir: artifacts.dir, TargetQuarantinePath: artifacts.target},
		},
	}
	if err := validateJournal(journal); err != nil {
		return result, err
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create filesystem Plugin uninstall journal: %w", err)
	}
	if err := s.ensurePluginTransactionDirectories(targetPath, artifacts); err != nil {
		return result, err
	}
	mutation, removeErr := request.Adapter.Remove(ctx, journal.Plugin.Target)
	switch mutation.State {
	case adapter.PluginMutationApplied:
		if removeErr != nil {
			return result, removeErr
		}
	case adapter.PluginMutationNotApplied:
		return result, s.finishPluginRollbackWithoutMutation(journal, pluginMutationError("disable filesystem Plugin", removeErr))
	case adapter.PluginMutationUnknown:
		return result, indeterminatePluginMutation("disable filesystem Plugin", removeErr)
	default:
		return result, indeterminatePluginMutation(fmt.Sprintf("disable filesystem Plugin returned state %q", mutation.State), removeErr)
	}
	if exists, err := s.pathExists(targetPath); err != nil {
		return result, err
	} else if !exists {
		return result, fmt.Errorf("filesystem Plugin target disappeared before quarantine: %w", ErrIndeterminateState)
	}
	if err := s.files.rename(targetPath, artifacts.target); err != nil {
		return result, fmt.Errorf("quarantine filesystem Plugin target: %w", err)
	}
	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return result, fmt.Errorf("persist filesystem Plugin uninstall committing: %w", err)
	}
	if err := s.manifests.Remove(request.Agent, domain.PowerTypePlugin, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove filesystem Plugin manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.target); err != nil {
		return result, fmt.Errorf("remove filesystem Plugin quarantine: %w", err)
	}
	if err := s.cleanupPluginTransactionDir(artifacts); err != nil {
		return result, err
	}
	if err := s.journals.Delete(id); err != nil {
		return result, fmt.Errorf("delete filesystem Plugin uninstall journal: %w", err)
	}
	return result, nil
}

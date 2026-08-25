package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/lock"
)

// SyncStatus 描述本次目录同步是否更新或以旧快照降级；Stale 时 Cause 保留一次上层 warning 的根因。
type SyncStatus struct {
	Updated bool
	Stale   bool
	Cause   error
}

// Manager 编排目录缓存同步；Provider 只提供字节来源，Repository 独占本地校验与原子发布。
type Manager interface {
	// Snapshot 非阻塞获取并释放全局 APM 锁后同步目录；锁竞争直接返回 lock.ErrLocked。
	Snapshot(context.Context) (Snapshot, SyncStatus, error)
	// SnapshotLocked 在调用方已持有全局 APM 锁时同步目录；不得用于未持锁路径，也不会再次获取锁。
	SnapshotLocked(context.Context) (Snapshot, SyncStatus, error)
}

type manager struct {
	source     cloud.CatalogSource
	repository Repository
	locker     lock.Locker
	tempRoot   string
	ops        managerOps
}

type managerOps struct {
	mkdirAll  func(string, os.FileMode) error
	mkdirTemp func(string, string) (string, error)
	removeAll func(string) error
}

// NewManager 创建只负责目录同步策略的 Manager；tempRoot 必须是绝对、规范的临时目录根路径。
func NewManager(source cloud.CatalogSource, repository Repository, locker lock.Locker, tempRoot string) (Manager, error) {
	if source == nil {
		return nil, errors.New("catalog source is required")
	}
	if repository == nil {
		return nil, errors.New("catalog repository is required")
	}
	if locker == nil {
		return nil, errors.New("catalog locker is required")
	}
	if tempRoot == "" || !filepath.IsAbs(tempRoot) || filepath.Clean(tempRoot) != tempRoot {
		return nil, fmt.Errorf("catalog temporary root %q must be absolute and clean", tempRoot)
	}
	return &manager{
		source:     source,
		repository: repository,
		locker:     locker,
		tempRoot:   tempRoot,
		ops: managerOps{
			mkdirAll:  os.MkdirAll,
			mkdirTemp: os.MkdirTemp,
			removeAll: os.RemoveAll,
		},
	}, nil
}

// Snapshot 非阻塞获取全局锁，并在释放后返回同步结果。
func (manager *manager) Snapshot(ctx context.Context) (snapshot Snapshot, status SyncStatus, resultErr error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, SyncStatus{}, err
	}
	unlock, err := manager.locker.Acquire()
	if err != nil {
		return Snapshot{}, SyncStatus{}, fmt.Errorf("acquire catalog synchronization lock: %w", err)
	}
	defer func() {
		if releaseErr := unlock.Release(); releaseErr != nil {
			primaryErr := primaryResultError(status, resultErr)
			snapshot = Snapshot{}
			status = SyncStatus{}
			resultErr = errors.Join(primaryErr, fmt.Errorf("release catalog synchronization lock: %w", releaseErr))
		}
	}()
	return manager.snapshotLocked(ctx)
}

// SnapshotLocked 在全局锁已由调用方持有时同步目录，避免非重入锁的二次获取。
func (manager *manager) SnapshotLocked(ctx context.Context) (Snapshot, SyncStatus, error) {
	return manager.snapshotLocked(ctx)
}

func (manager *manager) snapshotLocked(ctx context.Context) (snapshot Snapshot, status SyncStatus, resultErr error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, SyncStatus{}, err
	}

	localSnapshot, localVersion, loadErr := manager.repository.Load()
	if loadErr != nil && !repairableCatalogLoadError(loadErr) {
		return Snapshot{}, SyncStatus{}, fmt.Errorf("load catalog: %w", loadErr)
	}
	hasLocalSnapshot := loadErr == nil
	if err := contextError(ctx); err != nil {
		return Snapshot{}, SyncStatus{}, err
	}

	remoteVersion, err := manager.source.CatalogVersion(ctx)
	if contextErr := contextError(ctx); contextErr != nil {
		return Snapshot{}, SyncStatus{}, contextErr
	}
	if err != nil {
		return manager.providerFailure(localSnapshot, hasLocalSnapshot, err)
	}
	if err := remoteVersion.Validate(); err != nil {
		return manager.providerFailure(localSnapshot, hasLocalSnapshot, fmt.Errorf("validate provider catalog version: %w", err))
	}
	if remoteVersion.Size > MaxCatalogSize {
		return manager.providerFailure(localSnapshot, hasLocalSnapshot, fmt.Errorf("provider catalog size %d exceeds %d bytes: %w", remoteVersion.Size, MaxCatalogSize, ErrInvalidCatalog))
	}
	if hasLocalSnapshot && remoteVersion.SHA256 == localVersion.SHA256 {
		return localSnapshot, SyncStatus{}, nil
	}

	temporaryDirectory, err := manager.createTemporaryDirectory()
	if err != nil {
		return Snapshot{}, SyncStatus{}, fmt.Errorf("create catalog temporary directory: %w", err)
	}
	defer func() {
		if cleanupErr := manager.ops.removeAll(temporaryDirectory); cleanupErr != nil {
			primaryErr := primaryResultError(status, resultErr)
			snapshot = Snapshot{}
			status = SyncStatus{}
			resultErr = errors.Join(primaryErr, fmt.Errorf("cleanup catalog temporary directory: %w", cleanupErr))
		}
	}()
	destination := filepath.Join(temporaryDirectory, "catalog.json")
	downloadErr := manager.source.DownloadCatalog(ctx, destination)
	if contextErr := contextError(ctx); contextErr != nil {
		return Snapshot{}, SyncStatus{}, contextErr
	}
	if downloadErr != nil {
		if errors.Is(downloadErr, cloud.ErrCatalogDestination) {
			return Snapshot{}, SyncStatus{}, fmt.Errorf("download catalog: %w", downloadErr)
		}
		return manager.providerFailure(localSnapshot, hasLocalSnapshot, downloadErr)
	}

	// Replace 需要读取 staged 文件，因此 defer 在替换完成后才清理临时目录。
	return manager.replaceDownloaded(ctx, localSnapshot, hasLocalSnapshot, remoteVersion, destination)
}

func (manager *manager) createTemporaryDirectory() (string, error) {
	if err := manager.ops.mkdirAll(manager.tempRoot, 0o700); err != nil {
		return "", err
	}
	return manager.ops.mkdirTemp(manager.tempRoot, ".catalog-")
}

func (manager *manager) replaceDownloaded(ctx context.Context, localSnapshot Snapshot, hasLocalSnapshot bool, version domain.CatalogVersion, sourcePath string) (Snapshot, SyncStatus, error) {
	snapshot, replaceErr := manager.repository.Replace(version, sourcePath)
	if contextErr := contextError(ctx); contextErr != nil {
		return Snapshot{}, SyncStatus{}, contextErr
	}
	if replaceErr != nil {
		if errors.Is(replaceErr, ErrCatalogSourceInvalid) {
			return manager.providerFailure(localSnapshot, hasLocalSnapshot, replaceErr)
		}
		return Snapshot{}, SyncStatus{}, fmt.Errorf("replace catalog: %w", replaceErr)
	}
	return snapshot, SyncStatus{Updated: true}, nil
}

func (manager *manager) providerFailure(snapshot Snapshot, hasSnapshot bool, cause error) (Snapshot, SyncStatus, error) {
	if hasSnapshot {
		return snapshot, SyncStatus{Stale: true, Cause: cause}, nil
	}
	return Snapshot{}, SyncStatus{}, fmt.Errorf("%w: %w", ErrCatalogUnavailable, cause)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("catalog synchronization context is required")
	}
	return ctx.Err()
}

func repairableCatalogLoadError(err error) bool {
	if hasMultipleErrorCauses(err) {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrInvalidCatalog)
}

func hasMultipleErrorCauses(err error) bool {
	for err != nil {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			return len(joined.Unwrap()) > 1
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}

func primaryResultError(status SyncStatus, resultErr error) error {
	if resultErr != nil {
		return resultErr
	}
	if status.Stale {
		return status.Cause
	}
	return nil
}

var _ Manager = (*manager)(nil)

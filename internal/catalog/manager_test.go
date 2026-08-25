package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/lock"
)

func TestManagerStateTable(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "local message")
	remoteSnapshot, remoteVersion := managerSnapshot(t, "remote message")
	versionFailure := errors.New("version unavailable")
	downloadFailure := errors.New("download unavailable")
	replaceFailure := fmt.Errorf("catalog hash or schema mismatch: %w", ErrCatalogSourceInvalid)

	tests := []struct {
		name          string
		loadSnapshot  Snapshot
		loadVersion   domain.CatalogVersion
		loadErr       error
		version       domain.CatalogVersion
		versionErr    error
		downloadErr   error
		replaceErr    error
		wantSnapshot  Snapshot
		wantUpdated   bool
		wantStale     bool
		wantCause     error
		wantErr       error
		wantDownloads int
	}{
		{
			name:          "no cache first sync",
			loadErr:       os.ErrNotExist,
			version:       remoteVersion,
			wantSnapshot:  remoteSnapshot,
			wantUpdated:   true,
			wantDownloads: 1,
		},
		{
			name:          "matching hash skips download",
			loadSnapshot:  localSnapshot,
			loadVersion:   localVersion,
			version:       localVersion,
			wantSnapshot:  localSnapshot,
			wantDownloads: 0,
		},
		{
			name:          "different hash downloads and replaces",
			loadSnapshot:  localSnapshot,
			loadVersion:   localVersion,
			version:       remoteVersion,
			wantSnapshot:  remoteSnapshot,
			wantUpdated:   true,
			wantDownloads: 1,
		},
		{
			name:          "damaged cache repairs online",
			loadErr:       fmt.Errorf("damaged cache: %w", ErrInvalidCatalog),
			version:       remoteVersion,
			wantSnapshot:  remoteSnapshot,
			wantUpdated:   true,
			wantDownloads: 1,
		},
		{
			name:         "version failure uses valid stale cache",
			loadSnapshot: localSnapshot,
			loadVersion:  localVersion,
			versionErr:   versionFailure,
			wantSnapshot: localSnapshot,
			wantStale:    true,
			wantCause:    versionFailure,
		},
		{
			name:          "download failure uses valid stale cache",
			loadSnapshot:  localSnapshot,
			loadVersion:   localVersion,
			version:       remoteVersion,
			downloadErr:   downloadFailure,
			wantSnapshot:  localSnapshot,
			wantStale:     true,
			wantCause:     downloadFailure,
			wantDownloads: 1,
		},
		{
			name:          "hash or schema failure uses valid stale cache",
			loadSnapshot:  localSnapshot,
			loadVersion:   localVersion,
			version:       remoteVersion,
			replaceErr:    replaceFailure,
			wantSnapshot:  localSnapshot,
			wantStale:     true,
			wantCause:     replaceFailure,
			wantDownloads: 1,
		},
		{
			name:       "version failure without valid cache is unavailable",
			loadErr:    os.ErrNotExist,
			versionErr: versionFailure,
			wantCause:  versionFailure,
			wantErr:    ErrCatalogUnavailable,
		},
		{
			name:          "download failure without valid cache is unavailable",
			loadErr:       os.ErrNotExist,
			version:       remoteVersion,
			downloadErr:   downloadFailure,
			wantCause:     downloadFailure,
			wantErr:       ErrCatalogUnavailable,
			wantDownloads: 1,
		},
		{
			name:          "hash or schema failure without valid cache is unavailable",
			loadErr:       fmt.Errorf("damaged cache: %w", ErrInvalidCatalog),
			version:       remoteVersion,
			replaceErr:    replaceFailure,
			wantCause:     replaceFailure,
			wantErr:       ErrCatalogUnavailable,
			wantDownloads: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := []string{}
			repository := &managerRepository{
				loadSnapshot:    tt.loadSnapshot,
				loadVersion:     tt.loadVersion,
				loadErr:         tt.loadErr,
				replaceSnapshot: remoteSnapshot,
				replaceErr:      tt.replaceErr,
				order:           &order,
			}
			source := &managerSource{version: tt.version, versionErr: tt.versionErr, downloadErr: tt.downloadErr, data: validCatalogJSON(), order: &order}
			manager := newManagerForTest(t, source, repository, &managerLocker{order: &order})

			snapshot, status, err := manager.SnapshotLocked(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SnapshotLocked() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) && !errors.Is(status.Cause, tt.wantCause) {
				t.Fatalf("SnapshotLocked() error/status cause = %v/%v, want root cause %v", err, status.Cause, tt.wantCause)
			}
			if tt.wantErr == nil && (status.Updated != tt.wantUpdated || status.Stale != tt.wantStale || !errors.Is(status.Cause, tt.wantCause)) {
				t.Fatalf("SnapshotLocked() status = %+v, want updated=%t stale=%t cause=%v", status, tt.wantUpdated, tt.wantStale, tt.wantCause)
			}
			if tt.wantStale && status.Cause != tt.wantCause {
				t.Fatalf("SnapshotLocked() stale cause = %v, want original %v", status.Cause, tt.wantCause)
			}
			if tt.wantErr != nil && status != (SyncStatus{}) {
				t.Fatalf("SnapshotLocked() status = %+v, want empty status with error", status)
			}
			if tt.wantErr == nil && !reflect.DeepEqual(snapshot.List(""), tt.wantSnapshot.List("")) {
				t.Fatalf("SnapshotLocked() snapshot = %#v, want %#v", snapshot.List(""), tt.wantSnapshot.List(""))
			}
			if source.downloadCalls != tt.wantDownloads {
				t.Fatalf("DownloadCatalog calls = %d, want %d", source.downloadCalls, tt.wantDownloads)
			}
			if source.versionCalls != 1 {
				t.Fatalf("CatalogVersion calls = %d, want exactly one", source.versionCalls)
			}
		})
	}
}

func TestManagerSourceSwitchWithMatchingHashSkipsDownload(t *testing.T) {
	snapshot, version := managerSnapshot(t, "cached")
	first := &managerSource{version: version, data: validCatalogJSON()}
	second := &managerSource{version: version, data: validCatalogJSON()}

	for _, source := range []*managerSource{first, second} {
		repository := &managerRepository{loadSnapshot: snapshot, loadVersion: version}
		manager := newManagerForTest(t, source, repository, &managerLocker{})
		got, status, err := manager.SnapshotLocked(context.Background())
		if err != nil || status.Updated || status.Stale || source.downloadCalls != 0 {
			t.Fatalf("unchanged sync = %+v, %+v, %v, downloads=%d", got, status, err, source.downloadCalls)
		}
	}
}

func TestManagerDownloadsAtMostOncePerCall(t *testing.T) {
	_, oldVersion := managerSnapshot(t, "old")
	newSnapshot, newVersion := managerSnapshot(t, "new")
	source := &managerSource{version: newVersion, data: validCatalogJSON()}
	repository := &managerRepository{loadSnapshot: newSnapshot, loadVersion: oldVersion, replaceSnapshot: newSnapshot}
	manager := newManagerForTest(t, source, repository, &managerLocker{})

	for call := 0; call < 2; call++ {
		if _, _, err := manager.SnapshotLocked(context.Background()); err != nil {
			t.Fatalf("call %d: SnapshotLocked() error = %v", call, err)
		}
	}
	if source.downloadCalls != 2 || source.versionCalls != 2 {
		t.Fatalf("calls = downloads %d, versions %d, want one each per call", source.downloadCalls, source.versionCalls)
	}
}

func TestManagerCancellationNeverUsesStale(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	t.Run("pre-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		source := &managerSource{version: localVersion, data: validCatalogJSON()}
		repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
		manager := newManagerForTest(t, source, repository, &managerLocker{})

		_, status, err := manager.SnapshotLocked(ctx)
		if !errors.Is(err, context.Canceled) || status.Stale || status.Cause != nil || source.versionCalls != 0 {
			t.Fatalf("pre-cancel = status %+v, error %v, version calls %d", status, err, source.versionCalls)
		}
	})
	t.Run("mid-download", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		source := &managerSource{version: domain.CatalogVersion{SHA256: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", Size: 1}, data: validCatalogJSON()}
		source.download = func(_ context.Context, _ string) error {
			cancel()
			return context.Canceled
		}
		repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
		manager := newManagerForTest(t, source, repository, &managerLocker{})

		_, status, err := manager.SnapshotLocked(ctx)
		if !errors.Is(err, context.Canceled) || status.Stale || status.Cause != nil || source.downloadCalls != 1 {
			t.Fatalf("mid-download = status %+v, error %v, download calls %d", status, err, source.downloadCalls)
		}
	})
}

func TestManagerLocalTemporaryFailureNeverUsesStale(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
	repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(source, repository, &managerLocker{}, root)
	if err != nil {
		t.Fatal(err)
	}

	_, status, err := manager.SnapshotLocked(context.Background())
	if err == nil || status != (SyncStatus{}) || source.downloadCalls != 0 {
		t.Fatalf("local temporary failure = status %+v, error %v, download calls %d", status, err, source.downloadCalls)
	}
}

func TestManagerCreatesTemporaryRootForFirstSync(t *testing.T) {
	remoteSnapshot, remoteVersion := managerSnapshot(t, "remote")
	root := filepath.Join(t.TempDir(), "catalog-tmp")
	source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
	repository := &managerRepository{loadErr: os.ErrNotExist, replaceSnapshot: remoteSnapshot}
	manager, err := NewManager(source, repository, &managerLocker{}, root)
	if err != nil {
		t.Fatal(err)
	}

	if _, status, err := manager.SnapshotLocked(context.Background()); err != nil || !status.Updated {
		t.Fatalf("SnapshotLocked() = status %+v, error %v", status, err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		t.Fatalf("temporary root = %v, %v, want directory", info, err)
	}
}

func TestManagerLocalReplacementFailureNeverUsesStale(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	repository := &managerRepository{
		loadSnapshot: localSnapshot,
		loadVersion:  localVersion,
		replaceErr:   fs.ErrPermission,
	}
	source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
	manager := newManagerForTest(t, source, repository, &managerLocker{})

	_, status, err := manager.SnapshotLocked(context.Background())
	if !errors.Is(err, fs.ErrPermission) || status != (SyncStatus{}) {
		t.Fatalf("local replacement failure = status %+v, error %v", status, err)
	}
}

func TestManagerLoadOnlyRepairsMissingOrInvalidCatalog(t *testing.T) {
	remoteSnapshot, remoteVersion := managerSnapshot(t, "remote")
	closeErr := errors.New("catalog parent close failed")
	for _, tt := range []struct {
		name             string
		loadErr          error
		wantVersionCalls int
		wantErr          error
	}{
		{name: "missing", loadErr: os.ErrNotExist, wantVersionCalls: 1},
		{name: "invalid bytes", loadErr: ErrInvalidCatalog, wantVersionCalls: 1},
		{name: "permission", loadErr: fs.ErrPermission, wantErr: fs.ErrPermission},
		{name: "trust race", loadErr: fs.ErrInvalid, wantErr: fs.ErrInvalid},
		{name: "invalid bytes plus close failure", loadErr: errors.Join(ErrInvalidCatalog, closeErr), wantErr: closeErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
			repository := &managerRepository{loadErr: tt.loadErr, replaceSnapshot: remoteSnapshot}
			manager := newManagerForTest(t, source, repository, &managerLocker{})

			_, _, err := manager.SnapshotLocked(context.Background())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SnapshotLocked() error = %v, want %v", err, tt.wantErr)
			}
			if source.versionCalls != tt.wantVersionCalls {
				t.Fatalf("CatalogVersion calls = %d, want %d", source.versionCalls, tt.wantVersionCalls)
			}
		})
	}
}

func TestManagerReplaceStaleOnlyForProviderCatalogBytes(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	for _, tt := range []struct {
		name       string
		replaceErr error
		wantStale  bool
	}{
		{name: "provider hash or schema", replaceErr: ErrCatalogSourceInvalid, wantStale: true},
		{name: "io", replaceErr: syscall.EIO},
		{name: "no space", replaceErr: syscall.ENOSPC},
		{name: "rename exists", replaceErr: os.ErrExist},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
			repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion, replaceErr: tt.replaceErr}
			manager := newManagerForTest(t, source, repository, &managerLocker{})

			_, status, err := manager.SnapshotLocked(context.Background())
			if tt.wantStale {
				if err != nil || !status.Stale || status.Cause != tt.replaceErr {
					t.Fatalf("provider replace failure = status %+v, error %v", status, err)
				}
				return
			}
			if !errors.Is(err, tt.replaceErr) || status != (SyncStatus{}) {
				t.Fatalf("local replace failure = status %+v, error %v", status, err)
			}
		})
	}
}

func TestManagerDownloadClassifiesOnlyExplicitDestinationFailureAsLocal(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	for _, tt := range []struct {
		name        string
		downloadErr error
		wantStale   bool
	}{
		{name: "provider wraps not exist", downloadErr: fmt.Errorf("transport: %w", os.ErrNotExist), wantStale: true},
		{name: "provider wraps invalid", downloadErr: fmt.Errorf("transport: %w", fs.ErrInvalid), wantStale: true},
		{name: "destination local", downloadErr: cloud.ErrCatalogDestination},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := &managerSource{version: remoteVersion, data: validCatalogJSON(), downloadErr: tt.downloadErr}
			repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
			manager := newManagerForTest(t, source, repository, &managerLocker{})

			_, status, err := manager.SnapshotLocked(context.Background())
			if tt.wantStale {
				if err != nil || !status.Stale || status.Cause != tt.downloadErr {
					t.Fatalf("provider download failure = status %+v, error %v", status, err)
				}
				return
			}
			if !errors.Is(err, tt.downloadErr) || status != (SyncStatus{}) {
				t.Fatalf("destination failure = status %+v, error %v", status, err)
			}
		})
	}
}

func TestManagerJoinsReleaseWithPrimaryFailure(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	providerErr := errors.New("version transport failed")
	releaseErr := errors.New("unlock failed")
	source := &managerSource{versionErr: providerErr}
	repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
	manager := newManagerForTest(t, source, repository, &managerLocker{releaseErr: releaseErr})

	snapshot, status, err := manager.Snapshot(context.Background())
	if !errors.Is(err, providerErr) || !errors.Is(err, releaseErr) || status != (SyncStatus{}) || len(snapshot.List("")) != 0 {
		t.Fatalf("release plus provider failure = snapshot %#v, status %+v, error %v", snapshot.List(""), status, err)
	}
}

func TestManagerJoinsCleanupWithPrimaryFailure(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	cleanupErr := errors.New("temporary cleanup failed")
	providerErr := errors.New("download transport failed")
	for _, tt := range []struct {
		name        string
		configure   func(context.CancelFunc, *managerSource, *managerRepository)
		wantPrimary error
	}{
		{
			name: "cancellation",
			configure: func(cancel context.CancelFunc, source *managerSource, _ *managerRepository) {
				source.download = func(context.Context, string) error {
					cancel()
					return context.Canceled
				}
			},
			wantPrimary: context.Canceled,
		},
		{
			name: "download",
			configure: func(_ context.CancelFunc, source *managerSource, _ *managerRepository) {
				source.downloadErr = providerErr
			},
			wantPrimary: providerErr,
		},
		{
			name: "replace",
			configure: func(_ context.CancelFunc, _ *managerSource, repository *managerRepository) {
				repository.replaceErr = ErrCatalogSourceInvalid
			},
			wantPrimary: ErrCatalogSourceInvalid,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
			repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
			tt.configure(cancel, source, repository)
			manager := newManagerImplementationForTest(t, source, repository, &managerLocker{})
			manager.ops.removeAll = func(string) error { return cleanupErr }

			snapshot, status, err := manager.SnapshotLocked(ctx)
			if !errors.Is(err, tt.wantPrimary) || !errors.Is(err, cleanupErr) || status != (SyncStatus{}) || len(snapshot.List("")) != 0 {
				t.Fatalf("cleanup plus %s failure = snapshot %#v, status %+v, error %v", tt.name, snapshot.List(""), status, err)
			}
		})
	}
}

func TestManagerCancellationAfterVersionTakesPrecedence(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &managerSource{version: localVersion}
	source.versionCall = func(context.Context) (domain.CatalogVersion, error) {
		cancel()
		return domain.CatalogVersion{SHA256: "invalid", Size: 1}, nil
	}
	repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
	manager := newManagerForTest(t, source, repository, &managerLocker{})

	_, status, err := manager.SnapshotLocked(ctx)
	if !errors.Is(err, context.Canceled) || status != (SyncStatus{}) || source.downloadCalls != 0 {
		t.Fatalf("mid-version cancellation = status %+v, error %v, downloads %d", status, err, source.downloadCalls)
	}
}

func TestManagerCancellationAfterReplaceTakesPrecedence(t *testing.T) {
	localSnapshot, localVersion := managerSnapshot(t, "cached")
	_, remoteVersion := managerSnapshot(t, "remote")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
	repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
	repository.replace = func(domain.CatalogVersion, string) (Snapshot, error) {
		cancel()
		return Snapshot{}, ErrCatalogSourceInvalid
	}
	manager := newManagerForTest(t, source, repository, &managerLocker{})

	_, status, err := manager.SnapshotLocked(ctx)
	if !errors.Is(err, context.Canceled) || status != (SyncStatus{}) {
		t.Fatalf("mid-replace cancellation = status %+v, error %v", status, err)
	}
}

func TestManagerSnapshotLockProtocol(t *testing.T) {
	remoteSnapshot, remoteVersion := managerSnapshot(t, "remote")
	localSnapshot, localVersion := managerSnapshot(t, "local")
	order := []string{}
	source := &managerSource{version: remoteVersion, data: validCatalogJSON(), order: &order}
	repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion, replaceSnapshot: remoteSnapshot, order: &order}
	locker := &managerLocker{order: &order}
	manager := newManagerForTest(t, source, repository, locker)

	if _, _, err := manager.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"lock", "load", "version", "download", "replace", "unlock"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("Snapshot() order = %v, want %v", order, want)
	}

	t.Run("contention does not call provider or fallback", func(t *testing.T) {
		contended := &managerLocker{acquireErr: lock.ErrLocked}
		source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
		manager := newManagerForTest(t, source, repository, contended)
		_, status, err := manager.Snapshot(context.Background())
		if !errors.Is(err, lock.ErrLocked) || status != (SyncStatus{}) || source.versionCalls != 0 || source.downloadCalls != 0 {
			t.Fatalf("contention = status %+v, error %v, source calls %d/%d", status, err, source.versionCalls, source.downloadCalls)
		}
	})

	t.Run("locked entry does not acquire", func(t *testing.T) {
		locker := &managerLocker{}
		source := &managerSource{version: localVersion, data: validCatalogJSON()}
		repository := &managerRepository{loadSnapshot: localSnapshot, loadVersion: localVersion}
		manager := newManagerForTest(t, source, repository, locker)
		if _, _, err := manager.SnapshotLocked(context.Background()); err != nil {
			t.Fatal(err)
		}
		if locker.acquireCalls != 0 {
			t.Fatalf("SnapshotLocked() acquire calls = %d, want 0", locker.acquireCalls)
		}
	})
}

func TestManagerCleansOwnedTemporaryDestination(t *testing.T) {
	remoteSnapshot, remoteVersion := managerSnapshot(t, "remote")
	root := t.TempDir()
	var destination string
	source := &managerSource{version: remoteVersion, data: validCatalogJSON()}
	source.download = func(_ context.Context, path string) error {
		destination = path
		if !filepath.IsAbs(path) {
			return fmt.Errorf("destination %q is not absolute", path)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("destination exists before provider writes: %w", err)
		}
		return os.WriteFile(path, validCatalogJSON(), 0o600)
	}
	repository := &managerRepository{loadErr: os.ErrNotExist, replaceSnapshot: remoteSnapshot}
	manager, err := NewManager(source, repository, &managerLocker{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = manager.SnapshotLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if destination == "" {
		t.Fatal("DownloadCatalog was not called")
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
		t.Fatalf("temporary root entries = %v, %v, want empty", entries, err)
	}
}

func TestNewManagerRejectsInvalidDependenciesAndTemporaryRoot(t *testing.T) {
	snapshot, version := managerSnapshot(t, "cached")
	source := &managerSource{version: version, data: validCatalogJSON()}
	repository := &managerRepository{loadSnapshot: snapshot, loadVersion: version}
	for _, tt := range []struct {
		name       string
		source     cloud.CatalogSource
		repository Repository
		locker     lock.Locker
		tempRoot   string
	}{
		{name: "nil source", repository: repository, locker: &managerLocker{}, tempRoot: t.TempDir()},
		{name: "nil repository", source: source, locker: &managerLocker{}, tempRoot: t.TempDir()},
		{name: "nil locker", source: source, repository: repository, tempRoot: t.TempDir()},
		{name: "relative root", source: source, repository: repository, locker: &managerLocker{}, tempRoot: "relative"},
		{name: "unclean root", source: source, repository: repository, locker: &managerLocker{}, tempRoot: t.TempDir() + string(filepath.Separator) + "."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewManager(tt.source, tt.repository, tt.locker, tt.tempRoot); err == nil {
				t.Fatal("NewManager() error = nil, want validation failure")
			}
		})
	}
}

type managerSource struct {
	version       domain.CatalogVersion
	versionErr    error
	versionCall   func(context.Context) (domain.CatalogVersion, error)
	downloadErr   error
	data          []byte
	download      func(context.Context, string) error
	versionCalls  int
	downloadCalls int
	order         *[]string
}

func (source *managerSource) CatalogVersion(ctx context.Context) (domain.CatalogVersion, error) {
	source.versionCalls++
	source.appendOrder("version")
	if source.versionCall != nil {
		return source.versionCall(ctx)
	}
	return source.version, source.versionErr
}

func (source *managerSource) DownloadCatalog(ctx context.Context, destination string) error {
	source.downloadCalls++
	source.appendOrder("download")
	if source.download != nil {
		return source.download(ctx, destination)
	}
	if source.downloadErr != nil {
		return source.downloadErr
	}
	return os.WriteFile(destination, source.data, 0o600)
}

func (source *managerSource) appendOrder(value string) {
	if source.order != nil {
		*source.order = append(*source.order, value)
	}
}

type managerRepository struct {
	loadSnapshot    Snapshot
	loadVersion     domain.CatalogVersion
	loadErr         error
	replaceSnapshot Snapshot
	replaceErr      error
	replace         func(domain.CatalogVersion, string) (Snapshot, error)
	loadCalls       int
	replaceCalls    int
	order           *[]string
}

func (repository *managerRepository) Load() (Snapshot, domain.CatalogVersion, error) {
	repository.loadCalls++
	repository.appendOrder("load")
	return repository.loadSnapshot, repository.loadVersion, repository.loadErr
}

func (repository *managerRepository) Replace(version domain.CatalogVersion, sourcePath string) (Snapshot, error) {
	repository.replaceCalls++
	repository.appendOrder("replace")
	if _, err := os.Stat(sourcePath); err != nil {
		return Snapshot{}, fmt.Errorf("staged source is unavailable: %w", err)
	}
	if repository.replace != nil {
		return repository.replace(version, sourcePath)
	}
	return repository.replaceSnapshot, repository.replaceErr
}

func (repository *managerRepository) appendOrder(value string) {
	if repository.order != nil {
		*repository.order = append(*repository.order, value)
	}
}

type managerLocker struct {
	acquireErr   error
	releaseErr   error
	acquireCalls int
	order        *[]string
}

func (locker *managerLocker) Acquire() (lock.Unlock, error) {
	locker.acquireCalls++
	if locker.order != nil {
		*locker.order = append(*locker.order, "lock")
	}
	if locker.acquireErr != nil {
		return nil, locker.acquireErr
	}
	return managerUnlock{locker: locker}, nil
}

type managerUnlock struct {
	locker *managerLocker
}

func (unlock managerUnlock) Release() error {
	if unlock.locker.order != nil {
		*unlock.locker.order = append(*unlock.locker.order, "unlock")
	}
	return unlock.locker.releaseErr
}

func managerSnapshot(t *testing.T, message string) (Snapshot, domain.CatalogVersion) {
	t.Helper()
	entries := catalogEntriesFixture()
	entries[0].Message = message
	data, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, managerVersion(data)
}

func managerVersion(data []byte) domain.CatalogVersion {
	digest := sha256.Sum256(data)
	return domain.CatalogVersion{SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data))}
}

func newManagerForTest(t *testing.T, source cloud.CatalogSource, repository Repository, locker lock.Locker) Manager {
	t.Helper()
	manager, err := NewManager(source, repository, locker, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func newManagerImplementationForTest(t *testing.T, source cloud.CatalogSource, repository Repository, locker lock.Locker) *manager {
	t.Helper()
	return newManagerForTest(t, source, repository, locker).(*manager)
}

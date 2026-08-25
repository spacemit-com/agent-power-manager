//go:build linux || darwin || freebsd

package catalog

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
	"apm/internal/lock"
)

func TestManagerClassifiesOversizedStagedProviderCatalog(t *testing.T) {
	newData := repositoryCatalogVariant(t, "remote message")
	remoteVersion := repositoryVersion(newData)
	for _, tt := range []struct {
		name      string
		withCache bool
		wantStale bool
		wantErr   error
	}{
		{name: "valid cache becomes stale", withCache: true, wantStale: true},
		{name: "missing cache is unavailable", wantErr: ErrCatalogUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := filepath.Join(root, "catalog.json")
			if tt.withCache {
				writeRepositoryFile(t, root, "catalog.json", validCatalogJSON(), 0o600)
			}
			repository := newTestRepository(t, path)
			source := &repositoryManagerSource{version: remoteVersion, write: writeOversizedStagedCatalog}
			manager := newRealManager(t, source, repository, filepath.Join(root, ".tmp"))

			snapshot, status, err := manager.SnapshotLocked(context.Background())
			if tt.wantStale {
				if err != nil || !status.Stale || !errors.Is(status.Cause, ErrCatalogSourceInvalid) || !errors.Is(status.Cause, ErrInvalidCatalog) || len(snapshot.List("")) == 0 {
					t.Fatalf("oversized staged catalog = snapshot %#v, status %+v, error %v", snapshot.List(""), status, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) || !errors.Is(err, ErrCatalogSourceInvalid) || !errors.Is(err, ErrInvalidCatalog) || status != (SyncStatus{}) {
				t.Fatalf("oversized staged catalog = status %+v, error %v", status, err)
			}
		})
	}
}

func TestManagerDirectFailsForRepositoryLocalStagedFailures(t *testing.T) {
	newData := repositoryCatalogVariant(t, "remote message")
	remoteVersion := repositoryVersion(newData)
	readErr := errors.New("injected staged read failure")
	closeErr := errors.New("injected staged close failure")
	for _, tt := range []struct {
		name  string
		write func(*unixRepositoryPlatform, string) error
		want  error
	}{
		{
			name: "read",
			write: func(platform *unixRepositoryPlatform, destination string) error {
				platform.ops.readBounded = func(io.Reader, int64) ([]byte, error) { return nil, readErr }
				return writeStagedCatalog(destination, newData, 0o600)
			},
			want: readErr,
		},
		{
			name: "permission",
			write: func(_ *unixRepositoryPlatform, destination string) error {
				return writeStagedCatalog(destination, newData, 0o644)
			},
			want: os.ErrPermission,
		},
		{
			name: "close",
			write: func(platform *unixRepositoryPlatform, destination string) error {
				platform.ops.close = func(file *os.File) error {
					_ = file.Close()
					return closeErr
				}
				return writeStagedCatalog(destination, newData, 0o600)
			},
			want: closeErr,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := trustedRepositoryDir(t)
			path := writeRepositoryFile(t, root, "catalog.json", validCatalogJSON(), 0o600)
			repository := newTestRepository(t, path).(*filesystemRepository)
			platform := repository.platform.(*unixRepositoryPlatform)
			source := &repositoryManagerSource{version: remoteVersion}
			source.write = func(destination string) error { return tt.write(platform, destination) }
			manager := newRealManager(t, source, repository, filepath.Join(root, ".tmp"))

			_, status, err := manager.SnapshotLocked(context.Background())
			if !errors.Is(err, tt.want) || errors.Is(err, ErrCatalogSourceInvalid) || status != (SyncStatus{}) {
				t.Fatalf("local %s failure = status %+v, error %v", tt.name, status, err)
			}
		})
	}
}

type repositoryManagerSource struct {
	version domain.CatalogVersion
	write   func(string) error
}

func (source *repositoryManagerSource) CatalogVersion(context.Context) (domain.CatalogVersion, error) {
	return source.version, nil
}

func (source *repositoryManagerSource) DownloadCatalog(_ context.Context, destination string) error {
	return source.write(destination)
}

func newRealManager(t *testing.T, source *repositoryManagerSource, repository Repository, tempRoot string) Manager {
	t.Helper()
	manager, err := NewManager(source, repository, lock.New(filepath.Join(tempRoot, "manager.lock")), tempRoot)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func writeOversizedStagedCatalog(destination string) error {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := file.Truncate(MaxCatalogSize + 1); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeStagedCatalog(destination string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(destination, data, mode); err != nil {
		return err
	}
	return os.Chmod(destination, mode)
}

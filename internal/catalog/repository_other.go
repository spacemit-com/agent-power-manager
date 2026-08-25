//go:build !linux && !darwin && !freebsd

package catalog

import (
	"fmt"
	"runtime"

	"apm/internal/domain"
)

type unsupportedRepositoryPlatform struct{}

func newRepositoryPlatform() repositoryPlatform {
	return unsupportedRepositoryPlatform{}
}

func (unsupportedRepositoryPlatform) load(path string) (Snapshot, domain.CatalogVersion, error) {
	return Snapshot{}, domain.CatalogVersion{}, fmt.Errorf("descriptor-relative catalog loading is unsupported on %s for %q", runtime.GOOS, path)
}

func (unsupportedRepositoryPlatform) replace(path string, _ domain.CatalogVersion, _ string) (Snapshot, error) {
	return Snapshot{}, fmt.Errorf("descriptor-relative catalog replacement is unsupported on %s for %q", runtime.GOOS, path)
}

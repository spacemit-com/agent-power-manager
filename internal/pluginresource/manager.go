package pluginresource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	"apm/internal/domain"
	"apm/internal/pluginartifact"
	"apm/internal/utils/filesystem"
)

// Service 管理共享且不可变的 Plugin artifact。
type Service struct {
	cacheRoot       string
	cacheTempRoot   string
	stagingRoot     string
	stagingTempRoot string
	downloader      Downloader
	builder         artifactBuilder
	locksMu         sync.Mutex
	pathLocks       map[string]*sync.Mutex
	files           serviceFiles
}

type serviceFiles struct {
	copyDir   func(string, string) error
	lstat     func(string) (os.FileInfo, error)
	mkdirAll  func(string, os.FileMode) error
	mkdirTemp func(string, string) (string, error)
	rename    func(string, string) error
	removeAll func(string) error
}

var _ ArtifactLocator = (*Service)(nil)

// artifactBuilder 在下载后的同一个 artifact 根目录中构建并复验共享派生内容。
type artifactBuilder interface {
	Build(domain.PowerRef, string, string) error
	Validate(domain.PowerRef, string) error
}

func newService(
	cacheRoot, cacheTempRoot, stagingRoot, stagingTempRoot string,
	downloader Downloader,
	builder artifactBuilder,
) *Service {
	return &Service{
		cacheRoot:       cacheRoot,
		cacheTempRoot:   cacheTempRoot,
		stagingRoot:     stagingRoot,
		stagingTempRoot: stagingTempRoot,
		downloader:      downloader,
		builder:         builder,
		pathLocks:       make(map[string]*sync.Mutex),
		files: serviceFiles{
			copyDir:   filesystem.CopyDir,
			lstat:     os.Lstat,
			mkdirAll:  os.MkdirAll,
			mkdirTemp: os.MkdirTemp,
			rename:    os.Rename,
			removeAll: os.RemoveAll,
		},
	}
}

// Ensure 下载并原子发布尚未缓存的 artifact；已存在版本只复验，绝不替换。
func (service *Service) Ensure(ctx context.Context, entry domain.CatalogEntry) (artifact Artifact, resultErr error) {
	if err := entry.Validate(); err != nil {
		return Artifact{}, fmt.Errorf("validate Plugin catalog entry: %w", errors.Join(ErrInvalidArtifact, err))
	}
	ref := entry.Ref
	if ref.Type != domain.PowerTypePlugin {
		return Artifact{}, fmt.Errorf("Plugin catalog entry has type %q: %w", ref.Type, ErrInvalidArtifact)
	}
	artifact, err := service.Lookup(ref)
	if err == nil {
		return artifact, nil
	}
	if !errors.Is(err, ErrMiss) {
		return Artifact{}, err
	}

	artifact, err = service.artifactFor(ref)
	if err != nil {
		return Artifact{}, err
	}
	publishLock := service.pathLock(artifact.Root)
	publishLock.Lock()
	defer publishLock.Unlock()

	// 等待中的发布者必须重新查询，避免同一版本重复下载或覆盖。
	artifact, err = service.Lookup(ref)
	if err == nil {
		return artifact, nil
	}
	if !errors.Is(err, ErrMiss) {
		return Artifact{}, err
	}
	if isNilDependency(service.builder) {
		return Artifact{}, fmt.Errorf("plugin artifact builder: %w", ErrInvalidArtifact)
	}
	artifact, err = service.artifactFor(ref)
	if err != nil {
		return Artifact{}, err
	}

	temporaryParent := filepath.Join(service.cacheTempRoot, "plugins")
	if err := service.files.mkdirAll(temporaryParent, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary parent %q: %w", temporaryParent, err)
	}
	temporaryRoot, err := service.files.mkdirTemp(temporaryParent, "")
	if err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary root: %w", err)
	}
	// 临时目录唯一且仅由本次调用创建，清理绝不触及最终不可变目录。
	defer func() {
		if temporaryRoot != "" {
			if err := service.files.removeAll(temporaryRoot); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("cleanup plugin artifact temporary root %q: %w", temporaryRoot, err))
			}
		}
	}()

	temporaryPluginPath := filepath.Join(temporaryRoot, "plugins", ref.Name)
	if err := service.files.mkdirAll(temporaryPluginPath, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary path %q: %w", temporaryPluginPath, err)
	}
	downloader, err := service.downloaderFor()
	if err != nil {
		return Artifact{}, err
	}
	if err := downloader.DownloadPlugin(ctx, entry, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("download plugin artifact: %w", err)
	}
	// 共享 builder 读取 manifest 或写 marketplace 前，必须先拒绝下载树中的 symlink。
	if err := validatePhysicalTree(temporaryRoot); err != nil {
		return Artifact{}, err
	}
	// Cloud raw package 在共享 builder 读取 manifest 前统一拒绝 marketplace。
	if err := pluginartifact.ValidateRawPackage(ref, temporaryPluginPath); err != nil {
		return Artifact{}, errors.Join(ErrInvalidArtifact, err)
	}
	if err := service.builder.Build(ref, temporaryRoot, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("build plugin artifact: %w", err)
	}
	// 构建后再次复验，覆盖 APM 新生成的 marketplace 目录和文件。
	if err := validatePhysicalTree(temporaryRoot); err != nil {
		return Artifact{}, err
	}
	if err := service.builder.Validate(ref, temporaryRoot); err != nil {
		return Artifact{}, err
	}

	if err := service.files.mkdirAll(filepath.Dir(artifact.Root), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact parent %q: %w", filepath.Dir(artifact.Root), err)
	}
	if _, err := service.files.lstat(artifact.Root); err == nil {
		return service.Lookup(ref)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Artifact{}, fmt.Errorf("inspect plugin artifact root %q: %w", artifact.Root, err)
	}
	if err := service.files.rename(temporaryRoot, artifact.Root); err != nil {
		// 另一个发布者可能已在检查后完成；只复验其最终目录，绝不覆盖。
		if _, inspectErr := service.files.lstat(artifact.Root); inspectErr == nil {
			existing, lookupErr := service.Lookup(ref)
			if lookupErr == nil {
				return existing, nil
			}
			return Artifact{}, errors.Join(fmt.Errorf("publish plugin artifact %q: %w", artifact.Root, err), lookupErr)
		} else if !errors.Is(inspectErr, os.ErrNotExist) {
			return Artifact{}, errors.Join(
				fmt.Errorf("publish plugin artifact %q: %w", artifact.Root, err),
				fmt.Errorf("inspect published plugin artifact %q: %w", artifact.Root, inspectErr),
			)
		}
		return Artifact{}, fmt.Errorf("publish plugin artifact %q: %w", artifact.Root, err)
	}
	temporaryRoot = ""
	return artifact, nil
}

// Lookup 仅查询并复验已有 artifact，不会触发下载或修复。
func (service *Service) Lookup(ref domain.PowerRef) (Artifact, error) {
	if isNilDependency(service.builder) {
		return Artifact{}, fmt.Errorf("plugin artifact builder: %w", ErrInvalidArtifact)
	}
	artifact, err := service.artifactFor(ref)
	if err != nil {
		return Artifact{}, err
	}
	if err := validatePhysicalAncestors(service.cacheRoot, artifact.Root); err != nil {
		return Artifact{}, err
	}
	if _, err := service.files.lstat(artifact.Root); errors.Is(err, os.ErrNotExist) {
		return Artifact{}, fmt.Errorf("plugin artifact %q: %w", artifact.Root, ErrMiss)
	} else if err != nil {
		return Artifact{}, fmt.Errorf("inspect plugin artifact root %q: %w", artifact.Root, err)
	}
	if err := validatePhysicalTree(artifact.Root); err != nil {
		return Artifact{}, err
	}
	if err := service.builder.Validate(ref, artifact.Root); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// Locate 只构造并返回精确 Ref 的 artifact 身份与预期根，不读取缓存、不复验制品内容。
func (service *Service) Locate(ref domain.PowerRef) (Artifact, error) {
	return service.artifactFor(ref)
}

// Exists 只报告预期根是否为物理目录：缺失返回 false，symlink、非目录或不可读返回错误，绝不校验制品内容。
func (service *Service) Exists(ref domain.PowerRef) (bool, error) {
	artifact, err := service.artifactFor(ref)
	if err != nil {
		return false, err
	}
	info, err := service.files.lstat(artifact.Root)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect plugin artifact root %q: %w", artifact.Root, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("plugin artifact root %q is not a physical directory: %w", artifact.Root, ErrInvalidArtifact)
	}
	return true, nil
}

// StageFilesystem 将共享 Plugin 叶子原子物化为 filesystem Agent 共用的无版本 staging 目录。
func (service *Service) StageFilesystem(ref domain.PowerRef) (staging string, resultErr error) {
	artifact, err := service.Lookup(ref)
	if err != nil {
		return "", err
	}

	artifactLock := service.pathLock(artifact.Root)
	artifactLock.Lock()
	artifact, err = service.Lookup(ref)
	artifactLock.Unlock()
	if err != nil {
		return "", err
	}
	stageLock := service.pathLock(service.stagingRoot)
	stageLock.Lock()
	defer stageLock.Unlock()
	if err := validateStagingDisjoint(
		service.cacheRoot,
		artifact,
		service.stagingRoot,
		service.stagingTempRoot,
	); err != nil {
		return "", err
	}

	// 配置根由 lifecycle 全局锁协作保护；这里仅清理明确注入的 staging 临时根。
	if err := service.files.removeAll(service.stagingTempRoot); err != nil {
		return "", fmt.Errorf("remove filesystem staging temporary root %q: %w", service.stagingTempRoot, err)
	}
	if err := service.files.mkdirAll(filepath.Dir(service.stagingTempRoot), 0o700); err != nil {
		return "", fmt.Errorf("create filesystem staging temporary parent %q: %w", filepath.Dir(service.stagingTempRoot), err)
	}
	temporaryRoot, err := service.files.mkdirTemp(filepath.Dir(service.stagingTempRoot), filepath.Base(service.stagingTempRoot)+"-")
	if err != nil {
		return "", fmt.Errorf("create filesystem staging temporary root: %w", err)
	}
	defer func() {
		if temporaryRoot != "" {
			if err := service.files.removeAll(temporaryRoot); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("cleanup filesystem staging temporary root %q: %w", temporaryRoot, err))
			}
		}
	}()

	target := filepath.Join(temporaryRoot, "plugins", ref.Name)
	if err := service.files.copyDir(artifact.PluginPath, target); err != nil {
		return "", fmt.Errorf("copy filesystem plugin artifact to staging: %w", err)
	}
	if err := service.files.mkdirAll(filepath.Dir(service.stagingRoot), 0o700); err != nil {
		return "", fmt.Errorf("create filesystem staging parent %q: %w", filepath.Dir(service.stagingRoot), err)
	}

	// 旧 staging 先移动到唯一 sibling backup；新根发布失败时必须恢复，绝不直接递归删除旧完整树。
	backupRoot := ""
	if _, err := service.files.lstat(service.stagingRoot); err == nil {
		backupRoot, err = service.files.mkdirTemp(
			filepath.Dir(service.stagingRoot),
			filepath.Base(service.stagingRoot)+"-backup-",
		)
		if err != nil {
			return "", fmt.Errorf("reserve filesystem staging backup: %w", err)
		}
		if err := service.files.removeAll(backupRoot); err != nil {
			return "", fmt.Errorf("remove filesystem staging backup reservation %q: %w", backupRoot, err)
		}
		if err := service.files.rename(service.stagingRoot, backupRoot); err != nil {
			return "", fmt.Errorf("quarantine filesystem staging %q to %q: %w", service.stagingRoot, backupRoot, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect filesystem staging root %q: %w", service.stagingRoot, err)
	}
	if err := service.files.rename(temporaryRoot, service.stagingRoot); err != nil {
		publishErr := fmt.Errorf("publish filesystem staging %q: %w", service.stagingRoot, err)
		if backupRoot == "" {
			return "", publishErr
		}
		if restoreErr := service.files.rename(backupRoot, service.stagingRoot); restoreErr != nil {
			return "", errors.Join(
				publishErr,
				fmt.Errorf("restore filesystem staging backup %q: %w", backupRoot, restoreErr),
			)
		}
		return "", publishErr
	}
	temporaryRoot = ""
	if backupRoot != "" {
		if err := service.files.removeAll(backupRoot); err != nil {
			return "", fmt.Errorf("remove published filesystem staging backup %q: %w", backupRoot, err)
		}
	}
	return service.stagingRoot, nil
}

func (service *Service) downloaderFor() (Downloader, error) {
	if isNilDependency(service.downloader) {
		return nil, fmt.Errorf("plugin artifact downloader: %w", ErrInvalidArtifact)
	}
	return service.downloader, nil
}

func isNilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (service *Service) pathLock(key string) *sync.Mutex {
	service.locksMu.Lock()
	defer service.locksMu.Unlock()
	lock := service.pathLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		service.pathLocks[key] = lock
	}
	return lock
}

func (service *Service) artifactFor(ref domain.PowerRef) (Artifact, error) {
	logicalName, err := domain.PluginRuntimeName(ref)
	if err != nil {
		return Artifact{}, fmt.Errorf("plugin artifact reference %+v: %w", ref, errors.Join(ErrInvalidArtifact, err))
	}
	root := filepath.Join(service.cacheRoot, "plugins", ref.Name, ref.Version)
	marketplaceSource, err := filepath.Abs(root)
	if err != nil {
		return Artifact{}, fmt.Errorf("resolve plugin artifact marketplace source %q: %w", root, err)
	}
	root = filepath.Clean(marketplaceSource)
	artifact := Artifact{
		Power:                   ref,
		Root:                    root,
		PluginPath:              filepath.Join(root, "plugins", logicalName),
		CodexMarketplaceSource:  root,
		ClaudeMarketplaceSource: root,
	}
	if err := artifact.Validate(); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

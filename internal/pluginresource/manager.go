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

// Service 管理按 Agent 隔离且不可变的 Plugin artifact。
type Service struct {
	cacheRoot       string
	cacheTempRoot   string
	stagingRoot     string
	stagingTempRoot string
	downloader      Downloader
	builders        map[domain.AgentID]artifactBuilder
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

// artifactBuilder 在下载后的 artifact 根目录中构建并复验 Agent 专属内容。
type artifactBuilder interface {
	Build(domain.AgentID, domain.PowerRef, string, string) error
	Validate(domain.AgentID, domain.PowerRef, string) error
}

func newService(
	cacheRoot, cacheTempRoot, stagingRoot, stagingTempRoot string,
	downloader Downloader,
	builders map[domain.AgentID]artifactBuilder,
) *Service {
	return &Service{
		cacheRoot:       cacheRoot,
		cacheTempRoot:   cacheTempRoot,
		stagingRoot:     stagingRoot,
		stagingTempRoot: stagingTempRoot,
		downloader:      downloader,
		builders:        builders,
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
func (service *Service) Ensure(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (artifact Artifact, resultErr error) {
	artifact, err := service.Lookup(agent, ref)
	if err == nil {
		return artifact, nil
	}
	if !errors.Is(err, ErrMiss) {
		return Artifact{}, err
	}

	artifact, err = service.artifactFor(agent, ref)
	if err != nil {
		return Artifact{}, err
	}
	publishLock := service.pathLock("artifact:" + artifact.Root)
	publishLock.Lock()
	defer publishLock.Unlock()

	// 等待中的发布者必须重新查询，避免同一版本重复下载或覆盖。
	artifact, err = service.Lookup(agent, ref)
	if err == nil {
		return artifact, nil
	}
	if !errors.Is(err, ErrMiss) {
		return Artifact{}, err
	}
	builder, err := service.builderFor(agent)
	if err != nil {
		return Artifact{}, err
	}
	artifact, err = service.artifactFor(agent, ref)
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

	temporaryPluginPath := filepath.Join(temporaryRoot, "plugins", artifact.ActualName)
	if err := service.files.mkdirAll(temporaryPluginPath, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary path %q: %w", temporaryPluginPath, err)
	}
	downloader, err := service.downloaderFor()
	if err != nil {
		return Artifact{}, err
	}
	if err := downloader.DownloadPlugin(ctx, ref, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("download plugin artifact: %w", err)
	}
	// Agent builder 读取 manifest 或写 marketplace 前，必须先拒绝下载树中的 symlink。
	if err := validatePhysicalTree(temporaryRoot); err != nil {
		return Artifact{}, err
	}
	// Cloud raw package 在任何 Agent builder 读取 manifest 前统一拒绝 marketplace。
	if err := pluginartifact.ValidateRawPackage(temporaryPluginPath); err != nil {
		return Artifact{}, errors.Join(ErrInvalidArtifact, err)
	}
	if err := builder.Build(agent, ref, temporaryRoot, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("build plugin artifact: %w", err)
	}
	// 构建后再次复验，覆盖 APM 新生成的 marketplace 目录和文件。
	if err := validatePhysicalTree(temporaryRoot); err != nil {
		return Artifact{}, err
	}
	if err := builder.Validate(agent, ref, temporaryRoot); err != nil {
		return Artifact{}, err
	}

	if err := service.files.mkdirAll(filepath.Dir(artifact.Root), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact parent %q: %w", filepath.Dir(artifact.Root), err)
	}
	if _, err := service.files.lstat(artifact.Root); err == nil {
		return service.Lookup(agent, ref)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Artifact{}, fmt.Errorf("inspect plugin artifact root %q: %w", artifact.Root, err)
	}
	if err := service.files.rename(temporaryRoot, artifact.Root); err != nil {
		// 另一个发布者可能已在检查后完成；只复验其最终目录，绝不覆盖。
		if _, inspectErr := service.files.lstat(artifact.Root); inspectErr == nil {
			existing, lookupErr := service.Lookup(agent, ref)
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
func (service *Service) Lookup(agent domain.AgentID, ref domain.PowerRef) (Artifact, error) {
	builder, err := service.builderFor(agent)
	if err != nil {
		return Artifact{}, err
	}
	artifact, err := service.artifactFor(agent, ref)
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
	if err := builder.Validate(agent, ref, artifact.Root); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// StageHermes 将 Hermes artifact 物化为无版本 staging 目录。
func (service *Service) StageHermes(ref domain.PowerRef) (staging string, resultErr error) {
	artifact, err := service.Lookup(domain.AgentHermes, ref)
	if err != nil {
		return "", err
	}

	artifactLock := service.pathLock("artifact:" + artifact.Root)
	artifactLock.Lock()
	artifact, err = service.Lookup(domain.AgentHermes, ref)
	artifactLock.Unlock()
	if err != nil {
		return "", err
	}
	stageLock := service.pathLock("staging:" + service.stagingRoot)
	stageLock.Lock()
	defer stageLock.Unlock()

	// 配置根由 lifecycle 全局锁协作保护；这里仅清理明确注入的 staging 临时根。
	if err := service.files.removeAll(service.stagingTempRoot); err != nil {
		return "", fmt.Errorf("remove Hermes staging temporary root %q: %w", service.stagingTempRoot, err)
	}
	if err := service.files.mkdirAll(filepath.Dir(service.stagingTempRoot), 0o700); err != nil {
		return "", fmt.Errorf("create Hermes staging temporary parent %q: %w", filepath.Dir(service.stagingTempRoot), err)
	}
	temporaryRoot, err := service.files.mkdirTemp(filepath.Dir(service.stagingTempRoot), filepath.Base(service.stagingTempRoot)+"-")
	if err != nil {
		return "", fmt.Errorf("create Hermes staging temporary root: %w", err)
	}
	defer func() {
		if temporaryRoot != "" {
			if err := service.files.removeAll(temporaryRoot); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("cleanup Hermes staging temporary root %q: %w", temporaryRoot, err))
			}
		}
	}()

	target := filepath.Join(temporaryRoot, "plugins", ref.Name)
	if err := service.files.copyDir(artifact.PluginPath, target); err != nil {
		return "", fmt.Errorf("copy Hermes plugin artifact to staging: %w", err)
	}
	if err := service.files.mkdirAll(filepath.Dir(service.stagingRoot), 0o700); err != nil {
		return "", fmt.Errorf("create Hermes staging parent %q: %w", filepath.Dir(service.stagingRoot), err)
	}
	// 成功构建前保留旧 staging，避免复制失败时暴露半成品或删除完整版本。
	if err := service.files.removeAll(service.stagingRoot); err != nil {
		return "", fmt.Errorf("remove Hermes staging root %q: %w", service.stagingRoot, err)
	}
	if err := service.files.rename(temporaryRoot, service.stagingRoot); err != nil {
		return "", fmt.Errorf("publish Hermes staging %q: %w", service.stagingRoot, err)
	}
	temporaryRoot = ""
	return service.stagingRoot, nil
}

func (service *Service) builderFor(agent domain.AgentID) (artifactBuilder, error) {
	switch agent {
	case domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes:
	default:
		return nil, fmt.Errorf("plugin artifact agent %q: %w", agent, ErrUnsupportedAgent)
	}
	builder, ok := service.builders[agent]
	if !ok || isNilDependency(builder) {
		return nil, fmt.Errorf("plugin artifact agent %q: %w", agent, ErrUnsupportedAgent)
	}
	return builder, nil
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

func (service *Service) artifactFor(agent domain.AgentID, ref domain.PowerRef) (Artifact, error) {
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return Artifact{}, fmt.Errorf("plugin artifact reference %+v: %w", ref, err)
	}
	root := filepath.Join(service.cacheRoot, "plugins", string(agent), ref.Name, ref.Version)
	artifact := Artifact{
		Agent:      agent,
		Power:      ref,
		ActualName: actualName,
		Root:       root,
		PluginPath: filepath.Join(root, "plugins", actualName),
	}
	if agent == domain.AgentCodex || agent == domain.AgentClaudeCode {
		marketplaceName, err := domain.PluginMarketplaceName(ref)
		if err != nil {
			return Artifact{}, fmt.Errorf("plugin artifact marketplace: %w", err)
		}
		marketplaceSource, err := filepath.Abs(root)
		if err != nil {
			return Artifact{}, fmt.Errorf("resolve plugin artifact marketplace source %q: %w", root, err)
		}
		artifact.MarketplaceName = marketplaceName
		artifact.MarketplaceSource = filepath.Clean(marketplaceSource)
	}
	return artifact, nil
}

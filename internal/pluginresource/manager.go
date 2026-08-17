package pluginresource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"apm/internal/domain"
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
	artifactLocks   map[string]*sync.Mutex
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
		artifactLocks:   make(map[string]*sync.Mutex),
	}
}

// Ensure 下载并原子发布尚未缓存的 artifact；已存在版本只复验，绝不替换。
func (service *Service) Ensure(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (Artifact, error) {
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
	publishLock := service.artifactLock(artifact.Root)
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
	if err := os.MkdirAll(temporaryParent, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary parent %q: %w", temporaryParent, err)
	}
	temporaryRoot, err := os.MkdirTemp(temporaryParent, "")
	if err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary root: %w", err)
	}
	// 临时目录唯一且仅由本次调用创建，清理绝不触及最终不可变目录。
	defer os.RemoveAll(temporaryRoot)

	temporaryPluginPath := filepath.Join(temporaryRoot, "plugins", artifact.ActualName)
	if err := os.MkdirAll(temporaryPluginPath, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact temporary path %q: %w", temporaryPluginPath, err)
	}
	if err := service.downloader.DownloadPlugin(ctx, agent, ref, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("download plugin artifact: %w", err)
	}
	if err := builder.Build(agent, ref, temporaryRoot, temporaryPluginPath); err != nil {
		return Artifact{}, fmt.Errorf("build plugin artifact: %w", err)
	}
	if err := validatePhysicalTree(temporaryRoot); err != nil {
		return Artifact{}, err
	}
	if err := builder.Validate(agent, ref, temporaryRoot); err != nil {
		return Artifact{}, err
	}

	if err := os.MkdirAll(filepath.Dir(artifact.Root), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create plugin artifact parent %q: %w", filepath.Dir(artifact.Root), err)
	}
	if _, err := os.Lstat(artifact.Root); err == nil {
		return service.Lookup(agent, ref)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Artifact{}, fmt.Errorf("inspect plugin artifact root %q: %w", artifact.Root, err)
	}
	if err := os.Rename(temporaryRoot, artifact.Root); err != nil {
		// 另一个发布者可能已在检查后完成；只复验其最终目录，绝不覆盖。
		if _, inspectErr := os.Lstat(artifact.Root); inspectErr == nil {
			return service.Lookup(agent, ref)
		}
		return Artifact{}, fmt.Errorf("publish plugin artifact %q: %w", artifact.Root, err)
	}
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
	if _, err := os.Lstat(artifact.Root); errors.Is(err, os.ErrNotExist) {
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
func (service *Service) StageHermes(ref domain.PowerRef) (string, error) {
	artifact, err := service.Lookup(domain.AgentHermes, ref)
	if err != nil {
		return "", err
	}

	// 只重建注入的 staging 根及其临时根，不能清理缓存或其他 Agent artifact。
	if err := os.RemoveAll(service.stagingRoot); err != nil {
		return "", fmt.Errorf("remove Hermes staging root %q: %w", service.stagingRoot, err)
	}
	if err := os.RemoveAll(service.stagingTempRoot); err != nil {
		return "", fmt.Errorf("remove Hermes staging temporary root %q: %w", service.stagingTempRoot, err)
	}

	target := filepath.Join(service.stagingTempRoot, "plugins", ref.Name)
	if err := filesystem.CopyDir(artifact.PluginPath, target); err != nil {
		return "", fmt.Errorf("copy Hermes plugin artifact to staging: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(service.stagingRoot), 0o700); err != nil {
		return "", fmt.Errorf("create Hermes staging parent %q: %w", filepath.Dir(service.stagingRoot), err)
	}
	if err := os.Rename(service.stagingTempRoot, service.stagingRoot); err != nil {
		return "", fmt.Errorf("publish Hermes staging %q: %w", service.stagingRoot, err)
	}
	return service.stagingRoot, nil
}

func (service *Service) builderFor(agent domain.AgentID) (artifactBuilder, error) {
	switch agent {
	case domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes:
	default:
		return nil, fmt.Errorf("plugin artifact agent %q: %w", agent, ErrUnsupportedAgent)
	}
	builder, ok := service.builders[agent]
	if !ok || builder == nil {
		return nil, fmt.Errorf("plugin artifact agent %q: %w", agent, ErrUnsupportedAgent)
	}
	return builder, nil
}

func (service *Service) artifactLock(root string) *sync.Mutex {
	service.locksMu.Lock()
	defer service.locksMu.Unlock()
	lock := service.artifactLocks[root]
	if lock == nil {
		lock = &sync.Mutex{}
		service.artifactLocks[root] = lock
	}
	return lock
}

func (service *Service) artifactFor(agent domain.AgentID, ref domain.PowerRef) (Artifact, error) {
	actualName, err := domain.ActualPluginName(ref)
	if err != nil {
		return Artifact{}, fmt.Errorf("plugin artifact reference %+v: %w", ref, err)
	}
	root := filepath.Join(service.cacheRoot, "plugins", string(agent), ref.Name, ref.Version)
	return Artifact{
		Agent:      agent,
		Power:      ref,
		ActualName: actualName,
		Root:       root,
		PluginPath: filepath.Join(root, "plugins", actualName),
	}, nil
}

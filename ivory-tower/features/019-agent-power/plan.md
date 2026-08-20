# Agent Power（Bibit 专属）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增第四种 Power 类型 `agent`（仅 Bibit 适用），按版本化 Cloud 目录提供 `agent.yaml`、走版本化缓存 + staging，并以可恢复 filesystem transaction 安装到 `<bibit根>/agents/<agent.id>/agent.yaml`；同时修正 Bibit 默认根为 `~/.bianbu-agent`。

**Architecture:** agent 完全对齐 skill 的「版本目录 + 标记文件 + 版本化缓存 + staging + filesystem transaction + manifest」模型，唯一区别是资源为单个 `agent.yaml` 文件而非 `SKILL.md` 目录。新增 Bibit 专属 `AgentAdapter` 与 `InstallAgent/UninstallAgent` 事务，复用 `DriverFilesystem` 恢复路径；`--type agent` 走 Bibit 专属 `--agent` 默认/拒绝规则。

**Tech Stack:** Go 1.26、Cobra、`go.yaml.in/yaml/v3`、`github.com/Masterminds/semver/v3`、标准库文件系统 API、表驱动测试、`t.TempDir()` 隔离。

**Spec:** `ivory-tower/features/019-agent-power/spec.md`

## Global Constraints

- Power 类型值精确为 `"agent"`；Agent ID 精确为 `"bibit"`；Bibit CLI 可执行名精确为 `"bianbu-agent"`。
- Bibit 根为 `BIANBU_AGENT_HOME` 非空时取该值，否则 `<有效 HOME>/.bianbu-agent`（前导点）。
- Cloud 布局 `resources/agents/<name>/<version>/agent.yaml`；版本真相 = 版本目录名；Cloud 保证资源对齐，APM 不交叉校验 id/version 与目录名。
- 下载单元为单个 `agent.yaml`；缓存身份 `cache/agents/<name>/<version>/agent.yaml`；staging 身份 `staging/agents/<name>/agent.yaml`。
- 本地安装路径 `<bibit根>/agents/<ref.Name>/agent.yaml`（单文件、不版本化、版本仅记 manifest；`ref.Name == agent.id`）。
- APM 只解析 agent.yaml 的 `schema_version`（必须为 1）、`agent.id`（安全路径段）、`agent.version`（`vMAJOR.MINOR.PATCH`），其余字段原样字节写入、不重序列化。
- `--type agent` 需要 `--agent` 的命令（install、uninstall、`list --installed`）：空 → 默认 bibit，`bibit` → 允许，其它（含 `all`）→ 报错。
- 五个命令（search/list/install/uninstall/fix）均支持 `agent`。
- 不得探测或调用真实 `bianbu-agent`、真实 home；所有 Agent home、APM home、cache、staging 与可执行文件均位于 `t.TempDir()`。
- 不迁移旧 cache/journal；不改 journal schema v1；卸载后裁剪空的 `agents/<id>/` 与空的 `agents/` 父目录。
- 重要公共接口与 lifecycle 分支需简明中文注释。

## File Structure

- `internal/domain`：`PowerTypeAgent`、最小 `AgentConfig`（yaml 标签 + `Validate`）。
- `internal/adapter`：`AgentAdapter` 接口、`bibit_agent.go` 单文件发布实现、Registry 注册与 `ResolveAgent`、Bibit 根修正。
- `internal/cloud`：`List` 含 agent、`DownloadAgent` 单文件复制。
- `internal/cache`：`StageAgent` 单文件物化。
- `internal/transaction`：`AgentInstallRequest/AgentUninstallRequest`、`InstallAgent/UninstallAgent`、agent 校验、放宽 filesystem journal 校验。
- `internal/config`：`agents` manifest 分组。
- `internal/lifecycle`：agent 分派与 `--agent` 默认/拒绝规则。
- `internal/cli`：`--type agent` 白名单与 `--agent` 校验。
- `ivory-tower`：ADR-015、Feature 019 spec/plan、Project Overview、BUILD、C4 文档。

---

### Task 1: Domain — `PowerTypeAgent` 与 `AgentConfig`

**Files:**
- Modify: `internal/domain/power.go`
- Create: `internal/domain/agent_config.go`
- Create: `internal/domain/agent_config_test.go`

**Interfaces:**
- Produces: `domain.PowerTypeAgent PowerType = "agent"`。
- Produces: `domain.AgentConfig`、`domain.AgentInfo`、`(AgentConfig) Validate() error`、`domain.ErrInvalidAgent`、`isSafeAgentID(string) bool`。

- [ ] **Step 1: Write failing domain tests**

Create `internal/domain/agent_config_test.go`:

```go
package domain

import (
	"errors"
	"testing"
)

func TestPowerTypeAgentIsStable(t *testing.T) {
	if PowerTypeAgent != "agent" {
		t.Fatalf("PowerTypeAgent = %q, want %q", PowerTypeAgent, "agent")
	}
}

func TestAgentConfigValidate(t *testing.T) {
	valid := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{ID: "bianbu-agent-default", Version: "v1.0.0"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := map[string]func(*AgentConfig){
		"wrong schema":   func(c *AgentConfig) { c.SchemaVersion = 2 },
		"empty id":       func(c *AgentConfig) { c.Agent.ID = "" },
		"dot id":         func(c *AgentConfig) { c.Agent.ID = "." },
		"slash id":       func(c *AgentConfig) { c.Agent.ID = "a/b" },
		"empty version":  func(c *AgentConfig) { c.Agent.Version = "" },
		"bare version":   func(c *AgentConfig) { c.Agent.Version = "1.0.0" },
		"prerelease":     func(c *AgentConfig) { c.Agent.Version = "v1.0.0-rc.1" },
		"metadata":       func(c *AgentConfig) { c.Agent.Version = "v1.0.0+build" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if err := config.Validate(); !errors.Is(err, ErrInvalidAgent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAgent", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/domain -run 'Test(PowerTypeAgentIsStable|AgentConfigValidate)'`
Expected: FAIL（`PowerTypeAgent`、`AgentConfig`、`ErrInvalidAgent` 未定义）。

- [ ] **Step 3: Add the domain type and validation**

Modify `internal/domain/power.go`（在 `PowerTypePlugin` 后追加）：

```go
	// PowerTypeAgent 表示 Agent 能力（Bibit 子代理，由 agent.yaml 配置）。
	PowerTypeAgent PowerType = "agent"
```

Create `internal/domain/agent_config.go`:

```go
package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// ErrInvalidAgent 表示 Agent 配置不满足安装约束。
var ErrInvalidAgent = errors.New("invalid agent")

// AgentConfig 表示 agent.yaml 中 APM 需要的最小字段；其余字段由安装流程原样透传。
type AgentConfig struct {
	SchemaVersion int       `yaml:"schema_version"`
	Agent         AgentInfo `yaml:"agent"`
}

// AgentInfo 表示 agent.yaml 的 agent 区块标识字段。
type AgentInfo struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

// Validate 校验 Agent 配置的最小边界，不清洗或改写原始值。
func (config AgentConfig) Validate() error {
	if config.SchemaVersion != 1 {
		return fmt.Errorf("agent schema version %d must be 1: %w", config.SchemaVersion, ErrInvalidAgent)
	}
	if !isSafeAgentID(config.Agent.ID) {
		return fmt.Errorf("agent id %q: %w", config.Agent.ID, ErrInvalidAgent)
	}
	if _, err := canonicalAgentVersion(config.Agent.Version); err != nil {
		return err
	}
	return nil
}

// isSafeAgentID 报告 agent id 是否为安全且未经清洗的单个路径段。
func isSafeAgentID(id string) bool {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." {
		return false
	}
	if filepath.IsAbs(id) || strings.ContainsAny(id, `/\\`) {
		return false
	}
	return filepath.Clean(id) == id
}

func canonicalAgentVersion(version string) (string, error) {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return "", fmt.Errorf("agent version %q must use vMAJOR.MINOR.PATCH: %w", version, errors.Join(ErrInvalidAgent, err))
	}
	if parsed.Prerelease() != "" || parsed.Metadata() != "" || "v"+parsed.String() != version {
		return "", fmt.Errorf("agent version %q must use vMAJOR.MINOR.PATCH: %w", version, ErrInvalidAgent)
	}
	return parsed.String(), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/domain`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/domain/power.go internal/domain/agent_config.go internal/domain/agent_config_test.go
git commit -m "feat(domain): add agent power type and agent config"
```

---

### Task 2: Bibit 根目录修正为 `.bianbu-agent`

**Files:**
- Modify: `internal/adapter/registry.go:21`
- Modify: `internal/adapter/registry_test.go:21`
- Modify: `internal/adapter/agent_profile_test.go:20,60,163,178,184`
- Modify: `internal/adapter/bibit_plugin_test.go:134`
- Modify: `internal/adapter/skill_filesystem_test.go:61`
- Modify: `BUILD.md:159`
- Modify: `ivory-tower/project-overview.md:54`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd:66`
- Modify: `ivory-tower/features/018-bibit-agent/spec.md:7,16,44`

**Interfaces:**
- Consumes: 无。
- Produces: Bibit `agentProfile.defaultRoot` 为 `".bianbu-agent"`；所有依赖默认根的测试/文档同步。

- [ ] **Step 1: Change the production default root**

Modify `internal/adapter/registry.go`，把 Bibit 的 `defaultRoot` 由 `"bianbu-agent"` 改为 `".bianbu-agent"`：

```go
		profile:  agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent", adaptSkill: noOpSkillAdapt},
```

- [ ] **Step 2: Update the exact test assertions**

- `internal/adapter/registry_test.go:21`：`[]string{"bianbu-agent", ".claude", ".codex", ".hermes"}` → `[]string{".bianbu-agent", ".claude", ".codex", ".hermes"}`。
- `internal/adapter/agent_profile_test.go`：第 20、60 行 `"bianbu-agent"` → `".bianbu-agent"`；第 163、178 行 `defaultRoot: "bianbu-agent"` → `defaultRoot: ".bianbu-agent"`；第 184 行 `filepath.Join(home, "bianbu-agent", "plugins", "demo-plugin")` → `filepath.Join(home, ".bianbu-agent", "plugins", "demo-plugin")`。
- `internal/adapter/bibit_plugin_test.go:134`：`defaultRoot: "bianbu-agent"` → `defaultRoot: ".bianbu-agent"`。
- `internal/adapter/skill_filesystem_test.go:61`：`filepath.Join(home, "fake-agent-home", "bianbu-agent")` → `filepath.Join(home, "fake-agent-home", ".bianbu-agent")`。

- [ ] **Step 3: Update documentation**

- `BUILD.md:159`：`<有效 HOME>/bianbu-agent` → `<有效 HOME>/.bianbu-agent`。
- `ivory-tower/project-overview.md:54`：默认 `` `~/bianbu-agent` `` → `` `~/.bianbu-agent` ``。
- `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd:66`：``默认 ~/bianbu-agent`` → ``默认 ~/.bianbu-agent``。
- `ivory-tower/features/018-bibit-agent/spec.md:7,16,44`：`` `~/bianbu-agent` `` / `<有效 HOME>/bianbu-agent` → `.bianbu-agent`。

- [ ] **Step 4: Run tests and verify no stale default-root references**

Run: `go test -count=1 ./internal/adapter ./internal/lifecycle`
Expected: PASS。

Run（确认剩余 `bianbu-agent` 仅为可执行名/环境变量名，不是默认根）：
`rg -n 'defaultRoot: "bianbu-agent"|"bianbu-agent", "\.claude"|~/bianbu-agent|<有效 HOME>/bianbu-agent' internal BUILD.md ivory-tower --glob '*.go' --glob '*.md' --glob '*.mmd'`
Expected: 无匹配（exit 1）。

- [ ] **Step 5: Commit**

```bash
git add internal/adapter BUILD.md ivory-tower
git commit -m "fix(adapter): correct bibit default root to ~/.bianbu-agent"
```

---

### Task 3: Cloud — agent 枚举与 `DownloadAgent`

**Files:**
- Modify: `internal/cloud/client.go`
- Create: `internal/cloud/local_agent.go`
- Modify: `internal/cloud/local_client.go`
- Create: `internal/cloud/local_agent_test.go`

**Interfaces:**
- Consumes: `domain.PowerTypeAgent`（Task 1）。
- Produces: `cloud.Client.DownloadAgent(domain.PowerRef) error`；`LocalClient.List()` 产出 agent `PowerRef`；`listResources` 复用。

- [ ] **Step 1: Write failing Cloud tests**

Create `internal/cloud/local_agent_test.go`：

```go
package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestLocalClientListsAgents(t *testing.T) {
	client := newAgentTestClient(t)
	refs, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	for _, ref := range refs {
		if ref == want {
			return
		}
	}
	t.Fatalf("List() = %#v, missing %#v", refs, want)
}

func TestLocalClientDownloadsSingleAgentYAML(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if err := client.DownloadAgent(ref); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(client.cacheRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	contents, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("read cached agent %q: %v", final, err)
	}
	if string(contents) == "" {
		t.Fatalf("cached agent %q is empty", final)
	}
	// 幂等：再次下载不报错。
	if err := client.DownloadAgent(ref); err != nil {
		t.Fatalf("second DownloadAgent() error = %v", err)
	}
}

func TestLocalClientDownloadAgentRejectsWrongType(t *testing.T) {
	client := newAgentTestClient(t)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeSkill, Version: "v1.0.0"}
	if err := client.DownloadAgent(ref); err == nil {
		t.Fatal("DownloadAgent() error = nil, want ErrNotFound")
	}
}
```

其中 `newAgentTestClient` 复用现有 Cloud 测试的临时 cache 与真实 `resources` 根：

```go
func newAgentTestClient(t *testing.T) *LocalClient {
	t.Helper()
	resources, err := filepath.Abs(filepath.Join("..", "cloud", "resources"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewLocalClient(resources)
	if err != nil {
		t.Fatal(err)
	}
	client.cacheRoot = t.TempDir()
	client.cacheTempRoot = filepath.Join(t.TempDir(), ".tmp")
	return client
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/cloud -run TestLocalClient.*Agent`
Expected: FAIL（`DownloadAgent` 未定义、`List` 不含 agent）。

- [ ] **Step 3: Implement the interface and local agent support**

Modify `internal/cloud/client.go`，在接口中追加：

```go
	// DownloadAgent 将指定版本的 Agent yaml 原子复制到本地缓存。
	DownloadAgent(domain.PowerRef) error
```

Create `internal/cloud/local_agent.go`:

```go
package cloud

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

// listAgents 枚举 agents 目录下的版本化 agent.yaml 资源。
func (c *LocalClient) listAgents() ([]domain.PowerRef, error) {
	return c.listResources("agents", "agent.yaml", domain.PowerTypeAgent)
}

// DownloadAgent 将指定版本的 Agent yaml 原子复制到本地缓存；不做内容解析。
func (c *LocalClient) DownloadAgent(ref domain.PowerRef) (resultErr error) {
	if ref.Version == "" {
		return errors.New("power version is required")
	}
	if ref.Type != domain.PowerTypeAgent {
		return ErrNotFound
	}
	if err := os.RemoveAll(c.cacheTempRoot); err != nil {
		return fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err)
	}
	defer func() {
		if err := os.RemoveAll(c.cacheTempRoot); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("clean cache temporary path %q: %w", c.cacheTempRoot, err))
		}
	}()

	final := filepath.Join(c.cacheRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	temporary := filepath.Join(c.cacheTempRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	source := filepath.Join(c.resourcesRoot, "agents", ref.Name, ref.Version, "agent.yaml")

	if _, err := os.Stat(final); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat cache power %q: %w", final, err)
	}
	sourceInfo, err := os.Stat(source)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !sourceInfo.Mode().IsRegular()) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("stat source agent %q: %w", source, err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source agent %q: %w", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(temporary), 0o755); err != nil {
		return fmt.Errorf("create cache temporary parent %q: %w", filepath.Dir(temporary), err)
	}
	if err := os.WriteFile(temporary, contents, 0o644); err != nil {
		return fmt.Errorf("write cache temporary agent %q: %w", temporary, err)
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("create cache parent %q: %w", filepath.Dir(final), err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return fmt.Errorf("commit cached agent %q: %w", final, err)
	}
	return nil
}
```

Modify `internal/cloud/local_client.go` 的 `List()`，在 `pluginRefs` 追加后加入：

```go
	agentRefs, err := c.listAgents()
	if err != nil {
		return nil, err
	}
	refs = append(refs, agentRefs...)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/cloud`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/client.go internal/cloud/local_client.go internal/cloud/local_agent.go internal/cloud/local_agent_test.go
git commit -m "feat(cloud): list and download versioned agent yaml"
```

---

### Task 4: Cache — `StageAgent`

**Files:**
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Create: `internal/cache/agent_test.go`

**Interfaces:**
- Consumes: `domain.PowerTypeAgent`（Task 1）、Cloud 缓存布局（Task 3）。
- Produces: `cache.PowerCache.StageAgent(domain.PowerRef) (string, error)`。

- [ ] **Step 1: Write failing cache tests**

Create `internal/cache/agent_test.go`：

```go
package cache

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestFilesystemCacheStagesSingleAgentYAML(t *testing.T) {
	cacheRoot := t.TempDir()
	cache := newAgentCache(t, cacheRoot)
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}

	source := filepath.Join(cacheRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	staging, err := cache.StageAgent(ref)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(staging, "agents", ref.Name, "agent.yaml")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("staged agent %q missing: %v", want, err)
	}
}

func TestFilesystemCacheStageAgentMiss(t *testing.T) {
	cache := newAgentCache(t, t.TempDir())
	ref := domain.PowerRef{Name: "missing", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if _, err := cache.StageAgent(ref); err == nil {
		t.Fatal("StageAgent() error = nil, want ErrMiss")
	}
}
```

其中 `newAgentCache` 直接构造内部字段（同包测试）：

```go
func newAgentCache(t *testing.T, cacheRoot string) *FilesystemCache {
	t.Helper()
	return &FilesystemCache{
		cacheRoot:       cacheRoot,
		stagingPath:     filepath.Join(t.TempDir(), "staging"),
		stagingTempPath: filepath.Join(t.TempDir(), ".tmp", "staging"),
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/cache -run TestFilesystemCache.*Agent`
Expected: FAIL（`StageAgent` 未定义）。

- [ ] **Step 3: Implement StageAgent**

Modify `internal/cache/cache.go`：

```go
type PowerCache interface {
	StageSkill(domain.PowerRef) (stagingRoot string, err error)
	StageAgent(domain.PowerRef) (stagingRoot string, err error)
}
```

Modify `internal/cache/filesystem.go`，在 `StageSkill` 后追加：

```go
// StageAgent 将指定版本的缓存 Agent yaml 复制到公共 staging。
func (c *FilesystemCache) StageAgent(ref domain.PowerRef) (string, error) {
	return c.stageAgent(ref)
}

func (c *FilesystemCache) stageAgent(ref domain.PowerRef) (stagingRoot string, resultErr error) {
	if ref.Version == "" {
		return "", errors.New("power version is required")
	}
	if err := os.RemoveAll(c.stagingPath); err != nil {
		return "", fmt.Errorf("remove staging path %q: %w", c.stagingPath, err)
	}
	if err := os.RemoveAll(c.stagingTempPath); err != nil {
		return "", fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err)
	}
	defer func() {
		if err := os.RemoveAll(c.stagingTempPath); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove staging temporary path %q: %w", c.stagingTempPath, err))
		}
	}()

	source := filepath.Join(c.cacheRoot, "agents", ref.Name, ref.Version, "agent.yaml")
	contents, err := os.ReadFile(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrMiss
		}
		return "", fmt.Errorf("read cache path %q: %w", source, err)
	}

	target := filepath.Join(c.stagingTempPath, "agents", ref.Name)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create staging target %q: %w", target, err)
	}
	if err := os.WriteFile(filepath.Join(target, "agent.yaml"), contents, 0o644); err != nil {
		return "", fmt.Errorf("write staging agent %q: %w", filepath.Join(target, "agent.yaml"), err)
	}
	if err := os.MkdirAll(filepath.Dir(c.stagingPath), 0o755); err != nil {
		return "", fmt.Errorf("create staging parent path %q: %w", filepath.Dir(c.stagingPath), err)
	}
	if err := os.Rename(c.stagingTempPath, c.stagingPath); err != nil {
		return "", fmt.Errorf("rename staging temporary path %q to staging path %q: %w", c.stagingTempPath, c.stagingPath, err)
	}
	return c.stagingPath, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/cache`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cache/cache.go internal/cache/filesystem.go internal/cache/agent_test.go
git commit -m "feat(cache): stage single-file agent yaml"
```

---

### Task 5: Adapter — `AgentAdapter` 与 Bibit 实现

**Files:**
- Modify: `internal/adapter/errors.go`
- Create: `internal/adapter/agent.go`
- Create: `internal/adapter/bibit_agent.go`
- Modify: `internal/adapter/registry.go`
- Create: `internal/adapter/bibit_agent_test.go`
- Modify: `internal/adapter/registry_test.go`

**Interfaces:**
- Consumes: `domain.AgentConfig`/`domain.ErrInvalidAgent`（Task 1）、`agentProfile`（Task 2 的 `.bianbu-agent` 根）、`validateStagingIdentity`（既有）。
- Produces: `adapter.AgentAdapter`、`adapter.AgentInstallResult`、`newBibitAgentAdapter(agentProfile) *bibitAgentAdapter`、`adapter.ErrInvalidAgent`、`Registry.ResolveAgent(domain.AgentID) (AgentAdapter, error)`。

- [ ] **Step 1: Write failing adapter tests**

Create `internal/adapter/bibit_agent_test.go`：

```go
package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/domain"
)

func TestBibitAgentAdapterTargetPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	target, err := a.TargetPath(ref)
	if err != nil || target != filepath.Join(root, "agents", "bianbu-agent-default", "agent.yaml") {
		t.Fatalf("TargetPath() = %q, %v", target, err)
	}
}

func TestBibitAgentAdapterInstallsYAMLVerbatim(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	staging := t.TempDir()
	_ = os.MkdirAll(filepath.Join(staging, "agents", "bianbu-agent-default"), 0o755)
	raw := []byte("schema_version: 1\nagent:\n  id: bianbu-agent-default\n  version: v1.0.0\n  name: X\n")
	if err := os.WriteFile(filepath.Join(staging, "agents", "bianbu-agent-default", "agent.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	result, err := a.Install(ref, staging)
	if err != nil || !result.Published {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	target := filepath.Join(root, "agents", "bianbu-agent-default", "agent.yaml")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read installed agent: %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("installed bytes = %q, want verbatim %q", got, raw)
	}
}

func TestBibitAgentAdapterRejectsInvalidYAML(t *testing.T) {
	root := t.TempDir()
	t.Setenv("BIANBU_AGENT_HOME", root)
	staging := t.TempDir()
	_ = os.MkdirAll(filepath.Join(staging, "agents", "x"), 0o755)
	_ = os.WriteFile(filepath.Join(staging, "agents", "x", "agent.yaml"), []byte("schema_version: 1\nagent:\n  id: x\n  version: bad\n"), 0o644)

	a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
	ref := domain.PowerRef{Name: "x", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if _, err := a.Install(ref, staging); err == nil {
		t.Fatal("Install() error = nil, want validation error")
	}
}
```

Modify `internal/adapter/registry_test.go` 追加：

```go
func TestRegistryResolvesBibitAgentAdapter(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.ResolveAgent(domain.AgentBibit); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolveAgent(domain.AgentCodex); err == nil {
		t.Fatal("ResolveAgent(codex) error = nil, want unsupported")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/adapter -run 'Test(BibitAgent|RegistryResolvesBibitAgent)'`
Expected: FAIL（`AgentAdapter`、`ResolveAgent` 未定义）。

- [ ] **Step 3: Implement the adapter and registry wiring**

Modify `internal/adapter/errors.go`，追加：

```go
	// ErrInvalidAgent 表示 Agent 引用、staging 或 agent.yaml 不符合安装约束。
	ErrInvalidAgent = errors.New("invalid agent")
```

Create `internal/adapter/agent.go`:

```go
package adapter

import "apm/internal/domain"

// AgentInstallResult 报告 Agent Adapter 安装是否已经越过原子发布提交点。
type AgentInstallResult struct {
	Published bool
}

// AgentAdapter 定义智能体的文件型 Agent（Bibit 子代理）安装操作。
type AgentAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (AgentInstallResult, error)
}
```

Create `internal/adapter/bibit_agent.go`:

```go
package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"apm/internal/domain"
)

// bibitAgentAdapter 实现从公共 staging 到 Bibit agents 目录的单文件 Agent 安装流程。
type bibitAgentAdapter struct {
	profile   agentProfile
	readFile  func(string) ([]byte, error)
	mkdirAll  func(string, os.FileMode) error
	rename    func(string, string) error
	removeAll func(string) error
}

func newBibitAgentAdapter(profile agentProfile) *bibitAgentAdapter {
	return &bibitAgentAdapter{
		profile:   profile,
		readFile:  os.ReadFile,
		mkdirAll:  os.MkdirAll,
		rename:    os.Rename,
		removeAll: os.RemoveAll,
	}
}

// TargetPath 返回指定 Agent 的最终安装文件路径；目录名使用 ref.Name（Cloud 保证等于 agent.id）。
func (a *bibitAgentAdapter) TargetPath(ref domain.PowerRef) (string, error) {
	if ref.Type != domain.PowerTypeAgent || !isSafeSkillName(ref.Name) {
		return "", fmt.Errorf("target path for agent %q power %q: %w", a.profile.id, ref.Name, ErrInvalidAgent)
	}
	root, err := a.profile.rootPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "agents", ref.Name, "agent.yaml"), nil
}

// Install 从公共 staging 读取并校验 agent.yaml，再原子发布单文件到 Bibit agents 目录。
func (a *bibitAgentAdapter) Install(ref domain.PowerRef, staging string) (result AgentInstallResult, resultErr error) {
	stagingRoot, err := validateStagingIdentity(staging)
	if err != nil {
		return result, a.installError(ref, "staging", staging, err)
	}
	defer func() {
		if err := a.removeAll(stagingRoot); err != nil {
			resultErr = errors.Join(resultErr, a.installError(ref, "cleanup", stagingRoot, err))
		}
	}()

	if ref.Type != domain.PowerTypeAgent || !isSafeSkillName(ref.Name) || ref.Version == "" {
		return result, a.installError(ref, "validation", stagingRoot, ErrInvalidAgent)
	}

	root, err := a.profile.rootPath()
	if err != nil {
		return result, a.installError(ref, "detection", stagingRoot, err)
	}
	if err := ensureBibitAgentRoot(root); err != nil {
		return result, a.installError(ref, "detection", root, err)
	}

	source := filepath.Join(stagingRoot, "agents", ref.Name, "agent.yaml")
	raw, err := a.readFile(source)
	if err != nil {
		return result, a.installError(ref, "read", source, err)
	}
	var config domain.AgentConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return result, a.installError(ref, "parse", source, fmt.Errorf("%w: %v", ErrInvalidAgent, err))
	}
	if err := config.Validate(); err != nil {
		return result, a.installError(ref, "validation", source, err)
	}

	target := filepath.Join(root, "agents", ref.Name, "agent.yaml")
	if err := ensureAgentTargetAbsent(target); err != nil {
		return result, a.installError(ref, "detection", target, err)
	}
	if err := a.mkdirAll(filepath.Dir(target), 0o755); err != nil {
		return result, a.installError(ref, "directory", filepath.Dir(target), err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".apm-"+ref.Name+"-*")
	if err != nil {
		return result, a.installError(ref, "temporary", filepath.Dir(target), err)
	}
	tempName := temp.Name()
	cleanupTemp := func() {
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}
	defer cleanupTemp()
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return result, a.installError(ref, "write", tempName, err)
	}
	if err := temp.Close(); err != nil {
		return result, a.installError(ref, "write", tempName, err)
	}
	if err := a.rename(tempName, target); err != nil {
		return result, a.installError(ref, "publication", target, err)
	}
	tempName = ""
	result.Published = true
	return result, nil
}

func (a *bibitAgentAdapter) installError(ref domain.PowerRef, phase, path string, err error) error {
	return fmt.Errorf("install agent %q power %q phase %q path %q: %w", a.profile.id, ref.Name, phase, path, err)
}

// ensureBibitAgentRoot 确保 Bibit 根目录存在且为目录。
func ensureBibitAgentRoot(root string) error {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return ErrAgentNotInstalled
	}
	if err != nil {
		return fmt.Errorf("stat agent root %q: %w", root, err)
	}
	if !info.IsDir() {
		return ErrAgentNotInstalled
	}
	return nil
}

// ensureAgentTargetAbsent 确保最终目标文件不存在任何文件系统条目。
func ensureAgentTargetAbsent(target string) error {
	_, err := os.Lstat(target)
	if err == nil {
		return fmt.Errorf("agent target %q: %w", target, ErrTargetConflict)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("lstat agent target %q: %w", target, err)
}

var _ AgentAdapter = (*bibitAgentAdapter)(nil)
```

Modify `internal/adapter/registry.go`：

- 在 `agentRegistration` 结构体追加字段 `newAgent func(agentProfile) AgentAdapter`。
- Bibit 注册项追加：

```go
		newAgent: func(profile agentProfile) AgentAdapter { return newBibitAgentAdapter(profile) },
```

- `Registry` 结构体追加 `agentByAgent map[domain.AgentID]AgentAdapter`，`NewRegistry` 初始化并填充；其它注册项 `newAgent` 为 nil。
- 追加方法：

```go
// ResolveAgent 解析指定智能体的文件型 Agent Adapter。
func (r *Registry) ResolveAgent(agent domain.AgentID) (AgentAdapter, error) {
	adapter, ok := r.agentByAgent[agent]
	if !ok {
		return nil, fmt.Errorf("resolve agent %q power %q: %w", agent, domain.PowerTypeAgent, ErrUnsupportedAdapter)
	}
	return adapter, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/adapter`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/adapter
git commit -m "feat(adapter): add bibit filesystem agent adapter"
```

---

### Task 6: Transaction — `InstallAgent`/`UninstallAgent` 与 filesystem 校验放宽

**Files:**
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/skill_validation.go`
- Create: `internal/transaction/agent_validation.go`
- Create: `internal/transaction/agent_install.go`
- Create: `internal/transaction/agent_uninstall.go`
- Create: `internal/transaction/agent_test.go`

**Interfaces:**
- Consumes: `adapter.AgentAdapter`（Task 5）、既有 `artifactPaths`/`cleanupInstallStagingWithOps`/`cleanupTransactionDirWithOps`/`rollbackInstall`/`rollbackUninstall`/`pathExists`。
- Produces: `transaction.AgentInstallRequest`、`transaction.AgentUninstallRequest`、`Manager.InstallAgent`、`Manager.UninstallAgent`；`validatePower` 接受 `PowerTypeAgent`；`validateFilesystemJournal` 接受 `skill|agent`。

- [ ] **Step 1: Write failing transaction tests**

Create `internal/transaction/agent_test.go`：

```go
package transaction

import (
	"testing"

	"apm/internal/domain"
)

func TestValidatePowerAcceptsAgent(t *testing.T) {
	if err := validatePower(domain.PowerRef{Name: "a", Type: domain.PowerTypeAgent, Version: "v1.0.0"}, ErrInvalidRequest); err != nil {
		t.Fatalf("validatePower(agent) error = %v", err)
	}
}

func TestValidateAgentInstallRequest(t *testing.T) {
	valid := AgentInstallRequest{
		Agent:       domain.AgentBibit,
		Power:       domain.PowerRef{Name: "a", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
		StagingRoot: agentStagingRoot(t),
		Adapter:     &agentValidationAdapter{},
	}
	if err := validateAgentInstallRequest(valid); err != nil {
		t.Fatalf("validateAgentInstallRequest() error = %v", err)
	}
	tests := map[string]func(*AgentInstallRequest){
		"empty agent":       func(r *AgentInstallRequest) { r.Agent = "" },
		"all agents":        func(r *AgentInstallRequest) { r.Agent = domain.AllAgents },
		"empty name":        func(r *AgentInstallRequest) { r.Power.Name = "" },
		"skill power":       func(r *AgentInstallRequest) { r.Power.Type = domain.PowerTypeSkill },
		"empty version":     func(r *AgentInstallRequest) { r.Power.Version = "" },
		"empty staging":     func(r *AgentInstallRequest) { r.StagingRoot = "" },
		"nil adapter":       func(r *AgentInstallRequest) { r.Adapter = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := validateAgentInstallRequest(request); err == nil {
				t.Fatalf("validateAgentInstallRequest() error = nil")
			}
		})
	}
}
```

测试用到的辅助定义：

```go
type agentValidationAdapter struct{}

func (a *agentValidationAdapter) TargetPath(domain.PowerRef) (string, error) {
	return "/tmp/agent.yaml", nil
}
func (a *agentValidationAdapter) Install(domain.PowerRef, string) (adapter.AgentInstallResult, error) {
	return adapter.AgentInstallResult{Published: true}, nil
}

// agentStagingRoot 返回与 paths.StagingPath 一致的隔离 staging 绝对路径。
func agentStagingRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("APM_HOME", t.TempDir())
	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	return staging
}
```

并在测试文件头 import `"apm/internal/adapter"` 与 `"apm/internal/paths"`。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/transaction -run 'Test(ValidatePowerAcceptsAgent|ValidateAgentInstallRequest)'`
Expected: FAIL（`AgentInstallRequest`、`validateAgentInstallRequest` 未定义；`validatePower` 拒绝 agent）。

- [ ] **Step 3: Implement transaction support**

Modify `internal/transaction/manager.go`：

- `Manager` 接口追加：

```go
	InstallAgent(context.Context, AgentInstallRequest) error
	UninstallAgent(context.Context, AgentUninstallRequest) (UninstallResult, error)
```

- 追加请求类型：

```go
// AgentInstallRequest 描述单个 Agent 的已解析文件型 Agent 安装事务输入。
type AgentInstallRequest struct {
	Agent       domain.AgentID
	Power       domain.PowerRef
	Previous    *domain.PowerRef
	StagingRoot string
	Adapter     adapter.AgentAdapter
}

// AgentUninstallRequest 描述单个 Agent 的已解析文件型 Agent 卸载事务输入。
type AgentUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.AgentAdapter
}
```

Modify `internal/transaction/validation.go` 的 `validatePower`，在 switch 中追加 `domain.PowerTypeAgent`：

```go
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP, domain.PowerTypeAgent:
		return nil
```

Modify `internal/transaction/skill_validation.go` 的 `validateFilesystemJournal`，把首行类型校验放宽：

```go
	if !isFilesystemPowerType(journal.Power.Type) {
		return fmt.Errorf("filesystem power type %q: %w", journal.Power.Type, ErrInvalidJournal)
	}
```

并在同文件追加：

```go
// isFilesystemPowerType 报告该类型是否由 filesystem driver 恢复。
func isFilesystemPowerType(powerType domain.PowerType) bool {
	return powerType == domain.PowerTypeSkill || powerType == domain.PowerTypeAgent
}
```

Create `internal/transaction/agent_validation.go`：

```go
package transaction

import (
	"fmt"

	"apm/internal/domain"
)

// validateAgentInstallRequest 校验文件型 Agent 安装事务的完整输入边界。
func validateAgentInstallRequest(request AgentInstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate install request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeAgent {
		return fmt.Errorf("filesystem install power type %q: %w", request.Power.Type, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate install request: nil adapter: %w", ErrInvalidRequest)
	}
	if err := validateCanonicalStaging(request.StagingRoot, ErrInvalidRequest); err != nil {
		return fmt.Errorf("validate install request: %w", err)
	}
	if request.Previous != nil {
		if err := validatePower(*request.Previous, ErrInvalidRequest); err != nil {
			return fmt.Errorf("validate install request previous power: %w", err)
		}
		if request.Previous.Name != request.Power.Name || request.Previous.Type != request.Power.Type {
			return fmt.Errorf("validate install request previous power does not match target: %w", ErrInvalidRequest)
		}
	}
	return nil
}

// validateAgentUninstallRequest 校验文件型 Agent 卸载事务的完整输入边界。
func validateAgentUninstallRequest(request AgentUninstallRequest) error {
	if err := validateAgentAndPower(request.Agent, request.Power); err != nil {
		return fmt.Errorf("validate uninstall request: %w", err)
	}
	if request.Power.Type != domain.PowerTypeAgent {
		return fmt.Errorf("filesystem uninstall power type %q: %w", request.Power.Type, ErrInvalidRequest)
	}
	if isNilInterface(request.Adapter) {
		return fmt.Errorf("validate uninstall request: nil adapter: %w", ErrInvalidRequest)
	}
	return nil
}
```

Create `internal/transaction/agent_install.go`（与 `skill_install.go` 的 `InstallSkill` 逐行对应，仅替换类型名与校验名）：

```go
package transaction

import (
	"context"
	"errors"
	"fmt"

	"apm/internal/adapter"
)

// InstallAgent 执行单个 Agent 的可恢复文件型 Agent 安装事务。
func (s *Service) InstallAgent(ctx context.Context, request AgentInstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateAgentInstallRequest(request); err != nil {
		return err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return fmt.Errorf("install transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return ErrActiveTransaction
	}

	target, err := request.Adapter.TargetPath(request.Power)
	if err != nil {
		return fmt.Errorf("resolve install target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return err
	}
	targetExists, err := s.pathExists(target)
	if err != nil {
		return fmt.Errorf("inspect install target %q: %w", target, err)
	}
	if request.Previous == nil && targetExists {
		return fmt.Errorf("unmanaged install target %q: %w", target, adapter.ErrTargetConflict)
	}
	if request.Previous != nil && !targetExists {
		staleErr := fmt.Errorf("installed target %q for agent %q power %q is missing: %w", target, request.Agent, request.Power.Name, ErrStaleManifest)
		removeErr := s.manifests.Remove(request.Agent, request.Previous.Type, request.Previous.Name)
		cleanupErr := cleanupInstallStagingWithOps(request.StagingRoot, target, s.files)
		if removeErr != nil {
			return errors.Join(
				staleErr,
				fmt.Errorf("remove stale manifest entry for agent %q power %q: %w", request.Agent, request.Previous.Name, removeErr),
				cleanupErr,
			)
		}
		return errors.Join(
			fmt.Errorf("removed stale manifest entry for agent %q power %q after target disappeared: %w", request.Agent, request.Previous.Name, ErrStaleManifest),
			cleanupErr,
		)
	}

	transactionID, err := s.newID()
	if err != nil {
		return err
	}
	artifacts, err := artifactPaths(target, transactionID)
	if err != nil {
		return err
	}
	journal := Journal{
		SchemaVersion: 1,
		TransactionID: transactionID,
		Driver:        DriverFilesystem,
		Operation:     OperationInstall,
		Phase:         PhasePreparing,
		Agent:         request.Agent,
		Power:         request.Power,
		Previous:      clonePowerRef(request.Previous),
		Filesystem: &FilesystemJournalPayload{
			StagingRoot:    request.StagingRoot,
			TargetPath:     target,
			TransactionDir: artifacts.dir,
			BackupPath:     artifacts.backup,
		},
	}
	if err := s.journals.Create(journal); err != nil {
		return fmt.Errorf("create install journal: %w", err)
	}
	s.debugTransaction(journal)

	if err := ctx.Err(); err != nil {
		return s.rollbackInstall(journal, err)
	}
	if request.Previous != nil && targetExists {
		if err := ensureTransactionDirWithOps(artifacts, s.files); err != nil {
			return s.rollbackInstall(journal, err)
		}
		if err := s.files.rename(target, artifacts.backup); err != nil {
			return s.rollbackInstall(journal, fmt.Errorf("backup install target %q to %q: %w", target, artifacts.backup, err))
		}
	}
	if exists, err := s.pathExists(target); err != nil {
		return s.rollbackInstall(journal, fmt.Errorf("recheck install target %q: %w", target, err))
	} else if exists {
		return s.rollbackInstall(journal, fmt.Errorf("install target appeared before publication %q: %w", target, adapter.ErrTargetConflict))
	}

	journal.Phase = PhasePublishing
	if err := s.journals.Update(journal); err != nil {
		return s.rollbackInstall(journal, fmt.Errorf("persist install publishing intent: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return s.rollbackInstall(journal, err)
	}
	result, installErr := request.Adapter.Install(request.Power, request.StagingRoot)
	if !result.Published {
		if installErr == nil {
			installErr = fmt.Errorf("adapter returned success before publication: %w", ErrInvalidRequest)
		}
		return s.rollbackInstall(journal, installErr)
	}

	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return errors.Join(installErr, fmt.Errorf("persist install committing direction: %w", err))
	}
	if installErr != nil {
		return installErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.manifests.Set(request.Agent, request.Power); err != nil {
		return fmt.Errorf("set installed power manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.backup); err != nil {
		return fmt.Errorf("remove install backup %q: %w", artifacts.backup, err)
	}
	if err := cleanupCommittedStagingWithOps(journal, s.files); err != nil {
		return err
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return err
	}
	if err := s.journals.Delete(transactionID); err != nil {
		return fmt.Errorf("delete completed install journal: %w", err)
	}
	return nil
}
```

> 说明：`InstallAgent` 复用的 `rollbackInstall`（`skill_install.go`）、`artifactPaths`、`cleanup*`、`pathExists`、`debugTransaction`、`clonePowerRef` 均为类型无关的既有同包函数，agent 文件无需重复定义，也无需 import `slog`/`logging`/`os`/`domain`。

Create `internal/transaction/agent_uninstall.go`（与 `skill_uninstall.go` 的 `UninstallSkill` 逐行对应，仅替换类型名与校验名）：

```go
package transaction

import (
	"context"
	"errors"
	"fmt"
)

// UninstallAgent 执行单个 Agent 的可恢复文件型 Agent 卸载事务。
func (s *Service) UninstallAgent(ctx context.Context, request AgentUninstallRequest) (UninstallResult, error) {
	result := UninstallResult{}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := validateAgentUninstallRequest(request); err != nil {
		return result, err
	}
	if isNilInterface(s.manifests) || isNilInterface(s.journals) {
		return result, fmt.Errorf("uninstall transaction dependencies are nil: %w", ErrInvalidRequest)
	}
	if _, found, err := s.journals.Inspect(); err != nil {
		return result, fmt.Errorf("inspect active transaction: %w", err)
	} else if found {
		return result, ErrActiveTransaction
	}

	target, err := request.Adapter.TargetPath(request.Power)
	if err != nil {
		return result, fmt.Errorf("resolve uninstall target: %w", err)
	}
	if err := validateSafeAbsolutePath("target", target); err != nil {
		return result, err
	}
	transactionID, err := s.newID()
	if err != nil {
		return result, err
	}
	artifacts, err := artifactPaths(target, transactionID)
	if err != nil {
		return result, err
	}
	journal := Journal{
		SchemaVersion: 1,
		TransactionID: transactionID,
		Driver:        DriverFilesystem,
		Operation:     OperationUninstall,
		Phase:         PhaseRemoving,
		Agent:         request.Agent,
		Power:         request.Power,
		Filesystem: &FilesystemJournalPayload{
			TargetPath:     target,
			TransactionDir: artifacts.dir,
			QuarantinePath: artifacts.quarantine,
		},
	}
	if err := s.journals.Create(journal); err != nil {
		return result, fmt.Errorf("create uninstall journal: %w", err)
	}
	s.debugTransaction(journal)

	if err := ctx.Err(); err != nil {
		return result, s.rollbackUninstall(journal, err)
	}
	targetExists, err := s.pathExists(target)
	if err != nil {
		return result, s.rollbackUninstall(journal, fmt.Errorf("inspect uninstall target %q: %w", target, err))
	}
	if !targetExists {
		result.TargetMissing = true
	}
	if targetExists {
		if err := ensureTransactionDirWithOps(artifacts, s.files); err != nil {
			return result, s.rollbackUninstall(journal, err)
		}
		if err := s.files.rename(target, artifacts.quarantine); err != nil {
			existsAfterFailure, inspectErr := s.pathExists(target)
			if inspectErr != nil {
				return result, errors.Join(err, fmt.Errorf("recheck uninstall target %q: %w", target, inspectErr))
			}
			if existsAfterFailure {
				return result, s.rollbackUninstall(journal, fmt.Errorf("quarantine uninstall target %q: %w", target, err))
			}
			result.TargetMissing = true
		}
	}

	journal.Phase = PhaseCommitting
	if err := s.journals.Update(journal); err != nil {
		return result, fmt.Errorf("persist uninstall committing direction: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.manifests.Remove(request.Agent, request.Power.Type, request.Power.Name); err != nil {
		return result, fmt.Errorf("remove installed power manifest: %w", err)
	}
	if err := s.files.removeAll(artifacts.quarantine); err != nil {
		return result, fmt.Errorf("remove uninstall quarantine %q: %w", artifacts.quarantine, err)
	}
	if err := cleanupTransactionDirWithOps(journal, s.files); err != nil {
		return result, err
	}
	if err := s.journals.Delete(transactionID); err != nil {
		return result, fmt.Errorf("delete completed uninstall journal: %w", err)
	}
	return result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/transaction`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/transaction
git commit -m "feat(transaction): add recoverable filesystem agent install/uninstall"
```

---

### Task 7: Config — `agents` manifest 分组

**Files:**
- Modify: `internal/config/manifest.go`
- Modify: `internal/config/json_repository.go`
- Modify: `internal/config/json_repository_test.go`

**Interfaces:**
- Consumes: `domain.PowerTypeAgent`（Task 1）。
- Produces: manifest JSON 字段 `agents`；`emptyPowerGroups`/`manifestToPowerGroups`/`powerGroupsToManifest`/`clonePowerGroups`/`validPowerType`/`validatePowerGroups` 均支持 `agent`。

- [ ] **Step 1: Write failing config tests**

Modify `internal/config/json_repository_test.go`，追加：

```go
func TestManifestAgentsGroupRoundTrip(t *testing.T) {
	repository := NewJSONRepository()
	agent := domain.AgentBibit
	ref := domain.PowerRef{Name: "bianbu-agent-default", Type: domain.PowerTypeAgent, Version: "v1.0.0"}
	if err := repository.Set(agent, ref); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.Find(agent, domain.PowerTypeAgent, "bianbu-agent-default")
	if err != nil || !found || got != ref {
		t.Fatalf("Find() = %#v, %t, %v", got, found, err)
	}
	groups, err := repository.List(agent)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups[domain.PowerTypeAgent]) != 1 || groups[domain.PowerTypeAgent][0] != ref {
		t.Fatalf("List() agent group = %#v", groups[domain.PowerTypeAgent])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/config -run TestManifestAgentsGroupRoundTrip`
Expected: FAIL（`Set` 因 `validPowerType` 拒绝 agent 而报错）。

- [ ] **Step 3: Implement the agents group**

Modify `internal/config/manifest.go`：

- `manifestDocument` 在 `Skills` 后追加 `Agents []powerEntry \`json:"agents"\``。
- `emptyPowerGroups` 追加 `domain.PowerTypeAgent: make([]domain.PowerRef, 0)`。
- `manifestToPowerGroups` 追加 `groups[domain.PowerTypeAgent] = entriesToPowerRefs(document.Agents, domain.PowerTypeAgent)`。
- `powerGroupsToManifest` 追加 `Agents: powerRefsToEntries(groups[domain.PowerTypeAgent])`。
- `clonePowerGroups` 的循环切片追加 `domain.PowerTypeAgent`。

Modify `internal/config/json_repository.go`：

- `validPowerType` switch 追加 `case domain.PowerTypeAgent:`。
- `validatePowerGroups` 的循环切片追加 `domain.PowerTypeAgent`。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/config`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat(config): add agents manifest group"
```

---

### Task 8: Lifecycle — agent 分派与 `--agent` 默认/拒绝

**Files:**
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/uninstall_agent.go`
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/uninstall.go`
- Create: `internal/lifecycle/install_agent_power.go`
- Create: `internal/lifecycle/uninstall_agent_power.go`
- Create: `internal/lifecycle/agent_test.go`

**Interfaces:**
- Consumes: `transaction.AgentInstallRequest/AgentUninstallRequest`（Task 6）、`AdapterRegistry.ResolveAgent`、`cache.StageAgent`、`cloud.DownloadAgent`。
- Produces: `AdapterRegistry` 增加 `ResolveAgent`；`installForAgent`/`uninstallForAgent` 增加 `agent` 分支；`installAgents`/`uninstallAgents` 对 `--type agent` 空 `--agent` 默认 bibit。

- [ ] **Step 1: Write failing lifecycle tests**

Create `internal/lifecycle/agent_test.go`：

```go
package lifecycle

import (
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestInstallAgentsDefaultsBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentBibit}}
	agents, err := installAgents(cli.Flags{Type: "agent", Agent: ""}, snapshot)
	if err != nil || len(agents) != 1 || agents[0] != domain.AgentBibit {
		t.Fatalf("installAgents() = %v, %v", agents, err)
	}
}

func TestInstallAgentsRejectsNonBibitForAgentType(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{domain.AgentCodex, domain.AgentBibit}}
	if _, err := installAgents(cli.Flags{Type: "agent", Agent: "codex"}, snapshot); err == nil {
		t.Fatal("installAgents() error = nil, want unsupported agent")
	}
}
```

> 说明：`installAgents`/`uninstallAgents` 需在 `install.go`/`uninstall.go` 中增加对 `--type agent` 且非 bibit 的拒绝分支（而非仅在 CLI 层校验），使 lifecycle 单测可独立覆盖该约束。

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/lifecycle -run 'Test(InstallAgentsDefaultsBibit|InstallAgentsRejectsNonBibit)ForAgentType'`
Expected: FAIL（默认/拒绝逻辑尚未实现）。

- [ ] **Step 3: Implement lifecycle wiring**

Modify `internal/lifecycle/dependencies.go` 的 `AdapterRegistry`：

```go
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}
```

Create `internal/lifecycle/install_agent_power.go`：

```go
package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"

	"apm/internal/cache"
	"apm/internal/cli"
	"apm/internal/domain"
	"apm/internal/transaction"
)

// stageAgent 按“缓存、Cloud、缓存”的既定顺序准备 Agent staging。
func (s *InstallService) stageAgent(ref domain.PowerRef) (string, error) {
	staging, err := s.cache.StageAgent(ref)
	if err == nil {
		return staging, nil
	}
	if !errors.Is(err, cache.ErrMiss) {
		return "", fmt.Errorf("fetch install power %+v from cache: %w", ref, err)
	}
	if err := s.cloud.DownloadAgent(ref); err != nil {
		return "", fmt.Errorf("get install power %+v from Cloud: %w", ref, err)
	}
	staging, err = s.cache.StageAgent(ref)
	if err != nil {
		return "", fmt.Errorf("fetch downloaded install power %+v from cache: %w", ref, err)
	}
	return staging, nil
}

// installAgentPower 先核对本地清单，再准备 staging 并交由 Agent 事务完成发布和结果分类。
func (s *InstallService) installAgentPower(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error) {
	resolvedAdapter, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("resolve install adapter for agent %q: %w", agent, err)
	}

	previous, installed, err := s.config.Find(agent, ref.Type, ref.Name)
	if err != nil {
		return cli.InstallItem{}, fmt.Errorf("find installed power %+v for agent %q: %w", ref, agent, err)
	}
	if installed && previous.Version == ref.Version {
		return cli.InstallItem{Agent: agent, Power: ref, Outcome: cli.InstallOutcomeUnchanged}, nil
	}

	staging, err := s.stageAgent(ref)
	if err != nil {
		return cli.InstallItem{}, err
	}

	request := transaction.AgentInstallRequest{
		Agent:       agent,
		Power:       ref,
		StagingRoot: staging,
		Adapter:     resolvedAdapter,
	}
	if installed {
		request.Previous = &previous
	}
	if err := s.transactions.InstallAgent(ctx, request); err != nil {
		return cli.InstallItem{}, fmt.Errorf("install power %+v for agent %q: %w", ref, agent, err)
	}

	outcome := cli.InstallOutcomeInstalled
	if installed {
		if semver.MustParse(ref.Version).GreaterThan(semver.MustParse(previous.Version)) {
			outcome = cli.InstallOutcomeUpgraded
		} else {
			outcome = cli.InstallOutcomeDowngraded
		}
	}
	return cli.InstallItem{Agent: agent, Power: ref, Outcome: outcome}, nil
}
```

Create `internal/lifecycle/uninstall_agent_power.go`：

```go
package lifecycle

import (
	"context"
	"fmt"

	"apm/internal/domain"
	"apm/internal/transaction"
)

// uninstallAgentPower 解析 Agent Adapter，并将文件型卸载交给事务层。
func (s *UninstallService) uninstallAgentPower(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (transaction.UninstallResult, error) {
	resolved, err := s.registry.ResolveAgent(agent)
	if err != nil {
		return transaction.UninstallResult{}, fmt.Errorf("resolve uninstall adapter for agent %q: %w", agent, err)
	}
	return s.transactions.UninstallAgent(ctx, transaction.AgentUninstallRequest{Agent: agent, Power: ref, Adapter: resolved})
}
```

Modify `internal/lifecycle/install_agent.go` 的 `installForAgent` switch 追加：

```go
	case domain.PowerTypeAgent:
		return s.installAgentPower(ctx, agent, ref)
```

Modify `internal/lifecycle/uninstall_agent.go` 的 `uninstallForAgent` switch 追加：

```go
	case domain.PowerTypeAgent:
		transactionResult, err = s.uninstallAgentPower(ctx, agent, ref)
```

Modify `internal/lifecycle/install.go` 的 `installAgents`：

```go
func installAgents(flags cli.Flags, snapshot domain.AgentSnapshot) ([]domain.AgentID, error) {
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent != "" && domain.AgentID(flags.Agent) != domain.AgentBibit {
		return nil, fmt.Errorf("install power type %q only supports agent %q", domain.PowerTypeAgent, domain.AgentBibit)
	}
	target := domain.AgentID(flags.Agent)
	if flags.Type == string(domain.PowerTypeAgent) && flags.Agent == "" {
		target = domain.AgentBibit
	}
	// 其余逻辑保持不变：target == all 时取快照，否则校验快照包含 target。
	...
}
```

Modify `internal/lifecycle/uninstall.go` 的 `uninstallAgents`，同样在函数头部追加等价于 `installAgents` 的 `--type agent` 拒绝与默认逻辑（把错误文案中的 `install` 换成 `uninstall`）。

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/lifecycle`
Expected: PASS（若其它测试因 `AdapterRegistry` 新接口需更新 fake，同步补齐 `ResolveAgent` 到 `*_test_helpers_test.go` 里的 fake registry）。

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle
git commit -m "feat(lifecycle): add agent install/uninstall and bibit default"
```

---

### Task 9: CLI — `--type agent` 白名单与 `--agent` 校验

**Files:**
- Modify: `internal/cli/flags.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/flags_test.go`（或既有 flags 测试）

**Interfaces:**
- Consumes: 无（用字符串字面量，与既有 `validatePowerType` 一致）。
- Produces: `validateCommon` 支持 `--type agent`；`validateAgentTypeFlags`。

- [ ] **Step 1: Write failing CLI tests**

在 `internal/cli` 的 flags 测试中追加：

```go
func TestValidateCommonAgentType(t *testing.T) {
	if err := validateCommon(Flags{Type: "agent", Agent: ""}); err != nil {
		t.Fatalf("empty agent error = %v, want nil", err)
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "bibit"}); err != nil {
		t.Fatalf("bibit agent error = %v, want nil", err)
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "codex"}); err == nil {
		t.Fatal("codex agent error = nil, want error")
	}
	if err := validateCommon(Flags{Type: "agent", Agent: "all"}); err == nil {
		t.Fatal("all agent error = nil, want error")
	}
}

func TestValidatePowerTypeAcceptsAgent(t *testing.T) {
	if err := validatePowerType(Flags{Type: "agent"}); err != nil {
		t.Fatalf("validatePowerType(agent) error = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -count=1 ./internal/cli -run 'Test(ValidateCommonAgentType|ValidatePowerTypeAcceptsAgent)'`
Expected: FAIL。

- [ ] **Step 3: Implement CLI validation**

Modify `internal/cli/flags.go` 的 `validateCommon`：

```go
func validateCommon(flags Flags) error {
	if flags.Type == "agent" {
		return validateAgentTypeFlags(flags)
	}
	if strings.TrimSpace(flags.Agent) == "" {
		return errors.New("--agent is required")
	}
	return validatePowerType(flags)
}

// validateAgentTypeFlags 约束 agent power 仅适用于 Bibit；空 --agent 由 lifecycle 默认 bibit。
func validateAgentTypeFlags(flags Flags) error {
	if err := validatePowerType(flags); err != nil {
		return err
	}
	switch flags.Agent {
	case "", "bibit":
		return nil
	default:
		return fmt.Errorf("--type agent only supports --agent bibit, got %q", flags.Agent)
	}
}
```

Modify `validatePowerType` 白名单与错误文案：

```go
	switch flags.Type {
	case "", "skill", "plugin", "mcp", "agent":
		return nil
	default:
		return fmt.Errorf("invalid --type %q: must be skill, plugin, mcp, or agent", flags.Type)
	}
```

Modify `internal/cli/root.go` 的 `bindTypeFlag`：

```go
	command.Flags().StringVar(&flags.Type, "type", "", "power type: skill, plugin, mcp, or agent")
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 ./internal/cli`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat(cli): accept --type agent with bibit-only agent rule"
```

---

### Task 10: 文档、全量门禁与 Changelog

**Files:**
- Modify: `BUILD.md`（Agent/Power 矩阵加 agent 行、示例命令）。
- Modify: `ivory-tower/project-overview.md`（Power 矩阵加 agent）。
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/*.mmd`（涉及 cloud/cache/adapter/lifecycle 处补充 agent，按需）。
- Modify: `docs/Changelog.md`（仅在 Tasks 1–9 全绿后追加）。

**Interfaces:**
- Consumes: 全部前序任务。
- Produces: 文档与最终验证证据。

- [ ] **Step 1: Update current docs**

在 `BUILD.md` 的 Agent/Power 能力矩阵中为 Bibit 增加 `agent` 行，说明：Cloud `agents/<name>/<version>/agent.yaml`、安装 `<bibit根>/agents/<id>/agent.yaml`、`--type agent` 默认 `--agent bibit`。`ivory-tower/project-overview.md` 同步；C4 图按需在 cloud-client / cache-manager / adapter-registry / core-lifecycle 中补 agent 分支。

- [ ] **Step 2: Run complete pre-review gates**

Run: `test -z "$(gofmt -l cmd internal)"`
Run: `go test -count=1 ./...`
Run: `go test -race -count=1 ./...`
Run: `go vet ./...`
Run: `verify_dir="$(mktemp -d)"; go build -o "$verify_dir/apm" ./cmd/apm; test -x "$verify_dir/apm"`
Run: `git diff --check`
Expected: 全部 exit 0。

- [ ] **Step 3: Commit documentation**

```bash
git add BUILD.md ivory-tower
git commit -m "docs(agent): document agent power across build and overview"
```

- [ ] **Step 4: Add one Changelog entry after review passes**

在 `docs/Changelog.md` 顶部追加一条请求级条目，列出 Tasks 1–10 的提交，包含：用户需求（新增 Bibit 专属 agent power、修正 Bibit 根目录）、完成结果、验证与限制（fake-bin/home 隔离、不迁移旧 cache/journal、Cloud 资源对齐由其管理方保证），并单独提交：

```bash
git add docs/Changelog.md
git commit -m "docs(changelog): record bibit-only agent power"
```

---

## Self-Review 记录

- Spec 覆盖：领域模型（Task 1）、Cloud（Task 3）、缓存（Task 4）、Adapter（Task 5）、事务/恢复（Task 6）、配置清单（Task 7）、生命周期与 CLI 规则（Task 8/9）、Bibit 根修正（Task 2）、文档与门禁（Task 10）。`fix` 通过 filesystem driver 复用（Task 6 放宽校验）天然支持，无需单独任务。
- 类型一致性：`adapter.AgentAdapter`、`adapter.AgentInstallResult`、`transaction.AgentInstallRequest/AgentUninstallRequest`、`cache.PowerCache.StageAgent`、`cloud.Client.DownloadAgent`、`AdapterRegistry.ResolveAgent` 在 Task 3–8 中命名一致。
- 占位符：无 TBD/TODO；每个代码步骤给出完整代码。

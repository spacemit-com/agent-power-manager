# Local Cloud and Versioned Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现开发期本地 Cloud、版本化文件系统缓存和 staging 物化，使确定版本的 Skill 支持 `Fetch → miss → Get → Fetch`。

**Architecture:** `PowerRef` 统一携带类别、名称和版本；Cloud 扫描本地 fixture，并经缓存文件系统内的临时目录原子发布目标版本。Cache 在每次有效 Fetch 开头清理 staging，经状态文件系统内的临时目录将版本化缓存原子物化为无版本层 staging；Cloud 与 Cache 共用 `internal/utils/filesystem.CopyDir`，但分别拥有路径、错误和原子发布职责。

**Tech Stack:** Go 1.26.5、标准库 `os`/`io`/`path/filepath`/`sort`/`errors`/`testing`，不增加第三方依赖。

## Global Constraints

- 仅实现 Skill；Plugin/MCP 不扫描、不获取，Cloud 返回 `ErrNotFound`，Cache 返回 `ErrMiss`。
- `PowerRef.Version` 在 Cloud `Get` 和 Cache `Fetch` 前必须非空；空版本返回普通参数错误。
- Cloud `List()` 返回带版本的全部 `PowerRef`，不接受 Query，不筛选、不打印。
- 本 feature 不串联 CLI、lifecycle、Adapter、Transaction 或 logging。
- Cloud 缓存布局为 `<cache-root>/skills/<name>/<version>`；staging 布局为 `<state-root>/staging/skills/<name>`。
- Get 和 Fetch 使用目标文件系统内的临时目录与原子 rename；不实现 checksum、内容寻址或内容复验。
- 不校验 Power 名称、版本语义或复制节点类型，默认开发期资源合规。
- 修改环境变量的测试不得调用 `t.Parallel()`。
- 重要导出对象和方法使用中文 GoDoc。
- 设计依据为 [spec.md](spec.md)，路径优先级继续遵循 [ADR-005](../../adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/domain/power.go` | 合并 Power 版本字段并保留空 Power 占位。 |
| `internal/domain/domain_test.go` | 验证共享领域类型的新形态。 |
| `internal/paths/paths.go` | 提供 cache/staging 及其临时路径。 |
| `internal/paths/paths_test.go` | 验证临时目录与目标目录处于相同路径根。 |
| `internal/utils/filesystem/copy.go` | 递归复制目录并保留权限位。 |
| `internal/utils/filesystem/copy_test.go` | 验证复制内容、子目录和权限。 |
| `internal/cloud/client.go` | 定义无 Query 的 List/Get 接口。 |
| `internal/cloud/errors.go` | 定义 `ErrNotFound`。 |
| `internal/cloud/local_client.go` | 扫描本地资源并原子写入缓存。 |
| `internal/cloud/local_client_test.go` | 验证 List/Get、错误和幂等语义。 |
| `internal/cloud/local_cache_integration_test.go` | 验证真实 fixture 的 `Fetch → Get → Fetch`。 |
| `internal/cloud/model.go` | 删除旧 Query/PowerMeta 占位。 |
| `internal/cloud/http_client.go` | 删除无功能 HTTPClient 占位。 |
| `internal/cache/cache.go` | 定义只含 Path 的 Content 和 Fetch 接口。 |
| `internal/cache/errors.go` | 定义 `ErrMiss`。 |
| `internal/cache/filesystem.go` | 从版本化缓存原子物化 staging。 |
| `internal/cache/filesystem_test.go` | 验证 miss、清理、复制和返回路径。 |
| `internal/cache/content_addressed.go` | 删除旧 ContentAddressed/Put 占位。 |
| `docs/Changelog.md` | 记录本次行为变更、提交与验证。 |

---

### Task 1: Consolidate Power version metadata

**Files:**
- Modify: `internal/domain/power.go`
- Modify: `internal/domain/domain_test.go`

**Interfaces:**
- Produces: `domain.PowerRef{Name string, Type PowerType, Version string}`.
- Produces: empty placeholder `domain.Power struct{}`.
- Produces: `domain.InstalledPower{Ref PowerRef, InstalledAt time.Time}`.

- [ ] **Step 1: Write the failing domain model test**

Replace `internal/domain/domain_test.go` with:

```go
package domain_test

import (
	"testing"
	"time"

	"apm/internal/domain"
)

func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	installed := domain.InstalledPower{Ref: ref, InstalledAt: time.Unix(1, 0)}
	_ = domain.Power{}
	result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
	if installed.Ref != ref || installed.Ref.Version != "1.0.0" || len(result.Items) != 1 {
		t.Fatal("共享类型未保存输入")
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
go test ./internal/domain
```

Expected: compilation fails because `PowerRef.Version` is absent and `InstalledPower` still requires the old independent version model.

- [ ] **Step 3: Implement the consolidated model**

Change the affected declarations in `internal/domain/power.go` to:

```go
// PowerRef 表示能力的类别、名称和可选版本。
type PowerRef struct {
	Name    string
	Type    PowerType
	Version string
}

// Power 是后续 Adapter 安装参数的占位类型。
type Power struct{}

// InstalledPower 表示已安装能力的元数据。
type InstalledPower struct {
	Ref         PowerRef
	InstalledAt time.Time
}
```

- [ ] **Step 4: Run the domain and full compile tests**

Run:

```bash
go test ./internal/domain ./internal/adapter ./internal/transaction
```

Expected: PASS; Adapter and Transaction continue compiling with the empty `domain.Power` placeholder.

- [ ] **Step 5: Commit the domain model**

```bash
git add internal/domain/power.go internal/domain/domain_test.go
git commit -m "refactor(domain): consolidate power version metadata"
```

---

### Task 2: Add cache and staging path boundaries

**Files:**
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: existing `paths.CachePath() (string, error)` and `paths.StateDir() (string, error)`.
- Produces: `paths.CacheTempPath() (string, error)`.
- Produces: `paths.StagingPath() (string, error)`.
- Produces: `paths.StagingTempPath() (string, error)`.

- [ ] **Step 1: Write the failing path test**

Append to `internal/paths/paths_test.go`:

```go
func TestCacheAndStagingTemporaryPathsStayWithTheirTargets(t *testing.T) {
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	cacheTemp, err := paths.CacheTempPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm", ".tmp"); cacheTemp != want {
		t.Fatalf("CacheTempPath() = %q, want %q", cacheTemp, want)
	}

	staging, err := paths.StagingPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateHome, ".apm", "staging"); staging != want {
		t.Fatalf("StagingPath() = %q, want %q", staging, want)
	}

	stagingTemp, err := paths.StagingTempPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(stateHome, ".apm", ".tmp", "staging"); stagingTemp != want {
		t.Fatalf("StagingTempPath() = %q, want %q", stagingTemp, want)
	}
}
```

- [ ] **Step 2: Run the focused test and verify it fails**

Run:

```bash
go test ./internal/paths -run TestCacheAndStagingTemporaryPathsStayWithTheirTargets -count=1
```

Expected: compilation fails because the three path functions do not exist.

- [ ] **Step 3: Implement the path functions**

Add to `internal/paths/paths.go`:

```go
// CacheTempPath 返回 Cloud 获取资源时使用的缓存临时目录。
func CacheTempPath() (string, error) {
	cache, err := CachePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, ".tmp"), nil
}

// StagingPath 返回当前 Power 的 staging 根目录。
func StagingPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "staging"), nil
}

// StagingTempPath 返回构建 staging 时使用的临时目录。
func StagingTempPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, ".tmp", "staging"), nil
}
```

- [ ] **Step 4: Run all path tests**

Run:

```bash
go test ./internal/paths -count=1
```

Expected: PASS, including existing `XDG_CACHE_HOME > APM_HOME > system default` tests.

- [ ] **Step 5: Commit the paths**

```bash
git add internal/paths/paths.go internal/paths/paths_test.go
git commit -m "feat(paths): add cache and staging paths"
```

---

### Task 3: Add the shared recursive directory copier

**Files:**
- Create: `internal/utils/filesystem/copy.go`
- Create: `internal/utils/filesystem/copy_test.go`

**Interfaces:**
- Consumes: a readable source directory and destination path.
- Produces: `filesystem.CopyDir(source, destination string) error`.
- Preserves: directory and regular-file permission bits; does not preserve timestamps, owner, or xattrs.

- [ ] **Step 1: Write the failing copy test**

Create `internal/utils/filesystem/copy_test.go`:

```go
package filesystem_test

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/utils/filesystem"
)

func TestCopyDirCopiesTreeContentAndPermissions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "nested", "destination")
	if err := os.MkdirAll(filepath.Join(source, "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(source, "child"), 0o710); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "root.txt"), []byte("root"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "child", "nested.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := filesystem.CopyDir(source, destination); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{
		filepath.Join(destination, "root.txt"):            "root",
		filepath.Join(destination, "child", "nested.txt"): "nested",
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	for path, want := range map[string]os.FileMode{
		destination:                         0o750,
		filepath.Join(destination, "child"): 0o710,
		filepath.Join(destination, "root.txt"): 0o640,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s mode = %o, want %o", path, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the utility test and verify it fails**

Run:

```bash
go test ./internal/utils/filesystem -count=1
```

Expected: compilation fails because the package and `CopyDir` do not exist.

- [ ] **Step 3: Implement the minimal copier**

Create `internal/utils/filesystem/copy.go`:

```go
// Package filesystem 提供内部文件系统工具。
package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CopyDir 递归复制目录内容并保留权限位。
func CopyDir(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("stat source directory %q: %w", source, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source %q is not a directory", source)
	}
	if err = os.MkdirAll(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create destination directory %q: %w", destination, err)
	}

	entries, err := os.ReadDir(source)
	if err != nil {
		return fmt.Errorf("read source directory %q: %w", source, err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			if err = CopyDir(sourcePath, destinationPath); err != nil {
				return err
			}
			continue
		}
		if err = copyFile(sourcePath, destinationPath); err != nil {
			return err
		}
	}
	if err = os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set destination directory mode %q: %w", destination, err)
	}
	return nil
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", source, err)
	}
	defer input.Close()

	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("stat source file %q: %w", source, err)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", destination, err)
	}
	if _, err = io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy %q to %q: %w", source, destination, err)
	}
	if err = output.Close(); err != nil {
		return fmt.Errorf("close destination file %q: %w", destination, err)
	}
	if err = os.Chmod(destination, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set destination file mode %q: %w", destination, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the utility tests**

Run:

```bash
go test ./internal/utils/filesystem -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the utility**

```bash
git add internal/utils/filesystem/copy.go internal/utils/filesystem/copy_test.go
git commit -m "feat(utils): add recursive directory copy"
```

---

### Task 4: Implement the local Cloud client

**Files:**
- Modify: `internal/cloud/client.go`
- Create: `internal/cloud/errors.go`
- Create: `internal/cloud/local_client.go`
- Create: `internal/cloud/local_client_test.go`
- Delete: `internal/cloud/model.go`
- Delete: `internal/cloud/http_client.go`

**Interfaces:**
- Consumes: `paths.CachePath()`, `paths.CacheTempPath()`, `filesystem.CopyDir`, and an absolute resource root.
- Produces: `cloud.Client.List() ([]domain.PowerRef, error)`.
- Produces: `cloud.Client.Get(domain.PowerRef) error`.
- Produces: `cloud.NewLocalClient(string) (*LocalClient, error)` and `cloud.ErrNotFound`.

- [ ] **Step 1: Write failing constructor and List tests**

Create `internal/cloud/local_client_test.go` with tests that:

```go
package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestNewLocalClientRejectsRelativeResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := cloud.NewLocalClient("resources"); err == nil {
		t.Fatal("NewLocalClient() error = nil, want relative-path error")
	}
}

func TestLocalClientListsCommittedSkillFixture(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "0.0.1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListSortsAndIgnoresInvalidSkillDirectories(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	writeSkill := func(name, version string, valid bool) {
		dir := filepath.Join(root, "skills", name, version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if valid {
			if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeSkill("zeta", "2.0.0", true)
	writeSkill("alpha", "1.1.0", true)
	writeSkill("alpha", "1.0.0", true)
	writeSkill("ignored", "1.0.0", false)

	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "1.0.0"},
		{Name: "alpha", Type: domain.PowerTypeSkill, Version: "1.1.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "2.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestLocalClientListRequiresReadableResourceRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "missing")
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.List(); err == nil {
		t.Fatal("List() error = nil, want missing resource-root error")
	}
}

func TestLocalClientListReturnsEmptyWithoutSkillsDirectory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
}
```

- [ ] **Step 2: Run List tests and verify they fail**

Run:

```bash
go test ./internal/cloud -run 'Test(NewLocalClient|LocalClientList)' -count=1
```

Expected: compilation fails because `NewLocalClient` and the new Client signature do not exist.

- [ ] **Step 3: Add Get error and atomic-copy tests**

Extend `internal/cloud/local_client_test.go` with:

```go
func TestLocalClientGetValidatesRefAndNotFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Get(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}); err == nil || errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("empty version error = %v, want non-sentinel parameter error", err)
	}
	for _, ref := range []domain.PowerRef{
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "1.0.0"},
		{Name: "missing", Type: domain.PowerTypeSkill, Version: "1.0.0"},
	} {
		if err = client.Get(ref); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("Get(%#v) error = %v, want ErrNotFound", ref, err)
		}
	}
}

func TestLocalClientGetCopiesAtomicallyAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	source := filepath.Join(root, "skills", "demo", "1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skill-card.md"), []byte("card"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	if err = client.Get(ref); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cacheHome, "apm", "skills", "demo", "1.0.0")
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if _, err = os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(cacheHome, "apm", ".tmp")); !os.IsNotExist(err) {
		t.Fatalf("cache temp remains: %v", err)
	}
	if err = os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err = client.Get(ref); err != nil {
		t.Fatalf("idempotent Get() error = %v", err)
	}
}
```

- [ ] **Step 4: Implement the Cloud interface and sentinel**

Change `internal/cloud/client.go` to the approved interface, create `errors.go`, delete `model.go` and `http_client.go`, then implement `LocalClient`. The core declarations must be:

```go
var ErrNotFound = errors.New("cloud power not found")

type LocalClient struct {
	resourcesRoot string
	cacheRoot     string
	cacheTempRoot string
}

func NewLocalClient(resourcesRoot string) (*LocalClient, error)
func (c *LocalClient) List() ([]domain.PowerRef, error)
func (c *LocalClient) Get(ref domain.PowerRef) (resultErr error)
```

Implement `List` with `os.ReadDir`, a regular `SKILL.md` check, and `sort.Slice`. Implement `Get` in this exact order: reject empty version; map non-Skill to `ErrNotFound`; remove `cacheTempRoot`; return success if the final cache path already exists; map absent/invalid source Skill to `ErrNotFound`; copy to the temporary version path; create the final parent; rename the temporary version directory; remove `cacheTempRoot` on every exit. Use `errors.Join` in the deferred cleanup so cleanup failures are not discarded.

The final and temporary paths are:

```go
final := filepath.Join(c.cacheRoot, "skills", ref.Name, ref.Version)
temporary := filepath.Join(c.cacheTempRoot, "skills", ref.Name, ref.Version)
source := filepath.Join(c.resourcesRoot, "skills", ref.Name, ref.Version)
```

- [ ] **Step 5: Run Cloud tests**

Run:

```bash
go test ./internal/cloud -count=1
```

Expected: PASS for List ordering, fixture discovery, parameter errors, `ErrNotFound`, complete copy, cleanup, and idempotency.

- [ ] **Step 6: Commit the Cloud implementation**

```bash
git add internal/cloud
git commit -m "feat(cloud): add local resource client"
```

---

### Task 5: Implement the versioned filesystem Cache

**Files:**
- Modify: `internal/cache/cache.go`
- Create: `internal/cache/errors.go`
- Create: `internal/cache/filesystem.go`
- Create: `internal/cache/filesystem_test.go`
- Delete: `internal/cache/content_addressed.go`

**Interfaces:**
- Consumes: `paths.CachePath()`, `paths.StagingPath()`, `paths.StagingTempPath()`, and `filesystem.CopyDir`.
- Produces: `cache.NewFilesystem() (*FilesystemCache, error)`.
- Produces: `cache.PowerCache.Fetch(domain.PowerRef) (Content, error)` and `cache.ErrMiss`.

- [ ] **Step 1: Write failing miss and cleanup tests**

Create `internal/cache/filesystem_test.go`:

```go
package cache_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/domain"
)

func TestFilesystemCacheRejectsEmptyVersion(t *testing.T) {
	setCacheEnvironment(t)
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Fetch(domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill})
	if err == nil || errors.Is(err, cache.ErrMiss) {
		t.Fatalf("Fetch() error = %v, want non-sentinel parameter error", err)
	}
}

func TestFilesystemCacheMissClearsOldStaging(t *testing.T) {
	stateHome, _ := setCacheEnvironment(t)
	old := filepath.Join(stateHome, ".apm", "staging", "old.txt")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Fetch(domain.PowerRef{Name: "missing", Type: domain.PowerTypeSkill, Version: "1.0.0"})
	if !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("Fetch() error = %v, want ErrMiss", err)
	}
	if _, err = os.Stat(filepath.Join(stateHome, ".apm", "staging")); !os.IsNotExist(err) {
		t.Fatalf("old staging remains: %v", err)
	}
}

func setCacheEnvironment(t *testing.T) (stateHome, cacheHome string) {
	t.Helper()
	stateHome = t.TempDir()
	cacheHome = t.TempDir()
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	return stateHome, cacheHome
}
```

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
go test ./internal/cache -count=1
```

Expected: compilation fails because `NewFilesystem` and `ErrMiss` do not exist.

- [ ] **Step 3: Add the successful Fetch test**

Append to `internal/cache/filesystem_test.go`:

```go
func TestFilesystemCacheFetchesVersionWithoutStagingVersionLayer(t *testing.T) {
	stateHome, cacheHome := setCacheEnvironment(t)
	source := filepath.Join(cacheHome, "apm", "skills", "demo", "1.0.0")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "skill-card.md"), []byte("card"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	content, err := store.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(stateHome, ".apm", "staging", "skills", "demo")
	if content.Path != wantPath {
		t.Fatalf("Content.Path = %q, want %q", content.Path, wantPath)
	}
	for _, name := range []string{"SKILL.md", "skill-card.md"} {
		if _, err = os.Stat(filepath.Join(wantPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = os.Stat(filepath.Join(stateHome, ".apm", ".tmp", "staging")); !os.IsNotExist(err) {
		t.Fatalf("staging temp remains: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(source, "SKILL.md")); err != nil || string(got) != "skill" {
		t.Fatalf("cache source changed: %q, %v", got, err)
	}
}
```

Append the unsupported-type test:

```go
func TestFilesystemCacheUnsupportedTypesMissAndClearStaging(t *testing.T) {
	for _, powerType := range []domain.PowerType{domain.PowerTypePlugin, domain.PowerTypeMCP} {
		t.Run(string(powerType), func(t *testing.T) {
			stateHome, _ := setCacheEnvironment(t)
			old := filepath.Join(stateHome, ".apm", "staging", "old.txt")
			if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			store, err := cache.NewFilesystem()
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Fetch(domain.PowerRef{Name: "demo", Type: powerType, Version: "1.0.0"})
			if !errors.Is(err, cache.ErrMiss) {
				t.Fatalf("Fetch() error = %v, want ErrMiss", err)
			}
			if _, err = os.Stat(filepath.Join(stateHome, ".apm", "staging")); !os.IsNotExist(err) {
				t.Fatalf("old staging remains: %v", err)
			}
		})
	}
}
```

- [ ] **Step 4: Implement the Cache interface and filesystem cache**

Change `cache.go`, create `errors.go`, delete `content_addressed.go`, and implement these declarations:

```go
var ErrMiss = errors.New("power cache miss")

type Content struct {
	Path string
}

type PowerCache interface {
	Fetch(domain.PowerRef) (Content, error)
}

type FilesystemCache struct {
	cacheRoot      string
	stagingPath   string
	stagingTempPath string
}

func NewFilesystem() (*FilesystemCache, error)
func (c *FilesystemCache) Fetch(ref domain.PowerRef) (content Content, resultErr error)
```

Implement `Fetch` in this exact order: reject empty version; remove `stagingPath`; remove `stagingTempPath`; defer removal of `stagingTempPath` with `errors.Join`; map non-Skill to `ErrMiss`; stat the exact cache directory and map only `os.ErrNotExist` to `ErrMiss`; copy it to `filepath.Join(stagingTempPath, "skills", ref.Name)`; create `filepath.Dir(stagingPath)`; rename `stagingTempPath` to `stagingPath`; return the versionless target path. Wrap other filesystem errors with operation and path context.

- [ ] **Step 5: Run Cache and dependency tests**

Run:

```bash
go test ./internal/cache ./internal/lifecycle -count=1
```

Expected: PASS; lifecycle aliases compile against the new Cloud/Cache interfaces without implementing orchestration.

- [ ] **Step 6: Commit the Cache implementation**

```bash
git add internal/cache
git commit -m "feat(cache): add versioned filesystem cache"
```

---

### Task 6: Verify the complete local Cloud/Cache flow and record it

**Files:**
- Create: `internal/cloud/local_cache_integration_test.go`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: `cloud.NewLocalClient`, `cache.NewFilesystem`, `cloud.Client.Get`, `cache.PowerCache.Fetch`, and the committed fixture.
- Verifies: exact `Fetch → ErrMiss → Get → Fetch` behavior under isolated APM/cache roots.

- [ ] **Step 1: Write the end-to-end integration test**

Create `internal/cloud/local_cache_integration_test.go` in package `cloud_test`:

```go
package cloud_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cache"
	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestLocalCloudAndCacheFetchGetFetch(t *testing.T) {
	realHome := t.TempDir()
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", realHome)
	t.Setenv("APM_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	resourcesRoot, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	client, err := cloud.NewLocalClient(resourcesRoot)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cache.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	ref := domain.PowerRef{Name: "foo-before-summary", Type: domain.PowerTypeSkill, Version: "0.0.1"}

	if _, err = store.Fetch(ref); !errors.Is(err, cache.ErrMiss) {
		t.Fatalf("first Fetch() error = %v, want ErrMiss", err)
	}
	if err = client.Get(ref); err != nil {
		t.Fatal(err)
	}
	content, err := store.Fetch(ref)
	if err != nil {
		t.Fatal(err)
	}

	cachePath := filepath.Join(cacheHome, "apm", "skills", ref.Name, ref.Version)
	stagingPath := filepath.Join(stateHome, ".apm", "staging", "skills", ref.Name)
	if content.Path != stagingPath {
		t.Fatalf("Content.Path = %q, want %q", content.Path, stagingPath)
	}
	for _, root := range []string{cachePath, stagingPath} {
		for _, name := range []string{"SKILL.md", "skill-card.md"} {
			if _, err = os.Stat(filepath.Join(root, name)); err != nil {
				t.Fatalf("stat %s/%s: %v", root, name, err)
			}
		}
	}
	if entries, err := os.ReadDir(realHome); err != nil || len(entries) != 0 {
		t.Fatalf("HOME touched: entries=%v err=%v", entries, err)
	}
}
```

- [ ] **Step 2: Run the integration test**

Run:

```bash
go test ./internal/cloud -run TestLocalCloudAndCacheFetchGetFetch -count=1
```

Expected: PASS and no writes under the test's real HOME.

- [ ] **Step 3: Run formatting and complete verification**

Run:

```bash
gofmt -w internal/domain internal/paths internal/utils internal/cloud internal/cache
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0; all test packages report `ok`.

- [ ] **Step 4: Commit the integration test**

```bash
git add internal/cloud/local_cache_integration_test.go
git commit -m "test: cover local cloud cache flow"
```

- [ ] **Step 5: Add one Changelog entry for this implementation request**

Add a new `2026-08-06` entry immediately below `# Changelog` using `~/.codex/docs/Changelog.md`. Classify it as `新增功能`; describe the local Skill catalog, atomic versioned cache, clean versionless staging, defensive empty-version error, and the boundary excluding CLI/lifecycle/Adapter/HTTP. Populate `相关提交` by copying the exact output of:

```bash
git log --reverse --format='- `%h %s`' 56d9c84^..HEAD
```

Record the four final verification commands and the focused integration test in `验证结果或限制`.

- [ ] **Step 6: Commit the completed Changelog**

```bash
git add docs/Changelog.md
git commit -m "docs: record local cloud cache implementation"
```

- [ ] **Step 7: Re-run final verification after the Changelog commit**

Run:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
git status --short
```

Expected: all commands exit 0 and `git status --short` prints nothing.


# APM_HOME Test Filesystem Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 APM 在 `APM_HOME` 非空时将已实现的本地路径和默认文件锁隔离到测试目录，为后续 Agent 和 Power 生命周期测试提供唯一的路径入口。

**Architecture:** `internal/paths` 在每次解析时优先读取非空 `APM_HOME`，否则保留 `os.UserHomeDir` 和 `os.UserCacheDir` 的生产语义。APM 状态根统一为 `<effective-home>/.apm`；隔离模式缓存固定为 `$APM_HOME/.cache/apm`。现有 `lock.NewDefault` 继续只消费 `paths.LockPath`。

**Tech Stack:** Go 1.26.5、标准库 `os`/`path/filepath`/`testing`、`gofrs/flock`。

## Global Constraints

- `APM_HOME` 仅供测试使用；不增加 CLI flag，不修改 Cobra help。
- `APM_HOME` 非空时直接使用；空字符串视为未定义并回退 HOME。
- 不校验、清理、绝对化或解析 `APM_HOME`；测试负责提供规范目录。
- 不修改进程的 `HOME` 或 `XDG_CACHE_HOME`，不缓存环境解析结果。
- 隔离模式的状态根为 `$APM_HOME/.apm`，缓存为 `$APM_HOME/.cache/apm` 且忽略 `XDG_CACHE_HOME`。
- 本 feature 不实现 Adapter、Config、Cache、Transaction 或 Lifecycle 的占位业务方法。
- 修改环境变量的测试不得调用 `t.Parallel()`。
- 路径决策以 [ADR-005](../../adr/ADR-005-isolate-test-filesystem-with-apm-home.md) 为准，设计语义见 [Project Overview](../../project-overview.md#test-filesystem-isolation)。

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/paths/paths.go` | 解析有效 HOME、状态根、lock 和缓存路径；保留其他路径占位。 |
| `internal/paths/paths_test.go` | 验证环境变量优先级和路径结果。 |
| `internal/lock/flock_test.go` | 验证默认 lock 的真实写入不越出隔离目录。 |
| `docs/Changelog.md` | 记录行为变更、相关提交、验证和当前边界。 |

---

### Task 1: Implement the centralized effective-home paths

**Files:**
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`
- Modify: `internal/lock/flock_test.go`

**Interfaces:**
- Consumes: `os.Getenv("APM_HOME")`, `os.UserHomeDir()`, `os.UserCacheDir()` and `lock.NewDefault() (Locker, error)`.
- Produces: `paths.HomeDir() (string, error)`, `paths.StateDir() (string, error)`, `paths.LockPath() (string, error)` and `paths.CachePath() (string, error)`.
- Preserves: `paths.JournalPath() string`, `paths.ManifestPath(domain.AgentID) string` and `paths.TransactionPath() string` as zero-value placeholders.

- [ ] **Step 1: Write failing precedence and path tests**

Replace `internal/paths/paths_test.go` with:

```go
package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"apm/internal/paths"
)

func TestHomeDirUsesAPMHomeWhenNonEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := t.TempDir()
	t.Setenv("APM_HOME", want)

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirFallsBackWhenAPMHomeIsEmpty(t *testing.T) {
	want := t.TempDir()
	t.Setenv("HOME", want)
	t.Setenv("APM_HOME", "")

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirFallsBackWhenAPMHomeIsUnset(t *testing.T) {
	want := t.TempDir()
	t.Setenv("HOME", want)
	unsetEnv(t, "APM_HOME")

	got, err := paths.HomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HomeDir() = %q, want %q", got, want)
	}
}

func TestHomeDirReturnsErrorWhenHomeIsUnavailable(t *testing.T) {
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", "")

	if _, err := paths.HomeDir(); err == nil {
		t.Fatal("HomeDir() error = nil, want home resolution error")
	}
}

func TestIsolatedStateLockAndCachePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	state, err := paths.StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".apm"); state != want {
		t.Fatalf("StateDir() = %q, want %q", state, want)
	}

	lockPath, err := paths.LockPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".apm", "apm.lock"); lockPath != want {
		t.Fatalf("LockPath() = %q, want %q", lockPath, want)
	}

	cache, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".cache", "apm"); cache != want {
		t.Fatalf("CachePath() = %q, want %q", cache, want)
	}
}

func TestCachePathUsesXDGWithoutAPMHome(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("APM_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	got, err := paths.CachePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cacheHome, "apm"); got != want {
		t.Fatalf("CachePath() = %q, want %q", got, want)
	}
}

func unsetEnv(t *testing.T, name string) {
	t.Helper()
	old, existed := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			if err := os.Setenv(name, old); err != nil {
				t.Errorf("restore %s: %v", name, err)
			}
			return
		}
		if err := os.Unsetenv(name); err != nil {
			t.Errorf("unset %s: %v", name, err)
		}
	})
}
```

Append to `internal/lock/flock_test.go`:

```go
func TestDefaultLockUsesAPMHomeWithoutTouchingHome(t *testing.T) {
	realHome := t.TempDir()
	isolationHome := filepath.Join(t.TempDir(), "sandbox")
	t.Setenv("HOME", realHome)
	t.Setenv("APM_HOME", isolationHome)

	locker, err := lock.NewDefault()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := locker.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err = unlock.Release(); err != nil {
		t.Fatal(err)
	}

	if _, err = os.Stat(filepath.Join(isolationHome, ".apm", "apm.lock")); err != nil {
		t.Fatalf("stat isolated lock: %v", err)
	}
	if _, err = os.Stat(filepath.Join(realHome, ".apm")); !os.IsNotExist(err) {
		t.Fatalf("real HOME was touched: %v", err)
	}
}
```

- [ ] **Step 2: Run the focused tests and verify the API is absent**

Run:

```bash
go test ./internal/paths ./internal/lock
```

Expected: compilation fails because `paths.HomeDir` and `paths.StateDir` are missing and the old `CachePath` has a different signature.

- [ ] **Step 3: Implement the minimal resolver**

Replace `internal/paths/paths.go` with:

```go
// Package paths 定义 APM 本地文件的默认路径。
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"apm/internal/domain"
)

const apmHomeEnv = "APM_HOME"

// HomeDir 返回 APM 使用的有效用户主目录。
func HomeDir() (string, error) {
	return resolveHome()
}

// StateDir 返回 APM 本地状态根目录。
func StateDir() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".apm"), nil
}

// LockPath 返回全局 APM 文件锁的默认路径。
func LockPath() (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "apm.lock"), nil
}

// JournalPath 返回操作日志路径。
//
// 当前为零值占位实现。
func JournalPath() string { return "" }

// ManifestPath 返回指定智能体的清单路径。
//
// 当前为零值占位实现。
func ManifestPath(domain.AgentID) string { return "" }

// CachePath 返回 APM 下载缓存目录。
func CachePath() (string, error) {
	if home := os.Getenv(apmHomeEnv); home != "" {
		return filepath.Join(home, ".cache", "apm"), nil
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	if cache == "" {
		return "", errors.New("resolve user cache directory: empty path")
	}
	return filepath.Join(cache, "apm"), nil
}

// TransactionPath 返回事务记录路径。
//
// 当前为零值占位实现。
func TransactionPath() string { return "" }

func resolveHome() (string, error) {
	if home := os.Getenv(apmHomeEnv); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("resolve home directory: empty path")
	}
	return home, nil
}
```

- [ ] **Step 4: Format and run the focused tests**

Run:

```bash
gofmt -w internal/paths/paths.go internal/paths/paths_test.go internal/lock/flock_test.go
go test -count=1 ./internal/paths ./internal/lock
```

Expected: both packages report `ok`; the lock test proves the test HOME has no `.apm` directory.

- [ ] **Step 5: Commit the behavior change**

```bash
git add internal/paths/paths.go internal/paths/paths_test.go internal/lock/flock_test.go
git commit -m "feat(paths): isolate test filesystem with APM_HOME"
```

---

### Task 2: Verify and record the completed behavior

**Files:**
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: the committed path and lock behavior from Task 1.
- Produces: repository-wide verification evidence and one reverse-chronological changelog entry.

- [ ] **Step 1: Confirm environment access is centralized**

Run:

```bash
rg -n 'UserHomeDir|UserCacheDir|Getenv\("APM_HOME"\)|LookupEnv\("APM_HOME"\)' internal --glob '*.go'
```

Expected: production matches occur only in `internal/paths/paths.go`; test matches may occur in `internal/paths/paths_test.go`.

- [ ] **Step 2: Run complete verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0; tested packages report `ok`; packages without tests report `[no test files]`.

- [ ] **Step 3: Collect exact related commits and add the changelog entry**

Run:

```bash
git log --reverse --format='- `%h %s`' 3320f3a^..HEAD
```

Add a new `2026-08-06` entry immediately below `# Changelog` using the template required by `~/.codex/docs/Changelog.md`. Use:

- 分类：`新增功能`
- 用户需求：增加仅供测试使用的 `APM_HOME`，使 APM 可在临时目录模拟 Agent 和 Power 状态而不影响用户目录。
- 相关提交：copy the exact lines printed by the command above.
- 完成结果：record effective HOME, `$APM_HOME/.apm`, `$APM_HOME/.cache/apm`, XDG override, and default-lock isolation.
- 验证：record the commands and actual successful results from Step 2.
- 限制：only paths and the existing lock are integrated now; Agent detection, install/uninstall, config, cache writes and transaction recovery remain with their owning features.

- [ ] **Step 4: Check and commit the changelog**

```bash
git diff --check
git diff -- docs/Changelog.md
git add docs/Changelog.md
git commit -m "docs(changelog): record APM_HOME isolation"
```

Expected: one new latest changelog entry whose related-commit lines exactly match Step 3.

- [ ] **Step 5: Verify final repository state**

```bash
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git status --short
```

Expected: tests, vet and build exit 0; `git status --short` prints nothing.

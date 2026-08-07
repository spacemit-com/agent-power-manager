# Agent Power 安装清单与无状态 Config Repository Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现按 Agent 独立、按 Power 类型固定分组的 JSON 安装清单，并让 lifecycle 后续只需依赖无状态 `List/Find/Set/Remove` Repository 接口。

**Architecture:** `domain.PowerGroups` 作为 config、lifecycle 和输出层共享的分组模型；`config.JSONRepository` 每次调用内部读取目标 Agent 的 JSON，`Set/Remove` 在外层全局锁假设下完成一次原子读改写，不保存跨调用状态。磁盘 DTO 与领域模型分离，组内 JSON 省略 type，读取时根据 `skills/plugins/mcps` 补全 `PowerRef.Type`。

**Tech Stack:** Go 1.26.5、标准库 `encoding/json`/`os`/`path/filepath`/`log/slog`、现有 `internal/logging`、标准 `testing`。

## Global Constraints

- 规格来源：`ivory-tower/features/005-agent-power-manifest-config/spec.md`；架构决策来源：`ivory-tower/adr/ADR-007-use-stateless-config-repository.md`。
- JSON 顶层固定输出 `agent`、`skills`、`plugins`、`mcps`；组内只输出 `name`、`version`；两空格缩进并保留末尾换行。
- Repository 只暴露 `List/Find/Set/Remove`；不得暴露 `Read/Write/Manifest/Release` 或保存跨调用状态。
- `List/Find` 不创建目录；`Set/Remove` 仅在清单实际变化时写文件。
- `Set` 只校验已知类型、非空名称、非空版本；`Find/Remove` 只校验已知类型和非空名称；不验证 semver、名称字符集或已有磁盘条目内容。
- Repository 不获取全局锁且不保证并发写安全；lifecycle 串联不属于本 feature。
- 清单目录权限为 `0700`，目标和同目录临时文件权限为 `0600`；写入必须执行文件同步、原子 rename 和父目录同步，不重试。
- config 只使用包级 `slog.Debug` 输出 verbose 诊断；不得直接打印或创建私有 logger。
- 不新增第三方依赖，不增加文件系统抽象、schema version、迁移、checksum、签名、加密或批量事务。
- 生产代码中的重要对象和方法保留简洁中文 GoDoc。
- 修改环境变量或全局默认 logger 的测试不得使用 `t.Parallel()`。

---

## File Map

| Path | Responsibility |
| --- | --- |
| `internal/domain/power.go` | 新增共享 `PowerGroups`，最终删除 `InstalledPower`。 |
| `internal/domain/domain_test.go` | 验证分组引用模型。 |
| `internal/paths/paths.go` | 提供 `ManifestPath(AgentID) (string, error)`。 |
| `internal/paths/paths_test.go` | 验证 HOME/APM_HOME 清单路径。 |
| `internal/config/repository.go` | 定义无状态 Repository 接口。 |
| `internal/config/errors.go` | 定义 `ErrInvalidPower`。 |
| `internal/config/manifest.go` | 定义私有 JSON DTO、转换和深拷贝。 |
| `internal/config/json_repository.go` | 实现查询、读改写、直接原子写入和 Debug 日志。 |
| `internal/config/json_repository_test.go` | 覆盖格式、语义、权限、失败和无状态行为。 |
| `internal/config/store.go` | 删除旧 Store 占位。 |
| `internal/config/json_store.go` | 删除旧 JSONStore 占位。 |
| `internal/config/atomic_write.go` | 删除独立 AtomicWrite 占位。 |
| `internal/lifecycle/dependencies.go` | 把占位依赖改为 config.Repository 别名，不串联。 |
| `internal/lifecycle/service.go` | 把已安装列表结果改为 PowerGroups。 |
| `docs/Changelog.md` | 功能完成后记录本次用户请求和提交。 |

---

### Task 1: Add PowerGroups and manifest paths

**Files:**
- Modify: `internal/domain/power.go:17-31`
- Modify: `internal/domain/domain_test.go:10-18`
- Modify: `internal/paths/paths.go:41-50`
- Modify: `internal/paths/paths_test.go:62-91`

**Interfaces:**
- Consumes: `domain.PowerType`、`domain.PowerRef`、`paths.StateDir() (string, error)`。
- Produces: `type domain.PowerGroups map[domain.PowerType][]domain.PowerRef`；`paths.ManifestPath(domain.AgentID) (string, error)`。

- [ ] **Step 1: Write failing domain and path tests**

在 domain 测试中增加：

```go
func TestPowerGroupsKeepTypedVersionedReferences(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{
		domain.PowerTypeSkill:  {ref},
		domain.PowerTypePlugin: {},
		domain.PowerTypeMCP:    {},
	}
	if got := groups[domain.PowerTypeSkill][0]; got != ref {
		t.Fatalf("skill group = %#v, want %#v", got, ref)
	}
}
```

在 paths 测试中增加：

```go
func TestManifestPathUsesAPMHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)

	got, err := paths.ManifestPath(domain.AgentID("codex"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".apm", "manifests", "codex.json")
	if got != want {
		t.Fatalf("ManifestPath() = %q, want %q", got, want)
	}
}
```

再设置空 `APM_HOME` 和临时 `HOME`，确认默认路径为 `$HOME/.apm/manifests/claude.json`；同时设置空 `APM_HOME`/`HOME`，确认 ManifestPath 透传 HOME 解析错误。这些测试不得并行。

- [ ] **Step 2: Run focused tests and verify the APIs are absent**

Run:

```bash
go test ./internal/domain ./internal/paths -run 'TestPowerGroups|TestManifestPath' -count=1
```

Expected: FAIL，编译错误指出 `domain.PowerGroups` 未定义，且 `ManifestPath` 仍只返回一个值。

- [ ] **Step 3: Add the minimal model and path implementation**

在 `PowerRef` 后增加：

```go
// PowerGroups 表示按能力类别组织的能力引用集合。
type PowerGroups map[PowerType][]PowerRef
```

把路径占位改为：

```go
// ManifestPath 返回指定智能体的安装清单路径。
func ManifestPath(agent domain.AgentID) (string, error) {
	state, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "manifests", string(agent)+".json"), nil
}
```

本任务暂时保留旧 `InstalledPower` 以保证全仓编译；不要在路径层校验 Agent ID 或创建目录。

- [ ] **Step 4: Format and verify**

```bash
gofmt -w internal/domain/power.go internal/domain/domain_test.go internal/paths/paths.go internal/paths/paths_test.go
go test ./internal/domain ./internal/paths -count=1
go test ./... -count=1
```

Expected: all PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/domain/power.go internal/domain/domain_test.go internal/paths/paths.go internal/paths/paths_test.go
git commit -m "feat(config): add power groups and manifest paths"
```

---

### Task 2: Implement JSON List and Find

**Files:**
- Create: `internal/config/repository.go`
- Create: `internal/config/errors.go`
- Modify: `internal/config/manifest.go:1-10`
- Create: `internal/config/json_repository.go`
- Create: `internal/config/json_repository_test.go`

**Interfaces:**
- Consumes: `domain.AgentID`、`domain.PowerType`、`domain.PowerRef`、`domain.PowerGroups`、`paths.ManifestPath`、`logging.Agent`。
- Produces:

```go
type Repository interface {
	List(domain.AgentID) (domain.PowerGroups, error)
	Find(domain.AgentID, domain.PowerType, string) (domain.PowerRef, bool, error)
	Set(domain.AgentID, domain.PowerRef) error
	Remove(domain.AgentID, domain.PowerType, string) error
}

var ErrInvalidPower error
type JSONRepository struct{}
func NewJSONRepository() *JSONRepository
```

- [ ] **Step 1: Write failing missing-file and valid-format tests**

创建 `json_repository_test.go`，使用 `package config_test`。测试辅助函数固定写入测试 HOME：

```go
func manifestPath(t *testing.T, agent domain.AgentID) string {
	t.Helper()
	path, err := paths.ManifestPath(agent)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeManifest(t *testing.T, agent domain.AgentID, content string) string {
	t.Helper()
	path := manifestPath(t, agent)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
```

缺失文件测试必须断言三个 key 均存在、切片非 nil 且为空，并断言只读 List 没有创建 `.apm`：

```go
func TestJSONRepositoryListMissingManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APM_HOME", home)
	groups, err := config.NewJSONRepository().List("codex")
	if err != nil {
		t.Fatal(err)
	}
	for _, powerType := range []domain.PowerType{
		domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP,
	} {
		items, ok := groups[powerType]
		if !ok || items == nil || len(items) != 0 {
			t.Fatalf("group %q = %#v, want present non-nil empty slice", powerType, items)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".apm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only List created state path: %v", err)
	}
}
```

有效 fixture 同时包含三组、未知顶层字段和未知条目字段；断言每个返回引用的 Type 由分组补全。修改第一次 List 返回的 map 和 skill 切片，再次 List，断言磁盘结果未改变。

- [ ] **Step 2: Write failing format and Find tests**

用表驱动测试覆盖：

| Case | JSON | Expected |
| --- | --- | --- |
| missing groups | `{"agent":"codex"}` | 三个非 nil 空组 |
| null groups | `{"agent":"codex","skills":null,"plugins":null,"mcps":null}` | 三个非 nil 空组 |
| invalid JSON | `{` | error |
| trailing JSON | `{"agent":"codex"}{}` | error |
| wrong group type | `{"agent":"codex","skills":{}}` | error |
| missing agent | `{"skills":[]}` | error |
| mismatched agent | `{"agent":"claude","skills":[]}` | error |
| unchecked content | `{"agent":"codex","skills":[{"name":"","version":""},{"name":"","version":""}]}` | success with two entries |

Find 测试覆盖：重复 skill 名称返回第一项；缺失名称返回 `(PowerRef{}, false, nil)`；未知类型和空名称满足 `errors.Is(err, config.ErrInvalidPower)`。把清单父路径做成普通文件后传入无效参数，仍应先返回 `ErrInvalidPower`，证明校验先于 I/O。

- [ ] **Step 3: Run focused tests and verify failure**

```bash
go test ./internal/config -run 'TestJSONRepository(List|Find|Read)' -count=1
```

Expected: FAIL，编译错误指出 `NewJSONRepository`、`Repository` 或 `ErrInvalidPower` 未定义。

- [ ] **Step 4: Define interface, error, DTO, and read conversion**

`repository.go` 使用上面的四方法接口。`errors.go` 定义：

```go
var ErrInvalidPower = errors.New("invalid power reference")
```

在 `manifest.go` 暂时保留旧导出 `Manifest`，并增加：

```go
type manifestDocument struct {
	Agent   domain.AgentID `json:"agent"`
	Skills  []powerEntry   `json:"skills"`
	Plugins []powerEntry   `json:"plugins"`
	MCPs    []powerEntry   `json:"mcps"`
}

type powerEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
```

增加 `emptyPowerGroups`、DTO/领域双向转换和深拷贝辅助函数。所有空组使用 `make([]domain.PowerRef, 0)`，不得留下 nil。

`json_repository.go` 定义零字段 `JSONRepository` 和构造函数。内部 `read(agent)` 必须：解析路径；把 `os.ErrNotExist` 映射为空清单；包装其他读取错误；用 `json.Unmarshal` 拒绝尾随非空白；检查 Agent 非空且匹配；转换分组；用包级 `slog.Debug` 记录 Agent 和路径。

`List` 为 read 后返回深拷贝；`Find` 为输入校验后 read，再返回目标分组第一个同名值。未导出 `validPowerType` 只接受三种类型。

- [ ] **Step 5: Format and verify read-only behavior**

```bash
gofmt -w internal/config/repository.go internal/config/errors.go internal/config/manifest.go internal/config/json_repository.go internal/config/json_repository_test.go
go test ./internal/config -run 'TestJSONRepository(List|Find|Read)' -count=1
go test ./... -count=1
```

Expected: all PASS；缺失文件用例确认 `.apm` 不存在。

- [ ] **Step 6: Add and verify verbose Debug coverage**

保存并恢复 `slog.Default()`，安装 `logging.NewLogger(io.Discard, &stderr, true)`，调用缺失清单的 List，断言 stderr 含 `level=DEBUG`、`agent=codex` 和清单路径；不固定内部诊断文案。测试不得并行：

```go
previous := slog.Default()
t.Cleanup(func() { slog.SetDefault(previous) })
slog.SetDefault(logging.NewLogger(io.Discard, &stderr, true))
```

Run: `go test ./internal/config -run TestJSONRepositoryLogsVerboseRead -count=1`

Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/config/repository.go internal/config/errors.go internal/config/manifest.go internal/config/json_repository.go internal/config/json_repository_test.go
git commit -m "feat(config): add manifest list and find"
```

---

### Task 3: Implement Set, Remove, and direct atomic writes

**Files:**
- Modify: `internal/config/json_repository.go`
- Modify: `internal/config/json_repository_test.go`

**Interfaces:**
- Consumes: Task 2 的 `read`、DTO/conversion、`ErrInvalidPower`、`validPowerType`。
- Produces: `(*JSONRepository).Set(domain.AgentID, domain.PowerRef) error`；`(*JSONRepository).Remove(domain.AgentID, domain.PowerType, string) error`；未导出 `write(agent, groups) error`。

- [ ] **Step 1: Write failing Set tests**

逐个重新读取磁盘并断言：缺失文件 Set skill 后创建固定四字段 JSON；新名称追加到分组末尾；同类型同名新版本替换第一项并删除其余重复项；其他条目和分组顺序不变；已有无效非目标条目保持原样。

精确输出使用：

```go
const want = `{
  "agent": "codex",
  "skills": [
    {
      "name": "foo-before-summary",
      "version": "0.0.1"
    }
  ],
  "plugins": [],
  "mcps": []
}
`
```

相同类型/名称/版本且只有一项时，调用前后 `os.Stat` 的 FileInfo 必须满足 `os.SameFile(before, after)`，证明未原子替换。未知类型、空名称、空版本必须满足 `errors.Is(err, config.ErrInvalidPower)` 且不创建文件。

- [ ] **Step 2: Write failing Remove tests**

覆盖删除目标组全部同名项并保持其余顺序；名称不存在时 inode 不变；清单文件不存在时成功且不创建 `.apm`；未知类型和空名称返回 `ErrInvalidPower` 且不触碰文件。

- [ ] **Step 3: Write failing permissions, cleanup, and stateless tests**

加入：

1. 预先以 `0755` 创建 `manifests/`、以 `0644` 写带未知字段的旧清单，Set 后断言目录 `Perm()==0700`、目标 `Perm()==0600`，且输出不再包含未知字段。
2. Set 成功后 `os.ReadDir(manifestsDir)` 只包含 `<agent>.json`，没有临时文件。
3. 把 `.apm/manifests` 创建为普通文件，Set 返回含 Agent/路径上下文的错误，且没有在其他目录产生临时文件。
4. 同一个 Repository 依次 Set codex skill 和 claude plugin，再分别 List，断言两个 Agent 文件互不混合。

- [ ] **Step 4: Run focused tests and verify mutation methods fail**

```bash
go test ./internal/config -run 'TestJSONRepository(Set|Remove|Write|IsStateless)' -count=1
```

Expected: FAIL，`JSONRepository` 缺少 Set/Remove 或测试观察不到目标文件。

- [ ] **Step 5: Implement Set and Remove target-state semantics**

Set 顺序固定为校验、read、扫描目标组、判断是否需要写、替换/归并、write。替换时保存第一个匹配位置，跳过后续同名项；新项追加。只有恰好一个同名项且完整 `PowerRef` 等于输入才直接成功不写。

Remove 顺序固定为校验、read、过滤目标组、无匹配返回 nil、有匹配 write。过滤后保留非目标项的相对顺序。

两个幂等路径都调用 `slog.Debug` 并包含 Agent、Power；Set 还包含 Version。使用 `internal/logging` 字段辅助函数。

Set/Remove 完成后增加 `var _ Repository = (*JSONRepository)(nil)` 编译期断言，保证实现与 Task 2 接口一致。

- [ ] **Step 6: Implement direct atomic writing in JSONRepository**

未导出 write 先编码：

```go
document := documentFromGroups(agent, groups)
data, err := json.MarshalIndent(document, "", "  ")
if err != nil {
	return fmt.Errorf("encode manifest for agent %q: %w", agent, err)
}
data = append(data, '\n')
```

随后严格执行：

1. `paths.ManifestPath(agent)`；
2. `os.MkdirAll(parent, 0o700)` 后 `os.Chmod(parent, 0o700)`；
3. `os.CreateTemp(parent, "."+filepath.Base(target)+"-*.tmp")`；
4. `temp.Chmod(0o600)`，并 defer 清理临时路径；
5. `temp.Write(data)`、`temp.Sync()`、`temp.Close()`，每步包装上下文，失败路径显式关闭；
6. `os.Rename(tempName, target)`；
7. `os.Open(parent)`、目录 `Sync()`、`Close()`；
8. 成功后 `slog.Debug`，包含 Agent 和目标路径。

defer 清理遇到 `os.ErrNotExist` 时忽略；主操作和清理同时失败时用 `errors.Join` 保留两者。rename 成功但目录 Sync 失败仍返回错误，不重试，也不声称旧内容仍在。不得调用或新建通用 AtomicWrite。

- [ ] **Step 7: Format and verify**

```bash
gofmt -w internal/config/json_repository.go internal/config/json_repository_test.go
go test ./internal/config -count=1
go test ./... -count=1
go vet ./...
```

Expected: all PASS。

- [ ] **Step 8: Commit**

```bash
git add internal/config/json_repository.go internal/config/json_repository_test.go
git commit -m "feat(config): atomically set and remove powers"
```

---

### Task 4: Remove legacy manifest state and align placeholders

**Files:**
- Delete: `internal/config/store.go`
- Delete: `internal/config/json_store.go`
- Delete: `internal/config/atomic_write.go`
- Modify: `internal/config/manifest.go`
- Modify: `internal/domain/power.go`
- Modify: `internal/domain/domain_test.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/service.go`

**Interfaces:**
- Consumes: `config.Repository` 与 `domain.PowerGroups`。
- Produces: 删除 `InstalledPower`、`config.Manifest`、`config.Store`、`JSONStore`、`AtomicWrite`；新增 `type ConfigRepository = config.Repository`；`ListResult.InstalledByAgent` 使用 `map[domain.AgentID]domain.PowerGroups`。

- [ ] **Step 1: Change compile tests to require the final domain model**

删除测试的 time 导入和 InstalledPower 构造，改为：

```go
func TestSharedTypesCompile(t *testing.T) {
	ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill, Version: "1.0.0"}
	groups := domain.PowerGroups{domain.PowerTypeSkill: {ref}}
	_ = domain.Power{}
	result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
	if groups[domain.PowerTypeSkill][0] != ref || len(result.Items) != 1 {
		t.Fatal("共享类型未保存输入")
	}
}
```

- [ ] **Step 2: Remove InstalledPower and observe remaining dependents**

从 `power.go` 删除 time 导入及 InstalledPower。Run: `go test ./... -count=1`

Expected: FAIL，错误只来自旧 config Manifest 和 lifecycle ListResult。

- [ ] **Step 3: Delete old config entry points**

删除 `store.go`、`json_store.go`、`atomic_write.go`；从 `manifest.go` 删除导出 Manifest，只保留 Task 2 的私有 JSON DTO 和转换。不得保留兼容别名。

- [ ] **Step 4: Align lifecycle compile-time placeholders without wiring behavior**

`dependencies.go` 使用：

```go
// ConfigRepository 是配置清单 Repository 的别名。
type ConfigRepository = config.Repository
```

`service.go` 使用：

```go
type ListResult struct {
	CloudPowers      []domain.PowerRef
	InstalledByAgent map[domain.AgentID]domain.PowerGroups
	Failures         map[domain.AgentID]error
}
```

不要给 Service 增加 Repository 字段，不修改构造函数，不让 CLI 执行 config。

- [ ] **Step 5: Format, test, and prove old APIs are gone**

```bash
gofmt -w internal/domain/power.go internal/domain/domain_test.go internal/config/manifest.go internal/lifecycle/dependencies.go internal/lifecycle/service.go
go test ./... -count=1
go vet ./...
test -z "$(rg -n 'InstalledPower|ConfigStore|NewJSONStore|type JSONStore|AtomicWrite|type Store interface|config\.Manifest' internal --glob '*.go')"
```

Expected: tests/vet PASS；扫描无输出。

- [ ] **Step 6: Commit**

```bash
git add internal/domain internal/config internal/lifecycle
git commit -m "refactor(config): replace manifest store with repository"
```

---

### Task 5: Acceptance verification and changelog

**Files:**
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: Feature 005 全部实现提交和项目 Changelog 模板。
- Produces: 完整验收证据和一条 2026-08-07“新增功能”记录。

- [ ] **Step 1: Run full verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0；所有含测试的包报告 ok。

- [ ] **Step 2: Run focused acceptance without test cache**

```bash
go clean -testcache
go test ./internal/config ./internal/paths ./internal/domain -count=1 -v
```

Expected: PASS；List/Find/Set/Remove、格式、权限、失败、路径和无状态测试均实际执行，不得有 SKIP。

- [ ] **Step 3: Prove scope and logging boundaries**

```bash
test -z "$(rg -n 'InstalledPower|ConfigStore|NewJSONStore|type JSONStore|AtomicWrite|type Store interface|config\.Manifest' internal --glob '*.go')"
test -z "$(rg -n 'fmt\.(Print|Printf|Println|Fprint|Fprintf|Fprintln)|log\.(Print|Printf|Println|Fatal|Fatalf|Fatalln)|slog\.New' internal/config --glob '*.go')"
rg -n 'slog\.Debug' internal/config --glob '*.go'
```

Expected: first two commands无输出并退出 0；最后一条只显示 JSONRepository 的 Debug 调用。

- [ ] **Step 4: Smoke-test that CLI remains parse-only**

```bash
go run ./cmd/apm --help >/tmp/apm-feature005-help.txt
go run ./cmd/apm search demo >/tmp/apm-feature005-stdout.txt 2>/tmp/apm-feature005-stderr.txt
test ! -s /tmp/apm-feature005-stdout.txt
test ! -s /tmp/apm-feature005-stderr.txt
```

Expected: commands exit 0；help 含 search/list/install/uninstall；普通 search 无业务输出。检查后删除三个明确的 `/tmp/apm-feature005-*` 文件。

- [ ] **Step 5: Record the completed behavior**

运行 `git log --oneline --reverse 4ca8df6^..HEAD`，把输出中的 Feature 005 设计、计划和实现提交逐条写入同一个 Changelog 记录。记录正文使用：

```markdown
## 2026-08-07

分类：新增功能

用户需求：

定义 Agent Power 安装清单格式并实现 config 接口，使 lifecycle 后续只调用无状态 Repository 的 List、Find、Set、Remove。

解决方案或完成结果：

每个 Agent 使用固定 skills/plugins/mcps 分组 JSON；清单条目统一为版本已确定的 PowerRef。JSONRepository 在四个业务接口内部封装读取和原子读改写，不保留跨调用状态，也不获取全局锁；缺失文件、幂等 Set/Remove、重复项归并和输入错误按 Feature 005 规格处理。

验证结果或限制：

记录全量 test、vet、build、diff check、聚焦无缓存测试和静态边界扫描的实际结果。说明 lifecycle/CLI 尚未串联，Repository 写入依赖调用方持有全局 APM 锁。
```

在“相关提交”与“解决方案或完成结果”之间写入上一步命令产生的每个实际 `<hash> <summary>`，逐行使用 Changelog 模板的项目格式；不得预填尚不存在的哈希。不得在验证失败时写完成记录。该记录不列尚未创建的 Changelog 提交自身。

- [ ] **Step 6: Commit changelog and rerun final verification**

```bash
git add docs/Changelog.md
git commit -m "docs: record config repository implementation"
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
git status --short
```

Expected: all verification exits 0；`git status --short` 无输出。

---

## Execution Notes

- 执行前使用 `superpowers:using-git-worktrees` 建立隔离 worktree，不在设计提交所在主工作区直接混入实现。
- 每个 Task 完成后复核规格符合性和代码质量，再进入下一 Task。
- 声称完成前使用 `superpowers:verification-before-completion` 运行新鲜验证。
- 完成全部任务后使用 `superpowers:finishing-a-development-branch` 决定合并、PR 或保留分支。

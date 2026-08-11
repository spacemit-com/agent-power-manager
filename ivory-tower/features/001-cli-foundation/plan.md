# CLI 基础骨架 Implementation Plan

> **历史计划（已被后续 feature 修订）：** 本计划中的 parse-only CLI 和 `Fetch(... Content ...)` 仅记录当时的实施步骤；Search、List、Install 已分别被 Feature 008、009、010 实现，只有 Uninstall 仍为 parse-only，Cache staging-root 契约已被 Feature 010 supersede。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立可编译的 APM Go 工程框架，交付只解析命令的 CLI，以及可独立使用的 logging 和全局 flock。

**Architecture:** `cmd/apm` 只启动 `internal/cli`；CLI 使用 Cobra 解析四个动词，并只连接 `internal/logging`。`internal/lock` 通过 `internal/paths` 获得 `$HOME/.apm/apm.lock`，但不接入 CLI；其余 ADR-001 模块只保留可编译的主要类型、接口和零值方法占位。

**Tech Stack:** Go 1.26、`spf13/cobra`、`gofrs/flock`、标准库 `log/slog` 与 `testing`。

## Global Constraints

- Go module 命名为 `apm`。
- 创建 ADR-001 列出的生产文件，但不创建三个内置 Adapter 目录。
- 只有 CLI、logging、lock 和 lock 所需的 paths 逻辑可以实现；其余具体方法只能返回零值。
- CLI 只连接 logging，不创建或调用 Lifecycle，也不获取 lock。
- 重要导出对象、接口、构造函数和方法必须有中文 GoDoc 注释。
- Cobra 生成 help/usage；logging 只管理 writer 和 verbose 诊断。
- 默认锁路径为 `$HOME/.apm/apm.lock`，获取方式为非阻塞。
- 不实现 `--version`、default-agent、业务成功输出、内置 Adapter 或 Power 生命周期逻辑。
- 实现完成后按 `/home/liangff/.codex/docs/Changelog.md` 更新 `docs/Changelog.md`。

---

## File Map

| 路径 | 职责 |
| --- | --- |
| `go.mod`, `go.sum` | module 与 Cobra/flock 依赖。 |
| `cmd/apm/main.go` | 创建 logger 并执行 CLI。 |
| `internal/domain/*.go` | Agent、Power 和结果共享类型。 |
| `internal/logging/*.go` | stdout/stderr、稳定输出和 verbose slog。 |
| `internal/cli/*.go` | Cobra 根命令、公共 flags 和四个子命令。 |
| `internal/paths/paths.go`, `internal/lock/*.go` | 默认锁路径与非阻塞 flock。 |
| `internal/lifecycle/*.go`, `internal/transaction/*.go` | 生命周期、journal 与事务占位。 |
| `internal/adapter/*.go`, `internal/config/*.go` | Adapter Registry、manifest 与 Store 占位。 |
| `internal/cache/*.go`, `internal/cloud/*.go`, `internal/version/*.go` | 缓存、云端和版本占位。 |
| `docs/Changelog.md` | 本次行为性源码交付记录。 |

---

### Task 1: 初始化 module 与共享领域类型

**Files:**
- Create: `go.mod`
- Create: `go.sum`
- Create: `internal/domain/agent.go`
- Create: `internal/domain/power.go`
- Create: `internal/domain/result.go`
- Test: `internal/domain/domain_test.go`

**Interfaces:**
- Produces: `AgentID`、`AgentSnapshot`、`PowerType`、`PowerRef`、`Power`、`InstalledPower`、`Result`。

- [ ] **Step 1: 初始化 module 和依赖**

```bash
go mod init apm
go get github.com/spf13/cobra
go get github.com/gofrs/flock
```

- [ ] **Step 2: 写失败测试**

```go
func TestSharedTypesCompile(t *testing.T) {
    ref := domain.PowerRef{Name: "demo", Type: domain.PowerTypeSkill}
    installed := domain.InstalledPower{Ref: ref, Version: "1.0.0"}
    result := domain.Result{Items: []domain.AgentResult{{Agent: "codex", Status: domain.StatusCompleted}}}
    if installed.Ref != ref || len(result.Items) != 1 { t.Fatal("共享类型未保存输入") }
}
```

- [ ] **Step 3: 运行 `go test ./internal/domain`，确认因类型缺失而失败。**

- [ ] **Step 4: 创建带中文 GoDoc 的最小类型**

```go
type AgentID string
const AllAgents AgentID = "all"
type AgentSnapshot struct { Agents []AgentID }
func (s AgentSnapshot) Contains(AgentID) bool { return false }
func (s AgentSnapshot) Ordered() []AgentID { return nil }

type PowerType string
const ( PowerTypeSkill PowerType = "skill"; PowerTypePlugin PowerType = "plugin"; PowerTypeMCP PowerType = "mcp" )
type PowerRef struct { Name string; Type PowerType }
type Power struct { Ref PowerRef; Version string; Data []byte }
type InstalledPower struct { Ref PowerRef; Version string; InstalledAt time.Time }

type ResultStatus string
const ( StatusCompleted ResultStatus = "completed"; StatusFailed ResultStatus = "failed"; StatusUnexecuted ResultStatus = "unexecuted" )
type AgentResult struct { Agent AgentID; Status ResultStatus; Err error }
type Result struct { Items []AgentResult }
```

`Contains` 和 `Ordered` 是零值占位，不实现排序或查找。

- [ ] **Step 5: 验证并提交**

```bash
gofmt -w internal/domain
go test ./internal/domain
git add go.mod go.sum internal/domain
git commit -m "chore: initialize APM Go module"
```

---

### Task 2: 实现统一 logging

**Files:**
- Create: `internal/logging/logger.go`
- Create: `internal/logging/output.go`
- Create: `internal/logging/verbose.go`
- Create: `internal/logging/fields.go`
- Test: `internal/logging/logger_test.go`

**Interfaces:**
- Produces: `New(io.Writer, io.Writer, bool) *Logger`、`NewDefault(bool)`、`SetVerbose(bool)`、writer 访问器、四类稳定输出和 `Verbose`。

- [ ] **Step 1: 写输出路由和 verbose 失败测试**

```go
func TestRoutesAndVerbose(t *testing.T) {
    var out, errOut bytes.Buffer
    logger := logging.New(&out, &errOut, false)
    if err := logger.Output("result"); err != nil { t.Fatal(err) }
    if err := logger.Warning("warning"); err != nil { t.Fatal(err) }
    logger.Verbose("parsed", slog.String("command", "search"))
    if out.String() != "result\n" || !strings.Contains(errOut.String(), "warning") || strings.Contains(errOut.String(), "parsed") { t.Fatal("输出路由错误") }

    errOut.Reset()
    logger = logging.New(io.Discard, &errOut, false)
    logger.SetVerbose(true)
    logger.Verbose("parsed", slog.String("command", "search"))
    if !strings.Contains(errOut.String(), "parsed") || !strings.Contains(errOut.String(), "search") { t.Fatal("verbose 未输出") }
}
```

另断言 `Error`、`Recovery` 写 stderr，`Stdout`/`Stderr` 返回注入 writer。

- [ ] **Step 2: 运行 `go test ./internal/logging`，确认 API 缺失。**

- [ ] **Step 3: 实现 Logger**

```go
type Logger struct { stdout, stderr io.Writer; verbose bool; diagnostic *slog.Logger }
func New(stdout, stderr io.Writer, verbose bool) *Logger {
    return &Logger{stdout: stdout, stderr: stderr, verbose: verbose,
        diagnostic: slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}
func NewDefault(verbose bool) *Logger { return New(os.Stdout, os.Stderr, verbose) }
func (l *Logger) SetVerbose(verbose bool) { l.verbose = verbose }
func (l *Logger) Stdout() io.Writer { return l.stdout }
func (l *Logger) Stderr() io.Writer { return l.stderr }
func writeLine(w io.Writer, message string) error { _, err := fmt.Fprintln(w, message); return err }
func (l *Logger) Output(v string) error { return writeLine(l.stdout, v) }
func (l *Logger) Warning(v string) error { return writeLine(l.stderr, v) }
func (l *Logger) Error(v string) error { return writeLine(l.stderr, v) }
func (l *Logger) Recovery(v string) error { return writeLine(l.stderr, v) }
func (l *Logger) Verbose(message string, attrs ...slog.Attr) {
    if l.verbose { l.diagnostic.LogAttrs(context.Background(), slog.LevelDebug, message, attrs...) }
}
```

`fields.go` 精确提供 `Agent(domain.AgentID)`、`Power(string)`、`Version(string)`、`Phase(string)` 四个 `slog.Attr` 构造函数。

- [ ] **Step 4: 验证并提交**

```bash
gofmt -w internal/logging
go test ./internal/logging
git add internal/logging
git commit -m "feat: add unified logging"
```

---

### Task 3: 实现只解析命令的 Cobra CLI

**Files:**
- Create: `internal/cli/root.go`
- Create: `internal/cli/flags.go`
- Create: `internal/cli/search.go`
- Create: `internal/cli/list.go`
- Create: `internal/cli/install.go`
- Create: `internal/cli/uninstall.go`
- Test: `internal/cli/cli_test.go`
- Create: `cmd/apm/main.go`

**Interfaces:**
- Consumes: `logging.Logger`。
- Produces: `New(*logging.Logger) *CLIEntry`、`Execute([]string) error`、`Flags`。

- [ ] **Step 1: 写表驱动失败测试**

```go
tests := []struct { name string; args []string; wantErr bool }{
    {"search", []string{"search", "foo", "--agent=all"}, false},
    {"list", []string{"list", "--agent=codex", "--installed"}, false},
    {"install", []string{"install", "demo=1.2.3", "--agent=codex", "--type=skill"}, false},
    {"uninstall", []string{"uninstall", "demo", "--agent=codex"}, false},
    {"missing agent", []string{"search", "foo"}, true},
    {"invalid type", []string{"list", "--agent=all", "--type=other"}, true},
    {"empty version", []string{"install", "demo=", "--agent=all"}, true},
    {"extra arg", []string{"uninstall", "demo", "extra", "--agent=all"}, true},
}
```

每个 case 注入独立 buffers；合法 case 断言 stdout/stderr 为空。另测 `--help` stdout 含四个命令且 stderr 为空；verbose 只在 stderr 含 `command=search` 和 `agent=all`。

- [ ] **Step 2: 运行 `go test ./internal/cli`，确认 API 缺失。**

- [ ] **Step 3: 定义 flags 和校验器**

```go
type Flags struct { Agent, Type, Name, Version, Keyword string; Installed bool }
func validateCommon(f Flags) error {
    if strings.TrimSpace(f.Agent) == "" { return errors.New("--agent is required") }
    switch f.Type { case "", "skill", "plugin", "mcp": return nil }
    return fmt.Errorf("invalid --type %q: must be skill, plugin, or mcp", f.Type)
}
func parsePower(v string) (string, string, error) {
    name, version, hasVersion := strings.Cut(v, "=")
    if strings.TrimSpace(name) == "" { return "", "", errors.New("power name must not be empty") }
    if hasVersion && strings.TrimSpace(version) == "" { return "", "", errors.New("power version must not be empty") }
    return name, version, nil
}
```

- [ ] **Step 4: 实现根命令和四个构造函数**

```go
type CLIEntry struct { root *cobra.Command }
func New(logger *logging.Logger) *CLIEntry {
    root := &cobra.Command{Use: "apm", Short: "Agent Power Manager"}
    root.SetOut(logger.Stdout()); root.SetErr(logger.Stderr())
    root.PersistentFlags().Bool("verbose", false, "enable verbose diagnostics")
    root.AddCommand(newSearchCommand(logger), newListCommand(logger), newInstallCommand(logger), newUninstallCommand(logger))
    return &CLIEntry{root: root}
}
func (e *CLIEntry) Execute(args []string) error { e.root.SetArgs(args); return e.root.Execute() }
```

四个命令使用 `cobra.ExactArgs`；绑定并校验 `--agent`/`--type`；`list` 绑定 `--installed`；`install` 调用 `parsePower`。每个合法 `RunE` 最后只执行：

```go
if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
    logger.SetVerbose(true)
    logger.Verbose("command parsed", slog.String("command", cmd.Name()),
        slog.String("agent", flags.Agent), slog.String("type", flags.Type), slog.Any("args", args))
}
return nil
```

Cobra 错误写 stderr 并显示 usage；help 使用默认生成器写 stdout。不得调用 Lifecycle、lock 或打印业务结果。

- [ ] **Step 5: 创建入口，验证并提交**

```go
func main() {
    logger := logging.NewDefault(false)
    if err := cli.New(logger).Execute(os.Args[1:]); err != nil { os.Exit(1) }
}
```

```bash
gofmt -w internal/cli cmd/apm
go test ./internal/cli
go build ./cmd/apm
git add cmd/apm internal/cli
git commit -m "feat: add parse-only CLI"
```

---

### Task 4: 实现默认锁路径与非阻塞 flock

**Files:**
- Create: `internal/paths/paths.go`
- Test: `internal/paths/paths_test.go`
- Create: `internal/lock/locker.go`
- Create: `internal/lock/flock.go`
- Test: `internal/lock/flock_test.go`

**Interfaces:**
- Produces: `paths.LockPath()`、`lock.ErrLocked`、`Locker`、`Unlock`、`New(string)`、`NewDefault()`。

- [ ] **Step 1: 写失败测试**

```go
func TestLockPathUsesHome(t *testing.T) {
    home := t.TempDir(); t.Setenv("HOME", home)
    got, err := paths.LockPath(); if err != nil { t.Fatal(err) }
    if want := filepath.Join(home, ".apm", "apm.lock"); got != want { t.Fatalf("got %q want %q", got, want) }
}
func TestFlockContendsAndCanBeReacquired(t *testing.T) {
    path := filepath.Join(t.TempDir(), "nested", "apm.lock")
    first, second := lock.New(path), lock.New(path)
    unlock, err := first.Acquire(); if err != nil { t.Fatal(err) }
    if _, err = second.Acquire(); !errors.Is(err, lock.ErrLocked) { t.Fatalf("err=%v", err) }
    if err = unlock.Release(); err != nil { t.Fatal(err) }
    again, err := second.Acquire(); if err != nil { t.Fatal(err) }
    if err = again.Release(); err != nil { t.Fatal(err) }
}
```

- [ ] **Step 2: 运行 `go test ./internal/paths ./internal/lock`，确认 API 缺失。**

- [ ] **Step 3: 实现 path 和 lock**

```go
func LockPath() (string, error) {
    home, err := os.UserHomeDir()
    if err != nil { return "", fmt.Errorf("resolve home directory: %w", err) }
    if home == "" { return "", errors.New("resolve home directory: empty path") }
    return filepath.Join(home, ".apm", "apm.lock"), nil
}
func JournalPath() string { return "" }
func ManifestPath(domain.AgentID) string { return "" }
func CachePath() string { return "" }
func TransactionPath() string { return "" }

var ErrLocked = errors.New("APM lock is already held")
type Locker interface { Acquire() (Unlock, error) }
type Unlock interface { Release() error }
type flockLocker struct { path string }
type flockUnlock struct { file *flock.Flock }
func New(path string) Locker { return &flockLocker{path: path} }
func NewDefault() (Locker, error) { path, err := paths.LockPath(); if err != nil { return nil, err }; return New(path), nil }
func (l *flockLocker) Acquire() (Unlock, error) {
    if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil { return nil, fmt.Errorf("create lock directory: %w", err) }
    file := flock.New(l.path); acquired, err := file.TryLock()
    if err != nil { return nil, fmt.Errorf("acquire APM lock: %w", err) }
    if !acquired { return nil, ErrLocked }
    return &flockUnlock{file: file}, nil
}
func (u *flockUnlock) Release() error { if err := u.file.Unlock(); err != nil { return fmt.Errorf("release APM lock: %w", err) }; return nil }
```

- [ ] **Step 4: 验证并提交**

```bash
gofmt -w internal/paths internal/lock
go test ./internal/paths ./internal/lock
git add internal/paths internal/lock
git commit -m "feat: add global APM file lock"
```

---

### Task 5: 创建 Lifecycle 与 Transaction 占位

**Files:**
- Create: `internal/lifecycle/service.go`
- Create: `internal/lifecycle/dependencies.go`
- Create: `internal/lifecycle/preflight.go`
- Create: `internal/lifecycle/batch.go`
- Create: `internal/lifecycle/search.go`
- Create: `internal/lifecycle/list.go`
- Create: `internal/lifecycle/install.go`
- Create: `internal/lifecycle/uninstall.go`
- Create: `internal/transaction/journal.go`
- Create: `internal/transaction/journal_store.go`
- Create: `internal/transaction/recovery.go`
- Create: `internal/transaction/install.go`
- Create: `internal/transaction/uninstall.go`
- Create: `internal/transaction/path_state.go`

**Interfaces:**
- Consumes: `cli.Flags` 和 domain 类型。
- Produces: `Lifecycle`、`Service`、`Journal`、`JournalStore` 及事务占位。

- [ ] **Step 1: 创建 Lifecycle 精确占位**

```go
type ListResult struct { CloudPowers []domain.PowerRef; InstalledByAgent map[domain.AgentID][]domain.InstalledPower; Failures map[domain.AgentID]error }
type Lifecycle interface { Search(cli.Flags) ([]domain.PowerRef, error); List(cli.Flags) (ListResult, error); Install(cli.Flags) (domain.Result, error); Uninstall(cli.Flags) (domain.Result, error) }
type Service struct{}
func New() *Service { return &Service{} }
func (s *Service) Search(cli.Flags) ([]domain.PowerRef, error) { return nil, nil }
func (s *Service) List(cli.Flags) (ListResult, error) { return ListResult{}, nil }
func (s *Service) Install(cli.Flags) (domain.Result, error) { return domain.Result{}, nil }
func (s *Service) Uninstall(cli.Flags) (domain.Result, error) { return domain.Result{}, nil }
```

`dependencies.go` 本任务只包含 package 声明和“依赖接口由组件任务统一声明”的中文包内注释，避免在具体组件类型创建前制造重复模型。其余六个 lifecycle 文件精确定义以下未导出零值函数，不调用依赖：

```go
func preflight(context.Context) error { return nil }
func orderAgents(domain.AgentSnapshot) []domain.AgentID { return nil }
func search(context.Context, cli.Flags) ([]domain.PowerRef, error) { return nil, nil }
func list(context.Context, cli.Flags) (ListResult, error) { return ListResult{}, nil }
func install(context.Context, cli.Flags) (domain.Result, error) { return domain.Result{}, nil }
func uninstall(context.Context, cli.Flags) (domain.Result, error) { return domain.Result{}, nil }
```

- [ ] **Step 2: 创建 Transaction 精确占位**

```go
type Operation string
const ( OperationInstall Operation = "install"; OperationUninstall Operation = "uninstall" )
type Phase string
const ( PhasePreparing Phase = "preparing"; PhasePublishing Phase = "publishing"; PhaseRemoving Phase = "removing" )
type Journal struct { Operation Operation; Agent domain.AgentID; Power domain.PowerRef; Version string; Phase Phase }
type JournalStore interface { Inspect() (Journal, error); Write(Journal) error; Delete() error }
type Recovery struct{}
func (r *Recovery) Recover(context.Context, Journal) error { return nil }
type Installer struct{}
func (i *Installer) Install(context.Context, domain.Power) error { return nil }
type Uninstaller struct{}
func (u *Uninstaller) Uninstall(context.Context, domain.PowerRef) error { return nil }
type PathState struct { StagingExists, TargetExists, BackupExists, QuarantineExists bool }
func InspectPathState(Journal) (PathState, error) { return PathState{}, nil }
```

按文件职责拆分并添加中文 GoDoc，不写恢复、事务或文件系统逻辑。

- [ ] **Step 3: 编译验证并提交**

```bash
gofmt -w internal/lifecycle internal/transaction
go test ./...
git add internal/lifecycle internal/transaction
git commit -m "chore: scaffold lifecycle transactions"
```

---

### Task 6: 创建其余组件占位

**Files:**
- Modify: `internal/lifecycle/dependencies.go`
- Create: `internal/adapter/adapter.go`
- Create: `internal/adapter/registry.go`
- Create: `internal/config/manifest.go`
- Create: `internal/config/store.go`
- Create: `internal/config/json_store.go`
- Create: `internal/config/atomic_write.go`
- Create: `internal/cache/cache.go`
- Create: `internal/cache/content_addressed.go`
- Create: `internal/cloud/client.go`
- Create: `internal/cloud/http_client.go`
- Create: `internal/cloud/model.go`
- Create: `internal/version/selector.go`

**Interfaces:**
- Consumes: domain 类型。
- Produces: C4 中 AdapterRegistry、ConfigStore、PowerCache、CloudClient 和 Selector 占位。

- [ ] **Step 1: 创建 Adapter 和 Config**

```go
type Adapter interface { DetectAgent() (bool, error); InstallPath(domain.AgentID) string; Install(domain.Power, string) error; Uninstall(domain.PowerRef) error }
type Registry struct{}
func NewRegistry() *Registry { return &Registry{} }
func (r *Registry) Resolve(domain.AgentID, domain.PowerType) (Adapter, error) { return nil, nil }
func (r *Registry) RefreshInstalled() (domain.AgentSnapshot, error) { return domain.AgentSnapshot{}, nil }

type Manifest struct { Agent domain.AgentID; Powers []domain.InstalledPower }
type Store interface { Read(domain.AgentID) (Manifest, error); Write(domain.AgentID, Manifest) error }
type JSONStore struct{}
func NewJSONStore() *JSONStore { return &JSONStore{} }
func (s *JSONStore) Read(domain.AgentID) (Manifest, error) { return Manifest{}, nil }
func (s *JSONStore) Write(domain.AgentID, Manifest) error { return nil }
func AtomicWrite(string, []byte) error { return nil }
```

- [ ] **Step 2: 创建 Cache、Cloud 和 Version**

```go
type Content struct { Path, Checksum string }
type PowerCache interface { Fetch(domain.PowerRef) (Content, error); Put(domain.PowerRef, []byte) error }
type ContentAddressed struct{}
func NewContentAddressed() *ContentAddressed { return &ContentAddressed{} }
func (c *ContentAddressed) Fetch(domain.PowerRef) (Content, error) { return Content{}, nil }
func (c *ContentAddressed) Put(domain.PowerRef, []byte) error { return nil }

type Query struct { Keyword string; Type domain.PowerType }
type PowerMeta struct { Ref domain.PowerRef; Version, DownloadURL string }
type Client interface { List(Query) ([]domain.PowerRef, error); Get(domain.PowerRef) (PowerMeta, error) }
type HTTPClient struct{}
func NewHTTPClient() *HTTPClient { return &HTTPClient{} }
func (c *HTTPClient) List(Query) ([]domain.PowerRef, error) { return nil, nil }
func (c *HTTPClient) Get(domain.PowerRef) (PowerMeta, error) { return PowerMeta{}, nil }

type Selector struct{}
func NewSelector() *Selector { return &Selector{} }
func (s *Selector) Select([]string, string) (string, error) { return "", nil }
```

按文件职责拆分、补中文 GoDoc；不得创建 Adapter 子目录、发 HTTP 请求或实现 semver。

- [ ] **Step 3: 用具体组件类型补全 Lifecycle 依赖接口**

```go
type AdapterRegistry interface {
    Resolve(domain.AgentID, domain.PowerType) (adapter.Adapter, error)
    RefreshInstalled() (domain.AgentSnapshot, error)
}
type ConfigStore interface { Read(domain.AgentID) (config.Manifest, error); Write(domain.AgentID, config.Manifest) error }
type JournalStore interface { Inspect() (transaction.Journal, error); Write(transaction.Journal) error; Delete() error }
type Locker = lock.Locker
type PowerCache = cache.PowerCache
type CloudClient = cloud.Client
```

- [ ] **Step 4: 验证并提交**

```bash
gofmt -w internal/adapter internal/config internal/cache internal/cloud internal/version
go test ./...
test -z "$(find internal/adapter -mindepth 1 -maxdepth 1 -type d -print)"
git add internal/adapter internal/config internal/cache internal/cloud internal/version internal/lifecycle/dependencies.go
git commit -m "chore: scaffold APM components"
```

---

### Task 7: 最终验收与 Changelog

**Files:**
- Create or Modify: `docs/Changelog.md`
- Verify: Tasks 1–6 的全部文件。

- [ ] **Step 1: 运行全部质量检查**

```bash
test -z "$(gofmt -l cmd internal)"
go test ./...
go vet ./...
go build -o ./apm ./cmd/apm
```

Expected: 全部为 0。

- [ ] **Step 2: 分别执行并记录行为和退出码**

```bash
./apm --help
./apm search foo --agent=all
./apm search foo --agent=all --verbose
./apm list --agent=all --type=other
```

Expected: help 展示四个命令；第二条静默成功；第三条只在 stderr 输出解析诊断；第四条显示 usage 并非零退出。

- [ ] **Step 3: 检查范围、注释和工作树**

```bash
test -z "$(find internal/adapter -mindepth 1 -maxdepth 1 -type d -print)"
rg -n "TO[D]O|TB[D]|panic\\(" cmd internal
go doc ./internal/cli ./internal/logging ./internal/lock ./internal/domain
git diff --check
```

Expected: 无 Adapter 子目录；无未完成标记或 panic；重要导出声明显示中文 GoDoc；diff check 无输出。

- [ ] **Step 4: 按模板写一条 Changelog**

日期使用完成实现的实际日期，分类为“新增功能”；用户需求写“建立 ADR-001 整体框架，实现只解析命令的 CLI、logging 和全局 lock”；相关提交列出本计划产生的全部源码提交和规格提交 `baea8bc`；验证结果只记录实际运行输出。

- [ ] **Step 5: 提交 Changelog 并复验**

```bash
git add docs/Changelog.md
git commit -m "docs(changelog): record CLI foundation"
go test ./...
go vet ./...
git status --short
```

Expected: test/vet PASS；不得提交根目录验收二进制 `apm`。

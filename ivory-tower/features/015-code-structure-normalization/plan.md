# APM Code Structure and Interface Naming Normalization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Normalize APM's Skill/MCP/Plugin and Codex/Claude/Hermes file layout, interface names, and declaration ownership without changing observable behavior.

**Architecture:** Keep every existing Go package and the four command-specific lifecycle services. Make Skill names explicit beside MCP and Plugin, keep Agent-specific implementations in one Adapter package, preserve the shared schema-v1 journal with type-specific transaction drivers, and use declarative Agent registration instead of Agent-ID branching.

**Tech Stack:** Go 1.24, Cobra, `log/slog`, Masterminds SemVer, strict JSON decoding, filesystem transactions, target Agent CLIs isolated by fake-bin tests.

## Global Constraints

- Implement the accepted specification at `ivory-tower/features/015-code-structure-normalization/spec.md` exactly; do not introduce new Power or Agent behavior.
- Keep the existing package boundaries; do not create Power subpackages, Agent subpackages, an application package, or a generic `PowerDriver`.
- Preserve CLI flags, output, usage, exit codes, lifecycle ports, Agent order, locks, recovery, fail-fast, logs, errors, filesystem paths, Cloud resources, Cache/staging, and manifest behavior.
- Preserve `transactions/active.json` schema v1, JSON tags, `filesystem`/`agent-cli`/`plugin` driver values, payload exclusivity, phase writes, and `ErrInvalidJournal` fail-closed behavior.
- Preserve every Agent CLI executable name, argv element, scope, stdin, 30-second timeout, process-group handling, bounded output, and status parser.
- Keep CLI runner private in `internal/adapter/cli_runner.go`; do not rename the file or export its types.
- Rename internal exported identifiers directly and update all consumers; do not leave deprecated aliases or compatibility wrappers in the completed feature.
- Use Power order Skill, MCP, Plugin and Agent order Codex, Claude, Hermes in declarations and documentation; preserve runtime snapshot sorting.
- Add concise Chinese comments to major interfaces and business flows. Comments explain ownership, ordering, fail-closed constraints, and prohibited dependencies rather than translating statements line by line.
- Keep tests isolated with temporary `APM_HOME`, `XDG_CACHE_HOME`, Agent homes, and fake-bin. Never invoke the user's real Codex, Claude, or Hermes CLI.
- Use `apply_patch` for source and documentation edits. Preserve unrelated user changes.
- This is a behavior-neutral refactor. Use a green-baseline/refactor/green cycle for pure moves and renames; do not manufacture product behavior changes to obtain a failing test.
- Commit each task only after its focused checks and `git diff --check` pass.
- Add exactly one final `docs/Changelog.md` entry after all code work, review fixes, and final verification. Follow `~/.codex/docs/Changelog.md` and list all related commits except the self-referential Changelog commit.

---

## Target File Map

### Adapter

```text
internal/adapter/
├── doc.go
├── skill.go
├── skill_filesystem.go
├── skill_frontmatter.go
├── mcp.go
├── plugin.go
├── codex_mcp.go
├── codex_plugin.go
├── claude_mcp.go
├── claude_plugin.go
├── hermes_mcp.go
├── hermes_plugin.go
├── agent_profile.go
├── registry.go
├── cli_runner.go
└── errors.go
```

### Cloud and Cache

```text
internal/cloud/{client.go,local_client.go,local_skill.go,local_mcp.go,local_plugin.go,errors.go}
internal/cache/{cache.go,filesystem.go,errors.go}
```

### Lifecycle

```text
internal/lifecycle/
├── doc.go
├── dependencies.go
├── search.go
├── search_result.go
├── list.go
├── list_installed.go
├── list_result.go
├── install.go
├── install_agent.go
├── install_resolve.go
├── install_skill.go
├── install_mcp.go
├── install_plugin.go
├── uninstall.go
├── uninstall_agent.go
├── uninstall_resolve.go
├── uninstall_skill.go
├── uninstall_mcp.go
└── uninstall_plugin.go
```

### Transaction

```text
internal/transaction/
├── doc.go
├── manager.go
├── journal.go
├── journal_store.go
├── filesystem_journal_store.go
├── recovery.go
├── validation.go
├── errors.go
├── skill_install.go
├── skill_uninstall.go
├── skill_recovery.go
├── skill_artifacts.go
├── skill_path_state.go
├── skill_validation.go
├── mcp_install.go
├── mcp_uninstall.go
├── mcp_recovery.go
├── mcp_validation.go
├── plugin_install.go
├── plugin_uninstall.go
├── plugin_recovery.go
├── plugin_filesystem.go
└── plugin_validation.go
```

---

### Task 1: Make the Skill Adapter Contract Explicit Across Packages

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/skill_adapter.go`
- Modify: `internal/adapter/registry.go`
- Modify: `internal/adapter/adapter_test.go`
- Modify: `internal/adapter/registry_test.go`
- Modify: `internal/adapter/skill_adapter_test.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/uninstall_agent.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/list_test.go`
- Modify: `internal/lifecycle/uninstall_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/install_test.go`
- Modify: `internal/transaction/uninstall_test.go`
- Modify: `internal/transaction/validation_test.go`
- Modify: any compile-time consumer found by the old-symbol scan below

**Interfaces:**
- Consumes: existing `adapter.Adapter`, `adapter.InstallResult`, and all existing Skill behavior.
- Produces: `adapter.SkillAdapter` and `adapter.SkillInstallResult` with identical method bodies and publication semantics.

- [ ] **Step 1: Record the green baseline and old-symbol inventory**

Run:

```bash
go test -count=1 ./internal/adapter ./internal/lifecycle ./internal/transaction
rg -n '\bAdapter\b|\bInstallResult\b|DetectAgent\(' internal/adapter internal/lifecycle internal/transaction --glob '*.go'
```

Expected: focused tests PASS; the scan identifies the current Skill contract, fakes, requests, and assertions that must migrate.

- [ ] **Step 2: Rename the Skill contract without changing its temporary method set**

Use `apply_patch` so `internal/adapter/adapter.go` temporarily contains:

```go
// SkillInstallResult 报告 Skill Adapter 是否已经越过原子发布提交点。
type SkillInstallResult struct {
	Published bool
}

// SkillAdapter 定义 Agent 的文件型 Skill 安装操作。
type SkillAdapter interface {
	DetectAgent() (bool, error)
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (SkillInstallResult, error)
}
```

Rename every `adapter.Adapter`/package-local `Adapter` reference to `SkillAdapter`, every Adapter-valued Skill request field accordingly, and every `adapter.InstallResult`/package-local `InstallResult` to `SkillInstallResult`. Do not rename CLI `InstallResult` or lifecycle result types.

- [ ] **Step 3: Update implementations, fakes, and compile-time assertions**

Update `skillAdapter.Install` to return `SkillInstallResult`; update Registry maps and `ResolveSkill`; update lifecycle/transaction fakes and request fields. Keep `DetectAgent` until Task 2 moves Agent discovery so this task remains independently green.

Run the ambiguity scan before formatting:

```bash
rg -n '\badapter\.Adapter\b|\badapter\.InstallResult\b|type Adapter interface|type InstallResult struct' cmd internal --glob '*.go'
```

Expected: no matches. CLI `InstallResult` occurrences without the `adapter.` qualifier remain valid.

- [ ] **Step 4: Format and verify the explicit Skill contract**

Run:

```bash
gofmt -w internal/adapter internal/lifecycle internal/transaction
go test -count=1 ./internal/adapter ./internal/lifecycle ./internal/transaction
go test -count=1 ./...
git diff --check
```

Expected: every command exits 0 and observable tests remain unchanged.

- [ ] **Step 5: Commit the contract normalization**

```bash
git add internal/adapter internal/lifecycle internal/transaction
git commit -m "refactor(adapter): name skill contract explicitly"
```

---

### Task 2: Normalize Adapter Files and Introduce Declarative Agent Registration

**Files:**
- Create: `internal/adapter/doc.go`
- Create: `internal/adapter/skill.go`
- Create: `internal/adapter/skill_filesystem.go`
- Create: `internal/adapter/skill_frontmatter.go`
- Create: `internal/adapter/mcp.go`
- Create: `internal/adapter/plugin.go`
- Create: `internal/adapter/agent_profile.go`
- Create: `internal/adapter/codex_mcp.go`
- Create: `internal/adapter/codex_plugin.go`
- Create: `internal/adapter/claude_mcp.go`
- Create: `internal/adapter/claude_plugin.go`
- Create: `internal/adapter/hermes_mcp.go`
- Create: `internal/adapter/hermes_plugin.go`
- Modify: `internal/adapter/registry.go`
- Keep: `internal/adapter/cli_runner.go`
- Keep: `internal/adapter/errors.go`
- Delete: `internal/adapter/adapter.go`
- Delete: `internal/adapter/config.go`
- Delete: `internal/adapter/skill_adapter.go`
- Delete: `internal/adapter/frontmatter.go`
- Delete: `internal/adapter/mcp_adapter.go`
- Delete: `internal/adapter/plugin_adapter.go`
- Delete: `internal/adapter/mcp_codex.go`
- Delete: `internal/adapter/plugin_codex.go`
- Delete: `internal/adapter/mcp_claude.go`
- Delete: `internal/adapter/plugin_claude.go`
- Delete: `internal/adapter/mcp_hermes.go`
- Delete: `internal/adapter/plugin_hermes.go`
- Rename corresponding tests to the accepted target names

**Interfaces:**
- Consumes: Task 1 `SkillAdapter`/`SkillInstallResult`, existing `MCPAdapter`, `PluginAdapter`, and private `agentCLIRunner`.
- Produces: `filesystemSkillAdapter`, `agentProfile`, `agentRegistration`, and a Registry whose Agent discovery no longer depends on Skill Adapter.

- [ ] **Step 1: Update the contract tests to express the final Agent boundary**

Change the Skill contract fixture so it implements only:

```go
func (contractSkillAdapter) TargetPath(domain.PowerRef) (string, error)
func (contractSkillAdapter) Install(domain.PowerRef, string) (SkillInstallResult, error)
```

Add a Registry test with a Skill Adapter that has no detection method and a profile-controlled temporary root. Assert that `RefreshInstalled` returns the same sorted snapshot and stops on the same profile detection error.

Run:

```bash
go test -count=1 ./internal/adapter
```

Expected: FAIL to compile while `SkillAdapter` still requires `DetectAgent`, proving the final contract boundary is not yet implemented.

- [ ] **Step 2: Move declarations into the target Adapter files**

Use `apply_patch` moves/splits with no body changes:

```text
adapter.go          -> doc.go + skill.go
skill_adapter.go    -> skill_filesystem.go
frontmatter.go      -> skill_frontmatter.go
mcp_adapter.go      -> mcp.go
plugin_adapter.go   -> plugin.go
config.go           -> agent_profile.go
mcp_<agent>.go      -> <agent>_mcp.go
plugin_<agent>.go   -> <agent>_plugin.go
```

Rename private types exactly:

```text
skillAdapter        -> filesystemSkillAdapter
newSkillAdapter     -> newFilesystemSkillAdapter
agentConfig         -> agentProfile
adaptFunc           -> skillAdaptFunc
```

Keep `cli_runner.go` and every CLI command/parser body unchanged.

- [ ] **Step 3: Remove Agent discovery from Skill Adapter**

Final `SkillAdapter`:

```go
type SkillAdapter interface {
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) (SkillInstallResult, error)
}
```

Move the old detection behavior without semantic changes to:

```go
func (p agentProfile) detectInstalled() (bool, error)
```

Remove `DetectAgent` from `filesystemSkillAdapter` and all unrelated fakes.

- [ ] **Step 4: Replace the Agent-ID switch with declarative registration**

Implement in `registry.go`:

```go
type agentRegistration struct {
	profile   agentProfile
	newSkill  func(agentProfile) SkillAdapter
	newMCP    func(agentCLIRunner) MCPAdapter
	newPlugin func(agentProfile, agentCLIRunner) PluginAdapter
}
```

Define `builtInAgentRegistrations` for Codex, Claude, and Hermes. `NewRegistry` iterates registrations, invokes non-nil factories, and stores profiles separately from Adapter maps. `RefreshInstalled` sorts IDs and calls the matching profile's `detectInstalled`. `ResolveSkill`, `ResolveMCP`, and `ResolvePlugin` keep exact unsupported error text.

- [ ] **Step 5: Align Adapter tests and static file layout**

Move test files to:

```text
skill_test.go
skill_filesystem_test.go
skill_frontmatter_test.go
mcp_test_helpers_test.go
codex_mcp_test.go
claude_mcp_test.go
hermes_mcp_test.go
plugin_test.go
codex_plugin_test.go
claude_plugin_test.go
hermes_plugin_test.go
agent_profile_test.go
registry_test.go
cli_runner_test.go
```

Rename test fixture types only where old production names are embedded. Preserve assertions and fake CLI responses.

Run:

```bash
test -z "$(gofmt -l internal/adapter)"
go test -count=1 ./internal/adapter
go test -race -count=1 ./internal/adapter
go test -count=1 ./...
test ! -e internal/adapter/adapter.go
test ! -e internal/adapter/config.go
test ! -e internal/adapter/mcp_codex.go
test ! -e internal/adapter/plugin_codex.go
! rg -n 'DetectAgent|agentConfig|builtInAgentConfigs|newSkillAdapter|type skillAdapter' internal/adapter --glob '*.go'
git diff --check
```

Expected: every command exits 0; only target Adapter production/test files remain.

- [ ] **Step 6: Commit the Adapter layout**

```bash
git add internal/adapter
git commit -m "refactor(adapter): align power and agent layout"
```

---

### Task 3: Make Cloud and Cache Power Capabilities Explicit

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_client.go`
- Modify: `internal/cloud/local_skill.go`
- Modify: `internal/cloud/local_mcp.go`
- Modify: `internal/cloud/local_plugin.go`
- Create: `internal/cloud/local_skill_test.go`
- Modify: `internal/cloud/local_client_test.go`
- Modify: `internal/cloud/local_mcp_test.go`
- Modify: `internal/cloud/local_plugin_test.go`
- Modify: `internal/cloud/local_cache_integration_test.go`
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/filesystem_test.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/install_plugin.go`
- Modify: lifecycle test fakes implementing Cloud or Cache interfaces
- Modify: `cmd/apm/main.go` and `cmd/apm/main_test.go` only where compilation requires new interfaces

**Interfaces:**
- Consumes: existing Cloud `List/Get/GetMCP/GetPlugin` and Cache `Fetch` behavior.
- Produces: Cloud `DownloadSkill/GetMCP/GetPlugin/DownloadPlugin` and Cache `StageSkill/StagePlugin`.

- [ ] **Step 1: Record the Cloud/Cache characterization baseline**

Run:

```bash
go test -count=1 ./internal/cloud ./internal/cache ./internal/lifecycle ./cmd/apm
rg -n '\.Get\(|\.Fetch\(' internal/cloud internal/cache internal/lifecycle cmd/apm --glob '*.go'
```

Expected: tests PASS; the scan shows generic Cloud/Cache calls that will be replaced without changing order.

- [ ] **Step 2: Split the Cloud interface and LocalClient methods**

Replace `Client.Get` with:

```go
DownloadSkill(domain.PowerRef) error
GetMCP(domain.PowerRef) (domain.MCPServer, error)
GetPlugin(domain.PowerRef) (domain.PluginDefinition, error)
DownloadPlugin(domain.PowerRef) error
```

Rename `getSkill` to exported `DownloadSkill` and `getPlugin` to exported `DownloadPlugin`. Delete the generic `LocalClient.Get` switch from `local_client.go`. Keep `GetMCP`, `GetPlugin`, copies, temp cleanup, errors, and paths unchanged.

Move Skill download tests from `local_client_test.go` into `local_skill_test.go`; preserve all original assertions.

- [ ] **Step 3: Split the Cache staging interface**

Replace `Fetch` with:

```go
StageSkill(domain.PowerRef) (stagingRoot string, err error)
StagePlugin(domain.PowerRef) (stagingRoot string, err error)
```

Implement both through:

```go
func (c *FilesystemCache) stage(ref domain.PowerRef, directory string) (string, error)
```

Pass literal `"skills"` or `"plugins"`; remove the public type switch. Preserve `ErrMiss`, cleanup, copy, and rename behavior.

- [ ] **Step 4: Update lifecycle and all fakes without reordering calls**

For Skill use:

```text
StageSkill -> ErrMiss -> DownloadSkill -> StageSkill
```

For Hermes Plugin use:

```text
StagePlugin -> ErrMiss -> DownloadPlugin -> StagePlugin
```

MCP continues to use only `GetMCP`; Codex/Claude Plugin continues to use only `GetPlugin`. Update fakes to record distinct method names while retaining event sequence assertions.

- [ ] **Step 5: Verify explicit resource capabilities**

Run:

```bash
gofmt -w internal/cloud internal/cache internal/lifecycle cmd/apm
go test -count=1 ./internal/cloud ./internal/cache ./internal/lifecycle ./cmd/apm
go test -count=1 ./...
! rg -n 'func \([^)]*LocalClient\) Get\(|\.cloud\.Get\(|\.cache\.Fetch\(' internal cmd --glob '*.go'
rg -n 'DownloadSkill|DownloadPlugin|StageSkill|StagePlugin' internal cmd --glob '*.go'
git diff --check
```

Expected: negative scan has no matches; positive scan shows interface, implementation, lifecycle, and tests; all tests PASS.

- [ ] **Step 6: Commit Cloud/Cache capability names**

```bash
git add internal/cloud internal/cache internal/lifecycle cmd/apm
git commit -m "refactor(resource): expose power-specific staging"
```

---

### Task 4: Align Lifecycle Command Files and Dependency Ownership

**Files:**
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/search.go`
- Modify: `internal/lifecycle/list.go`
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_agent.go`
- Create: `internal/lifecycle/install_skill.go`
- Modify: `internal/lifecycle/install_mcp.go`
- Modify: `internal/lifecycle/install_plugin.go`
- Modify: `internal/lifecycle/uninstall.go`
- Modify: `internal/lifecycle/uninstall_agent.go`
- Create: `internal/lifecycle/uninstall_skill.go`
- Modify: `internal/lifecycle/uninstall_mcp.go`
- Modify: `internal/lifecycle/uninstall_plugin.go`
- Create: `internal/lifecycle/install_test_helpers_test.go`
- Create: `internal/lifecycle/install_skill_test.go`
- Create: `internal/lifecycle/uninstall_test_helpers_test.go`
- Create: `internal/lifecycle/uninstall_skill_test.go`
- Modify/split: existing Install/Uninstall tests according to the accepted target map

**Interfaces:**
- Consumes: Task 1 `SkillAdapter`, Task 3 Cloud/Cache methods, existing transaction method names until Task 5.
- Produces: command-first lifecycle layout with `installForAgent`, `uninstallForAgent`, and explicit Skill/MCP/Plugin handlers.

- [ ] **Step 1: Record lifecycle test and symbol inventory**

Run:

```bash
go test -count=1 ./internal/lifecycle
rg -n '^func Test' internal/lifecycle/*_test.go > /tmp/apm-feature-015-lifecycle-tests.before
rg -n 'type (ConfigRepository|TransactionManager|Locker|PowerCache|CloudClient)' internal/lifecycle/dependencies.go
```

Expected: tests PASS; five dependency aliases are present before removal.

- [ ] **Step 2: Remove dependency aliases and use source package interfaces**

Keep only:

```go
type AdapterRegistry interface {
	ResolveSkill(domain.AgentID) (adapter.SkillAdapter, error)
	ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
	ResolvePlugin(domain.AgentID) (adapter.PluginAdapter, error)
	RefreshInstalled() (domain.AgentSnapshot, error)
}

type VersionSelector interface {
	Select([]string, string) (string, error)
}
```

Change Service fields and constructor parameters to package-qualified `lock.Locker`, `transaction.Manager`, `cloud.Client`, `cache.PowerCache`, and `config.Repository`. Preserve parameter order, nil checks, and exact error strings.

- [ ] **Step 3: Move Skill Install into its own handler file**

`install_agent.go` keeps only Power dispatch and renames:

```go
func (s *InstallService) installForAgent(ctx context.Context, agent domain.AgentID, ref domain.PowerRef) (cli.InstallItem, error)
```

Move `installSkill` plus `stageSkill` to `install_skill.go`. `install_mcp.go` and `install_plugin.go` remain type-specific; `install_plugin.go` owns `stagePlugin`. Keep exact Config/Cloud/Cache/Adapter/Transaction order and outcomes.

- [ ] **Step 4: Move Skill Uninstall into its own handler file**

`uninstall_agent.go` keeps manifest resolution, Power dispatch, result classification, and fact Warn helpers. Rename:

```go
func (s *UninstallService) uninstallForAgent(ctx context.Context, agent domain.AgentID, flags cli.Flags) (cli.UninstallItem, error)
```

Move `uninstallSkill` to `uninstall_skill.go`. Keep MCP Cloud access and Plugin offline behavior in their existing Power files.

- [ ] **Step 5: Split lifecycle fixtures and tests by responsibility**

Move only shared fakes/assertions into `install_test_helpers_test.go` and `uninstall_test_helpers_test.go`. Move Skill-specific cases into `install_skill_test.go` and `uninstall_skill_test.go`. Keep main orchestration, Agent dispatch, resolve, MCP, Plugin, and integration cases in their named files.

After moves, run:

```bash
rg -n '^func Test' internal/lifecycle/*_test.go > /tmp/apm-feature-015-lifecycle-tests.after
cut -d: -f3- /tmp/apm-feature-015-lifecycle-tests.before | sort > /tmp/apm-feature-015-lifecycle-names.before
cut -d: -f3- /tmp/apm-feature-015-lifecycle-tests.after | sort > /tmp/apm-feature-015-lifecycle-names.after
diff -u /tmp/apm-feature-015-lifecycle-names.before /tmp/apm-feature-015-lifecycle-names.after
```

Expected: no test function is lost. Rename a test only if its old production symbol is embedded; record such one-to-one rename in the task report before committing.

- [ ] **Step 6: Verify lifecycle layout and behavior**

Run:

```bash
test -z "$(gofmt -l internal/lifecycle)"
go test -count=1 ./internal/lifecycle
go test -race -count=1 ./internal/lifecycle
go test -count=1 ./...
test -e internal/lifecycle/install_skill.go
test -e internal/lifecycle/uninstall_skill.go
! rg -n 'type (ConfigRepository|TransactionManager|Locker|PowerCache|CloudClient)' internal/lifecycle --glob '*.go'
! rg -n 'installOne|uninstallOne|fetchInstallStaging' internal/lifecycle --glob '*.go'
git diff --check
```

Expected: all checks PASS and integration tests remain fake-bin isolated.

- [ ] **Step 7: Commit lifecycle organization**

```bash
git add internal/lifecycle
git commit -m "refactor(lifecycle): align power handlers"
```

---

### Task 5: Make the Skill Transaction Driver Explicit

**Files:**
- Create: `internal/transaction/doc.go`
- Modify: `internal/transaction/manager.go`
- Create: `internal/transaction/skill_install.go`
- Create: `internal/transaction/skill_uninstall.go`
- Create: `internal/transaction/skill_recovery.go`
- Create: `internal/transaction/skill_artifacts.go`
- Create: `internal/transaction/skill_path_state.go`
- Modify: `internal/transaction/recovery.go`
- Delete: `internal/transaction/install.go`
- Delete: `internal/transaction/uninstall.go`
- Delete: `internal/transaction/artifacts.go`
- Delete: `internal/transaction/path_state.go`
- Rename/split corresponding Skill transaction tests
- Modify: `internal/lifecycle/install_skill.go`
- Modify: `internal/lifecycle/uninstall_skill.go`
- Modify: lifecycle transaction fakes and tests

**Interfaces:**
- Consumes: existing filesystem Skill transaction behavior and shared schema-v1 journal.
- Produces: `InstallSkill`, `UninstallSkill`, `SkillInstallRequest`, `SkillUninstallRequest`, and explicit filesystem Skill recovery files.

- [ ] **Step 1: Record transaction baseline and journal contract**

Run:

```bash
go test -count=1 ./internal/transaction ./internal/lifecycle
rg -n 'SchemaVersion|json:"(filesystem|agent_cli|plugin)' internal/transaction/journal.go
rg -n '^func Test' internal/transaction/*_test.go > /tmp/apm-feature-015-transaction-tests.before
```

Expected: tests PASS and journal scan records schema v1 with all three existing payload fields.

- [ ] **Step 2: Rename Manager Skill methods and requests across consumers**

Final Manager interface:

```go
type Manager interface {
	Recover(context.Context) (RecoveryResult, error)
	InstallSkill(context.Context, SkillInstallRequest) error
	UninstallSkill(context.Context, SkillUninstallRequest) (UninstallResult, error)
	InstallMCP(context.Context, MCPInstallRequest) error
	UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)
	InstallPlugin(context.Context, PluginInstallRequest) error
	UninstallPlugin(context.Context, PluginUninstallRequest) (UninstallResult, error)
}
```

Rename `InstallRequest` to `SkillInstallRequest`, `UninstallRequest` to `SkillUninstallRequest`, `Service.Install` to `Service.InstallSkill`, and `Service.Uninstall` to `Service.UninstallSkill`. Update lifecycle and all fakes directly; do not leave old wrappers or aliases.

- [ ] **Step 3: Move Skill filesystem operation files**

Use `apply_patch` moves without changing bodies:

```text
install.go    -> skill_install.go
uninstall.go  -> skill_uninstall.go
artifacts.go  -> skill_artifacts.go
path_state.go -> skill_path_state.go
```

Move the transaction package comment from `manager.go` to `doc.go`. Add concise Chinese comments to renamed exported methods and request types.

- [ ] **Step 4: Separate shared recovery dispatch from Skill recovery**

`recovery.go` must retain only common journal inspection/validation, driver routing, recovery-direction persistence, shared result initialization, and completed journal deletion. Move filesystem-specific path inspection, install/uninstall convergence, artifact validation, and filesystem cleanup to `skill_recovery.go`.

The dispatch remains semantically identical:

```go
switch journal.Driver {
case DriverFilesystem:
	err = s.recoverSkill(ctx, journal, &result)
case DriverAgentCLI:
	err = s.recoverMCP(ctx, journal, &result)
case DriverPlugin:
	err = s.recoverPlugin(ctx, journal, &result)
}
```

Do not change driver values, phase decisions, action order, or journal deletion rules.

- [ ] **Step 5: Align transaction tests**

Rename:

```text
install_test.go    -> skill_install_test.go
uninstall_test.go  -> skill_uninstall_test.go
artifacts_test.go  -> skill_artifacts_test.go
path_state_test.go -> skill_path_state_test.go
```

Move filesystem recovery tests from `recovery_test.go` to `skill_recovery_test.go`; keep common no-journal, invalid-journal, routing, and Manager construction tests in `recovery_test.go` or `manager_test.go` according to responsibility. Preserve every assertion and failure injection.

- [ ] **Step 6: Verify Skill driver naming and schema preservation**

Run:

```bash
gofmt -w internal/transaction internal/lifecycle
go test -count=1 ./internal/transaction ./internal/lifecycle
go test -race -count=1 ./internal/transaction ./internal/lifecycle
go test -count=1 ./...
! rg -n '\bInstallRequest\b|\bUninstallRequest\b|func \([^)]*\) Install\(|func \([^)]*\) Uninstall\(' internal/transaction internal/lifecycle --glob '*.go'
rg -n 'InstallSkill|UninstallSkill|SkillInstallRequest|SkillUninstallRequest' internal/transaction internal/lifecycle --glob '*.go'
test ! -e internal/transaction/install.go
test ! -e internal/transaction/uninstall.go
test ! -e internal/transaction/artifacts.go
test ! -e internal/transaction/path_state.go
git diff --check
```

Then compare journal schema output from Step 1. Expected: only naming/file location changes; JSON tags and schema remain byte-for-byte identical.

- [ ] **Step 7: Commit explicit Skill driver organization**

```bash
git add internal/transaction internal/lifecycle
git commit -m "refactor(transaction): name skill driver explicitly"
```

---

### Task 6: Split Transaction Validation and Remove the Dead Domain Result

**Files:**
- Modify: `internal/transaction/validation.go`
- Create: `internal/transaction/skill_validation.go`
- Create: `internal/transaction/mcp_validation.go`
- Create: `internal/transaction/plugin_validation.go`
- Modify: `internal/transaction/validation_test.go`
- Create: `internal/transaction/skill_validation_test.go`
- Create: `internal/transaction/mcp_validation_test.go`
- Create: `internal/transaction/plugin_validation_test.go`
- Modify: `internal/transaction/mcp_install_test.go` where request validation tests move
- Modify: `internal/transaction/journal_schema_test.go` where type-specific validation tests move
- Modify: `internal/domain/power.go`
- Modify: `internal/domain/domain_test.go`
- Delete: `internal/domain/result.go`

**Interfaces:**
- Consumes: Task 5 request names and unchanged journal schema.
- Produces: common validation primitives plus explicit Skill/MCP/Plugin request and payload validation files; no generic domain Result.

- [ ] **Step 1: Record validation and dead-code facts**

Run:

```bash
go test -count=1 ./internal/transaction ./internal/domain
rg -n '\bdomain\.(Result|AgentResult|ResultStatus|StatusCompleted|StatusFailed|StatusUnexecuted)\b' cmd internal --glob '*.go'
rg -n '^func validate|^func valid|^func isNilInterface' internal/transaction/validation.go
```

Expected: tests PASS; domain Result references exist only in `domain/result.go` and its isolated test; validation functions are currently mixed in one file.

- [ ] **Step 2: Split validation by Power without changing function bodies**

Move:

```text
SkillInstall/SkillUninstall request + filesystem payload checks -> skill_validation.go
MCP request + agent-cli payload checks                          -> mcp_validation.go
Plugin request + Plugin/filesystem payload checks              -> plugin_validation.go
Envelope dispatch, identity, phase, transaction ID,
absolute/clean path and exact-artifact primitives               -> validation.go
```

Keep function call order and error construction unchanged. Add Chinese comments only to significant validation boundaries, especially invalid journal preservation.

- [ ] **Step 3: Split validation tests by the same ownership**

Move each existing test table with its production validator. Keep common sentinel/envelope tests in `validation_test.go`, schema JSON decoding in `journal_schema_test.go`, and operation behavior tests in operation files. Do not weaken exact error or `errors.Is` assertions.

- [ ] **Step 4: Remove unused domain Result and normalize Power declaration order**

Delete `result.go`. Remove only the Result construction from `TestSharedTypesCompile`; retain PowerRef/PowerGroups assertions. Reorder `PowerType` declarations to:

```go
PowerTypeSkill
PowerTypeMCP
PowerTypePlugin
```

Do not change string values or manifest field order.

- [ ] **Step 5: Verify validation and domain cleanup**

Run:

```bash
gofmt -w internal/transaction internal/domain
go test -count=1 ./internal/transaction ./internal/domain
go test -count=1 ./...
test ! -e internal/domain/result.go
! rg -n '\b(ResultStatus|AgentResult|StatusCompleted|StatusFailed|StatusUnexecuted)\b' internal/domain --glob '*.go'
test "$(rg -l '^func validateSkill' internal/transaction --glob '*.go' | sort -u | tr '\n' ' ')" = "internal/transaction/skill_validation.go "
rg -n 'SchemaVersion:\s*1|SchemaVersion != 1|json:"(filesystem|agent_cli|plugin)' internal/transaction --glob '*.go'
git diff --check
```

Expected: all checks PASS; schema scan still shows only schema v1 and existing payload tags.

- [ ] **Step 6: Commit validation and dead-code cleanup**

```bash
git add internal/transaction internal/domain
git commit -m "refactor(transaction): separate driver validation"
```

---

### Task 7: Audit Comments, Review the Refactor, Verify Everything, and Record Changelog

**Files:**
- Modify: major interface and business-flow files from Tasks 1-6 only where Chinese comments are missing or inaccurate
- Modify: `cmd/apm/main.go` and `cmd/apm/main_test.go` for any remaining compile-only name synchronization
- Modify: `docs/Changelog.md`
- Do not modify: ADR, C4, `BUILD.md`, or `ivory-tower/project-overview.md`

**Interfaces:**
- Consumes: all target interfaces and layouts from Tasks 1-6.
- Produces: reviewed, fully verified behavior-neutral refactor plus one completed-task Changelog entry.

- [ ] **Step 1: Run the target-layout and old-symbol audit**

Run:

```bash
rg --files internal/adapter internal/lifecycle internal/transaction | sort
! rg -n '\badapter\.Adapter\b|\badapter\.InstallResult\b|\btransaction\.InstallRequest\b|\btransaction\.UninstallRequest\b' cmd internal --glob '*.go'
! rg -n 'func \([^)]*LocalClient\) Get\(|\.cache\.Fetch\(|installOne|uninstallOne|fetchInstallStaging|DetectAgent|agentConfig|builtInAgentConfigs' cmd internal --glob '*.go'
! test -e internal/domain/result.go
! test -e internal/transaction/install.go
! test -e internal/transaction/uninstall.go
```

Expected: file list exactly matches the accepted spec; all negative scans exit 0.

- [ ] **Step 2: Audit and complete Chinese comments**

Inspect exported declarations and these business boundaries:

```text
Adapter contracts and agentRegistration
Cloud Download* and Cache Stage*
Search/List/Install/Uninstall lock and result boundaries
install/uninstall Skill, MCP, Plugin handlers
Transaction Manager entries and recovery driver dispatch
journal validation fail-closed behavior
Plugin offline uninstall, MCP no-cache, Hermes conditional cache
```

Add only concise Chinese comments that explain purpose, ordering, ownership, or prohibition. Confirm comments do not claim behavior absent from tests/spec.

- [ ] **Step 3: Request and apply code review before final verification**

Use `superpowers:requesting-code-review` against the complete feature diff from commit `5b50685` through HEAD. Review for:

```text
Critical: behavior, journal schema, CLI, real-Agent isolation regressions
Important: wrong module ownership, leftover generic Skill names, incomplete file matrix, inaccurate Chinese comments
Minor: naming/readability issues within accepted scope
```

Apply valid findings with `superpowers:receiving-code-review`, rerun affected focused tests, and commit fixes with a scoped `fix(refactor): ...` message. Do not expand into unrelated refactoring.

- [ ] **Step 4: Run final formatting, unit, race, vet, build, and diff verification**

Run fresh commands:

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
verify_root="$(mktemp -d)"
verify_bin="$verify_root/apm"
go build -o "$verify_bin" ./cmd/apm
test -x "$verify_bin"
git diff --check
```

Expected: every command exits 0. The temporary directory is explicit and contains only the verification binary.

- [ ] **Step 5: Prove fake CLI and filesystem isolation**

Run the MCP and Plugin lifecycle integration suites verbosely:

```bash
go test -count=1 -v ./internal/lifecycle -run 'Test(MCP|Plugin)'
```

Inspect assertions confirming executed paths are within each test's fake-bin. Also run:

```bash
git status --short
```

Expected: no Agent home/config changes appear; only intended feature files are modified before the final commit.

- [ ] **Step 6: Perform a requirement-by-requirement completion audit**

Compare every acceptance criterion in `spec.md` with current files and command evidence. Record any missing proof as incomplete and continue fixing; do not treat passing unit tests alone as proof of layout, schema, comments, or isolation requirements.

- [ ] **Step 7: Add the single final Changelog entry**

Use the template from `~/.codex/docs/Changelog.md`. Add a reverse-chronological `2026-08-14` entry with:

```text
分类：其他
用户需求：规范 Skill/MCP/Plugin 与 Codex/Claude/Hermes 的文件、接口和职责布局，保持现有功能。
相关提交：Feature 015 spec、plan、Tasks 1-6 and review-fix commits; omit the Changelog commit itself.
解决方案或完成结果：explicit Skill names, declarative Agent registration, type-specific Cloud/Cache, command-first lifecycle, driver-first transaction, dead Result removal, Chinese comments.
验证结果或限制：exact final commands, fake-bin isolation, behavior-neutral scope, no ADR/C4 changes.
```

Use actual hashes and summaries from:

```bash
git log --oneline 5b50685^..HEAD
```

- [ ] **Step 8: Commit Changelog and rerun completion checks**

```bash
git add docs/Changelog.md
git commit -m "docs(changelog): record structure normalization"
git diff --check HEAD^
go test -count=1 ./...
git status --short --branch
```

Expected: Changelog is committed once, tests PASS, and the feature worktree is clean.

---

## Completion Evidence Matrix

| Requirement | Authoritative evidence |
|---|---|
| Power and Agent file layout | `rg --files` target comparison and absence of old files |
| Explicit Skill names | negative old-symbol scans plus Go compilation |
| Agent discovery independent of Skill | final `SkillAdapter` method set and Registry/profile tests |
| Declarative Agent extension | `agentRegistration`, no Agent-ID construction switch, Registry tests |
| Cloud/Cache type-specific methods | interfaces, implementation calls, negative generic-method scans |
| Lifecycle command-first handlers | target files and focused lifecycle tests |
| Transaction driver-first organization | target files, Manager interface, recovery routing tests |
| Journal unchanged | schema/tag scans, schema tests, recovery tests |
| Behavior unchanged | full unit/race/vet/build plus existing exact order/error/output tests |
| Real Agent environment untouched | fake-bin integration assertions and clean worktree |
| Chinese comments | manual exported-interface/business-flow audit against spec |
| Dead domain Result removed | file absence and symbol scan |
| Changelog recorded once | final reverse-chronological entry with actual commit list |

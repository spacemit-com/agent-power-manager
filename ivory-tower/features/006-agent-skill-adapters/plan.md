# Codex、Claude Code and Hermes Agent Skill Adapters Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement safe global Skill adapters for Codex, Claude Code, and Hermes Agent, from public staging adaptation through atomic installation and staging cleanup.

**Architecture:** A shared `skillAdapter` owns the complete single-Agent installation pipeline while three immutable `agentConfig` values supply root-directory and future adaptation differences. The Registry resolves only Skill adapters and exposes stable installed-Agent snapshots; lifecycle, config manifests, transaction backups, uninstall, Plugin, and MCP remain outside this feature.

**Tech Stack:** Go 1.26.5, standard library filesystem APIs, `go.yaml.in/yaml/v3`, existing `internal/utils/filesystem.CopyDir`, package-level `slog`, Go `testing`.

## Global Constraints

- Supported IDs are exactly `codex`, `claude-code`, and `hermes-agent`; Registry order is exactly that order.
- This feature supports global Skill installation only. Do not implement Plugin, MCP, uninstall, project-local installation, symlink installation, lifecycle orchestration, manifest changes, journal, backup, or recovery.
- Root precedence is `CODEX_HOME` / `CLAUDE_CONFIG_DIR` / `HERMES_HOME` after trimming whitespace, then `paths.HomeDir()` plus `.codex` / `.claude` / `.hermes`.
- Agent detection checks only that the selected Agent root exists and resolves to a directory. Do not detect Codex through `/etc/codex`; do not create a missing Agent root.
- `TargetPath` owns Agent path rules and performs no filesystem mutation. Transaction code must not duplicate target path construction.
- `Install` accepts only the absolute clean equivalent of `paths.StagingPath()` and must never pass an invalid, empty, mismatched, or filesystem-root path to `os.RemoveAll`.
- On every valid-staging success or failure path, remove the whole public staging root. Remove an unpublished target temporary directory before removing staging, and join cleanup errors with the primary error.
- Check the final target with `Lstat` before reading/adapting staging and again before rename. Any existing entry is `ErrTargetConflict` and must not be overwritten.
- Create a missing `<agent-root>/skills` with one-level `os.Mkdir(0o755)`, never recursive creation that could recreate the Agent root.
- Install by copying to a unique hidden temporary directory inside the target Skill root, preserving dotfiles and permission bits, then rename on the same filesystem. Never leave a symlink to staging.
- `Install` accepts only `PowerTypeSkill` with trim-nonempty `Name` and `Version`; names must be safe raw single path segments and are never sanitized or rewritten.
- `SKILL.md` must contain first-byte YAML frontmatter with string, trim-nonempty `name` and `description`; raw `name` must equal `PowerRef.Name`; unknown fields and LF/CRLF are allowed; validation never rewrites the file.
- Use only package-level `slog.Debug` inside adapter code. Do not print, create a private logger, or log Info/Warn/Error.
- Tests that mutate environment variables or injectable filesystem functions must not call `t.Parallel()`.
- Source behavior changes require one reverse-chronological `docs/Changelog.md` entry for this user request after all implementation commits pass verification.

---

## File Structure

- `internal/domain/agent.go`: formal Agent IDs and immutable snapshot behavior.
- `internal/domain/domain_test.go`: domain and snapshot contract tests; remove obsolete `domain.Power` coverage.
- `internal/domain/power.go`: remove the empty placeholder `Power` type.
- `internal/adapter/adapter.go`: minimal exported Adapter interface.
- `internal/adapter/errors.go`: exported sentinel error categories.
- `internal/adapter/config.go`: built-in Agent configurations, root resolution, detection, and target paths.
- `internal/adapter/frontmatter.go`: delimiter extraction and YAML-node validation.
- `internal/adapter/skill_adapter.go`: shared adaptation, copy, atomic publication, and cleanup pipeline.
- `internal/adapter/registry.go`: Skill-only resolution and ordered installed snapshot refresh.
- `internal/adapter/*_test.go`: focused contract, config, frontmatter, pipeline, and Registry tests.
- `internal/lifecycle/dependencies.go`: compile against the narrowed Adapter interface; no lifecycle behavior.
- `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`: implemented interface and shared adapter view.
- `docs/Changelog.md`: one completion record after all source behavior is verified.

### Task 1: Formalize Agent IDs and snapshot semantics

**Files:**
- Modify: `internal/domain/agent.go:4-27`
- Modify: `internal/domain/domain_test.go`

**Interfaces:**
- Consumes: existing `AgentID` and `AgentSnapshot`.
- Produces: `AgentCodex`, `AgentClaudeCode`, `AgentHermes`; `Contains(AgentID) bool`; `Ordered() []AgentID` defensive copy.

- [ ] **Step 1: Write failing domain tests**

```go
func TestAgentIDsAreStable(t *testing.T) {
	if domain.AgentCodex != "codex" || domain.AgentClaudeCode != "claude-code" || domain.AgentHermes != "hermes-agent" {
		t.Fatalf("agent IDs = %q, %q, %q", domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes)
	}
}

func TestAgentSnapshotContainsAndOrderedCopy(t *testing.T) {
	snapshot := domain.AgentSnapshot{Agents: []domain.AgentID{
		domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes,
	}}
	if !snapshot.Contains(domain.AgentClaudeCode) || snapshot.Contains("unknown") {
		t.Fatal("Contains returned an incorrect result")
	}
	got := snapshot.Ordered()
	got[0] = "changed"
	if snapshot.Agents[0] != domain.AgentCodex {
		t.Fatal("Ordered exposed snapshot storage")
	}
}
```

- [ ] **Step 2: Run the focused tests and confirm failure**

Run: `go test ./internal/domain -run 'TestAgent(IDs|Snapshot)' -count=1 -v`

Expected: FAIL because the constants do not exist and snapshot methods are placeholders.

- [ ] **Step 3: Implement constants and snapshot methods**

```go
const (
	AllAgents       AgentID = "all"
	AgentCodex      AgentID = "codex"
	AgentClaudeCode AgentID = "claude-code"
	AgentHermes     AgentID = "hermes-agent"
)

func (s AgentSnapshot) Contains(agent AgentID) bool {
	for _, candidate := range s.Agents {
		if candidate == agent { return true }
	}
	return false
}

func (s AgentSnapshot) Ordered() []AgentID {
	return append([]AgentID(nil), s.Agents...)
}
```

- [ ] **Step 4: Run domain tests**

Run: `go test ./internal/domain -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the domain unit**

```bash
git add internal/domain/agent.go internal/domain/domain_test.go
git commit -m "feat(domain): define supported agent snapshots"
```

### Task 2: Narrow the Adapter contract and define errors

**Files:**
- Modify: `internal/adapter/adapter.go:1-12`
- Create: `internal/adapter/adapter_test.go`
- Create: `internal/adapter/errors.go`
- Modify: `internal/domain/power.go`
- Modify: `internal/domain/domain_test.go:9-17`
- Verify: `internal/lifecycle/dependencies.go:13-17`

**Interfaces:**
- Consumes: `domain.PowerRef`.
- Produces: exact Adapter contract and `ErrAgentNotInstalled`, `ErrUnsupportedAdapter`, `ErrTargetConflict`, `ErrInvalidSkill`.

- [ ] **Step 1: Add a compile-time contract test**

```go
type contractAdapter struct{}
func (contractAdapter) DetectAgent() (bool, error) { return true, nil }
func (contractAdapter) TargetPath(domain.PowerRef) (string, error) { return "/target", nil }
func (contractAdapter) Install(domain.PowerRef, string) error { return nil }

func TestAdapterContractAndErrors(t *testing.T) {
	var _ adapter.Adapter = contractAdapter{}
	for _, err := range []error{adapter.ErrAgentNotInstalled, adapter.ErrUnsupportedAdapter, adapter.ErrTargetConflict, adapter.ErrInvalidSkill} {
		if err == nil { t.Fatal("adapter sentinel is nil") }
	}
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/adapter -run TestAdapterContractAndErrors -count=1 -v`

Expected: FAIL because the new method and errors do not exist.

- [ ] **Step 3: Replace the interface and add sentinels**

```go
type Adapter interface {
	DetectAgent() (bool, error)
	TargetPath(domain.PowerRef) (string, error)
	Install(domain.PowerRef, string) error
}
```

```go
var (
	ErrAgentNotInstalled  = errors.New("agent is not installed")
	ErrUnsupportedAdapter = errors.New("unsupported adapter")
	ErrTargetConflict     = errors.New("target path already exists")
	ErrInvalidSkill       = errors.New("invalid skill")
)
```

Delete empty `domain.Power` and its compile assertion. Do not add uninstall behavior or change the lifecycle Registry interface.

- [ ] **Step 4: Run compile and focused tests**

Run: `go test ./internal/adapter ./internal/domain ./internal/lifecycle -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the contract unit**

```bash
git add internal/adapter/adapter.go internal/adapter/adapter_test.go internal/adapter/errors.go internal/domain/power.go internal/domain/domain_test.go
git commit -m "refactor(adapter): define skill installation contract"
```

### Task 3: Resolve Agent roots, detection, and target paths

**Files:**
- Create: `internal/adapter/config.go`
- Create: `internal/adapter/config_test.go`

**Interfaces:**
- Consumes: `paths.HomeDir`, supported Agent constants, `domain.PowerRef`.
- Produces: `agentConfig`, `builtInAgentConfigs`, `rootPath`, `detectAgent`, `targetPath`.

- [ ] **Step 1: Write table-driven path tests**

Use this exact matrix without `t.Parallel()`:

```go
tests := []struct { name, env, defaultDir string; agent domain.AgentID }{
	{"codex", "CODEX_HOME", ".codex", domain.AgentCodex},
	{"claude", "CLAUDE_CONFIG_DIR", ".claude", domain.AgentClaudeCode},
	{"hermes", "HERMES_HOME", ".hermes", domain.AgentHermes},
}
```

For each row assert a trimmed override wins, an all-space override falls back to `filepath.Join(APM_HOME, defaultDir)`, missing root is false/nil, a directory and symlink-to-directory are true, a regular file wraps `ErrAgentNotInstalled`, and missing `skills` does not affect detection. Assert `targetPath` returns `<root>/skills/demo`, mutates nothing, and rejects non-Skill, empty, absolute, `.`, `..`, `/`, and `\\` names with `ErrInvalidSkill`.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/adapter -run 'TestAgentConfig|TestTargetPath' -count=1 -v`

Expected: FAIL because Agent configuration does not exist.

- [ ] **Step 3: Implement immutable configurations**

```go
type adaptFunc func(domain.PowerRef, string) error
type agentConfig struct {
	id domain.AgentID
	homeEnv string
	defaultRoot string
	adapt adaptFunc
}

var builtInAgentConfigs = []agentConfig{
	{id: domain.AgentCodex, homeEnv: "CODEX_HOME", defaultRoot: ".codex", adapt: noOpAdapt},
	{id: domain.AgentClaudeCode, homeEnv: "CLAUDE_CONFIG_DIR", defaultRoot: ".claude", adapt: noOpAdapt},
	{id: domain.AgentHermes, homeEnv: "HERMES_HOME", defaultRoot: ".hermes", adapt: noOpAdapt},
}
func noOpAdapt(domain.PowerRef, string) error { return nil }
```

`rootPath` uses the trimmed override value. `detectAgent` uses `os.Stat`, maps only `os.ErrNotExist` to false/nil, maps non-directory to `ErrAgentNotInstalled`, and wraps other errors with Agent/path. `targetPath` validates a safe raw single path segment but never stats or creates it.

- [ ] **Step 4: Run config tests**

Run: `go test ./internal/adapter -run 'TestAgentConfig|TestTargetPath' -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit Agent configuration**

```bash
git add internal/adapter/config.go internal/adapter/config_test.go
git commit -m "feat(adapter): resolve agent skill targets"
```

### Task 4: Parse and validate YAML frontmatter

**Files:**
- Create: `internal/adapter/frontmatter.go`
- Create: `internal/adapter/frontmatter_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Consumes: `ErrInvalidSkill`, expected raw Skill name, `SKILL.md` bytes.
- Produces: `validateSkillFrontmatter(path, expectedName string) error`.

- [ ] **Step 1: Write the acceptance matrix**

Accept LF, CRLF, unknown fields, empty body, and block description. Reject missing delimiters, leading bytes, malformed YAML, sequence/scalar root, duplicate `name` or `description`, missing fields, numeric/bool/null fields, whitespace-only fields, and raw name mismatch.

```go
valid := "---\nname: demo\ndescription: |\n  first line\n  second line\nmetadata:\n  owner: apm\n---\n# Body\n"
wrongType := "---\nname: 42\ndescription: true\n---\n"
mismatch := "---\nname: Demo\ndescription: valid\n---\n"
```

Assert every invalid case matches `ErrInvalidSkill`, and successful validation leaves file bytes unchanged.

- [ ] **Step 2: Add YAML dependency and confirm failure**

```bash
go get go.yaml.in/yaml/v3
go test ./internal/adapter -run TestValidateSkillFrontmatter -count=1 -v
```

Expected: dependency resolves; test FAILS because validator is absent.

- [ ] **Step 3: Implement YAML-node validation**

Recognize only a first-byte `---\n` or `---\r\n`, locate a closing standalone delimiter with the same newline form, decode between delimiters into `yaml.Node`, require document → mapping, and locate exactly one string `name` and `description`. Both values must trim nonempty and raw name must equal `expectedName`; unknown keys are allowed.

```go
func validateSkillFrontmatter(path, expectedName string) error {
	raw, err := os.ReadFile(path)
	if err != nil { return fmt.Errorf("read skill frontmatter %q: %w", path, err) }
	yamlBytes, err := extractYAMLFrontmatter(raw)
	if err != nil { return errors.Join(ErrInvalidSkill, err) }
	return validateFrontmatterNode(yamlBytes, expectedName)
}
```

Preserve YAML decoder causes, do not use KnownFields, and never rewrite the file.

- [ ] **Step 4: Run parser tests**

Run: `go test ./internal/adapter -run TestValidateSkillFrontmatter -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit parser and dependency**

```bash
git add go.mod go.sum internal/adapter/frontmatter.go internal/adapter/frontmatter_test.go
git commit -m "feat(adapter): validate skill frontmatter"
```

### Task 5: Implement the basic shared installation pipeline

**Files:**
- Create: `internal/adapter/skill_adapter.go`
- Create: `internal/adapter/skill_adapter_test.go`

**Interfaces:**
- Consumes: `agentConfig`, `validateSkillFrontmatter`, `paths.StagingPath`, `filesystem.CopyDir`.
- Produces: concrete `skillAdapter` implementing `Adapter`, safe staging identity, initial conflict protection, copy publication, and normal cleanup.

- [ ] **Step 1: Write happy-path and initial-conflict tests**

Build every test under `APM_HOME=t.TempDir()` with one selected Agent root and staging returned by `paths.StagingPath()`. A helper writes valid `SKILL.md`, `.hidden`, executable `scripts/run.sh`, and a read-only nested directory.

Assert all of these exact behaviors:

- success creates `<root>/skills/demo`, preserves bytes and permission bits, creates no symlink, and removes staging and `.apm-demo-*` temporary directories;
- missing Agent root returns `ErrAgentNotInstalled`, does not create it, and removes valid staging;
- missing `skills` is created with one-level `Mkdir` and install succeeds;
- invalid refs are rejected after valid-staging cleanup;
- empty, root, or mismatched staging is rejected without deleting that path;
- pre-existing target directory, file, and symlink each return `ErrTargetConflict`, preserve sentinel content, remove staging, and do not call the adapt hook;
- malformed frontmatter returns `ErrInvalidSkill`, creates no final target, and removes staging.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/adapter -run 'TestSkillAdapterInstall|TestSkillAdapterConflict' -count=1 -v`

Expected: FAIL because `skillAdapter.Install` does not exist.

- [ ] **Step 3: Implement the shared pipeline**

Use narrow function dependencies, not a general filesystem interface:

```go
type skillAdapter struct {
	config    agentConfig
	copyDir   func(string, string) error
	rename    func(string, string) error
	removeAll func(string) error
}

func newSkillAdapter(config agentConfig) *skillAdapter {
	return &skillAdapter{
		config: config,
		copyDir: filesystem.CopyDir,
		rename: os.Rename,
		removeAll: os.RemoveAll,
	}
}
```

`DetectAgent` and `TargetPath` delegate to config. `Install` uses a named result error and this order:

```text
validate expected staging identity (no cleanup before it passes)
defer staging cleanup
validate versioned Skill ref
stat Agent root
Lstat final target; conflict before frontmatter/adapt
validate SKILL.md frontmatter
run config.adapt(ref, sourceDir)
create/check skills root with one-level Mkdir
MkdirTemp(skillsRoot, ".apm-"+name+"-*")
defer unpublished temp cleanup
copyDir(sourceDir, tempDir)
Lstat final target again; classify any new entry as ErrTargetConflict
rename(tempDir, target)
mark temp as published
```

Compare staging paths after `filepath.Abs(filepath.Clean(...))`; reject any value equal to its filesystem volume root. Cleanup uses only the validated absolute path. `ensureTargetAbsent` uses `os.Lstat`, maps every existing entry to `ErrTargetConflict`, treats only `os.ErrNotExist` as absence, and wraps other errors.

For Skill root creation, call only `os.Mkdir(skillsRoot, 0o755)`. On `os.ErrExist`, follow with `os.Stat` and require a directory. Do not call `MkdirAll`.

- [ ] **Step 4: Run basic pipeline tests**

Run: `go test ./internal/adapter -run 'TestSkillAdapterInstall|TestSkillAdapterConflict' -count=1 -v`

Expected: PASS.

- [ ] **Step 5: Commit the basic pipeline**

```bash
git add internal/adapter/skill_adapter.go internal/adapter/skill_adapter_test.go
git commit -m "feat(adapter): install skills from staging"
```

### Task 6: Harden atomic publication and joined cleanup errors

**Files:**
- Modify: `internal/adapter/skill_adapter.go`
- Modify: `internal/adapter/skill_adapter_test.go`

**Interfaces:**
- Consumes: injectable `copyDir`, `rename`, `removeAll` from Task 5.
- Produces: deterministic failure cleanup, preserved primary/cleanup chains, and direct tests of the Task 5 second no-clobber check.

- [ ] **Step 1: Add failing failure-path tests**

Use internal tests without `t.Parallel()` and injected errors:

```go
primaryErr := errors.New("rename failed")
cleanupErr := errors.New("staging cleanup failed")
subject.rename = func(string, string) error { return primaryErr }
subject.removeAll = func(path string) error {
	if path == stagingRoot { return cleanupErr }
	return os.RemoveAll(path)
}
err := subject.Install(ref, stagingRoot)
if !errors.Is(err, primaryErr) || !errors.Is(err, cleanupErr) {
	t.Fatalf("Install() error = %v, want both causes", err)
}
```

Add cases for injected copy failure; an adapt hook that creates a non-empty target after the first check; injected rename failure; staging cleanup failure after a successful rename; simultaneous temp/staging cleanup errors; and error messages containing Agent, Power, phase, and path. Capture a debug-enabled default logger and assert phase logs exist.

- [ ] **Step 2: Run new tests and confirm failure**

Run: `go test ./internal/adapter -run 'TestSkillAdapter(Failure|SecondConflict|Cleanup)' -count=1 -v`

Expected: FAIL because full injected-failure and joined cleanup behavior are absent.

- [ ] **Step 3: Verify the second check and add robust defers**

Keep the Task 5 `ensureTargetAbsent` call immediately before rename and make its `ErrTargetConflict` path explicit in the new test. Never overwrite the primary error:

```go
defer func() {
	if tempDir != "" {
		if err := a.removeAll(tempDir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove target temporary path %q: %w", tempDir, err))
		}
	}
	if err := a.removeAll(validatedStaging); err != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("remove staging path %q: %w", validatedStaging, err))
	}
}()
```

Set `tempDir = ""` only after rename succeeds. Do not delete a published target when later staging cleanup fails. Add package-level `slog.Debug` calls at detection, adaptation, copy, publication, and cleanup boundaries with stable Agent/Power/version/phase/path attrs.

- [ ] **Step 4: Run all Adapter tests and logging scan**

```bash
go test ./internal/adapter -count=1 -v
! rg -n 'fmt\.Print|log\.Print|slog\.(Info|Warn|Error)|slog\.New' internal/adapter --glob '*.go'
```

Expected: tests PASS and scan has no matches.

- [ ] **Step 5: Commit hardening**

```bash
git add internal/adapter/skill_adapter.go internal/adapter/skill_adapter_test.go
git commit -m "fix(adapter): preserve atomic install failures"
```

### Task 7: Implement Registry and update its C4 view

**Files:**
- Modify: `internal/adapter/registry.go:1-21`
- Create: `internal/adapter/registry_test.go`
- Verify: `internal/lifecycle/dependencies.go:13-17`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`

**Interfaces:**
- Consumes: `builtInAgentConfigs`, `newSkillAdapter`, `domain.AgentSnapshot`.
- Produces: deterministic `NewRegistry`, `Resolve`, and `RefreshInstalled`.

- [ ] **Step 1: Write Registry tests**

Under isolated `APM_HOME`, create only Codex and Hermes roots:

```go
registry := adapter.NewRegistry()
for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
	got, err := registry.Resolve(agent, domain.PowerTypeSkill)
	if err != nil || got == nil { t.Fatalf("Resolve(%q) = %v, %v", agent, got, err) }
}
snapshot, err := registry.RefreshInstalled()
if err != nil { t.Fatal(err) }
want := []domain.AgentID{domain.AgentCodex, domain.AgentHermes}
if !slices.Equal(snapshot.Ordered(), want) { t.Fatalf("snapshot = %v, want %v", snapshot.Ordered(), want) }
```

Assert unknown Agent and Plugin/MCP wrap `ErrUnsupportedAdapter`; a Claude root regular file makes refresh return `ErrAgentNotInstalled` and a zero snapshot. Resolve must not require installation.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/adapter -run TestRegistry -count=1 -v`

Expected: FAIL because Registry is a placeholder.

- [ ] **Step 3: Implement deterministic Registry**

```go
type Registry struct {
	ordered []domain.AgentID
	byAgent map[domain.AgentID]Adapter
}
```

Construct one adapter for each `builtInAgentConfigs` entry and preserve slice order. `Resolve` rejects non-Skill then unknown IDs, wrapping `ErrUnsupportedAdapter`. `RefreshInstalled` loops ordered IDs, returns immediately on detection error with a zero snapshot, and appends only installed Agents.

- [ ] **Step 4: Update C4 and run package tests**

Replace `InstallPath`, `Power`, and `Uninstall` with `TargetPath(PowerRef)` and `Install(PowerRef, stagingRoot)`. Show one shared `skillAdapter` fed by three `agentConfig` values; note Skill-only resolution, stable order, and Adapter-owned staging cleanup.

Run: `go test ./internal/adapter ./internal/domain ./internal/lifecycle -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Registry and C4**

```bash
git add internal/adapter/registry.go internal/adapter/registry_test.go ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd
git commit -m "feat(adapter): register built-in skill adapters"
```

### Task 8: Verify the feature and record the Changelog

**Files:**
- Modify: `docs/Changelog.md`
- Verify: all Go source/tests, Feature 006, ADR-008, and C4 Adapter diagram.

**Interfaces:**
- Consumes: Tasks 1–7 commits and accepted Feature 006 spec.
- Produces: verified behavior and one completion record; no additional source behavior.

- [ ] **Step 1: Run focused acceptance tests without cache**

Run: `go test ./internal/adapter ./internal/domain ./internal/lifecycle -count=1 -v`

Expected: all tests PASS and no SKIP; lifecycle may still report `[no test files]`, but it must compile.

- [ ] **Step 2: Run full repository verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 3: Run scope and safety scans**

```bash
! rg -n 'fmt\.Print|log\.Print|slog\.(Info|Warn|Error)|slog\.New' internal/adapter --glob '*.go'
! rg -n 'InstallPath|domain\.Power\b|Uninstall\(' internal/adapter internal/domain internal/lifecycle --glob '*.go'
rg -n 'TargetPath|DetectAgent|Install\(' internal/adapter --glob '*.go'
git status --short
```

Expected: negative scans have no matches; positive scan shows the new contract/implementation; worktree is clean before Changelog edit.

- [ ] **Step 4: Add one reverse-chronological Changelog entry**

Run `git log --oneline 543ad83^..HEAD` and copy the exact returned design, plan, and Tasks 1–7 hashes into one newest `2026-08-07` “新增功能” entry. Describe the three IDs, global copy-only install, frontmatter rules, unmanaged-target conflicts, atomic temporary publication, staging cleanup, exact verification results, and remaining external rename race. Do not create one entry per commit and do not record incomplete work.

- [ ] **Step 5: Validate and commit the completion record**

Run: `git diff --check && git diff -- docs/Changelog.md`

Expected: clean diff check and exactly one complete newest entry.

```bash
git add docs/Changelog.md
git commit -m "docs: record agent skill adapters"
```

- [ ] **Step 6: Re-run final verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
git status --short
```

Expected: every command exits 0 and worktree is clean.

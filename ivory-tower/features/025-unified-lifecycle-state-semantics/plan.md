# Unified Power Lifecycle Entry-state Semantics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Skill, Agent, MCP, and Plugin return the same macro result, warning, and public conflict error for the same install/uninstall manifest and target entry state.

**Architecture:** Add a pure state-decision layer in `internal/lifecycle`, while preserving type-specific probes and transaction drivers. Missing/incomplete managed targets converge through an uninstall-style repair followed by a fresh install; Plugin marketplace uninstall becomes commit-directed and removes both Plugin and marketplace while retaining artifact cache.

**Tech Stack:** Go 1.26.5, Cobra, `log/slog`, existing APM adapter/config/catalog/cache/pluginresource/transaction packages, table-driven Go tests, and shell-backed fake Agent CLIs.

**Spec:** `ivory-tower/features/025-unified-lifecycle-state-semantics/spec.md`

## Global Constraints

- Read the approved spec and `ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md` before editing code.
- Keep the four physical transaction drivers separate; only macro decisions are shared.
- Same-version manifests return `unchanged` before target inspection.
- Manifest-absent uninstall returns `unexecuted` without target inspection or mutation.
- Manifest-absent install with any existing target or Plugin auxiliary fact exposes only `adapter.ErrTargetConflict` as the public business sentinel.
- Missing/incomplete managed-target repair emits exactly one warning and commits a fully uninstalled state before fresh install.
- Plugin uninstall removes Plugin, dedicated marketplace, and manifest, but never artifact cache.
- Automated tests must use isolated homes and fake Agent CLIs; never call a real Agent CLI.
- Add short normative Chinese comments to new important interfaces and methods.
- Add exactly one Changelog entry only after all behavior and verification work succeeds.
- Preserve unrelated working-tree changes; create an isolated worktree at execution time if needed.

---

### Task 1: Add the shared lifecycle state policy

**Files:**
- Create: `internal/lifecycle/power_state.go`
- Create: `internal/lifecycle/power_state_test.go`

**Interfaces:**
- Consumes: normalized manifest and target facts from later type-specific code.
- Produces: `decideInstallState(manifestState, targetState) stateDecision` and `decideUninstallState(bool, targetState) stateDecision`.

- [ ] **Step 1: Write the failing exhaustive policy tests**

Create table tests with these exact rows:

```go
install := []struct {
	manifest manifestState
	target   targetState
	want     stateDecision
}{
	{manifestAbsent, targetAbsent, stateDecision{action: actionInstallNew}},
	{manifestAbsent, targetComplete, stateDecision{action: actionRejectConflict}},
	{manifestAbsent, targetIncomplete, stateDecision{action: actionRejectConflict}},
	{manifestAbsent, targetConflict, stateDecision{action: actionRejectConflict}},
	{manifestSameVersion, targetAbsent, stateDecision{action: actionUnchanged}},
	{manifestSameVersion, targetConflict, stateDecision{action: actionUnchanged}},
	{manifestDifferentVersion, targetComplete, stateDecision{action: actionReplace}},
	{manifestDifferentVersion, targetAbsent, stateDecision{action: actionCleanAndInstall, warnMissing: true}},
	{manifestDifferentVersion, targetIncomplete, stateDecision{action: actionCleanAndInstall, warnMissing: true}},
	{manifestDifferentVersion, targetConflict, stateDecision{action: actionRejectState}},
}

uninstall := []struct {
	hasManifest bool
	target      targetState
	want        stateDecision
}{
	{false, targetComplete, stateDecision{action: actionUnexecuted}},
	{true, targetComplete, stateDecision{action: actionUninstall}},
	{true, targetAbsent, stateDecision{action: actionCleanOnly, warnMissing: true}},
	{true, targetIncomplete, stateDecision{action: actionCleanOnly, warnMissing: true}},
	{true, targetConflict, stateDecision{action: actionRejectState}},
}
```

- [ ] **Step 2: Prove the tests fail before implementation**

Run `go test ./internal/lifecycle -run 'TestDecide(Install|Uninstall)State' -count=1`.

Expected: compile failure because the policy types/functions are undefined.

- [ ] **Step 3: Implement the minimal pure policy**

Create private enums `manifestAbsent|manifestSameVersion|manifestDifferentVersion`, `targetAbsent|targetComplete|targetIncomplete|targetConflict`, and actions `actionUnchanged|actionInstallNew|actionReplace|actionCleanAndInstall|actionUninstall|actionCleanOnly|actionUnexecuted|actionRejectConflict|actionRejectState`.

Use this result type:

```go
type stateDecision struct {
	action      lifecycleAction
	warnMissing bool
}
```

Implement only the two table mappings. The functions must perform no I/O and return `actionUnchanged` before considering target state for same-version input.

- [ ] **Step 4: Format and pass the focused tests**

```bash
gofmt -w internal/lifecycle/power_state.go internal/lifecycle/power_state_test.go
go test ./internal/lifecycle -run 'TestDecide(Install|Uninstall)State' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle/power_state.go internal/lifecycle/power_state_test.go
git commit -m "refactor(lifecycle): add shared entry-state policy"
```

---

### Task 2: Align Skill and Agent install states

**Files:**
- Create: `internal/transaction/target_inspection.go`
- Create: `internal/transaction/target_inspection_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/install_skill.go`
- Modify: `internal/lifecycle/install_agent_power.go`
- Modify: `internal/lifecycle/install_skill_test.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/uninstall_test_helpers_test.go`

**Interfaces:**
- Consumes: Task 1 policy, existing `TargetPath`, file install/uninstall transactions, `PrepareSkill`, and `PrepareAgent`.
- Produces: `transaction.InspectFilesystemTarget(domain.PowerRef, FilesystemTarget) (TargetPresence, error)` and aligned Skill/Agent behavior.

- [ ] **Step 1: Write failing read-only target-inspection tests**

Cover missing path, existing physical path, unsafe relative target, and injected `lstat` failure. The core assertion is:

```go
got, err := service.InspectFilesystemTarget(ref, staticTarget{path: target})
if err != nil || got != TargetAbsent { t.Fatalf("missing = %q, %v", got, err) }
if err := os.WriteFile(target, []byte("managed"), 0o600); err != nil { t.Fatal(err) }
got, err = service.InspectFilesystemTarget(ref, staticTarget{path: target})
if err != nil || got != TargetPresent { t.Fatalf("present = %q, %v", got, err) }
```

- [ ] **Step 2: Prove the new interface is absent**

Run `go test ./internal/transaction -run TestInspectFilesystemTarget -count=1`.

Expected: compile failure.

- [ ] **Step 3: Add the narrow transaction inspection interface**

Add to `manager.go`:

```go
type FilesystemTarget interface {
	TargetPath(domain.PowerRef) (string, error)
}

type TargetPresence string

const (
	TargetAbsent  TargetPresence = "absent"
	TargetPresent TargetPresence = "present"
)
```

Add `InspectFilesystemTarget(domain.PowerRef, FilesystemTarget) (TargetPresence, error)` to `Manager`. Implement it using `TargetPath`, existing `validateSafeAbsolutePath`, and `s.pathExists`; it must not create a journal or mutate config.

- [ ] **Step 4: Write failing Skill and Agent state tables**

For both Power types cover: new/absent=`installed`; new/present=`ErrTargetConflict`; same/missing=`unchanged` with zero probe; different/present=`upgraded`; different/missing=one warning, repair, then `installed`.

For repair assert this exact order:

```text
config find → target inspect → uninstall transaction → cache prepare → fresh install transaction
```

Assert warning `installed power target is missing` occurs exactly once with `operation=install`, agent, type, power, and old version.

- [ ] **Step 5: Implement file-Power decisions**

In each lifecycle: read manifest; return same-version before resolving/probing; inspect target; apply `decideInstallState`; wrap only `adapter.ErrTargetConflict` for unmanaged present; on clean-and-install log once, call `UninstallSkill`/`UninstallAgent` with the old ref, then prepare and call install with `Previous=nil`; return `installed`. Preserve `Previous=&previous` only for complete upgrade/downgrade.

Add this shared logger in `install_agent.go`:

```go
func logInstallMissingTarget(agent domain.AgentID, ref domain.PowerRef) {
	slog.Warn("installed power target is missing",
		slog.String("operation", "install"), logging.Agent(agent),
		slog.String("type", string(ref.Type)), logging.Power(ref.Name), logging.Version(ref.Version))
}
```

- [ ] **Step 6: Update transaction fakes and run focused tests**

Fake managers must record `transaction:inspect-target:<agent>:<type>` and let tests choose absent/present. Same-version cases must not record it.

```bash
gofmt -w internal/transaction/manager.go internal/transaction/target_inspection.go internal/transaction/target_inspection_test.go internal/lifecycle/install_agent.go internal/lifecycle/install_skill.go internal/lifecycle/install_agent_power.go internal/lifecycle/*test_helpers_test.go internal/lifecycle/install_skill_test.go internal/lifecycle/install_agent_test.go
go test ./internal/transaction ./internal/lifecycle -run 'TestInspectFilesystemTarget|TestInstall.*(Skill|Agent|SameVersion)' -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/transaction/manager.go internal/transaction/target_inspection.go internal/transaction/target_inspection_test.go internal/lifecycle/install_agent.go internal/lifecycle/install_skill.go internal/lifecycle/install_agent_power.go internal/lifecycle/install_skill_test.go internal/lifecycle/install_agent_test.go internal/lifecycle/install_test_helpers_test.go internal/lifecycle/uninstall_test_helpers_test.go
git commit -m "feat(lifecycle): align file power install states"
```

---

### Task 3: Align MCP install states and public conflict error

**Files:**
- Modify: `internal/lifecycle/install_mcp.go`
- Modify: `internal/lifecycle/install_mcp_test.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`

**Interfaces:**
- Consumes: Task 1 policy, `MCPAdapter.Inspect`, `UninstallMCP`, `GetMCP`, and `InstallMCP`.
- Produces: manifest-first MCP behavior matching Skill/Agent.

- [ ] **Step 1: Change MCP tests to the approved matrix**

Add exact cases: same+absent returns `unchanged` without Inspect; no-manifest+present returns only `ErrTargetConflict` and not `ErrMCPConflict`; different+absent calls `UninstallMCP`, emits one warning, fetches only target definition, calls `InstallMCP` with `Previous=nil`, and returns `installed`.

- [ ] **Step 2: Prove old behavior fails**

Run `go test ./internal/lifecycle -run 'TestInstallMCP(StateTable|FirstInstall|Update)' -count=1`.

Expected: failures for stale-manifest and joined-error behavior.

- [ ] **Step 3: Implement manifest-first MCP flow**

Move `config.Find` before Adapter resolve/Inspect; same version returns immediately. Probe remaining states and apply the policy. Replace `errors.Join(adapter.ErrMCPConflict, adapter.ErrTargetConflict)` with context wrapping only `adapter.ErrTargetConflict`. For different+absent, repair using `UninstallMCP`, log once, fetch target server, and perform a fresh install. Fetch old+new definitions only for different+present.

- [ ] **Step 4: Format and test**

```bash
gofmt -w internal/lifecycle/install_mcp.go internal/lifecycle/install_mcp_test.go internal/lifecycle/install_test_helpers_test.go
go test ./internal/lifecycle -run TestInstallMCP -count=1
go test ./internal/transaction -run 'Test(Install|Uninstall)MCP' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle/install_mcp.go internal/lifecycle/install_mcp_test.go internal/lifecycle/install_test_helpers_test.go
git commit -m "feat(mcp): align install entry states"
```

---

### Task 4: Make Plugin uninstall remove partial targets and marketplace

**Files:**
- Modify: `internal/pluginresource/artifact.go`
- Modify: `internal/pluginresource/manager.go`
- Modify: `internal/pluginresource/manager_test.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/plugin_validation.go`
- Modify: `internal/transaction/plugin_marketplace.go`
- Modify: `internal/transaction/plugin_marketplace_recovery.go`
- Modify: `internal/transaction/plugin_marketplace_test.go`
- Modify: `internal/transaction/plugin_marketplace_recovery_test.go`
- Modify: `internal/transaction/journal_schema_test.go`

**Interfaces:**
- Consumes: immutable Plugin path layout, exact Plugin/marketplace facts, journal store, and manifest writer.
- Produces: `ArtifactLocator.Locate(ref)` plus `ArtifactLocator.Exists(ref)` and commit-directed Plugin uninstall.

- [ ] **Step 1: Write a failing no-I/O locator test**

Inject an `lstat` function that calls `t.Fatal`, invoke `service.Locate(ref)`, and assert the returned Root equals `<cache>/plugins/<name>/<version>`. This proves path calculation does not require valid cached content. Separately test `Exists(ref)` for a missing root, a physical directory, a symlink, and an `lstat` error; it reports only safe physical-root presence and never invokes the artifact builder.

- [ ] **Step 2: Write failing Plugin uninstall tables**

Use rows: both present→remove Plugin+marketplace; marketplace-only→unregister marketplace and `TargetMissing=true`; Plugin-only→remove Plugin and `TargetMissing=true`; both absent→manifest-only and `TargetMissing=true`. Every success removes manifest. Wrong source/selector performs zero writes. Mutation unknown preserves journal+manifest.

- [ ] **Step 3: Prove both old contracts fail**

```bash
go test ./internal/pluginresource -run TestLocateReturnsExpectedArtifactWithoutReadingCache -count=1
go test ./internal/transaction -run 'Test.*Marketplace.*Uninstall' -count=1
```

- [ ] **Step 4: Add the locator and journal fields**

Add this narrow interface to `pluginresource`:

```go
type ArtifactLocator interface {
	Locate(domain.PowerRef) (Artifact, error)
	Exists(domain.PowerRef) (bool, error)
}
```

Implement `Locate` by calling the existing identity/path constructor only. Implement `Exists` by locating the expected root and accepting only a physical directory; missing returns false, while symlink, non-directory, and unreadable states return an error. Neither method validates artifact contents. Add:

```go
PhaseUnregisteringMarketplace Phase = "unregistering-marketplace"
```

and `TargetPresent bool` to `PluginMarketplaceJournalPayload`. For uninstall, `Plugin.TargetPresent` records Plugin pre-state and `Marketplace.TargetPresent` records marketplace pre-state. Update strict journal validation for removing-plugin, unregistering-marketplace, and committing.

- [ ] **Step 5: Implement commit-directed uninstall**

Re-inspect both facts; reject mismatches before journal creation; directly clean manifest if both absent; otherwise journal the observed pre-state, remove Plugin if present, persist unregistering phase, unregister marketplace if present, persist committing, remove manifest, and delete journal. Do not validate/download artifact during uninstall.

- [ ] **Step 6: Rewrite recovery toward removal only**

`recoverLocalMarketplacePluginUninstall` must idempotently remove remaining Plugin, unregister remaining expected marketplace, persist committing, remove manifest, and delete journal. Adjust recovery fact inspection so uninstall journals permit the reachable Plugin-only and marketplace-only states recorded by their presence bits; install recovery must keep rejecting those states. Recovery must never Add/Register or call `validateMarketplaceRecoveryArtifact`. Add `RecoveryActionUnregisterMarketplace`.

- [ ] **Step 7: Add recovery tests at every boundary**

Cover pre-remove, Plugin removed, marketplace removed, committing, marketplace-only, Plugin-only, wrong source/selector, and unknown mutation. Assert manifest removal occurs only after both runtime facts are absent.

- [ ] **Step 8: Format and run Plugin transaction/schema tests**

```bash
gofmt -w internal/pluginresource/artifact.go internal/pluginresource/manager.go internal/pluginresource/manager_test.go internal/transaction/journal.go internal/transaction/plugin_validation.go internal/transaction/plugin_marketplace.go internal/transaction/plugin_marketplace_recovery.go internal/transaction/plugin_marketplace_test.go internal/transaction/plugin_marketplace_recovery_test.go internal/transaction/journal_schema_test.go
go test ./internal/pluginresource -run 'Test(Locate|Lookup|Ensure)' -count=1
go test ./internal/transaction -run 'Test.*(Marketplace|Plugin.*Journal|Recover)' -count=1
```

- [ ] **Step 9: Commit**

```bash
git add internal/pluginresource/artifact.go internal/pluginresource/manager.go internal/pluginresource/manager_test.go internal/transaction/journal.go internal/transaction/plugin_validation.go internal/transaction/plugin_marketplace.go internal/transaction/plugin_marketplace_recovery.go internal/transaction/plugin_marketplace_test.go internal/transaction/plugin_marketplace_recovery_test.go internal/transaction/journal_schema_test.go
git commit -m "feat(plugin): remove marketplace during uninstall"
```

---

### Task 5: Align Plugin lifecycle install and uninstall

**Files:**
- Modify: `internal/lifecycle/install_plugin.go`
- Modify: `internal/lifecycle/uninstall.go`
- Modify: `internal/lifecycle/uninstall_plugin.go`
- Modify: `internal/lifecycle/install_plugin_test.go`
- Modify: `internal/lifecycle/uninstall_plugin_test.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/uninstall_test_helpers_test.go`
- Modify: `internal/lifecycle/plugin_integration_test.go`
- Modify: `internal/lifecycle/plugin_acceptance_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: Task 1 policy, Task 4 locator and Plugin uninstall transaction, existing `Ensure/Lookup/StageFilesystem`.
- Produces: Plugin absent/complete/incomplete/conflict classification and aligned lifecycle results.

- [ ] **Step 1: Write failing Plugin classification/behavior tables**

Test both absent, both complete, marketplace-only, Plugin-only, wrong source, and wrong selector. Managed one-sided states are incomplete; mismatches are conflict. Verify different+incomplete warns once, repairs, then first-installs as `installed`; no-manifest+any residual returns only `ErrTargetConflict`; uninstall partial completes with one warning; damaged/missing artifact cache does not block uninstall.

- [ ] **Step 2: Prove old behavior fails**

Run `go test ./internal/lifecycle -run 'Test(Install|Uninstall)Plugin' -count=1`.

- [ ] **Step 3: Add exact Plugin fact classification**

Use:

```go
type pluginTargetFacts struct {
	state       targetState
	plugin      adapter.PluginFact
	marketplace *adapter.MarketplaceFact
}
```

Compare Adapter facts with `plugins.Locate(ref).MarketplaceFor(agent)`. Locate supplies expected source without reading cache; `plugins.Exists(ref)` distinguishes a present physical source root from a missing one without requiring valid artifact contents. A missing necessary source is incomplete only when manifest ownership is established; wrong source/selector is conflict. Filesystem mode maps Adapter+physical path facts to the same states.

- [ ] **Step 4: Implement Plugin install decisions**

Keep same-version return before dependencies. New+absent ensures and first-installs. New+residual returns only `ErrTargetConflict`. Different+complete keeps old Lookup plus atomic switch. Different+absent/incomplete logs once, calls `UninstallPlugin`, then Ensure/Stage and first-installs with no previous state, returning `installed`. Conflict stays fail closed.

- [ ] **Step 5: Implement Plugin uninstall decisions without Lookup**

Change `UninstallService.plugins` to `pluginresource.ArtifactLocator`. Locate expected source, classify, reject conflict, and delegate absent/incomplete/complete cleanup to `UninstallPlugin`; remove direct Plugin manifest-deletion branches. Update `NewUninstall`, fake resources, `cmd/apm` composition, and constructor tests. Uninstall must not create Cloud/catalog dependencies.

- [ ] **Step 6: Update integration and acceptance expectations**

Fake CLIs must observe Plugin remove followed by marketplace remove. Assert artifact cache remains and installing a different version after uninstall succeeds without marketplace conflict. Every executable path must be under fake-bin.

- [ ] **Step 7: Format and test**

```bash
gofmt -w internal/lifecycle/install_plugin.go internal/lifecycle/uninstall.go internal/lifecycle/uninstall_plugin.go internal/lifecycle/install_plugin_test.go internal/lifecycle/uninstall_plugin_test.go internal/lifecycle/install_test_helpers_test.go internal/lifecycle/uninstall_test_helpers_test.go cmd/apm/main.go cmd/apm/main_test.go
go test ./internal/lifecycle -run 'Test(Install|Uninstall)Plugin' -count=1
go test ./internal/lifecycle -run 'TestPlugin.*(Integration|Acceptance)' -count=1
go test ./cmd/apm -run 'TestNew(Uninstall|Install)Lifecycle' -count=1
```

- [ ] **Step 8: Commit**

```bash
git add internal/lifecycle/install_plugin.go internal/lifecycle/uninstall.go internal/lifecycle/uninstall_plugin.go internal/lifecycle/install_plugin_test.go internal/lifecycle/uninstall_plugin_test.go internal/lifecycle/install_test_helpers_test.go internal/lifecycle/uninstall_test_helpers_test.go internal/lifecycle/plugin_integration_test.go internal/lifecycle/plugin_acceptance_test.go cmd/apm/main.go cmd/apm/main_test.go
git commit -m "feat(lifecycle): align plugin entry states"
```

---

### Task 6: Generalize explicit exact-manifest fast path

**Files:**
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`

**Interfaces:**
- Consumes: explicit Type/Name/Version, selected Agents, and `config.Find`.
- Produces: `explicitManifestHit(flags, agents) (InstallResult, bool)` for all four types.

- [ ] **Step 1: Write failing four-type fast-path tests**

Table over Skill, Agent, MCP, Plugin. Exact manifests must produce only `acquire → recover → refresh → config find(s) → release`; assert zero catalog, Cloud, cache, resource, Adapter, probe, or transaction calls. Add mixed Agents where one differs: one catalog snapshot occurs, but exact Agents still skip probe during execution.

- [ ] **Step 2: Prove non-Plugin cases fail**

Run `go test ./internal/lifecycle -run 'TestInstall.*Explicit.*Manifest' -count=1`.

- [ ] **Step 3: Generalize the helper**

Rename `explicitPluginManifestHit` to `explicitManifestHit`; require explicit nonempty Type/Name/Version; construct and validate a canonical `PowerRef`; compare every selected Agent’s manifest. Any absent/different/unreadable entry returns `matched=false` without changing normal catalog error priority. Exact per-Agent execution still returns before target inspection.

- [ ] **Step 4: Format and test**

```bash
gofmt -w internal/lifecycle/install.go internal/lifecycle/install_test.go internal/lifecycle/install_test_helpers_test.go
go test ./internal/lifecycle -run TestInstall -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle/install.go internal/lifecycle/install_test.go internal/lifecycle/install_test_helpers_test.go
git commit -m "feat(install): generalize exact manifest fast path"
```

---

### Task 7: Add cross-Power contract and CLI acceptance coverage

**Files:**
- Create: `internal/lifecycle/lifecycle_state_contract_test.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/uninstall_test.go`
- Modify: `internal/cli/install_test.go`
- Modify: `internal/cli/uninstall_test.go`
- Modify: `internal/lifecycle/plugin_acceptance_test.go`
- Modify: `internal/lifecycle/mcp_integration_test.go`
- Modify: `internal/lifecycle/agent_test.go`

**Interfaces:**
- Consumes: completed four-type implementations.
- Produces: one regression suite proving identical macro behavior.

- [ ] **Step 1: Write a cross-type contract table**

For each applicable type run: install absent/absent→installed; absent/present→failed+only ErrTargetConflict; same/any→unchanged+zero probe; different/complete→upgrade/downgrade; different/absent→one warning+installed; uninstall absent/any→one warning+unexecuted+zero probe; present/complete→completed; present/absent→one warning+completed. Add Plugin incomplete to the missing rows, never as an exception.

Use an explicit case shape:

```go
type lifecycleContractCase struct {
	name            string
	manifest        string
	target          string
	wantInstall     cli.InstallOutcome
	wantUninstall   cli.UninstallOutcome
	wantWarning     string
	wantPublicError error
}
```

- [ ] **Step 2: Add CLI line/error-count tests**

Assert stdout newline count equals result item count, runtime batch failure produces exactly one `level=ERROR`, runtime failure has no `Usage:`, and Release failure suppresses stdout while preserving an already emitted warning. Keep parameter-error usage tests unchanged.

- [ ] **Step 3: Run contract tests and fix only real gaps**

```bash
go test ./internal/lifecycle -run 'TestLifecycleStateContract|TestInstall|TestUninstall' -count=1
go test ./internal/cli -run 'Test(Install|Uninstall)' -count=1
```

- [ ] **Step 4: Run isolated integration scenarios**

Verify Bibit Agent repair+install, MCP repair+target-only definition, Codex/Claude partial Plugin cleanup+fresh install, Plugin uninstall marketplace removal with cache retention, different-version reinstall, and `fix` for every new phase without Cloud.

Run `go test ./internal/lifecycle -run 'Test.*(Acceptance|Integration|AgentPower)' -count=1`.

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle/lifecycle_state_contract_test.go internal/lifecycle/install_test.go internal/lifecycle/uninstall_test.go internal/cli/install_test.go internal/cli/uninstall_test.go internal/lifecycle/plugin_acceptance_test.go internal/lifecycle/mcp_integration_test.go internal/lifecycle/agent_test.go
git commit -m "test(lifecycle): enforce cross-power state contract"
```

---

### Task 8: Synchronize docs, verify, and record behavior

**Files:**
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/features/010-lifecycle-install/spec.md`
- Modify: `ivory-tower/features/018-bibit-agent/spec.md`
- Modify: `ivory-tower/adr/ADR-013-generate-per-plugin-local-marketplaces.md`
- Modify: `ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: all verified behavior commits and ADR-022.
- Produces: current docs and exactly one reverse-chronological Changelog entry.

- [ ] **Step 1: Update current docs**

Document: same-version no-probe unchanged; different+missing/incomplete warning→repair→fresh installed; unmanaged target common ErrTargetConflict; Plugin uninstall removes Plugin+marketplace+manifest and retains artifact. Link Feature 025/ADR-022. Update C4 lifecycle/transaction relationships without adding a container.

- [ ] **Step 2: Add targeted historical supersession notices**

Feature 010, Feature 018, ADR-013, and ADR-014 must name exactly which stale-manifest, partial Plugin, retained marketplace, and Plugin-only fast-path rules ADR-022 supersedes. Preserve unrelated historical text.

- [ ] **Step 3: Run complete verification**

```bash
go test ./internal/lifecycle ./internal/transaction ./internal/cli ./cmd/apm -count=1
go test ./...
go test -race ./...
go vet ./...
go build -o ./apm ./cmd/apm
gofmt -l internal/lifecycle internal/transaction internal/pluginresource cmd/apm
git diff --check
```

Every command must exit 0 and `gofmt -l` must print nothing. If it prints paths, format only the Feature 025 files named by Tasks 1–7, then rerun `go test ./...`. Remove the generated `./apm` only after confirming it is an untracked build output and not a pre-existing user file.

- [ ] **Step 4: Add exactly one Changelog entry**

Read `~/.codex/docs/Changelog.md` completely. Add one newest Feature 025 entry describing all behavior. List every related behavior commit according to the template; omit design-only commit `7aeb277` unless the template explicitly requires design commits.

- [ ] **Step 5: Verify docs and workspace**

```bash
rg -n 'ADR-022|Feature 025|ErrTargetConflict|marketplace|target is missing' BUILD.md ivory-tower docs/Changelog.md
rg -n 'retained marketplace|保留 marketplace|ErrStaleManifest' BUILD.md ivory-tower/project-overview.md ivory-tower/features/010-lifecycle-install/spec.md ivory-tower/features/018-bibit-agent/spec.md ivory-tower/adr/ADR-013-generate-per-plugin-local-marketplaces.md ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md
git diff --check
git status --short
```

Current docs must cite ADR-022; old wording must appear only under explicit supersession notices; no generated binary or unrelated file may be staged.

- [ ] **Step 6: Commit docs and Changelog**

```bash
git add BUILD.md ivory-tower/project-overview.md ivory-tower/c4/components/apm-cli.dsl ivory-tower/features/010-lifecycle-install/spec.md ivory-tower/features/018-bibit-agent/spec.md ivory-tower/adr/ADR-013-generate-per-plugin-local-marketplaces.md ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md docs/Changelog.md
git commit -m "docs(lifecycle): publish unified state contract"
```

- [ ] **Step 7: Capture final evidence**

Run `git status --short` and `git log --oneline --decorate -10`.

Expected: clean worktree and ordered Feature 025 commits. Report unrelated pre-existing changes instead of altering them.

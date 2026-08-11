# Central Lifecycle Transactions Implementation Plan

> **历史计划（Install 分支已被 Feature 010 supersede）：** 本计划中的 Install self-heal 描述只记录当时步骤；当前已纳管 Install target 缺失时只 Remove 对应清单、清理 canonical staging、返回 `ErrStaleManifest` 并中止。Uninstall target 缺失语义不变。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement centralized, crash-recoverable install and uninstall transactions that keep Agent targets and manifests consistent while preserving Adapter-owned publication.

**Architecture:** Lifecycle resolves business intent and staging, then calls a high-level transaction `Manager`. The Manager persists one central journal, keeps backup/quarantine beside the target for same-filesystem renames, delegates publication to the Adapter, owns manifest writes after the commit point, and recovers any prior transaction from exact journaled paths.

**Tech Stack:** Go 1.26.5, standard-library JSON/filesystem/`crypto/rand`/`context`/`errors`/`log/slog`, existing Adapter, Config Repository, Paths, and filesystem utilities.

## Global Constraints

- Implement Feature 007 only; do not wire lifecycle/CLI command behavior, Cloud/Cache fetching, version selection, Agent batching, Plugin, or MCP.
- Central state is `<state>/transactions/active.json`; backup/quarantine content stays at `<target-parent>/.apm-transactions/<transaction-id>` for atomic rename.
- The caller must hold the global APM lock. Transaction code does not acquire another lock and must reject an already active journal.
- Adapter still owns staging adaptation, copy, target-local atomic publication, and normal staging cleanup.
- Transaction owns journal ordering, managed backup/quarantine, manifest `Set/Remove`, rollback, commit completion, and recovery.
- Never recursively delete an unvalidated path, filesystem root, symlinked transaction directory, or staging that overlaps the physical target root.
- Corrupt journals and indeterminate path states fail closed and preserve evidence.
- Production code does not print; internal diagnostics use package-level `slog.Debug` with existing structured fields.
- 重要接口、公开类型、构造函数、事务入口、阶段判定和安全关键辅助函数必须添加简短中文用途注释，并以对应 Go 标识符开头。
- Follow TDD. Preserve unrelated changes. After behavior is complete, add exactly one `docs/Changelog.md` entry for this user request.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `internal/adapter/adapter.go` | Adapter contract and explicit `InstallResult.Published`. |
| `internal/adapter/skill_adapter.go` | Preserve publication outcome across cleanup errors. |
| `internal/utils/filesystem/path.go` | Shared physical-path resolution and containment checks. |
| `internal/paths/paths.go` | Central transaction and journal paths. |
| `internal/transaction/errors.go` | Stable transaction error categories. |
| `internal/transaction/journal.go` | Journal schema, operation, phase, recovery result/action types. |
| `internal/transaction/manager.go` | Public Manager/request/writer contracts, Service dependencies, default constructor. |
| `internal/transaction/validation.go` | Request and persisted-journal validation. |
| `internal/transaction/filesystem_journal_store.go` | Atomic JSON Create/Inspect/Update/Delete. |
| `internal/transaction/artifacts.go` | Transaction ID, same-disk artifact layout, guarded cleanup. |
| `internal/transaction/path_state.go` | Path inspection and phase/state recovery direction. |
| `internal/transaction/install.go` | Normal install transaction and immediate rollback. |
| `internal/transaction/uninstall.go` | Normal uninstall transaction and immediate rollback. |
| `internal/transaction/recovery.go` | Central journal recovery and structured result. |
| `internal/lifecycle/dependencies.go` | Future lifecycle dependency on transaction Manager, not JournalStore. |

Test files mirror these responsibilities; keep helpers in the narrowest `_test.go` file that shares them.

---

### Task 1: Adapter Publication Result and Central Paths

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/skill_adapter.go`
- Modify: `internal/adapter/adapter_test.go`
- Modify: `internal/adapter/skill_adapter_test.go`

**Interfaces:**
- Produces: `adapter.InstallResult`, `Adapter.Install(domain.PowerRef, string) (InstallResult, error)`.

- [x] **Step 1: Write failing Adapter result tests**

Update the contract test adapter and add assertions to the existing success, pre-publication failure, and cleanup-after-publication tests:

```go
result, err := subject.Install(ref, staging)
if err != nil || !result.Published {
    t.Fatalf("Install() = %+v, %v; want Published=true", result, err)
}

result, err = failingSubject.Install(ref, staging)
if err == nil || result.Published {
    t.Fatalf("failed Install() = %+v, %v; want Published=false and error", result, err)
}

result, err = cleanupFailingSubject.Install(ref, staging)
if err == nil || !result.Published {
    t.Fatalf("cleanup-failing Install() = %+v, %v; want Published=true and error", result, err)
}
```

- [x] **Step 2: Run Adapter tests and verify the contract fails to compile**

Run: `go test ./internal/adapter -count=1`

Expected: FAIL because `InstallResult` and the two-value return do not exist.

- [x] **Step 3: Implement the minimal publication-result contract**

Add the documented type and change `skillAdapter.Install` to a named result. Set `Published=true` immediately after successful target rename and never reset it in deferred cleanup:

```go
type InstallResult struct {
    Published bool
}

func (a *skillAdapter) Install(ref domain.PowerRef, staging string) (result InstallResult, resultErr error) {
    // existing validation, adaptation, copy, and cleanup
    if err := a.rename(tempDir, target); err != nil {
        return result, a.installError(ref, "publication", target, err)
    }
    result.Published = true
    tempDir = ""
    return result, nil
}
```

Mechanically update existing test call sites to receive or discard the result; assert its value wherever a test crosses or does not cross publication.

- [x] **Step 4: Run focused Adapter tests**

Run: `go test ./internal/adapter -count=1`

Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/adapter
git commit -m "refactor(adapter): report publication outcome"
```

---

### Task 2: Shared Physical-Path Safety

**Files:**
- Create: `internal/utils/filesystem/path.go`
- Create: `internal/utils/filesystem/path_test.go`
- Modify: `internal/adapter/skill_adapter.go`
- Modify: `internal/adapter/skill_adapter_test.go`

**Interfaces:**
- Produces: `filesystem.ResolvePhysicalPath(string) (string, error)`.
- Produces: `filesystem.PathEqualOrWithin(parent, path string) (bool, error)`.
- Consumed by Task 5 transaction staging and artifact validation.

- [x] **Step 1: Write failing table-driven utility tests**

Cover an existing symlink, missing suffix below a symlink, equal paths, real descendants, `parent-sibling`, and a symlink loop:

```go
func TestResolvePhysicalPathPreservesMissingSuffix(t *testing.T) {
    root := t.TempDir()
    physical := filepath.Join(root, "physical")
    if err := os.Mkdir(physical, 0o755); err != nil { t.Fatal(err) }
    link := filepath.Join(root, "link")
    if err := os.Symlink(physical, link); err != nil { t.Fatal(err) }
    got, err := ResolvePhysicalPath(filepath.Join(link, "missing", "leaf"))
    if err != nil { t.Fatal(err) }
    if want := filepath.Join(physical, "missing", "leaf"); got != want {
        t.Fatalf("ResolvePhysicalPath() = %q, want %q", got, want)
    }
}
```

- [x] **Step 2: Run the utility tests and verify failure**

Run: `go test ./internal/utils/filesystem -count=1`

Expected: FAIL because the exported helpers do not exist.

- [x] **Step 3: Move the existing proven algorithms into the shared utility**

Move `resolvePhysicalPathDepth` and the separator-aware `filepath.Rel` check from Adapter, preserving the 255-link cap and missing-path behavior. Add brief Chinese comments on the two exported safety interfaces.

- [x] **Step 4: Switch Adapter to the shared helpers and remove duplicates**

```go
physicalStaging, err := filesystem.ResolvePhysicalPath(stagingRoot)
physicalSkills, err := filesystem.ResolvePhysicalPath(skillsRoot)
contained, err := filesystem.PathEqualOrWithin(physicalStaging, physicalSkills)
```

Retain Adapter-specific error context and its exact cleanup exception.

- [x] **Step 5: Run focused and regression tests**

Run: `go test ./internal/utils/filesystem ./internal/adapter -count=1`

Expected: PASS, including every existing symlink-overlap Adapter test.

- [x] **Step 6: Commit**

```bash
git add internal/utils/filesystem internal/adapter
git commit -m "refactor(filesystem): share physical path safety"
```

---

### Task 3: Transaction Contracts, Schema, and Validation

**Files:**
- Create: `internal/transaction/errors.go`
- Modify: `internal/transaction/journal.go`
- Create: `internal/transaction/manager.go`
- Create: `internal/transaction/validation.go`
- Create: `internal/transaction/validation_test.go`
- Create: `internal/transaction/test_helpers_test.go`
- Modify: `internal/transaction/journal_store.go`
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`
- Delete: `internal/transaction/install_test.go`

**Interfaces:**
- Produces exactly the Manager, request, ManifestWriter, JournalStore, Journal, phase, error, and RecoveryResult contracts from `spec.md`.
- Produces internal `validateInstallRequest`, `validateUninstallRequest`, and `validateJournal`.
- Produces: `paths.TransactionPath() (string, error)`, `paths.JournalPath() (string, error)`.
- Produces: `newService(manifests ManifestWriter, journals JournalStore) *Service` for same-package tests and the default constructor.
- Produces sentinels: `ErrActiveTransaction`, `ErrInvalidRequest`, `ErrInvalidJournal`, `ErrUnsafePath`, `ErrIndeterminateState`, and `ErrRecoveryFailed`.

- [x] **Step 1: Write failing central-path tests**

```go
func TestTransactionPathsUseAPMState(t *testing.T) {
    home := t.TempDir()
    t.Setenv("APM_HOME", home)
    root, err := TransactionPath()
    if err != nil { t.Fatal(err) }
    journal, err := JournalPath()
    if err != nil { t.Fatal(err) }
    wantRoot := filepath.Join(home, ".apm", "transactions")
    if root != wantRoot || journal != filepath.Join(wantRoot, "active.json") {
        t.Fatalf("transaction paths = %q, %q", root, journal)
    }
}
```

Extend the existing unavailable-HOME test to require both functions to return an error.

- [x] **Step 2: Write failing contract and validation tests**

Use a table whose cases include empty Agent, `all`, empty name/type/version, mismatched Previous name/type, empty staging, nil Adapter, schema != 1, incompatible operation/phase, dirty/relative/root paths, wrong transaction-ID directory, and operation-specific nonempty fields:

```go
func TestValidateJournalRejectsUnsafeFields(t *testing.T) {
    valid := validInstallJournal(t)
    tests := map[string]func(*Journal){
        "unknown schema": func(j *Journal) { j.SchemaVersion = 2 },
        "relative target": func(j *Journal) { j.TargetPath = "relative" },
        "wrong artifact id": func(j *Journal) { j.TransactionDir = filepath.Join(filepath.Dir(j.TargetPath), ".apm-transactions", "other") },
        "uninstall publishing": func(j *Journal) { j.Operation, j.Phase = OperationUninstall, PhasePublishing },
    }
    for name, mutate := range tests {
        t.Run(name, func(t *testing.T) {
            journal := valid
            mutate(&journal)
            if err := validateJournal(journal); !errors.Is(err, ErrInvalidJournal) {
                t.Fatalf("validateJournal() error = %v", err)
            }
        })
    }
}
```

- [x] **Step 3: Run and verify failure**

Run: `go test ./internal/paths ./internal/transaction -run 'TestTransactionPaths|TestValidate|TestTransactionContracts' -count=1`

Expected: FAIL because the contracts and validators do not exist.

- [x] **Step 4: Implement paths, exact types, and sentinels**

Implement `TransactionPath` and `JournalPath` by composing `StateDir` and propagating its error. Implement the signatures and constants copied verbatim from `spec.md`. Remove the duplicate top-level Journal `Version`, add schema/path fields, replace the placeholder package comment, and replace JournalStore's old `Inspect/Write/Delete` contract with `Inspect/Create/Update/Delete(id)`. Define `Service` initially with `manifests ManifestWriter` and `journals JournalStore`; Task 5 adds its unexported filesystem seams. Do not expose primitive transaction steps.

- [x] **Step 5: Implement strict validation and shared test fakes**

Validation must wrap the relevant stable sentinel, require safe path segments, confirm `.apm-transactions/<transaction-id>` is a lexical child of the target parent, and enforce install/uninstall field/phase matrices. Do not touch the filesystem in pure schema validation. In `test_helpers_test.go`, define `validInstallJournal`, `recordingJournalStore`, `recordingManifestWriter`, and `recordingAdapter`; each fake records the exact event strings used by Tasks 4, 6, 7, and 8 and exposes per-operation injected errors.

- [x] **Step 6: Run tests**

Run: `go test ./internal/paths ./internal/transaction -run 'TestTransactionPaths|TestValidate|TestTransactionContracts' -count=1`

Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add internal/paths internal/transaction
git commit -m "feat(transaction): define recovery contracts"
```

---

### Task 4: Atomic Filesystem Journal Store

**Files:**
- Modify: `internal/transaction/journal_store.go`
- Create: `internal/transaction/filesystem_journal_store.go`
- Create: `internal/transaction/filesystem_journal_store_test.go`

**Interfaces:**
- Consumes: `Journal`, `JournalStore`, `validateJournal`, transaction sentinels.
- Produces: `newFilesystemJournalStore(path string) *filesystemJournalStore`.

- [x] **Step 1: Write failing happy-path and strict-decode tests**

```go
func TestFilesystemJournalStoreLifecycle(t *testing.T) {
    path := filepath.Join(t.TempDir(), "transactions", "active.json")
    store := newFilesystemJournalStore(path)
    journal := validInstallJournal(t)
    if err := store.Create(journal); err != nil { t.Fatal(err) }
    got, found, err := store.Inspect()
    if err != nil || !found || got.TransactionID != journal.TransactionID { t.Fatalf("Inspect() = %+v, %v, %v", got, found, err) }
    journal.Phase = PhasePublishing
    if err := store.Update(journal); err != nil { t.Fatal(err) }
    if err := store.Delete(journal.TransactionID); err != nil { t.Fatal(err) }
    _, found, err = store.Inspect()
    if err != nil || found { t.Fatalf("Inspect() after Delete = %v, %v", found, err) }
}
```

Add cases for invalid JSON, trailing JSON, unknown field, unknown schema, Create over active, and Update/Delete ID mismatch.

- [x] **Step 2: Run and verify failure**

Run: `go test ./internal/transaction -run 'TestFilesystemJournalStore' -count=1`

Expected: FAIL because the filesystem store does not exist.

- [x] **Step 3: Implement atomic encode, replace, and delete**

Use `0700` parent and `0600` temp files. Decode with `json.Decoder.DisallowUnknownFields`, reject a second JSON value, validate before returning, and add helpers that sync the file and parent directory. Create checks absence; Update/Delete Inspect and compare IDs before mutation.

- [x] **Step 4: Add failure-injection tests for durability seams**

Inject file operations into the store in same-package tests. Assert temp cleanup and preservation/repair behavior for write, file sync, rename, directory sync, remove, and post-remove sync errors. In a post-remove sync failure, best-effort recreate `active.json` from the in-memory journal; return joined errors if recreation also fails.

- [x] **Step 5: Run Journal tests**

Run: `go test ./internal/transaction -run 'TestFilesystemJournalStore' -count=1`

Expected: PASS; `find` assertions in tests show no stale temp files.

- [x] **Step 6: Commit**

```bash
git add internal/transaction
git commit -m "feat(transaction): persist atomic journal"
```

---

### Task 5: Transaction Artifacts and Recovery Direction

**Files:**
- Create: `internal/transaction/artifacts.go`
- Create: `internal/transaction/artifacts_test.go`
- Modify: `internal/transaction/path_state.go`
- Create: `internal/transaction/path_state_test.go`

**Interfaces:**
- Produces: internal `newTransactionID`, `artifactPaths`, `ensureTransactionDir`, `cleanupTransactionDir`, `cleanupCommittedStaging`.
- Produces: internal `inspectPathState(Journal) (PathState, error)` and `recoveryDirection(Journal, PathState) (RecoveryDirection, error)`.

- [x] **Step 1: Write failing artifact-layout and safety tests**

Assert a 32-character lowercase hex ID, exact sibling layout, `0700` directories, refusal of symlink/non-directory roots, cleanup limited to the current ID, and preservation of a nonempty shared parent.

```go
func TestArtifactPathsStayBesideTarget(t *testing.T) {
    target := filepath.Join(t.TempDir(), "skills", "demo")
    paths, err := artifactPaths(target, "00112233445566778899aabbccddeeff")
    if err != nil { t.Fatal(err) }
    wantDir := filepath.Join(filepath.Dir(target), ".apm-transactions", "00112233445566778899aabbccddeeff")
    if paths.dir != wantDir || paths.backup != filepath.Join(wantDir, "backup") || paths.quarantine != filepath.Join(wantDir, "quarantine") {
        t.Fatalf("artifactPaths() = %+v", paths)
    }
}
```

- [x] **Step 2: Write failing recovery-direction table**

Cover every table row in `spec.md`, plus target+backup/quarantine contradictions. Expected indeterminate cases must satisfy `errors.Is(err, ErrIndeterminateState)`.

- [x] **Step 3: Run and verify failure**

Run: `go test ./internal/transaction -run 'TestArtifact|TestRecoveryDirection|TestInspectPathState' -count=1`

Expected: FAIL because artifact and direction helpers do not exist.

- [x] **Step 4: Implement artifacts, guarded staging cleanup, and path state**

Use `crypto/rand.Read` plus hex encoding. Use `Lstat` to reject symlink transaction roots. Before committed staging `RemoveAll`, require canonical equality with `paths.StagingPath`, reject filesystem root, resolve physical staging/target via shared filesystem helpers, and reject either root containing the other.

- [x] **Step 5: Run focused tests**

Run: `go test ./internal/transaction ./internal/utils/filesystem -run 'TestArtifact|TestRecoveryDirection|TestInspectPathState|TestCleanupCommittedStaging' -count=1`

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/transaction
git commit -m "feat(transaction): guard transaction artifacts"
```

---

### Task 6: Install Transaction

**Files:**
- Rewrite: `internal/transaction/install.go`
- Rewrite: `internal/transaction/install_test.go`
- Modify: `internal/transaction/manager.go`

**Interfaces:**
- Consumes: Adapter two-value Install, JournalStore, ManifestWriter, artifact/path helpers.
- Produces: `(*Service).Install(context.Context, InstallRequest) error`.

- [x] **Step 1: Write failing install success tests**

Use real temp target/staging paths with the recording Adapter, JournalStore, and ManifestWriter from Task 3. Define `newInstallFixture(t)` in `install_test.go`; it sets `APM_HOME` to a temp root, creates the canonical staging tree and Agent skill root, points the recording Adapter at `<agent-root>/skills/demo`, and constructs `newService(manifests, journals)`. Cover first install, upgrade backup, downgrade, and managed target already missing. Assert journal phase order, Adapter target emptiness, Set before backup cleanup, and journal deletion last.

```go
func TestInstallUpgradePublishesSetsManifestAndCleansJournal(t *testing.T) {
    fixture := newInstallFixture(t)
    fixture.writeOldTarget("old")
    err := fixture.service.Install(context.Background(), fixture.upgradeRequest())
    if err != nil { t.Fatal(err) }
    fixture.assertEvents("create:preparing", "rename:target->backup", "update:publishing", "adapter:install", "update:committing", "manifest:set", "remove:backup", "delete:journal")
    fixture.assertTarget("new")
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./internal/transaction -run 'TestInstall' -count=1`

Expected: FAIL because Service Install is not implemented.

- [x] **Step 3: Implement the green install path**

Follow the nine ordered steps in `spec.md`. Before journal creation reject an unmanaged existing target with an error wrapping `adapter.ErrTargetConflict`. After any managed backup, recheck target absence, update `publishing`, call Adapter, and treat `err == nil && !result.Published` as a contract failure wrapping `ErrInvalidRequest`. Then update `committing`, Set, clean, and Delete.

- [x] **Step 4: Add failing rollback and commit-point tests**

Table-drive failure at journal create/update, backup rename, Adapter pre-publication, rolling-back update, restore rename, Adapter `Published=true` cleanup error, committing update, Set, backup/staging cleanup, and journal Delete. Assert:

```go
if published {
    fixture.assertJournalPreserved()
    fixture.assertNewTargetPreserved()
} else if rollbackSucceeded {
    fixture.assertOldTargetRestored()
    fixture.assertJournalDeleted()
} else {
    fixture.assertJournalPreserved()
}
```

Also cancel context before backup, before Adapter, and after publication; verify pre-commit rollback versus post-commit journal preservation.

- [x] **Step 5: Implement failure transitions and run tests**

Persist `rolling-back` before restoring backup. If that update fails, do not restore under stale `publishing`. On `Published=true`, never restore backup; preserve the journal on any subsequent error. Join primary and cleanup errors.

Run: `go test ./internal/transaction -run 'TestInstall' -count=1`

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/transaction
git commit -m "feat(transaction): implement install transaction"
```

---

### Task 7: Uninstall Transaction

**Files:**
- Rewrite: `internal/transaction/uninstall.go`
- Create: `internal/transaction/uninstall_test.go`

**Interfaces:**
- Consumes: JournalStore, ManifestWriter, Adapter.TargetPath, artifact helpers.
- Produces: `(*Service).Uninstall(context.Context, UninstallRequest) error`.

- [x] **Step 1: Write failing success and drift tests**

Define `newUninstallFixture(t)` in `uninstall_test.go` using the Task 3 recording fakes, a temp Agent skill target, and `newService(manifests, journals)`; its Adapter only resolves the exact target and must never receive Install.

```go
func TestUninstallQuarantinesRemovesManifestAndCleansJournal(t *testing.T) {
    fixture := newUninstallFixture(t)
    fixture.writeTarget()
    if err := fixture.service.Uninstall(context.Background(), fixture.request()); err != nil { t.Fatal(err) }
    fixture.assertEvents("create:removing", "rename:target->quarantine", "update:committing", "manifest:remove", "remove:quarantine", "delete:journal")
    fixture.assertTargetAbsent()
}

func TestUninstallMissingManagedTargetSelfHealsManifest(t *testing.T) {
    fixture := newUninstallFixture(t)
    if err := fixture.service.Uninstall(context.Background(), fixture.request()); err != nil { t.Fatal(err) }
    fixture.assertEvents("create:removing", "update:committing", "manifest:remove", "delete:journal")
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./internal/transaction -run 'TestUninstall' -count=1`

Expected: FAIL because Service Uninstall is not implemented.

- [x] **Step 3: Implement success and self-heal paths**

Write `removing` before any target move. Treat initial absence or `os.ErrNotExist` during rename as committed drift, then persist `committing`, Remove manifest, remove quarantine/transaction directory, and delete journal last.

- [x] **Step 4: Add and implement failure tests**

Inject Create, Mkdir, rename, rolling-back update, committing update, Remove, quarantine cleanup, journal Delete, and context cancellation. A rename failure with target still present is pre-commit: persist `rolling-back`, leave target/manifest, clean safe artifacts, delete journal, and return the original error. Any error after target absence/quarantine creation preserves journal and never restores target.

- [x] **Step 5: Run focused transaction tests**

Run: `go test ./internal/transaction -run 'TestUninstall|TestInstall' -count=1`

Expected: PASS.

- [x] **Step 6: Commit**

```bash
git add internal/transaction
git commit -m "feat(transaction): implement uninstall transaction"
```

---

### Task 8: Cross-Command Recovery and Lifecycle Boundary

**Files:**
- Rewrite: `internal/transaction/recovery.go`
- Create: `internal/transaction/recovery_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/lifecycle/dependencies.go`
- Test: `internal/lifecycle` package compilation through `go test`.

**Interfaces:**
- Produces: `NewManager(ManifestWriter) (*Service, error)` using default central paths/store.
- Produces: `(*Service).Recover(context.Context) (RecoveryResult, error)`.
- Produces lifecycle dependency alias/interface for `Recover/Install/Uninstall`; removes lifecycle JournalStore.

- [x] **Step 1: Write failing no-journal and fresh-manager recovery tests**

Define `newRecoveryFixture(t, operation, phase)` in `recovery_test.go`; it creates a real filesystem JournalStore under a temp `APM_HOME`, writes exact target/artifact paths, and can construct a new Service sharing only that store path and the manifest fake. Create persisted journal/path states directly, discard the original service/Adapter, construct a fresh service, and call Recover. Cover every phase/direction table row.

```go
func TestRecoverPublishingInstallWithTargetCompletesCommitWithoutAdapter(t *testing.T) {
    fixture := newRecoveryFixture(t, OperationInstall, PhasePublishing)
    fixture.writeTarget("new")
    fixture.writeBackup("old")
    result, err := fixture.freshService().Recover(context.Background())
    if err != nil || !result.Recovered || result.Direction != RecoveryCommit { t.Fatalf("Recover() = %+v, %v", result, err) }
    fixture.assertManifestSet()
    fixture.assertJournalAbsent()
}
```

- [x] **Step 2: Run and verify failure**

Run: `go test ./internal/transaction -run 'TestRecover' -count=1`

Expected: FAIL because Recover is still a placeholder.

- [x] **Step 3: Implement common recovery orchestration**

Inspect; return zero result on absence; preserve original phase; validate journal; inspect state; choose direction; execute idempotent install/uninstall rollback/commit helpers; append each completed structured action; delete journal last. Wrap any failure with `ErrRecoveryFailed` while preserving the underlying sentinel/error and partial result.

- [x] **Step 4: Add corruption, contradiction, cancellation, and idempotency tests**

Assert invalid JSON/schema/path performs zero manifest/filesystem actions; contradictory states return `ErrIndeterminateState` joined with `ErrRecoveryFailed`; cancellation before a recovery mutation preserves journal; a Delete failure leaves a recoverable fixed direction; repeating with a fresh Manager converges and deletes the journal.

- [x] **Step 5: Implement default constructor and lifecycle dependency boundary**

```go
func NewManager(manifests ManifestWriter) (*Service, error) {
    journalPath, err := paths.JournalPath()
    if err != nil { return nil, fmt.Errorf("resolve journal path: %w", err) }
    return newService(manifests, newFilesystemJournalStore(journalPath)), nil
}
```

In lifecycle, delete its direct JournalStore contract and add the exact alias `type TransactionManager = transaction.Manager`. Do not call it from placeholder lifecycle methods in this feature.

- [x] **Step 6: Run package and full tests**

Run: `go test ./internal/transaction ./internal/lifecycle -count=1`

Expected: PASS.

Run: `go test ./... -count=1`

Expected: PASS.

- [x] **Step 7: Commit**

```bash
git add internal/transaction internal/lifecycle
git commit -m "feat(transaction): recover centralized transactions"
```

---

### Task 9: Documentation, Changelog, and Final Verification

**Files:**
- Modify: `ivory-tower/features/007-central-lifecycle-transactions/plan.md`
- Modify: `docs/Changelog.md`
- Verify: all Feature 007 production/test/docs files.

**Interfaces:**
- Consumes: all preceding tasks and commits.
- Produces: completed plan checklist, one project-format Changelog entry, verified build.

- [x] **Step 1: Verify important-interface Chinese comments**

Run a targeted source review:

```bash
rg -n '^type (Manager|InstallRequest|UninstallRequest|ManifestWriter|Journal|RecoveryResult|JournalStore)|^func (NewManager|TransactionPath|JournalPath)|^func \(.*\) (Install|Uninstall|Recover)' internal
```

For each important identifier, confirm the immediately preceding GoDoc begins with that identifier and briefly states its purpose in Chinese. Add only missing comments; do not narrate trivial helpers.

- [x] **Step 2: Run formatting and static checks**

```bash
rg --files internal/adapter internal/paths internal/utils/filesystem internal/transaction internal/lifecycle -g '*.go' -0 | xargs -0 gofmt -w
test -z "$(gofmt -l cmd internal)"
git diff --check
go vet ./...
go build -o /dev/null ./cmd/apm
```

Expected: every command exits 0 with no formatting or whitespace output.

- [x] **Step 3: Run focused safety and full regression tests uncached**

```bash
go test -count=1 ./internal/adapter ./internal/utils/filesystem ./internal/paths ./internal/transaction ./internal/lifecycle
go test -count=1 ./...
```

Expected: PASS; transaction tests include every crash window, unsafe path, and post-commit failure from the spec.

- [x] **Step 4: Add exactly one Changelog entry**

Read `~/.codex/docs/Changelog.md`, then add one reverse-chronological entry under `# Changelog`. Classify it as `新增功能`; describe centralized active journal, same-disk artifacts, explicit Adapter publication outcome, manifest ownership, self-healing drift, fail-closed recovery, and the boundary excluding lifecycle/CLI wiring. Populate `相关提交` with design commit `73487d5`, this plan's commit, and every Feature 007 implementation commit produced by Tasks 1–8. Do not create a second entry for documentation or tests.

- [x] **Step 5: Mark the plan complete and verify the final diff**

Change every completed checkbox in this file to `[x]`, then run:

```bash
git diff --check
git status --short
git diff --stat
git diff -- docs/Changelog.md ivory-tower/features/007-central-lifecycle-transactions/plan.md
```

Expected: only final documentation/checklist changes remain unstaged; no unrelated files appear.

- [x] **Step 6: Commit final documentation**

```bash
git add docs/Changelog.md ivory-tower/features/007-central-lifecycle-transactions/plan.md
git commit -m "docs: record centralized transactions"
```

- [x] **Step 7: Verify clean handoff**

```bash
git status --short --branch
git log --oneline --decorate -12
```

Expected: clean feature worktree and a linear sequence containing the design commit, Tasks 1–8 commits, and the final documentation commit.

# Journal Schema Development Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove legacy journal compatibility and make the existing driver-based shared envelope the only supported format, numbered `schema_version: 1`.

**Architecture:** Keep the `Journal` in-memory model, filesystem/agent-cli drivers, payloads, phases, and recovery routing unchanged. Strictly decode exactly one current envelope, validate version `1`, and fail closed with `ErrInvalidJournal` while preserving malformed files. Remove the legacy normalization layer and align every producer, test, ADR, and current-state document.

**Tech Stack:** Go 1.26.5, standard-library `encoding/json`, existing transaction/lifecycle test fixtures, Markdown/Mermaid/C4 DSL.

## Global Constraints

- The only valid persistent journal version is `schema_version: 1` using the current shared envelope.
- Old flat Skill journals are unsupported and must return an error wrapping `ErrInvalidJournal`.
- Every malformed journal remains byte-for-byte present after `Inspect` or `Recover`; no automatic deletion, rewrite, or migration.
- The envelope must contain exactly one payload matching `driver=filesystem` or `driver=agent-cli`.
- Skill/MCP transaction phases, recovery direction, single-active-journal semantics, lifecycle behavior, Cloud, adapters, and manifests do not change.
- Automated tests must not invoke a real Agent CLI or write a real Agent configuration.
- Use TDD: observe each new behavioral test fail before changing production code.
- Behavior changes require one new reverse-chronological `docs/Changelog.md` entry after final verification.
- Record the architectural decision in ADR-011 and link current-state documentation to it.

## File Map

| File | Responsibility after this change |
| --- | --- |
| `internal/transaction/journal.go` | Defines the single current shared envelope and both driver payloads. |
| `internal/transaction/filesystem_journal_store.go` | Strictly decodes one current envelope and preserves invalid active files. |
| `internal/transaction/validation.go` | Accepts only schema version 1 and validates driver/payload/phase semantics. |
| `internal/transaction/{install,uninstall,mcp_install,mcp_uninstall}.go` | Creates schema v1 journals for all four transaction variants. |
| `internal/transaction/journal_legacy.go` | Deleted; no legacy type or normalization remains. |
| `internal/transaction/journal_schema_test.go` | Proves the sole supported envelope and all fail-closed format boundaries. |
| `internal/transaction/filesystem_journal_store_test.go` | Proves invalid files are returned as invalid and preserved byte-for-byte. |
| `ivory-tower/adr/ADR-011-reset-journal-schema-before-release.md` | Records the development-stage compatibility decision. |
| Feature/C4/BUILD/project docs | Describe one schema v1 envelope, with no legacy recovery claim. |

---

### Task 1: Strictly Accept Only the Shared Schema v1 Envelope

**Files:**
- Delete: `internal/transaction/journal_legacy.go`
- Replace: `internal/transaction/journal_v2_test.go`
- Create: `internal/transaction/journal_schema_test.go`
- Modify: `internal/transaction/filesystem_journal_store.go`
- Modify: `internal/transaction/filesystem_journal_store_test.go`
- Modify: `internal/transaction/recovery_test.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/validation_test.go`

**Interfaces:**
- Consumes: `decodeExactJSON(contents []byte, target any) error`, `validateJournal(Journal) error`, `ErrInvalidJournal`.
- Produces: `decodeJournal(contents []byte) (Journal, error)` that accepts only the current schema v1 envelope.

- [ ] **Step 1: Replace legacy-success tests with failing strict-format tests**

Delete the legacy normalization success test. Add a table in `journal_schema_test.go` that starts from JSON-marshaled `validInstallJournal(t)` and mutates these cases:

```go
func TestDecodeJournalAcceptsOnlyCurrentSchemaV1Envelope(t *testing.T) {
    valid := validInstallJournal(t)
    valid.SchemaVersion = 1
    contents, err := json.Marshal(valid)
    if err != nil { t.Fatal(err) }
    got, err := decodeJournal(contents)
    if err != nil || !reflect.DeepEqual(got, valid) {
        t.Fatalf("decodeJournal(valid) = %+v, %v", got, err)
    }

    legacy := legacyFlatJournalJSON(valid)
    invalid := map[string][]byte{
        "missing version": bytes.Replace(contents, []byte(`"schema_version":1,`), nil, 1),
        "version zero": bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":0`), 1),
        "version two": bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1),
        "version three": bytes.Replace(contents, []byte(`"schema_version":1`), []byte(`"schema_version":3`), 1),
        "legacy flat": []byte(legacy),
        "trailing JSON": append(append([]byte(nil), contents...), []byte(` {}`)...),
    }
    for name, document := range invalid {
        t.Run(name, func(t *testing.T) {
            if _, err := decodeJournal(document); !errors.Is(err, ErrInvalidJournal) {
                t.Fatalf("decodeJournal() error = %v, want ErrInvalidJournal", err)
            }
        })
    }
}
```

Define the old flat document helper in the same test file so the rejected input is explicit and self-contained:

```go
func legacyFlatJournalJSON(journal Journal) string {
    return `{"schema_version":1,"transaction_id":"` + journal.TransactionID +
        `","operation":"install","phase":"preparing","agent":"codex",` +
        `"power":{"Name":"demo","Type":"skill","Version":"2.0.0"},` +
        `"staging_root":"` + journal.Filesystem.StagingRoot +
        `","target_path":"` + journal.Filesystem.TargetPath +
        `","transaction_dir":"` + journal.Filesystem.TransactionDir +
        `","backup_path":"` + journal.Filesystem.BackupPath + `"}`
}
```

Keep the existing driver/payload table and set its valid fixture to schema 1. Cover missing/unknown driver, missing/both/wrong payload, unknown fields, illegal phases, and trailing JSON. Change the shared `validInstallJournal(t)` fixture to schema 1 in this task because strict store tests persist it directly.

- [ ] **Step 2: Add a failing preservation test at the store boundary**

In `filesystem_journal_store_test.go`, write each invalid document to `active.json`, retain a copy, call `Inspect`, and prove both the sentinel and bytes remain:

```go
_, found, err := store.Inspect()
if !found || !errors.Is(err, ErrInvalidJournal) { t.Fatalf(...) }
after, readErr := os.ReadFile(path)
if readErr != nil || !bytes.Equal(after, before) {
    t.Fatalf("invalid journal changed: %v, %q", readErr, after)
}
```

Include the old flat format as one case. Change `validJournalJSON` to marshal `validInstallJournal(t)` instead of returning legacy JSON.

Extend `TestRecoverCorruptJournalFailsClosed` in `recovery_test.go`: write the old flat bytes, call `Recover`, assert both `ErrRecoveryFailed` and `ErrInvalidJournal`, then read `journalPath` and compare it byte-for-byte with the original. This proves the high-level recovery boundary also preserves malformed files.

- [ ] **Step 3: Run focused tests and verify RED**

Run:

```bash
go test -count=1 ./internal/transaction -run 'TestDecodeJournalAcceptsOnlyCurrentSchemaV1Envelope|TestFilesystemJournalStoreRejectsInvalidDocuments|TestValidateJournal'
```

Expected: FAIL because the current envelope still requires version 2 and the legacy flat document is still normalized successfully.

- [ ] **Step 4: Implement the single-format decoder and validator**

Delete `journal_legacy.go`. Replace `decodeJournal` with direct strict decoding:

```go
func decodeJournal(contents []byte) (Journal, error) {
    var journal Journal
    if err := decodeExactJSON(contents, &journal); err != nil {
        return Journal{}, err
    }
    if err := validateJournal(journal); err != nil {
        return Journal{}, err
    }
    return journal, nil
}
```

Change the first `validateJournal` guard to:

```go
if journal.SchemaVersion != 1 {
    return fmt.Errorf("schema version %d: %w", journal.SchemaVersion, ErrInvalidJournal)
}
```

Remove legacy-only helpers from tests. Ensure every JSON decoding or semantic validation failure continues to wrap `ErrInvalidJournal`.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run:

```bash
go test -count=1 ./internal/transaction -run 'TestDecodeJournalAcceptsOnlyCurrentSchemaV1Envelope|TestFilesystemJournalStoreRejectsInvalidDocuments|TestValidateJournal'
```

Expected: PASS.

- [ ] **Step 6: Commit the decoder boundary**

```bash
git add internal/transaction
git commit -m "refactor(transaction): reset journal envelope to schema v1"
```

---

### Task 2: Make Every Transaction Producer Write Schema v1

**Files:**
- Modify: `internal/transaction/install.go`
- Modify: `internal/transaction/uninstall.go`
- Modify: `internal/transaction/mcp_install.go`
- Modify: `internal/transaction/mcp_uninstall.go`
- Modify: `internal/transaction/install_test.go`
- Modify: `internal/transaction/uninstall_test.go`
- Modify: `internal/transaction/mcp_install_test.go`
- Modify: `internal/transaction/mcp_uninstall_test.go`
- Modify: `internal/transaction/recovery_test.go`
- Modify: `internal/transaction/mcp_recovery_test.go`
- Modify: `internal/lifecycle/mcp_integration_test.go`

**Interfaces:**
- Consumes: `Journal{SchemaVersion, Driver, ...}` and the schema-v1-only store from Task 1.
- Produces: all new filesystem and agent-cli transactions persist `SchemaVersion: 1`.

- [ ] **Step 1: Add failing producer assertions**

In each transaction family’s existing journal-creation test, assert:

```go
if got.SchemaVersion != 1 {
    t.Fatalf("journal schema = %d, want 1", got.SchemaVersion)
}
```

The four required producers are Skill Install, Skill Uninstall, MCP Install/Update, and MCP Uninstall. Change static recovery and integration fixtures to schema 1 only after observing the producer failures.

- [ ] **Step 2: Run producer tests and verify RED**

Run:

```bash
go test -count=1 ./internal/transaction -run 'TestInstall|TestUninstall|TestMCPInstall|TestMCPUninstall'
```

Expected: FAIL with journal schema 2 where producers still construct `SchemaVersion: 2`.

- [ ] **Step 3: Change all producers and fixtures to schema 1**

Change exactly these constructors:

```go
Journal{SchemaVersion: 1, Driver: DriverFilesystem, ...} // Skill install/uninstall
Journal{SchemaVersion: 1, Driver: DriverAgentCLI, ...}   // MCP install/uninstall
```

Update hand-built journal fixtures in `recovery_test.go`, `mcp_recovery_test.go`, `validation_test.go`, and `mcp_integration_test.go` to version 1. Do not change driver, payload, phase, or recovery expectations.

- [ ] **Step 4: Prove both recovery drivers still work serially**

Run:

```bash
go test -count=1 ./internal/transaction ./internal/lifecycle
```

Expected: PASS, including `TestCrossTypeRecoveryKeepsFilesystemDriverIndependentOfMCPResolver` and `TestMCPIntegrationSearchRecoversInterruptedAgentCLIJournal`.

- [ ] **Step 5: Scan for obsolete production/test version literals**

Run:

```bash
! rg -n 'SchemaVersion:\s*2|SchemaVersion != 2|schema_version.?2|journal_v2|legacyFilesystemJournal|legacyJournalJSON' internal --glob '*.go'
```

Expected: no matches and exit 0.

- [ ] **Step 6: Commit producer changes**

```bash
git add internal/transaction internal/lifecycle/mcp_integration_test.go
git commit -m "refactor(transaction): write one journal schema"
```

---

### Task 3: Record ADR-011 and Align Current-State Documentation

**Files:**
- Create: `ivory-tower/adr/ADR-011-reset-journal-schema-before-release.md`
- Modify: `ivory-tower/adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md`
- Modify: `ivory-tower/features/013-agent-mcp-adapters/spec.md`
- Modify: `ivory-tower/features/013-agent-mcp-adapters/plan.md`
- Modify: `ivory-tower/features/013-agent-mcp-adapters/verification.md`
- Modify: `ivory-tower/features/007-central-lifecycle-transactions/feature-overview.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/common-journal-recovery.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/uninstall.mmd`
- Modify: `BUILD.md`

**Interfaces:**
- Consumes: the verified single schema v1 implementation from Tasks 1–2.
- Produces: ADR-011 and current-state documentation that cites it without rewriting historical task records.

- [ ] **Step 1: Write ADR-011**

Use the repository ADR template with:

```markdown
# Reset Journal Schema Before Release

- Status：Accepted
- Date：2026-08-13
- Decision Makers：APM maintainers

## Decision

APM 在开发阶段不兼容旧平铺 journal。当前带显式 driver 和互斥类型载荷的共享信封是唯一正式格式，编号为 schema v1；任意其他格式返回 ErrInvalidJournal 并原样保留。
```

Document consequences: less compatibility code, clean future versioning, developer must manually handle stale development journals. Alternatives: keep v2 only, keep compatibility, remove version field.

- [ ] **Step 2: Mark ADR-010’s compatibility clause as superseded**

Add a dated note near the top of ADR-010 linking ADR-011. Preserve the driver/shared-envelope decision; explicitly state only the legacy-version paragraph is superseded.

- [ ] **Step 3: Align Feature and architecture documents**

Replace current-state claims as follows:

```text
"schema v2 + legacy schema v1 recovery"
→ "唯一 schema v1 shared envelope；filesystem/agent-cli driver；其他格式 ErrInvalidJournal"
```

In `verification.md`, replace the legacy-normalization evidence row with strict schema and invalid-file-preservation test names. In Feature 013’s original plan, add a dated supersession note rather than falsifying completed historical checkboxes. Update C4 recovery flow to route schema v1 directly to `driver`; remove the normalization branch.

- [ ] **Step 4: Preserve historical Changelog text**

Do not edit the completed 2026-08-12 MCP entry. It remains evidence of what that task delivered at the time. The new behavior is recorded only in Task 4’s new entry.

- [ ] **Step 5: Verify documentation consistency**

Run:

```bash
rg -n 'schema v1|ErrInvalidJournal|ADR-011' BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'
git diff --check
```

Manually classify remaining `schema v2`/legacy matches: only historical Changelog text, the reset design’s background, or explicitly superseded historical plan text may remain. Current-state prose and diagrams must have none.

- [ ] **Step 6: Commit ADR and documentation**

```bash
git add BUILD.md ivory-tower
git commit -m "docs(transaction): record single journal schema"
```

---

### Task 4: Final Verification and Changelog

**Files:**
- Modify: `docs/Changelog.md`
- Modify: `ivory-tower/features/013-agent-mcp-adapters/journal-schema-reset-plan.md`

**Interfaces:**
- Consumes: all implementation and documentation commits from Tasks 1–3.
- Produces: one reverse-chronological completion record and checked plan evidence.

- [ ] **Step 1: Run formatting and focused verification**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./internal/transaction ./internal/lifecycle
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 2: Run full verification**

```bash
go test -count=1 ./...
go test -race -count=1 ./internal/transaction ./internal/lifecycle
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0; integration tests resolve only temporary fake Agent CLI executables.

- [ ] **Step 3: Audit requirements and stale compatibility code**

Run:

```bash
! rg -n 'legacyFilesystemJournal|legacyJournalJSON|journal_v2|SchemaVersion:\s*2|SchemaVersion != 2' internal --glob '*.go'
rg -n 'SchemaVersion:\s*1|SchemaVersion != 1' internal/transaction internal/lifecycle --glob '*.go'
git status --short
```

Expected: the negative scan exits 0; positive scan shows the four producers, validators, and schema-v1 test fixtures; only intended documentation changes remain uncommitted.

- [ ] **Step 4: Add one Changelog entry**

Add a top `## 2026-08-13` entry using `/home/liangff/.codex/docs/Changelog.md`:

- classification: `功能变更`;
- user request: APM is pre-release and journal backward compatibility is unnecessary;
- related commits: design, plan, decoder, producers, ADR/docs;
- result: one schema v1 shared envelope and strict `ErrInvalidJournal` preservation boundary;
- validation: paste the exact successful commands from Steps 1–3;
- limitation: old development journals require manual handling; no migration or deletion is performed.

- [ ] **Step 5: Check completed plan boxes and commit completion docs**

Mark only steps supported by actual command output, then:

```bash
git add docs/Changelog.md ivory-tower/features/013-agent-mcp-adapters/journal-schema-reset-plan.md
git commit -m "docs(changelog): record journal schema reset"
```

- [ ] **Step 6: Run post-commit smoke verification**

```bash
git status --short
go test -count=1 ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: clean worktree and all commands exit 0.

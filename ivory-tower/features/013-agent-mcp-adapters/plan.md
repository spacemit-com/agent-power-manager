# Agent MCP Adapter and Recoverable CLI Transactions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add versioned stdio/HTTP MCP installation and uninstallation for Codex, Claude Code, and Hermes through their CLIs while preserving APM manifests with one cross-type recoverable journal.

**Architecture:** Keep the existing filesystem Skill adapter and transaction path intact behind a `filesystem` journal driver, and add a separate MCP adapter plus `agent-cli` transaction driver. Lifecycle, locking, Cloud version selection, Agent batching, result rendering, and the per-Agent manifest remain shared; MCP definitions come directly from Cloud and never enter Cache or staging.

**Tech Stack:** Go 1.26.5, standard library `context`/`encoding/json`/`os/exec`/`net/url`, Cobra CLI, existing flock/manifest/journal infrastructure, fake CLI executables in isolated Go tests.

## Global Constraints

- Implement [Feature 013](spec.md) and [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md) without adding Plugin support.
- Preserve the current Search/List/Install/Uninstall CLI syntax, output formats, lock boundaries, Agent ordering, fail-fast behavior, and Release suppression rules.
- MCP supports only `stdio(command,args[])` and `http(url)`; no SSE, env, headers, bearer token, OAuth, login, secrets, or tool selection.
- MCP uses `Cloud.GetMCP` directly and must never call Cache, Fetch, staging, or direct Agent configuration I/O.
- Agent state checks and mutations must use the target Agent CLI found through `exec.LookPath`; command argv must never pass through a shell.
- Each Agent CLI invocation has a fixed 30-second timeout, bounded captured output, and fail-closed parsing.
- Claude Code MCP operations use only `--scope user`; local/project state is never modified.
- Hermes normal Add/Remove trusts explicit `Saved`/`Removed` text and does not run a post-mutation List; preflight and recovery still use `hermes mcp list`.
- Every manifest repair means `Remove(agent, mcp, name)` only; never delete the manifest file or unrelated entries.
- Keep one global active journal; schema v1 Skill journals remain readable, and all new Skill/MCP journals use schema v2 with an explicit driver.
- Add brief normative Chinese comments to important new interfaces and non-obvious recovery constraints.
- Automated tests must use temporary fake `codex`/`claude`/`hermes` executables and temporary `APM_HOME`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, and `HERMES_HOME`; never execute a real Agent mutation.
- Do not add a Changelog entry until the entire feature is implemented and all final verification commands pass.

---

## Planned File Structure

| File | Responsibility |
| --- | --- |
| `internal/domain/mcp.go` | MCP transport, server definition, clone, and strict validation. |
| `internal/domain/mcp_test.go` | Discriminated-union validation and input immutability tests. |
| `internal/cloud/local_mcp.go` | Strict `MCP.json` decode and exact-version `GetMCP`. |
| `internal/cloud/local_mcp_test.go` | MCP resource discovery/GetMCP/error tests. |
| `internal/adapter/mcp_adapter.go` | Public MCP adapter contract, presence/mutation states, shared workflow. |
| `internal/adapter/mcp_runner.go` | LookPath, timeout, bounded stdout/stderr, and subprocess execution. |
| `internal/adapter/mcp_codex.go` | Codex argv and JSON state parser. |
| `internal/adapter/mcp_claude.go` | Claude user-scope argv and text state parser. |
| `internal/adapter/mcp_hermes.go` | Hermes argv, fixed stdin, table parser, and success markers. |
| `internal/adapter/mcp_*_test.go` | Shared runner and per-Agent adapter contract tests. |
| `internal/transaction/journal.go` | Schema v2 shared envelope and driver-specific payloads. |
| `internal/transaction/journal_legacy.go` | Strict schema v1 decode and conversion to the v2 in-memory model. |
| `internal/transaction/mcp_install.go` | MCP install/update transaction and rollback. |
| `internal/transaction/mcp_uninstall.go` | MCP uninstall transaction and missing-target repair. |
| `internal/transaction/mcp_recovery.go` | Agent-CLI recovery routing and phase convergence. |
| `internal/transaction/mcp_*_test.go` | Mutation phase, drift, rollback, and recovery tests. |
| `internal/lifecycle/install_mcp.go` | MCP-specific single-Agent install flow. |
| `internal/lifecycle/uninstall_mcp.go` | MCP-specific single-Agent uninstall flow. |
| `internal/lifecycle/*_mcp_test.go` | Lifecycle order/outcome/drift/Cache-exclusion tests. |
| `internal/lifecycle/mcp_integration_test.go` | Fully isolated Cloud→lifecycle→transaction→fake CLI integration. |
| `internal/cloud/resources/mcps/.../MCP.json` | Development Cloud MCP fixtures. |
| `ivory-tower/features/013-agent-mcp-adapters/verification.md` | Requirement-to-test/source completion evidence. |

Existing files are modified only where their declared boundary expands: `internal/cloud/client.go`, `internal/cloud/local_client.go`, `internal/adapter/registry.go`, `internal/adapter/errors.go`, `internal/lifecycle/dependencies.go`, `internal/lifecycle/install_agent.go`, `internal/lifecycle/uninstall_agent.go`, `internal/transaction/{manager,validation,recovery,filesystem_journal_store,path_state,install,uninstall}.go`, their tests, `cmd/apm/main.go`, C4/feature/project docs, BUILD, and Changelog.

### Task 1: MCP Domain Model and Cloud Retrieval

**Files:**
- Create: `internal/domain/mcp.go`
- Create: `internal/domain/mcp_test.go`
- Create: `internal/cloud/local_mcp.go`
- Create: `internal/cloud/local_mcp_test.go`
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/local_client.go`
- Modify: `internal/cloud/local_client_test.go`

**Interfaces:**
- Produces: `domain.MCPTransport`, `domain.MCPServer`, `func (MCPServer) Validate() error`, `func (MCPServer) Clone() MCPServer`.
- Produces: `cloud.Client.GetMCP(domain.PowerRef) (domain.MCPServer, error)` and `(*LocalClient).GetMCP`.
- Preserves: `cloud.Client.Get(domain.PowerRef) error` as the Skill-only cache writer.

- [x] **Step 1: Write failing MCP domain tests**

Add a table that proves the exact union and clone behavior:

```go
func TestMCPServerValidate(t *testing.T) {
    tests := []struct {
        name    string
        server  MCPServer
        wantErr bool
    }{
        {name: "stdio", server: MCPServer{Transport: MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}},
        {name: "stdio empty args", server: MCPServer{Transport: MCPTransportStdio, Command: "node"}},
        {name: "http", server: MCPServer{Transport: MCPTransportHTTP, URL: "https://mcp.example.com/mcp"}},
        {name: "blank command", server: MCPServer{Transport: MCPTransportStdio, Command: "  "}, wantErr: true},
        {name: "stdio url", server: MCPServer{Transport: MCPTransportStdio, Command: "npx", URL: "https://example.com"}, wantErr: true},
        {name: "http command", server: MCPServer{Transport: MCPTransportHTTP, URL: "https://example.com", Command: "npx"}, wantErr: true},
        {name: "relative url", server: MCPServer{Transport: MCPTransportHTTP, URL: "/mcp"}, wantErr: true},
        {name: "unsupported scheme", server: MCPServer{Transport: MCPTransportHTTP, URL: "ftp://example.com/mcp"}, wantErr: true},
        {name: "unknown transport", server: MCPServer{Transport: "sse", URL: "https://example.com"}, wantErr: true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.server.Validate()
            if (err != nil) != tt.wantErr {
                t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
    source := MCPServer{Transport: MCPTransportStdio, Command: "npx", Args: []string{"one"}}
    clone := source.Clone()
    clone.Args[0] = "changed"
    if source.Args[0] != "one" { t.Fatalf("Clone mutated source: %#v", source) }
}
```

- [x] **Step 2: Run the domain test and verify it fails**

Run: `go test ./internal/domain -run MCPServer -count=1`

Expected: FAIL because `MCPServer` and transports do not exist.

- [x] **Step 3: Implement the strict MCP domain type**

Create `mcp.go` with the approved fields, `ErrInvalidMCPServer`, URL parsing via `net/url`, exact transport checks, and a deep-copying `Clone`:

```go
func (server MCPServer) Clone() MCPServer {
    server.Args = append([]string(nil), server.Args...)
    return server
}
```

Do not trim or rewrite stored Command/Args/URL values after validation.

- [x] **Step 4: Write failing Cloud MCP tests**

Cover these concrete cases in `local_mcp_test.go`:

```go
func TestLocalClientGetMCPReadsExactVersion(t *testing.T) {
    root := t.TempDir()
    writeMCPJSON(t, root, "filesystem", "v1.2.3", `{"transport":"stdio","command":"npx","args":["-y","pkg"]}`)
    client := newTestLocalClient(t, root)
    got, err := client.GetMCP(domain.PowerRef{Name: "filesystem", Type: domain.PowerTypeMCP, Version: "v1.2.3"})
    if err != nil { t.Fatal(err) }
    want := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "npx", Args: []string{"-y", "pkg"}}
    if !reflect.DeepEqual(got, want) { t.Fatalf("GetMCP() = %#v, want %#v", got, want) }
}
```

Also assert: List returns both Skill and MCP refs in Type/Name/Version order; missing/wrong-type/symlink marker returns `cloud.ErrNotFound`; unknown field, second JSON value, invalid union, and wrong JSON types fail; `GetMCP` leaves configured cache and staging paths absent.

- [x] **Step 5: Run Cloud tests and verify they fail**

Run: `go test ./internal/cloud -run 'MCP|List' -count=1`

Expected: FAIL because `GetMCP` is missing and List scans only Skills.

- [x] **Step 6: Implement Cloud MCP discovery and retrieval**

Add `GetMCP` to `cloud.Client`. Refactor `LocalClient.List` into two focused scanners that recognize only regular `SKILL.md` and `MCP.json` markers, concatenate refs, then apply the existing stable sort. In `local_mcp.go`, use `json.Decoder.DisallowUnknownFields`, require EOF after one value, call `MCPServer.Validate`, and return a cloned value.

- [x] **Step 7: Run focused and package tests**

Run: `go test ./internal/domain ./internal/cloud -count=1`

Expected: PASS, including all existing Skill Cloud tests.

- [x] **Step 8: Commit the domain and Cloud slice**

```bash
git add internal/domain internal/cloud
git commit -m "feat(mcp): add versioned Cloud definitions"
```

### Task 2: MCP Adapter Contract and Safe CLI Runner

**Files:**
- Create: `internal/adapter/mcp_adapter.go`
- Create: `internal/adapter/mcp_runner.go`
- Create: `internal/adapter/mcp_runner_test.go`
- Modify: `internal/adapter/errors.go`

**Interfaces:**
- Consumes: `domain.MCPServer.Validate`, `domain.PowerRef`.
- Produces: `adapter.MCPAdapter`, `MCPPresence`, `MCPMutationState`, and `MCPMutationResult` exactly as specified.
- Produces private testable boundary: `mcpCommandRunner.Run(context.Context, string, []string, string) (mcpCommandResult, error)`.

- [x] **Step 1: Write compile-time and runner failure tests**

Define a fake runner and assert the public enum values exactly match the journal vocabulary. Add integration-style runner tests that create a temporary executable and verify argv/stdin capture:

```go
func TestDefaultMCPRunnerUsesLookPathAndPreservesArgv(t *testing.T) {
    fakeBin := writeFakeCLI(t, `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_ARGV"
cat > "$FAKE_STDIN"
printf 'ok'
`)
    t.Setenv("PATH", fakeBin)
    result, err := newDefaultMCPRunner().Run(context.Background(), "codex", []string{"mcp", "add", "demo", "--", "cmd", "a b"}, "answer\n")
    if err != nil { t.Fatal(err) }
    if filepath.Dir(result.Path) != fakeBin { t.Fatalf("resolved path = %q, want inside %q", result.Path, fakeBin) }
    if result.Stdout != "ok" { t.Fatalf("stdout = %q, want ok", result.Stdout) }
    argv, err := os.ReadFile(os.Getenv("FAKE_ARGV"))
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(argv), "a b\n") { t.Fatalf("argv = %q, want preserved spaced argument", argv) }
}
```

Add tests for missing executable before start, context cancellation, injected short timeout, stdout/stderr truncation at the declared bound, and no shell expansion of `$HOME`, backticks, or semicolons.

- [x] **Step 2: Run Adapter runner tests and verify they fail**

Run: `go test ./internal/adapter -run 'MCPRunner|MCPAdapterContract' -count=1`

Expected: FAIL because the MCP contract and runner do not exist.

- [x] **Step 3: Implement the MCP contract and runner**

Implement these exact result carriers:

```go
type mcpCommandResult struct {
    Path     string
    Stdout   string
    Stderr   string
    ExitCode int
    Started  bool
    TimedOut bool
}
```

The default runner must resolve with `exec.LookPath`, create `context.WithTimeout(ctx, 30*time.Second)`, call `exec.CommandContext` directly, use a fixed-size writer for each output stream, and close stdin unless a non-empty scripted input is supplied. It returns enough fact for adapters to distinguish pre-start `not-applied` from post-start `unknown`.

- [x] **Step 4: Add stable MCP adapter errors**

Add and document:

```go
ErrInvalidMCP        = errors.New("invalid MCP server")
ErrMCPStateUnknown   = errors.New("MCP state is unknown")
ErrMCPConflict       = errors.New("MCP server already exists")
ErrMCPMutationUnknown = errors.New("MCP mutation result is unknown")
ErrAgentCLIUnavailable = errors.New("agent CLI is unavailable")
```

Every wrapped error must include Agent, operation, Power name, executable, and bounded stderr context where available.

- [x] **Step 5: Run focused Adapter tests**

Run: `go test ./internal/adapter -run 'MCPRunner|MCPAdapterContract' -count=1`

Expected: PASS without invoking any real Agent binary.

- [x] **Step 6: Commit the contract and runner**

```bash
git add internal/adapter/mcp_adapter.go internal/adapter/mcp_runner.go internal/adapter/mcp_runner_test.go internal/adapter/errors.go
git commit -m "feat(mcp): add safe Agent CLI runner"
```

### Task 3: Codex, Claude, Hermes MCP Adapters and Registry

**Files:**
- Create: `internal/adapter/mcp_codex.go`
- Create: `internal/adapter/mcp_claude.go`
- Create: `internal/adapter/mcp_hermes.go`
- Create: `internal/adapter/mcp_codex_test.go`
- Create: `internal/adapter/mcp_claude_test.go`
- Create: `internal/adapter/mcp_hermes_test.go`
- Modify: `internal/adapter/registry.go`
- Modify: `internal/adapter/registry_test.go`

**Interfaces:**
- Consumes: `mcpCommandRunner`, `MCPAdapter`, `domain.MCPServer`.
- Produces: `Registry.ResolveSkill(domain.AgentID) (Adapter, error)` and `Registry.ResolveMCP(domain.AgentID) (MCPAdapter, error)`.
- Preserves: `Registry.RefreshInstalled()` root-directory detection and lexicographic Agent order.

- [x] **Step 1: Write failing exact-argv tests for all Agents**

Use a recording runner and assert these full arrays:

```go
codexStdio := []string{"mcp", "add", "demo", "--", "npx", "-y", "pkg"}
codexHTTP := []string{"mcp", "add", "demo", "--url", "https://example.com/mcp"}
claudeStdio := []string{"mcp", "add", "--scope", "user", "demo", "--", "npx", "-y", "pkg"}
claudeHTTP := []string{"mcp", "add", "--scope", "user", "--transport", "http", "demo", "https://example.com/mcp"}
hermesStdio := []string{"mcp", "add", "demo", "--command", "npx", "--args", "-y", "pkg"}
hermesHTTP := []string{"mcp", "add", "demo", "--url", "https://example.com/mcp"}
```

Also prove empty stdio args omit Hermes `--args`, all Remove arrays are exact, and no Agent runs a post-mutation Inspect after an applied result.

- [x] **Step 2: Write failing Inspect parser tests**

Codex fixtures cover valid `get --json`, explicit not-found, invalid/trailing JSON, empty/duplicate names, and nonzero ambiguous errors. Claude fixtures cover confirmed `User scope`, absent, local/project-only, health-error ambiguity, and format changes. Hermes fixtures cover ANSI-stripped table rows, empty-list text, exact names, duplicate rows, malformed headers, truncation, and nonzero output.

Use assertions like:

```go
presence, err := subject.Inspect(context.Background(), "demo")
if presence != MCPPresent || err != nil { t.Fatalf("Inspect() = %q, %v", presence, err) }
```

- [x] **Step 3: Write failing Hermes interaction/result tests**

Prove stdio scripted stdin begins with default acceptance, HTTP begins with `n\n`, Remove confirms with a blank default-yes line, applied requires an exact-name Saved/Removed marker, exit zero without a marker is unknown, and a connection failure is not applied only when output explicitly proves no save.

- [x] **Step 4: Run per-Agent tests and verify they fail**

Run: `go test ./internal/adapter -run 'CodexMCP|ClaudeMCP|HermesMCP' -count=1`

Expected: FAIL because the Agent implementations do not exist.

- [x] **Step 5: Implement Agent-specific adapters**

Keep parser/argv logic in its Agent file. Shared Add/Remove workflow validates `ref.Type == mcp`, version/name presence, and `server.Validate`; it maps pre-start errors to not-applied, explicit success to applied, and all post-start ambiguous results to unknown. Do not read Agent config files.

- [x] **Step 6: Write failing Registry tests**

Replace the old generic Resolve expectations with:

```go
for _, agent := range []domain.AgentID{domain.AgentCodex, domain.AgentClaudeCode, domain.AgentHermes} {
    if skill, err := registry.ResolveSkill(agent); err != nil || skill == nil { t.Fatalf("ResolveSkill(%q) = %v, %v", agent, skill, err) }
    if mcp, err := registry.ResolveMCP(agent); err != nil || mcp == nil { t.Fatalf("ResolveMCP(%q) = %v, %v", agent, mcp, err) }
}
```

Retain detection ordering/error tests and prove unknown Agents fail both resolvers with `ErrUnsupportedAdapter`.

- [x] **Step 7: Implement the type-specific Registry**

Store independent `skillByAgent` and `mcpByAgent` maps plus the existing ordered Agent IDs. `RefreshInstalled` must continue using Agent root detection only and must not call LookPath or an MCP CLI.

- [x] **Step 8: Run all Adapter tests**

Run: `go test ./internal/adapter -count=1`

Expected: PASS; fake runner call logs show no real executable paths.

- [x] **Step 9: Commit the Agent adapters**

```bash
git add internal/adapter
git commit -m "feat(mcp): add Agent-specific adapters"
```

### Task 4: Journal Schema v2 and Legacy Skill Compatibility

**Files:**
- Create: `internal/transaction/journal_legacy.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/filesystem_journal_store.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/path_state.go`
- Modify: `internal/transaction/install.go`
- Modify: `internal/transaction/uninstall.go`
- Modify: `internal/transaction/recovery.go`
- Modify: `internal/transaction/*_test.go`

**Interfaces:**
- Produces: `DriverFilesystem`, `DriverAgentCLI`, `FilesystemJournalPayload`, `AgentCLIJournalPayload`.
- Produces: schema v1 strict decode → normalized schema v2 in-memory `Journal`.
- Preserves: all existing filesystem transaction phases, paths, recovery directions, and filesystem behavior.

- [x] **Step 1: Write failing schema v2 and legacy decode tests**

Add exact JSON fixtures for:

```json
{
  "schema_version": 2,
  "transaction_id": "00112233445566778899aabbccddeeff",
  "driver": "filesystem",
  "operation": "install",
  "phase": "preparing",
  "agent": "codex",
  "power": {"Name":"demo","Type":"skill","Version":"v1.0.0"},
  "filesystem": {
    "staging_root": "/tmp/apm/staging",
    "target_path": "/tmp/codex/skills/demo",
    "transaction_dir": "/tmp/codex/skills/.apm-transactions/00112233445566778899aabbccddeeff",
    "backup_path": "/tmp/codex/skills/.apm-transactions/00112233445566778899aabbccddeeff/backup"
  }
}
```

Use test-generated canonical staging paths rather than hard-coded `/tmp/apm/staging`. Prove a current schema v1 fixture decodes to `SchemaVersion==2`, `DriverFilesystem`, and a populated filesystem payload. Reject unknown driver, missing driver, both payloads, wrong payload, unknown fields, invalid phases, and schema 3.

- [x] **Step 2: Run journal tests and verify they fail**

Run: `go test ./internal/transaction -run 'Journal|Legacy' -count=1`

Expected: FAIL because the shared envelope and drivers do not exist.

- [x] **Step 3: Implement the v2 envelope and v1 conversion**

Use this in-memory shape:

```go
type Journal struct {
    SchemaVersion int                     `json:"schema_version"`
    TransactionID string                  `json:"transaction_id"`
    Driver        Driver                  `json:"driver"`
    Operation     Operation               `json:"operation"`
    Phase         Phase                   `json:"phase"`
    Agent         domain.AgentID          `json:"agent"`
    Power         domain.PowerRef         `json:"power"`
    Previous      *domain.PowerRef        `json:"previous,omitempty"`
    Filesystem    *FilesystemJournalPayload `json:"filesystem,omitempty"`
    AgentCLI      *AgentCLIJournalPayload `json:"agent_cli,omitempty"`
}
```

Decode a small schema probe first; decode schema 1 through a private exact legacy struct and normalize immediately; decode schema 2 strictly into `Journal`. `Create/Update` writes only schema 2.

- [x] **Step 4: Migrate filesystem transaction code to the payload**

Change journal construction and every path access to `journal.Filesystem.*`. Keep `OperationInstall/Uninstall`, phases, artifact validation, rollback, cleanup, and recovery order byte-for-byte equivalent in behavior. Add helpers such as `filesystemPayload(journal)` only when they centralize nil validation rather than hide control flow.

- [x] **Step 5: Run all transaction tests and fix only schema-shape expectations**

Run: `go test ./internal/transaction -count=1`

Expected: PASS. Existing install/uninstall/recovery event order and filesystem assertions remain unchanged; stored new journals assert schema 2/driver filesystem.

- [x] **Step 6: Commit the journal migration**

```bash
git add internal/transaction
git commit -m "refactor(transaction): add typed journal drivers"
```

### Task 5: Recoverable MCP Install and Update Transactions

**Files:**
- Create: `internal/transaction/mcp_install.go`
- Create: `internal/transaction/mcp_install_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/errors.go`
- Modify: `internal/transaction/journal.go`

**Interfaces:**
- Consumes: `adapter.MCPAdapter`, `domain.MCPServer`, v2 `AgentCLIJournalPayload`, manifest writer, journal store.
- Produces: `MCPInstallRequest` and `Manager.InstallMCP(context.Context, MCPInstallRequest) error`.
- Produces phases: `adding-new` and `removing-old` in addition to shared `preparing`, `rolling-back`, `committing`.

- [x] **Step 1: Write request validation tests**

Cover exact combinations: non-MCP Power, empty Agent/name/version, `all`, nil Adapter, mismatched Previous Type/Name, PreviousServer without Previous, first install missing Server, update missing either definition, same previous/target version, and the allowed no-definition conflict/stale probe requests.

Use a request factory:

```go
func validMCPInstallRequest() MCPInstallRequest {
    return MCPInstallRequest{
        Agent: domain.AgentCodex,
        Power: domain.PowerRef{Name: "demo", Type: domain.PowerTypeMCP, Version: "v2.0.0"},
        Server: &domain.MCPServer{Transport: domain.MCPTransportHTTP, URL: "https://new.example/mcp"},
        Adapter: &recordingMCPAdapter{presence: adapter.MCPAbsent},
    }
}
```

- [x] **Step 2: Write failing first-install transaction tests**

Prove order `journal:create(adding-new) → adapter:add → journal:update(committing) → manifest:set → journal:delete`. Add cases for preflight present conflict, preflight absent with no definition invalid request, Add applied, Add not-applied, Add unknown, phase update failure, manifest failure, and journal deletion failure.

- [x] **Step 3: Write failing update transaction tests**

Prove order `journal:create(removing-old) → adapter:remove → journal:update(adding-new) → adapter:add → journal:update(committing) → manifest:set → journal:delete`. Verify:

- preflight absent calls only `ManifestWriter.Remove(agent,mcp,name)`, returns `ErrStaleManifest`, and creates no journal;
- Remove not-applied preserves old state and deletes the rollback journal;
- Remove unknown preserves journal at removing-old;
- Add not-applied adds the old definition back and preserves the old manifest;
- Add unknown preserves journal at adding-new;
- applied plus error follows the applied direction.

- [x] **Step 4: Run MCP install tests and verify they fail**

Run: `go test ./internal/transaction -run 'MCPInstall' -count=1`

Expected: FAIL because `InstallMCP` does not exist.

- [x] **Step 5: Implement MCP install/update transaction flow**

Create the journal before the first write, persist every next intent before invoking the CLI, clone definitions into `AgentCLIJournalPayload`, and never call Cloud from transaction. Map mutation states exactly: applied follows forward direction, not-applied performs current-process rollback, unknown returns an error wrapping `ErrIndeterminateState` and leaves the journal.

- [x] **Step 6: Run focused and full transaction tests**

Run: `go test ./internal/transaction -run 'MCPInstall' -count=1 && go test ./internal/transaction -count=1`

Expected: PASS with filesystem transaction regressions green.

- [x] **Step 7: Commit MCP install transactions**

```bash
git add internal/transaction
git commit -m "feat(mcp): add recoverable install transactions"
```

### Task 6: MCP Uninstall and Cross-Type Recovery

**Files:**
- Create: `internal/transaction/mcp_uninstall.go`
- Create: `internal/transaction/mcp_uninstall_test.go`
- Create: `internal/transaction/mcp_recovery.go`
- Create: `internal/transaction/mcp_recovery_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/recovery.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/validation.go`
- Modify: `internal/transaction/recovery_test.go`

**Interfaces:**
- Produces: `MCPUninstallRequest` and `Manager.UninstallMCP(context.Context, MCPUninstallRequest) (UninstallResult, error)`.
- Consumes: narrow `MCPAdapterResolver.ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)` held by `Service` for recovery.
- Preserves: `Manager.Recover(context.Context) (RecoveryResult, error)` as the single recovery entry point.

- [x] **Step 1: Write failing MCP uninstall tests**

Prove present order `journal:create(removing) → adapter:remove → journal:update(committing) → manifest:remove → journal:delete`. Cover applied/not-applied/unknown, manifest/delete failure, context cancellation, and applied plus error.

For absent state assert:

```go
result, err := service.UninstallMCP(ctx, requestWithNilServer)
if err != nil || !result.TargetMissing { t.Fatalf("UninstallMCP() = %+v, %v", result, err) }
if got, want := manifests.removes, []removeCall{{Agent: domain.AgentCodex, Type: domain.PowerTypeMCP, Name: "demo"}}; !reflect.DeepEqual(got, want) {
    t.Fatalf("manifest removes = %#v, want %#v", got, want)
}
if journals.created || mcpAdapter.removeCalls != 0 {
    t.Fatalf("created/remove calls = %v/%d, want false/0", journals.created, mcpAdapter.removeCalls)
}
```

Also prove an Inspect error leaves both config and manifest unchanged.

- [x] **Step 2: Implement MCP uninstall transaction flow**

Validate/Inspect before journal creation. The absent branch removes only the MCP entry and preserves `TargetMissing=true` even if the manifest removal returns an error. The present branch requires a valid Server, journals removing intent, and uses mutation state to commit, roll back without change, or retain unknown state.

- [x] **Step 3: Write failing phase-table recovery tests**

Use table cases with a fresh Manager and resolver:

| Operation/phase | Inspect | Expected action/direction |
| --- | --- | --- |
| install adding-new, no Previous | present | Set new manifest / commit |
| install adding-new, no Previous | absent | Delete journal / rollback |
| update removing-old | present | Keep old manifest / rollback |
| update removing-old | absent | Add old definition / rollback |
| update adding-new | present | Set new manifest / commit |
| update adding-new | absent | Add old definition / rollback |
| uninstall removing | absent | Remove MCP manifest / commit |
| uninstall removing | present | Keep old manifest / rollback |
| any committing | phase-specific | Complete Set/Remove without Cloud |
| rolling-back | absent | Re-add old when required, then delete journal |
| any mutable phase | Inspect error | Preserve journal and return ErrRecoveryFailed |

Assert RecoveryResult driver-independent fields, direction, original phase, and new MCP action kinds.

- [x] **Step 4: Add cross-type and resolver failure tests**

Prove a Manager recovers a schema v1/v2 filesystem journal when an MCP resolver is installed, recovers an MCP journal before a simulated Skill operation, rejects a second active journal, dynamically calls ResolveMCP with the journal Agent, and retains the journal when the resolver or CLI is unavailable.

- [x] **Step 5: Run recovery tests and verify they fail**

Run: `go test ./internal/transaction -run 'MCPUninstall|MCPRecovery|CrossType' -count=1`

Expected: FAIL because MCP recovery routing is missing.

- [x] **Step 6: Implement driver-based Recover routing**

Keep filesystem logic in `recovery.go`; dispatch `DriverAgentCLI` to `recoverMCP`. Persist committing/rolling-back before recovery writes, use only the resolver and journal definitions, append structured MCP Add/Remove/Set/Remove actions, and delete the shared journal only after convergence.

- [x] **Step 7: Change NewManager to require the recovery resolver**

Use the exact signature:

```go
func NewManager(manifests ManifestWriter, mcpAdapters MCPAdapterResolver) (*Service, error)
```

Reject nil and typed-nil dependencies. Update transaction unit fixtures with a recording resolver; production composition is handled in Task 9.

- [x] **Step 8: Run all transaction tests including race**

Run: `go test ./internal/transaction -count=1 && go test -race ./internal/transaction -count=1`

Expected: PASS for legacy filesystem and new agent-cli drivers.

- [x] **Step 9: Commit uninstall and recovery**

```bash
git add internal/transaction
git commit -m "feat(mcp): recover Agent CLI transactions"
```

### Task 7: Install Lifecycle MCP Branch

**Files:**
- Create: `internal/lifecycle/install_mcp.go`
- Create: `internal/lifecycle/install_mcp_test.go`
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `internal/lifecycle/install_test.go`

**Interfaces:**
- Consumes: `CloudClient.GetMCP`, `AdapterRegistry.ResolveSkill/ResolveMCP`, `TransactionManager.Install/InstallMCP`.
- Preserves: `InstallService.Install` outer lock/batch/result flow and Skill Cache path.

- [x] **Step 1: Update lifecycle dependency fakes to compile with type-specific ports**

Change the interface declarations to:

```go
type AdapterRegistry interface {
    ResolveSkill(domain.AgentID) (adapter.Adapter, error)
    ResolveMCP(domain.AgentID) (adapter.MCPAdapter, error)
    RefreshInstalled() (domain.AgentSnapshot, error)
}
```

Extend lifecycle Cloud/Transaction fakes with recording `GetMCP` and `InstallMCP/UninstallMCP` methods while keeping existing event names for Skill tests.

- [x] **Step 2: Write failing MCP install lifecycle tests**

Cover the approved state table and exact events. First install must be:

```text
acquire, recover, refresh, cloud:list, resolve-mcp:codex,
config:find:codex, mcp:inspect:codex, cloud:get-mcp:v1.0.0,
transaction:install-mcp:codex, release
```

Add same-version present unchanged with no GetMCP/Cache/transaction; no-record present conflict; same/different-version absent stale repair through InstallMCP without GetMCP; update present with old GetMCP before new GetMCP; GetMCP old/new failures; all-Agent refetch of definitions; Inspect unknown fail-fast; and installed/upgraded/downgraded outcomes.

- [x] **Step 3: Run lifecycle MCP install tests and verify they fail**

Run: `go test ./internal/lifecycle -run 'InstallMCP' -count=1`

Expected: FAIL because `installMCP` dispatch is absent.

- [x] **Step 4: Implement a narrow type dispatch**

Refactor existing `installOne` to switch only on `ref.Type`:

```go
switch ref.Type {
case domain.PowerTypeSkill:
    return s.installSkill(ctx, agent, ref)
case domain.PowerTypeMCP:
    return s.installMCP(ctx, agent, ref)
default:
    return cli.InstallItem{}, fmt.Errorf("install power type %q: %w", ref.Type, adapter.ErrUnsupportedAdapter)
}
```

Move the current body unchanged to `installSkill`. Implement `installMCP` from the state table, clone Cloud definitions into requests, and compare SemVer only after transaction success.

- [x] **Step 5: Run all Install lifecycle tests**

Run: `go test ./internal/lifecycle -run 'Install' -count=1`

Expected: PASS; existing Skill event order remains exact and MCP fakes show zero Cache calls.

- [x] **Step 6: Commit lifecycle Install support**

```bash
git add internal/lifecycle
git commit -m "feat(mcp): wire install lifecycle"
```

### Task 8: Uninstall Lifecycle MCP Branch

**Files:**
- Create: `internal/lifecycle/uninstall_mcp.go`
- Create: `internal/lifecycle/uninstall_mcp_test.go`
- Modify: `internal/lifecycle/uninstall_agent.go`
- Modify: `internal/lifecycle/uninstall_agent_test.go`
- Modify: `internal/lifecycle/uninstall_test.go`

**Interfaces:**
- Consumes: `CloudClient.GetMCP`, `AdapterRegistry.ResolveMCP`, `TransactionManager.UninstallMCP`.
- Requires: add `CloudClient` to `UninstallService` and `NewUninstall` because MCP rollback definitions come from exact Cloud versions.
- Preserves: local manifest resolution, Warn timing, TargetMissing handling, fail-fast, and result rendering.

- [x] **Step 1: Write constructor dependency tests for Uninstall Cloud**

Add nil/typed-nil Cloud cases at the same deterministic position used by the constructor, and update all valid fixture construction to pass the existing fake Cloud. Assert no Cloud call for a missing manifest record or Skill uninstall.

- [x] **Step 2: Write failing MCP uninstall lifecycle tests**

Cover:

- no manifest record → Warn/unexecuted without ResolveMCP, Inspect, GetMCP, or transaction;
- present → ResolveMCP, Inspect, exact-version GetMCP, UninstallMCP, completed;
- absent → UninstallMCP with nil Server, TargetMissing Warn, completed, no GetMCP;
- Inspect error → full-ref failed with no Cloud/transaction;
- GetMCP error → full-ref failed with no transaction;
- transaction returns `TargetMissing=true` plus error → Warn before Release and failed item;
- mixed per-Agent manifests and `all` fail-fast preserve current behavior.

- [x] **Step 3: Run MCP uninstall tests and verify they fail**

Run: `go test ./internal/lifecycle -run 'UninstallMCP' -count=1`

Expected: FAIL because Uninstall has no Cloud dependency or MCP branch.

- [x] **Step 4: Implement Uninstall type dispatch and MCP flow**

Keep manifest `List`/resolve in `uninstallOne`, then dispatch the resolved ref to unchanged `uninstallSkill` or new `uninstallMCP`. Only MCP present calls `GetMCP`; absent delegates single-entry repair to Transaction and logs the existing target-missing Warn from `UninstallResult`.

- [x] **Step 5: Run all lifecycle tests**

Run: `go test ./internal/lifecycle -count=1`

Expected: PASS with existing Search/List/Skill Install/Skill Uninstall ordering unchanged.

- [ ] **Step 6: Commit lifecycle Uninstall support**

```bash
git add internal/lifecycle
git commit -m "feat(mcp): wire uninstall lifecycle"
```

### Task 9: Production Composition, Cloud Fixtures, and Isolated End-to-End Tests

**Files:**
- Modify: `cmd/apm/main.go`
- Create: `cmd/apm/main_test.go`
- Create: `internal/cloud/resources/mcps/filesystem/v0.0.1/MCP.json`
- Create: `internal/cloud/resources/mcps/filesystem/v0.1.0/MCP.json`
- Create: `internal/cloud/resources/mcps/company-tools/v0.0.1/MCP.json`
- Create: `internal/lifecycle/mcp_integration_test.go`
- Modify: Cloud/Search/List expected fixture tests

**Interfaces:**
- Consumes: `transaction.NewManager(repository, registry)` and `lifecycle.NewUninstall(..., cloudClient, ...)`.
- Produces: default CLI factories capable of recovering either driver before every recovery-enabled command.

- [ ] **Step 1: Write failing production composition tests/build checks**

Add `cmd/apm/main_test.go` that sets `APM_HOME` and `XDG_CACHE_HOME` to `t.TempDir()`, clears all Agent root overrides, calls `newSearchLifecycle`, `newListLifecycle`, `newInstallLifecycle`, and `newUninstallLifecycle`, and asserts every returned lifecycle is non-nil with no construction error. Then compile after the NewManager/NewUninstall signatures change; the source change in Step 2 supplies the same Registry instance to recovery and normal resolution.

```go
func TestDefaultLifecycleFactoriesConstructWithMCPRecoveryResolver(t *testing.T) {
    root := t.TempDir()
    t.Setenv("APM_HOME", root)
    t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
    t.Setenv("CODEX_HOME", "")
    t.Setenv("CLAUDE_CONFIG_DIR", "")
    t.Setenv("HERMES_HOME", "")
    factories := []func() (any, error){
        func() (any, error) { return newSearchLifecycle() },
        func() (any, error) { return newListLifecycle() },
        func() (any, error) { return newInstallLifecycle() },
        func() (any, error) { return newUninstallLifecycle() },
    }
    for index, factory := range factories {
        got, err := factory()
        if err != nil || got == nil { t.Fatalf("factory %d = %v, %v", index, got, err) }
    }
}
```

Run: `go test ./cmd/apm ./internal/cli -count=1 && go build -o /dev/null ./cmd/apm`

Expected: FAIL until all factories construct Registry before TransactionManager and pass Cloud to Uninstall.

- [ ] **Step 2: Update all four default factories**

For Search/List/Install/Uninstall, construct one Registry per factory, pass it to `transaction.NewManager(repository, registry)`, and pass the same instance to lifecycle where that command needs normal resolution. Search now constructs Registry solely so Recover can resolve an interrupted MCP journal. Preserve one Repository instance per factory for recovery and normal manifest access.

- [ ] **Step 3: Add committed local Cloud MCP fixtures**

Use exact definitions:

```json
{"transport":"stdio","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem"]}
```

for filesystem v0.0.1; use `args=["-y","@modelcontextprotocol/server-filesystem","/workspace"]` for v0.1.0 so the version definitions are observably distinct. Use:

```json
{"transport":"http","url":"https://mcp.example.com/mcp"}
```

for company-tools. Update Cloud/Search/List fixture expectations to include all versions in stable order.

- [ ] **Step 4: Write the isolated end-to-end harness**

Create a fake-bin under `t.TempDir()`, prepend it to PATH, set all four home variables to the same test root subdirectories, and make each fake CLI record state/calls under `FAKE_MCP_STATE`. Before every subprocess invocation, assert the resolved executable path is within fake-bin.

The fake CLIs implement only the exact supported commands and outputs:

```text
codex mcp get --json/add/remove
claude mcp get/add/remove with User scope
hermes mcp list/add/remove with Saved/Removed markers
```

They never read real home directories.

- [ ] **Step 5: Add end-to-end lifecycle cases**

For each Agent, install stdio v0.0.1, verify the fake state and exact APM MCP manifest entry, upgrade to v0.1.0, uninstall, and verify only that MCP entry is removed. Add one HTTP install/uninstall, one unmanaged conflict, one stale-entry repair, and one interrupted agent-cli journal recovered by a subsequent Skill/Search recovery call. Assert Cache/Staging paths are absent throughout MCP-only cases.

- [ ] **Step 6: Run integration and full functional tests**

Run: `go test ./internal/lifecycle ./internal/cloud ./cmd/apm -count=1`

Expected: PASS; the harness reports every executed Agent path inside fake-bin.

- [ ] **Step 7: Commit production wiring and fixtures**

```bash
git add cmd/apm internal/cloud internal/lifecycle internal/cli
git commit -m "feat(mcp): enable end-to-end CLI lifecycle"
```

### Task 10: Architecture Documentation, Changelog, and Completion Audit

**Files:**
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/common-journal-recovery.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/uninstall.mmd`
- Modify: `ivory-tower/features/006-agent-skill-adapters/{feature-overview.md,spec.md}`
- Modify: `ivory-tower/features/007-central-lifecycle-transactions/{feature-overview.md,spec.md}`
- Modify: `ivory-tower/features/010-lifecycle-install/{feature-overview.md,spec.md}`
- Modify: `ivory-tower/features/011-lifecycle-uninstall/{feature-overview.md,spec.md}`
- Modify: `BUILD.md`
- Modify: `docs/Changelog.md`
- Modify: `ivory-tower/features/013-agent-mcp-adapters/plan.md` checkbox state
- Create: `ivory-tower/features/013-agent-mcp-adapters/verification.md`

**Interfaces:**
- Consumes: final verified implementation and commit hashes.
- Produces: current architecture/business documentation that cites ADR-010 and one reverse-chronological Changelog entry for this user request.

- [ ] **Step 1: Update current-state architecture documentation**

Replace “Plugin/MCP unsupported” with the actual matrix: Skill and MCP on all three Agents; Plugin unsupported. Show Cloud List/GetMCP direct flow, Skill-only Cache/Staging, type-specific Registry resolution, one filesystem/agent-cli journal envelope, and cross-type Recover routing. Update Install/Uninstall Mermaid flows with explicit Skill/MCP branches without changing outer lock/Release semantics.

- [ ] **Step 2: Add historical supersession notes to related Features**

Do not rewrite past scopes as if MCP existed then. Add dated current-state notes linking Feature 013 and ADR-010 wherever Feature 006/007/010/011 says MCP is unsupported or Transaction only has a filesystem target.

- [ ] **Step 3: Update BUILD usage examples**

Add MCP search/list/install/uninstall examples, describe stdio/http Cloud definitions, three-Agent support, user-scope Claude behavior, fail-closed CLI checks, no MCP Cache/Staging, and test-environment isolation. Keep Plugin explicitly unsupported.

- [ ] **Step 4: Run formatting and focused tests before Changelog**

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./internal/domain ./internal/cloud ./internal/adapter ./internal/transaction ./internal/lifecycle ./internal/cli ./cmd/apm
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 5: Run full verification**

```bash
go test -count=1 ./...
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: every command exits 0 with no real Agent CLI invocation.

- [ ] **Step 6: Audit Feature 013 requirement-by-requirement**

Create `verification.md` with columns `Spec requirement`, `Evidence`, and `Status`. Use every `spec.md` heading and each table row as the checklist. For every scope item, domain/Cloud rule, Agent argv/parser rule, state-table row, journal phase, cross-type recovery case, error boundary, and environment-isolation condition, record the exact proving test name or source/document path and mark only `proved` or `missing`. Treat every `missing` row as unfinished work and add the missing test or implementation; final verification requires zero `missing` rows.

- [ ] **Step 7: Add one Changelog entry after verification passes**

Use `/home/liangff/.codex/docs/Changelog.md` exactly. Add a top `## 2026-08-12` entry classified `新增功能`, describe this single user request, list every related implementation commit including the design/plan commits, summarize type-specific adapters/shared journal/Cloud direct definitions, and paste the exact successful verification commands plus remaining limitations.

- [ ] **Step 8: Mark plan checkboxes from actual evidence and commit docs**

Only check steps whose commands/actions actually completed. Then commit:

```bash
git add BUILD.md docs/Changelog.md ivory-tower
git commit -m "docs(mcp): document Agent CLI lifecycle"
```

- [ ] **Step 9: Re-run post-commit smoke verification**

Run:

```bash
git status --short
go test -count=1 ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

Expected: clean worktree and all commands exit 0.

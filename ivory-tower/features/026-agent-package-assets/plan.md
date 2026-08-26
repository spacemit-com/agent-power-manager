# Agent Package and Assets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement V0.5 Agent directories, deterministic archive submission, PNG avatar validation, directory-based Cloud/cache/install flows, and matching APM documentation.

**Architecture:** Keep Agent-specific Cloud, Cache, and Adapter ports, but replace their single-file payload with an immutable directory artifact. A new `internal/agentartifact` package owns package/root/avatar rules; existing hardened snapshots and archive extraction enforce physical safety and invoke that validator before cache or target publication.

**Tech Stack:** Go 1.24, standard `archive/tar`, `compress/gzip`, `image/png`, YAML v3, existing filesystem journal, table-driven Go tests.

**Spec:** `ivory-tower/features/026-agent-package-assets/spec.md`

## Global Constraints

- External identity is `agent.name` plus `agent.version`; `agent.id` is required but Bibit-internal.
- Required fields are integer `schema_version: 1` and non-empty strings `agent.id/name/version/description/soul`.
- Optional `metadata.avatar` must reference a real PNG below `assets/`, size `1..1 MiB`, square, width/height `128..1024`.
- Agent root contains only `agent.yaml` and optional `assets/`; links, special files, escaping paths, executable modes, and nested archives are forbidden.
- Limits are 20 MiB compressed, 10 MiB/file, 100 MiB extracted, 1000 nodes, depth 16, path 255 bytes, and 100x expansion.
- Agent, Skill, and Plugin use `application/gzip`; MD5 describes exact archive bytes.
- Cache is `cache/agents/<name>/<version>`; active Bibit target is `agents/<name>` with no version directory.
- Feature 025 outcomes, one-warning rule, same-version no-probe path, and sole public `adapter.ErrTargetConflict` do not change.
- No raw-YAML, single-file-source, or dual-format compatibility branch is allowed.
- Important new interfaces require concise normative Chinese comments.

---

### Task 1: Extend the Agent V0.5 Domain Model

**Files:**
- Modify: `internal/domain/agent_config.go`
- Modify: `internal/domain/agent_config_test.go`

**Interfaces:**
- Consumes: Existing `DecodeAgentConfigYAML`, `AgentConfig.Validate`, and `ValidatePowerRef` callers.
- Produces: `AgentInfo{Name, Soul, Metadata}` and identity validation against `agent.name`/`agent.version`.

- [ ] **Step 1: Write failing V0.5 parsing and identity tests**

Use this valid baseline and table-drive each missing, blank, and wrong-typed required field:

```go
valid := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{
    ID: "bibit-internal-42", Name: "demo-agent", Version: "v1.0.0",
    Description: "demo", Soul: "help safely",
}}
```

Test noncanonical name/version, unknown optional fields, optional `metadata.avatar`, and prove identity uses Name:

```go
config := mustDecodeAgent(t, validAgentYAML("bibit-internal-42", "demo-agent", "v1.2.3"))
if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "demo-agent", Version: "v1.2.3"}); err != nil {
    t.Fatal(err)
}
if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "bibit-internal-42", Version: "v1.2.3"}); !errors.Is(err, ErrInvalidAgent) {
    t.Fatalf("error = %v", err)
}
```

- [ ] **Step 2: Run the focused tests and observe failure**

Run: `go test ./internal/domain -run 'Test(AgentConfig|DecodeAgent)' -count=1`

Expected: FAIL because Name, Soul, and Metadata are absent and identity still uses ID.

- [ ] **Step 3: Implement the minimum V0.5 model**

```go
type AgentInfo struct {
    ID          string        `yaml:"id"`
    Name        string        `yaml:"name"`
    Version     string        `yaml:"version"`
    Description string        `yaml:"description"`
    Soul        string        `yaml:"soul"`
    Metadata    AgentMetadata `yaml:"metadata"`
}

type AgentMetadata struct {
    Avatar *string `yaml:"avatar"`
}
```

Extend the YAML-node decoder; do not switch to permissive struct unmarshal. Preserve duplicate/multi-document checks, require correct scalar types, validate canonical Name/Version, require ID/Description/Soul only to be nonblank, and compare PowerRef to Name/Version.

- [ ] **Step 4: Run domain tests**

Run: `go test ./internal/domain -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/domain/agent_config.go internal/domain/agent_config_test.go
git commit -m "feat(agent): parse v0.5 resource identity"
```

### Task 2: Add the Authoritative Agent Artifact Validator

**Files:**
- Create: `internal/agentartifact/artifact.go`
- Create: `internal/agentartifact/png.go`
- Create: `internal/agentartifact/filesystem.go`
- Create: `internal/agentartifact/artifact_test.go`
- Create: `internal/agentartifact/filesystem_test.go`

**Interfaces:**
- Consumes: `domain.DecodeAgentConfigYAML`, `AgentConfig.ValidatePowerRef`, `pluginartifact.ValidatePhysicalTree`.
- Produces: `Entry`, `Snapshot`, `Validate`, and `ValidateDirectory` for all content boundaries.

- [ ] **Step 1: Write failing logical-package tests**

Test these intended interfaces:

```go
type Entry struct {
    Path      string
    Directory bool
    Mode      fs.FileMode
    Data      []byte
}
type Snapshot struct {
    Config      domain.AgentConfig
    Entries     []Entry
    FileCount   int
    ContentSize int64
}
func Validate(entries []Entry, ref *domain.PowerRef) (Snapshot, error)
func ValidateDirectory(ctx context.Context, root string, ref *domain.PowerRef) (Snapshot, error)
```

Generate PNGs using `image.NewNRGBA` and `png.Encode`. Cover minimal package, avatar at 128/1024, non-square, 127/1025, empty/fake/over-1-MiB PNG, path outside assets, missing target, extra root file, executable entry, archive extension/signature, duplicate normalized path, and PowerRef mismatch.

- [ ] **Step 2: Run tests and observe missing package failure**

Run: `go test ./internal/agentartifact -count=1`

Expected: FAIL because the package does not exist.

- [ ] **Step 3: Implement logical validation**

Normalize `/` paths, defensively copy returned bytes, enforce the root closed set, reject duplicates/ambiguity/executable files, locate `agent.yaml`, decode it, and optionally compare ref. Reject `.zip`, `.tar`, `.tgz`, `.tar.gz`, `.gz`, `.bz2`, `.xz`, `.7z` and known archive signatures. Join semantic failures with `domain.ErrInvalidAgent`.

- [ ] **Step 4: Implement PNG validation**

```go
const (
    maxAvatarBytes = 1 << 20
    minAvatarSide  = 128
    maxAvatarSide  = 1024
)
```

Require canonical `assets/...png`, a regular nonempty entry, PNG signature, `image.DecodeConfig` format `png`, square dimensions, and inclusive bounds. Do not decode all pixels or validate DPI/color/alpha.

- [ ] **Step 5: Implement physical-directory adaptation**

Call `pluginartifact.ValidatePhysicalTree(root)`, walk the physical directory with context cancellation, reject non-directory roots/special files/hard links, read regular files, enforce 10 MiB/file, 100 MiB total, 1000 nodes, depth 16, and 255-byte path, then call `Validate`.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/agentartifact -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agentartifact
git commit -m "feat(agent): validate packaged assets"
```

### Task 3: Package Agent Submission Directories

**Files:**
- Modify: `internal/submission/agent.go`
- Modify: `internal/submission/validate.go`
- Modify: `internal/submission/archive.go`
- Modify: `internal/submission/agent_test.go`
- Modify: `internal/submission/prepare_test.go`
- Modify: `internal/submission/source_snapshot_test.go`
- Modify: `internal/cloud/remote/submission.go`
- Modify: `internal/cloud/remote/submission_test.go`

**Interfaces:**
- Consumes: `agentartifact.Validate(entries, nil)` and deterministic tar.gz builder.
- Produces: Agent gzip `Content`, metadata from `agent.name`, and multipart filename `<name>-<version>.tar.gz`.

- [ ] **Step 1: Write failing directory/archive tests**

Create a temp root with `agent.yaml` and generated `assets/avatar.png`, then assert:

```go
got, err := Prepare(ctx, Request{Type: domain.PowerTypeAgent, Path: root, Message: "first"})
if err != nil { t.Fatal(err) }
if got.Name != "demo-agent" || got.ContentType != "application/gzip" { t.Fatalf("got %+v", got) }
assertTarGzFiles(t, got.Bytes, []string{"agent.yaml", "assets/", "assets/avatar.png"})
```

Reject a single YAML path, prebuilt gzip, symlink, mutation after Load, extra root entry, over-limit and over-ratio output. Assert unchanged directories generate identical bytes and MD5.

- [ ] **Step 2: Run tests and observe old behavior**

Run: `go test ./internal/submission -run 'Agent|Prepare|Snapshot' -count=1`

Expected: FAIL because Agent still accepts one file and returns YAML.

- [ ] **Step 3: Make source capture policy-aware**

```go
type sourceContentPolicy func(path string, body []byte) error
func captureSourceTreeWithPolicy(ctx context.Context, root string, policy sourceContentPolicy) (sourceTreeSnapshot, error)
```

Keep the existing default text policy for Skill/Plugin. Agent requires safe text for `agent.yaml`, permits binary below `assets/`, converts the frozen snapshot to `[]agentartifact.Entry`, and never rereads during Build.

- [ ] **Step 4: Build deterministic Agent archives**

Make `agentSourceLoader.Load` directory-only, validate the frozen entries, derive Name/Version/Description from config, set `application/gzip`, and build the same snapshot. Normalize Agent file modes to `0644` and directories to `0755`.

- [ ] **Step 5: Add multipart filenames and align content types**

```go
func submissionFilename(request domain.SubmissionRequest) string {
    switch request.Type {
    case domain.PowerTypeMCP:
        return request.Name + "-" + request.Version + ".json"
    case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeAgent:
        return request.Name + "-" + request.Version + ".tar.gz"
    default:
        return ""
    }
}
```

Change Agent content type to gzip. Keep metadata filename-free; write content with a safely escaped Content-Disposition filename. Test all four types.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/submission ./internal/cloud/remote -run 'Agent|Submission|Multipart|Prepare|Snapshot' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/submission internal/cloud/remote/submission.go internal/cloud/remote/submission_test.go
git commit -m "feat(submission): upload packaged agent assets"
```

### Task 4: Migrate Local and Remote Cloud Agent Content

**Files:**
- Modify: `internal/cloud/local/agent.go`
- Modify: `internal/cloud/local/catalog.go`
- Modify: `internal/cloud/local/source.go`
- Modify platform helpers as required: `internal/cloud/local/source_*.go`
- Modify: `internal/cloud/local/agent_test.go`
- Modify: `internal/cloud/local/catalog_test.go`
- Modify: `internal/cloud/local/safety_limits_linux_test.go`
- Modify: `internal/cloud/remote/agent.go`
- Modify: `internal/cloud/remote/archive.go`
- Modify: `internal/cloud/remote/agent_test.go`
- Modify: `internal/cloud/remote/archive_test.go`
- Modify: `internal/cloud/contract_test.go`
- Replace fixture: `internal/cloud/resources/agents/bianbu-agent-default/v1.0.0/agent.yaml`
- Create fixture: `internal/cloud/resources/agents/bianbu-agent-default/v1.0.0/assets/avatar.png`

**Interfaces:**
- Consumes: artifact validator and gzip Catalog entries.
- Produces: `DownloadAgent(ctx, entry, destinationDirectory)` with Local/Remote parity and no invalid visible destination.

- [ ] **Step 1: Write failing parity and safety tests**

Call both providers with directory destinations and compare normalized `agent.yaml` plus PNG. Cover binary non-avatar asset, invalid avatar, extra root file, wrong Content-Type, MD5 mismatch, unsafe tar path, and semantic failure leaving destination absent.

- [ ] **Step 2: Run focused tests and observe failure**

Run: `go test ./internal/cloud/local ./internal/cloud/remote ./internal/cloud -run 'Agent|Archive|Contract' -count=1`

Expected: FAIL because providers still publish one YAML file.

- [ ] **Step 3: Make Local snapshot policy-aware**

```go
type localTreeContentPolicy func(relative string, body []byte) error
func capturePhysicalTreeWithPolicy(ctx context.Context, boundary, source string, policy localTreeContentPolicy, hooks ...) (*treeSnapshot, error)
```

Preserve default Skill/Plugin behavior. Agent permits binary assets but requires text YAML. Convert `treeSnapshot.nodes` to artifact Entries and validate before catalog encoding/copy publication.

- [ ] **Step 4: Change Local Agent catalog and download**

Make `loadCatalogAgent` return description, deterministic archive bytes, and captured tree; set `application/gzip`; use the version directory as fixture source; atomically copy the tree to the caller directory.

- [ ] **Step 5: Make Remote extraction policy-aware**

```go
type archivePolicy struct {
    validateFile func(path string, body []byte) error
    validateTree func(root string) error
}
```

Skill/Plugin keep all-text behavior. Agent requires text YAML, permits binary assets, and invokes `agentartifact.ValidateDirectory(ctx, root, &entry.Ref)` while extraction output is still private. Any error cleans private output and leaves destination absent.

- [ ] **Step 6: Replace Remote DownloadAgent**

```go
func (c *Client) DownloadAgent(ctx context.Context, entry domain.CatalogEntry, destination string) error {
    if entry.Ref.Type != domain.PowerTypeAgent || !validResolveQuery(entry.Ref) || entry.Ref.Version == "" {
        return fmt.Errorf("download invalid remote Agent identity: %w", cloud.ErrNotFound)
    }
    return c.downloadArchivePower(ctx, entry, destination, agentArchivePolicy(ctx, entry.Ref), "agent.yaml")
}
```

Remove single-file helpers after all callers/tests migrate.

- [ ] **Step 7: Run Cloud tests**

Run: `go test ./internal/cloud/... -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/cloud
git commit -m "feat(cloud): download agent archives as directories"
```

### Task 5: Store and Stage Complete Agent Directories

**Files:**
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/agent_test.go`
- Modify: `internal/cache/prepare_test.go`
- Modify: `internal/cache/filesystem_internal_test.go`
- Modify Agent recording helpers in: `internal/cache/*_test.go`

**Interfaces:**
- Consumes: directory `DownloadAgent` and `agentartifact.ValidateDirectory`.
- Produces: immutable Agent version directory and `staging/agents/<name>`.

- [ ] **Step 1: Write failing complete-directory tests**

Make recording Source create YAML and PNG below destination. Assert exact cache/staging trees, cache-hit no-download, and fail-closed behavior for wrong Name, missing/corrupt avatar, extra root file, symlink asset, and source error. Damaged cache must remain and cause zero downloads.

- [ ] **Step 2: Run tests and observe failure**

Run: `go test ./internal/cache -run 'Agent|Prepare' -count=1`

Expected: FAIL because the destination is `<temp>/agent.yaml` and hit validation is YAML-only.

- [ ] **Step 3: Implement directory cache miss**

Pass the temporary version directory directly to Source; validate the whole directory before atomically renaming to `cache/agents/<name>/<version>`.

- [ ] **Step 4: Implement complete cache hit and staging validation**

Replace `cachedAgent` with the artifact validator. Treat absent root as miss, but any present malformed tree as a preserved error. Copy the whole cache directory to staging and validate it before staging publication.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/cache -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cache
git commit -m "feat(cache): cache complete agent directories"
```

### Task 6: Publish and Recover Complete Bibit Agent Directories

**Files:**
- Modify: `internal/adapter/agent.go`
- Modify: `internal/adapter/bibit_agent.go`
- Modify: `internal/adapter/bibit_agent_test.go`
- Modify: `internal/transaction/agent_uninstall.go`
- Modify: `internal/transaction/agent_test.go`
- Modify: `internal/transaction/agent_recovery_test.go`
- Modify: `internal/transaction/filesystem_recovery.go` if file-only assumptions remain.

**Interfaces:**
- Consumes: `staging/agents/<name>` and artifact directory validation.
- Produces: TargetPath `<bibit>/agents/<name>` and unchanged schema-v1 filesystem recovery.

- [ ] **Step 1: Write failing Adapter tests**

Assert directory TargetPath, YAML+PNG install, normalized modes, identity by Name, invalid avatar publishes nothing, and existing target returns `ErrTargetConflict`.

- [ ] **Step 2: Run tests and observe failure**

Run: `go test ./internal/adapter -run 'BibitAgent|AgentAdapter' -count=1`

Expected: FAIL because TargetPath ends in YAML and Install writes one file.

- [ ] **Step 3: Implement directory publication**

Update normative Chinese interface comments. Validate staging, create a private directory under the final parent, copy the complete tree with `0644/0755`, revalidate the copy, and rename no-replace to `<root>/agents/<ref.Name>`. Clean private/staging paths on pre-publication failures.

- [ ] **Step 4: Write failing transaction/recovery tests**

Cover first install, replacement backup, uninstall quarantine, rollback, commit recovery, corrupt target/backup/quarantine, and missing/incomplete target. Assert journal schema remains 1 and driver remains filesystem.

- [ ] **Step 5: Adjust cleanup and recovery**

Keep rename ordering. After uninstall, prune only the now-empty `<bibit>/agents` parent; never attempt to remove the Bibit root. Validate complete directories before restoring or committing target/backup/quarantine.

- [ ] **Step 6: Run suites**

Run: `go test ./internal/adapter ./internal/transaction -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter internal/transaction
git commit -m "feat(agent): publish complete bibit agent directories"
```

### Task 7: Preserve Lifecycle State Semantics

**Files:**
- Modify: `internal/lifecycle/agent_test.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `internal/lifecycle/uninstall_agent_test.go`
- Modify: `internal/lifecycle/lifecycle_state_contract_test.go`
- Modify Agent fixture helpers in: `internal/lifecycle/*_test.go`
- Modify if affected: `internal/cli/submission_test.go`, `internal/cli/validate_test.go`

**Interfaces:**
- Consumes: directory Cache/Adapter and Feature 025 policy.
- Produces: unchanged Agent outcomes, warning counts, and public errors.

- [ ] **Step 1: Convert fixtures to V0.5 directories**

Every YAML fixture includes:

```yaml
schema_version: 1
agent:
  id: bibit-internal-demo
  name: demo-agent
  version: v1.0.0
  description: demo
  soul: help safely
```

Generate PNGs in tests when avatar is present.

- [ ] **Step 2: Add directory-specific state rows**

Cover missing YAML, missing referenced avatar, and extra/inconsistent assets. Assert:

```text
same-version + any target -> unchanged, no probe warning
different-version + missing/incomplete owned target -> one warning, installed
no manifest + existing directory -> failed, ErrTargetConflict
uninstall manifest + missing/incomplete target -> one warning, completed
```

Identity conflicts remain fail-closed, not missing.

- [ ] **Step 3: Run lifecycle and CLI tests**

Run: `go test ./internal/lifecycle ./internal/cli -count=1`

Expected: PASS with existing Feature 025 marker counts.

- [ ] **Step 4: Run all Go tests for stale fixtures**

Run: `go test ./... -count=1`

Expected: PASS. Failures mentioning missing Name/Soul, YAML media type, or destination `agent.yaml` identify fixtures to migrate here.

- [ ] **Step 5: Commit**

```bash
git add internal/lifecycle internal/cli
git commit -m "test(agent): enforce packaged lifecycle contract"
```

### Task 8: Reconcile Documentation, Template, and Changelog

**Files:**
- Modify: `docs/power-template/agent.md`
- Modify: `docs/power-template/README.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/c4/containers/apm.dsl`
- Modify: `ivory-tower/features/022-agent-submission/spec.md`
- Modify: `ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md`
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: Tasks 1–7, ADR-023, APC ADR-006/API contract.
- Produces: user V0.5 template, current docs, history notices, one changelog entry.

- [ ] **Step 1: Replace the Agent template from V0.5**

Use `/home/liangff/Downloads/agent_yaml_配置模板_V0.5.yaml` as field source. In `agent.md`, document a directory, use canonical sample `bibit-edge-assistant`, mark five required fields, make avatar optional PNG-only, remove emoji/JPG/WebP wording, and state 1 MiB/square/128–1024. Retain model/tools/skills/mcps/hooks/permissions/resource/network/data-protection examples without claiming APM validates them.

- [ ] **Step 2: Update submission format docs**

```markdown
| Agent | 确定性 `tar.gz` | 根含 `agent.yaml` 与可选 `assets/`；MD5 针对归档原始字节 |
```

Separate Agent binary assets from default text-only rules and list the approved validation limits.

- [ ] **Step 3: Update architecture and C4**

Replace current single-file statements with directory cache/staging/target and gzip. Describe `internal/agentartifact` and physical snapshot boundaries. Reference ADR-023 and APC ADR-006.

- [ ] **Step 4: Add supersession notices**

At Feature 022 and ADR-019 headers, state that Feature 026/ADR-023 replaces raw YAML, ID-as-name, and single-file cache/target while other governance principles remain.

- [ ] **Step 5: Add one Changelog entry**

Run: `sed -n '1,240p' ~/.codex/docs/Changelog.md`

Then add one reverse-chronological entry listing all implementation commits and V0.5, PNG, archive, API, directory lifecycle, migration, and verification results.

- [ ] **Step 6: Scan stale wording**

```bash
rg -n 'Agent.*application/yaml|Agent.*单个原始|agents/<agent\.id>/agent\.yaml|Agent.*原始字节透传' BUILD.md docs/power-template ivory-tower/project-overview.md ivory-tower/c4
rg -n 'application/gzip|agent\.name|assets/avatar\.png|128.*1024|1 MiB' BUILD.md docs/power-template ivory-tower/project-overview.md ivory-tower/c4
git diff --check
```

Expected: no stale current-state match; new contract appears in all document areas.

- [ ] **Step 7: Run final verification**

```bash
gofmt -w internal/agentartifact/*.go internal/domain/agent_config.go internal/domain/agent_config_test.go
gofmt -w internal/submission/*.go internal/cloud/local/*.go internal/cloud/remote/*.go internal/cloud/*.go
gofmt -w internal/cache/*.go internal/adapter/*.go internal/transaction/*.go internal/lifecycle/*.go internal/cli/*.go
go test ./... -count=1
go vet ./...
git diff --check
git status --short
```

Expected: tests PASS, vet and diff checks exit 0, status contains only intended Task 8 docs.

- [ ] **Step 8: Commit docs and changelog**

```bash
git add BUILD.md docs/power-template docs/Changelog.md ivory-tower/project-overview.md \
  ivory-tower/c4/components/apm-cli.dsl ivory-tower/c4/containers/apm.dsl \
  ivory-tower/features/022-agent-submission/spec.md \
  ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md
git commit -m "docs(agent): publish packaged resource contract"
```

- [ ] **Step 9: Verify clean completion evidence**

```bash
go test ./... -count=1
go vet ./...
git status --short
git log -10 --oneline
```

Expected: tests/vet pass, worktree is clean, and task commits plus design commit are visible.

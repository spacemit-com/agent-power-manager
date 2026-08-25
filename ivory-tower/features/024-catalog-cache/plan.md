# Versioned Resource Catalog Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a content-addressed, persistent APC resource catalog cache that powers APM list/search/install, removes the public paginated metadata API, and keeps uninstall/fix entirely local.

**Architecture:** `internal/catalog` owns strict decoding, immutable queries, secure persistence, synchronization, locking, and stale fallback. Local and Remote Cloud providers expose only catalog version/full-catalog transport plus exact content; lifecycle receives one immutable snapshot and passes its exact `CatalogEntry` through cache/resource download boundaries. APC publishes one deterministic four-group JSON catalog and a small SHA-256 version document.

**Tech Stack:** Go 1.24, standard-library JSON/HTTP/crypto/fs packages, existing `jsonstrict`, `semver`, `lock`, secure descriptor-relative filesystem helpers, Mermaid, Structurizr DSL, Markdown.

**Spec:** `ivory-tower/features/024-catalog-cache/spec.md`

## Global Constraints

- The only persistent catalog path is `~/.apm/catalog.json`; no source/provider namespace and no sidecar marker.
- The local marker is SHA-256 over the exact catalog bytes; switching provider/origin/root does not invalidate identical bytes.
- Catalog JSON has top-level schema version 1 and required `skills`, `plugins`, `mcps`, and `agents` groups, each with schema version 1 and non-null `items`.
- Catalog JSON is strict: duplicate keys, unknown fields, missing groups, null arrays, invalid identities/metadata, wrong order, duplicate refs, and unsupported schema versions fail closed.
- Items are sorted by canonical name ascending and SemVer descending; tags are trimmed, sorted, and unique.
- Catalog size is bounded at 100 MiB, including exact-boundary acceptance and one-byte-over rejection.
- Repository publication is mode `0600`, atomic, and resistant to symlink, hardlink, terminal replacement, and competing-target attacks; failures preserve the previous valid catalog and remove only owned temporary files.
- `Snapshot` is immutable and performs no I/O; it returns cloned entries/slices.
- `list`, `search`, and cloud-resolving `install` check version at most once per command; `list --installed`, every `uninstall`, `fix`, and exact Plugin unchanged early return make zero catalog calls.
- `Snapshot` acquires the existing non-blocking `apm.lock`; lock contention returns `lock.ErrLocked` and never falls back. `SnapshotLocked` requires the caller already hold that lock and must not re-acquire it.
- Provider/version/download/protocol failure may use one strictly valid stale catalog with one stderr warning; missing/invalid cache and caller cancellation fail.
- Remote version/catalog requests are anonymous and never carry the Submission API key.
- Catalog is the sole metadata authority for install; exact content methods accept `domain.CatalogEntry`, and obsolete public list/cursor/series/latest/exact-metadata calls and types are removed.
- MCP uninstall takes only name/ref plus Agent CLI state. Its uninstall journal stores no Server definition and recovery always converges to absent; MCP install/version-switch journals retain definitions for rollback.
- Existing transaction recovery ordering, manifest-first rules, output ordering, JSON stdout purity, and Submission behavior remain unchanged.
- Important exported or cross-module interfaces receive concise Chinese comments describing purpose and constraints.
- Every production behavior change follows RED, verified failure, minimal GREEN, refactor, and focused/full verification.
- APC remains documentation/design-only in this request; no server/database/object-store implementation is added.
- Add exactly one Feature 024 entry to `docs/Changelog.md` after all behavior tasks and final verification, listing every related APM behavior commit and the APC documentation commit.

---

### Task 1: Catalog Domain, Strict Codec, and Immutable Snapshot

**Files:**
- Create: `internal/domain/catalog.go`
- Create: `internal/domain/catalog_test.go`
- Create: `internal/catalog/errors.go`
- Create: `internal/catalog/codec.go`
- Create: `internal/catalog/codec_test.go`
- Create: `internal/catalog/snapshot.go`
- Create: `internal/catalog/snapshot_test.go`

**Interfaces:**
- Consumes: `domain.PowerRef`, `domain.PowerType`, canonical power-name validation, and the existing SemVer selector semantics.
- Produces:

```go
package domain

type CatalogVersion struct {
	SHA256 string
	Size   int64
}

type CatalogEntry struct {
	Ref          PowerRef
	Description  string
	Message      string
	ContentType  string
	ContentSize  int64
	ContentMD5   string
	PublishedAt  string
	Tags         []string
}

func (v CatalogVersion) Validate() error
func (e CatalogEntry) Clone() CatalogEntry
func (e CatalogEntry) Validate() error
```

```go
package catalog

const MaxCatalogSize int64 = 100 << 20

type Snapshot struct { /* unexported immutable index */ }

func Decode(data []byte) (Snapshot, error)
func Encode(entries []domain.CatalogEntry) ([]byte, error)
func (s Snapshot) List(powerType domain.PowerType) []domain.CatalogEntry
func (s Snapshot) Resolve(ref domain.PowerRef) (domain.CatalogEntry, error)
func (s Snapshot) Metadata(ref domain.PowerRef) (domain.CatalogEntry, error)
```

- [ ] **Step 1: Write domain validation tests**

Add table tests proving `CatalogVersion.Validate` accepts only 64 lowercase hex SHA-256 plus non-negative size, and `CatalogEntry.Validate` requires an exact canonical ref/version, trimmed non-empty description/message/content type, positive size, lowercase 32-hex MD5, RFC3339 timestamp, and sorted unique trimmed tags. Add a mutation test proving `Clone` does not share `Tags`.

```go
got := original.Clone()
got.Tags[0] = "changed"
if original.Tags[0] == "changed" {
	t.Fatal("CatalogEntry.Clone shares Tags")
}
```

- [ ] **Step 2: Run domain RED**

Run: `go test -count=1 ./internal/domain -run 'TestCatalog'`

Expected: FAIL because catalog domain types and methods do not exist.

- [ ] **Step 3: Implement domain values minimally**

Implement the exact API above, reuse canonical name/version helpers, use `time.Parse(time.RFC3339, ...)`, and return contextual errors without logging.

- [ ] **Step 4: Run domain GREEN**

Run: `go test -count=1 ./internal/domain -run 'TestCatalog'`

Expected: PASS.

- [ ] **Step 5: Write strict codec and snapshot tests**

Use one valid fixture with all four groups, then table-mutate it to cover: top/group schema mismatch; missing/unknown/duplicate fields; missing group; `items:null`; duplicate ref; bad item order; bad tag order; invalid metadata; and 100 MiB + 1. Assert `Encode` emits all groups with `[]`, is byte-deterministic under shuffled input, and orders name ascending/version descending. Assert `List`, `Resolve`, and `Metadata` return clones and that `Resolve` handles exact version, highest SemVer, type filtering, not-found, and cross-type ambiguity.

```go
first, err := Encode(shuffled)
if err != nil { t.Fatal(err) }
second, err := Encode(reversed)
if err != nil { t.Fatal(err) }
if !bytes.Equal(first, second) {
	t.Fatalf("deterministic bytes differ")
}
```

- [ ] **Step 6: Run codec/snapshot RED**

Run: `go test -count=1 ./internal/catalog`

Expected: FAIL because `Decode`, `Encode`, and `Snapshot` do not exist.

- [ ] **Step 7: Implement codec and snapshot minimally**

Define private JSON DTOs with required four groups, decode through `jsonstrict.Decode(..., true)`, reject trailing values, validate canonical order rather than silently sorting downloaded input, and build private indexes. `Encode` validates entries, clones/sorts them, groups by type, and marshals one deterministic compact JSON value.

- [ ] **Step 8: Verify and commit**

Run:

```bash
gofmt -w internal/domain/catalog.go internal/domain/catalog_test.go internal/catalog
go test -count=1 ./internal/domain ./internal/catalog
go test -race -count=1 ./internal/domain ./internal/catalog
git diff --check
git add internal/domain/catalog.go internal/domain/catalog_test.go internal/catalog
git commit -m "feat(catalog): add strict immutable snapshots"
```

Expected: all commands exit 0.

---

### Task 2: Secure Persistent Catalog Repository

**Files:**
- Modify: `internal/paths/paths.go`
- Modify: `internal/paths/paths_test.go`
- Create: `internal/catalog/repository.go`
- Create: `internal/catalog/repository_test.go`
- Create: `internal/catalog/repository_unix.go`
- Create: `internal/catalog/repository_other.go`
- Create: `internal/catalog/repository_unix_test.go`

**Interfaces:**
- Consumes: `catalog.Decode`, `catalog.MaxCatalogSize`, existing trusted-directory and descriptor-relative publication patterns.
- Produces:

```go
func paths.CatalogPath() (string, error)

type Repository interface {
	Load() (Snapshot, domain.CatalogVersion, error)
	Replace(expected domain.CatalogVersion, sourcePath string) (Snapshot, error)
}

func NewRepository(path string) (Repository, error)
```

- [ ] **Step 1: Write path and repository RED tests**

Prove `CatalogPath()` is exactly `$APM_HOME/.apm/catalog.json`. Repository tests must cover absent file, valid mode-0600 load/hash, wrong mode, directory, symlink, hardlink, size boundary, invalid JSON/schema, changed file identity while reading, and immutable returned snapshot.

- [ ] **Step 2: Run repository RED**

Run: `go test -count=1 ./internal/paths ./internal/catalog -run 'Test(CatalogPath|Repository)'`

Expected: FAIL because path/repository APIs do not exist.

- [ ] **Step 3: Implement secure load**

Use the repository path supplied at construction; validate it is clean and absolute. Open the parent through platform helpers, reject terminal symlinks/non-regular files/link-count other than one, require mode `0600`, read at most `MaxCatalogSize+1`, compare pre/post identity and size, calculate SHA-256 over the same bytes, and decode those bytes.

- [ ] **Step 4: Add atomic replacement attack tests**

Cover expected hash/size mismatch, invalid staged bytes, staged symlink/hardlink, destination substitution, competing publication, injected fsync/rename failures, prior valid-file preservation, exact temp cleanup, and output mode. Test successful replacement changes both snapshot and computed local marker.

- [ ] **Step 5: Run atomic replacement RED**

Run: `go test -count=1 ./internal/catalog -run 'TestRepositoryReplace'`

Expected: FAIL because `Replace` is missing or does not enforce secure atomic publication.

- [ ] **Step 6: Implement atomic replacement**

Validate the provider-owned staged file and expected version before copying into an owned mode-0600 sibling. Re-check copied bytes, fsync file and parent, publish atomically over the existing catalog without exposing partial bytes, and remove only identity-matched owned staging on failure. Reuse existing platform patterns; unsupported platforms fail closed rather than following links.

- [ ] **Step 7: Verify platforms and commit**

Run:

```bash
gofmt -w internal/paths internal/catalog
go test -count=1 ./internal/paths ./internal/catalog
go test -race -count=1 ./internal/catalog
GOOS=linux GOARCH=amd64 go test -c ./internal/catalog -o /tmp/apm-catalog-linux.test
GOOS=darwin GOARCH=amd64 go test -c ./internal/catalog -o /tmp/apm-catalog-darwin.test
GOOS=freebsd GOARCH=amd64 go test -c ./internal/catalog -o /tmp/apm-catalog-freebsd.test
GOOS=windows GOARCH=amd64 go test -c ./internal/catalog -o /tmp/apm-catalog-windows.test.exe
rm -f /tmp/apm-catalog-linux.test /tmp/apm-catalog-darwin.test /tmp/apm-catalog-freebsd.test /tmp/apm-catalog-windows.test.exe
git diff --check
git add internal/paths internal/catalog
git commit -m "feat(catalog): persist snapshots securely"
```

Expected: all commands exit 0 and test binaries are removed.

---

### Task 3: Catalog Synchronization Manager and Stale State Machine

**Files:**
- Create: `internal/catalog/manager.go`
- Create: `internal/catalog/manager_test.go`
- Modify: `internal/catalog/errors.go`
- Modify: `internal/cloud/client.go`

**Interfaces:**
- Consumes: Task 2 `Repository`, existing `lock.Locker`, and provider-created private destination semantics.
- Produces:

```go
package cloud

type CatalogSource interface {
	CatalogVersion(context.Context) (domain.CatalogVersion, error)
	DownloadCatalog(context.Context, string) error
}
```

```go
package catalog

type SyncStatus struct {
	Updated bool
	Stale   bool
	Cause   error
}

type Manager interface {
	Snapshot(context.Context) (Snapshot, SyncStatus, error)
	SnapshotLocked(context.Context) (Snapshot, SyncStatus, error)
}

func NewManager(source cloud.CatalogSource, repository Repository, locker lock.Locker, tempRoot string) (Manager, error)
```

- [ ] **Step 1: Write manager state-table tests**

Use fakes that count calls and record order. Cover: no cache first sync; matching hash skips download; differing hash downloads/replaces; damaged cache repaired online; version/download/hash/schema failures with valid stale cache; same failures without valid cache; caller pre-cancel and mid-download cancel; source switch with equal hash; one download per call; and temp cleanup.

```go
snapshot, status, err := manager.SnapshotLocked(ctx)
if err != nil || status.Updated || status.Stale || source.downloadCalls != 0 {
	t.Fatalf("unchanged sync = %+v, %+v, %v, downloads=%d", snapshot, status, err, source.downloadCalls)
}
```

- [ ] **Step 2: Run manager RED**

Run: `go test -count=1 ./internal/catalog -run 'TestManager'`

Expected: FAIL because the manager and Cloud source boundary do not exist.

- [ ] **Step 3: Implement shared synchronization state machine**

`Snapshot` non-blockingly acquires/releases the existing lock and calls one private `snapshotLocked`; `SnapshotLocked` calls only that private method. Load local state first, request version exactly once, skip download on matching valid hash, otherwise download to an owned nonexistent temp destination and call `Replace`. Return stale only for provider/update failures with a previously valid snapshot; never for lock/cancellation/local security failure.

- [ ] **Step 4: Add lock and warning-status tests**

Prove `Snapshot` order is `lock → load → version → optional download/replace → unlock`, contention returns `lock.ErrLocked` with zero provider calls, `SnapshotLocked` has zero locker calls, and stale `Cause` preserves the original provider error for one upper-layer warning.

- [ ] **Step 5: Verify and commit**

Run:

```bash
gofmt -w internal/cloud/client.go internal/catalog
go test -count=1 ./internal/catalog ./internal/cloud
go test -race -count=1 ./internal/catalog
git diff --check
git add internal/cloud/client.go internal/catalog
git commit -m "feat(catalog): synchronize cached snapshots"
```

Expected: all commands exit 0.

---

### Task 4: Remote Version and Full Catalog Transport

**Files:**
- Modify: `internal/cloud/remote/catalog.go`
- Modify: `internal/cloud/remote/catalog_test.go`
- Modify: `internal/cloud/remote/content.go`
- Modify: `internal/cloud/remote/content_test.go`

**Interfaces:**
- Consumes: `cloud.CatalogSource`, `domain.CatalogVersion`, and `catalog.MaxCatalogSize`.
- Produces: Remote `CatalogVersion` and `DownloadCatalog`. Existing public discovery/content methods remain temporarily unchanged so this commit keeps the whole repository buildable; Task 7 performs the single atomic interface migration and Task 9 deletes discovery.

- [ ] **Step 1: Replace pagination tests with version/catalog RED tests**

Assert exact anonymous requests to `GET /api/v1/resources/version` and `GET /api/v1/resources`; reject query parameters, redirects across origins, Bearer headers, non-2xx, wrong/missing Content-Type, invalid/duplicate-key version JSON, invalid lowercase hash, negative/oversized size, Content-Length mismatch, short/trailing bytes, and destination pre-existence. Assert the catalog method writes only exact response bytes to a mode-0600 private file and cleans it on failure.

- [ ] **Step 2: Run Remote catalog RED**

Run: `go test -count=1 ./internal/cloud/remote -run 'Test(CatalogVersion|DownloadCatalog)'`

Expected: FAIL because Remote still exposes cursor-based list/metadata behavior.

- [ ] **Step 3: Implement Remote catalog transport**

Use strict JSON for version, `application/json` with legal parameters, `MaxCatalogSize`, exact content length, same-origin redirect rules, no auth injection, and existing private destination helpers. `DownloadCatalog` does not decode schema; repository owns schema validation.

- [ ] **Step 4: Add the private entry verification seam used by Task 7**

Add one unexported helper in `content.go` that verifies Content-Type/Length/actual size/MD5 from a validated `domain.CatalogEntry`. Test it directly from the Remote package without changing the public Skill/Plugin/MCP/Agent method signatures. Preserve existing metadata transport until Task 7.

- [ ] **Step 5: Verify and commit**

Run:

```bash
gofmt -w internal/cloud/remote
go test -count=1 ./internal/cloud/remote
go test -race -count=1 ./internal/cloud/remote
go vet ./internal/cloud/remote
git diff --check
git add -A internal/cloud/remote
git commit -m "feat(cloud): download remote catalog snapshots"
```

Expected: all commands exit 0 and `metadata.go` is absent.

---

### Task 5: Local Deterministic Catalog Provider

**Files:**
- Modify: `internal/cloud/local/client.go`
- Modify: `internal/cloud/local/client_test.go`
- Create: `internal/cloud/local/catalog.go`
- Create: `internal/cloud/local/catalog_test.go`
- Modify: `internal/cloud/contract_test.go`

**Interfaces:**
- Consumes: Task 1 deterministic `catalog.Encode` and existing local trusted resource loaders.
- Produces: Local `cloud.CatalogSource` parity and deterministic metadata bytes. Existing public discovery/content methods remain unchanged until Tasks 7 and 9.

- [ ] **Step 1: Write deterministic Local catalog RED tests**

Generate a fixture tree with every power type and multiple SemVer versions. Assert `CatalogVersion` and `DownloadCatalog` produce identical bytes/hash/size across repeated calls and fixture enumeration order, all four groups are present, descriptions/messages/tags come from authoritative resource files, MCP uses canonical complete-resource JCS bytes, Agent uses raw YAML, and Plugin metadata comes from `apc-plugin.json`.

- [ ] **Step 2: Run Local catalog RED**

Run: `go test -count=1 ./internal/cloud/local -run 'TestLocalCatalog'`

Expected: FAIL because Local has no full-catalog source.

- [ ] **Step 3: Implement one Local inventory builder**

Scan/validate the physical fixture tree once per call into `[]domain.CatalogEntry`, calculate the exact downloadable content metadata with the same source snapshots used by downloads, feed entries through `catalog.Encode`, return hash/size for version, and atomically create the caller-owned destination for download. Do not retain mutable global cache state inside Local.

- [ ] **Step 4: Add Local/Remote catalog-source parity tests**

Extend `internal/cloud/contract_test.go` with version and full-catalog cases shared by Local and Remote: exact bytes, exact hash/size, cancellation, destination pre-existence, and zero Submission credentials. Leave content parity for Task 7.

- [ ] **Step 5: Verify and commit**

Run:

```bash
gofmt -w internal/cloud/local internal/cloud/contract_test.go
go test -count=1 ./internal/cloud/local ./internal/cloud
go test -race -count=1 ./internal/cloud/local ./internal/cloud
git diff --check
git add internal/cloud/local internal/cloud/contract_test.go
git commit -m "feat(cloud): generate local catalog snapshots"
```

Expected: all commands exit 0.

---

### Task 6: List and Search from One Cached Snapshot

**Files:**
- Modify: `internal/lifecycle/dependencies.go`
- Modify: `internal/lifecycle/list.go`
- Modify: `internal/lifecycle/list_test.go`
- Modify: `internal/lifecycle/search.go`
- Modify: `internal/lifecycle/search_test.go`
- Modify: `internal/lifecycle/list_result.go`
- Modify: `internal/lifecycle/list_result_test.go`
- Modify: `internal/lifecycle/search_result.go`
- Modify: `internal/lifecycle/search_result_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `catalog.Manager.Snapshot` and immutable `Snapshot.List`.
- Produces:

```go
type CatalogManager interface {
	Snapshot(context.Context) (catalog.Snapshot, catalog.SyncStatus, error)
	SnapshotLocked(context.Context) (catalog.Snapshot, catalog.SyncStatus, error)
}
```

- [ ] **Step 1: Write list/search lifecycle RED tests**

Assert Cloud list and search call `Snapshot` exactly once, filter by group/type without Cloud calls, preserve result ordering, and render exactly one stale warning through the existing logger/stderr path. Assert warning never appears in JSON result data. Assert `list --installed` makes zero Manager/source/lock calls and still only inspects—not recovers—the journal.

- [ ] **Step 2: Run lifecycle RED**

Run: `go test -count=1 ./internal/lifecycle -run 'Test(List|Search).*Catalog'`

Expected: FAIL because lifecycle still calls `cloud.List`.

- [ ] **Step 3: Implement snapshot-backed list/search**

Replace Cloud dependencies in `ListService` and `SearchService` with `CatalogManager`. Convert cloned entries to existing CLI result values without changing output schema. Centralize stale warning rendering in one unexported helper so each command emits it once.

- [ ] **Step 4: Wire composition and verify lock behavior**

Construct one repository/manager in `cmd/apm/main.go` using `paths.CatalogPath`, `paths.CacheTempPath`, selected Cloud provider, and the existing global locker factory. Add composition tests proving one shared manager instance reaches list/search and manager construction remains lazy for commands that do not need it. Task 7 connects the same instance to install.

- [ ] **Step 5: Verify and commit**

Run:

```bash
gofmt -w internal/lifecycle cmd/apm
go test -count=1 ./internal/lifecycle -run 'Test(List|Search)'
go test -count=1 ./cmd/apm
go test -race -count=1 ./internal/lifecycle ./cmd/apm
git diff --check
git add internal/lifecycle cmd/apm
git commit -m "feat(lifecycle): query cached catalog snapshots"
```

Expected: all commands exit 0.

---

### Task 7: Resolve Install and Download Content from Catalog Entries

**Files:**
- Modify: `internal/cache/cache.go`
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/prepare_test.go`
- Modify: `internal/cache/agent_test.go`
- Modify: `internal/pluginresource/artifact.go`
- Modify: `internal/pluginresource/manager.go`
- Modify: `internal/pluginresource/manager_test.go`
- Modify: `internal/lifecycle/install.go`
- Modify: `internal/lifecycle/install_resolve.go`
- Modify: `internal/lifecycle/install_resolve_test.go`
- Modify: `internal/lifecycle/install_skill.go`
- Modify: `internal/lifecycle/install_plugin.go`
- Modify: `internal/lifecycle/install_mcp.go`
- Modify: `internal/lifecycle/install_agent.go`
- Modify: `internal/lifecycle/install_test_helpers_test.go`
- Modify: `internal/lifecycle/install_test.go`
- Modify: `internal/lifecycle/install_resolve_test.go`
- Modify: `internal/lifecycle/install_skill_test.go`
- Modify: `internal/lifecycle/install_plugin_test.go`
- Modify: `internal/lifecycle/install_mcp_test.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/remote/skill.go`
- Modify: `internal/cloud/remote/plugin.go`
- Modify: `internal/cloud/remote/mcp.go`
- Modify: `internal/cloud/remote/agent.go`
- Modify: `internal/cloud/remote/content.go`
- Modify: `internal/cloud/remote/metadata.go`
- Modify: `internal/cloud/remote/skill_test.go`
- Modify: `internal/cloud/remote/plugin_test.go`
- Modify: `internal/cloud/remote/mcp_test.go`
- Modify: `internal/cloud/remote/agent_test.go`
- Modify: `internal/cloud/remote/content_test.go`
- Modify: `internal/cloud/local/skill.go`
- Modify: `internal/cloud/local/plugin.go`
- Modify: `internal/cloud/local/mcp.go`
- Modify: `internal/cloud/local/agent.go`
- Modify: `internal/cloud/local/skill_test.go`
- Modify: `internal/cloud/local/plugin_test.go`
- Modify: `internal/cloud/local/mcp_test.go`
- Modify: `internal/cloud/local/agent_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: `CatalogManager.SnapshotLocked`, `Snapshot.Resolve`, Task 4's private Remote verification seam, and Task 5's Local catalog inventory.
- Produces:

```go
type cache.Source interface {
	DownloadSkill(context.Context, domain.CatalogEntry, string) error
	DownloadAgent(context.Context, domain.CatalogEntry, string) error
}

type cache.PowerCache interface {
	PrepareSkill(context.Context, domain.CatalogEntry) (string, error)
	PrepareAgent(context.Context, domain.CatalogEntry) (string, error)
}

type pluginresource.Downloader interface {
	DownloadPlugin(context.Context, domain.CatalogEntry, string) error
}

type pluginresource.Manager interface {
	Ensure(context.Context, domain.CatalogEntry) (Artifact, error)
	Lookup(domain.PowerRef) (Artifact, error)
	StageFilesystem(domain.PowerRef) (string, error)
}
```

- [ ] **Step 1: Write cache/resource entry propagation RED tests**

Assert cache keys still use exact `entry.Ref`, but every miss forwards the complete immutable entry once. Assert Plugin Ensure forwards the same entry; cache hits perform zero provider calls and reject cached artifacts whose identity differs from `entry.Ref`.

- [ ] **Step 2: Run cache/resource RED**

Run: `go test -count=1 ./internal/cache ./internal/pluginresource`

Expected: FAIL at compile time because interfaces still accept `PowerRef`.

- [ ] **Step 3: Implement entry propagation in cache/resource modules**

Change only miss/download boundaries; keep artifact layout, validation, and staging semantics unchanged. Add Chinese comments to the modified interfaces stating that catalog metadata is authoritative and must not be reconstructed.

- [ ] **Step 4: Write install resolution RED tests**

Assert install obtains the existing global lifecycle lock, performs Recover, preserves exact Plugin manifest unchanged early return with zero catalog calls, then calls `SnapshotLocked` at most once. Cover explicit type/latest, explicit SemVer, missing type cross-group ambiguity, missing ref, stale warning once, stale artifact-cache hit success, and stale cache miss content failure. Assert each type receives the exact `CatalogEntry` returned by resolution.

- [ ] **Step 5: Implement entry-based install**

Atomically change all four Cloud content interfaces and both provider implementations from `PowerRef` to `CatalogEntry`. For Remote, URL identity comes from `entry.Ref` and verification comes only from entry metadata; remove metadata lookup from those paths. For Local, verify the supplied entry matches freshly loaded exact bytes. Replace `resolvePower` with `resolveCatalogEntry`; use one `SnapshotLocked` for type/version resolution and metadata, pass `entry.Ref` only to transaction/adapters/manifests, and pass full `entry` to cache/plugin/MCP/Agent content. Connect the same composed Manager to install and do not perform a second Manager or provider metadata call.

- [ ] **Step 6: Verify and commit**

Run:

```bash
gofmt -w internal/cache internal/pluginresource internal/lifecycle
go test -count=1 ./internal/cache ./internal/pluginresource ./internal/lifecycle -run 'Test(Prepare|Ensure|Install|Resolve)'
go test -race -count=1 ./internal/cache ./internal/pluginresource ./internal/lifecycle
git diff --check
git add internal/cache internal/pluginresource internal/lifecycle
git commit -m "refactor(install): consume catalog metadata once"
```

Expected: all commands exit 0.

---

### Task 8: Remove MCP Definition Dependency from Uninstall Recovery

**Files:**
- Modify: `internal/lifecycle/uninstall_mcp.go`
- Modify: `internal/lifecycle/uninstall_mcp_test.go`
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/journal.go`
- Modify: `internal/transaction/mcp_uninstall.go`
- Modify: `internal/transaction/mcp_uninstall_test.go`
- Modify: `internal/transaction/mcp_recovery.go`
- Modify: `internal/transaction/mcp_recovery_test.go`
- Modify: `internal/transaction/mcp_validation.go`
- Modify: `internal/transaction/mcp_validation_test.go`
- Modify: `internal/transaction/journal_schema_test.go`
- Modify: MCP lifecycle/integration test helpers that construct uninstall requests or journals

**Interfaces:**
- Consumes: Agent CLI `Inspect` and `Remove`, local manifest repository, journal schema v1 with no backward compatibility requirement.
- Produces:

```go
type MCPUninstallRequest struct {
	Agent   domain.AgentID
	Power   domain.PowerRef
	Adapter adapter.MCPAdapter
}
```

For `OperationUninstall`, `AgentCLIJournalPayload.Server` is not required and recovery never calls `Add`; for `OperationInstall`, Server/PreviousServer requirements remain unchanged.

- [ ] **Step 1: Write lifecycle zero-cloud RED tests**

For MCP present and absent, assert uninstall makes zero Cloud/catalog calls. Present must call inspect then transaction/remove; absent must remove only manifest. Add a fake that fails the test on any `GetMCP`, `Snapshot`, or content request.

- [ ] **Step 2: Write transaction/recovery RED tests**

Assert present uninstall creates a journal without Server definition, removes by ref, and commits manifest deletion. Cover crashes at: before remove, after applied remove before phase update, after phase update, and during manifest/delete. `fix` must converge as follows: if MCP present, invoke Remove; if absent, commit manifest removal; it must never invoke Add. Preserve `Unknown` as an active recoverable journal rather than guessing synchronously.

- [ ] **Step 3: Run MCP uninstall RED**

Run: `go test -count=1 ./internal/transaction ./internal/lifecycle -run 'Test.*MCP.*Uninstall|Test.*MCP.*Recovery'`

Expected: FAIL because Server is currently mandatory and recovery can re-add it.

- [ ] **Step 4: Implement the simplified uninstall journal and recovery**

Remove `Server` from `MCPUninstallRequest`; do not fetch definition in lifecycle. Encode uninstall AgentCLI payload without a definition, update validation by operation, and make `recoverMCPUninstall` continue toward absence: present → persist removing direction and Remove; absent → persist committing and remove manifest. Keep install rollback code and its definitions intact.

- [ ] **Step 5: Verify schema and integration, then commit**

Run:

```bash
gofmt -w internal/transaction internal/lifecycle
go test -count=1 ./internal/transaction ./internal/lifecycle -run 'Test.*MCP'
go test -race -count=1 ./internal/transaction ./internal/lifecycle
git diff --check
git add internal/transaction internal/lifecycle
git commit -m "refactor(mcp): uninstall without cloud definition"
```

Expected: all commands exit 0.

---

### Task 9: Complete Composition, Remove Obsolete APIs, and Prove Provider Parity

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/provider.go`
- Modify: `internal/cloud/provider_test.go`
- Modify: `internal/cloud/contract_test.go`
- Modify: `internal/cloud/remote_test_server_test.go`
- Modify: lifecycle fake clients/helpers still implementing removed methods
- Modify: `cmd/apm/cloud.go`
- Modify: `cmd/apm/cloud_test.go`
- Modify: `cmd/apm/main.go`
- Modify: `cmd/apm/main_test.go`
- Delete: `internal/cloud/remote/metadata.go`
- Delete: `internal/cloud/remote/metadata_test.go`

**Interfaces:**
- Consumes: Tasks 3–8 final interfaces.
- Produces final `cloud.Client`:

```go
type Client interface {
	CatalogSource
	SkillSource
	PluginSource
	MCPSource
	AgentSource
	Submission
}
```

where all four content source methods accept `domain.CatalogEntry` and no public resource discovery/metadata method remains.

- [ ] **Step 1: Add strict final contract RED tests**

Create one oracle catalog and run the same suite against Local and Remote: version/hash/size parity, full exact bytes, exact content success for all types, cancellation, missing exact content, and mismatched metadata. The Remote fake must count and reject `/resources?cursor=`, `/versions`, `/latest`, and non-content exact metadata paths.

- [ ] **Step 2: Run contract RED**

Run: `go test -count=1 ./internal/cloud/... ./internal/lifecycle ./cmd/apm`

Expected: FAIL while stale interfaces/fakes/composition remain.

- [ ] **Step 3: Remove compatibility surface and finish composition**

Delete `cloud.Catalog`, old Remote cursor DTO/loops, series/latest/exact-metadata routes, Local List/Resolve, and every fake wrapper used only by them. Keep review-ticket cursor and Submission active-list behavior. Ensure selected provider implements one final Client and one Manager instance is shared by command lifecycles without eager Remote construction on Local-only commands.

- [ ] **Step 4: Run mandatory stale scans**

Run:

```bash
! rg -n 'NextCursor|Catalog\.List|Catalog\.Resolve|\.List\(ctx\)|\.Resolve\(ctx|latest metadata|exact metadata|/latest|/versions\?' --glob '*.go' cmd internal
rg -n 'Review.*Cursor|review.*cursor' internal/cloud internal/domain
```

Expected: first command exits 0 with no output; second command still finds the internal review-ticket cursor contract.

- [ ] **Step 5: Verify whole codebase and commit**

Run:

```bash
gofmt -w cmd internal
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
build_root=$(mktemp -d)
go build -o "$build_root/apm" ./cmd/apm
test -x "$build_root/apm"
rm -f "$build_root/apm"
rmdir "$build_root"
git diff --check
git add -A cmd internal
git commit -m "refactor(cloud): finalize catalog provider contract"
```

Expected: all commands exit 0 and the temporary build directory is absent.

---

### Task 10: Update APM/APC Authority Docs, Changelog, and Final Gates

**Files:**
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/list.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/search.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/uninstall.mmd`
- Modify: historical Feature 020/023 documents only to add explicit supersession notices linking Feature 024/ADR-021
- Modify in APC worktree: `ivory-tower/project-overview.md`
- Modify in APC worktree: `ivory-tower/project-overview-apm.md`
- Modify in APC worktree: `ivory-tower/c4/components/apm_cli.dsl`
- Modify in APC worktree: current HTTP API/interface specification files found from Project Overview links
- Modify in APC worktree: historical public resource pagination documents only to add supersession notices linking Feature 002/ADR-004
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: final implementation and all behavior commit hashes.
- Produces: one consistent APM/APC authority model and one Feature 024 changelog entry.

- [ ] **Step 1: Update APM current documentation**

Document: grouped schema versions; `~/.apm/catalog.json`; SHA-256/size version check; stale warning/failure matrix; non-blocking `apm.lock`; `Snapshot` versus `SnapshotLocked`; list/search/install call boundaries; offline artifact-cache install; entry-bound downloads; zero-catalog uninstall/fix; and MCP uninstall recovery without definition. Add ADR-021 links at each architectural decision point.

- [ ] **Step 2: Update APC current API documentation**

Specify exactly:

```http
GET /api/v1/resources/version
Content-Type: application/json

{"schema_version":1,"catalog_sha256":"<64 lowercase hex>","catalog_size":123456}
```

```http
GET /api/v1/resources
Content-Type: application/json
```

The second response is the exact deterministic catalog object referenced by version. Remove public list pagination, series/latest/exact-metadata endpoints and query parameters; retain only `GET /api/v1/resources/{type}/{name}/versions/{version}/content`. State that publication/yank/restore atomically replaces catalog; Submission/review intermediate states do not. Keep internal review-ticket cursor and Submission active-list unchanged. Link ADR-004.

- [ ] **Step 3: Update and validate diagrams**

Show `Catalog Manager → Cloud CatalogSource → Local/Remote`, Repository ownership, lifecycle snapshot consumption, and exact entry propagation. Show list/search taking/releasing `apm.lock`, install using its already-held lock, and uninstall bypassing catalog. Convert every changed Mermaid source with Draw.io into a private temp output and remove it. Validate the APC Structurizr workspace.

- [ ] **Step 4: Add exactly one Changelog entry**

Use `~/.codex/docs/Changelog.md`. The entry must summarize catalog caching/API contraction/MCP uninstall recovery, list all Feature 024 APM behavior commits plus the APC documentation commit, omit plan/design-only commits if the template classifies them separately, and remain reverse chronological. Do not add a second entry during fixes.

- [ ] **Step 5: Run documentation and stale-contract checks**

Run:

```bash
feature024_apc_root=/home/liangff/Workspace/projects/bianbu-next/apc/.worktrees/feature-024-catalog-cache
rg -n 'ADR-021|catalog.json|SnapshotLocked|/api/v1/resources/version' BUILD.md ivory-tower
rg -n 'ADR-004|catalog_sha256|/api/v1/resources/version' "$feature024_apc_root/ivory-tower"
! rg -n 'public.*cursor|/latest|exact-metadata|series endpoint' BUILD.md ivory-tower/project-overview.md ivory-tower/c4 "$feature024_apc_root"/ivory-tower/project-overview*.md "$feature024_apc_root/ivory-tower/c4"
git diff --check
git -C "$feature024_apc_root" diff --check
```

Expected: current documents contain the new authority; the stale-current scan has no output; both diff checks exit 0.

- [ ] **Step 6: Run fresh final verification**

Run:

```bash
feature024_apc_root=/home/liangff/Workspace/projects/bianbu-next/apc/.worktrees/feature-024-catalog-cache
gofmt -w cmd internal
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
final_build_root=$(mktemp -d)
go build -o "$final_build_root/apm" ./cmd/apm
test -x "$final_build_root/apm"
rm -f "$final_build_root/apm"
rmdir "$final_build_root"
git diff --check
git -C "$feature024_apc_root" diff --check
```

Expected: all commands exit 0, no build/temp residue exists, and both worktrees contain only the intended uncommitted documentation/Changelog files.

- [ ] **Step 7: Commit APC docs, then APM docs and Changelog**

Run:

```bash
feature024_apc_root=/home/liangff/Workspace/projects/bianbu-next/apc/.worktrees/feature-024-catalog-cache
git -C "$feature024_apc_root" add ivory-tower
git -C "$feature024_apc_root" commit -m "docs(api): define catalog snapshot endpoints"
git add BUILD.md ivory-tower docs/Changelog.md
git commit -m "docs(catalog): document cached resource discovery"
git status --short
git -C "$feature024_apc_root" status --short
```

Expected: both commits succeed and both worktrees are clean.

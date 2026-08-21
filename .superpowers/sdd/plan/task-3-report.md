# Task 3 Report: Isolate Local Provider and Add Explicit Registry

## Status

Completed in commit `ed1eb08 refactor(cloud): isolate local provider plugin`.

## Implementation

- Added the instance-owned `cloud.ProviderRegistry`, `ProviderID`, `Factory`, `ProviderLocal`, and `ProviderRemote`. Registration rejects empty IDs, duplicate IDs, and nil factories. Open rejects empty/unknown providers, factory failures, nil clients, and typed-nil clients; every failure wraps `cloud.ErrProviderConfig`.
- Moved the entire Local implementation and its tests into `internal/cloud/local`, including the Linux and non-Linux Agent no-replace helpers and the private `beforeAgentPublish` same-package test seam.
- Added `local.New(resourcesRoot string) (*local.Client, error)` and a compile-time `cloud.Client` assertion. Production composition exposes `cloud.Client` and calls `local.New` directly; no Remote factory is registered.
- Centralized physical source/destination validation in `local/filesystem.go`. Skill and Plugin trees reject symlinks, hardlinks, special nodes, overlap, unsafe destination state, binary/archive payloads, files over 10 MiB, trees over 100 MiB, over 1,000 nodes, depth over 16, and relative paths over 255 bytes.
- MCP and Agent reads reject non-physical, hardlinked, binary/archive, or over-20-MiB content. Skill/Plugin publication uses call-owned sibling temporary directories; Agent retains atomic no-replace publication. Cancellation is checked during validation/copy and before Agent publication.
- Local production code has no Cache/staging fields and no `internal/paths`, home, or cache imports. Cache integration remains through `cloud.Client` only.
- Migrated direct test consumers in lifecycle and pluginresource packages to `local.New` so the old Local symbols are fully absent.

## Files and moves

Created:

- `internal/cloud/provider.go`
- `internal/cloud/provider_test.go`
- `internal/cloud/local/filesystem.go`
- `internal/cloud/local/filesystem_test.go`

Moved and updated:

- `internal/cloud/local_client.go` -> `internal/cloud/local/client.go`
- `internal/cloud/local_skill.go` -> `internal/cloud/local/skill.go`
- `internal/cloud/local_plugin.go` -> `internal/cloud/local/plugin.go`
- `internal/cloud/local_mcp.go` -> `internal/cloud/local/mcp.go`
- `internal/cloud/local_agent.go` -> `internal/cloud/local/agent.go`
- `internal/cloud/local_agent_rename_linux.go` -> `internal/cloud/local/agent_rename_linux.go`
- `internal/cloud/local_agent_rename_other.go` -> `internal/cloud/local/agent_rename_other.go`
- All six former `local_*_test.go` files -> corresponding `internal/cloud/local/*_test.go` files.

Modified consumers/tests:

- `cmd/apm/main.go`, `cmd/apm/main_test.go`
- `internal/cloud/errors_test.go`
- `internal/lifecycle/mcp_integration_test.go`
- `internal/lifecycle/plugin_acceptance_test.go`
- `internal/lifecycle/plugin_integration_test.go`
- `internal/pluginresource/manager_test.go`
- `internal/pluginresource/marketplace_test.go`

## TDD evidence

### RED

Registry command:

```bash
go test -count=1 ./internal/cloud -run TestProviderRegistry
```

Exact output:

```text
FAIL	apm/internal/cloud [build failed]
FAIL
# apm/internal/cloud_test [apm/internal/cloud.test]
internal/cloud/provider_test.go:26:20: undefined: cloud.NewProviderRegistry
internal/cloud/provider_test.go:28:36: undefined: cloud.ProviderLocal
internal/cloud/provider_test.go:31:34: undefined: cloud.ProviderLocal
internal/cloud/provider_test.go:40:18: undefined: cloud.ProviderID
internal/cloud/provider_test.go:41:18: undefined: cloud.Factory
internal/cloud/provider_test.go:42:24: undefined: cloud.ProviderRegistry
internal/cloud/provider_test.go:45:35: undefined: cloud.ProviderLocal
internal/cloud/provider_test.go:48:16: undefined: cloud.ProviderLocal
internal/cloud/provider_test.go:50:35: undefined: cloud.ProviderRegistry
internal/cloud/provider_test.go:51:39: undefined: cloud.ProviderLocal
internal/cloud/provider_test.go:51:39: too many errors
```

Local safety command:

```bash
go test -count=1 ./internal/cloud/local -run 'TestDownloadSkillRejectsUnsafeSourceTree|TestDownloadSkillRejectsOverlappingAndNonEmptyDestination|TestMCPAndAgentRejectContentOverTwentyMiB'
```

Exact output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/filesystem_test.go:115:19: undefined: New
internal/cloud/local/filesystem_test.go:128:17: undefined: New
internal/cloud/local/filesystem_test.go:156:17: undefined: New
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

The failures were expected: registry symbols and the isolated Local constructor did not exist before production edits.

### GREEN

Registry focused command and exact output:

```text
go test -count=1 ./internal/cloud -run TestProviderRegistry
ok  	apm/internal/cloud	0.004s
```

Local safety focused command and exact output:

```text
go test -count=1 ./internal/cloud/local -run 'TestDownloadSkillRejectsUnsafeSourceTree|TestDownloadSkillRejectsOverlappingAndNonEmptyDestination|TestMCPAndAgentRejectContentOverTwentyMiB'
ok  	apm/internal/cloud/local	0.061s
```

Moved Local package command and exact output:

```text
go test -count=1 -timeout=60s ./internal/cloud/local
ok  	apm/internal/cloud/local	0.095s
```

Required package command and exact output:

```text
go test -count=1 ./internal/cloud/... ./cmd/apm
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	0.129s
ok  	apm/cmd/apm	0.117s
```

Required race command and exact output:

```text
go test -race -count=1 ./internal/cloud/...
ok  	apm/internal/cloud	1.017s
ok  	apm/internal/cloud/local	1.147s
```

Migration searches produced no output and exited successfully:

```bash
rg -n 'NewLocalClient|type LocalClient' internal/cloud cmd --glob '*.go'
rg -n 'apm/internal/paths' internal/cloud/local --glob '*.go'
```

`go vet ./internal/cloud/... ./cmd/apm` also exited successfully with no output.

Full-suite command and exact output:

```text
go test -count=1 ./...
ok  	apm/cmd/apm	0.067s
ok  	apm/internal/adapter	0.080s
ok  	apm/internal/cache	0.019s
ok  	apm/internal/cli	0.012s
ok  	apm/internal/cloud	0.006s
ok  	apm/internal/cloud/local	0.095s
ok  	apm/internal/config	0.056s
ok  	apm/internal/domain	0.003s
ok  	apm/internal/lifecycle	5.630s
ok  	apm/internal/lock	0.006s
ok  	apm/internal/logging	0.006s
ok  	apm/internal/paths	0.008s
ok  	apm/internal/pluginartifact	0.033s
ok  	apm/internal/pluginresource	0.079s
ok  	apm/internal/transaction	0.267s
ok  	apm/internal/utils/filesystem	0.017s
ok  	apm/internal/version	0.005s
```

`git diff --check` and the cached equivalent both exited successfully with no output.

## Self-review

- Confirmed the registry contains only per-instance state, initializes safely from either the constructor or zero value, uses a mutex for concurrent Register/Open access, and does not mutate during Open.
- Confirmed every registry configuration path is contextual and classifiable with `errors.Is(err, cloud.ErrProviderConfig)`, including typed-nil results and factory errors.
- Confirmed all unsafe source/destination matrix paths are classifiable with `cloud.ErrUnsafeContent`; validation happens before any destination or temporary publication path is created.
- Confirmed Skill and Plugin share the same limits and context-aware physical-tree validator, while MCP and Agent share the 20 MiB physical-file validator.
- Confirmed Local never imports `internal/paths` or accesses cache/home production paths. Integration tests use only temporary homes and resources.
- Confirmed Task 1 canonical version filtering/order/Resolve behavior and Task 2 destination/no-replace behavior remain covered by the moved tests and full suite.
- Confirmed no global registry, package `init`, or partial Remote construction/registration was introduced.
- Confirmed no Changelog entry was added, per the task instruction.

## Concerns

No functional concerns identified. The physical-tree checks are intentionally conservative and accept text-only Skill/Plugin content; binary and archive payloads are rejected by design.

## Fix Round 1

Commit: `2e9a27d fix(cloud): harden local provider snapshots`.

### Scope and root causes

1. Local tree validation walked pathnames, then publication walked and reopened the same pathnames. Single-file validation similarly used `Lstat` followed by `ReadFile`. Source entries, ancestors, sizes, or contents could therefore change between approval and consumption.
2. Tree copy used an unbounded `io.Copy`, and no context check existed after copy immediately before destination removal/rename. MCP also returned a decoded value without a final cancellation check.
3. The non-Linux Agent link/unlink fallback left its newly linked destination behind when unlinking the source temporary failed.
4. Reflective `Nlink` extraction silently returned “not hardlinked” when platform metadata was missing.
5. The original safety matrix lacked device coverage, inclusive-boundary acceptance, a direct Plugin shared-limit assertion, and MCP/Agent hardlink/binary/archive cases; Unix-only FIFO/socket setup was also in an untagged test file.

### Implementation

- Linux source consumption now opens the absolute resources root with `openat2(RESOLVE_NO_SYMLINKS|RESOLVE_NO_MAGICLINKS)`, opens all descendants beneath that pinned descriptor with `RESOLVE_BENEATH`, and uses `O_PATH` plus descriptor identity comparisons to reject swaps between metadata inspection and data open.
- Regular-file bytes are bounded-read from verified descriptors into an immutable in-memory snapshot. Limits and binary/archive signatures are evaluated against those exact bytes, and Skill/Plugin publication copies only that snapshot. Source mutations after capture cannot alter published bytes.
- Snapshot output writes are context-aware in 32 KiB chunks. Tree publication checks cancellation after copy, after destination revalidation, and immediately before rename. MCP checks cancellation after decode; Agent retains its existing pre-publication check.
- Non-Linux source capture fails closed with `cloud.ErrUnsafeContent`. The deployment Linux contract is not silently weakened on platforms without the descriptor-safe traversal.
- The non-Linux Agent fallback delegates link/unlink to a testable helper. Source-unlink failure attempts to remove the newly created destination and joins rollback failure with the primary error.
- Link-count extraction is build-tagged. Linux requires `*syscall.Stat_t` and rejects unavailable metadata; every non-Linux build has an explicit fail-closed definition.
- Linux-tagged tests cover FIFO, socket, and device nodes (device creation skips only when the kernel denies `mknod`). Additional tests cover exact 10 MiB file / 100 MiB tree / 20 MiB single-file acceptance, Plugin shared file limits, and MCP/Agent hardlink, binary, ZIP, and `ar` archive content.
- The descriptor snapshot has an intentional memory cost of up to 100 MiB per Skill/Plugin download (plus destination write buffers). Kernels without usable `openat2`, and all current non-Linux builds, reject Local source reads rather than weakening the safety guarantee.

### TDD evidence

#### Finding 1 and 2: descriptor binding and cancellation

RED command:

```bash
go test -count=1 ./internal/cloud/local -run 'Test(DownloadSkillRejectsSourceRootSwapBeforeDescriptorSnapshot|DownloadSkillRejectsFileGrowthBetweenMetadataAndDescriptorRead|DownloadSkillPublishesValidatedSnapshotWhenSourceChangesAfterRead|DownloadSkillCancellationBeforePublishLeavesNoDestination|WriteAllContextStopsWhenCanceledDuringCopy|GetMCPCancellationAfterDecodeReturnsCanceled)'
```

Exact RED output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/source_linux_test.go:24:9: client.beforeTreeSnapshot undefined (type *Client has no field or method beforeTreeSnapshot)
internal/cloud/local/source_linux_test.go:49:9: client.beforeSnapshotFileOpen undefined (type *Client has no field or method beforeSnapshotFileOpen)
internal/cloud/local/source_linux_test.go:73:9: client.beforeTreePublish undefined (type *Client has no field or method beforeTreePublish)
internal/cloud/local/source_linux_test.go:96:9: client.beforeTreePublish undefined (type *Client has no field or method beforeTreePublish)
internal/cloud/local/source_linux_test.go:122:9: undefined: writeAllContext
internal/cloud/local/source_linux_test.go:141:9: client.afterMCPDecode undefined (type *Client has no field or method afterMCPDecode)
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

Reason: Local had no immutable source snapshot boundary, context-aware snapshot writer, deterministic pre-publication seam, or post-decode cancellation boundary.

The first GREEN attempt exposed one unused import while compiling the new Linux file:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/source_linux.go:8:2: "fmt" imported and not used
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

After removing that import, exact GREEN output:

```text
ok  	apm/internal/cloud/local	0.065s
```

An additional deterministic file-inode replacement regression was added beside the root-swap and growth cases and passed in the complete Local package run.

#### Finding 3: non-Linux Agent rollback

RED command:

```bash
go test -count=1 ./internal/cloud/local -run '^TestLinkAgentNoReplaceRollsBackDestinationWhenSourceRemovalFails$'
```

Exact RED output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/agent_rename_fallback_test.go:26:9: undefined: linkAgentNoReplace
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

Reason: the fallback directly called `os.Link` and `os.Remove`; it had neither rollback orchestration nor a fakeable operation boundary.

Exact GREEN output:

```text
ok  	apm/internal/cloud/local	0.005s
```

#### Finding 4: fail-closed hard-link metadata

RED command:

```bash
go test -count=1 ./internal/cloud/local -run '^TestRejectMultipleLinksFailsClosedWithoutLinuxLinkMetadata$'
```

Exact RED output against the reflective baseline:

```text
--- FAIL: TestRejectMultipleLinksFailsClosedWithoutLinuxLinkMetadata (0.00s)
    link_count_linux_test.go:17: rejectMultipleLinks() error = <nil>, want ErrUnsafeContent
FAIL
FAIL	apm/internal/cloud/local	0.003s
FAIL
```

Reason: missing/unknown `Sys().Nlink` metadata was silently interpreted as a single-link file.

Exact GREEN output with tagged implementations:

```text
ok  	apm/internal/cloud/local	0.004s
```

#### Finding 5: expanded safety matrix

RED command:

```bash
go test -count=1 ./internal/cloud/local -run '^TestMCPAndAgentRejectHardlinkBinaryAndArchiveContent$'
```

Exact RED output:

```text
--- FAIL: TestMCPAndAgentRejectHardlinkBinaryAndArchiveContent (0.01s)
    --- FAIL: TestMCPAndAgentRejectHardlinkBinaryAndArchiveContent/mcp/ar_archive (0.00s)
        safety_limits_linux_test.go:123: GetMCP() error = decode MCP definition "/tmp/TestMCPAndAgentRejectHardlinkBinaryAndArchiveContentmcpar_arch1312041320/001/mcps/demo/v1.0.0/MCP.json": invalid character '!' looking for beginning of value, want ErrUnsafeContent
    --- FAIL: TestMCPAndAgentRejectHardlinkBinaryAndArchiveContent/agent/ar_archive (0.00s)
        safety_limits_linux_test.go:129: download error = <nil>, want ErrUnsafeContent
FAIL
FAIL	apm/internal/cloud/local	0.014s
FAIL
```

Reason: the ASCII `ar` archive magic was valid UTF-8 and escaped the existing binary/archive signature set; MCP exposed it as a decode error and Agent published it.

Exact GREEN output:

```text
ok  	apm/internal/cloud/local	0.044s
```

The broader newly tagged safety command also passed, covering exact limits, Plugin limits, and Linux special nodes:

```text
go test -count=1 ./internal/cloud/local -run 'Test(SkillAcceptsExactFileAndTreeSizeLimits|AgentAndMCPAcceptExactTwentyMiBLimit|DownloadPluginRejectsSharedTreeFileLimit|MCPAndAgentRejectHardlinkBinaryAndArchiveContent|DownloadSkillRejectsLinuxSpecialNodes)'
ok  	apm/internal/cloud/local	0.523s
```

### Final verification

Focused Local package:

```text
go test -count=1 -timeout=120s ./internal/cloud/local
ok  	apm/internal/cloud/local	0.831s
```

Required Cloud/APM packages:

```text
go test -count=1 ./internal/cloud/... ./cmd/apm
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	0.834s
ok  	apm/cmd/apm	0.048s
```

Race verification:

```text
go test -race -count=1 ./internal/cloud/...
ok  	apm/internal/cloud	1.015s
ok  	apm/internal/cloud/local	7.965s
```

`go vet ./internal/cloud/... ./cmd/apm` exited successfully with no output.

Full suite:

```text
go test -count=1 ./...
ok  	apm/cmd/apm	0.051s
ok  	apm/internal/adapter	0.109s
ok  	apm/internal/cache	0.028s
ok  	apm/internal/cli	0.016s
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	0.927s
ok  	apm/internal/config	0.044s
ok  	apm/internal/domain	0.004s
ok  	apm/internal/lifecycle	5.198s
ok  	apm/internal/lock	0.005s
ok  	apm/internal/logging	0.003s
ok  	apm/internal/paths	0.007s
ok  	apm/internal/pluginartifact	0.053s
ok  	apm/internal/pluginresource	0.109s
ok  	apm/internal/transaction	0.317s
ok  	apm/internal/utils/filesystem	0.013s
ok  	apm/internal/version	0.003s
```

Cross-platform compile-only checks all exited successfully with no output:

```bash
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/apm-local-darwin.test ./internal/cloud/local
GOOS=windows GOARCH=amd64 go build ./internal/cloud/local
GOOS=freebsd GOARCH=amd64 go build ./internal/cloud/local
```

`git diff --check` and `git diff --cached --check` exited successfully with no output.

### Files

- Descriptor snapshots and portable boundary: `source.go`, `source_linux.go`, `source_other.go`, `client.go`, `skill.go`, `plugin.go`, `mcp.go`, `agent.go`, `filesystem.go`.
- Hard-link boundary: `link_count_linux.go`, `link_count_other.go`, `link_count_linux_test.go`.
- Agent fallback rollback: `agent_rename_fallback.go`, `agent_rename_other.go`, `agent_rename_fallback_test.go`.
- Regressions/matrix: `source_linux_test.go`, `filesystem_test.go`, `filesystem_special_linux_test.go`, `safety_limits_linux_test.go`.

### Self-review

- Confirmed production has no pathname-based `WalkDir`/`ReadFile` source consumption remaining. Pathname prechecks only preserve stable exact-miss classification; all accepted bytes come from verified descriptors.
- Confirmed data-descriptor identity is compared with the pinned metadata descriptor after the deterministic swap point, and link count is checked on the data descriptor actually read.
- Confirmed every published Skill/Plugin byte was already size/signature validated in the immutable snapshot; post-snapshot source changes do not alter output.
- Confirmed cancellation stops bounded reads/writes and is checked after snapshot copy and directly before publication. MCP checks after decode and Agent checks before no-replace rename.
- Confirmed rollback removes only the destination created by the current fallback call and joins rollback failure without hiding the source-unlink error.
- Confirmed non-Linux compilation succeeds and non-Linux Local source reads are explicitly unsafe rather than silently using weaker path checks.
- Confirmed registry/package isolation, Task 1 resolution rules, Task 2 no-replace behavior, and cache/home ownership remain unchanged.

### Concerns

- Linux Local downloads now require a kernel with `openat2`; unavailable syscalls fail closed with `cloud.ErrUnsafeContent`.
- Skill/Plugin downloads snapshot up to 100 MiB in memory to guarantee that published bytes exactly match validated bytes.
- Non-Linux Local catalog discovery still compiles, but content retrieval intentionally fails closed until an equivalent descriptor-safe backend is implemented.

## Fix Round 2

### Outcome

- Skill now requires `SKILL.md`, and Plugin now requires `.codex-plugin/plugin.json`, in the descriptor/handle-captured tree itself. Removal or replacement with a directory after the pathname precheck returns an error wrapping `cloud.ErrUnsafeContent` and publishes nothing.
- MCP clones the decoded server before its final context check. The private `afterMCPClone` same-package seam deterministically proves cancellation between clone and return cannot leak a successful result.
- Linux, Darwin, and FreeBSD use a component-by-component `openat(O_NOFOLLOW)` snapshot backend. It pins each directory/file descriptor, compares file identity across the deterministic metadata/open race seam, bounds reads from the verified descriptor, and does not depend on `openat2`; Linux therefore works when `openat2` is unavailable.
- Windows uses `CreateFile` handles with `FILE_FLAG_OPEN_REPARSE_POINT`, rejects reparse-point handles, verifies every component's normalized final handle path, snapshots bytes from the verified handle, and rejects files whose handle reports multiple links.
- Other targets remain explicit fail-closed builds rather than silently consuming source content through pathname-following APIs.

### TDD evidence

#### Finding 1: required markers belong to the captured tree

RED command:

```bash
go test ./internal/cloud/local -run 'TestDownload(Skill|Plugin)RequiresMarkerInDescriptorSnapshot' -count=1
```

Exact RED output:

```text
--- FAIL: TestDownloadSkillRequiresMarkerInDescriptorSnapshot (0.00s)
    --- FAIL: TestDownloadSkillRequiresMarkerInDescriptorSnapshot/removed (0.00s)
        source_linux_test.go:64: download error = <nil>, want ErrUnsafeContent
    --- FAIL: TestDownloadSkillRequiresMarkerInDescriptorSnapshot/directory (0.00s)
        source_linux_test.go:64: download error = <nil>, want ErrUnsafeContent
--- FAIL: TestDownloadPluginRequiresMarkerInDescriptorSnapshot (0.00s)
    --- FAIL: TestDownloadPluginRequiresMarkerInDescriptorSnapshot/removed (0.00s)
        source_linux_test.go:95: download error = <nil>, want ErrUnsafeContent
    --- FAIL: TestDownloadPluginRequiresMarkerInDescriptorSnapshot/directory (0.00s)
        source_linux_test.go:95: download error = <nil>, want ErrUnsafeContent
FAIL
FAIL	apm/internal/cloud/local	0.008s
FAIL
```

Reason: both callers trusted pre-snapshot path state; the captured snapshot carried no invariant describing required regular files.

Exact first GREEN output:

```text
ok  	apm/internal/cloud/local	0.008s
```

The regressions were then moved to the platform-neutral `source_snapshot_test.go`, so each enabled platform exercises the same captured-marker contract.

#### Finding 2: cancellation after MCP clone

RED command:

```bash
go test ./internal/cloud/local -run TestGetMCPCancellationAfterCloneReturnsCanceled -count=1
```

Exact RED output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/source_linux_test.go:245:9: client.afterMCPClone undefined (type *Client has no field or method afterMCPClone)
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

Reason: the only deterministic boundary was after decode, matching the production cancellation check that incorrectly preceded `server.Clone()`.

Exact GREEN output, including the earlier after-decode regression:

```text
ok  	apm/internal/cloud/local	0.030s
```

#### Finding 3: portable descriptor/handle snapshot backend

RED command:

```bash
go test ./internal/cloud/local -run TestOpenatSnapshotBackendDownloadsValidSkillWithoutOpenat2 -count=1
```

Exact RED output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/source_linux_test.go:106:19: undefined: capturePhysicalTreeOpenat
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

Reason: Linux had only the `openat2` implementation and all non-Linux builds returned `ErrUnsafeContent` for every valid retrieval.

Exact first Linux GREEN output:

```text
ok  	apm/internal/cloud/local	0.006s
```

Platform compile-only command (all three commands exited successfully with no output):

```bash
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/apm-local-darwin.test ./internal/cloud/local
GOOS=freebsd GOARCH=amd64 go test -c -o /tmp/apm-local-freebsd.test ./internal/cloud/local
GOOS=windows GOARCH=amd64 go test -c -o /tmp/apm-local-windows.test ./internal/cloud/local
```

Linux runs the same openat backend used by the Darwin/FreeBSD tagged runtime regression. A Windows-tagged runtime regression exercises a valid handle snapshot when run on Windows; this Linux worker could compile but not execute that PE test binary.

### Final verification

Focused Local and required Cloud/APM packages:

```text
ok  	apm/internal/cloud/local	1.381s
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	0.861s
ok  	apm/cmd/apm	0.043s
```

Race verification:

```text
ok  	apm/internal/cloud	1.014s
ok  	apm/internal/cloud/local	8.391s
```

`go vet ./internal/cloud/... ./cmd/apm` exited successfully with no output.

Full suite:

```text
ok  	apm/cmd/apm	0.065s
ok  	apm/internal/adapter	0.110s
ok  	apm/internal/cache	0.032s
ok  	apm/internal/cli	0.016s
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	1.132s
ok  	apm/internal/config	0.057s
ok  	apm/internal/domain	0.004s
ok  	apm/internal/lifecycle	3.214s
ok  	apm/internal/lock	0.006s
ok  	apm/internal/logging	0.005s
ok  	apm/internal/paths	0.006s
ok  	apm/internal/pluginartifact	0.037s
ok  	apm/internal/pluginresource	0.137s
ok  	apm/internal/transaction	0.467s
ok  	apm/internal/utils/filesystem	0.013s
ok  	apm/internal/version	0.005s
```

The stale-symbol migration search returned no matches, and `git diff --check` exited successfully with no output.

### Files and moves

- Replaced `source_linux.go` with `source_unix.go` for the shared Linux/Darwin/FreeBSD `openat` backend.
- Added `source_windows.go`, `source_bsd_test.go`, `source_windows_test.go`, and platform-neutral `source_snapshot_test.go`.
- Replaced the Linux-suffixed hard-link implementation with `link_count_unix.go`; narrowed `source_other.go` and `link_count_other.go` to unsupported targets.
- Updated `source.go`, `skill.go`, and `plugin.go` for captured required-marker validation.
- Updated `client.go` and `mcp.go` for the clone-before-final-cancellation boundary.
- Split the unprivileged subprocess test setup into Unix/Windows tagged helpers so Windows test compilation covers the new backend.

### Self-review

- Confirmed required marker validation examines only immutable snapshot nodes whose bytes/type/signature/link count were captured from verified descriptors/handles; it does not re-open marker pathnames.
- Confirmed the Unix resource root and every descendant component are opened one component at a time with `O_NOFOLLOW`, preventing ancestor symlink traversal without requiring `openat2`.
- Confirmed deterministic same-inode growth remains bounded while inode replacement fails identity comparison; publication still uses only snapshot bytes.
- Confirmed Windows rejects final and intermediate reparse-point handles, verifies final handle paths component by component, and reads/link-counts the exact accepted handle.
- Confirmed MCP's last observable test seam is after deep clone and directly before `ctx.Err()` and return.
- Confirmed Registry behavior, Local package ownership, Task 1 resolution order, Task 2 no-replace publication, size/signature limits, and cache/home isolation remain unchanged.

### Concerns

- Skill/Plugin snapshots retain the previously documented memory cost (up to the 100 MiB tree limit); the deferred snapshot-memory Minor was intentionally left unchanged.
- This worker could only cross-compile the Darwin, FreeBSD, and Windows tagged runtime tests. Linux executed the shared Unix backend tests; native BSD/Windows CI should execute their tagged tests.
- Targets other than Linux, Darwin, FreeBSD, and Windows remain fail closed because no equivalent backend is implemented for them.

## Fix Round 3

### Outcome

- Linux no longer opens an unknown directory entry with `O_RDONLY` merely to determine its type. Every entry is first pinned with `O_PATH|O_NOFOLLOW`; `fstat` rejects special nodes without invoking their data-open behavior.
- An inspected Linux regular file/directory is reopened through a verified procfs descriptor link, so the readable descriptor refers to the exact inode already confirmed as regular/directory. All readable opens include `O_NOCTTY`; regular opens also include `O_NONBLOCK` as defense in depth.
- The pathname identity is checked immediately before the pinned data open and again after it, before any read. A deterministic replacement with a FIFO/device is rejected before the data-open seam; a replacement after the pinned open is rejected before consuming bytes.
- FreeBSD keeps full Local retrieval using its documented `O_PATH` inspection descriptor and `O_EMPTY_PATH` upgrade of that exact descriptor. The constants are isolated in the FreeBSD-tagged file because x/sys v0.37.0 only generates them for one FreeBSD architecture.
- Darwin is isolated as fail closed. Its available `O_EVTONLY` descriptor is explicitly for event notification and has no documented operation to upgrade the same pinned vnode to a readable descriptor. Reopening by pathname would recreate the merge-blocking special-node race, so the weaker Round 2 implementation was removed.

Platform references used for this boundary:

- FreeBSD `open(2)`, including `O_PATH` and `O_EMPTY_PATH`: https://man.freebsd.org/cgi/man.cgi?query=open&sektion=2
- Apple `O_EVTONLY` documentation: https://developer.apple.com/documentation/system/filedescriptor/openoptions/eventonly

### TDD evidence

Primary RED command:

```bash
go test -count=1 ./internal/cloud/local -run '^TestDownloadSkillRejectsSpecialNodeSwapBeforeAnyDataOpen$'
```

Exact RED output:

```text
# apm/internal/cloud/local [apm/internal/cloud/local.test]
internal/cloud/local/source_linux_test.go:117:11: client.afterSnapshotDataOpen undefined (type *Client has no field or method afterSnapshotDataOpen)
FAIL	apm/internal/cloud/local [build failed]
FAIL
```

Reason: there was no observable separation between side-effect-free metadata inspection and a data-capable open; the old `openNodeAt` itself used `O_RDONLY|O_NONBLOCK` on the unknown node.

The same invariant was mutation-checked for the single-file MCP/Agent capture path. With the pre-open pathname identity check removed, exact RED output was:

```text
--- FAIL: TestDownloadAgentRejectsFIFOSwapBeforeAnyDataOpen (0.00s)
    source_linux_test.go:180: swapped FIFO was opened for data access
FAIL
FAIL	apm/internal/cloud/local	0.004s
FAIL
```

The check was restored, and exact focused GREEN output for both tree and single-file regressions was:

```text
ok  	apm/internal/cloud/local	0.005s
```

The Linux device subcase uses a character device with the null-device major/minor and skips only when `mknod` is denied. The FIFO subcases always run.

### Final verification

Focused Local and required Cloud/APM packages:

```text
ok  	apm/internal/cloud/local	1.374s
ok  	apm/internal/cloud	0.004s
ok  	apm/internal/cloud/local	0.890s
ok  	apm/cmd/apm	0.047s
```

Race verification:

```text
ok  	apm/internal/cloud	1.014s
ok  	apm/internal/cloud/local	8.373s
```

`go vet ./internal/cloud/... ./cmd/apm` exited successfully with no output.

Full suite:

```text
ok  	apm/cmd/apm	0.077s
ok  	apm/internal/adapter	0.132s
ok  	apm/internal/cache	0.039s
ok  	apm/internal/cli	0.018s
ok  	apm/internal/cloud	0.005s
ok  	apm/internal/cloud/local	1.140s
ok  	apm/internal/config	0.068s
ok  	apm/internal/domain	0.005s
ok  	apm/internal/lifecycle	3.287s
ok  	apm/internal/lock	0.010s
ok  	apm/internal/logging	0.008s
ok  	apm/internal/paths	0.014s
ok  	apm/internal/pluginartifact	0.065s
ok  	apm/internal/pluginresource	0.123s
ok  	apm/internal/transaction	0.462s
ok  	apm/internal/utils/filesystem	0.015s
ok  	apm/internal/version	0.005s
```

Cross-platform compile-only command exited successfully with no output:

```bash
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/apm-local-darwin.test ./internal/cloud/local
GOOS=freebsd GOARCH=amd64 go test -c -o /tmp/apm-local-freebsd.test ./internal/cloud/local
GOOS=windows GOARCH=amd64 go test -c -o /tmp/apm-local-windows.test ./internal/cloud/local
```

### Files

- `source_unix.go`: shared Linux/FreeBSD descriptor traversal, pre/post identity checks, and no-read-before-final-verification.
- `source_inspection_linux.go`: Linux `O_PATH` inspection and verified-procfs pinned reopen.
- `source_inspection_freebsd.go`: FreeBSD `O_PATH`/`O_EMPTY_PATH` pinned reopen.
- `source_other.go`, `source_bsd_test.go`, `source_darwin_test.go`: isolated Darwin fail-closed boundary and FreeBSD runtime contract.
- `client.go`, `skill.go`, `plugin.go`, `mcp.go`, `agent.go`, `source_windows.go`: private data-open seam plumbing for deterministic same-package regressions.
- `source_linux_test.go`: tree FIFO/device swap and single-file FIFO swap regressions.

### Self-review

- Confirmed no Linux code path opens an untyped source entry for read: `openInspectionAt` is the only initial node open and uses `O_PATH|O_NOFOLLOW`.
- Confirmed all Linux/FreeBSD data-capable opens operate on the already inspected descriptor, never on the mutable entry pathname.
- Confirmed the deterministic swap happens after inspection; current-path identity rejection precedes the data-open seam and leaves no destination or sibling temporary residue.
- Confirmed the post-open identity check occurs before link-count inspection, bounded read, signature validation, or publication.
- Confirmed directories use the same inspection/pinned-open pattern, so an unknown FIFO/device is never passed to a directory data open.
- Confirmed Windows continues using non-following reparse handles, and Registry/package/no-replace/cache boundaries are unchanged.
- Confirmed the deferred snapshot-memory Minor was not modified.

### Concerns

- Linux descriptor upgrade now requires `/proc/self/fd` to be mounted as genuine procfs; missing or substituted procfs fails closed with `cloud.ErrUnsafeContent`.
- FreeBSD full retrieval requires a kernel supporting `O_PATH`/`O_EMPTY_PATH`; older kernels return an open error and fail closed.
- Darwin Local source retrieval is intentionally fail closed until a readable, no-follow handle can be derived from the exact event/metadata handle without another pathname lookup.

## Fix Round 4

### Investigation and architecture

Round 3's Linux procfd concern is not reproducible. The committed
`reopenLinuxInspection` passes only `O_RDONLY`, `O_DIRECTORY` where applicable,
`O_CLOEXEC`, `O_NONBLOCK`, and `O_NOCTTY`; it does not pass `O_NOFOLLOW` when it
opens the numeric procfd entry. The focused valid-snapshot test passes. A focused
`strace` likewise records numeric procfd opens such as
`openat(procfd, "5", O_RDONLY|O_NOCTTY|O_NONBLOCK|O_CLOEXEC)`, without
`O_NOFOLLOW`. Linux therefore needs no production change for the reported
`ELOOP` premise.

Darwin's Round 3 fail-closed backend is a real functionality regression, but a
direct metadata-handle upgrade is not available to an ordinary process:

- XNU's `openbyid_np` admits only platform binaries or processes carrying the
  private `com.apple.private.vfs.open-by-id` entitlement and otherwise returns
  `EPERM`.
- `F_SETFL` cannot change descriptor access mode, `/dev/fd` duplication rejects
  a requested read mode that the original descriptor lacks, and
  `fclonefileat` explicitly requires `FREAD`. Consequently neither an
  `O_EXEC`/`O_EVTONLY` descriptor nor a duplicate/fileport can become a readable
  descriptor for the same vnode without privilege.
- Filesystem snapshots also require superuser privilege plus an entitlement.

The unprivileged backend will instead use an equivalent pinned snapshot:

1. Open the configured resource root in one atomic
   `O_DIRECTORY|O_NOFOLLOW` operation. This intentionally permits trusted
   ancestor aliases such as Darwin's `/var -> /private/var`, while rejecting a
   final-component root symlink. Resolve every descendant directory relative to
   the pinned root with `O_DIRECTORY|O_NOFOLLOW` and pre/post identity checks.
   XNU checks `O_DIRECTORY` against vnode type before `VNOP_OPEN`, so a raced
   FIFO/device is rejected before any data-capable open.
2. Inspect a non-directory candidate with `fstatat(...,
   AT_SYMLINK_NOFOLLOW)`, then atomically `linkat` that exact directory entry
   into a random, mode-0700 workspace on the same filesystem. Darwin `linkat`
   with no `AT_SYMLINK_FOLLOW` links a final symlink itself rather than following
   it, so a symlink/special-node swap cannot escape and is rejected from the
   pinned link's metadata without being data-opened.
3. Require the pinned node to be the same regular inode inspected before the
   deterministic race seam and to have link count two (the unique source name
   plus this call's private link). Only then open the private random name for
   reading, compare descriptor identity, unlink the private name, and capture
   bounded bytes into the existing immutable in-memory snapshot. A post-open
   source-identity check preserves replacement detection.
4. Unlink every private file immediately and remove the empty workspace on all
   paths. A same-filesystem writable workspace is the unavoidable cost of the
   unprivileged pinned-link mechanism; failure to create or clean it fails
   closed rather than falling back to pathname `ReadFile`/`WalkDir`.

Primary Darwin references used for this decision:

- XNU `openbyid_np`, `open1`, `linkat`, and `fclonefileat` implementations:
  https://github.com/apple-oss-distributions/xnu/blob/main/bsd/vfs/vfs_syscalls.c
- XNU open authorization (`O_DIRECTORY` type check before `VNOP_OPEN`):
  https://github.com/apple-oss-distributions/xnu/blob/main/bsd/vfs/vfs_vnops.c
- Darwin `linkat(2)` atomic/no-follow semantics:
  https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/link.2
- Darwin filesystem snapshot privilege contract:
  https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/fs_snapshot_create.2

### TDD evidence

The Darwin behavioral test was written first. It replaces the Round 3
fail-closed assertion with native-CI coverage that retrieves a valid Skill,
Plugin, MCP, and Agent through the complete `Client`, plus regressions for a
FIFO swap before the data-open boundary and a hard-linked single file. Because
Darwin binaries cannot execute on this Linux host, the exact shared backend is
also exercised against a real filesystem by Linux-only tests.

The first RED run, before the backend existed, was:

```text
$ go test -count=1 ./internal/cloud/local -run '^TestLinkSnapshotBackend'
internal/cloud/local/source_link_snapshot_linux_test.go:22:19: undefined: capturePhysicalTreeLinkSnapshot
internal/cloud/local/source_link_snapshot_linux_test.go:32:22: undefined: capturePhysicalFileLinkSnapshot
internal/cloud/local/source_link_snapshot_linux_test.go:47:12: undefined: capturePhysicalTreeLinkSnapshot
internal/cloud/local/source_link_snapshot_linux_test.go:110:17: undefined: capturePhysicalFileLinkSnapshot
internal/cloud/local/source_link_snapshot_linux_test.go:122:75: undefined: linkSnapshotWorkspacePrefix
FAIL apm/internal/cloud/local [build failed]
```

The Darwin test independently failed to compile before production support:

```text
$ GOOS=darwin GOARCH=amd64 go test -c -o /tmp/apm-local-darwin.test ./internal/cloud/local
internal/cloud/local/source_darwin_test.go:122:75: undefined: linkSnapshotWorkspacePrefix
```

After the initial implementation, a new read-only-root regression exposed an
incorrect `FileInfo.Sys()` platform assumption:

```text
--- FAIL: TestLinkSnapshotBackendUsesWritableSameFilesystemParentForReadOnlyRoot
    capture from read-only resource root: permission denied: cloud unsafe content
```

Using `fstat` on both pinned directory descriptors made that regression green.
A configured-root regression through a symlinked ancestor then exposed an
overly strict component-by-component absolute-root walk:

```text
--- FAIL: TestLinkSnapshotBackendPinsConfiguredRootAcrossSymlinkedAncestor
    capture through configured root ancestor: open physical resource root ".../alias/resources": invalid argument: cloud unsafe content
```

Opening the trusted configured root atomically, while retaining no-follow
descriptor traversal below it, made the test green. The complete focused set
then passed:

```text
$ go test -count=1 ./internal/cloud/local -run '^TestLinkSnapshotBackend'
ok  apm/internal/cloud/local  0.015s
```

A mutation check removed the pinned-inode equality predicate. The regular-file
replacement regression failed exactly at its intended assertion:

```text
--- FAIL: TestLinkSnapshotBackendRejectsRegularSwapBeforeDataOpen
    capturePhysicalTreeLinkSnapshot() error = <nil>, want ErrUnsafeContent
```

Restoring the predicate returned the focused test to green:

```text
$ go test -count=1 ./internal/cloud/local -run '^TestLinkSnapshotBackendRejectsRegularSwapBeforeDataOpen$'
ok  apm/internal/cloud/local  0.004s
```

Self-review identified a second functional race: two valid captures of the same
unique file could enter the hard-link window together and transiently observe a
link count above two. A regression written before the fix failed with:

```text
--- FAIL: TestLinkSnapshotBackendSerializesConcurrentPinning
    source_link_snapshot_linux_test.go:258: concurrent capture entered the same hard-link pinning window
```

The backend now serializes only the short stat/link/open/verify/unlink phase and
refreshes pathname identity after acquiring that lock; bounded reads occur
after unlocking. The normal and race-focused tests then passed:

```text
ok  apm/internal/cloud/local  0.108s
ok  apm/internal/cloud/local  1.128s
```

### Implementation

- `source_link_snapshot_unix.go` contains the Darwin link-snapshot backend and
  is compiled on Linux as well so its filesystem behavior can be executed in
  this environment. It pins the configured root and descendant directories,
  creates a private same-filesystem workspace, atomically hard-links only the
  exact inspected entry, verifies type/device/inode/link count before any data
  open, re-verifies after open, unlinks before bounded reading, and fails closed
  on cleanup errors.
- `source_darwin.go` restores Darwin's `capturePhysicalTree` and
  `capturePhysicalFile` entry points. `source_other.go` continues to fail closed
  for unsupported exotic targets.
- `source_darwin_test.go` provides native Darwin behavioral coverage for all
  four Local resource kinds and the unsafe-node boundary.
- `source_link_snapshot_linux_test.go` provides executable coverage for valid
  tree/single-file capture, a read-only resource root with a writable
  same-filesystem parent, configured-root ancestor aliases, FIFO and regular
  replacement races, concurrent pinning, hard links, symlinks, and workspace
  cleanup.

No Linux production snapshot path was changed; Linux continues to use its
existing `O_PATH` inspection/procfd reopen implementation.

### Final verification

Fresh focused Linux and shared-backend tests:

```text
$ go test -count=1 ./internal/cloud/local -run '^TestOpenatSnapshotBackendDownloadsValidSkillWithoutOpenat2$'
ok  apm/internal/cloud/local  0.005s

$ go test -count=1 ./internal/cloud/local -run '^TestLinkSnapshotBackend'
ok  apm/internal/cloud/local  0.107s

$ go test -count=1 -timeout=120s ./internal/cloud/local
ok  apm/internal/cloud/local  1.016s
```

Package, race, vet, and full-repository gates:

```text
$ go test -count=1 ./internal/cloud/... ./cmd/apm
ok  apm/internal/cloud        0.004s
ok  apm/internal/cloud/local  1.023s
ok  apm/cmd/apm               0.050s

$ go test -race -count=1 ./internal/cloud/...
ok  apm/internal/cloud        1.015s
ok  apm/internal/cloud/local  8.255s

$ go vet ./internal/cloud/... ./cmd/apm
# exit 0, no output

$ go test -count=1 ./...
# all packages passed; apm/internal/cloud/local 1.069s
```

Formatting and patch hygiene passed with no output:

```bash
test -z "$(gofmt -l internal/cloud/local/source_darwin.go internal/cloud/local/source_darwin_test.go internal/cloud/local/source_link_snapshot_linux_test.go internal/cloud/local/source_link_snapshot_unix.go internal/cloud/local/source_other.go)"
git diff --check
```

All compile-only platform checks exited zero with no output:

```bash
GOOS=darwin GOARCH=amd64 go test -c -o /tmp/apm-local-round4-darwin-amd64.test ./internal/cloud/local
GOOS=darwin GOARCH=arm64 go test -c -o /tmp/apm-local-round4-darwin-arm64.test ./internal/cloud/local
GOOS=freebsd GOARCH=amd64 go test -c -o /tmp/apm-local-round4-freebsd-amd64.test ./internal/cloud/local
GOOS=windows GOARCH=amd64 go test -c -o /tmp/apm-local-round4-windows-amd64.test ./internal/cloud/local
GOOS=linux GOARCH=arm64 go test -c -o /tmp/apm-local-round4-linux-arm64.test ./internal/cloud/local
```

`go tool nm` on the Darwin amd64 binary confirmed that all three new
Darwin-tagged behavioral tests are present. Migration scans found no stale
`NewLocalClient`/`LocalClient` Go symbols and no pathname `ReadFile`, `Open`, or
`WalkDir` in the Darwin snapshot backend.

### Self-review and concerns

- The only initial data-capable opens are directory opens with
  `O_DIRECTORY|O_NOFOLLOW`; XNU rejects a non-directory vnode before its open
  operation. Unknown file candidates are inspected and hard-linked without
  following the final component, and only a private name whose regular-file
  type and device/inode match are opened for data.
- Pre-existing hard links remain rejected. In-process concurrent captures are
  serialized only while the temporary link exists; cross-process overlap can
  still fail closed transiently rather than accepting an ambiguous link count.
- Darwin needs permission to create a mode-0700 workspace either beside or
  inside the resource root on the same filesystem, and permission to hard-link
  each uniquely linked source file. Read-only/global layouts without such a
  writable same-filesystem location fail closed with `cloud.ErrUnsafeContent`.
- Native Darwin execution is delegated to CI because this host is Linux. Both
  Darwin architectures compile, the tests are linked into the Darwin binary,
  and the identical backend is executed on Linux, but this run cannot replace
  native APFS/XNU execution evidence.
- Marker checks, cancellation, binary/archive rejection, size/node/depth/path
  limits, atomic no-replace publication, Registry selection, and unsupported
  target fail-closed behavior remain covered by the existing untagged suite or
  unchanged platform-specific code.

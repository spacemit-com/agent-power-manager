# Feature 018 Task 9 Report: Register Bibit and Integrate Skill/MCP Lifecycle

## Status

Task 9 implementation is complete. The built-in Registry now registers Bibit's profile and its already-separated filesystem Skill, dedicated MCP, and filesystem Plugin adapters. Installed snapshots include Bibit by profile-root detection and remain sorted by Agent ID.

Planned commit message: `feat(agent): register bibit power adapters`

## TDD evidence

### RED

Tests were added before production registration. The required RED command exited 1:

```text
go test -count=1 ./internal/adapter ./internal/lifecycle -run 'Test(Registry.*Bibit|Bibit.*Skill|Bibit.*MCP)'
```

The intended missing behavior was observed:

- `TestRegistryResolvesAllBibitPowerAdapters` failed because `ResolveSkill("bibit")` returned `ErrUnsupportedAdapter`.
- `TestBibitSkillAdapterInstallsUnderIsolatedAgentRoot` failed at the same missing Registry wiring.
- `TestBibitMCPIntegrationFixUsesResolvedFakeExecutablePath` failed because Bibit was absent from the installed Registry snapshot.

### GREEN

After the minimal Bibit profile/factory registration, the same command exited 0:

```text
ok apm/internal/adapter
ok apm/internal/lifecycle
```

The first complete scoped run identified a fake-contract mismatch: shell `echo` appended a newline to Bibit's exact absent diagnostic. The existing Task 2 Adapter contract explicitly rejects extra stderr, so the fake was corrected to use no-newline `printf`; no MCP production protocol was changed. The focused Bibit install/upgrade/uninstall subtest and the complete scoped packages then exited 0.

## Changed behavior

- Registered `bibit` with `BIANBU_AGENT_HOME`, default root `bianbu-agent`, and no-op Skill adaptation.
- Wired Bibit Skill to the common profile-driven filesystem adapter.
- Wired Bibit MCP to the dedicated `newBibitMCPAdapter`; no Codex MCP implementation or command builder is shared.
- Wired Bibit Plugin to `newBibitPluginAdapter`, retaining filesystem deployment and no marketplace capability.
- Kept support and installed detection profile-based. Registry construction never probes for `bianbu-agent`; only actual MCP calls resolve the CLI.
- Kept installed snapshots sorted as `[bibit, claude-code, codex, hermes-agent]`.
- Extended all-Agent install, uninstall, and installed-list ordering tests to include Bibit.

## Isolation and fake CLI guarantees

- Bibit root override, default root, missing root, and non-directory root are tested with `t.TempDir()` and `APM_HOME`/`BIANBU_AGENT_HOME`.
- Bibit Skill publication targets only `<temporary-bibit-root>/skills/demo`; staging and all checked Agent roots remain temporary.
- The MCP harness creates `bianbu-agent` only under `<t.TempDir>/fake-bin`, places that directory first on its isolated PATH, and keeps APM home, cache, Agent homes, state, manifests, and journal under the same temporary harness root.
- Every recorded CLI call compares `$0` for exact equality with the absolute `<t.TempDir>/fake-bin/<expected-executable>` path. Bibit therefore must execute the fake absolute `bianbu-agent` path; a PATH escape or different executable fails the test.

## Verification gates

The following fresh commands all exited 0:

```text
go test -count=1 ./internal/adapter ./internal/lifecycle
go test -race -count=1 ./internal/adapter ./internal/lifecycle
go vet ./internal/adapter ./internal/lifecycle
go test -count=1 ./...
git diff --check
```

## Files and scope

Production:

- `internal/adapter/registry.go`
- `internal/adapter/agent_profile.go`

Tests:

- `internal/adapter/registry_test.go`
- `internal/adapter/agent_profile_test.go`
- `internal/adapter/skill_filesystem_test.go`
- `internal/lifecycle/mcp_integration_test.go`
- `internal/lifecycle/install_test.go`
- `internal/lifecycle/uninstall_test.go`
- `internal/lifecycle/list_installed_test.go`

Report:

- `.superpowers/sdd/plan/task-9-report.md`

The only fixture update is the temporary MCP harness in `mcp_integration_test.go`, which adds a fake `bianbu-agent`, a temporary Bibit root, and exact absolute-executable assertions. No Cloud/resource fixture changed.

Task 10's complete four-Agent Plugin acceptance, Task 11 documentation, and Changelog are not included.

## Self-review

- Registry capability boundaries are explicit: Codex/Claude remain marketplace Plugin adapters; Hermes/Bibit remain filesystem Plugin adapters.
- Registration order is not used as execution order; `RefreshInstalled` clones and sorts Agent IDs before detection.
- Bibit support is determined only from the configured/default root being a directory. Missing roots are absent, while non-directory roots fail closed with `ErrAgentNotInstalled`.
- The Bibit MCP factory remains independent from Codex even though their current ideal command contracts are similar.
- MCP install, upgrade, uninstall, read-only journal preservation, and Fix use only fake executables and temporary state. Fix performs exactly one recovery inspection and commits the Bibit manifest without changing the already-present MCP state.
- ADR-014 already records the separate-Adapter and shared-artifact decision; Task 9 introduces no new major decision requiring another ADR.

# Feature 014 Verification

- Date: 2026-08-13
- Result: Passed
- Decision: [ADR-012](../../adr/ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md)

## Acceptance evidence

| Acceptance criterion | Behavioral evidence | Implementation boundary |
| --- | --- | --- |
| Cloud List exposes versioned marketplace entries as logical refs | `TestLocalClientListsCommittedSkillFixture`, `TestLocalClientListsPluginsFromClaudeMarketplaceOnly` | `internal/cloud/local_plugin.go`, `.claude-plugin/marketplace.json` |
| `name=version` supports every marketplace version | `TestPluginIntegrationMarketplaceAgentVersionLifecycleIsIsolated`, `TestPluginIntegrationHermesVersionLifecycleIsIsolated` | existing CLI/version selector plus `domain.ActualPluginName` |
| Codex/Claude add once and update the named marketplace | `TestCodexPluginPrepareInstallCommands`, `TestClaudePluginPrepareInstallCommands` | `plugin_codex.go`, `plugin_claude.go` |
| Source and unmanaged-target conflicts fail closed | `TestPluginIntegrationRejectsUnmanagedTargetAndMarketplaceConflict`, Adapter conflict tables | `ErrMarketplaceConflict`, `ErrPluginConflict`, `ErrTargetConflict` |
| Hermes conditionally uses Cache/Staging and versioned directories | `TestPluginInstallFilesystemPublishesThenEnables`, `TestPluginInstallFilesystemUpdateQuarantinesPrevious`, Hermes integration | `plugin_filesystem.go`, `cache/filesystem.go` |
| Three Agents support install, unchanged, upgrade, downgrade and uninstall | `TestPluginIntegrationMarketplaceAgentVersionLifecycleIsIsolated`, `TestPluginIntegrationHermesVersionLifecycleIsIsolated` | production Local Cloud/Cache/Registry/Transaction/Lifecycle composition |
| Disabled still counts as installed; new APM installs enable | `TestCodexPluginInspectAndMutations`, `TestClaudePluginInspectAndMutations`, `TestHermesPluginInspectIgnoresStatus`, filesystem recovery test | Agent-specific Plugin Adapters |
| Uninstall is offline and removes only Plugin manifest state | `TestUninstallPluginUsesLocalActualNameWithoutCloud`, `TestPluginUninstallMarketplaceMissingRemovesOnlyManifest`, `TestPluginIntegrationUninstallRepairsMissingTargetManifest` | `uninstall_plugin.go`, `plugin_uninstall.go` |
| Plugin shares one recoverable journal with Skill/MCP | `TestPluginIntegrationSearchRecoversInterruptedJournal`, `TestPluginRecoveryMarketplaceAddingNewPresentCommits`, rollback/uninstall recovery tests, `TestPluginRecoveryFilesystemCommittingRejectsMissingInstallTarget`, `TestPluginRecoveryFilesystemRemovingOldReenablesUnmovedPreviousTarget`, `TestPluginRecoveryFilesystemUninstallReenablesUnmovedTarget`, existing MCP/Skill recovery suites | schema v1 `DriverPlugin` routing in `recovery.go` |
| Every malformed journal maps to `ErrInvalidJournal` | `TestValidatePluginJournalEnvelope`, `TestValidatePluginFilesystemJournals`, existing strict decode/schema tests | `transaction/validation.go` |
| Tests never touch real Agent environments | Plugin integration asserts every executable path is under `t.TempDir()/fake-bin`; all Agent homes, APM home and cache are temporary; no call contains `/home/liangff/.local/bin/hermes` | `plugin_integration_test.go` |
| Multi-Agent operations stop after the first failure | `TestPluginIntegrationMultiAgentInstallFailsFast` | shared lifecycle batch orchestration |
| Skill and MCP regressions remain green | full suite plus `MCPIntegration` and race suite | existing Skill/MCP packages unchanged except shared runner/interface extensions |
| Module boundaries remain type-specific | lifecycle branches only by Power type; transaction branches by deployment mode; Agent-specific argv/JSON remains in Adapter files | source files listed in `spec.md` |

## Verification commands

The following commands completed with exit code 0 on 2026-08-13:

```text
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./internal/domain ./internal/cloud ./internal/cache ./internal/adapter ./internal/transaction ./internal/lifecycle ./internal/cli ./cmd/apm
go test -count=1 ./...
go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
! rg -n 'Plugin (不受支持|仍明确不支持|unsupported)' BUILD.md ivory-tower/project-overview.md ivory-tower/c4 --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'ResolvePlugin|DriverPlugin|GetPlugin|ADR-012' internal BUILD.md ivory-tower --glob '*.go' --glob '*.md' --glob '*.mmd' --glob '*.dsl'
rg -n 'SchemaVersion:\s*1|SchemaVersion != 1' internal/transaction internal/lifecycle --glob '*.go'
! rg -n 'SchemaVersion:\s*2|SchemaVersion != 2' internal --glob '*.go'
```

No required Plugin test was skipped. Integration uses real production composition with fake external CLIs; no real Agent smoke test is part of this feature.

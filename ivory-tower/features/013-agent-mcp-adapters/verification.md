# Feature 013 验收审计

审计日期：2026-08-12。状态仅使用 `proved` 或 `missing`；最终审计没有 `missing`。

| Spec requirement | Evidence | Status |
| --- | --- | --- |
| 目标：三 Agent MCP 安装、卸载、升级、清单和中断恢复 | `TestMCPIntegrationInstallUpgradeUninstallForEveryAgent`、`TestMCPIntegrationSearchRecoversInterruptedAgentCLIJournal` | proved |
| 范围与非目标 / 包含项 | `internal/domain/mcp.go`、`internal/cloud/local_mcp.go`、`internal/adapter/mcp_*.go`、`internal/transaction/mcp_*.go`、`internal/lifecycle/*_mcp.go` | proved |
| 范围与非目标 / 不包含 Plugin、并行事务、外部并发等 | `TestInstallPluginDoesNotDispatchThroughSkillAdapter`、`TestUninstallPluginDoesNotDispatchThroughSkillAdapter`；唯一 `JournalStore`；spec 保留明确非目标 | proved |
| 用户命令与既有生命周期 | `cmd/apm/main.go` 四个 factory；`TestDefaultLifecycleFactoriesConstructWithMCPRecoveryResolver` | proved |
| 领域模型 / MCP 启动定义判别联合 | `TestMCPServerValidateAcceptsOnlyStdioOrHTTP`、`TestMCPServerCloneDoesNotShareArguments` | proved |
| Cloud JSON 严格字段与定义互斥 | `TestLocalClientGetMCPRejectsInvalidResourceDefinitions` | proved |
| Cloud 边界：GetMCP 精确版本且不写 Cache/Staging | `TestLocalClientGetMCPReadsExactVersionWithoutCacheMaterialization`、`assertMCPOnlyPathsAbsent` | proved |
| 类型专属 Adapter 与 Registry / 接口分离 | `MCPAdapter`、`AdapterRegistry.ResolveSkill/ResolveMCP`；`TestRegistryResolvesBuiltInSkillAdaptersAndRefreshesInstalledAgentsInAgentIDOrder` | proved |
| Registry RefreshInstalled 不调用 MCP CLI | `Registry.RefreshInstalled` 只遍历 `skillByAgent`；`registry_test.go` | proved |
| Agent CLI 共同执行规则：动态 PATH、无 shell、30 秒、输出有界且截断可见 | `TestDefaultMCPRunnerUsesLookPathAndPreservesArgvAndStdin`、`TestMCPRunnerTimesOutAndBoundsOutput`、三个 Inspect 的 `truncated valid` 用例 | proved |
| Agent CLI 共同执行规则：取消、不可用、非零退出 fail closed | `TestMCPRunnerHonorsCanceledParentContext`、`TestMCPRunnerReportsUnavailableBeforeStarting`、`TestMCPRunnerCapturesNonzeroExitWithoutDiscardingDiagnostics` | proved |
| CLI 映射 / Codex stdio、HTTP、Remove | `TestCodexMCPAdapterBuildsExactMutationArgv` | proved |
| CLI 映射 / Claude Code stdio、HTTP、Remove，固定 user scope | `TestClaudeMCPAdapterUsesOnlyUserScope` | proved |
| CLI 映射 / Hermes stdio、HTTP、Remove | `TestHermesMCPAdapterUsesExactArgvAndScriptedInput`、`TestHermesMCPAdapterOmitsArgsFlagWhenEmpty` | proved |
| Inspect：所有检查通过目标 Agent CLI，不读配置文件 | 三个 `mcp_*.go` 的 `Inspect` 与 `TestMCPIntegrationInstallUpgradeUninstallForEveryAgent` | proved |
| Codex Inspect 严格结构化名称 | `TestCodexMCPInspectRequiresExactStructuredName` | proved |
| Claude Inspect 要求精确名称与 User scope | `TestClaudeMCPInspectRejectsNonUserOrUnclearScope` | proved |
| Hermes Inspect 严格表格解析 | `TestHermesMCPInspectStrictlyParsesListTable` | proved |
| Hermes 非交互固定 stdin、Saved/Removed 标记、正常写后跳过 list | `TestHermesMCPAdapterUsesExactArgvAndScriptedInput`、`TestHermesMCPMutationRequiresExplicitSuccessMarker` | proved |
| Manifest 纳管与漂移：只写精确 MCP PowerRef | `TestMCPIntegrationInstallUpgradeUninstallForEveryAgent` | proved |
| Manifest 纳管与漂移：只删除对应 MCP 清单项 | `TestMCPInstallUpdateStaleManifestRemovesOnlyMatchingEntry`、集成子用例 `stale manifest removes one entry` | proved |
| Install 顶层单锁、fail-fast 和结果语义 | 原有 `install_test.go` 全量回归；`TestInstallMCPFirstInstallOrdersCLIAndCloudWithoutCache` | proved |
| Install 表：无记录 + absent → GetMCP + 首装 | `TestInstallMCPFirstInstallOrdersCLIAndCloudWithoutCache` | proved |
| Install 表：无记录 + present → 清单外冲突且不读取定义 | `TestInstallMCPStateTableAvoidsUnneededCloudDefinitions/unmanaged_present_conflict`、集成 `unmanaged conflict` | proved |
| Install 表：同版本 + present → unchanged | `TestInstallMCPStateTableAvoidsUnneededCloudDefinitions/same_present_unchanged` | proved |
| Install 表：同版本 + absent → 单条 stale 修复 | `TestInstallMCPStateTableAvoidsUnneededCloudDefinitions/same_absent_stale` | proved |
| Install 表：不同版本 + present → 先旧后新定义再切换 | `TestInstallMCPUpdateFetchesOldThenNewAndClassifies` | proved |
| Install 表：不同版本 + absent → 单条 stale 修复 | `TestInstallMCPStateTableAvoidsUnneededCloudDefinitions/different_absent_stale` | proved |
| Install 表：Inspect error → fail closed | `TestInstallMCPDefinitionAndInspectFailuresStopBeforeTransaction/inspect` | proved |
| Transaction 安装前再次 Inspect 并防御竞态 | `TestMCPInstallPreflightBranchesDoNotCreateJournal`、`TestMCPInstallUpdateStaleManifestRemovesOnlyMatchingEntry` | proved |
| Uninstall 业务流程：无清单不解析 Adapter/Cloud | `TestUninstallMCPMissingManifestStopsBeforeAdapterCloudAndTransaction` | proved |
| Uninstall present：精确 GetMCP 后事务删除 | `TestUninstallMCPPresentGetsExactDefinitionAndCompletes` | proved |
| Uninstall absent：不 GetMCP，单项修复与 TargetMissing Warn | `TestUninstallMCPAbsentSkipsCloudAndWarns`、`TestMCPUninstallAbsentRemovesOnlyManifestEntry` | proved |
| Uninstall error 与 TargetMissing 可并存并在 Release 前 Warn | `TestUninstallMCPMissingFactWithErrorWarnsBeforeRelease`、`TestMCPUninstallAbsentPreservesFactWithManifestError` | proved |
| 唯一活动 journal：共享信封 + 类型载荷 | `Journal`、`FilesystemJournalPayload`、`AgentCLIJournalPayload` | proved |
| Journal schema v1 归一化为 v2 filesystem | `TestDecodeJournalConvertsLegacyFilesystemSchemaToV2` | proved |
| Journal v2 必须显式 driver 且只有匹配载荷 | `TestValidateJournalAcceptsOnlyOneMatchingV2DriverPayload` | proved |
| Journal 未知字段、schema、trailing JSON fail closed | `TestDecodeJournalRejectsUnknownV2FieldsAndTrailingJSON`、`filesystem_journal_store_test.go` | proved |
| TransactionManager 类型专属接口 | `Manager.InstallMCP`、`Manager.UninstallMCP`、`MCPAdapterResolver` | proved |
| MCP 首装阶段 adding-new → committing/rolling-back | `TestMCPInstallFirstInstallOrderAndClonesServer`、`TestMCPInstallFirstAddMutationStates` | proved |
| MCP 更新阶段 removing-old → adding-new → committing/rolling-back | `TestMCPInstallUpdateOrder`、`TestMCPInstallUpdateFailureDirections` | proved |
| applied + error 固定已发生方向；unknown 保留 journal | `TestMCPInstallFirstAddMutationStates/applied_plus_error`、`TestMCPInstallUpdateFailureDirections/remove_unknown` | proved |
| MCP 卸载阶段 removing → committing/rolling-back | `TestMCPUninstallPresentOrder`、`TestMCPUninstallMutationDirections` | proved |
| 提交期 manifest/journal 错误保留 committing journal | `TestMCPInstallCommitFailuresPreserveRecoverableJournal`、`TestMCPUninstallCommitFailuresPreserveJournal` | proved |
| 跨类型串行恢复：filesystem 不依赖 MCP resolver | `TestCrossTypeRecoveryKeepsFilesystemDriverIndependentOfMCPResolver` | proved |
| 跨类型串行恢复：后续 Search 恢复 MCP journal | `TestMCPIntegrationSearchRecoversInterruptedAgentCLIJournal` | proved |
| MCP 恢复：首次 adding-new present/absent | `TestMCPRecoveryPhaseTable/first_add_present_commits`、`first_add_absent_rolls_back` | proved |
| MCP 恢复：更新 removing-old present/absent | `TestMCPRecoveryPhaseTable/update_remove_old_present_rolls_back`、`update_remove_old_absent_restores` | proved |
| MCP 恢复：更新 adding-new present/absent | `TestMCPRecoveryPhaseTable/update_add_new_present_commits`、`update_add_new_absent_restores` | proved |
| MCP 恢复：卸载 removing present/absent | `TestMCPRecoveryPhaseTable/uninstall_remove_present_rolls_back`、`uninstall_remove_absent_commits` | proved |
| MCP 恢复：committing 不依赖 CLI/Cloud | `TestMCPRecoveryCommittingDoesNotRequireCLI` | proved |
| MCP 恢复：rolling-back 恢复旧状态 | `TestMCPRecoveryRollingBackRestoresRequiredOldState` | proved |
| MCP 恢复：resolver/Inspect 不可用保留 journal | `TestMCPRecoveryInspectAndResolverFailuresRetainJournal` | proved |
| 错误与日志：不确定状态、MCP 冲突、非法定义具有稳定分类 | `adapter/errors.go`、`transaction/errors.go`；lifecycle、transaction 与集成冲突测试断言 `ErrMCPConflict` | proved |
| 错误与日志：外部输出有界、参数不写 journal | `mcp_runner.go`、`AgentCLIJournalPayload` 只保存定义而不保存 CLI 路径/argv | proved |
| 测试与验收 / 真实环境隔离 | `newIsolatedMCPHarness` 设置五个 Home/Cache 变量、临时 PATH/fake-bin；`assertOnlyFakeExecutables` | proved |
| 测试与验收 / Cloud 与领域模型 | `internal/domain/mcp_test.go`、`internal/cloud/local_mcp_test.go` | proved |
| 测试与验收 / Adapter 与 CLI | `internal/adapter/mcp_*_test.go`、`mcp_runner_test.go` | proved |
| 测试与验收 / Lifecycle 与 Manifest | `install_mcp_test.go`、`uninstall_mcp_test.go`、`mcp_integration_test.go` | proved |
| 测试与验收 / Transaction 与恢复 | `mcp_install_test.go`、`mcp_uninstall_test.go`、`mcp_recovery_test.go` | proved |
| 测试与验收 / 最终验证 | `test -z "$(gofmt -l cmd internal)"`、全量测试、四包 race、vet、build 与 `git diff --check` 均以退出码 0 完成；结果记录在 Changelog | proved |
| 风险与控制 | 有界 runner、严格 parser、journal 保留、无 post-write list 与隔离集成测试均已实现 | proved |
| 文档与决策 | ADR-010、Feature 013 spec/plan、本审计、C4、BUILD 与历史 supersession notes | proved |

结论：零个 `missing`，Feature 013 的批准范围均有源代码或自动化测试证据。

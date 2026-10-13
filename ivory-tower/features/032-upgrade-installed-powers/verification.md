# Feature 032：验证记录（`apm upgrade`）

- Status：Accepted
- Date：2026-10-13
- Related ADRs：[ADR-045](../../adr/ADR-045-add-upgrade-command-for-installed-powers.md)
- Spec：[spec.md](spec.md)｜Plan：[plan.md](plan.md)

## 1. 自动化证据

| 目标 | 测试 |
| --- | --- |
| planner 决策表：更高/相等/更低/无候选/旧版本下架/多类型同名/排序 | `TestPlanUpgradeDecidesEveryManifestAgainstLatestCatalogVersion`、`TestPlanUpgradeOrdersTypesAndKeepsManifestOrder`、`TestCatalogSnapshotDecodeKeepsPerTypeGroupsIndependent` |
| planner `--type` 过滤与空清单 | `TestPlanUpgradeFiltersByRequestedTypes`、`TestPlanUpgradeHandlesEmptyAndDuplicateManifestEntries` |
| planner 纯函数：不改输入、确定性 | `TestPlanUpgradeDoesNotMutateManifestGroups`、`TestPlanUpgradeIsDeterministic` |
| planner 选择器失败降级为跳过 | `TestPlanUpgradeTreatsSelectorFailureAsSkip` |
| 服务：升级到最新版本 | `TestUpgradeInstallsLatestVersionForInstalledPower` |
| 服务：幂等，up-to-date 不触碰事务与内容边界 | `TestUpgradeReportsUpToDateWithoutTouchingContentBoundaries` |
| 服务：云端无候选跳过且不建事务 | `TestUpgradeSkipsAndWarnsWhenCatalogHasNoCandidate` |
| 服务：只升不降 | `TestUpgradeSkipsNewerLocalVersionWithoutDowngrading` |
| 服务：`--dry-run` 纯计划 | `TestUpgradeDryRunPlansWithoutTouchingLocalState` |
| 服务：单条失败继续下一条 | `TestUpgradeContinuesAfterSingleFailure` |
| 服务：残留 journal 立即停止并补 `unexecuted` | `TestUpgradeStopsImmediatelyOnResidualJournal`、`TestUpgradeStopsWhenInspectReportsActiveJournal` |
| 服务：空快照成功空结果 | `TestUpgradeEmptySnapshotSucceedsWithEmptyResult` |
| 服务：具体 Agent / expert 校验 | `TestUpgradeRejectsUnknownConcreteAgent`、`TestUpgradeRejectsExpertForNonBibitAgent` |
| 服务：清单不可读为 fatal | `TestUpgradeFailsFastWhenAgentManifestIsUnreadable` |
| 服务：Release 失败抑制结果 | `TestUpgradeSuppressesResultWhenReleaseFails` |
| 服务：多 Agent 按快照顺序 | `TestUpgradeMultiAgentProcessesInSnapshotOrder` |
| 服务：依赖 nil 校验 | `TestNewUpgradeRejectsNilDependencies` |
| 服务：`--type` 过滤只处理该类别 | `TestUpgradeTypeFilterOnlyProcessesRequestedType` |
| CLI：默认 all、flag 转发、`--type=agent` 别名规范化 | `TestUpgradeDefaultsAgentToAllAndForwardsType`、`TestUpgradeForwardsConcreteFlags`、`TestUpgradeNormalizesLegacyAgentTypeAlias` |
| CLI：位置参数与非法 flag 在 factory 前拒绝 | `TestUpgradeRejectsPositionalArgumentsBeforeFactory`、`TestUpgradeRejectsInvalidFlagsBeforeFactory` |
| CLI：factory nil / typed-nil | `TestUpgradeRequiresLifecycleFactory`、`TestUpgradeRejectsTypedNilLifecycle` |
| CLI：batch 失败先渲染再非零退出；fatal 不渲染 | `TestUpgradeRendersBatchFailureThenReturnsNonZero`、`TestUpgradeSuppressesBusinessLinesOnFatalError` |
| CLI：文本行与 JSON 契约 | `TestRenderUpgradeWritesExactOutcomeLines`、`TestRenderUpgradeJSONWritesStableRecords` |
| CLI：写入错误包装并保留已写前缀 | `TestRenderUpgradeWrapsWriteFailureAndPreservesFirstLine` |
| CLI：安装结果如实映射 | `TestUpgradeOutcomeFromInstallKeepsInstallSemantics` |
| CLI：批处理错误聚合与 `errors.Is` | `TestUpgradeBatchErrorUnwrapPreservesEveryCause` |
| 帮助列表包含 upgrade | `TestCLIHelpListsCommandsOnStdout` |
| 组合根：Upgrade 只开一个 Cloud Client | `TestUpgradeCompositionOpensOneCloudClientPerInvocation` |
| 端到端：安装旧版后 upgrade 到最新版 | `TestNewUpgradeLifecycleUpgradesBundledSkillToLatest` |
| 端到端：dry-run 不改清单/不建事务 | `TestNewUpgradeLifecycleDryRunLeavesLocalStateUntouched` |
| 端到端：无已安装 Power 成功空结果 | `TestNewUpgradeLifecycleSucceedsWithNothingInstalled` |

`internal/cli`、`internal/lifecycle`（除下列环境阻塞用例）、`cmd/apm`（除下列环境阻塞用例）
全部通过；`gofmt`、`go vet ./...`、`go build ./...` 无输出。

## 2. 环境阻塞（非本特性引入）

在当前沙箱（euid 1000，但 `/` 被报告为 uid 65534）中，需要「受信目录校验」或真实 remote
Cloud 的用例无法运行：`internal/catalog`、`internal/cloud`、`internal/cloud/remote` 的契约
测试，以及 `internal/lifecycle`、`cmd/apm` 中依赖 bundled local Cloud / remote catalog 的用例。

已用 `git stash -u` 在干净工作树上复跑同一批用例，失败集合与本改动后**完全一致**；本改动仅新增
了 3 个同因阻塞的端到端用例（`TestNewUpgradeLifecycle*`），未引入任何新的类别差异。这些用例需要
在具备正常目录属主权限的环境（或板端）复跑。

## 3. 未覆盖项

- 真实 remote Cloud 下的端到端 upgrade（受沙箱网络与目录信任限制）。
- 板端 Bibit 的 expert upgrade 真实安装（需 `bianbu-agent-runtime`）。
- `--prune`、`list --outdated`、`--to/--patch/--minor` 版本策略：明确不在本特性范围（见 spec §2.2）。

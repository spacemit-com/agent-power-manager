# Feature 032：实施计划（`apm upgrade`）

- Status：Accepted
- Date：2026-10-13
- Related ADRs：[ADR-045](../../adr/ADR-045-add-upgrade-command-for-installed-powers.md)
- Spec：[spec.md](spec.md)

## 0. 交付顺序

1. `internal/cli` 契约层与命令层。
2. `internal/cli/root.go` 接线。
3. `internal/lifecycle/upgrade_plan.go` 纯函数 planner。
4. `internal/lifecycle/upgrade.go` 服务。
5. `cmd/apm/main.go` 组合根。
6. 端到端测试与文档。

每步都保持 `go build ./...` 与 `go test ./...` 绿。

## 1. CLI 契约层 — `internal/cli/upgrade_contract.go`（新增）

```go
// UpgradeOutcome 表示单条已安装 Power 的一次升级可见结果。
type UpgradeOutcome string

const (
	UpgradeOutcomeUpgraded   UpgradeOutcome = "upgraded"
	UpgradeOutcomeUpToDate   UpgradeOutcome = "up-to-date"
	UpgradeOutcomeSkipped    UpgradeOutcome = "skipped"
	UpgradeOutcomeFailed     UpgradeOutcome = "failed"
	UpgradeOutcomeUnexecuted UpgradeOutcome = "unexecuted"
)

// UpgradeItem 表示一个 Agent 上一条已安装 Power 的升级结果。
// Current 来自本地清单，Target 来自同一目录快照的计划目标版本。
type UpgradeItem struct {
	Agent   domain.AgentID
	Current domain.PowerRef
	Target  domain.PowerRef
	Outcome UpgradeOutcome
	Skip    string // not-in-catalog / newer-local；非 skipped 时为空
}

// UpgradeResult 表示 CLI 可直接渲染的升级业务结果集合。
type UpgradeResult struct {
	Items  []UpgradeItem
	DryRun bool
}

// UpgradeFailure 表示一条 Power 的升级失败及底层原因；单条失败不中断整批。
type UpgradeFailure struct {
	Agent domain.AgentID
	Power domain.PowerRef
	Err   error
}

// UpgradeBatchError 表示本批存在一条或多条失败。
type UpgradeBatchError struct {
	Failures []UpgradeFailure
}

func (e *UpgradeBatchError) Error() string
func (e *UpgradeBatchError) Unwrap() []error // 返回全部底层原因，支持 errors.Is

// UpgradeLifecycle 定义 upgrade 命令唯一需要的生命周期入口。
type UpgradeLifecycle interface {
	Upgrade(context.Context, Flags) (UpgradeResult, error)
}

// UpgradeLifecycleFactory 在日志初始化后创建 Upgrade 生命周期。
type UpgradeLifecycleFactory func() (UpgradeLifecycle, error)
```

要点：
- `UpgradeBatchError.Error()` 汇总 `Failures`，格式 `upgrade failed for N power(s): agent "x" power "y": cause; ...`。
- `Unwrap() []error` 而非 `Unwrap() error`：多条失败需同时可被 `errors.Is` 命中（对齐
  `cli.PartialListError` 的既有做法）。

## 2. CLI 命令层 — `internal/cli/upgrade.go`（新增）

```go
func newUpgradeCommand(newLifecycle UpgradeLifecycleFactory) *cobra.Command {
	var flags Flags
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade all installed powers to the latest versions",
		Args:  cobra.ExactArgs(0),
		RunE:  func(command *cobra.Command, args []string) error { ... },
	}
	command.Flags().StringVar(&flags.Agent, "agent", "", "target agent ID or all (defaults to all)")
	bindTypeFlag(command, &flags)
	command.Flags().BoolVar(&flags.DryRun, "dry-run", false, "print the upgrade plan without making changes")
	command.Flags().BoolVar(&flags.JSON, "json", false, "machine-readable JSON output")
	return command
}
```

`RunE` 顺序：
1. `resolvePowerType(&flags)`（必须在读取 `flags.Type` 之前，见 ADR-044）。
2. `validateAgentValue(flags)`。
3. `isExpertType(flags.Type)` 时走 `validateExpertTypeFlags(&flags)`（只允许空或 `bibit`）。
4. `flags.Agent` 为空 → 就地设为 `domain.AllAgents`，让 lifecycle 与 install 的 all 语义对齐。
5. `logParsedCommand(command, flags, args)`。
6. factory nil / typed-nil 检查（同 install 的文案）。
7. 调 `service.Upgrade` 并渲染：
   - `err == nil` → `renderUpgrade(stdout, result, flags)`。
   - `err` 是 `*UpgradeBatchError` → 先 `renderUpgrade`，再返回 `&runtimeFailure{cause: err}`；
     render 失败则 `errors.Join`。
   - 其它 err → `&runtimeFailure{cause: err}`。

注意：**不复用 `validateCommon`**（它对空 `--agent` 报 required）；upgrade 的空值语义是 all。

## 3. CLI 渲染层 — `internal/cli/upgrade_render.go`（新增）

- `renderUpgrade(writer, result, flags)`：`flags.JSON` 时走 JSON，否则文本。
- 文本行：`<agent>/<type>/<name>@<target-version> <outcome>`，`skipped` 时追加 ` skip=<reason>`；
  行格式与 [install_render.go](../../../internal/cli/install_render.go) 保持一致，仅多出 skip 后缀。
- JSON 记录类型：

```go
// upgradeJSON 是 upgrade --json 的机器记录；字段恒定存在，空值为 ""。
type upgradeJSON struct {
	Agent          string `json:"agent"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version"`
	TargetVersion  string `json:"target_version"`
	Outcome        string `json:"outcome"`
	Skip           string `json:"skip"`
}
```

- 使用 `json.NewEncoder` + `SetIndent("", "  ")`，与 `listPowerJSON` 一致。
- 写入错误按既有惯例包装：`fmt.Errorf("write upgrade result: %w", err)` /
  `fmt.Errorf("encode upgrade JSON: %w", err)`。

## 4. `Flags` 扩展 — `internal/cli/flags.go`（修改）

```go
// DryRun 仅用于 upgrade：输出计划而不落盘。
DryRun bool
```

只加字段，不改既有校验函数。

## 5. 根命令接线 — `internal/cli/root.go`（修改）

- `CLIEntry` 加 `newUpgradeLifecycle UpgradeLifecycleFactory`。
- `New(...)` 末尾追加 `upgradeFactories ...UpgradeLifecycleFactory`，取 `[0]`（沿用
  `showFactories` 先例，避免改动既有 10 处调用点）。
- `newRootCommand(...)` 加对应参数，`AddCommand` 注册 `newUpgradeCommand(newUpgradeLifecycle)`。

## 6. 纯函数计划器 — `internal/lifecycle/upgrade_plan.go`（新增）

```go
// upgradePlanAction 表示计划对一条已安装 Power 采取的本地动作。
type upgradePlanAction int

const (
	planUpgrade  upgradePlanAction = iota // 执行安装到 target
	planUpToDate                          // 已是最新，无需动作
	planSkip                              // 有意不动，原因见 reason
)

// upgradePlanItem 是计划中的一条；entry 仅在 planUpgrade 时有效。
type upgradePlanItem struct {
	agent   domain.AgentID
	current domain.PowerRef
	target  domain.PowerRef
	entry   domain.CatalogEntry
	action  upgradePlanAction
	reason  string // not-in-catalog / newer-local
}

// planUpgrade 是纯函数：不执行 I/O，输入单 Agent 的清单分组与已验证的不可变目录快照，
// 按固定类型顺序与清单原序输出计划。
func planUpgrade(agent domain.AgentID, groups domain.InstalledPowerGroups,
	catalogSnapshot catalog.Snapshot, selector VersionSelector,
	types []domain.PowerType) []upgradePlanItem
```

实现要点：
- 类型顺序固定 `skill → plugin → mcp → expert`（与 `installedPowerTypes` 一致），组内保持
  manifest 原序，输出确定。
- 每个 `(type, name)`：
  - 从 `catalogSnapshot.List(powerType)` 收集同名候选版本。
  - 无候选，或**当前版本不在候选中**：`planSkip` + `not-in-catalog`。
  - 否则 `selector.Select(candidates, "")` 得 latest（复用 [selector.go](../../../internal/version/selector.go)，
    不自己写 semver 比较）。
  - `target == current`：`planUpToDate`。
  - `target < current`：`planSkip` + `newer-local`。
  - `target > current`：`planUpgrade` 并带上 `catalogSnapshot.Resolve(target)` 的完整 `CatalogEntry`。
- 同名重复清单条目各自成条（与 [ADR-007](../../adr/ADR-007-use-stateless-config-repository.md) 的
  「重复记录合法」一致），由执行期 install 的 `Set` 归并语义收敛。

## 7. 生命周期服务 — `internal/lifecycle/upgrade.go`（新增）

```go
// UpgradeService 实现 Upgrade 命令在单个全局锁区间内的全量升级生命周期。
type UpgradeService struct {
	locker       lock.Locker
	transactions transaction.Manager
	catalogs     CatalogManager
	cloud        cloud.Client
	cache        cache.PowerCache
	plugins      pluginresource.Manager
	registry     AdapterRegistry
	config       config.Repository
	versions     VersionSelector
}

func NewUpgrade(locker, transactions, catalogs, cloud, cache, plugins, registry, config, versions) (*UpgradeService, error)
```

`NewUpgrade` 的 nil 校验与 `NewInstall` 逐项对齐（不同文案 `create Upgrade lifecycle with nil ...`）。

`Upgrade(ctx, flags)` 流程：

```text
Acquire                                    // 失败 → "acquire upgrade lock: %w"
→ transactions.Recover                     // 失败 → releaseUpgrade 包装后返回
→ logUpgradeRecovery
→ registry.RefreshInstalled                // 失败 → releaseUpgrade
→ upgradeAgents(flags, snapshot)           // 空 --agent 已由 CLI 置为 all
→ catalogs.SnapshotLocked                  // 唯一快照；失败 → releaseUpgrade
→ warnStaleCatalog("upgrade", status)
→ 逐 agent：config.List(agent) → planUpgrade(...) 汇总为有序计划
→ flags.DryRun：Release 后返回 UpgradeResult{Items, DryRun: true}
→ 逐条执行计划：
     · planUpToDate / planSkip → 直接构造结果项（skip 记一次 Warn）
     · planUpgrade → installForAgent(ctx, agent, entry, snapshot)
          - 成功 → Outcome = upgraded（updateItem 以 item.Power 为 Target）
          - 失败 → failed + UpgradeFailure；随后检查 journal：
               errors.Is(err, transaction.ErrActiveTransaction) 或 Inspect().Active
               → 立即停止，剩余计划项记 unexecuted
               → 否则继续下一条
→ Release                                  // 失败 → 返回零结果并 errors.Join
→ 有失败 → 返回 (result, &cli.UpgradeBatchError{Failures})
```

`upgradeAgents` 规则：
- `--type=expert`：只返回 `bibit`；bibit 不在快照时报错（与 install 文案一致）。
- `--agent=all`：返回 `snapshot.Ordered()`；**空快照不报错**，返回空切片（与 install 的
  `no supported installed agents` 不同，见 ADR-045）。
- 具体 agent：不在快照时报 `upgrade powers for agent %q: unsupported or not installed` +
  `availableAgentSuffix`。

journal 残留判据：新增小函数在每次 `installForAgent` 失败后调用 `transactions.Inspect`，把
「事务仍存在」与业务失败区分开；`ErrActiveTransaction` 是直接判据，Inspect 是兜底。

## 8. 组合根 — `cmd/apm/main.go`（修改）

- 抽出共享依赖装配 helper，供 install 与 upgrade 复用，避免复制 40 行：

```go
func newInstallLifecycleWithCatalogAndCloudFactories(...) // 保持签名不变
func newUpgradeLifecycleWithCatalogAndCloudFactories(...) (cli.UpgradeLifecycle, error)
```

- `catalogLifecycleComposition` 加：

```go
func (composition *catalogLifecycleComposition) newUpgradeLifecycle() (cli.UpgradeLifecycle, error) {
	return newUpgradeLifecycleWithCatalogAndCloudFactories(
		composition.manager, newCloudClient,
		func(source cache.Source) (cache.PowerCache, error) { return cache.NewFilesystem(source) },
		newPluginResources,
	)
}
```

- `main()` 的 `cli.New` 追加 `catalogLifecycles.newUpgradeLifecycle`。
- 保留 `newUpgradeLifecycleWithCloudFactories` seam，供组合根的「同一 Cloud Client」复验。

## 9. 测试计划

### 9.1 `internal/cli`（package `cli_test`）

`upgrade_test.go`：
- 位置参数被拒（`accepts 0 arg(s)`）、`--type` 非法、`--agent` 非法。
- 空 `--agent` 被改写为 `all` 后才进 lifecycle（用记录 flags 的 fake 断言）。
- `--type=expert` + `--agent=codex` 在校验阶段失败，factory 不被调用。
- factory 为 nil 与 typed-nil 的报错。
- `UpgradeBatchError` 时先渲染全部行再非零退出；fatal error 不渲染。
- nil factory + 合法参数 → `upgrade lifecycle factory is required`。

`upgrade_contract_test.go`（package `cli`）：
- `renderUpgrade` 文本行精确匹配（含 `skip=` 后缀）。
- `--json` 记录形状、字段恒存在、缩进。
- `--dry-run` 只输出计划行。
- 写入错误在第 N 次写入时包装并保留已写前缀。

`cli_test.go`：`TestCLIHelpListsCommandsOnStdout` 的期望命令列表加 `upgrade`。

### 9.2 `internal/lifecycle`

`upgrade_plan_test.go`：planner 决策表全覆盖 —— 更高/相等/更低/不在 catalog/当前版本不在
快照/`--type` 过滤/空清单/重复条目/多类型同名/排序稳定性。

`upgrade_test.go`：扩展既有 `installFixture`（[install_test_helpers_test.go](../../../internal/lifecycle/install_test_helpers_test.go)）
而非新建 fake 体系：
- `installFixture` 加 `installedGroups map[domain.AgentID]domain.InstalledPowerGroups` 与
  `configListErrors map[domain.AgentID]error`；
- `installFakeConfig.List` 改为返回 `fixture.installedGroups[agent]`（当前恒返回 nil）；
- 覆盖：默认 all 的字典序、`--type` 过滤、`--dry-run` 零事务零下载（断言 calls 无
  `transaction:*` 与 `resource:ensure:*`）、journal 残留立即停止并补 `unexecuted`、单条失败
  继续、Release 失败抑制结果、空快照成功空结果、expert 仅 bibit。

保持既有 install 测试全部通过：`installFakeConfig.List` 的改动对 install 路径无影响（install
不使用 List）。

### 9.3 `cmd/apm`

`main_test.go` 新增：
- bundled local cloud：`install foo-before-summary=v0.0.1` → `upgrade` → 断言 `upgraded` 到
  `v0.1.0`、manifest 更新、磁盘 marker 更新、`active.json`/staging 被清理。
- 幂等：再次 `upgrade` → 全 `up-to-date`，且无新增缓存下载。
- `--dry-run`：不改 manifest、无 journal、无 staging。
- `newUpgradeLifecycleWithCloudFactories` 的 Cloud factory 只被调用一次。

### 9.4 验证命令

```bash
gofmt -l ./cmd ./internal
go vet ./...
go build ./...
go test ./...
```

## 10. 风险与应对

| 风险 | 应对 |
| --- | --- |
| MCP 版本切换依赖旧版本 metadata | planner 把「当前版本不在快照」归为 `not-in-catalog` 跳过，前置可读原因 |
| Plugin 旧版本制品必须仍在本地 Cache | 该条走 `failed` 且不中断整批；错误文案保留既有原因 |
| 计划与执行目录漂移 | plan 与 execute 共用同一个 `SnapshotLocked` 结果与同一份 `CatalogEntry` |
| journal 残留后续跑不安全 | 每次失败后判 `ErrActiveTransaction`/Inspect，命中即停并提示 `apm fix` |
| `installFakeConfig.List` 改动影响既有测试 | install 路径不使用 `List`；改后全量跑 `go test ./...` 复验 |
| `--json` 成为长期契约 | 字段与空值策略写入 ADR-045 与 spec §3.3，一次定型 |
| `cli.New` 签名扩展 | 用 variadic 参数，既有调用点零改动 |

## 11. 文档

- `ivory-tower/adr/ADR-045-add-upgrade-command-for-installed-powers.md`（已创建）。
- `ivory-tower/features/032-upgrade-installed-powers/{spec,plan,verification}.md`。
- `BUILD.md`：命令清单加 `apm upgrade` 示例，新增一节说明升级语义、跳过原因与输出格式，引用 ADR-045。
- `ivory-tower/project-overview.md`：命令面与生命周期列表补 `upgrade`。
- `docs/Changelog.md`：新增条目（分类：新命令 + 契约新增），引用 ADR-045 与 Feature 032。
- `debian/changelog`：不新增版本段（当前 0.0.2 段含未提交的 plugin 版本修订）。

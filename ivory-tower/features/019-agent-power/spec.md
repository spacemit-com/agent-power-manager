# Agent Power（Bibit 专属）设计规格

> 状态与取代说明：本文主体保留为 Feature 019 的历史设计记录；其中 Cloud provider 契约已由 [Feature 020](../020-cloud-client-providers/spec.md) 与 [ADR-016](../../adr/ADR-016-select-cloud-clients-with-explicit-providers.md) 取代，Plugin canonical/派生契约已由 Feature 020 与 [ADR-017](../../adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 取代。
>
> 状态：设计已确认，待编写实现计划；重大决策将记录于 [ADR-015](../../adr/ADR-015-agent-power-bibit.md)。

## 背景

APM 已为 Codex、Claude Code、Hermes 与 Bibit 提供 Skill、MCP、Plugin 的统一 lifecycle，均以 `PowerType`（skill/plugin/mcp）组织、以版本化目录提供 Cloud 资源，并以可恢复 transaction + manifest 管理安装状态。

Bianbu（Bibit）支持以 yaml 文件定义「子 Agent」。本 feature 新增第四种 Power 类型 `agent`：由 `agent.yaml` 配置的 Bibit 子代理，安装到 `<bibit根>/agents/<agent.id>/agent.yaml`。该类型仅适用于 Bibit，且安装/卸载需支持 `--type agent` 省略 `--agent bibit` 的默认语义。

同时修正 Bibit 资源根：现有 `defaultRoot: "bianbu-agent"` 应为 `~/.bianbu-agent`（隐藏目录）。

## 目标

- 新增 `PowerType agent`，值 `"agent"`。
- Cloud 资源布局对齐 skill：`resources/agents/<name>/<version>/agent.yaml`，版本目录为版本真相。
- 仅 Bibit 支持 agent；`--type agent` 时 `--agent` 默认 `bibit`、拒绝其它（含 `all`）。
- 安装路径 `<bibit根>/agents/<agent.id>/agent.yaml`（单文件、本地不版本化，版本仅记 manifest）。
- 下载单元为单个 `agent.yaml`，走版本化缓存 + staging（与 skill 同构）。
- 安装/卸载/升级/降级/fix 由可恢复 filesystem transaction + manifest 管理。
- 修正 Bibit 默认根为 `~/.bianbu-agent`。

## 不在范围内

- 探测或调用真实 `bianbu-agent` CLI、真实 home。
- 解析/校验 agent.yaml 中 id/version/schema_version 之外的字段（name/model/tools/skills/mcps/hooks/metadata 原样透传）。
- 交叉校验 id/version 与目录名的一致性（Cloud 保证资源对齐，下载即指定版本）。
- 解释 `metadata.is_default` 等业务语义。
- cache refresh/TTL/GC、旧 cache/journal 自动迁移。
- 让 agent 适用于 Bibit 以外的 Agent。

## 领域模型

`internal/domain`：

- `power.go` 增加 `PowerTypeAgent PowerType = "agent"`。
- 新增最小 `AgentConfig`（yaml 标签）：

```go
type AgentConfig struct {
    SchemaVersion int       `yaml:"schema_version"`
    Agent         AgentInfo `yaml:"agent"`
}

type AgentInfo struct {
    ID      string `yaml:"id"`
    Version string `yaml:"version"`
}
```

`AgentConfig.Validate()` 校验：`schema_version == 1`、`id` 为安全单路径段（新增 `isSafeAgentID`，语义同 `isSafeSkillName`）、`version` 精确 `vMAJOR.MINOR.PATCH`。其余字段不解析。

`PowerRef.Name == agent.id`；`PowerRef.Version == 版本目录名`（Cloud 保证对齐）。

## Cloud 资源

`internal/cloud`：

- `List()` 额外枚举 `agents/*/v*/agent.yaml`（标记文件 `agent.yaml`，与 skill 的 `SKILL.md` 同构）：产出 `PowerRef{Name: 顶层目录名, Type: agent, Version: 版本目录名}`；不解析 yaml 内部 id/version。
- 新增 `DownloadAgent(PowerRef) error`：把 `resources/agents/<name>/<version>/agent.yaml` 原子复制到 `cache/agents/<name>/<version>/agent.yaml`（命中幂等返回）；不做内容解析（与 `DownloadSkill` 一致）。

示例布局：

```text
internal/cloud/resources/agents/
└── bianbu-agent-default/
    └── v1.0.0/
        └── agent.yaml
```

## 缓存与 staging

`internal/cache`：

- `PowerCache` 增加 `StageAgent(PowerRef) (string, error)`：`cache/agents/<name>/<version>/agent.yaml` → `staging/agents/<name>/agent.yaml`（单文件，镜像 `StageSkill`）。

## Adapter

`internal/adapter`：

- 新增 `AgentAdapter`：

```go
type AgentAdapter interface {
    TargetPath(domain.PowerRef) (string, error)
    Install(domain.PowerRef, string) (AgentInstallResult, error)
}
```

- `bibitAgentAdapter`（仅 Bibit 注册；`Registry.ResolveAgent` 对其它 Agent 返回 `ErrUnsupportedAdapter`）：
  - `TargetPath(ref)` = `<bibit根>/agents/<agent.id>/agent.yaml`（`agent.id` 取 yaml 解析结果，安全名校验）。
  - `Install(ref, staging)`：校验 staging 身份 → 读取 `staging/agents/<name>/agent.yaml` 并 `AgentConfig.Validate()` → 取 `agent.id` 作本地目录名 → 确保 bibit 根存在 → 创建 `agents/<id>/` → 临时文件 + rename 原子发布**原样字节** → 清理 staging → `Published=true`。
- `Registry` 增加 `ResolveAgent(AgentID)`。

## 事务与恢复

`internal/transaction`：

- 新增 `AgentInstallRequest/AgentUninstallRequest` + `InstallAgent/UninstallAgent`（镜像 skill，校验 `Type==agent`、调用 `AgentAdapter`）。
- journal 复用 `DriverFilesystem` + `FilesystemJournalPayload`（target 为**文件**路径）。
- `validateFilesystemJournal` 放宽为 `skill|agent`。
- 恢复：现有 `recoverSkill` 纯按路径收敛、不依赖具体 adapter，泛化为「文件系统驱动」后 agent 免费复用；`fix` 无需新 driver。
- 卸载：移除 `agent.yaml` 后裁剪空的 `agents/<id>/` 与空的 `agents/` 父目录。

## 配置清单

`internal/config`：

- `manifestDocument` 增加 `Agents []powerEntry`（JSON 字段 `agents`，顺序排在 `skills` 后）。
- `emptyPowerGroups` / `manifestToPowerGroups` / `powerGroupsToManifest` / `clonePowerGroups` / `validPowerType` / `validatePowerGroups` 全量补 `agent` 分组。

## 生命周期与 CLI 规则

`internal/lifecycle` + `internal/cli`：

- install/uninstall 分派（`installForAgent`/`uninstallForAgent`）增加 `agent` 分支；`AdapterRegistry` 增加 `ResolveAgent`。
- `validatePowerType` 白名单加 `agent`；`bindTypeFlag` 文案更新。
- flag 规则（`--type agent`，适用于需要 `--agent` 的命令：install、uninstall、`list --installed`）：`--agent` 空 → 默认 `bibit`；`--agent bibit` → 允许；`--agent` 其它值（含 `all`）→ 报错。
- `search`/`list --type agent`（Cloud 列表）仅过滤结果，不要求 `--agent`。
- 版本解析走既有 selector（候选为版本目录名）；`--version` 不符按既有「not found」语义。
- 五个命令（search/list/install/uninstall/fix）均支持 `agent`。

## Bibit 根目录修正

- `registry.go` 中 Bibit `defaultRoot` 由 `"bianbu-agent"` 改为 `".bianbu-agent"`。
- `BIANBU_AGENT_HOME` 环境变量名、CLI 可执行名 `bianbu-agent` 不变。
- skills/plugins 默认根随之变为 `~/.bianbu-agent/…`。
- 同步更新 `registry_test.go`、`agent_profile_test.go`、集成测试及 `BUILD.md`、`ivory-tower/project-overview.md`、feature 018 中的 `~/bianbu-agent` 断言。

## 错误处理

- agent 目标 Agent 非 Bibit → 报错（`--agent` 规则 + `ResolveAgent`）。
- agent.yaml 缺失/非法/`schema_version != 1`/id 不安全/version 非规范 → fail closed。
- 目标 `agents/<id>/agent.yaml` 已存在且非受管 → 冲突（沿用 `ErrTargetConflict`）。
- 已存在同名同版本 → unchanged（manifest 判定）。
- bibit 根不存在 → 未安装（不进入快照/显式选择报错）。

## 测试与验收

- Cloud：`List` 枚举 agent、`DownloadAgent` 单文件复制、命中幂等、缺失/非法 marker。
- Cache：`StageAgent` 单文件物化、miss 语义。
- Adapter：单文件发布、安全路径、原样字节、bibit 根缺失、非法 yaml、目标冲突。
- Transaction：install/uninstall/升级/降级/rollback/fix 中断恢复（文件级 target）。
- Lifecycle/CLI：`--type agent` 默认 bibit、拒绝其它 Agent（含 all）、search/list 过滤、五个命令。
- Config：`agents` 分组 round-trip、版本校验。
- Bibit 根：`.bianbu-agent` 路径断言。
- 隔离：所有 Agent home/APM home/cache/staging 位于 `t.TempDir()`，禁止调用真实 `bianbu-agent`。
- 门禁：`gofmt`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、临时目录构建、`git diff --check`。

## 文件布局计划

### 新增

- `internal/domain/agent_config.go`（+ 测试）：`AgentConfig`、`isSafeAgentID`、`Validate`。
- `internal/adapter/agent.go`、`bibit_agent.go`（+ 测试）：`AgentAdapter` 接口与 Bibit 实现。
- `internal/transaction/agent_install.go`、`agent_uninstall.go`、`agent_validation.go`（+ 测试）。
- `internal/cloud/local_agent.go`（+ 测试）：agent 枚举与下载。
- `internal/lifecycle` 的 agent power install/uninstall 文件（避免与现有 `install_agent.go` 分派文件重名）。
- `ivory-tower/features/019-agent-power/spec.md`：本规格。

### 修改

- `internal/domain/power.go`：`PowerTypeAgent`。
- `internal/adapter/registry.go`：Bibit `defaultRoot` 修正、`ResolveAgent`、注册 Bibit agent adapter。
- `internal/cloud/client.go`：`List` 含 agent、`DownloadAgent` 接口。
- `internal/cache/cache.go`、`filesystem.go`：`StageAgent`。
- `internal/lifecycle/dependencies.go`、分派与 flag 处理文件：agent 分支与 `--agent` 规则。
- `internal/config/manifest.go`、`json_repository.go`：`agents` 分组。
- `internal/cli/flags.go`、`root.go`：`--type agent` 白名单与文案。
- `BUILD.md`、`ivory-tower/project-overview.md`、相关 C4/文档、feature 018 引用：`.bianbu-agent` 与 agent power。

## 迁移策略

不迁移旧 cache/journal；`agents` 为新增分组，旧 manifest 缺失该分组按空数组处理（与现有空分组语义一致）。Bibit 根由 `bianbu-agent` 改 `.bianbu-agent`，运行时不自动搬迁旧目录。

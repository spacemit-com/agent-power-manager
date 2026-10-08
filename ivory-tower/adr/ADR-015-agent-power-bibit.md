# Agent Power（Bibit 专属）与 Bibit 根目录修正

- Status：Accepted
- Date：2026-08-20
- Decision Makers：APM maintainers

> 拼写说明（2026-10-08）：本文出现的 `--type agent` 是改名前的写法，现在规范拼写为
> `--type expert`（旧值仍被接受，非 `--json` 时会提示弃用）。类别改名与保留清单见
> [ADR-044](ADR-044-rename-agent-power-type-to-expert.md)。

> 局部取代说明：本文保留 ADR-015 落定时的历史决策；其中 Agent 以单个 `agent.yaml` 安装到 `<bibit根>/agents/<agent.id>/agent.yaml`、并把 `agent.id` 作为外部 PowerRef name 的安装机制，已由 [Feature 026](../features/026-agent-package-assets/spec.md) 和 [ADR-023](ADR-023-package-agent-with-assets.md) 取代（Agent 改为按 `agent.name` 发布的完整目录 `<bibit根>/agents/<agent.name>/`，根含 `agent.yaml` 与可选 `assets/`，云端内容为确定性 `.tar.gz`），当前实现与接口以 Feature 026/ADR-023 为准；本决策的 Bibit 专属边界、默认根修正与 filesystem transaction 复用中未被修改的部分继续有效。

## Context

APM 已为 Codex、Claude Code、Hermes 与 Bibit 提供 Skill、MCP、Plugin 的统一 lifecycle，均以 `PowerType`（skill/plugin/mcp）组织、以版本化目录提供 Cloud 资源、以可恢复 transaction + manifest 管理安装状态。

Bianbu（Bibit）支持以 yaml 文件定义「子 Agent」。这需要新增第四种 Power 类型 `agent`，由 `agent.yaml` 配置、仅适用于 Bibit。同时，现有 Bibit profile 的 `defaultRoot: "bianbu-agent"`（无前导点）与其真实配置根 `~/.bianbu-agent`（隐藏目录）不符，需要一并修正。

本决策关联 [Feature 019](../features/019-agent-power/spec.md)，并继续遵守 ADR-010（类型专属 driver）、ADR-011（唯一 schema v1 journal）与 ADR-009（集中事务 + 全局锁）。

## Decision

新增 `PowerType agent`（值 `"agent"`），仅 Bibit 适用，并让 Cloud 布局与 skill 完全对齐：

```text
resources/agents/<name>/<version>/agent.yaml   # 版本目录为版本真相
cache/agents/<name>/<version>/agent.yaml       # 版本化缓存（单文件）
staging/agents/<name>/agent.yaml               # 公共 staging（单文件）
<bibit根>/agents/<agent.id>/agent.yaml          # 本地安装（单文件、不版本化）
```

- 版本真相为版本目录名（`PowerRef.Version`）；Cloud 保证资源对齐，APM 不交叉校验 id/version 与目录名。
- 下载单元为单个 `agent.yaml`，走版本化缓存 + staging（与 skill 同构）；`DownloadAgent` 只做字节复制，不解析内容。
- 安装路径 `<bibit根>/agents/<agent.id>/agent.yaml`，本地不版本化，版本仅记 APM manifest。
- APM 只解析 agent.yaml 的 `schema_version`、`agent.id`、`agent.version` 用于命名与校验；其余字段（model/tools/skills/mcps/hooks/metadata）原样字节写入，不重序列化。
- 新增 Bibit 专属 `AgentAdapter`（`TargetPath` + `Install` 单文件原子发布）；`ResolveAgent` 对其它 Agent 返回不支持。
- 事务复用 `DriverFilesystem` + `FilesystemJournalPayload`（target 为**文件**路径），新增 `InstallAgent/UninstallAgent`，并把文件系统 journal 校验从 `== skill` 放宽为 `skill|agent`；`fix` 恢复逻辑纯按路径收敛，agent 免费复用。
- `--type agent` 需要 `--agent` 的命令（install、uninstall、`list --installed`）：`--agent` 空 → 默认 `bibit`；`--agent bibit` → 允许；其它值（含 `all`）→ 报错。`search`/`list --type agent` 仅过滤结果。

同时修正 Bibit `defaultRoot` 由 `"bianbu-agent"` 改为 `".bianbu-agent"`；`BIANBU_AGENT_HOME` 环境变量名与 CLI 可执行名 `bianbu-agent` 不变，skills/plugins 默认根随之变为 `~/.bianbu-agent/…`。

## Consequences

- 正面影响：agent 完全复用 skill 的版本目录、缓存/staging、filesystem transaction 与 fix 恢复模型，新概念最少。
- 正面影响：单文件、原样字节写入，APM 不耦合 agent schema 的后续演化。
- 正面影响：Bibit 根目录与其真实配置根一致。
- 代价与风险：Bibit 根由 `bianbu-agent` 改 `.bianbu-agent` 是破坏性修正，运行时不自动搬迁旧目录。
- 代价与风险：agent 仅 Bibit 适用，`--type agent` 的 `--agent` 默认/拒绝规则是新增的 CLI 特殊分支。
- 后续动作：Feature 019、Project Overview、BUILD、C4 与 Changelog 引用本 ADR；实现阶段按仓库惯例使用 worktree 隔离。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 把 agent 伪装成 skill 复用 `PowerTypeSkill` | 语义错误（agent.yaml 无 SKILL.md/frontmatter），云资源与校验模型不同。 |
| 每次直接下载、不进缓存 | 与 skill 的幂等/离线缓存语义不一致。 |
| 本地安装路径也版本化 | 与 skill 本地 `skills/<name>/` 不版本化、版本仅记 manifest 的模型不一致。 |
| 解析并重序列化完整 agent.yaml | 丢失注释/未知字段，且 APM 无需耦合完整 schema。 |
| 保留 `defaultRoot: "bianbu-agent"` | 与 Bibit 真实配置根 `~/.bianbu-agent` 不符。 |

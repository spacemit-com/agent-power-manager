# Generate Per-Plugin Local Marketplaces

- Status：Superseded by ADR-014
- Date：2026-08-17
- Decision Makers：APM maintainers

> 已由 [ADR-014](ADR-014-share-multi-agent-plugin-artifacts.md) 取代：Cloud 与持久缓存改为每个 Plugin 版本一份共享多 Agent 制品，Plugin 运行时名称和 selector 改为逻辑名称；以下正文保留为历史决策上下文。

## Context

[ADR-012](ADR-012-manage-versioned-plugins-through-marketplace-and-filesystem.md) 曾假定 Cloud 能托管 Codex 与 Claude Code 可注册的稳定全量 marketplace。实际 Agent Power Cloud 以按 Agent 区分的原始 Plugin 文件提供资源，不能承担 marketplace 的生成、版本聚合或 source 治理。继续把远程 marketplace 作为安装前提，会使 Cloud 资源形态、缓存归属和中断恢复与实现脱节。

Plugin 仍须保持既有逻辑名称与精确版本的用户体验，并与 Skill、MCP 共用全局锁、manifest、唯一 schema v1 journal 和 `fix` 恢复入口。Codex 与 Claude Code 要求原生 marketplace 才能通过 CLI 安装；Hermes 继续使用版本化目录与 enable/disable。版本切换必须在进程中断后可仅依赖本地状态继续收敛，不能在 recovery 时重新连接 Cloud 或临时生成恢复材料。

## Decision

Cloud 只负责发现逻辑 `PowerRef`，并按 `agent + ref` 将原始 Plugin 文件下载到调用方指定目录；Cloud 不保存、解析或返回 marketplace。原始包不得携带 Codex 或 Claude marketplace 文件。

APM 的 `pluginresource` 为每个 `agent + logical plugin + version` 构建并原子发布不可变 artifact：

```text
cache/plugins/<agent>/<logical-name>/<version>/
├── .agents/plugins/marketplace.json       # 仅 Codex
├── .claude-plugin/marketplace.json        # 仅 Claude Code
└── plugins/<logical-name>-v<version>/     # 原始 Plugin 内容
```

Codex 与 Claude Code 各自从其原生 Plugin manifest 生成一份只含当前一个版本的本地 marketplace。marketplace 固定命名为 `apm-<logical-name>`，版本不进入名称；source 是 artifact 根的绝对 clean 路径。两个 builder 是并列的 Agent 专属实现。Hermes 不生成 marketplace；它使用相同 agent-aware artifact 键，并在发布前由 `StageHermes` 物化无版本 staging。

Install 和 Uninstall 以 APM manifest 为第一事实来源。Install 已记录目标精确版本时直接返回 `unchanged`，不得访问 Cloud、Plugin Resource、Adapter 或 Transaction。缺失或版本不同的目标可用 `Ensure` 下载并构建新 artifact；旧版本以及卸载所需材料只能用离线 `Lookup` 复验，缺失、损坏、部分或身份不一致的状态均保留 manifest 并 fail closed。没有 manifest 的 Agent Plugin 或 marketplace 不被接管、修复或删除。

Codex/Claude 的 marketplace 注册、Plugin 增删、manifest 写入与 rollback 属于同一个 `plugin` transaction。首装按“注册 marketplace、添加 Plugin、提交 manifest”执行；版本切换按“删除旧 Plugin、注销旧 marketplace、注册新 marketplace、添加新 Plugin、提交 manifest”执行。每次 Agent 写操作前持久化 journal phase；recovery 只使用 journal、Adapter、manifest 和不可变 artifact，绝不连接 Cloud 或 Plugin Resource。正常卸载只移除 manifest 指向的精确 Plugin，保留 marketplace 与 artifact。Transaction 依赖 Adapter、Journal 与 Manifest，不依赖 Cloud 或 Plugin Resource。

Plugin Resource 与 Transaction recovery 共同依赖中立、只读的 Plugin Artifact Validator。Validator 对完整后代树拒绝 symlink 与特殊文件，精确校验 Agent 原生 manifest，并按闭集规则要求 Codex 仅有其生成 marketplace、Claude Code 仅有其生成 marketplace、Hermes 不含 marketplace；Codex/Claude 的 marketplace 文档还必须与 name、version 和本地 source 语义一致。Recovery 只复验既定 commit/rollback 方向实际需要的 target 或 previous artifact，既不通过 Plugin Resource 间接连接 Cloud，也不下载、生成或修改缓存。

## Consequences

- 正面影响：Cloud 的责任收敛为原始资源交付，Codex/Claude 可从本地、可复验的 Agent 专属 artifact 安装，Hermes 保持 filesystem 部署。
- 正面影响：每个逻辑 Plugin 的 marketplace 独立，切换一个 Plugin 不会重建或影响同一 Agent 的其他 Plugin；历史版本保留为离线降级与恢复材料。
- 正面影响：marketplace 与 Plugin 的联合 journal 让首装、升级、降级和回滚具有明确、可恢复的事务边界。
- 正面影响：同一个中立 Validator 让 cache hit 与离线 recovery 使用一致的完整 artifact 证据，同时保持 Transaction 与 Plugin Resource/Cloud 解耦。
- 代价与风险：缓存不会自动刷新、回收或迁移；缓存损坏、source 冲突和 Agent 事实不完整都会 fail closed，需要保留现场并人工处理。
- 后续动作：Feature 017、项目概览、BUILD 与 C4 图以本 ADR 为当前语义；ADR-012 保留为被取代的历史记录。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Cloud 继续提供稳定全量远程 marketplace | Cloud 的实际资源形态是原始 Agent 文件；同时聚合全部 Plugin/版本会把发布与恢复责任推回 Cloud。 |
| 每 Agent 使用一个聚合本地 marketplace | 任一 Plugin 变化都要重建无关 Plugin 的索引，缓存与 rollback 会跨越无关资源。 |
| 每版本使用一个 marketplace 名称 | 会累积注册项，并绕过同名 marketplace 的受控切换与恢复语义。 |
| 在 transaction/recovery 时重新下载或重建 artifact | recovery 不能依赖 Cloud 可用性，也不能替换不可变历史版本的恢复证据。 |
| Hermes 也生成 marketplace | Hermes 只有 filesystem Plugin 语义；增加 marketplace 没有可消费方且会混淆其 staging/enable/disable 边界。 |

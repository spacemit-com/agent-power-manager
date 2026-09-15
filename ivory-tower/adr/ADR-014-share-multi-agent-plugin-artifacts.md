# Share Multi-Agent Plugin Artifacts

- Status：Superseded by [ADR-017](ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)
- Date：2026-08-20
- Decision Makers：APM maintainers

> 取代说明（2026-08-26）：[ADR-022](ADR-022-unify-lifecycle-entry-state-semantics.md) 定向取代本文（连同 ADR-013 与 Feature 018）中的以下旧规则：Plugin 部分状态一律保留 manifest 并 fail closed（现按统一入口状态矩阵收敛）；Plugin 正常卸载保留 marketplace（现卸载同时删除 Plugin、注销专属 marketplace 并删除 manifest，仅保留 artifact cache）；只有 Plugin 支持显式精确版本 manifest 全命中的无 catalog 快速路径（现已扩展到四类 Power）。
>
> 布局修正：[ADR-031](ADR-031-hermes-plugin-manifest-lives-at-plugin-root.md) 取代本文与 ADR-017 中的 `.hermes-plugin/` 子目录布局，派生 manifest 改为插件目录根的 `plugin.yaml` 与 `__init__.py`；[ADR-035](ADR-035-share-derived-filesystem-plugin-manifest.md) 进一步明确 `plugin.yaml` 携带 `schema_version: 1`；[ADR-036](ADR-036-per-agent-plugin-landing-filtering.md) 取代 ADR-035 的单根共用布局，改为按目标 Agent 落入 `landing/<agent-id>/plugins/<name>` 且移除 `author`。下文布局示意已按此更新。

## Context

[ADR-013](ADR-013-generate-per-plugin-local-marketplaces.md) 按 `agent + plugin + version` 下载、构建并缓存 Plugin。实际 Cloud Plugin 是可以同时携带 `.codex-plugin`、`.claude-plugin` 与 `.hermes-plugin` 的完整多 Agent 包；按 Agent 维护多份内容会重复发布、下载和缓存相同资源。新增内部 Agent Bibit 后，这种重复会继续扩大。

同时，Codex、Claude Code、Hermes 与 Bibit 都可以用逻辑 `<name>` 标识已安装 Plugin，精确版本由 APM manifest、缓存版本目录以及 Agent 原生 manifest 表达。继续把版本编码进实际名和 selector 会使 filesystem 发布路径偏离 Agent 规范，也没有提供额外的事务区分能力。

本决策关联 [Feature 018](../features/018-bibit-agent/spec.md)，并继续遵守 ADR-010、ADR-011 的类型专属 driver 与唯一 journal schema v1。

## Decision

Cloud 按 `PowerRef` 提供一份完整的多 Agent Plugin 原始包，不接受 Agent 参数，不保存 marketplace。APM 的 `pluginresource` 在首次缓存该版本时原样保存完整包，同时从 Codex、Claude 原生 manifest 生成两份单 Plugin 本地 marketplace，并将三者作为一个共享、不可变制品原子发布：

```text
cache/plugins/<logical-name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<logical-name>/
    ├── .codex-plugin/plugin.json
    ├── .claude-plugin/plugin.json
    ├── plugin.yaml
    ├── __init__.py
    └── ...
```

marketplace 是 APM 缓存制品的派生文件，不属于 Cloud 原始资源。它们在缓存 miss 的临时制品中同时生成，随完整制品一次发布，后续缓存命中、安装与恢复不重新生成或修改。

所有 Agent 的 Plugin 运行时名称和 Adapter selector 均为逻辑 `<name>`。Codex 与 Claude Code 通过固定 `apm-<name>` marketplace 注册不同版本的共享缓存根；版本切换先删除旧 `<name>`、切换同名 marketplace source，再安装 `<name>`。Hermes 与 Bibit 发布到各自 `plugins/<name>`；Hermes 保留 enable/disable CLI，Bibit 以目录存在为状态且不执行 Plugin CLI。版本由 `PowerRef`、APM manifest、缓存路径及原生 manifest 发现。

共享制品不主动审计 Cloud 是否完整适配所有 Agent；Cloud 管理方负责包规范。APM 仍保留 marketplace 生成所必需的 manifest 读取，以及本地路径、物理节点、已生成 marketplace、缓存和 journal 的损坏保护。Transaction recovery 继续只依赖 journal、Adapter、manifest 与本地制品，不连接 Cloud 或 Plugin Resource。

不提供旧按 Agent cache、版本化 selector 或旧 Plugin journal 的自动迁移。仓库资源和测试完全切换到新布局；运行时不主动删除旧 cache，避免在 recovery 前破坏事务证据。

## Consequences

- 正面影响：每个 Plugin 版本只发布、下载、校验和缓存一次，新增 Agent 不再复制 Cloud 内容。
- 正面影响：marketplace 与 filesystem Agent 消费同一份不可变恢复材料，Cloud、Plugin Resource、Adapter 与 Transaction 边界更清晰。
- 正面影响：四个 Agent 使用一致的逻辑 Plugin 名称，Hermes/Bibit 发布路径符合原生目录规范。
- 代价与风险：Cloud 必须保证多 Agent 包内容规范；某个必需 manifest 缺失会在生成或目标 Agent 消费时失败。
- 代价与风险：逻辑 selector 与共享缓存不兼容 ADR-013 的旧开发状态，需要人工清理旧状态。
- 后续动作：Feature 018、Project Overview、BUILD、C4 与 Changelog 引用本 ADR；ADR-013 保留为被取代的历史记录。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Cloud 共享包、缓存继续按 Agent 派生 | 仍重复磁盘内容、构建和恢复材料，新增 Agent 持续扩散 builder 映射。 |
| 共享原始缓存、按需生成 marketplace sidecar | 会在缓存发布后继续修改持久状态，增加恢复期间的中间态。 |
| 保留版本化实际名和 selector | filesystem 发布路径不符合 Hermes/Bibit 规范，版本信息重复编码。 |
| 运行时自动删除旧 cache | Plugin Resource 初始化早于 lifecycle recovery，可能先删除活动 journal 需要的证据。 |

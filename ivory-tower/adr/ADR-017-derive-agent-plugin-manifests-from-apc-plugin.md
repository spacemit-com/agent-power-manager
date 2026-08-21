# Derive Agent Plugin Manifests from apc-plugin.json

- Status：Accepted
- Date：2026-08-21
- Decision Makers：APM maintainers

## Context

[ADR-014](ADR-014-share-multi-agent-plugin-artifacts.md) 决定每个 Plugin 版本只保留一份共享不可变缓存，但假设 Cloud 原始包已经同时携带 Codex、Claude 与 Hermes 原生 manifest。APC 的权威 Plugin v1 契约改为只保存 `apc-plugin.json + skills/...`，并明确禁止开发者维护 Agent 原生 manifest。若 APM 只新增 Remote 下载而不改变构建边界，APC Plugin 在下载成功后仍无法被当前 Plugin Resource 消费，Local 与 Remote 也会产生不同格式。

本决策关联 [Feature 020](../features/020-cloud-client-providers/spec.md)，并取代 ADR-014 中关于 Cloud 原始包权威内容的决定；ADR-014 的“每个逻辑版本一份共享不可变 artifact、逻辑 selector、双 marketplace 与离线 recovery”继续保留并在本 ADR 中承接。

## Decision

Cloud 与 `internal/cloud/resources` 每个 Plugin 版本只维护 canonical APC Plugin v1：

```text
apc-plugin.json
skills/<declared-skill>/...
```

`apc-plugin.json` 是 name、version、description 和 Skill 闭集的唯一权威。raw package 禁止 `.codex-plugin`、`.claude-plugin`、`.hermes-plugin`、marketplace、commands、hooks、agents 与 MCP。

`pluginresource.Ensure` 在 cache miss 的唯一临时 artifact 中严格解析 canonical manifest，并确定性生成当前全部正式适配 Agent 所需的：

- `.codex-plugin/plugin.json`；
- `.claude-plugin/plugin.json`；
- `.hermes-plugin/plugin.yaml` 与 `.hermes-plugin/__init__.py`；
- Codex 与 Claude 的单 Plugin 本地 marketplace。

生成结果与 canonical package 一起复验并一次 rename 到：

```text
cache/plugins/<logical-name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<logical-name>/
    ├── apc-plugin.json
    ├── <APM 派生的 Agent manifest>
    └── skills/...
```

生成不依赖本次目标 Agent，因此同一 artifact 可供 Codex、Claude、Hermes 与 Bibit 复用。逻辑 Plugin 名与 selector 继续为 `<name>`。marketplace 和 Agent manifest 都是本地 cache 派生物，不属于 Cloud 内容。

Cache hit 与 transaction recovery 只复验已有 canonical manifest、派生配置、marketplace 和物理树；不连接 Cloud、不重新生成、不修复 cache。缺少 `apc-plugin.json` 的旧 artifact 视为损坏并 fail closed，不自动覆盖、删除或迁移。

## Consequences

- 正面影响：开发者与 APC 只维护一个跨 Agent、无原生绑定的 Plugin 索引。
- 正面影响：新增 Agent 的适配责任位于 APM builder，不要求重新发布或评审历史 Plugin。
- 正面影响：Local fixture 与 Remote 下载具有相同 canonical 输入，provider 真正可替换。
- 正面影响：继续保留 ADR-014 的共享 artifact、双 marketplace、逻辑 selector 和离线恢复模型。
- 代价与风险：APM builder 必须准确维护各 Agent 原生 manifest schema，并对历史 canonical Plugin 做兼容测试。
- 代价与风险：旧 cache 缺少 canonical 权威文件后无法继续使用，需要开发阶段人工清理。
- 代价与风险：Plugin Resource 的 validator 需要同时理解 raw canonical package 与最终派生 artifact 两个阶段。
- 后续动作：将 ADR-014 标记为被本 ADR 取代；Feature 018 与 Project Overview 的 Cloud 原始包描述迁移到本 ADR。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| APC 继续保存多套 Agent manifest | 与 APC Plugin v1 权威格式冲突，让开发者维护重复且可能不一致的配置。 |
| Remote Client 下载时直接生成，Local 保留旧 fixture | provider 输出不等价，生成逻辑错误地归属 transport 层。 |
| 每次安装只生成目标 Agent manifest | 同一版本 cache 会随安装顺序变化，破坏共享不可变 artifact。 |
| 在 cache 发布后按需生成 sidecar | 会修改已发布 cache，引入 recovery 无法单义解释的中间状态。 |
| 保留旧 artifact 兼容分支 | APM 尚在开发阶段，双 schema 会扩大 validator 与恢复测试矩阵而无用户迁移价值。 |

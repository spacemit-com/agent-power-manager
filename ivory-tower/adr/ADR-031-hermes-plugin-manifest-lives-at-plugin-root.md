# Hermes 插件 manifest 直接位于插件目录根

- Status：Accepted
- Date：2026-09-10
- Decision Makers：APM maintainers

> 局部取代说明：本 ADR 修正 ADR-017 与 ADR-014 中 Hermes 派生 manifest 的布局。此前的 `.hermes-plugin/plugin.yaml` 与 `.hermes-plugin/__init__.py` 由本决策取代为插件目录根下的 `plugin.yaml` 与 `__init__.py`；Codex/Claude 的 `.codex-plugin/`、`.claude-plugin/` 布局及 `apc-plugin.json` 权威源、raw package 校验、marketplace 生成不变。

## Context

APM 在缓存未命中时，从 canonical `apc-plugin.json` 确定性派生各 Agent 的原生 manifest，并在复验后一次性发布到 `cache/plugins/<logical-name>/<version>/plugins/<logical-name>/`。Codex 与 Claude 的原生 manifest 分别位于 `.codex-plugin/plugin.json` 与 `.claude-plugin/plugin.json` 子目录，此前 Hermes 沿用同一模式，派生到 `.hermes-plugin/plugin.yaml` 与 `.hermes-plugin/__init__.py`。

但真实 `hermes` CLI 的插件发现契约（v0.16.0，`hermes_cli/plugins.py` 的 `_scan_directory` 与加载逻辑）是：用户插件目录 `~/.hermes/plugins/<name>/` 必须**直接**包含 `plugin.yaml`（或 `plugin.yml`）与含 `register(ctx)` 的 `__init__.py`。`.hermes-plugin/` 子目录包裹不被识别。

实证结果：错误布局下 `hermes plugins list --json` 不列出该插件，但 `hermes plugins enable <name>` 仍退出 0（其存在性检查只验证目录存在）——APM 安装“成功”却从不加载插件，且后续 `Inspect` 把它判为 absent，破坏 install → list → reinstall → uninstall 的一致性。改用根目录布局后 `list --json` 正确列出（`name/version/source=user`），enable/disable 均正常。

本决策关联 [Feature 016](../features/016-lifecycle-fix/spec.md)，并继续遵守 ADR-010（类型专属 driver）与 ADR-017（从 `apc-plugin.json` 派生，仅布局路径修正）。

## Decision

Hermes 插件的派生 manifest 直接写入插件目录根，不再使用 `.hermes-plugin/` 子目录：

```text
cache/plugins/<logical-name>/<version>/plugins/<logical-name>/
├── apc-plugin.json
├── .codex-plugin/plugin.json
├── .claude-plugin/plugin.json
├── plugin.yaml          # Hermes 原生 manifest（原名 .hermes-plugin/plugin.yaml）
├── __init__.py          # Hermes 注册器（原名 .hermes-plugin/__init__.py）
└── skills/...
```

- 生成（`pluginresource.writeAgentManifests`）与复验（`pluginartifact.validateHermes`、closed-world 允许文件表）同步改为根目录路径。
- 派生 artifact 的 closed-world 不再允许 `.hermes-plugin/` 目录，使旧布局成为非法路径，防止回归。
- `plugin.yaml` 内容（`name/version/description/author`，`version` 为去 `v` 原生版本）与 `__init__.py` 内容（`def register(ctx): return None`）不变。
- Hermes adapter（`plugins list --json` / `enable` / `disable` / `TargetPath`）与 filesystem transaction 不读取 manifest 子路径，无需改动。

## Consequences

- 正面影响：Hermes 插件安装后可被真实 CLI 发现、启用与加载，Inspect 与 manifest 状态一致。
- 正面影响：旧 `.hermes-plugin/` 布局在 closed-world 中被显式拒绝，防止再次漂移。
- 代价与风险：`plugin.yaml`/`__init__.py` 出现在共享插件目录根，与 Codex/Claude marketplace 插件源同处；Codex/Claude 只读取各自 `.codex-plugin`/`.claude-plugin` 子目录，忽略这两个文件（由既有 Codex/Claude 测试继续覆盖）。
- 代价与风险：历史安装若残留 `.hermes-plugin/` 布局不会被自动迁移；APM 按当前正确布局重新安装即可覆盖。
- 后续动作：Project Overview、BUILD 与 Changelog 引用本 ADR；Feature 016 作为本次修复的实现记录。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保留 `.hermes-plugin/` 并在发布时改写路径 | 派生与发布两处不一致，增加隐藏耦合且复验复杂度上升。 |
| 在 filesystem transaction 中特判搬移 | transaction 应保持按目录整体原子发布，不应感知 Agent 的 manifest 子路径。 |
| 让 Hermes adapter 读取 `.hermes-plugin/` 再桥接 | CLI 契约不可改；桥接只掩盖真实 CLI 的发现行为。 |

# 派生的 plugin.yaml 由 Hermes 与 Bibit 共用并携带 schema_version

- Status：Superseded by [ADR-036](ADR-036-per-agent-plugin-landing-filtering.md)
- Date：2026-09-15
- Decision Makers：APM maintainers

> 取代说明（2026-09-15）：[ADR-036](ADR-036-per-agent-plugin-landing-filtering.md) 取代本文的「单一共享插件根同时服务 Hermes 与 Bibit」布局与 `author: APM` 字段：板端验收证明 Bibit 会拒绝 `__init__.py`、`apc-plugin.json` 与两个原生目录，也会拒绝 `author` 键，因此派生内容改为按目标 Agent 落入 `landing/<agent-id>/plugins/<name>`，`plugin.yaml` 只保留 `{schema_version, name, version, description}`。本文的 `schema_version: 1` 决策与 ADR-017 的派生/复用原则继续有效；下文第 2、4、6 条中的布局与 `author` 描述已过时。
>
> 局部取代说明：本 ADR 扩展 [ADR-031](ADR-031-hermes-plugin-manifest-lives-at-plugin-root.md) 的插件根布局：`plugin.yaml` 不再只是 “Hermes 原生 manifest”，而是 Hermes 与 Bibit 共用的 filesystem Agent manifest，并在原字段上新增 `schema_version: 1`。ADR-031 的路径决策与 [ADR-017](ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 的「从 `apc-plugin.json` 单次确定性派生」原则不变。

## Context

ADR-017 决定 `apc-plugin.json` 是唯一权威，APM 在 cache miss 时确定性派生全部正式适配 Agent 的原生 manifest，一次发布为共享不可变 artifact；ADR-031 把 Hermes 的 `plugin.yaml` 与 `__init__.py` 从 `.hermes-plugin/` 移到插件目录根，理由是真实 `hermes` CLI 只扫描 `~/.hermes/plugins/<name>/plugin.yaml`。

Bibit（`BIANBU_AGENT_HOME`，默认 `~/.bianbu-agent`）同样以 `plugins/<name>` 目录作为 Plugin 状态，其目录包同样以 `plugin.yaml` 表达身份（`bianbu-agent-cli-guide.md` 及板端验收记录 `artifacts/install-uninstall/2026-09-10-k3/report.md` 的 bibit `plugin/superpowers@v6.3.0` 用例）。也就是说 Hermes 与 Bibit 需要的 manifest 文件名与布局已经一致，不需要第二份派生文件；但 Bibit 侧的消费方要求在 manifest 中显式携带 `schema_version`，用于判断 manifest 契约版本。

与此同时 APM 内其他 canonical 清单已经统一使用整数 `schema_version: 1`（`apc-plugin.json`、`skill-card.md`、`agent.yaml`、MCP JSON）；filesystem Plugin manifest 是唯一没有该字段的派生物。

## Decision

1. `plugin.yaml` 是 filesystem deployment（Hermes 与 Bibit）**共用**的派生 manifest，路径固定为 `plugins/<logical-name>/plugin.yaml`，不新增 Bibit 专用文件名或字段前缀。

2. 在原字段之前新增 `schema_version`，取值固定为整数 `1`，字段顺序即 YAML 输出顺序：

   ```yaml
   schema_version: 1
   name: demo
   version: 1.2.3
   description: Canonical description
   author: APM
   ```

   `version` 仍为去 `v` 的原生版本。~~`author` 仍为 `APM`~~（已被 ADR-036 移除：Bibit 以 `unknown plugin.yaml key: author` 拒绝该键）。

3. 生成与复验共用同一常量与同一 struct，避免两侧漂移：

   - 生成：`pluginresource.writeAgentManifests` 写 `pluginartifact.FilesystemPluginManifest{SchemaVersion: 1, ...}`；
   - 复验：`pluginartifact.expectedFilesystemPluginManifest` 返回同值，`validateFilesystemPluginManifest` 以 `reflect.DeepEqual` 比较，`decodeExactRequiredYAML` 保持 `KnownFields(true)`，因此多余字段、缺失字段、错误类型（如 `schema_version: "1"`）与其他取值仍然 fail closed；
   - 常量 `pluginManifestSchemaVersion`（导出为 `PluginManifestSchemaVersion`）是唯一版本来源。

4. 复用语义不变，也不新增按 Agent 条件写：派生仍在 cache miss 的单次 pass 内完成，`pluginresource.Ensure` 先 `Lookup`（命中即返回），未命中才在 `pathLock(artifact.Root)` 下二次 `Lookup`，仍未命中才在新建的唯一临时根中构建并原子 `rename`。因此先装 Hermes 再装 Bibit（或反序）时，第二次安装命中同一不可变 artifact，不会重新下载，也不会重新派生，`writePrivateFile` 的 “derived Plugin file already exists” 防御性报错在复用路径上不可达。

5. 旧 cache fail closed，**不提供双 schema 兼容分支**：本变更前发布的 artifact 其 `plugin.yaml` 缺少 `schema_version`，`Lookup` 复验会返回 `ErrInvalid`（而非 `ErrMiss`），`Ensure` 直接失败且不修改不可变缓存，与 ADR-017 「缺少权威字段的旧 artifact 视为损坏、人工清理」一致。

6. 命名去除 Hermes 专属含义：`HermesPluginManifest` → `FilesystemPluginManifest`，`hermesManifestPath`/`hermesInitPath`/`hermesInitContents` → `pluginManifestPath`/`pluginInitPath`/`pluginInitContents`，`expectedHermesManifest` → `expectedFilesystemPluginManifest`，`validateHermes` → `validateFilesystemPluginManifest`；Claude marketplace 的 owner 名改用独立常量 `apmAuthor`，不再复用 `hermesAuthor`。~~`__init__.py` 仍保留在插件根，因为它是 Hermes 注册器；Bibit 忽略该额外条目。~~（已被 ADR-036 取代：Hermes 与 Bibit 各自消费独立落盘叶子，`__init__.py` 只写 Hermes 叶子。）

7. Codex/Claude 的 `.codex-plugin/plugin.json`、`.claude-plugin/plugin.json` 与两份 marketplace 完全不变——它们是各自 CLI 的严格 schema，加入未知字段会被其解析器拒绝。

## Consequences

- 正面影响：Hermes 与 Bibit 消费字节完全相同的 `plugin.yaml`，不存在两份可能漂移的 manifest；新增字段只落在一个文件、一处生成、一处复验。
- 正面影响：`schema_version` 与其他 canonical 清单的版本约定统一，Bibit 侧可据此判断契约版本。
- 正面影响：共享 artifact 的复用要求由既有 `Ensure` 锁 + `Lookup` + 原子发布满足，无需引入安装期按需生成，也不会产生冲突错误。
- 代价与风险：已生成的旧 cache（`plugin.yaml` 无 `schema_version`）在新版本 APM 下复验失败，需要开发阶段人工清理 `cache/plugins/<name>/<version>`；不自动迁移，以免破坏 recovery 证据。
- 代价与风险：`plugin.yaml` 同时被两个 Agent 读取，任何字段变更都要同时满足两侧消费方；因此字段集保持最小，仅新增 `schema_version`。
- 代价与风险：Hermes 侧对新增键的容忍依赖其 `yaml.safe_load` 且不校验已知字段（v0.16.0 实测）；Bibit 侧的 `schema_version` 字段名与取值沿用本 ADR，若板端消费方定义不同，仅需改 `FilesystemPluginManifest` 的 yaml tag 与常量值。
- 后续动作：BUILD.md、Project Overview、Feature 018/020 的 spec/plan、`docs/power-template/plugin.md` 与 Changelog 引用本 ADR；ADR-014/017/031 中过时的 `.hermes-plugin/` 与“Bibit 复用 Claude 版本身份”表述同步修正。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 为 Bibit 新增独立文件名（如 `bibit-plugin.yaml`） | Hermes 与 Bibit 的目录布局与 manifest 语义已经一致，第二份派生文件只会引入漂移与重复校验。 |
| 安装第二个 Agent 时才按需生成/补齐 manifest | 破坏「生成不依赖目标 Agent」的共享不可变 artifact 前提（ADR-017 已明确拒绝），并会让同一版本 cache 内容随安装顺序变化。 |
| 保留缺失 `schema_version` 的旧 artifact（双 schema 兼容） | 需要在复验中接受两种 schema，弱化 closed-world；且旧 artifact 本就允许人工清理。 |
| 由 Bibit 侧读取 Codex/Claude manifest 推断身份 | 与 ADR-031 修正前的错误假设同源：Bibit 按其自己的 `plugin.yaml` 契约发现插件，不应依赖其他 Agent 的私有文件。 |
| 用字符串 `"1"` 或独立版本号命名空间表达 schema 版本 | 与仓库既有整数 `schema_version: 1` 约定不一致，且削弱严格的类型校验。 |

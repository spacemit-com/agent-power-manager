# 共享 artifact 内按目标 Agent 落盘，安装目录只出现目标 Agent 的配置

- Status：Accepted
- Date：2026-09-15
- Decision Makers：APM maintainers

> 局部取代说明：本 ADR 取代 [ADR-035](ADR-035-share-derived-filesystem-plugin-manifest.md) 中的「单一共享插件根同时服务 Hermes 与 Bibit」布局，以及该 ADR 保留的 `plugin.yaml` 的 `author: APM` 字段。[ADR-017](ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 的「`apc-plugin.json` 是唯一权威、单次确定性派生、一次原子发布共享不可变 artifact」原则，与 ADR-035 新增的 `schema_version: 1` 决策不变。

## Context

ADR-035 假设 Hermes 与 Bibit 可以消费同一份插件目录内容（`plugin.yaml` + `skills/` + `__init__.py`，由 Bibit「忽略额外条目」）。板端验收否决了这个假设：

- Bibit 0.1.61 的 `installer.py::_validate_package` 只允许目录 `{tools, skills, hooks, lib, scripts, mcps}` 与文件 `{plugin.yaml, config.yaml, console.py, entrypoint .py}`；出现 `apc-plugin.json`、`.codex-plugin/`、`.claude-plugin/`、`__init__.py` 任意一项都会以 `unsupported Plugin package entry: <name>` 拒绝安装。
- Bibit 的 manifest 解析只接受根键 `{schema_version, name, version, description, dependencies, config_schema, entrypoint}`；`author` 会以 `unknown plugin.yaml key: author` 拒绝（`Skipping unreadable Plugin ...`）。
- 同一时刻 Hermes 的 `PluginManager` 要求插件根存在 `__init__.py`，否则 `enabled=False error='No __init__.py in ...'`。

因此「Hermes 必需、Bibit 禁止」的 `__init__.py` 使单一共享插件根在物理上不可能同时满足两个 filesystem Agent。Codex 与 Claude 侧虽是各自严格 schema（`.codex-plugin/plugin.json`、`.claude-plugin/plugin.json`），但真实 CLI 实测证明：`claude plugin marketplace add` + `claude plugin install`、`codex plugin marketplace add` + `codex plugin add` 只会复制 marketplace `source` 指向的那棵子树到安装目录，不复制整个 artifact 根。

## Decision

1. 保持「一次下载、一次派生、一次原子发布」不变：`Ensure`/`Lookup` 依旧不携带 Agent，同一版本在四个 Agent 之间复用同一 `cache/plugins/<name>/<version>/` 不可变 artifact。按目标的过滤只发生在落盘内容上，不在下载或派生次数上。

2. artifact 根内为每个 Agent 分配独立的落盘插件叶子，目录名即 Agent ID；同时保留 canonical 包：

   ```text
   cache/plugins/<name>/<version>/
   ├── .agents/plugins/marketplace.json      # Codex；source ./landing/codex/plugins/<name>
   ├── .claude-plugin/marketplace.json       # Claude；source ./landing/claude-code/plugins/<name>
   ├── plugins/<name>/                       # canonical 包，只含 apc-plugin.json + 声明 Skill
   └── landing/<agent-id>/plugins/<name>/    # 目标 Agent 安装目录的完整内容来源
       ├── codex/         .codex-plugin/plugin.json + skills/…
       ├── claude-code/   .claude-plugin/plugin.json + skills/…
       ├── hermes-agent/  plugin.yaml + __init__.py + skills/…
       └── bibit/         plugin.yaml + skills/…
   ```

   固定顺序为 `{codex, claude-code, hermes-agent, bibit}`，由 `pluginartifact.PluginLandingAgents()` 与 `LandingRelativeDir`/`LandingPluginPath`/`LandingMarketplaceSource` 单点定义。

3. 四个 Agent 的过滤方式：

   - Codex / Claude：marketplace 的 `source` 从 `./plugins/<name>` 改为 `./landing/<agent-id>/plugins/<name>`；Agent CLI 只复制该子树。
   - Hermes / Bibit：`Manager.StageFilesystem(agent, ref)` 只把 `landing/<agent-id>/plugins/<name>` 复制到 staging，安装目录不再出现其他 Agent 的配置。

4. 派生 manifest 的 `author` 字段整体移除：`FilesystemPluginManifest` 只保留 `{schema_version, name, version, description}`，字节输出精确为四行；生成与复验继续共用同一 struct 与 `reflect.DeepEqual`，`decodeExactRequiredYAML` 保持 `KnownFields(true)`，因此重新加入 `author`（或其他未知键）会 fail closed。`__init__.py` 只写 Hermes 叶子，Bibit 叶子不写。

5. 复验同步收紧：`ValidateShared` 复验 canonical 包、四个落盘叶子与两份 marketplace 的精确相对路径闭集，并用 `validateLandingSkillPayloads` 要求每个叶子的 `skills/` 子树与 canonical 逐字节相等（禁止 symlink/hardlink 复用，理由见 Alternatives）。

6. 旧布局 cache（没有 `landing/` 目录）fail closed：`ValidateShared` 返回带可操作提示的 `ErrInvalid`（提示删除该 artifact 根以重新派生），`Lookup`/`Ensure` 不自动改写、不自动清理不可变缓存，与 ADR-017/035 的旧 artifact 处理一致。

7. 不新增 CLI flag、不改 journal schema、不改卸载语义：卸载仍只按运行时事实移除目标 Agent 的 plugin 与专属 marketplace。

## Consequences

- 正面影响：Bibit 安装目录不再出现 `apc-plugin.json`、`.codex-plugin/`、`.claude-plugin/`、`__init__.py`；Hermes 仍拿到 `__init__.py`；Codex/Claude 只拿到自己的原生 manifest，无需运行期按目标改写文件。
- 正面影响：`plugin.yaml` 只含四个字段，同时满足 Bibit 的根键白名单与 Hermes 的宽松解析；`author` 不再成为跨 Agent 的契约分歧点。
- 正面影响：跨 Agent 复用逻辑不变，先装 Hermes 再装 Bibit（或反序、或四者任意顺序）仍为一次下载、一次派生；第二个 Agent 命中 `Lookup` 直接复用，不会触发「派生文件已存在」的冲突错误。
- 代价与风险：artifact 内 `skills/` 被物理复制多份（canonical + 四个叶子），cache 体积约增大数倍；物理树校验禁止 symlink/hardlink，因此接受该体积代价。当前 `demo-plugin` 级别 Skills 体积可忽略。
- 代价与风险：落盘叶子与 canonical 必须保持同步，否则复验失败；这由同一 `writeAgentLandings` 单点生成 + `validateLandingSkillPayloads` 的逐字节比较兜住。
- 代价与风险：本变更前的 cache 需要人工清理 `cache/plugins/<name>/<version>`，否则安装直接失败并给出提示。
- 后续动作：ADR-035 标记为被本文取代（其中 `schema_version` 决策仍有效）；`ivory-tower/project-overview.md`、Feature 018/020 spec/plan、`docs/power-template/plugin.md`、`docs/testing/apm-cli-functional-test.md`（原 `install-uninstall.md`）、Changelog 与 BUILD/README 引用本文。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 继续用单一共享插件根（ADR-035 布局） | Hermes 必需、Bibit 禁止 `__init__.py`，物理上不可满足；Bibit 还会拒绝 `apc-plugin.json` 与两个原生目录。 |
| 在 staging 阶段按目标 Agent 过滤 | 只能影响 filesystem Agent，无法影响 Codex/Claude——它们的安装内容由 Agent CLI 依据 marketplace `source` 决定。 |
| 落盘叶子用 symlink/hardlink 指向 canonical `skills/` | 违反 `ValidatePhysicalTree`/`requireUniqueLink` 的物理树约束，且会让安装目录依赖 artifact 内其他路径。 |
| 把四个 Agent 合并成一份「最大公约数」manifest | 每个 Agent 的 schema 都是封闭的严格契约，并集必然被至少一方拒绝（`author` 与 `__init__.py` 已证明）。 |
| 每个 Agent 独立下载/派生一份 artifact | 破坏跨 Agent 复用，重复下载与派生，且会让同一版本在不同 Agent 下内容不一致。 |
| 运行时按目标 Agent 改写已发布 artifact | 破坏不可变 artifact 与 recovery 证据；安装顺序会改变 cache 内容。 |

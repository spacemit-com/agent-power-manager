# 目录与 CLI JSON 通道携带 v2 展示字段

- Status：Accepted
- Date：2026-09-28
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- Related ADR：[ADR-024](ADR-024-persist-installed-power-description-and-tags.md)（元数据持久化先例）、[ADR-029](ADR-029-list-json-carries-description-and-tags.md)（`list --json` 字段恒集）
- 局部取代说明：本 ADR 取代 [ADR-038](ADR-038-power-format-v2-localized-display-fields.md) 第 7 条中
  「APC 目录 item 契约不动、`list --json`/`show` 输出不变」与 [ADR-039](ADR-039-add-name-en-and-display-name-length-limits.md)
  第 7 条中同样的「APC 目录契约、`list`/`show` 输出不变」两处决定。ADR-038 的其余决定（v2 版本门禁、
  提交侧与安装侧校验分层、`zh_name` → `name_zh` 更名、`search` 只匹配 `name`、派生清单不携带展示字段）
  继续有效；ADR-039 的字段形状规则继续有效。

## Context

ADR-038 引入 `name_zh`/`name_en`/`description_zh` 时，明确把「谁把它们送到前端」留在了所有通道之外：

- 三字段只存在于资源内容（`SKILL.md` frontmatter、`apc-plugin.json`、`MCP.json`、`agent.yaml`）；
- 提交 metadata 不上报它们（APC 的 metadata 是闭集，携带未知字段会失败）；
- 目录 item 契约、`list`/`list --json`、`show`、`search` 的输出一律不变；
- 安装时只把 `description`/`tags` 抄进本地清单与 journal。

结果是既有的降级路径：前端只能拿到 `name`/`description`，卡片与详情无法随应用语言切换。用户已确认
APC 会在公共目录 item 上补齐三字段，因此本仓库需要打通「目录解码 → 安装捕获 → 本地清单/journal →
`list`/`show` 的 JSON 输出」这条链。

需要决策的是四件事：三字段落在哪些契约层、`search` 与文本输出是否一并改变、存储侧 schema 是否升版本、
以及目录侧 `schema_version` 的处理方式。

已取证的约束（决定了条款 4 与 5）：

- APC 公共目录 item 当前带 `"schema_version": 1`（26/26 条目），但它表达的是**资源格式版本**而非目录契约版本；
  APC-API.md §4.1 的 `ResourceMetadata` 字段表里甚至没有列出该字段。
- 旧 APM（≤ 0.1.5）对目录三处版本号的实测行为：顶层 envelope 与分组内的 `schema_version` 只要不是 `1`
  就报 `must be 1` 并拒绝整份目录；**item 级 `schema_version` 完全不参与校验**。
- 目录 item 采用字段白名单严格解码，未知键会报 `unknown catalog field`。因此只给 APC 加字段、不先发 APM，
  会让所有旧客户端把新目录判为非法，退化到本地陈旧快照（无快照则 `ErrCatalogUnavailable`），
  **不报错但静默冻结在旧目录**。
- 远端目录端点路径硬编码为 `api/v1`（`internal/cloud/remote/catalog.go`），APC 没有匿名可读的 v2 端点可迁。
- `~/.apm/manifests/*.json` 用 `encoding/json` 读取（容忍未知键）；journal 用 `DisallowUnknownFields`
  （不容忍未知字段）。

## Decision

三个展示字段作为**可选的追加字段**进入目录条目契约与本地持久化，并**恒定出现**在 `list`/`show` 的 JSON
记录中；`search` 与所有文本输出保持不变；目录与存储的 `schema_version` 都不升。

具体条款：

1. **目录 item 契约**：`CatalogEntry`（`GET /api/v1/resources` 的 `items[]`）在 `description`/`tags`
   之外新增可选的 `name_zh`、`name_en`、`description_zh`。字段缺失即空串（历史 v1 条目一律如此），
   提供时按 ADR-039 的形状规则校验。APM 侧对应 `domain.CatalogEntry`、`catalog.itemDTO` 与
   `catalogObjectAllowsKey` 的 item 白名单。APC 侧补齐字段与文档属 `../apc` 的独立交付。
2. **`list --json` 字段恒集扩展**：`apm list --json`（含 `--all`、`--installed`、`--installed --agent=all`
   分组模式）的每条记录在 `description`/`tags` 之外恒带 `name_zh`/`name_en`/`description_zh`，空值为 `""`。
   这局部取代 ADR-029 的字段恒集，沿用其理由：前端需要一次批量取得展示元数据。
3. **`show --json` 同步扩展**：`show` 是精确版本的详情通道，输出与 `list --json` 相同的发布元数据字段集，
   使 per-version 的 `description_zh` 可被读取。`show` 的**文本**输出不变。
4. **`search` 与全部文本输出不变**：`search --json` 仍只有 `type`/`name`/`version`/`description`，
   不输出 `tags` 也不输出展示字段；`search` 仍只匹配 `name`；`list`/`show` 的文本渲染逐字节不变。
   `search` 不是展示元数据通道，保持 ADR-028/ADR-038 承诺的形状。
5. **目录与存储的版本号一律不升**：目录 envelope、目录分组、目录 item 的 `schema_version` 与
   `/resources/version` 的 `schema_version` 全部保持 `1`；本地 manifest 与 journal 的 `schema_version`
   同样保持 `1`，新字段以 `omitempty` 追加。理由是条款 Context 中列出的实测行为：item 级版本号不参与校验
   （升了没有作用），envelope/分组版本号会让所有旧客户端硬拒目录且没有迁移端点（升了只有代价），
   而「追加可选字段不升版本」正是 ADR-024 已确立的先例。表达「目录现在可能带展示字段」的载体是本文档与
   APC-API.md，不是版本号。
6. **本地持久化**：安装时从目录条目捕获三字段，写入本地 manifest 的 `powerEntry` 与 journal envelope，
   使 `list --installed` 离线可读、离线 recovery 重放后仍可见。展示字段不参与身份比较、幂等、缓存键、
   排序或 recovery 判定；`installedPowerEqual` 仅用于判断是否需要重写清单。
7. **`CatalogEntry.Validate` 校验展示字段形状**：目录解码路径对提供的展示字段执行 ADR-039 的形状校验
   （缺失合法）。这是有意的严格性：展示文本只在目录与资源内容里各有一份真相，脏数据应在入库时暴露，
   而不是被前端的回退逻辑掩盖。代价见 Consequences。
8. **派生清单边界不变**：为 Codex/Claude/Hermes/Bibit 派生的原生清单继续只含
   `name`/`version`/`description`/`skills`；canonical Skill 子树是原样复制的内容，其 `SKILL.md` 中的展示
   字段必须保留。守卫测试按「派生清单」与「复制的 canonical 内容」逐文件区分，而不是对 landing 做整体扫描。

## Consequences

- 正面影响：前端可以从 `list --json`（云端与已安装两个范围）与 `show --json` 一次拿到三字段，
  卡片与详情可随应用语言切换；插件侧无需再读资源文件或逐个查询。
- 正面影响：APM 是唯一改动方，插件不需要新的数据源；`search` 与文本输出逐字节不变，既有脚本与文本
  解析不受影响。
- 正面影响：目录与存储都不升版本，旧 manifest 与旧 journal 无损读取。
- 代价与风险：**发布顺序成为硬约束**——必须 APM 先发、APC 后加字段。若 APC 先行，旧 APM 会拒绝整份
  目录并静默冻结在陈旧快照。反向（新 APM + 旧目录）安全：字段缺失即空串，前端回退到 `name`/`description`。
- 代价与风险：单条目录数据形状非法（如 `name_zh` 只有 1 个字符）会让整份目录解码失败，所有云端命令
  退化到陈旧快照。这是条款 7 的已知代价，依赖 APC 提交侧已强制形状（Feature 031 已实现）。
- 代价与风险：**已安装的旧记录不会自动回填**。展示字段是安装时捕获的元数据，同版本重装会命中
  `manifestSameVersion` 短路（`internal/lifecycle/power_state.go`）并返回 `unchanged`，因此改动前安装的
  Power 在其清单记录里仍是空串，前端继续走回退。板端实测：手写不带展示字段的清单后执行同版本
  `apm install`，结果为 `unchanged` 且清单未变；`uninstall` 后重装才写入三字段。升级 APM 后需要重装
  相关 Power 才能让已安装范围切换语言，这是「元数据只在安装时捕获」这一既有设计的直接后果。
- 代价与风险：旧 APM 读新 manifest 容忍未知键，但重写清单时会丢字段；旧 APM 读新 journal 会因
  `DisallowUnknownFields` 拒绝恢复。与 ADR-024 同级别风险，同样的缓解方式（不降级恢复）。
- 代价与风险：若 APC 未来必须提升目录 envelope 版本，则需 APM 同步接受新值，且旧客户端仍会冻结；
  届时应同时提供迁移端点，而不是只改版本号。
- 后续动作：APC 侧按本文档条款 1 与 5 在目录 item 上补齐字段并更新 APC-API.md（`ResourceMetadata`、
  `ResourceVersionMetadata`、§5.2 示例与 §4.1 字段表）；`../using-apm/references/cli.md` 中
  「三字段不是 `list`/`show` 输出字段」的说明需要随之更正。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 把目录 envelope/分组 `schema_version` 升到 2 | 旧客户端硬拒目录且路径硬编码 `api/v1`，没有迁移端点；结果是全量客户端静默冻结，收益只是「版本号更诚实」。 |
| 把目录 item 的 `schema_version` 升到 2 | 实测该字段完全不参与 APM 校验，升它是纯噪声；它表达资源格式版本，与目录字段集无关。 |
| 用 `search` 输出三字段 | `search` 的定位是精简匹配结果（无 `tags`），且它是唯一不带 `agent` 的记录形状；把展示字段塞进去会破坏 ADR-028 的形状承诺。 |
| 让文本输出也显示展示名 | 文本输出被脚本与文档引用的形状比 JSON 更脆弱，且展示名对文本场景没有增量价值。 |
| 升 manifest/journal schema version | 旧数据需迁移或拒绝，破坏向后兼容；追加可选字段不需要升版本（ADR-024 先例）。 |
| 让插件直接读本机资源文件补字段 | 绕过 Provider 边界，且 plugin 派生清单与各 Agent 的 MCP 落盘形态不一致；market 范围根本无文件可读。 |
| 目录解码时不校验展示字段形状 | 会把脏数据的暴露点推迟到前端回退逻辑，问题被掩盖而非修复；APC 提交侧已强制形状，严格校验的失败面很小。 |

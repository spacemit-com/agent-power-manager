# Feature 031：Power 资源格式 V2（本地化展示字段）

- Status：Accepted
- Date：2026-09-28
- Related ADRs：[ADR-038](../../adr/ADR-038-power-format-v2-localized-display-fields.md)、[ADR-039](../../adr/ADR-039-add-name-en-and-display-name-length-limits.md)、[ADR-040](../../adr/ADR-040-catalog-and-cli-json-carry-display-fields.md)
- Related Features：[Feature 021](../021-submission-validate/spec.md)（提交校验）、[Feature 023](../023-submission-resource-metadata-query/spec.md)（资源文件是身份来源）、[Feature 026](../026-agent-package-assets/spec.md)（Agent 目录资源）

## 1. 目标

为四类 Power 增加仅用于展示的 `name_zh`、`name_en` 与 `description_zh`，把资源格式升到 `schema_version: 2`，
提交侧只接受 v2，安装与目录解码路径继续兼容 v1；`agent.yaml` 的 `zh_name` 更名为 `name_zh`。`name` 仍是唯一
身份、调用名与排序键。展示名带长度约束：`name_zh` 为 2–16 个 Unicode 字符，`name_en` 不超过 32 个字符。

成功标准：

- 四类 v2 资源在各自的权威文件中声明三个展示字段，`apm validate` 通过；缺字段、超出长度或写 v1 时报错点名到字段与文件。
- 已发布的 v1 资源仍可被安装与解码（本地 provider 与既有 fixture 覆盖）。
- 展示字段经目录 item 契约与 CLI JSON 通道可达前端：见 [ADR-040](../../adr/ADR-040-catalog-and-cli-json-carry-display-fields.md)
  与 [§4.4](#44-展示字段的对外通道)（含 [§4.5](#45-已安装记录的回填边界) 的回填边界）。
- 内置资源 fixture 全部升到 v2 且保持可被安装链读取。
- 派生 Agent 原生清单不含新字段，且 `reflect.DeepEqual` 复验继续通过。

## 2. 范围

### 2.1 Included

- 四类资源文件的新字段（`name_zh`/`name_en`/`description_zh`）与 v2 版本门禁：`SKILL.md`、`skill-card.md`、`apc-plugin.json`、`MCP.json`、`agent.yaml`。
- 提交侧严格校验（`ValidateSubmit*`）与安装侧兼容校验（`Validate*`）的拆分。
- `agent.yaml` 的 `zh_name` → `name_zh` 更名与旧键显式拒绝。
- 展示字段的形状规则（必填、trim、字符数与字节上限、字符集、控制字符）与其单元测试。
- 内置资源 fixture（15 份 Skill、2 份 Plugin、3 份 MCP、1 份 Agent）升级 v2。
- 存量测试 fixture 的相应升级，以及 v1 拒绝 / 安装兼容 / 派生守卫的新增测试。
- `docs/power-template/*` 模板与规则文档、README。
- `../using-apm` 的模板与手册（含上传前用户审核同意流程）。

### 2.2 Excluded

- 不修改 APC 目录契约、`../apc` 仓库文档与交付。
- 不修改 `list` / `show` / `search` 的任何输出或匹配行为。
- 不把展示字段写入本地 manifest、journal 或提交 metadata。
  （**提交 metadata 一句已由 [ADR-041](../../adr/ADR-041-align-submission-metadata-with-resource-content.md) 取代**：
  metadata 现与资源内容对齐，携带三个展示字段与资源格式版本；本地 manifest/journal 的结论不变。）
- 不收紧 canonical `name`（仍为 1–64 字节小写 kebab），也不把中英文长度规则套用到它。
- 不修改 APM CLI 的交互能力（不加提示、`--dry-run`、`--yes`）。
- 不修改 `agents/<name>` 目录命名、安装落点与 Runtime 兼容逻辑。
- 不修改 Bibit 仓库（其 `RuntimeAgent.zh_name` 的映射列为跨仓库后续动作）。

## 3. 数据模型

### 3.1 版本常量

| 常量 | 值 | 含义 |
| --- | --- | --- |
| `PowerSchemaVersionV1` | 1 | 历史格式，无展示字段；仅安装侧接受 |
| `PowerSchemaVersionV2` | 2 | 当前格式，要求展示字段；提交侧唯一接受 |

### 3.2 字段

| 字段 | 位置 | 提交校验 | 安装校验 |
| --- | --- | --- | --- |
| `schema_version` | 各权威文件 | 必须为整数 2 | 必须为 1 或 2 |
| `name_zh` | 各权威文件（Agent 为 `agent.name_zh`） | 必填，2–16 个 Unicode 字符，无控制字符 | 可选；提供时校验形状 |
| `name_en` | 各权威文件（Agent 为 `agent.name_en`） | 必填，≤ 32 字符，全为可打印 ASCII 且至少一个字母 | 可选；提供时校验形状 |
| `description_zh` | 各权威文件 | 必填，≤ 1024 字节，允许换行 | 可选；提供时校验形状 |
| `name` | 不变 | canonical kebab（1–64 字节）；唯一调用名与排序键，不套用展示名长度规则 | 同左 |

### 3.3 派生边界

`apc-plugin.json` 是 Plugin 的唯一权威源；APM 派生出的 `.codex-plugin/plugin.json`、
`.claude-plugin/plugin.json`、`plugin.yaml` 与两份 marketplace 继续只含
`name`/`version`/`description`/`skills`，不携带展示字段。

## 4. 行为契约

### 4.1 提交侧（validate 与 submit）

- `schema_version: 1` 被拒绝，错误包含 `upgrade to 2`。
- 缺少或留空 `name_zh` / `name_en` / `description_zh` 被拒绝，错误点名具体字段与文件。
- `name_zh` 超出 2–16 字符、`name_en` 超过 32 字符或含非 ASCII 字母缺失时被拒绝。
- v2 且字段齐备时校验通过，`validate` 与 `submit` 一致。

### 4.2 安装侧（install / download / cache / recovery）

- 目录解码、内容校验、派生与事务恢复接受 v1 与 v2。
- v1 资源不因缺少展示字段而失败。
- v2 资源若展示字段形状非法则失败（fail closed），但缺失不报错。

### 4.3 `agent.yaml` 更名

- `agent.name_zh` 是唯一被识别的展示名键。
- 出现 `agent.zh_name` 时返回可操作错误，指出新键名。

### 4.4 展示字段的对外通道

ADR-038 只定义了资源文件里的字段，没有定义它们如何到达前端。ADR-040 补齐这条通道：

- **目录 item**：`GET /api/v1/resources` 的 `items[]` 可选携带 `name_zh`/`name_en`/`description_zh`；
  缺失即空串，提供时按 §4.1 的形状规则校验。字段与文档由 `../apc` 交付。
- **本地持久化**：安装时从目录条目捕获三字段写入本地 manifest 与 journal（可选追加字段，不升 schema version），
  `list --installed` 离线可读、离线 recovery 重放后仍可见。
- **CLI JSON**：`list --json`（含 `--all`、`--installed`、分组模式）与 `show --json` 的记录恒带三字段（空为 `""`）。
- **目录与存储版本号**：envelope、分组、item 与 `/resources/version` 的 `schema_version` 均保持 `1`；
  item 级 `schema_version` 始终不参与 APM 校验。

### 4.5 已安装记录的回填边界

展示字段是**安装时捕获**的元数据，不做离线回填：同版本重装命中 `manifestSameVersion` 短路并返回
`unchanged`，因此从旧版本 APM 升级后，已安装 Power 的清单记录仍为空串，前端继续走回退，直到该 Power 被
卸载后重装（或升/降版本）。这是「元数据只在安装时捕获」的既有设计的直接后果，不在本次改动范围内。

### 4.6 未变更行为

- `search` 的输出与匹配语义一律不变（仍只匹配 `name`、仍不输出 `tags` 与展示字段）。
- `list`、`list --all`、`show` 的**文本**输出逐字节不变。
- 提交 metadata（multipart）不新增字段。
  （**已由 [ADR-041](../../adr/ADR-041-align-submission-metadata-with-resource-content.md) 取代**：metadata 现新增
  `name_zh`/`name_en`/`description_zh`，`schema_version` 改为资源格式版本。）
- 派生 Agent 原生清单不新增字段；本地 manifest 与 journal 的 schema version 不变。

## 5. 验收

1. `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
2. `TestSubmitRejectsV1Resources`：四类 v1 资源在 validate/submit 均被拒且提示升级。
3. `TestSubmitRequiresLocalizedDisplayFields`：四类资源缺字段或长度越界时错误点名 `name_zh`/`name_en`/`description_zh`。
   领域层另有 `TestDisplayNameZHLengthBounds`、`TestDisplayNameENLengthAndCharset`、`TestDisplayMetadataRequiredFields`
   锁定 2–16 字与 ≤ 32 字符边界。
4. `TestSubmitAcceptsV2ResourcesWithDisplayFields`：四类 v2 资源校验通过。
5. `TestInstallPathAcceptsV1Resources`：v1 的 MCP 与 agent.yaml 可解码并通过安装侧校验。
6. `TestDerivedAgentManifestsCarryNoLocalizedFields`：派生清单不含展示字段（复制的 canonical Skill 内容保留）。
7. `TestDerivedAgentManifestsStillDeriveForV1Artifact`：v1 canonical 包仍可派生落盘清单。
8. 本地 provider 端到端：`APM_CLOUD_PROVIDER=local APM_CLOUD_LOCAL_ROOT=<repo>/internal/cloud/resources`
   下 `apm list --type skill --json`、`apm show`、安装 skill/plugin/mcp/agent 各一成功。
9. `../using-apm` 四类模板通过 `apm validate`。
10. 展示字段通道（ADR-040）：`TestDecodeCatalogWithDisplayFields`、`TestEncodeOmitsEmptyDisplayFields`、
    `TestDecodeRejectsInvalidDisplayFields`、`TestDecodeCatalogItemSchemaVersionIsNotValidated`、
    `TestLocalCatalogParsesDisplayFieldsFromEachAuthoritativeFile`、`TestLocalCatalogOmitsDisplayFieldsForV1Resources`、
    `TestJSONRepositoryManifestRoundTripsDisplayFields`、`TestJournalPersistsDisplayMetadata`、
    `TestRenderListJSONCarriesDisplayFields`、`TestShowJSONCarriesDisplayFields`、
    `TestSearchOutputCarriesNoDisplayFields`、`TestInstalledManifestCarriesDisplayFieldsEndToEnd` 全绿。

远端真实提交（v2 content 由 APC 接收）在 APC 就绪前记为 BLOCKED，不计为通过。

## 6. 跨仓库依赖

Bibit Runtime 通过 `agents/<name>/agent.yaml` 读取专家展示名，其 `RuntimeAgent` 目前用
`#[serde(alias = "zh_name")]` 兼容 `zh_name`。字段更名后需要在 Runtime 侧映射 `agent.name_zh`，
并在 `/api/v1/agents` 响应中继续以 `zh_name` 暴露（或同步调整前端契约）。本次不修改 Bibit 仓库。

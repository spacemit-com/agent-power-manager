# Feature 031：Power 资源格式 V2（本地化展示字段）

- Status：Accepted
- Date：2026-09-28
- Related ADRs：[ADR-038](../../adr/ADR-038-power-format-v2-localized-display-fields.md)、[ADR-039](../../adr/ADR-039-add-name-en-and-display-name-length-limits.md)
- Related Features：[Feature 021](../021-submission-validate/spec.md)（提交校验）、[Feature 023](../023-submission-resource-metadata-query/spec.md)（资源文件是身份来源）、[Feature 026](../026-agent-package-assets/spec.md)（Agent 目录资源）

## 1. 目标

为四类 Power 增加仅用于展示的 `name_zh`、`name_en` 与 `description_zh`，把资源格式升到 `schema_version: 2`，
提交侧只接受 v2，安装与目录解码路径继续兼容 v1；`agent.yaml` 的 `zh_name` 更名为 `name_zh`。`name` 仍是唯一
身份、调用名与排序键。展示名带长度约束：`name_zh` 为 2–16 个 Unicode 字符，`name_en` 不超过 32 个字符。

成功标准：

- 四类 v2 资源在各自的权威文件中声明三个展示字段，`apm validate` 通过；缺字段、超出长度或写 v1 时报错点名到字段与文件。
- 已发布的 v1 资源仍可被安装与解码（本地 provider 与既有 fixture 覆盖）。
- `list --all`、`list --json`、`show` 的输出**不变**（本 Feature 不触碰渲染层）。
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

### 4.4 未变更行为

- `list`、`list --all`、`show`、`search` 的输出与匹配语义一律不变。
- 提交 metadata（multipart）不新增字段。
- 本地 manifest 与 journal 结构不变。

## 5. 验收

1. `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
2. `TestSubmitRejectsV1Resources`：四类 v1 资源在 validate/submit 均被拒且提示升级。
3. `TestSubmitRequiresLocalizedDisplayFields`：四类资源缺字段或长度越界时错误点名 `name_zh`/`name_en`/`description_zh`。
   领域层另有 `TestDisplayNameZHLengthBounds`、`TestDisplayNameENLengthAndCharset`、`TestDisplayMetadataRequiredFields`
   锁定 2–16 字与 ≤ 32 字符边界。
4. `TestSubmitAcceptsV2ResourcesWithDisplayFields`：四类 v2 资源校验通过。
5. `TestInstallPathAcceptsV1Resources`：v1 的 MCP 与 agent.yaml 可解码并通过安装侧校验。
6. `TestDerivedAgentManifestsCarryNoLocalizedFields`：派生清单不含展示字段。
7. `TestDerivedAgentManifestsStillDeriveForV1Artifact`：v1 canonical 包仍可派生落盘清单。
8. 本地 provider 端到端：`APM_CLOUD_PROVIDER=local APM_CLOUD_LOCAL_ROOT=<repo>/internal/cloud/resources`
   下 `apm list --type skill --json`、`apm show`、安装 skill/plugin/mcp/agent 各一成功。
9. `../using-apm` 四类模板通过 `apm validate`。

远端真实提交（v2 content 由 APC 接收）在 APC 就绪前记为 BLOCKED，不计为通过。

## 6. 跨仓库依赖

Bibit Runtime 通过 `agents/<name>/agent.yaml` 读取专家展示名，其 `RuntimeAgent` 目前用
`#[serde(alias = "zh_name")]` 兼容 `zh_name`。字段更名后需要在 Runtime 侧映射 `agent.name_zh`，
并在 `/api/v1/agents` 响应中继续以 `zh_name` 暴露（或同步调整前端契约）。本次不修改 Bibit 仓库。

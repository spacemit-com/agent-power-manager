# Feature 031 实施计划：Power 资源格式 V2

> **For agentic workers:** 本计划的实施已在本轮完成；下列任务与提交状态用于复核与后续维护。

**Goal:** 为四类 Power 增加展示字段 `name_zh`/`name_en`/`description_zh`，把资源格式升到 v2，提交侧只接受 v2，
安装侧继续兼容 v1，并同步 APM 文档与 `../using-apm` 手册/模板。

**Spec:** `ivory-tower/features/031-power-format-v2/spec.md`
**ADR:** `ivory-tower/adr/ADR-038-power-format-v2-localized-display-fields.md`、`ivory-tower/adr/ADR-039-add-name-en-and-display-name-length-limits.md`、`ivory-tower/adr/ADR-040-catalog-and-cli-json-carry-display-fields.md`

## Global Constraints

- `name` 是唯一身份、调用名与排序键；展示字段不得进入身份比较、缓存键、排序或搜索匹配。
- 展示名长度：`name_zh` 为 2–16 个 Unicode 字符，`name_en` ≤ 32 个可打印 ASCII 字符且至少含一个字母。
- 提交侧只接受 `schema_version: 2`；安装、下载、缓存、目录解码与事务恢复接受 1 或 2。
- 派生 Agent 原生清单不携带展示字段，`reflect.DeepEqual` 复验必须继续通过。
- 不改 APC 目录 item 契约以外的 `../apc` 仓库内容；`search` 输出与匹配语义、文本渲染与提交 metadata 不变
  （ADR-040 局部取代「APC 目录契约与 `list`/`show` JSON 输出不变」两处决定）。
- 新增/修改接口代码添加简短规范中文注释。

## Task 1：领域层展示字段与版本常量

- [x] `internal/domain/display.go`：新增 `PowerSchemaVersionV1/V2`、`ValidateDisplayNameZH`、
      `ValidateDisplayDescriptionZH`、`ValidateOptionalDisplayMetadata`、`ValidateRequiredDisplayMetadata`、
      `ValidateSubmitSchemaVersion`、`ValidateInstallSchemaVersion`。
- [x] `internal/domain/mcp.go`：`MCPResource`/`mcpResourceWire` 增 `NameZH`/`DescriptionZH`；
      `Validate` 兼容 v1+v2，新增 `ValidateSubmit`。
- [x] `internal/domain/agent_config.go`：`AgentInfo` 增 `NameZH`/`DescriptionZH`；解码识别 `name_zh`/
      `description_zh` 并显式拒绝旧键 `zh_name`；`Validate` 兼容 v1+v2，新增 `ValidateSubmit`。

## Task 2：Plugin 权威索引

- [x] `internal/pluginartifact/apc_manifest.go`：`APCPluginManifest` 增两个字段；
      `ValidateAPCPluginManifest` 改为接受 v1+v2；新增 `ValidateSubmitAPCPluginManifest`。

## Task 3：提交侧严格校验

- [x] `internal/submission/skill.go`：`SKILL.md` frontmatter 要求整数 `schema_version: 2` 与三个展示字段；
      `skill-card.md` 的 `schema_version` 由 1 改为 2；`validateSkillContent` 返回 `skillMetadata`。
- [x] `internal/submission/plugin.go`：先跑 `ValidateSubmitAPCPluginManifest`，内嵌 Skill 复用同一校验。
- [x] `internal/submission/mcp.go`：解码后追加 `ValidateSubmit`。
- [x] `internal/submission/agent.go`：`agentartifact.Validate` 之后追加 `Config.ValidateSubmit()`。

## Task 4：内置资源 fixture 升 v2

- [x] 15 份 `SKILL.md`（含 demo-plugin 内嵌）加 `schema_version: 2` 与三个展示字段。
- [x] 15 份 `skill-card.md` 的 `schema_version` 1 → 2。
- [x] 2 份 `apc-plugin.json`、3 份 `MCP.json` 升 v2 并补字段。
- [x] 1 份 `agent.yaml` 升 v2，新增 `name_zh`/`name_en`/`description_zh`。

## Task 5：存量测试与新增门禁测试

- [x] 升级 `internal/{submission,domain,agentartifact,pluginartifact,cloud,cloud/local,lifecycle}` 的 v1 夹具。
- [x] 新增 `internal/submission/v2_gate_test.go`（v1 拒绝、缺字段、v2 通过）。
- [x] 新增 `internal/domain/v2_compat_test.go`（安装侧 v1 兼容、版本门禁、必填字段、2–16 字与 ≤ 32 字符边界）。
- [x] 新增 `internal/pluginresource/localized_fields_test.go`（派生清单守卫、v1 派生兼容）。
- [x] `go build ./...`、`go vet ./...`、`go test ./...` 全绿。

## Task 6：文档

- [x] `ivory-tower/adr/ADR-038-power-format-v2-localized-display-fields.md`。
- [x] `ivory-tower/features/031-power-format-v2/{spec.md,plan.md}`。
- [x] `docs/power-template/{skill,plugin,mcp,agent,README}.md`：模板与规则更新到 v2（含 `name_en` 与长度规则）。
- [x] `docs/Changelog.md`：新增行为/契约变更条目。
- [x] `internal/cloud/resources/*` 迁移脚本不入库（一次性本地脚本）。

## Task 7：`../using-apm`

- [x] 模板全部升 v2 并补三个展示字段；`agent.yaml` 的 `zh_name` → `name_zh`。
- [ ] `templates/README.md`、`references/submission.md`、`references/cli.md` 同步。
- [x] `SKILL.md` 增加「格式不正确时帮助补充信息 + 上传前必须取得用户审核同意」硬约束。
- [x] 手册自身的 `SKILL.md`/`skill-card.md` 升 v2、补字段并提升版本号。

## Task 8：验证

- [x] `gofmt -l`、`go vet ./...`、`go test ./...`。
- [x] 本地 provider 端到端：四类资源可安装，`list`/`show` 文本输出与改动前一致。
- [x] `../using-apm` 四类模板通过 `apm validate`。
- [ ] 远端 v2 提交验收（依赖 APC，未就绪时记 BLOCKED）。

## Task 9：展示字段对外通道（ADR-040，续作）

- [x] 目录层：`domain.CatalogEntry`、`catalog.itemDTO`、item 字段白名单支持三字段；
      `CatalogEntry.Validate` 校验提供的展示字段形状；`equalCatalogEntry` 同步比较。
- [x] Local provider：`parseLocalSkillMetadata` 与四类 loader 从各自权威文件解析三字段
      （Skill frontmatter、`apc-plugin.json`、`MCP.json`、`agent.yaml`），内嵌 Skill 不覆盖插件 manifest。
- [x] 持久化：`domain.InstalledPower`、`config.powerEntry`、`transaction.Journal` 与四个 request 携带三字段；
      `installedPowerEqual` 覆盖；installMetadata/applyToJournal/installedPower 收敛字段复制。
- [x] 装配与渲染：`cli.ListedPower` 携带三字段；`listPowerJSON` 与 `show` 的 JSON 记录恒带三字段；
      `catalogPowers` 显式丢弃以保持 `search` 不变。
- [x] 派生清单守卫改为逐文件区分「派生清单」与「复制的 canonical 内容」，fixture 换成真实 v2 Skill。
- [x] 新增锁定测试（目录 codec、领域、Local provider、config、transaction、lifecycle、cli）并全绿。
- [x] 文档：`ADR-040`、Feature 031 spec/verification、`docs/Changelog.md`、`docs/testing/apm-cli-functional-test.md`。
- [x] 板端构建与安装：`agent-power-manager_0.1.6_riscv64.deb` 覆盖 0.1.5；Local provider 下四类资源的
      展示字段解析、安装持久化与 `list --installed --json` 输出均已核实（见 verification §4.5）。
- [ ] APC 公共目录 item 补齐字段（`../apc` 独立交付；**APM 必须先发布**）。
- [ ] GUI 语言切换验证：需先重装已安装 Power 回填展示字段（见 spec §4.5），market 范围还依赖 APC 发布。

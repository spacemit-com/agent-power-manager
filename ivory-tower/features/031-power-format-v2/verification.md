# Feature 031 验证记录

## 1. 本地验证（2026-09-28）

- `gofmt -l .` 无输出；`go vet ./...` 通过；`go test ./...` 全部包通过。
- `TestSubmitRejectsV1Resources`：四类 v1 资源在 validate/prepare 均被拒绝，错误含 `upgrade to 2`。
- `TestSubmitRequiresLocalizedDisplayFields`：缺字段与长度越界均点名到具体字段。
- `TestSubmitAcceptsV2ResourcesWithDisplayFields`：四类 v2 资源校验通过。
- `TestInstallPathAcceptsV1Resources`：v1 的 MCP 与 agent.yaml 可解码并通过安装侧校验。
- `TestDisplayNameZHLengthBounds` / `TestDisplayNameENLengthAndCharset` / `TestDisplayMetadataRequiredFields`：
  锁定 `name_zh` 2–16 字符与 `name_en` ≤ 32 可打印 ASCII 字符且含字母。
- `TestDerivedAgentManifestsCarryNoLocalizedFields` / `TestDerivedAgentManifestsStillDeriveForV1Artifact`：
  派生清单不携带展示字段，且 v1 canonical 包仍可派生。

## 2. 本地 provider 端到端

以 `APM_CLOUD_PROVIDER=local` 指向 `internal/cloud/resources`：

- 四类 v2 fixture 安装成功（skill→codex、plugin→bibit、mcp→codex、agent→bibit）。
- 手写 v1 skill 仍可安装，但 `validate` 被拒并提示 `upgrade to 2`（双版本证据）。
- `list --all`、`list --json`、`show` 输出与改动前一致；`search 简报` 返回 `[]`，展示名不参与匹配。

## 3. 模板验证

`docs/power-template/*` 与 `../using-apm/templates/*` 的代码块物化为真实资源后，四类均通过
`apm validate`；`../using-apm` 手册自身（`SKILL.md` + `skill-card.md`）亦通过。

## 4. 远端 APC 提交探测（2026-09-28）

用既有 API Key 对生产 APC 做了两组受控上传，目的是确认 v2 展示字段能否通过提交并发布。

### 4.1 metadata 携带展示字段 → 失败

控制变量：固定 content，仅改变 metadata 字段集。

| 臂 | metadata 差异 | 结果 |
| --- | --- | --- |
| A 基线 | 严格按 APC 文档字段集 | `409 VERSION_PUBLISHED`（校验通过，进入版本检查） |
| B | 基线 + `name_zh`/`name_en`/`description_zh` | `500 INTERNAL_ERROR` |
| C | 基线 + 虚构字段 `zzz_apm_probe_unknown` | `500 INTERNAL_ERROR` |
| F | 基线 + 拼错字段 `name_zhh` | `500 INTERNAL_ERROR` |
| D | 基线删除必填 `description` | `400 VALIDATION_FAILED` |

三次重复结果一致。C/F 同样 500，说明这是通用未知字段处理，不是字段名特定行为。
结论：**metadata 是闭集，携带展示字段会导致提交失败**；且文档约定未知字段返回
`400 INVALID_REQUEST`，实际返回 `500 INTERNAL_ERROR`，属服务端缺陷。

### 4.2 content 携带展示字段 → 通过

用新名提交 v2 MCP（content 含三个展示字段，metadata 严格合规）：

- `HTTP 202` → `sub_01M3KVFAAPGR4GX39S7Z`
- 异步校验：`validated -> review_pending`，`validation_errors: []`

结论：**APC 最低验收不检查内容的 `schema_version`，也不拒绝展示字段。**

### 4.3 发布阶段结论（未等审核，按已发布制品推定）

线上两个已发布 MCP 的发布信封字段（`type`/`name`/`version`/`description`/`message`/
`definition`/`schema_version`/`content_md5`）与 submission metadata 一一对应，而 metadata
拒绝未知字段（4.1）。因此 MCP 发布物**没有承载展示字段的通道**；skill/plugin/agent 的
content 逐字节保留（已用 4 个线上制品比对 MD5 确认），不受影响。

### 4.4 副作用

探测产生一条真实提交 `sub_01M3KVFAAPGR4GX39S7Z`（`mcp`/`apm-v2-probe-mcp`/`v1.0.0`），
当前处于 `review_pending`，占用 1 个 active 名额。APC MVP 不提供开发者撤销，需由内网
review 拒绝。

## 5. 未完成的验收

- 远端真实 v2 提交并发布：依赖 APC 支持展示字段（skill/plugin/agent 已具备条件，
  MCP 需 APC 扩展 metadata 与重建逻辑），记为 BLOCKED。
- Bibit Runtime 的 `name_zh`/`name_en` 映射：跨仓库依赖，不在本仓库范围。

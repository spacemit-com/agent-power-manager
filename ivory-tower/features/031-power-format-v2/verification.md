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

## 5. 开发板构建与验证（2026-09-28）

### 5.1 环境

- 开发板：`bianbu@10.0.91.51`（k3），Bianbu 4.0.7，riscv64。
- Go：`go1.26.0 linux/riscv64`；debhelper `13.24.2ubuntu1-bb1`；`dpkg-buildpackage 1.23.7`。
- 源码目录：`/home/bianbu/Workspace/build/apm-v2`（由本地 `384d118` 同步，不含 `.git`）。
- 包版本：`0.1.5`（上一板端构建为 `0.1.3`；仓库归档候选为 `0.1.4`）。

### 5.2 构建

在板端执行 `dpkg-buildpackage -us -uc -b`，退出码 0，`go mod verify` 通过。

产物：

```text
agent-power-manager_0.1.5_riscv64.deb          3170864 字节
  SHA-256 f3d471ad36ce7e9a5a87828e6d4310baf28bbdd33af89655261989a0d7020cd9
agent-power-manager-dbgsym_0.1.5_riscv64.deb   3513800 字节
```

包内容仅 `/usr/bin/apm` 与 `/usr/share/doc/agent-power-manager/` 下的
`README.Debian`、`changelog.gz`、`copyright`；控制字段
`Version: 0.1.5`、`Architecture: riscv64`、`Depends: ca-certificates`，
无 maintainer script。

### 5.3 发现的打包缺陷：`-b` 会跳过测试

`debian/rules` 的 `override_dh_auto_test` 只在 `debian/rules build` 序列中被调用。
实测对比：

- `debian/rules build` → 日志含 `override_dh_auto_test` 与 `go test ./...`，22 个包 `ok`。
- `dpkg-buildpackage -us -uc -b` → 整份日志 71 行，只调用 `debian/rules clean` 与
  `debian/rules binary`，**没有** `debian/rules build`，因此既无 `override_dh_auto_test`
  也无 `go test`。debhelper 的 `dh binary` 序列包含 configure/build 但不含 test。

这与 Feature 030 spec「默认执行 `go test ./...`，最终验收不跳过测试」不符：`-b` 二进制打包
路径下测试被静默跳过，既往 `0.0.1`/`0.1.3` 包同样如此。本次通过**在板端单独执行
`go test ./...`** 补齐该证据（退出码 0，全部包通过），未把 `-b` 描述为已运行测试。

### 5.4 板端功能验证

| 项目 | 结果 |
| --- | --- |
| `apt-get install` 0.1.5（覆盖 0.1.3） | 成功，`dpkg-query` 显示 `0.1.5 install ok installed` |
| `dpkg -V agent-power-manager` | 无输出（文件完整性一致） |
| `/usr/bin/apm --help` | 退出 0 |
| v2 fixture `validate`（MCP/Skill） | 通过 |
| v1 资源 `validate` | 被拒，错误含 `upgrade to 2` |
| `name_zh` 长度门禁 | 1 字与 17 字分别报 `want 2-16` |
| `name_en` 字母门禁 | `123-456` 报 `must contain at least one ASCII letter` |
| 包自带 `debian/tests/smoke` | 退出 0 |
| 用户数据保留 | `~/.apm/catalog.json` SHA-256 前后一致（`8b1d73dcd3d15557…`），`~/.cache/apm` 仍在 |

板端功能验证使用隔离的 `APM_SOURCE_ROOT` 与临时目录，未修改真实用户状态或 Agent 资源。
最终板端保持 `agent-power-manager 0.1.5` 已安装；因 `0.1.5 > 0.1.4`，`apt upgrade`
不会把该本地构建降级回归档版本。

### 5.5 同步注意事项

`rsync --exclude='apm'` 会同时排除 `cmd/apm`，导致首次构建报
`stat .../cmd/apm: directory not found`。排除本地二进制必须写成锚定路径
`--exclude='/apm'`。

## 6. 未完成的验收

- 远端真实 v2 提交并发布：依赖 APC 支持展示字段（skill/plugin/agent 已具备条件，
  MCP 需 APC 扩展 metadata 与重建逻辑），记为 BLOCKED。
- Bibit Runtime 的 `name_zh`/`name_en` 映射：跨仓库依赖，不在本仓库范围。
- `debian/rules` 在 `-b` 路径下跳过测试：属打包配置缺陷，需单独修复（本次未改
  `debian/`，避免与板端既有构建流程冲突）。

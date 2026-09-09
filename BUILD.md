# 构建指南

本指南说明如何在本地构建、验证和运行 APM 命令行工具。

## 前置条件

- Git
- Go 1.26.0（版本以 [`go.mod`](go.mod) 为准）

在仓库根目录确认 Go 版本：

```bash
go version
```

## 快速构建

在仓库根目录执行：

```bash
go mod download
go build -o ./apm ./cmd/apm
./apm --help
```

构建完成后，可执行文件位于 `./apm`。

## Debian 软件包（Bianbu）

在 Bianbu 开发板的源码根目录安装构建依赖后执行：

```bash
sudo apt-get install debhelper golang-go ca-certificates
dpkg-buildpackage -us -uc -b
sudo apt-get install ../agent-power-manager_0.0.1_riscv64.deb
/usr/bin/apm --help
```

打包过程使用 Go modules 联网获取由 `go.mod` 和 `go.sum` 锁定的依赖；可按组织要求通过
`GOPROXY` 选择模块代理。构建使用本机 Go 工具链并执行 `go mod verify` 与 `go test ./...`。
`DEB_BUILD_OPTIONS=nocheck` 只用于明确跳过测试的构建，不能替代发布前验证。

软件包只安装 `/usr/bin/apm` 和包文档，不创建服务或系统用户。`apt remove` 与
`apt purge` 都保留用户的 `~/.apm`、APM 缓存、Agent 资源和配置；重装不会迁移或重置这些
数据。此约定见 [ADR-030](ivory-tower/adr/ADR-030-package-cli-and-preserve-user-data.md)。

## Cloud Provider 配置

APM 把 Local 与 Remote provider 静态链接到同一个二进制，并由 `cmd/apm` 的 `newCloudClient` 在每次 lifecycle 构造时创建独立 `ProviderRegistry`、注册 factory 后只打开被选中的 provider（[ADR-016](ivory-tower/adr/ADR-016-select-cloud-clients-with-explicit-providers.md)）。默认 provider 是 `remote`，默认 APC 测试环境根地址为 `https://developer.bianbu.xyz`（[ADR-026](ivory-tower/adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)）：

```bash
# 默认 Remote（默认 APC 测试环境根地址，可用 APC_BASE_URL 覆盖）
APC_BASE_URL=https://developer.bianbu.xyz ./apm list

# 显式 Local canonical fixture
APM_CLOUD_PROVIDER=local \
APM_CLOUD_LOCAL_ROOT="$(pwd)/internal/cloud/resources" \
./apm list
```

| 环境变量 | 默认值与约束 |
| --- | --- |
| `APM_CLOUD_PROVIDER` | 默认 `remote`；只接受 `remote` 或 `local`。 |
| `APC_BASE_URL` | 默认 `https://developer.bianbu.xyz`；必须是无凭据、query、fragment 和非根路径的绝对 URL。非 loopback 地址只允许 HTTPS，HTTP 仅允许 loopback。 |
| `APM_CLOUD_LOCAL_ROOT` | 选择 `local` 时必填，必须是 clean absolute path，且从文件系统根到 fixture 根不能经过 symlink/reparse point。 |

provider 配置、Remote 网络/协议或内容错误都不会自动回退另一 provider。composition root 为 Remote 构造具有两分钟整体 timeout 和同源 redirect 策略的 `http.Client`；调用方更短的 context deadline 优先。Search 与默认 Cloud List 只在首次目录快照请求时惰性构造所选 provider；installed List 不触发该构造。Install 为内容获取构造 provider，但四类 Power 显式精确 manifest 全命中时不调用 catalog（[Feature 025](ivory-tower/features/025-unified-lifecycle-state-semantics/spec.md)、[ADR-022](ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md)）；Uninstall 与 Fix 都不创建 Cloud Client。

### 资源目录缓存与同步

当前目录发现契约由 [ADR-021](ivory-tower/adr/ADR-021-cache-versioned-resource-catalog.md) 定义。APM 在 `~/.apm/catalog.json`（测试时位于 `$APM_HOME/.apm/catalog.json`）保存唯一完整目录，不按 provider、URL 或 Local root 分叉，也不保存 sidecar marker。catalog 顶层 `schema_version=1`，并且必须同时包含 `skills`、`plugins`、`mcps`、`agents` 四组；每组单独维护 `schema_version=1` 且空集合也编码为 `items: []`。条目按组确定类型，包含精确版本、description、message、content type/size/MD5、发布时间和有序 tags。

同步只使用两个匿名接口：`GET /api/v1/resources/version` 返回 catalog 原始字节的 SHA-256 与精确大小，`GET /api/v1/resources` 返回该确定性 JSON 对象。hash 相同时直接复用本地文件；hash 不同时下载到私有临时目录，经 100 MiB 上限、严格 JSON/schema、排序、size/hash 校验后以 `0600` 原子替换。旧的公开分页与逐项发现接口不再存在；latest、显式版本和跨类型歧义都在不可变 `Snapshot` 上完成，Snapshot 查询本身不访问网络或文件系统。

默认 List 与 Search 在无锁 Inspect journal 后调用 `Snapshot`：它短时、非阻塞获取现有 `apm.lock`，同步完成后释放锁，再由 lifecycle 本地过滤和输出；锁竞争返回 `ErrLocked`，不等待、不使用 stale fallback。Install 已持有同一全局锁，因此在 Recover、Agent 刷新和四类 Power 精确 manifest 早退之后调用 `SnapshotLocked`，不会二次加锁。`list --installed`、Uninstall 与 Fix 完全不调用 catalog。

version、下载、provider 协议或下载内容完整性失败时，若旧 `catalog.json` 仍严格有效，命令使用该 stale Snapshot 并只在 stderr/log 记录一次 warning；JSON stdout schema 不变。旧文件缺失或损坏则失败；caller cancellation、锁错误、临时目录/Repository 本地安全错误不允许 fallback。stale catalog 可离线 list/search，也可在目标 Skill/Agent/Plugin 制品缓存命中时完成 install；内容 cache miss 仍按网络错误失败，离线期间不能即时确认刚被下架的版本。

Catalog Repository 拒绝 symlink、hardlink、非 `0600` 文件、不可信目录链与所有可检测的目录项替换。其 pathname threat model 明确把 current EUID 视为受信任主体；parent advisory lock 只串行遵守 Repository 协议的发布者，不承诺隔离恶意同 EUID 进程在 POSIX 最终 `fstatat` 到 `unlinkat` 窄窗的替换。该边界及需要更强隔离时的要求记录在 ADR-021。

Catalog Repository 的平台边界独立于下文 Remote content 发布能力，固定如下：

| 平台 | `Load` | `Replace` |
| --- | --- | --- |
| Linux、macOS | 支持 | 支持 |
| FreeBSD | 支持，可读取既有严格有效 catalog | 不支持，返回 `ENOTSUP` 并 fail closed |
| Windows 与其它平台 | 不支持，fail closed | 不支持，fail closed |

因此 FreeBSD 可以在 hash 相同且已有有效本地文件时继续查询，但不能首次发布或更新 catalog；Windows 与其它未实现平台不能加载或替换 catalog。

四类 Power 通过同一 `cloud.Client` 获取，但保留各自的内容边界；四个内容方法都接收由同一 Catalog Snapshot 解析出的完整 `CatalogEntry`，禁止按 `PowerRef` 二次回源拼装 metadata：

- Skill：Local 复制 canonical fixture 目录；Remote 获取 `application/gzip`，先验证精确 metadata、size 与 32 位小写 MD5，再安全解包到调用方私有目录。版本化 cache 的命中、下载临时目标、原子发布和公共 staging 全部由 Cache 的 `PrepareSkill` 管理。
- Agent：Local/Remote 都把资源根目录（`agent.yaml` + 可选 `assets/`）的完整目录树写到调用方私有目录；Remote content type 为 `application/gzip`，解包后执行权威 Agent 目录校验（[ADR-023](ivory-tower/adr/ADR-023-package-agent-with-assets.md)，云端契约见 APC ADR-006）。版本化 cache 与 staging 由 `PrepareAgent` 管理，安装前仍由 Bibit Agent Adapter 对完整目录执行同一权威校验。
- MCP：Local/Remote 都严格解码 `application/json` 并返回克隆的 `MCPServer` 领域对象，不建立文件 cache；公共 HTTP endpoint 只允许无 userinfo 的绝对 HTTPS URL。
- Plugin：Local fixture 与 Remote 解包结果只包含 `apc-plugin.json + skills/...`。`apc-plugin.json` 是唯一权威索引；Plugin Resource 严格复验 canonical 闭集，先确定性生成 Codex、Claude、Hermes 原生配置，再由 Codex/Claude marketplace builder 读取并复验对应原生 manifest 后生成 marketplace，最后一次原子发布共享 artifact。当前权威决策是 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)；[ADR-014](ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅保留为被取代的历史背景。

Remote 的精确 content 受 20 MiB 客户端上限约束；Skill/Plugin/Agent 解包还限制为总大小 100 MiB、单文件 10 MiB、最多 1,000 个节点、深度 16、路径 255 字节和 100 倍解压倍率，并拒绝路径逃逸、链接、特殊节点、嵌套归档扩展名以及已知归档/可执行文件签名。Linux 与 macOS 支持完整的 descriptor-relative Skill/Plugin/Agent 目录发布和 MCP 临时 staging；FreeBSD 支持 descriptor-relative staging 和 MCP，但 Skill/Plugin/Agent 的目录发布以 `ENOTSUP` fail closed。Windows 及其它未实现平台的 Remote content 操作 fail closed；catalog 中存在条目不因此被描述为该平台可安装。

Remote descriptor-relative 操作要求 destination/MCP 临时目录链上的普通物理目录都由 root 或当前 euid 拥有，且 `(mode & 0022) == 0`。root-owned sticky 目录可出现在受信链中；继续遍历时，其直接子目录必须由当前 euid 拥有且仍不可被 group/other 写入，后续目录继续遵守 owner/mode 约束。root-owned sticky 目录本身是终点时，直接条目创建使用独占语义，发布使用 no-replace 语义。该边界不隔离能够以同一 euid 主动篡改目录的进程，device/inode 复验只用于发现变化，不能提升为跨同一用户进程的安全隔离保证。

## 开发验证

提交前建议依次执行以下检查：

```bash
# 检查 Go 源码是否已格式化；无输出表示通过。
test -z "$(gofmt -l cmd internal)"

# 运行全部单元测试。
go test ./...

# 执行 Go 静态检查。
go vet ./...

# 重新构建命令行程序。
go build -o ./apm ./cmd/apm
```

如需自动格式化源代码，执行：

```bash
gofmt -w cmd internal
```

## 本地运行

构建后可通过下列命令验证命令行参数解析：

```bash
./apm --help
./apm search summary
./apm search summary --type skill
./apm --verbose search summary
./apm list
./apm list --type skill
./apm list --type mcp
./apm list foo-before-summary
./apm list foo-before-summary --all
./apm list foo-before-summary --type skill
./apm show foo-before-summary --type skill
./apm show foo-before-summary --type skill --version v0.0.1 --json
./apm list --installed
./apm list --installed --agent codex
./apm list --installed --agent all --type skill
./apm list foo-before-summary --installed --agent codex
./apm install foo-before-summary --agent codex
./apm install foo-before-summary --agent bibit
./apm install foo-before-summary=v0.0.1 --agent codex --type skill
./apm install foo-before-summary --agent all
./apm install filesystem=v0.0.1 --agent codex --type mcp
./apm install company-tools=v0.0.1 --agent claude-code --type mcp
./apm install filesystem=v0.0.1 --agent bibit --type mcp
./apm install demo-plugin=v0.1.0 --agent codex --type plugin
./apm install demo-plugin=v0.1.0 --agent hermes-agent --type plugin
./apm install demo-plugin=v0.1.0 --agent bibit --type plugin
./apm uninstall foo-before-summary --agent codex
./apm uninstall foo-before-summary --agent all --type skill
./apm uninstall filesystem --agent hermes-agent --type mcp
./apm uninstall demo-plugin --agent claude-code --type plugin
./apm uninstall demo-plugin --agent bibit --type plugin
./apm install bianbu-agent-default=v1.0.0 --type agent
./apm install bianbu-agent-default=v1.0.0 --type agent --agent bibit
./apm uninstall bianbu-agent-default --type agent
./apm list --installed --type agent --agent bibit
./apm search bianbu-agent-default --type agent
./apm list --type agent
./apm fix
./apm --verbose fix
apm validate <path> --type <skill|plugin|mcp|agent> [--json]
apm submit <path> --type <skill|plugin|mcp|agent> --message "本次提交说明"
apm submission list [--status <active-status>] [--type <skill|plugin|mcp|agent>] [--name <canonical-name>] [--output text|json]
apm submission get <submission-id> [--output text|json]
```

### Validate、Submit 与 Submission

`validate` 与 `submit` 覆盖 Skill、Plugin、MCP、Agent 四类 Power，通过统一 Source Loader 复用同一套离线校验规则；当前契约由 [ADR-020](ivory-tower/adr/ADR-020-source-owned-submission-metadata.md) 与 [Feature 023](ivory-tower/features/023-submission-resource-metadata-query/spec.md) 定义：

- Skill：原始目录，身份和描述来自 `SKILL.md` frontmatter，content 是确定性 tar.gz。
- Plugin：原始目录，身份和描述来自 `apc-plugin.json`，content 是确定性 tar.gz。
- MCP：单个完整 v1 `MCP.json`，包含 `schema_version/name/version/description/definition`，完整资源经 RFC 8785 JCS 规范化。
- Agent：资源根目录，身份和描述分别来自 `agent.name`/`agent.version`/`agent.description`，content 是确定性 tar.gz（根含 `agent.yaml` 与可选 `assets/`）。

`validate` 完全离线，只接收 path、type 与可选 JSON 输出，也不读取 API Key、访问网络。`submit` 对四类 Power 统一要求 trim 后非空的 `--message`；校验通过后构造 content、计算原始字节 MD5 并经 `POST /api/v1/submissions` 上传，每次逻辑提交生成 UUID 幂等键。message 属于 Submission，不写入 Power content，也不参与 MD5。身份校验沿 domain parser → cache → adapter `ValidateArtifact` → recovery 四层加固。

`submit` 按 APC 响应输出统一 outcome：

```text
outcome: accepted|replayed
submission_id: sub_...
status: received|validating|...
track: apm submission get sub_...
```

- `accepted` 对应 `202 Accepted`：首次受理，返回带非空 `status_url` 的 receipt（`submission_id`/`status`/`status_url`）。
- `replayed` 对应 `200 OK`：同账号、同 `Idempotency-Key`、同请求的幂等重放，返回当前 Submission 详情，无 `status_url`，用 `submission_id` 生成 `apm submission get` 提示。
- `409 ALREADY_PUBLISHED` 表示已发布同标识、同 MD5，作为错误返回 `cloud.ErrAlreadyPublished`（同时满足 `cloud.ErrSubmissionConflict`），CLI 不伪造新的 Submission ID；`409 RESOURCE_VERSION_CONFLICT` 映射通用 conflict。

`accepted` 与 `replayed` 都不代表发布成功，只表示本次提交已被同步受理或已存在。Local provider 的 `Submit`/`GetSubmission`/`ListSubmissions` 对所有类型返回 `cloud.ErrSubmissionUnsupported`（fail loud），因此 validate/submit/submission 需指向 Remote APC（默认 provider）。

`submission list` 和 `submission get` 使用 `X-API-Key: <official-api-key>` 的独立认证请求，不向匿名资源接口泄露 Key。list 只返回当前 Key 所属账号按创建时间倒序的 active Submission（`received`、`validating`、`review_pending`、`approved`、`publishing`），最多 20 条，只接受 status/type/name 筛选，不提供分页或时间范围参数。get 展示 description、message、时间戳和原样 timeline；终态记录只能通过已知 ID 获取。

Search 已实现：它从同步后的一份不可变 Catalog Snapshot 按名称搜索 Power，为每个类别和名称选择最新版本，并输出精确格式 `<type>/<name>@<version>`，有 description 时在身份行下追加两空格缩进的 `description`（默认展示、不展示 tags）。例如：

```text
skill/foo-before-summary@v0.1.0
  description: 云函数事件前置汇总
```

Search 只接受可选的 `--type` 与 `--json`，不接受 `--agent`，也不再接受 `-a/--all`。全局选项 `--verbose` 输出解析和 journal 检查诊断到标准错误；帮助信息可查看每个子命令支持的参数。

Search 与两种 List 在原查询前都会无锁检查唯一活动 journal。合法活动 journal 会在 stderr Warn 并提示 `run apm fix`；journal 损坏或不可读时只 Warn，不附加 Fix 提示，随后继续原只读业务。检查错误不改变原命令的业务结果或退出状态。

List 已实现两种独立模式（命令边界见 [ADR-002](ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-021](ivory-tower/adr/ADR-021-cache-versioned-resource-catalog.md) 与 [ADR-027](ivory-tower/adr/ADR-027-list-cloud-latest-only.md)）：默认 Cloud List 在 Inspect 和一次 Catalog Snapshot 同步后对每个 type/name 只输出最高 SemVer 版本的精确行：

```text
<type>/<name>@<version>
```

Installed List 提供 `--installed`，`--agent` 可选：具体 `--agent=<agent-id>` 无锁刷新 Agent 并读取清单后输出不含 Agent 前缀的精确行；省略 `--agent` 或 `--agent=all` 则按 Agent ID 字典序分组缩进输出全部受支持 Agent，未安装与空清单分别输出 `not installed` / `no powers installed`：

```text
<type>/<name>@<version>
```

```text
<agent>
  <type>/<name>@<version>
```

两种模式都接受可选位置参数 `name`（`list [name]`），按区分大小写的原始字符串精确匹配 `PowerRef.Name`；例如 `./apm list foo-before-summary` 只输出名为 `foo-before-summary` 的记录（Cloud 每个 type 一条、最新版本），`./apm list foo-before-summary --all` 展开该名称的全部 Cloud 版本，`./apm list foo-before-summary --installed --agent codex` 只输出该 Agent 下的同名记录。`--all`/`-a` 未提供 name 时会被拒绝。

`show <name>` 只查询 Cloud catalog，默认显示最新版本；`--version=vMAJOR.MINOR.PATCH` 选择精确版本，跨类型同名时提供 `--type`。文本显示 type、name、version、description、tags；`--json` 输出单个对象。

例如 `./apm list --agent all` 是无效组合：它会非零退出，并在 stderr 报告 `--agent requires --installed` 和 usage。`--agent=all` 的 installed 读取若出现部分失败，会输出成功记录后以非零状态报告错误；fatal error 不输出业务行。已安装清单版本必须精确为 `vMAJOR.MINOR.PATCH`，APM 不会迁移或清理非法旧清单（[ADR-007](ivory-tower/adr/ADR-007-use-stateless-config-repository.md)）；Agent 刷新顺序见 [ADR-004](ivory-tower/adr/ADR-004-discover-installed-agents-before-local-operations.md)。

只读输出是 best-effort 观察，不是覆盖 journal、catalog、manifest 与 Agent target 的全局一致快照：默认 List/Search 只在 catalog 同步阶段短时获取锁，Inspect 与 Snapshot 之间、释放锁与渲染之间仍可能发生状态变化；`list --installed --agent=all` 全程不获取锁，也可能混合不同时间点的完整 manifest，并短暂观察到 target 与 manifest 不匹配。若业务需要全局一致读取，必须另行引入覆盖所有资源的锁或 MVCC；当前命令不提供该保证。

Fix 是显式恢复命令。它不接受位置参数或 Agent/Type 等命令专属选项，在一次全局锁内恰好调用一次 Recover 后退出，stdout 始终为空：

```bash
./apm fix
./apm --verbose fix
```

没有活动 journal 时普通模式静默成功，verbose 模式在 stderr 输出 Debug；恢复成功时输出 `transaction recovered` Warn。损坏、未知或不可读 journal 无法自动修复，Fix 会保留原文件和现场、记录 Error 并非零退出；不要手工删除 journal，除非已人工确认事务状态和数据所有权。

Install 已实现完整生命周期：显式版本必须是规范 `vMAJOR.MINOR.PATCH`，Name 按区分大小写的原始值精确匹配；同名候选跨 Type 时必须在版本选择前提供 `--type`。Install 从一次 Acquire 开始，持锁完成 Recover、Warn、Agent 刷新、`SnapshotLocked` 和串行 Transaction；Snapshot 一次解析出精确 `CatalogEntry`，该 entry 的 Ref 交给 Adapter/Transaction/manifest，完整 metadata 则原样传给四类内容边界。Skill 与 Agent 分别由 Cache 的 `PrepareSkill(entry)`/`PrepareAgent(entry)` 管理下载、版本化 cache 和 staging（Agent 为完整目录发布，仅 Bibit），MCP 通过 `GetMCP(entry)` 读取定义并调用目标 Agent CLI。Plugin 当前语义见 [Feature 020](ivory-tower/features/020-cloud-client-providers/spec.md)、[ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 与 [ADR-021](ivory-tower/adr/ADR-021-cache-versioned-resource-catalog.md)：Plugin Resource 通过 `DownloadPlugin(entry, destination)` 下载 canonical APC Plugin，从 `apc-plugin.json` 派生并原子发布共享制品，再由目标 Agent 消费 marketplace 或 filesystem staging。只有成功 Release 后 CLI 才渲染。

四类 Power 的安装入口状态语义由 [Feature 025](ivory-tower/features/025-unified-lifecycle-state-semantics/spec.md) 与 [ADR-022](ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 统一：同版本在目标探测前直接返回 `unchanged`；不同版本且目标完整时原子替换为 `upgraded`/`downgraded`；不同版本且目标缺失或不完整时记录一次 `installed power target is missing` warning，先经 repair transaction 清理仍存在的受管残留并删除旧清单，再以首次安装语义安装新版本，成功只输出 `installed`；无清单但存在运行目标时拒绝接管，唯一公共业务原因是 `adapter.ErrTargetConflict`。显式规范 Type/Name/Version 在所有目标 Agent 清单精确命中时四类 Power 都直接返回 `unchanged`，不访问 catalog、Cloud、cache、Plugin Resource、Adapter 或 Transaction。

每个目标 Agent 的 stdout 精确为以下六种 outcome 之一：

```text
<agent>/<type>/<name>@<version> installed
<agent>/<type>/<name>@<version> upgraded
<agent>/<type>/<name>@<version> downgraded
<agent>/<type>/<name>@<version> unchanged
<agent>/<type>/<name>@<version> failed
<agent>/<type>/<name>@<version> unexecuted
```

`--agent=all` 按 Agent ID 字典序 fail-fast；只有业务失败后又成功 Release 的批处理结果会先渲染 `failed/unexecuted` 行、再非零退出。fatal 或 Release error 不输出业务行。

Uninstall 已实现完整生命周期。它不接受版本；例如 `./apm uninstall foo-before-summary=v0.0.1 --agent codex` 会在创建 lifecycle 前报告 `uninstall does not accept a version`。省略 `--type` 时，每个 Agent 分别从自己的本地清单解析 Type 和 Version；同类别同名重复取清单第一条，跨 Type 同名则按去重类别报歧义并要求显式提供 `--type`。卸载成功后，该 Type/Name 的全部重复清单记录都会被清除。

Uninstall 在一次 Acquire/Release 之间完成 Recover、恢复 Warn、Agent 刷新、逐 Agent 清单解析和串行 Transaction。清单无记录时同步 Warn、记为 Agent-only `unexecuted` 并继续；清单有记录但 target 已缺失时，Transaction 仍移除该清单项，lifecycle 同步 Warn 并记为完整引用 `completed`。命令构造和执行都不选择 provider、不连接 Cloud、不读取 catalog。Plugin Uninstall 只通过只读 `Locate`/`Lookup` 复验 manifest 指向的本地精确 artifact，既不 `Ensure`、不下载，也不重建 cache；卸载同时删除 Plugin、注销专属 `apm-<name>` marketplace 并删除 manifest，但保留共享 artifact cache。卸载入口状态同样由 [Feature 025](ivory-tower/features/025-unified-lifecycle-state-semantics/spec.md) 与 [ADR-022](ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 统一：无清单不检查目标并返回 `unexecuted`；有清单且目标缺失或不完整时记录一次 warning、清理残留与清单并返回 `completed`；事实冲突或状态不确定仍 fail closed。MCP Uninstall 只使用 manifest 的精确 Ref 与目标 Agent CLI：target absent 时直接清理 manifest；target present 时创建不含 Server definition 的卸载 journal 并按名称删除。Fix 恢复时再次 Inspect，present 就继续 Remove，absent 就提交 manifest 删除，始终收敛到“已卸载”且永不 Add；MCP 安装/版本切换 journal 仍保留定义以支持安装回滚。决策边界见 [ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-009](ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md) 与 [ADR-021](ivory-tower/adr/ADR-021-cache-versioned-resource-catalog.md)。

stdout 只有两种精确行形式：

```text
<agent>/<type>/<name>@<version> <completed|failed|unexecuted>
<agent> <failed|unexecuted>
```

例如成功卸载可输出 `codex/skill/foo-before-summary@v0.0.1 completed` 或 `codex/mcp/filesystem@v0.0.1 completed`；重复执行时可输出 `codex unexecuted`。`--agent=all` 按 Agent ID 字典序 fail-fast：首个业务失败后，当前 Agent 记为 `failed`、剩余 Agent 记为 `unexecuted`。只有成功 Release 后才渲染汇总；Release error 抑制全部业务行。

MCP 支持 Bibit、Codex、Claude Code 和 Hermes Agent。Cloud 定义采用互斥结构：本地 stdio 使用 `transport=stdio`、`command`、`args`，远程端点使用 `transport=http`、`url`。Claude Code 固定写入 user scope；Bibit 由独立 Adapter 调用 `bianbu-agent mcp`，不与 Codex 共享命令协议实现。所有同名检查、安装、卸载和恢复都通过目标 Agent CLI 完成。CLI 从当前 `PATH` 动态解析，不经 shell，30 秒超时，输出有界；解析或状态不清晰时 fail closed 并保留共享 journal。Hermes 正常 Add/Remove 后按设计不额外执行 list。

### Agent 能力矩阵

| Agent ID | Skill | MCP | Plugin | Agent |
| --- | --- | --- | --- | --- |
| `bibit` | `<BIANBU_AGENT_HOME>/skills/<name>` | 独立 `bianbu-agent mcp` Adapter | `<BIANBU_AGENT_HOME>/plugins/<name>`；目录发布，不调用 Plugin CLI | `<BIANBU_AGENT_HOME>/agents/<name>`；完整目录（`agent.yaml` + `assets/`） |
| `claude-code` | `<CLAUDE_CONFIG_DIR>/skills/<name>` | Claude CLI user scope | 共享制品中的 Claude 本地 marketplace | 不支持 |
| `codex` | `<CODEX_HOME>/skills/<name>` | Codex CLI | 共享制品中的 Codex 本地 marketplace | 不支持 |
| `hermes-agent` | `<HERMES_HOME>/skills/<name>` | Hermes CLI | `<HERMES_HOME>/plugins/<name>`；Hermes CLI enable/disable | 不支持 |

Bibit profile 根优先使用 `BIANBU_AGENT_HOME`，未设置时为 `<有效 HOME>/.bianbu-agent`；根目录存在且为目录才进入本次命令的已安装 Agent 快照。Bibit 接口与路径约束见 [Feature 018](ivory-tower/features/018-bibit-agent/spec.md)，当前 Plugin canonical/派生边界见 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

Agent Power 仅 Bibit 支持（[Feature 019](ivory-tower/features/019-agent-power/spec.md) 与 [ADR-015](ivory-tower/adr/ADR-015-agent-power-bibit.md)；当前打包与目录契约见 [Feature 026](ivory-tower/features/026-agent-package-assets/spec.md) 与 [ADR-023](ivory-tower/adr/ADR-023-package-agent-with-assets.md)，云端契约见 APC ADR-006）：Agent 权威 content 是确定性 `.tar.gz`，归档根即资源根（必需 `agent.yaml` + 可选 `assets/`，无版本包裹目录）；Local canonical fixture 布局为 `agents/<name>/<version>/agent.yaml + assets/`，Remote exact content 为 `application/gzip` 归档，解包后执行权威 Agent 目录校验。Cache 的 `PrepareAgent` 把完整目录按精确版本发布到 `cache/agents/<name>/<version>` 并物化 `staging/agents/<name>`；本地安装到 `<bibit根>/agents/<name>`（完整目录、不含版本层级，版本仅记 manifest）。外部身份使用 `agent.name`，`agent.id` 仅供 Bibit 内部区分。`agent.metadata.avatar` 可选：存在时必须引用 `assets/` 下真实 PNG 普通文件——大小 ≤ 1 MiB、宽高均为 128..1024 像素且为正方形。`--type agent` 的 `--agent` 规则：install/uninstall 省略 `--agent` 时默认 `bibit`；`list --installed` 省略 `--agent` 时默认 `bibit`；`search`/`list` 的 Cloud 模式仅过滤结果、不要求 `--agent`。显式 `--agent bibit` 允许，其它值（含 `all`）报错。例如 `./apm install bianbu-agent-default=v1.0.0 --type agent`。

### Plugin 共享 artifact 与操作顺序

Plugin 当前架构以 [Feature 020](ivory-tower/features/020-cloud-client-providers/spec.md)、[ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 和 [ADR-021](ivory-tower/adr/ADR-021-cache-versioned-resource-catalog.md) 为准；[ADR-014](ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅解释共享不可变 artifact 的历史来源。Cloud 不托管 marketplace 或 Agent 原生配置；`DownloadPlugin(ctx, entry, destination)` 使用同一 CatalogEntry，把 `apc-plugin.json + skills/...` canonical package 写入调用方目录。Plugin Resource 每个逻辑 Plugin 版本只下载一次，在唯一 cache-miss 临时根内生成并复验派生内容后，以以下布局原子发布共享、不可变制品：

```text
cache/plugins/<logical-name>/<version>/
├── .agents/plugins/marketplace.json
├── .claude-plugin/marketplace.json
└── plugins/<logical-name>/
    ├── apc-plugin.json
    ├── .codex-plugin/plugin.json
    ├── .claude-plugin/plugin.json
    ├── .hermes-plugin/plugin.yaml
    ├── .hermes-plugin/__init__.py
    └── skills/...
```

所有 Agent 的 Plugin 运行时名称和 `PluginTarget` selector 都是逻辑 `<name>`，Plugin 叶子固定为 `plugins/<name>`；版本只由 `PowerRef`、APM manifest、缓存版本目录和原生 manifest 表达。`Artifact.MarketplaceFor(agent)` 为 Codex/Claude 返回同一共享根上的 Agent 原生 marketplace；Hermes/Bibit 通过 `StageFilesystem(ref)` 物化同一 Plugin 叶子。

Install 在锁内恢复和 Agent 选择后先处理显式规范 ref（四类 Power 通用）：每个所选 Agent 的 manifest 都精确命中时直接返回 `unchanged`，不会调用 Cloud、Plugin Resource、Adapter 或 Transaction；任一不匹配则回到既有 Cloud 解析路径。普通 Plugin 路径对每个 Agent 先读取 manifest，再统一调用 `ResolvePlugin(agent)`，随后按 `DeploymentMode` 分支。marketplace 分支先 `Locate` 目标（不同版本为旧版本）artifact 并 `InspectMarketplace`/`Inspect` 得到 Plugin 与 marketplace 事实，再以共享状态策略决策：无 manifest 且目标与 marketplace 都 absent 才全新安装；无 manifest 但存在任一 Plugin/marketplace 事实时按公共 `adapter.ErrTargetConflict` 拒绝；不同版本且 Plugin/marketplace/source 完整时执行原子版本切换，缺失或不完整但能证明归 APM 管理时记录一次 warning、先卸载清理再以目标 `CatalogEntry` `Ensure(ctx, entry)` 全新安装，身份或来源冲突仍 fail closed。filesystem 分支先完成旧目标或新目标的本地事实与物理缺失校验，再按相同状态策略决策，之后才以目标 `CatalogEntry` 调用 `Ensure(ctx, entry)`，执行 `StageFilesystem(ref)` 和 transaction。marketplace 首装注册目标 source、添加逻辑名 Plugin 并写 manifest；卸载已同时注销 marketplace，因此任意版本重装都按正常注册流程执行，artifact 缓存命中时不重新下载、缺失时才从 content source 获取。版本切换仍删除旧 Plugin、切换同名 marketplace source、重新添加逻辑名 Plugin 并写 manifest。Hermes/Bibit 使用同一个 `plugins/<name>` 目标执行可恢复 filesystem transaction；Hermes 保留 enable/disable，Bibit 不执行 Plugin CLI。

Uninstall 先从 manifest 取得精确版本；没有记录时直接返回未执行，不检查 Agent。Codex/Claude 先检查该 marketplace 与 Plugin，再离线 `Lookup` 并验证共享 artifact/source/selector，最后在 transaction 中移除 Plugin、注销专属 marketplace 并移除 manifest，同时保留共享 artifact；Hermes/Bibit 只处理逻辑名目录与 manifest。marketplace/Plugin 部分状态按新矩阵收敛：能证明归 APM 管理的缺失/不完整按缺失清理，source 或 selector 不匹配、artifact 缺失或损坏、Agent 事实不可读以及非法 journal 都会保留 manifest、artifact 和 journal 并 fail closed。旧版 Plugin 卸载 journal 若停留在 `rolling-back` 阶段，升级后会以 `ErrInvalidJournal` 暴露，需确认状态归属并删除残留 journal（spec §10 不提供兼容开关，此说明仅为可发现性）。Plugin Resource 与 recovery 共用中立只读 Validator，拒绝 symlink/特殊文件，并按消费路径复验原生 manifest 与已生成 marketplace；`fix`/recovery 只读取 journal、Adapter、manifest 和既有共享 artifact，绝不连接 Cloud、调用 Plugin Resource 或重建缓存，因此支持离线恢复。

Skill、Agent、MCP 与 Plugin 共用唯一活动 journal。唯一正式 schema v1 shared envelope 通过 `filesystem`、`agent-cli` 与 `plugin` driver 路由，并且只允许与 driver 匹配的互斥载荷；旧平铺或其他格式返回 `ErrInvalidJournal`，文件原样保留。Search 与两种 List 只告警；Fix、Install 或 Uninstall 才在全局锁内串行恢复未完成事务。Fix/recovery 只读取 journal、Adapter、manifest 和已发布的本地 artifact，不选择 provider、不连接 Cloud、不重新下载或派生内容。版本边界见 [ADR-011](ivory-tower/adr/ADR-011-reset-journal-schema-before-release.md)，当前 Plugin 架构见 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

自动化测试绝不能调用真实 Agent CLI。MCP/Plugin 测试应只把临时 fake-bin 置于 `PATH`，将 `APM_HOME`、`XDG_CACHE_HOME`、`CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`HERMES_HOME`、`BIANBU_AGENT_HOME` 全部指向 `t.TempDir()` 下的隔离路径，并断言每次执行路径都位于 fake-bin。仓库集成测试已按此约束覆盖四个 Agent 的 Plugin 首装、幂等、升级、降级、卸载和中断恢复；不得调用真实 `bianbu-agent` 或其他 Agent CLI。

## 清理本地构建产物

删除由本指南生成的可执行文件：

```bash
rm -f ./apm
```

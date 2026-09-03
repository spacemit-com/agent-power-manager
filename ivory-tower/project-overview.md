# Project Overview

> 本文档是项目设计的起点。先完成这里的关键信息，再建立 C4 模型、ADR 和 feature 设计。

## Introduction

- 项目名称：Agent Power Manager，简称 APM
- 交付目标：面向多 Agent，统一管理 Skill、Plugin、MCP、Agent 等 Power，提供能力发现、分发、安装和配置治理，并通过 Adapter 适配不同目标 Agent 的本地命令行工具。
- 代码工程：`apm`之下，`ivory-tower`仅作为文档归档目录。
- 当前状态：Search、List、Install、Uninstall 和 Fix 已通过独立 lifecycle 端口接线；Bibit、Codex、Claude Code 与 Hermes Agent 均支持 Skill、MCP 与 Plugin，Bibit 还支持专属 Agent Power（[Feature 019](features/019-agent-power/spec.md) 与 [ADR-015](adr/ADR-015-agent-power-bibit.md)；Agent 当前以确定性 `.tar.gz` 目录资源发布，见 [Feature 026](features/026-agent-package-assets/spec.md) 与 [ADR-023](adr/ADR-023-package-agent-with-assets.md)）。Cloud Client 已通过显式 `ProviderRegistry` 支持 Local canonical fixture 与 Remote APC（[ADR-016](adr/ADR-016-select-cloud-clients-with-explicit-providers.md)）；公开资源发现已收敛为内容寻址的完整 catalog，并由独立 Manager 持久化到 `~/.apm/catalog.json`（[Feature 024](features/024-catalog-cache/spec.md)、[ADR-021](adr/ADR-021-cache-versioned-resource-catalog.md)）。Plugin 以 `apc-plugin.json` 为权威并由 APM 派生 Agent 原生配置（[ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)）。journal 正式格式见 [ADR-011](adr/ADR-011-reset-journal-schema-before-release.md)。Submission 已通过统一 Source Loader 覆盖 Skill、Plugin、MCP 与 Agent：身份和描述只取自各自权威资源文件，`submit` 统一接收 Submission `message`，MCP 上传完整 v1 `MCPResource`，开发者 get/list 使用独立的 API Key 认证请求（[Feature 023](features/023-submission-resource-metadata-query/spec.md)、[ADR-020](adr/ADR-020-source-owned-submission-metadata.md)，云端契约见 APC ADR-003）。四类 Power 的 install/uninstall 入口状态语义已由 [Feature 025](features/025-unified-lifecycle-state-semantics/spec.md) 与 [ADR-022](adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 统一。
- 最小业务上下文：用户通过APM从云服务发现所需 Power，APM根据目标Agent将通用Power格式适配并安装到对应目录。

Power配置记录、文件锁等治理功能，不替代目标 Agent 的运行时。

## Scope

- 包含：
    - 提供 `search`、`list`、`install`、`uninstall`、`fix`、`validate`、`submit`、`submission get|list` 等 CLI 能力。
    - `search <keyword>` 搜索云端资源；`list` 默认列出云端资源，`list --installed` 列出本地安装清单。
    - `install <power>[=<version>]` 未指定版本时选择云端最新版，相同版本幂等成功，不同版本执行升级或显式降级。
    - 生命周期安全，在任意执行阶段中断，系统都可以识别并纠正。
    - Search、默认 List 和 installed List 在原只读业务前无锁 Inspect 唯一活动 journal：合法 journal Warn 并提示 `apm fix`，损坏或不可读 journal Warn 后继续；三者都不执行 Recover。默认 List/Search 随后仅在 catalog 同步阶段短时、非阻塞获取 `apm.lock`，installed List 全程不获取锁。
    - 默认 `list` 在 Inspect 和一次 Catalog Snapshot 同步后列出全部版本；`search` 从同一类不可变 Snapshot 本地过滤并选择最新 SemVer；`list --installed` 无锁刷新已安装 Agent 并读取 Config。`--agent=all` 继续输出成功记录和 partial error，但不承诺跨 catalog、Agent manifest、journal 与 target 的全局一致快照。
    - `cmd/apm` 的共享惰性 Catalog Manager 在默认 List/Search/Install 首次需要目录时才选择 `local|remote` provider；默认 Remote，错误不自动 fallback Local。installed List、Uninstall 和 Fix 不构造 provider。Install 仍为精确内容获取构造所选 Client，但四类 Power 显式精确 manifest 全命中时目录调用为零。
    - installed List、Install 和 Uninstall 在本地操作前扫描已注册 Agent 的根目录，生成当前命令内按 Agent ID 字典序的“已支持且已安装 Agent”快照；installed List 的快照是无锁观察，Install/Uninstall 的快照位于各自锁区间内。
    - Fix 在一次全局锁区间内恰好 Recover 一次并退出；没有 journal 时普通模式静默、verbose 模式 Debug，恢复成功时同步 Warn。损坏 journal 不能自动修复，Fix 会保留现场并非零退出。
    - Install 在一个锁区间内完成 Recover、Warn、Agent 快照刷新、`SnapshotLocked` 和串行 Transaction；Snapshot 一次解析精确 `CatalogEntry`，Skill 与 Agent 由 Cache 的 `PrepareSkill(entry)`/`PrepareAgent(entry)` 独占版本化 cache 发布与 staging（Agent 为完整目录、确定性 tar.gz、仅 Bibit），MCP 由 `GetMCP(entry)` 直接取得定义；Plugin Resource 使用同一 entry 从 canonical APC package 生成或复验一份共享不可变 artifact。成功 Release 后才返回 outcome。四类 Power 的入口状态由 [ADR-022](adr/ADR-022-unify-lifecycle-entry-state-semantics.md) 的共享状态策略统一：同版本不探测即 `unchanged`；不同版本加缺失/不完整目标记录一次 warning 后 repair 再全新安装；无清单目标冲突只暴露 `adapter.ErrTargetConflict`；显式精确 manifest 全命中四类 Power 都直接 `unchanged`。
    - Uninstall 在一个锁区间内完成 Recover、Warn、快照刷新、逐 Agent 本地清单解析和串行 Transaction；命令构造与执行均不选择 provider、不读取 catalog。Plugin 卸载只通过只读 `Locate`/`Lookup` 复验 manifest 指向的精确 artifact，并同时删除 Plugin、注销专属 marketplace、删除 manifest，但保留共享 artifact cache；MCP 只依据 manifest Ref 与 Agent CLI presence 删除，卸载 journal 不携带 definition，恢复永远收敛到 absent。
    - Skill、Agent、MCP 与 Plugin 共用唯一 `transactions/active.json`（Agent 复用 `filesystem` driver，target 为文件系统路径，Agent 为完整目录）。schema v1 shared envelope 以 `filesystem`/`agent-cli`/`plugin` driver 区分互斥载荷；其他格式返回 `ErrInvalidJournal` 并原样保留。
    - 实现类似apt的文件锁，保证软件执行安全。
    - 缓存下载的 Power；另以唯一 `~/.apm/catalog.json` 持久化严格、内容寻址的资源目录，目录缓存不属于 Skill/Plugin/Agent 制品缓存，MCP 仍无制品缓存。
    - `validate <path> --type skill|plugin|mcp|agent [--json]` 通过四类 Source Loader 完全离线校验格式、资源内身份、物理树与阈值；Skill/Plugin/MCP/Agent 的 name/version/description 分别只来自 `SKILL.md` frontmatter、`apc-plugin.json`、完整 v1 `MCP.json` 和 `agent.yaml`，不接受 CLI version 覆盖。
    - `submit <path> --type skill|plugin|mcp|agent --message ...` 复用 Source Loader，要求四类统一的非空 message；校验通过后构造 Skill/Plugin/Agent 确定性 tar.gz 或完整 MCPResource JCS JSON，计算不含 message 的 content MD5 并上传。APC 响应输出 `outcome: accepted`（首次 `202`）或 `outcome: replayed`（`200` 幂等重放）；`409 ALREADY_PUBLISHED` 不伪造新 Submission ID。
    - `submission get <id> [--output text|json]` 展示 description、message、状态、时间、公开校验/评审结果和原样 timeline；`submission list [--status ...] [--type ...] [--name ...] [--output text|json]` 只读取当前账号最多 20 条 active Submission，按 created_at 倒序且无分页。
    - submit 与 submission get/list 经 `APC_API_KEY`/`APC_BASE_URL` 配置凭据与地址，并使用独立的 `X-API-Key` 认证请求；匿名 catalog version/full-catalog/content 请求不携带 Key。Submission 认证、协议或网络错误不使用目录 stale fallback；目录同步失败则仅按 ADR-021 在已有严格有效快照时降级。
    - 可增添的Agent Power Adapters。
    - 已安装Power记录。
- 不包含：
    - 替代目标 Agent 的运行时，或定义目标 Agent 如何执行已安装的 Skill、Plugin、MCP、Agent 配置和指令。
    - APC 服务端、数据库、对象存储、发布管线、工单网站或内网审批接口的实现；本 MVP 只消费 APC 的提交与查询接口。

## Architecture Snapshot

- 系统边界与外部依赖：Agent Power Manager 位于 APC/开发 fixture、能力使用方与目标 Agent 之间；Remote Provider 通过 APC 匿名只读 HTTP API 获取 catalog SHA-256/size、完整确定性 catalog 与精确版本内容，Local Provider 从开发 fixture 生成相同 schema 的 catalog。目标 Agent 自身的运行时位于 Agent Power Manager 边界之外。
- 模块边界与职责：
    - Composition Root：`cmd/apm/cloud.go` 的 `newCloudClient` 直接 import `internal/cloud/local` 与 `internal/cloud/remote`，每次把两者的构造 closure 注册到实例级 registry，再依据环境只 `Open` 一个 provider。Registry 保持中立，只按 ID 选择 factory 并返回 `cloud.Client`。一个命令入口共享惰性 Catalog Manager；Search/List 首次 Snapshot 才构造目录 source，Install 另把所选 Client 注入 Cache 与 Plugin Resource，Uninstall/Fix composition 不包含 Cloud。
    - Cloud Client：`internal/cloud.Client` 由 `CatalogSource`、`SkillSource`、`PluginSource`、`MCPSource`、`AgentSource` 与 `Submission` 六个小端口组成。CatalogSource 只提供 version 和完整文件下载；四类内容方法只消费已经验证的 `CatalogEntry`，不得按 Ref 二次查询 metadata。provider 只验证和写调用方私有目标，不解释 APM cache、staging、manifest 或 journal。`Submission.Submit` 返回统一 `domain.SubmissionResult{Outcome, SubmissionID, Status, StatusURL}`；`ListSubmissions` 只接收 status/type/name 并返回至多 20 条 active 摘要，`GetSubmission` 返回含 message 和 `[]SubmissionEvent` 的完整详情。
    - Catalog Manager / Repository：`internal/catalog.Manager` 在 `Snapshot` 与受约束的 `SnapshotLocked` 两个入口共享同一同步状态机；前者非阻塞获取全局锁，后者要求 install 已持锁且绝不重入。Repository 独占 `~/.apm/catalog.json` 的有界读取、严格四分组 schema、SHA-256/size、`0600` 与原子替换；Snapshot 是无 I/O、返回 clone 的不可变查询值。Provider/update 失败只有在旧文件严格有效时才返回一次 stale 状态，lock/cancellation/local-security 失败不降级。Repository 平台矩阵为：Linux/macOS 支持 `Load` 与 `Replace`；FreeBSD 支持 `Load`，但 `Replace` 返回 `ENOTSUP` 并 fail closed；Windows 与其它平台的 `Load`/`Replace` 都 fail closed。该矩阵独立于 Remote content 的平台能力（[ADR-021](adr/ADR-021-cache-versioned-resource-catalog.md)）。
    - Submission Source Loader：`internal/submission` 为 Skill、Plugin、MCP、Agent 注册显式 Loader；每个 Loader 从权威文件读取并校验身份、描述和类型专属内容，公共编排统一处理 message、20 MiB 上限、content MD5 与上传，未知类型 fail closed。该协议只服务 Submission，不扩展安装、缓存或 transaction lifecycle。
    - Provider Registry：`internal/cloud.ProviderRegistry` 是显式实例，不使用 package `init()` 或全局可变注册表，也不 import Local/Remote；它只保存 factory、选择 ID、构造并返回 `cloud.Client`。Local 与 Remote 都实现完整 `cloud.Client`，由 composition root 静态链接在单一二进制中；配置、网络或协议错误都不尝试另一 factory。
    - Local Provider：`internal/cloud/local` 从绝对 canonical fixture 根读取 `skills/`、`plugins/`、`mcps/`、`agents/`，以实际可下载字节生成确定性四分组 catalog；精确内容调用先验证 caller entry，再从新的物理 snapshot 取得同一 metadata/内容。所有来源拒绝 symlink、hardlink、特殊节点、路径逃逸、重叠和非空目标。
    - Remote Provider：`internal/cloud/remote` 匿名调用 `GET /api/v1/resources/version` 与无 query 的 `GET /api/v1/resources`，不再消费旧的公开分页与逐项发现 endpoint；精确 content 仅使用 caller `CatalogEntry` 校验 HTTP content type、size、MD5 后才解包或解码。Skill/Plugin/Agent 使用受限安全归档（Agent 为 `application/gzip` 目录归档，解包后执行权威 Agent 目录校验），MCP 严格解码并校验完整 v1 `MCPResource` 后返回 definition。Linux/macOS 支持完整目录发布；FreeBSD 支持 descriptor-relative staging 和 MCP，但 Skill/Plugin/Agent 目录发布返回 `ENOTSUP`；Windows/其它平台 content 操作 fail closed。匿名 catalog/content 不携带 API Key，Submission 上传和 active-only get/list 使用独立 `X-API-Key` 认证请求；不自动重试、不跨 origin redirect。
    - 核心生命周期：提供独立的 Search、List、Install、Uninstall、Fix、Validate、Submit 与 Submission（get/list）用例。Search/默认 List 在无锁 Inspect 后通过 Manager 短时同步 catalog，再从 Snapshot 返回结构化结果；installed List 只读本地。Fix/Install/Uninstall 才执行 Recover，Install 在既有锁内使用 `SnapshotLocked`，Uninstall/Fix 的整个调用图不包含 catalog 或 Cloud。
    - 配置模型：通过无状态 Repository 维护每个 Agent 按 Skill、Agent、Plugin、MCP 分组的 Power 安装清单。
    - Cache Manager：`internal/cache` 通过包内 `Source` 小接口依赖 Cloud Client；`PrepareSkill(entry)`/`PrepareAgent(entry)` 负责 cache hit 复验、唯一临时目标、把同一权威 CatalogEntry 透传给下载、按精确版本原子发布和公共 staging。Agent 首次下载与缓存命中都对完整目录执行 `internal/agentartifact` 权威校验（内部复用 `domain.DecodeAgentConfigYAML` + `ValidatePowerRef`），损坏或不匹配时 fail closed。Cloud provider 不直接写 `apm.powerCache`，Catalog Manager 也不拥有制品缓存。
    - Plugin Resource：`internal/pluginresource` 独占 canonical APC Plugin 下载、`cache/plugins/<name>/<version>` 共享不可变 artifact、Codex/Claude 两份单条目本地 marketplace 和通用 filesystem staging。它通过 `internal/pluginartifact` 严格解析 `apc-plugin.json` 的 Skill 闭集，先确定性生成 Codex、Claude、Hermes 原生配置，再让 marketplace builder 读取并复验 Codex/Claude 原生 manifest 后生成两份 marketplace；Transaction recovery 共用同一中立只读 Validator，但不调用 Plugin Resource。
    - Adapter Registry：按 Agent 分别提供 `ResolveSkill`、`ResolveAgent`、`ResolveMCP`、`ResolvePlugin`；其中 `ResolveAgent` 仅 Bibit，对应 `<bibit根>/agents/<name>` 完整目录发布（`agent.yaml` + `assets/`）。`AgentAdapter` 增加只读 `ValidateArtifact(domain.PowerRef, string) error`，供 staging/target/backup/quarantine 完整目录身份校验；`bibitAgentAdapter.Install` 在发布最终目标前对完整目录执行权威校验。Codex/Claude Plugin Adapter 管理本地 marketplace；Hermes/Bibit Plugin Adapter 管理逻辑名目录，其中 Hermes 通过 CLI enable/disable，Bibit 不执行 Plugin CLI。Bibit MCP 使用独立 Adapter；MCP/Plugin 都不追加安装后的 list 验证。
    - CLI：提供入口，校验并解析命令参数；Search、List、Install、Uninstall、Fix、Validate、Submit 与 Submission 通过八个独立 factory 创建专属端口。CLI 渲染结构化结果；Fix 没有 stdout 业务结果，Validate 支持 `--json` 机器输出。
- 数据与接口：每个 Agent 单独维护一份固定 `skills`、`agents`、`plugins`、`mcps` 分组的 JSON 安装清单，条目统一为精确 `vMAJOR.MINOR.PATCH` 的 `PowerRef`。另有唯一 `~/.apm/catalog.json`：顶层和 `skills/plugins/mcps/agents` 四组分别维护 schema v1，组内条目按 name 升序、同名版本 SemVer 降序，文件原始字节 SHA-256/size 是唯一同步标记。Config Repository 在 `List/Find/Set/Remove` 内封装 manifest；Catalog Repository 则独占严格读取、物理安全和原子发布，两者不能混用。Search/默认 List 在业务前 Inspect 后短时同步目录；installed List 不同步。Fix/Install/Uninstall 才锁内 Recover，其中 Install 用 `SnapshotLocked` 解析精确 CatalogEntry，Uninstall/Fix 为零 catalog 调用。Skill/Plugin destination 是调用方私有目录，Agent destination 是调用方私有完整目录（`agent.yaml` + `assets/`），MCP 是无文件制品 cache 的领域对象。

Install 与 Uninstall 分别以独立 factory 和生命周期端口接线，不复用或耦合 Search/List 的锁边界。Install 显式版本必须为规范 `vMAJOR.MINOR.PATCH`，结果行精确为 `<agent>/<type>/<name>@<version> <outcome>`。Uninstall 拒绝版本，从每个 Agent 的本地清单独立解析 Type/Version，并输出完整 PowerRef 或 Agent-only 的结果行。
- 技术与运行方式：使用 Go 实现，交付为单个静态二进制。`APM_CLOUD_PROVIDER` 默认 `remote`，`APC_BASE_URL` 默认 `https://developer.bianbu.xyz`，`APC_API_KEY` 供 submit 与 submission get/list 使用；开发 fixture 需显式设置 `APM_CLOUD_PROVIDER=local` 与 clean absolute `APM_CLOUD_LOCAL_ROOT`。Catalog 固定为 `~/.apm/catalog.json`；Skill/Agent cache 与 staging 归 `internal/cache`，MCP 不进入制品 Cache；Plugin Resource 独占 `cache/plugins/<logical-name>/<version>` 的共享 artifact 与 filesystem staging。详见 [ADR-010](adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)、[ADR-011](adr/ADR-011-reset-journal-schema-before-release.md)、[ADR-016](adr/ADR-016-select-cloud-clients-with-explicit-providers.md)、[ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)、[ADR-021](adr/ADR-021-cache-versioned-resource-catalog.md) 与 [ADR-025](adr/ADR-025-apc-api-client-contract.md)。

### Agent 与 Power 能力

| Agent ID | Profile 根 | Skill | MCP | Plugin | Agent |
| --- | --- | --- | --- | --- | --- |
| `bibit` | `BIANBU_AGENT_HOME`，默认 `~/.bianbu-agent` | `skills/<name>` | 独立 `bianbu-agent mcp` Adapter | `plugins/<name>`；目录即状态，不调用 Plugin CLI | `agents/<name>`；完整目录（`agent.yaml` + `assets/`），仅 Bibit |
| `claude-code` | `CLAUDE_CONFIG_DIR`，默认 `~/.claude` | `skills/<name>` | Claude CLI user scope | 共享制品中的 Claude marketplace | 不支持 |
| `codex` | `CODEX_HOME`，默认 `~/.codex` | `skills/<name>` | Codex CLI | 共享制品中的 Codex marketplace | 不支持 |
| `hermes-agent` | `HERMES_HOME`，默认 `~/.hermes` | `skills/<name>` | Hermes CLI | `plugins/<name>`；CLI enable/disable | 不支持 |

Registry 仅以 profile 根存在且为目录判断 Agent 已安装；`--agent=all` 按 Agent ID 字典序使用当前快照。Bibit 的接口与路径约束见 [Feature 018](features/018-bibit-agent/spec.md)，Plugin 的当前 canonical/派生边界见 [ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

Agent Power 仅 Bibit 支持（[Feature 019](features/019-agent-power/spec.md) 与 [ADR-015](adr/ADR-015-agent-power-bibit.md)；当前打包与目录契约见 [Feature 026](features/026-agent-package-assets/spec.md) 与 [ADR-023](adr/ADR-023-package-agent-with-assets.md)，云端契约见 APC ADR-006）：Agent 权威 content 为确定性 `.tar.gz`，归档根即资源根（必需 `agent.yaml` + 可选 `assets/`，无版本包裹目录）；Local canonical fixture 为 `agents/<name>/<version>/agent.yaml + assets/`，Remote exact content 为 `application/gzip` 归档；Cache 的 `PrepareAgent` 将完整目录按精确版本发布到 `cache/agents/<name>/<version>` 并物化 `staging/agents/<name>`，最终安装到 `<bibit根>/agents/<name>`（完整目录、不含版本层级，本地不版本化）。外部身份使用 `agent.name`，`agent.id` 仅供 Bibit 内部区分。`--type agent` 的 `--agent` 规则：install/uninstall 省略 `--agent` 时默认 `bibit`，`list --installed` 省略 `--agent` 时默认 `bibit`，`search`/`list` 的 Cloud 模式仅过滤结果、不要求 `--agent`；显式 `--agent bibit` 允许，其它值（含 `all`）报错。

Agent 内容身份闭环（[ADR-019](adr/ADR-019-align-agent-submission-lifecycle.md)，当前目录契约见 [ADR-023](adr/ADR-023-package-agent-with-assets.md)）：`internal/agentartifact` 是 Agent 目录的唯一权威只读校验器，内部复用 `internal/domain` 的 `DecodeAgentConfigYAML`/`Validate`/`ValidatePowerRef` 作为 YAML 最小边界，submission、cache、Cloud 下载、Adapter 与 recovery 复用同一套规则（根闭集、avatar PNG、身份）。身份校验沿四层加固——domain parser → cache（首次下载与命中的 `PrepareAgent` 完整目录校验）→ adapter（`bibitAgentAdapter` 发布前 `ValidateArtifact`）→ recovery（filesystem 事务在写入 manifest 或恢复 target/backup/quarantine 前经 `AgentAdapterResolver` 校验）；任何一层身份不匹配或缓存损坏均 fail closed 并保留现场。

### Plugin 当前安装与卸载边界

当前 Plugin 语义以 [Feature 020](features/020-cloud-client-providers/spec.md)、[ADR-017](adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 和 [ADR-021](adr/ADR-021-cache-versioned-resource-catalog.md) 为准；[ADR-014](adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅作为被取代的历史背景。Cloud 的 `DownloadPlugin(ctx, entry, destination)` 使用同一 CatalogEntry，只把 canonical `apc-plugin.json + skills/...` 写入 Plugin Resource 提供的目标目录；Cloud 不生成 marketplace 或 Agent 原生配置。每个逻辑 Plugin 版本只有一份共享 artifact：

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

`apc-plugin.json` 严格定义 name、version、description 和声明 Skill 闭集。Plugin Resource 在唯一 cache-miss 临时根中保留 canonical package，先生成 Codex、Claude、Hermes 原生 manifest，再由 marketplace builder 读取并复验 Codex/Claude manifest 以生成两份 marketplace，经 `internal/pluginartifact` 复验后一次 rename 发布。Plugin 的运行时名称、`PluginTarget` selector 和制品叶子均使用逻辑 `<name>`；版本仅由 `PowerRef`、APM manifest、缓存版本目录与派生原生 manifest 表达。Install 的精确顺序是：锁内 Recover 与 Agent 选择后，显式规范 ref（四类 Power 通用）会先检查全部所选 Agent manifest；全部精确命中则立即 `unchanged`，不访问 Cloud、Plugin Resource、Adapter 或 Transaction，任一不匹配则回到既有 Cloud 解析路径。普通 Plugin 路径对每个 Agent 先读取 manifest，再统一 `ResolvePlugin(agent)` 并按 deployment 分支。Codex/Claude marketplace 分支在检查 Agent 事实前把 Snapshot 选出的目标 `CatalogEntry` 传给 `Ensure(ctx, entry)` 并取得目标 marketplace；无 manifest 时只检查目标 marketplace/Plugin，不查询旧 artifact，有 manifest 时依次检查旧 marketplace 与旧 Plugin 得到目标状态，再按共享状态策略决策：完整则执行原子版本切换，缺失或不完整但能证明归 APM 管理则记录一次 warning、先卸载清理再全新安装，身份或来源冲突仍 fail closed。Hermes/Bibit filesystem 分支先完成旧或目标 Plugin 的本地事实与物理缺失校验，之后才以目标 `CatalogEntry` 调用 `Ensure(ctx, entry)`，再执行 `StageFilesystem(ref)` 和事务发布。marketplace 首装执行“注册 source、添加逻辑名 Plugin、写 manifest”；卸载已同时注销 marketplace，因此任意版本重装都按正常注册流程执行，artifact 缓存命中时不重新下载。版本切换仍执行“删除旧 Plugin、切换同名 marketplace source、重新添加逻辑名 Plugin、写 manifest”。

Uninstall 先从 manifest 解析精确版本；没有记录时返回 Agent-only `unexecuted`，不查询 Agent。存在记录时检查该版本的 Adapter 事实；Codex/Claude 要离线 `Lookup` 共享 artifact 并验证 marketplace/source/selector 后，在 transaction 中移除 Plugin、注销专属 marketplace 并移除 manifest，同时保留共享 artifact；Hermes/Bibit 只处理逻辑名目录与 manifest。marketplace/Plugin 部分状态按新矩阵收敛：能证明归 APM 管理的缺失/不完整按缺失清理，身份或 source 不一致、旧 artifact 缺失或损坏、journal 非法或事实不可读时均保留现场和 manifest 并 fail closed。Plugin Resource 与 recovery 共用中立只读 Validator，验证 canonical manifest、派生原生配置、marketplace 和物理树；recovery 只读取 journal、Adapter、manifest 与既有 artifact，绝不连接 Cloud、调用 Plugin Resource 或修改缓存，因此可离线收敛。

## Test Filesystem Isolation

APM 支持仅供测试使用的 `APM_HOME` 环境变量，使测试能在临时目录中模拟 Agent 安装状态和完整的本地 Power 生命周期，不读写用户目录。该路径决策详见 [ADR-005](adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

### 测试与验收

- 路径单元测试覆盖 `APM_HOME` 未设置、空值回退、非空值优先于 HOME，lock 的隔离路径，以及缓存的 `XDG_CACHE_HOME > APM_HOME > 系统默认` 优先级。
- 修改环境变量的测试不得使用 `t.Parallel()`，避免共享进程环境互相污染。
- 在临时 `APM_HOME` 中创建伪 Agent 根目录，可分别模拟 Agent 已安装和未安装状态。后续生命周期测试在其中执行 Power 安装、卸载、清单读写、锁竞争、事务与恢复。
- MCP/Plugin 自动化测试还必须只使用临时 fake-bin 作为 `PATH`，把四个 Agent Home 和 CLI 状态指向同一临时测试根，并逐次断言实际可执行路径位于 fake-bin；禁止调用用户真实 Bibit、Codex、Claude Code 或 Hermes CLI。
- 要求所有变化均位于同一临时根的测试，必须清空 `XDG_CACHE_HOME` 以使缓存回退到 `APM_HOME`，或将 `XDG_CACHE_HOME` 也指向该测试根内；验收时必须证明没有读写预期边界之外的文件。

## Constraints and Quality

| 项目 | 要求或约束 |
| --- | --- |
| 性能与可靠性 | 命令执行中失败由 CLI 统一记录。Search/默认 List 仅在 catalog 同步阶段短时获取非阻塞全局锁，随后基于不可变 Snapshot 进行本地查询；installed List 全程无锁。有效旧 catalog 可在 provider/update 失败时 stale 降级并 warning，但锁竞争、取消和本地安全失败不降级。跨 journal/catalog/manifest/target 的全局一致读取仍不承诺。Fix、Install 与 Uninstall 的恢复由全局锁串行化（[ADR-003](adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-021](adr/ADR-021-cache-versioned-resource-catalog.md)）。 |
| 安全与合规 | MCP CLI 不经 shell，动态解析 PATH，单次调用固定 30 秒超时且 stdout/stderr 有界；状态输出不匹配时 fail closed 并保留 journal。MCP 定义不包含密钥字段，HTTP endpoint 只允许 HTTPS。Remote 以 CatalogEntry 验证 Content-Type、size、MD5、归档限制与已知归档/可执行签名。Catalog Repository 拒绝 symlink/hardlink、非私有 mode、不可信目录链和可检测替换；pathname 操作将 current EUID 视为受信任主体，advisory lock 只串行守约发布者，不承诺隔离恶意同 EUID 在最终 `fstatat`→`unlinkat` 窄窗的替换（ADR-021）。其它受信目录链同样不把同一 euid 主动篡改纳入隔离边界。 |
| 技术与交付 | APM 仅在相关命令中通过 Agent 根目录是否存在来感知安装状态，不承担目标 Agent 的安装、卸载、实时监控或运行时职责。 |

## Risks and Decisions

- 主要风险：
    - 不同目标 Agent 的安装位置与 Power 格式差异较大，Adapter 的支持范围和一致性可能受限。
    - Power 兼容性约束与兼容版本求解规则尚未确定，可能影响跨版本依赖与配置重建的可靠性；最新版选择、显式版本、升级/降级和相同版本幂等语义已由 ADR-002 确定。
    - 清单格式和本地文件权限已确定；跨来源内容冲突和更细粒度的权限策略尚未确定。
    - 损坏或不可读 journal 无法安全推断提交方向；只读命令只告警并继续，Fix/Install/Uninstall 保留现场并 fail closed，需要人工诊断。
    - Remote 失败不回退 Local，避免把开发 fixture 误表述为 APC 当前目录；已有严格有效 catalog 只按内容 hash 提供 stale 查询/已有制品安装，不能即时反映新下架状态。首次使用或本地 catalog 损坏时 APC 不可用仍会失败。
    - Remote content 的 descriptor-relative staging 覆盖 Linux、macOS 与 FreeBSD，但 FreeBSD 缺少目录 no-replace publication，Skill/Plugin 因 `ENOTSUP` fail closed；Windows 和其它平台 content 操作 fail closed。跨平台完整支持需要在保持同等物理目录约束后单独实现。
- 待决事项：
    - 确定 MVP 之后的框架、部署、安全校验与审计方案。

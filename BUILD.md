# 构建指南

本指南说明如何在本地构建、验证和运行 APM 命令行工具。

## 前置条件

- Git
- Go 1.26.5（版本以 [`go.mod`](go.mod) 为准）

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

## Cloud Provider 配置

APM 把 Local 与 Remote provider 静态链接到同一个二进制，并由 `cmd/apm` 的 `newCloudClient` 在每次 lifecycle 构造时创建独立 `ProviderRegistry`、注册 factory 后只打开被选中的 provider（[ADR-016](ivory-tower/adr/ADR-016-select-cloud-clients-with-explicit-providers.md)）。默认 provider 是 `remote`，当前开发默认 APC 地址为 `http://127.0.0.1:8080`：

```bash
# 默认 Remote（当前开发默认 APC 地址）
APC_BASE_URL=http://127.0.0.1:8080 ./apm list

# 显式 Local canonical fixture
APM_CLOUD_PROVIDER=local \
APM_CLOUD_LOCAL_ROOT="$(pwd)/internal/cloud/resources" \
./apm list
```

| 环境变量 | 默认值与约束 |
| --- | --- |
| `APM_CLOUD_PROVIDER` | 默认 `remote`；只接受 `remote` 或 `local`。 |
| `APC_BASE_URL` | 默认 `http://127.0.0.1:8080`；必须是无凭据、query、fragment 和非根路径的绝对 URL。非 loopback 地址只允许 HTTPS，HTTP 仅允许 loopback。 |
| `APM_CLOUD_LOCAL_ROOT` | 选择 `local` 时必填，必须是 clean absolute path，且从文件系统根到 fixture 根不能经过 symlink/reparse point。 |

provider 配置、Remote 网络/协议或内容错误都不会自动回退另一 provider。composition root 为 Remote 构造具有两分钟整体 timeout 和同源 redirect 策略的 `http.Client`；调用方更短的 context deadline 优先。Search、默认 Cloud List、installed List、Install 和 Uninstall 都在创建 lifecycle 时构造并校验所选 provider；installed List 进入查询后只刷新 Agent 和读取本地 manifest，不发起 Cloud 网络请求。Fix 只构造锁与 Transaction recovery，不创建 Cloud Client。

四类 Power 通过同一 `cloud.Client` 获取，但保留各自的内容边界：

- Skill：Local 复制 canonical fixture 目录；Remote 获取 `application/gzip`，先验证精确 metadata、size 与 32 位小写 MD5，再安全解包到调用方私有目录。版本化 cache 的命中、下载临时目标、原子发布和公共 staging 全部由 Cache 的 `PrepareSkill` 管理。
- Agent：Local/Remote 都把单个 `agent.yaml` 原始字节写到调用方私有文件；Remote content type 为 `application/yaml`。版本化 cache 与 staging 由 `PrepareAgent` 管理，安装前仍由 Bibit Agent Adapter 校验 YAML 身份和最小 schema。
- MCP：Local/Remote 都严格解码 `application/json` 并返回克隆的 `MCPServer` 领域对象，不建立文件 cache；公共 HTTP endpoint 只允许无 userinfo 的绝对 HTTPS URL。
- Plugin：Local fixture 与 Remote 解包结果只包含 `apc-plugin.json + skills/...`。`apc-plugin.json` 是唯一权威索引；Plugin Resource 严格复验 canonical 闭集，先确定性生成 Codex、Claude、Hermes 原生配置，再由 Codex/Claude marketplace builder 读取并复验对应原生 manifest 后生成 marketplace，最后一次原子发布共享 artifact。当前权威决策是 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)；[ADR-014](ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅保留为被取代的历史背景。

Remote 的 metadata/JSON 与 content 都受 20 MiB 客户端上限约束；Skill/Plugin 解包还限制为总大小 100 MiB、单文件 10 MiB、最多 1,000 个节点、深度 16、路径 255 字节和 100 倍解压倍率，并拒绝路径逃逸、链接、特殊节点、嵌套归档扩展名以及已知归档/可执行文件签名。Linux 与 macOS 支持完整的 descriptor-relative Skill/Plugin 目录发布、Agent 文件发布和 MCP 临时 staging；FreeBSD 支持 descriptor-relative staging、Agent 文件发布和 MCP，但 Skill/Plugin 的目录发布以 `ENOTSUP` fail closed。Windows 及其它未实现平台的 Remote content 操作 fail closed；目录 List/Resolve 不因此被描述为已发布内容可用。

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
./apm list --installed --agent codex
./apm list --installed --agent all --type skill
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
```

Search 已实现：它按名称搜索 Cloud Power，为每个类别和名称选择最新版本，并输出精确格式 `<type>/<name>@<version>`。例如：

```text
skill/foo-before-summary@v0.1.0
```

Search 只接受可选的 `--type`，不接受 `--agent`。全局选项 `--verbose` 输出解析和 journal 检查诊断到标准错误；帮助信息可查看每个子命令支持的参数。

Search 与两种 List 在原查询前都会无锁检查唯一活动 journal。合法活动 journal 会在 stderr Warn 并提示 `run apm fix`；journal 损坏或不可读时只 Warn，不附加 Fix 提示，随后继续原只读业务。检查错误不改变原命令的业务结果或退出状态。

List 已实现两种独立模式（命令边界见 [ADR-002](ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md) 和 [ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）：默认 Cloud List 在 Inspect 后输出每个版本的精确行：

```text
<type>/<name>@<version>
```

Installed List 必须同时提供 `--installed --agent=<agent-id|all>`，无锁刷新 Agent 并读取清单后输出精确行：

```text
<agent>/<type>/<name>@<version>
```

例如 `./apm list --agent all` 是无效组合：它会非零退出，并在 stderr 报告 `--agent requires --installed` 和 usage。`--agent=all` 的 installed 读取若出现部分失败，会输出成功记录后以非零状态报告错误；fatal error 不输出业务行。已安装清单版本必须精确为 `vMAJOR.MINOR.PATCH`，APM 不会迁移或清理非法旧清单（[ADR-007](ivory-tower/adr/ADR-007-use-stateless-config-repository.md)）；Agent 刷新顺序见 [ADR-004](ivory-tower/adr/ADR-004-discover-installed-agents-before-local-operations.md)。

只读输出是 best-effort 观察，不是全局一致快照：Inspect 前后可能并发创建或恢复 journal，`list --installed --agent=all` 也可能混合不同时间点的完整 manifest，并短暂观察到 target 与 manifest 不匹配。若业务需要全局一致读取，必须另行引入覆盖所有资源的锁或 MVCC；当前命令不提供该保证。

Fix 是显式恢复命令。它不接受位置参数或 Agent/Type 等命令专属选项，在一次全局锁内恰好调用一次 Recover 后退出，stdout 始终为空：

```bash
./apm fix
./apm --verbose fix
```

没有活动 journal 时普通模式静默成功，verbose 模式在 stderr 输出 Debug；恢复成功时输出 `transaction recovered` Warn。损坏、未知或不可读 journal 无法自动修复，Fix 会保留原文件和现场、记录 Error 并非零退出；不要手工删除 journal，除非已人工确认事务状态和数据所有权。

Install 已实现完整生命周期：显式版本必须是规范 `vMAJOR.MINOR.PATCH`，Name 按区分大小写的原始值精确匹配；同名 Cloud 候选跨 Type 时必须在版本选择前提供 `--type`。Install 从一次 Acquire 开始，持锁完成 Recover、Warn、Agent 刷新、Cloud 和串行 Transaction；`Resolve` 总是给出精确 `PowerRef`，Skill 与 Agent 分别由 Cache 的 `PrepareSkill`/`PrepareAgent` 管理下载、版本化 cache 和 staging（Agent 为单文件、原样字节发布，仅 Bibit），MCP 直接读取 Cloud 精确版本定义并调用目标 Agent CLI。Plugin 当前语义见 [Feature 020](ivory-tower/features/020-cloud-client-providers/spec.md) 与 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)：Cloud 按 `PowerRef` 下载 canonical APC Plugin，Plugin Resource 从 `apc-plugin.json` 派生并原子发布共享制品，再由目标 Agent 消费 marketplace 或 filesystem staging。只有成功 Release 后 CLI 才渲染。

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

Uninstall 在一次 Acquire/Release 之间完成 Recover、恢复 Warn、Agent 刷新、逐 Agent 清单解析和串行 Transaction。清单无记录时同步 Warn、记为 Agent-only `unexecuted` 并继续；清单有记录但 target 已缺失时，Transaction 仍移除该清单项，lifecycle 同步 Warn 并记为完整引用 `completed`。Plugin Uninstall 只 `Lookup` manifest 指向的本地精确 artifact，既不 `Ensure`、不回源 Cloud，也不重建 cache，因此其卸载业务保持离线；命令构造阶段仍会校验所选 provider 配置。MCP Uninstall 先用目标 Agent CLI 检查状态：target absent 时不访问 Cloud，target present 时才按 manifest 的精确版本调用 `GetMCP`，把定义保留给可恢复事务的回滚路径。决策边界见 [ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 与 [ADR-009](ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md)。

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
| `bibit` | `<BIANBU_AGENT_HOME>/skills/<name>` | 独立 `bianbu-agent mcp` Adapter | `<BIANBU_AGENT_HOME>/plugins/<name>`；目录发布，不调用 Plugin CLI | `<BIANBU_AGENT_HOME>/agents/<agent.id>/agent.yaml`；单文件、原样字节 |
| `claude-code` | `<CLAUDE_CONFIG_DIR>/skills/<name>` | Claude CLI user scope | 共享制品中的 Claude 本地 marketplace | 不支持 |
| `codex` | `<CODEX_HOME>/skills/<name>` | Codex CLI | 共享制品中的 Codex 本地 marketplace | 不支持 |
| `hermes-agent` | `<HERMES_HOME>/skills/<name>` | Hermes CLI | `<HERMES_HOME>/plugins/<name>`；Hermes CLI enable/disable | 不支持 |

Bibit profile 根优先使用 `BIANBU_AGENT_HOME`，未设置时为 `<有效 HOME>/.bianbu-agent`；根目录存在且为目录才进入本次命令的已安装 Agent 快照。Bibit 接口与路径约束见 [Feature 018](ivory-tower/features/018-bibit-agent/spec.md)，当前 Plugin canonical/派生边界见 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

Agent Power 仅 Bibit 支持（[Feature 019](ivory-tower/features/019-agent-power/spec.md) 与 [ADR-015](ivory-tower/adr/ADR-015-agent-power-bibit.md)）：Local canonical fixture 布局为 `agents/<name>/<version>/agent.yaml`，Remote exact content 为 `application/yaml` 原始字节。Cache 的 `PrepareAgent` 将单个 `agent.yaml` 按精确版本发布到 `cache/agents/<name>/<version>/agent.yaml` 并物化 staging；本地安装到 `<bibit根>/agents/<agent.id>/agent.yaml`（单文件、原样字节，本地不版本化，版本仅记 manifest）。`--type agent` 的 `--agent` 规则：install/uninstall 省略 `--agent` 时默认 `bibit`；`list --installed` 省略 `--agent` 时默认 `bibit`；`search`/`list` 的 Cloud 模式仅过滤结果、不要求 `--agent`。显式 `--agent bibit` 允许，其它值（含 `all`）报错。例如 `./apm install bianbu-agent-default=v1.0.0 --type agent`。

### Plugin 共享 artifact 与操作顺序

Plugin 当前架构以 [Feature 020](ivory-tower/features/020-cloud-client-providers/spec.md) 和 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md) 为准；[ADR-014](ivory-tower/adr/ADR-014-share-multi-agent-plugin-artifacts.md) 仅解释共享不可变 artifact 的历史来源。Cloud 不托管 marketplace 或 Agent 原生配置；`DownloadPlugin(ctx, ref, destination)` 只把 `apc-plugin.json + skills/...` canonical package 写入调用方目录。Plugin Resource 每个逻辑 Plugin 版本只下载一次，在唯一 cache-miss 临时根内生成并复验派生内容后，以以下布局原子发布共享、不可变制品：

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

Install 在锁内恢复和 Agent 选择后先处理显式规范 Plugin ref：每个所选 Agent 的 manifest 都精确命中时直接返回 `unchanged`，不会调用 Cloud、Plugin Resource、Adapter 或 Transaction；任一不匹配则回到既有 Cloud 解析路径。普通 Plugin 路径对每个 Agent 先读取 manifest，再统一调用 `ResolvePlugin(agent)`，随后按 `DeploymentMode` 分支。marketplace 分支在检查 Agent 事实前 `Ensure(ctx, ref)` 目标 artifact、校验 artifact、取得 `MarketplaceFor(agent)`：无 manifest 时只检查目标 marketplace/Plugin，绝不 `Lookup` 旧版本；有 manifest 时先检查旧 marketplace、再检查旧 Plugin，二者都 absent 才移除 stale manifest 并转入目标事实检查，部分存在则 fail closed，二者都 present 才离线 `Lookup(previous)` 并核对旧 source/selector。filesystem 分支则先完成旧目标或新目标的本地事实与物理缺失校验，之后才 `Ensure(ctx, ref)` 和 `StageFilesystem(ref)`，再进入 transaction。marketplace 首装会注册目标 source、添加逻辑名 Plugin 并写 manifest；正常卸载会保留 marketplace，因此 exact name/source retained 且 Plugin absent 的重装直接复用 source，只添加 Plugin 并写 manifest；其正常提交、回滚与恢复不做多余的 unregister/register，仅在恢复方向已固定为 `rolling-back` 且 retained source 缺失时离线复验缓存并恢复 source。版本切换仍删除旧 Plugin、切换同名 marketplace source、重新添加逻辑名 Plugin 并写 manifest。Hermes/Bibit 使用同一个 `plugins/<name>` 目标执行可恢复 filesystem transaction；Hermes 保留 enable/disable，Bibit 不执行 Plugin CLI。

Uninstall 先从 manifest 取得精确版本；没有记录时直接返回未执行，不检查 Agent。Codex/Claude 先检查该 marketplace 与 Plugin，再离线 `Lookup` 并验证共享 artifact/source/selector，最后在 transaction 中移除 Plugin 和 manifest，正常路径保留 marketplace 与 artifact；Hermes/Bibit 只处理逻辑名目录与 manifest。部分存在、source 或 selector 不匹配、artifact 缺失或损坏、Agent 事实不可读以及非法 journal 都会保留 manifest、artifact 和 journal 并 fail closed。Plugin Resource 与 recovery 共用中立只读 Validator，拒绝 symlink/特殊文件，并按消费路径复验原生 manifest 与已生成 marketplace；`fix`/recovery 只读取 journal、Adapter、manifest 和既有共享 artifact，绝不连接 Cloud、调用 Plugin Resource 或重建缓存，因此支持离线恢复。

Skill、Agent、MCP 与 Plugin 共用唯一活动 journal。唯一正式 schema v1 shared envelope 通过 `filesystem`、`agent-cli` 与 `plugin` driver 路由，并且只允许与 driver 匹配的互斥载荷；旧平铺或其他格式返回 `ErrInvalidJournal`，文件原样保留。Search 与两种 List 只告警；Fix、Install 或 Uninstall 才在全局锁内串行恢复未完成事务。Fix/recovery 只读取 journal、Adapter、manifest 和已发布的本地 artifact，不选择 provider、不连接 Cloud、不重新下载或派生内容。版本边界见 [ADR-011](ivory-tower/adr/ADR-011-reset-journal-schema-before-release.md)，当前 Plugin 架构见 [ADR-017](ivory-tower/adr/ADR-017-derive-agent-plugin-manifests-from-apc-plugin.md)。

自动化测试绝不能调用真实 Agent CLI。MCP/Plugin 测试应只把临时 fake-bin 置于 `PATH`，将 `APM_HOME`、`XDG_CACHE_HOME`、`CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`HERMES_HOME`、`BIANBU_AGENT_HOME` 全部指向 `t.TempDir()` 下的隔离路径，并断言每次执行路径都位于 fake-bin。仓库集成测试已按此约束覆盖四个 Agent 的 Plugin 首装、幂等、升级、降级、卸载和中断恢复；不得调用真实 `bianbu-agent` 或其他 Agent CLI。

## 清理本地构建产物

删除由本指南生成的可执行文件：

```bash
rm -f ./apm
```

// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由；Search、List、Install、Uninstall、Fix 分别通过独立 factory 创建专属端口；Fix 无业务 stdout。" "cmd/apm (Go, cobra)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cli-entry.mmd"
    }
}
lifecycle = component "Core Lifecycle" "提供独立的 Search、List、Install、Uninstall、Fix 生命周期端口。只读命令无锁检查 journal；Fix/Install/Uninstall 才串行恢复。Install/Uninstall 入口状态由共享纯状态策略统一：同版本不探测即 unchanged，不同版本加缺失目标 repair 后全新安装，无清单目标冲突只暴露 adapter.ErrTargetConflict（ADR-022）。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/core-lifecycle.mmd"
        "journalRecovery" "apm-cli-lifecycle-diagrams/common-journal-recovery.mmd"
        "journalInspection" "apm-cli-lifecycle-diagrams/common-journal-inspection.mmd"
        "agentDiscovery" "apm-cli-lifecycle-diagrams/common-agent-discovery.mmd"
        "searchLifecycle" "apm-cli-lifecycle-diagrams/search.mmd"
        "listLifecycle" "apm-cli-lifecycle-diagrams/list.mmd"
        "installLifecycle" "apm-cli-lifecycle-diagrams/install.mmd"
        "uninstallLifecycle" "apm-cli-lifecycle-diagrams/uninstall.mmd"
        "fixLifecycle" "apm-cli-lifecycle-diagrams/fix.mmd"
    }
}
submission = component "Submission Validation" "internal/submission 对四类 Power 提供完全离线的 Validate 与 Prepare：Skill/Plugin 确定性 tar.gz、MCP JCS 规范化、Agent 单个原始 agent.yaml 原样字节（Content-Type application/yaml、MD5 针对原始字节）。" "internal/submission (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/core-lifecycle.mmd"
    }
}
domain = component "Domain Model" "internal/domain 是共享领域契约：DecodeAgentConfigYAML/Validate/ValidatePowerRef 是 Agent 稳定 schema 的唯一权威；SubmissionOutcome/SubmissionResult 承载 accepted/replayed 分流。" "internal/domain (Go)" {
}
adapterRegistry = component "Adapter Registry" "管理 Bibit、Codex、Claude Code、Hermes 四个内置 Agent；分别 ResolveSkill、ResolveAgent、ResolveMCP、ResolvePlugin。AgentAdapter 增加只读 ValidateArtifact 身份校验。Codex/Claude Plugin 使用 marketplace，Hermes/Bibit 使用 filesystem（ADR-015/017/019）。" "internal/adapter (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/adapter-registry.mmd"
    }
}
configModel = component "Config Model" "无状态维护各 Agent 的安装列表；完整读取和写入只接受 vMAJOR.MINOR.PATCH，不迁移或清理非法历史清单（ADR-007）。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/config-model.mmd"
    }
}
lockManager = component "Lock Manager" "基于 flock 实现非阻塞、防重入全局锁；Catalog Snapshot 为默认 List/Search 短时获取，SnapshotLocked 复用 Install 已持锁区间；installed List 不获取，Fix/Install/Uninstall 覆盖各自状态边界（ADR-003/021）。" "Go (flock)" {
    properties {
        "classDiagram" "apm-cli-diagrams/lock-manager.mmd"
    }
}
catalogManager = component "Catalog Manager" "internal/catalog.Manager 通过 Snapshot/SnapshotLocked 编排 version 检查、必要下载、stale 状态和一次不可变 Snapshot；不读取 manifest 或恢复 journal（ADR-021）。" "internal/catalog (Go)"
catalogRepository = component "Catalog Repository" "严格读取、验证并以 0600 原子发布唯一 catalog.json；拒绝链接与可检测替换，pathname 操作信任 current EUID（ADR-021）。" "internal/catalog (Go)"
cacheManager = component "Cache Manager" "internal/cache 通过 Source 注入 Cloud Client；PrepareSkill/PrepareAgent 完整透传同一 CatalogEntry，独占命中复验、私有下载目标、精确版本原子 cache 发布与公共 staging。" "internal/cache (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudComposition = component "Cloud Composition" "cmd/apm.newCloudClient 直接 import local|remote，每次把两者的 factory closure 注册到 registry，按环境只打开一个 provider；默认 remote，禁止 fallback（ADR-016）。" "cmd/apm/cloud.go (Go)"
providerRegistry = component "Provider Registry" "internal/cloud.ProviderRegistry 为中立的实例级显式注册表；只保存 factory、按 ID 选择并返回 Client，拒绝空、重复、未知 provider 及 nil/typed-nil Client。" "internal/cloud (Go)"
cloudClient = component "Cloud Client Ports" "internal/cloud.Client 组合 CatalogSource、SkillSource、PluginSource、MCPSource、AgentSource 与 Submission；CatalogSource 只传输 version/full catalog，精确内容方法消费完整 CatalogEntry，不提供旧的分页或逐项发现能力。" "internal/cloud (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}
localProvider = component "Local Provider" "internal/cloud/local.Client 从显式绝对 canonical fixture 根生成确定性四分组 catalog，并以 fresh snapshot 校验/提供四类精确内容。" "internal/cloud/local (Go)"
remoteProvider = component "Remote Provider" "internal/cloud/remote.Client 匿名读取 APC catalog version/full object，并仅按 CatalogEntry 获取精确 content；Linux/macOS 完整发布，FreeBSD/其它平台按既有边界 fail closed。" "internal/cloud/remote (Go)"
pluginResource = component "Plugin Resource" "internal/pluginresource 下载 canonical apc-plugin.json + skills，先派生 Codex/Claude/Hermes 原生 manifest，再由 Codex/Claude builder 读取 manifest 生成双 marketplace，一次发布共享不可变 artifact（ADR-017）。" "internal/pluginresource (Go)" {
}
pluginArtifactValidator = component "Plugin Artifact Validator" "internal/pluginartifact 中立只读校验：复验 canonical apc-plugin 身份/Skill 闭集、派生 manifest/marketplace 与物理树；不下载、不生成、不修改 artifact。" "internal/pluginartifact (Go)" {
}
pluginArtifactCache = component "Plugin Artifact Cache Roots" "cache/plugins/<logical-name>/<version>；所有 Agent 共用的不可变 artifact 根，不属于通用 Cache Manager。" "filesystem" {
}
rawPluginDestination = component "Canonical Plugin Destination" "Plugin Resource 创建的空私有临时 plugins/<name> 目录；Cloud 只写 apc-plugin.json + skills。" "filesystem" {
}
nativeManifestGenerator = component "Native Manifest Generator" "internal/pluginresource.writeAgentManifests 从已复验的 canonical manifest 顺序生成 Codex、Claude、Hermes 原生 manifest。" "Go" {
}
codexMarketplaceBuilder = component "Codex Marketplace Builder" "从 Codex 原生 Plugin manifest 构建并复验单条目本地 marketplace。" "Go" {
}
claudeMarketplaceBuilder = component "Claude Marketplace Builder" "从 Claude 原生 Plugin manifest 构建并复验单条目本地 marketplace。" "Go" {
}
transactionManager = component "Transaction Manager" "Plugin transaction 持久化 phase，联合切换 marketplace、Plugin 与 manifest；卸载清除 Plugin、注销 marketplace 并删除 manifest（保留 artifact cache），安装缺失目标先 repair 再全新安装；恢复时只读复验方向所需 artifact，不依赖 Cloud 或 Plugin Resource。" "Go" {
}
journalStore = component "Journal Store" "唯一 transactions/active.json，schema v1 shared envelope。" "filesystem" {
}

cliEntry -> lifecycle "通过五个独立 factory 调用 Search/List/Install/Uninstall/Fix 生命周期用例"
cliEntry -> cloudComposition "Catalog 首次同步、Install content 与 Submission 命令按需创建 Cloud；installed List/Uninstall/Fix 不调用"
cloudComposition -> providerRegistry "每次注册 local|remote 并 Open 选中 ID"
cloudComposition -> localProvider "import local 并注册构造 closure"
cloudComposition -> remoteProvider "import remote 并注册构造 closure"
providerRegistry -> cloudClient "Open 选择 factory 并返回 Client 或 error"
localProvider -> cloudClient "implements internal/cloud.Client"
remoteProvider -> cloudClient "implements internal/cloud.Client"
remoteProvider -> apc "读取 catalog SHA-256/size、完整确定性对象与精确版本 content" "HTTPS/loopback HTTP + JSON/YAML/gzip"
cloudComposition -> catalogManager "惰性注入选中 CatalogSource"
cloudComposition -> lifecycle "Install content 与 Submission 命令注入选中 Client"
cloudComposition -> cacheManager "Install 将同一选中 Client 作为 Source 注入"
cloudComposition -> pluginResource "仅 Install 将选中 Client 作为 Downloader 注入；Uninstall 使用只读 Lookup 构造"
lifecycle -> adapterRegistry "installed List/Install/Uninstall 刷新快照；Install/Uninstall 按 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List、Install 与 Uninstall 读取/处理 manifest 事实"
lifecycle -> lockManager "Fix/Install/Uninstall 持有完整状态区间；installed List 不获取"
lifecycle -> catalogManager "默认 List/Search 调用 Snapshot；Install 已持锁调用 SnapshotLocked；Uninstall/Fix 不调用"
lifecycle -> cacheManager "Skill/Agent Install：PrepareSkill/PrepareAgent 返回公共 staging"
lifecycle -> cloudClient "Install 以 CatalogEntry 获取精确 MCP/Skill/Agent/Plugin content；Submit/get/list 使用 Submission 端口；Uninstall/Fix 无依赖"
lifecycle -> pluginResource "Plugin Ensure / Lookup / StageFilesystem；返回的 Artifact 再由调用方 MarketplaceFor(agent)"
lifecycle -> transactionManager "Install/Uninstall/Fix 的可恢复事务；Install 缺失修复→首次安装两阶段，Plugin 卸载清除 Plugin+marketplace+manifest"
lifecycle -> submission "Validate/Submit 对四类 Power 调用 Validate/Prepare；Submit 结果再经 cloud.Submit 上传"
submission -> domain "Agent 原始 YAML 严格解析与身份校验（DecodeAgentConfigYAML/Validate/ValidatePowerRef）"
catalogManager -> lockManager "Snapshot 非阻塞 Acquire/Release；SnapshotLocked 不再次获取"
catalogManager -> cloudClient "CatalogVersion + DownloadCatalog；匿名且每次同步至多一次"
catalogManager -> catalogRepository "Load 严格旧快照；Replace 验证 hash/size/schema 后原子发布"
catalogRepository -> apm.catalogCache "读取/替换唯一 0600 catalog.json"
cacheManager -> cloudClient "Source.DownloadSkill/DownloadAgent 完整透传 CatalogEntry 并写私有目标"
cacheManager -> domain "PrepareAgent 首次下载与缓存命中均校验 Agent 身份（DecodeAgentConfigYAML + ValidatePowerRef）"
pluginResource -> cloudClient "DownloadPlugin(ctx, entry, destination) 到临时 canonical Plugin 叶子"
pluginResource -> rawPluginDestination "创建并控制空的 canonical destination"
cloudClient -> rawPluginDestination "只写 apc-plugin.json + skills，不生成 Agent 配置/marketplace"
pluginResource -> pluginArtifactCache "保留 canonical 并一次发布共享 immutable artifact"
pluginResource -> pluginArtifactValidator "严格解析 canonical，构建后与 cache hit 完整只读复验"
pluginResource -> nativeManifestGenerator "先从复验后的 canonical manifest 生成 Codex、Claude、Hermes 原生 manifest"
pluginResource -> codexMarketplaceBuilder "随后读取并校验已生成的 Codex 原生 manifest，仅构建 Codex 单条目 marketplace"
pluginResource -> claudeMarketplaceBuilder "再读取并校验已生成的 Claude 原生 manifest，仅构建 Claude 单条目 marketplace"
transactionManager -> adapterRegistry "Plugin marketplace/Plugin 与 filesystem 变更；Agent recovery 经 AgentAdapterResolver 校验 target/backup/quarantine"
adapterRegistry -> domain "AgentAdapter.ValidateArtifact 只读身份校验（DecodeAgentConfigYAML + ValidatePowerRef）"
transactionManager -> journalStore "create/update/delete schema v1 journal"
transactionManager -> configModel "commit/rollback manifest Set/Remove"
transactionManager -> pluginArtifactValidator "recovery 仅复验固定方向实际需要的 target/previous artifact"
adapterRegistry -> targetAgent "Skill 访问 profile 根；MCP 通过各自 CLI；Codex/Claude Plugin 使用 marketplace；Hermes/Bibit 发布 plugins/<name>" "本地文件系统 / CLI"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
localProvider -> cloudRegistry "扫描 canonical Skill/Plugin/MCP/Agent fixture" "本地文件系统"

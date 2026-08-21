// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由；Search、List、Install、Uninstall、Fix 分别通过独立 factory 创建专属端口；Fix 无业务 stdout。" "cmd/apm (Go, cobra)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cli-entry.mmd"
    }
}
lifecycle = component "Core Lifecycle" "提供独立的 Search、List、Install、Uninstall、Fix 生命周期端口。只读命令无锁检查 journal；Fix/Install/Uninstall 才串行恢复。" "Go" {
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
adapterRegistry = component "Adapter Registry" "管理 Bibit、Codex、Claude Code、Hermes 四个内置 Agent；分别 ResolveSkill、ResolveAgent、ResolveMCP、ResolvePlugin。Codex/Claude Plugin 使用 marketplace，Hermes/Bibit 使用 filesystem（ADR-015/017）。" "internal/adapter (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/adapter-registry.mmd"
    }
}
configModel = component "Config Model" "无状态维护各 Agent 的安装列表；完整读取和写入只接受 vMAJOR.MINOR.PATCH，不迁移或清理非法历史清单（ADR-007）。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/config-model.mmd"
    }
}
lockManager = component "Lock Manager" "基于 flock 实现防重入文件锁；Search 与两种 List 不获取。Fix 以一次锁覆盖单次 Recover 与日志；Install/Uninstall 各以一次锁覆盖 Recover 与全部变更业务（ADR-003）。" "Go (flock)" {
    properties {
        "classDiagram" "apm-cli-diagrams/lock-manager.mmd"
    }
}
cacheManager = component "Cache Manager" "internal/cache 通过 Source 注入同一 Cloud Client；PrepareSkill/PrepareAgent 独占命中复验、私有下载目标、精确版本原子 cache 发布与公共 staging。" "internal/cache (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudComposition = component "Cloud Composition" "cmd/apm.newCloudClient 直接 import local|remote，每次把两者的 factory closure 注册到 registry，按环境只打开一个 provider；默认 remote，禁止 fallback（ADR-016）。" "cmd/apm/cloud.go (Go)"
providerRegistry = component "Provider Registry" "internal/cloud.ProviderRegistry 为中立的实例级显式注册表；只保存 factory、按 ID 选择并返回 Client，拒绝空、重复、未知 provider 及 nil/typed-nil Client。" "internal/cloud (Go)"
cloudClient = component "Cloud Client Ports" "internal/cloud.Client 组合 Catalog、SkillSource、PluginSource、MCPSource、AgentSource；目录 destination 必须不存在或为空物理目录，Agent 文件必须不存在；provider 不拥有 APM cache/staging。" "internal/cloud (Go)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}
localProvider = component "Local Provider" "internal/cloud/local.Client 从显式绝对 canonical fixture 根读取四类 Power；复制物理目录/文件或严格解码 MCP。" "internal/cloud/local (Go)"
remoteProvider = component "Remote Provider" "internal/cloud/remote.Client 读取 APC HTTP 目录/latest/exact/content；Linux/macOS 完整发布，FreeBSD 的 Skill/Plugin 目录发布返回 ENOTSUP，Windows/其它 content fail closed。" "internal/cloud/remote (Go)"
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
transactionManager = component "Transaction Manager" "Plugin transaction 持久化 phase，联合切换 marketplace、Plugin 与 manifest；恢复时只读复验方向所需 artifact，不依赖 Cloud 或 Plugin Resource。" "Go" {
}
journalStore = component "Journal Store" "唯一 transactions/active.json，schema v1 shared envelope。" "filesystem" {
}

cliEntry -> lifecycle "通过五个独立 factory 调用 Search/List/Install/Uninstall/Fix 生命周期用例"
cliEntry -> cloudComposition "Search/List/Install/Uninstall factory 创建 Cloud；Fix 不调用"
cloudComposition -> providerRegistry "每次注册 local|remote 并 Open 选中 ID"
cloudComposition -> localProvider "import local 并注册构造 closure"
cloudComposition -> remoteProvider "import remote 并注册构造 closure"
providerRegistry -> cloudClient "Open 选择 factory 并返回 Client 或 error"
localProvider -> cloudClient "implements internal/cloud.Client"
remoteProvider -> cloudClient "implements internal/cloud.Client"
remoteProvider -> apc "读取 Published Catalog、metadata 与精确版本 content" "HTTPS/loopback HTTP + JSON/YAML/gzip"
cloudComposition -> lifecycle "注入选中 Client；Fix 例外"
cloudComposition -> cacheManager "Install 将同一选中 Client 作为 Source 注入"
cloudComposition -> pluginResource "Install/Uninstall 将同一选中 Client 作为 Downloader 注入"
lifecycle -> adapterRegistry "installed List/Install/Uninstall 刷新快照；Install/Uninstall 按 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List、Install 与 Uninstall 读取/处理 manifest 事实"
lifecycle -> lockManager "仅 Fix、Install、Uninstall 在各自边界获取/释放；Search 与两种 List 不获取"
lifecycle -> cacheManager "Skill/Agent Install：PrepareSkill/PrepareAgent 返回公共 staging"
lifecycle -> cloudClient "Search/List/Install List/Resolve；Install MCP GetMCP；Uninstall 仅 target present MCP 精确 GetMCP"
lifecycle -> pluginResource "Plugin Ensure / Lookup / StageFilesystem；返回的 Artifact 再由调用方 MarketplaceFor(agent)"
lifecycle -> transactionManager "Install/Uninstall/Fix 的可恢复事务"
cacheManager -> cloudClient "Source.DownloadSkill/DownloadAgent 写入 Cache 私有目标"
pluginResource -> cloudClient "DownloadPlugin(ctx, ref, destination) 到临时 canonical Plugin 叶子"
pluginResource -> rawPluginDestination "创建并控制空的 canonical destination"
cloudClient -> rawPluginDestination "只写 apc-plugin.json + skills，不生成 Agent 配置/marketplace"
pluginResource -> pluginArtifactCache "保留 canonical 并一次发布共享 immutable artifact"
pluginResource -> pluginArtifactValidator "严格解析 canonical，构建后与 cache hit 完整只读复验"
pluginResource -> nativeManifestGenerator "先从复验后的 canonical manifest 生成 Codex、Claude、Hermes 原生 manifest"
pluginResource -> codexMarketplaceBuilder "随后读取并校验已生成的 Codex 原生 manifest，仅构建 Codex 单条目 marketplace"
pluginResource -> claudeMarketplaceBuilder "再读取并校验已生成的 Claude 原生 manifest，仅构建 Claude 单条目 marketplace"
transactionManager -> adapterRegistry "Plugin marketplace/Plugin 与 filesystem 变更"
transactionManager -> journalStore "create/update/delete schema v1 journal"
transactionManager -> configModel "commit/rollback manifest Set/Remove"
transactionManager -> pluginArtifactValidator "recovery 仅复验固定方向实际需要的 target/previous artifact"
adapterRegistry -> targetAgent "Skill 访问 profile 根；MCP 通过各自 CLI；Codex/Claude Plugin 使用 marketplace；Hermes/Bibit 发布 plugins/<name>" "本地文件系统 / CLI"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
localProvider -> cloudRegistry "扫描 canonical Skill/Plugin/MCP/Agent fixture" "本地文件系统"

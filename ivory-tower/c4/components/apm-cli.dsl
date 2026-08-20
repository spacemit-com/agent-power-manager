// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由；Search、List、Install、Uninstall、Fix 分别通过独立 factory 创建专属端口；Fix 无业务 stdout。" "Go (cobra)" {
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
adapterRegistry = component "Adapter Registry" "管理 Bibit、Codex、Claude Code、Hermes 四个内置 Agent；分别 ResolveSkill、ResolveMCP、ResolvePlugin。Codex/Claude Plugin 使用 marketplace，Hermes/Bibit 使用 filesystem（ADR-014）。" "Go" {
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
cacheManager = component "Cache Manager" "只供 Skill 从 type/name/version 缓存物化 staging；MCP 和 Plugin 不访问。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudClient = component "Cloud Client" "List 扫描全部 Power；DownloadSkill 保持既有缓存语义，DownloadPlugin(ref, destination) 写入完整多 Agent 原始包；GetMCP 返回安装定义（ADR-014）。" "Go (local filesystem)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}
pluginResource = component "Plugin Resource" "每个 Plugin 版本下载并原子发布一份共享不可变 artifact；同时生成 Codex/Claude marketplace，并为 Hermes/Bibit 提供 filesystem staging（ADR-014）。" "Go" {
}
pluginArtifactValidator = component "Plugin Artifact Validator" "中立只读校验：拒绝 symlink/特殊文件，并复验当前消费路径所需的原生 manifest 与已生成 marketplace；不下载、不生成、不修改 artifact。" "Go" {
}
pluginArtifactCache = component "Plugin Artifact Cache Roots" "cache/plugins/<logical-name>/<version>；所有 Agent 共用的不可变 artifact 根，不属于通用 Cache Manager。" "filesystem" {
}
rawPluginDestination = component "Raw Plugin Destination" "Plugin Resource 创建的空临时 plugins/<name> 目录；Cloud 在此写入完整多 Agent 原始包。" "filesystem" {
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
lifecycle -> adapterRegistry "installed List/Install/Uninstall 刷新快照；Install/Uninstall 按 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List、Install 与 Uninstall 读取/处理 manifest 事实"
lifecycle -> lockManager "仅 Fix、Install、Uninstall 在各自边界获取/释放；Search 与两种 List 不获取"
lifecycle -> cacheManager "Skill Install：为每个实际安装 Agent StageSkill 到公共 staging"
lifecycle -> cloudClient "Search/List/Install List；Skill DownloadSkill；MCP GetMCP"
lifecycle -> pluginResource "Plugin Ensure / Lookup / StageFilesystem；返回的 Artifact 再由调用方 MarketplaceFor(agent)"
lifecycle -> transactionManager "Install/Uninstall/Fix 的可恢复事务"
pluginResource -> cloudClient "DownloadPlugin(ref, destination) 到共享制品临时 Plugin 叶子"
pluginResource -> rawPluginDestination "创建并控制空的原始包 destination"
cloudClient -> rawPluginDestination "写入完整多 Agent 原始包，不生成 marketplace"
pluginResource -> pluginArtifactCache "一次发布、复验共享 immutable artifact"
pluginResource -> pluginArtifactValidator "构建后与 cache hit 完整只读复验"
pluginResource -> codexMarketplaceBuilder "构建与复验 Codex 单条目 marketplace"
pluginResource -> claudeMarketplaceBuilder "构建与复验 Claude 单条目 marketplace"
transactionManager -> adapterRegistry "Plugin marketplace/Plugin 与 filesystem 变更"
transactionManager -> journalStore "create/update/delete schema v1 journal"
transactionManager -> configModel "commit/rollback manifest Set/Remove"
transactionManager -> pluginArtifactValidator "recovery 仅复验固定方向实际需要的 target/previous artifact"
adapterRegistry -> targetAgent "Skill 访问 profile 根；MCP 通过各自 CLI；Codex/Claude Plugin 使用 marketplace；Hermes/Bibit 发布 plugins/<name>" "本地文件系统 / CLI"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
cloudClient -> apm.powerCache "DownloadSkill 经临时目录原子写入文件型缓存"
cloudClient -> cloudRegistry "扫描 Skill/MCP/Plugin fixture；读取 MCP 与完整多 Agent Plugin 包" "本地文件系统"

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
adapterRegistry = component "Adapter Registry" "管理三个内置 Agent；分别 ResolveSkill、ResolveMCP、ResolvePlugin。Plugin 对 Codex/Claude 使用 marketplace，对 Hermes 使用 filesystem（ADR-012）。" "Go" {
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
cacheManager = component "Cache Manager" "供 Skill 与 Hermes Plugin 从 type/name/version 缓存物化 staging；MCP 和 marketplace Plugin 不访问。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudClient = component "Cloud Client" "List 扫描全部 Power；DownloadSkill/DownloadPlugin 写文件型缓存；GetMCP/GetPlugin 返回安装定义。" "Go (local filesystem)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}

cliEntry -> lifecycle "通过五个独立 factory 调用 Search/List/Install/Uninstall/Fix 生命周期用例"
lifecycle -> adapterRegistry "installed List/Install/Uninstall 刷新快照；Install/Uninstall 按 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List 和 Uninstall 读取安装记录；Install Find；Transaction Set/Remove"
lifecycle -> lockManager "仅 Fix、Install、Uninstall 在各自边界获取/释放；Search 与两种 List 不获取"
lifecycle -> cacheManager "Skill 与 Hermes filesystem Plugin Install：为每个实际安装 Agent fetch 到公共 staging"
lifecycle -> cloudClient "Search/List/Install List；Skill DownloadSkill；Hermes Plugin DownloadPlugin；MCP/Plugin GetMCP/GetPlugin"
adapterRegistry -> targetAgent "Skill 访问文件根；MCP/Plugin 通过 Agent CLI；Hermes Plugin 发布本地目录" "本地文件系统 / CLI"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
cloudClient -> apm.powerCache "DownloadSkill/DownloadPlugin 经临时目录原子写入文件型缓存"
cloudClient -> cloudRegistry "扫描 Skill/MCP/Plugin fixture；读取 MCP 与 marketplace 定义" "本地文件系统"

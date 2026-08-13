// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由；Search、List、Install、Uninstall 分别通过独立 factory 创建专属端口，并渲染各自的结构化结果。" "Go (cobra)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cli-entry.mmd"
    }
}
lifecycle = component "Core Lifecycle" "提供独立的 Search、List、Install、Uninstall 生命周期端口。按 Skill/MCP/Plugin 分支调用类型专属 Transaction；所有恢复型命令串行恢复唯一活动 journal。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/core-lifecycle.mmd"
        "journalRecovery" "apm-cli-lifecycle-diagrams/common-journal-recovery.mmd"
        "agentDiscovery" "apm-cli-lifecycle-diagrams/common-agent-discovery.mmd"
        "searchLifecycle" "apm-cli-lifecycle-diagrams/search.mmd"
        "listLifecycle" "apm-cli-lifecycle-diagrams/list.mmd"
        "installLifecycle" "apm-cli-lifecycle-diagrams/install.mmd"
        "uninstallLifecycle" "apm-cli-lifecycle-diagrams/uninstall.mmd"
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
lockManager = component "Lock Manager" "基于 flock 实现防重入文件锁；Cloud List 不获取，installed List 覆盖 Recover→Warn→Refresh→全部 Config reads；Install 以一次锁覆盖 Recover→Warn→Refresh→Cloud→Cache→Transaction；Uninstall 以一次锁覆盖 Recover→Warn→Refresh→Config→Transaction，二者均在 CLI 渲染前 Release（ADR-003）。" "Go (flock)" {
    properties {
        "classDiagram" "apm-cli-diagrams/lock-manager.mmd"
    }
}
cacheManager = component "Cache Manager" "供 Skill 与 Hermes Plugin 从 type/name/version 缓存物化 staging；MCP 和 marketplace Plugin 不访问。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudClient = component "Cloud Client" "List 扫描全部 Power；Get 写 Skill/Hermes Plugin 缓存；GetMCP 返回启动定义；GetPlugin 返回实际名和 marketplace。" "Go (local filesystem)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}

cliEntry -> lifecycle "通过四个独立 factory 调用 Search/List/Install/Uninstall 生命周期用例"
lifecycle -> adapterRegistry "installed List/Install/Uninstall 刷新快照；Install/Uninstall 按 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List 和 Uninstall 读取安装记录；Install Find；Transaction Set/Remove"
lifecycle -> lockManager "Search、installed List、Install、Uninstall 在各自边界获取/释放；Cloud List 不获取"
lifecycle -> cacheManager "仅 Skill Install：为每个实际安装 Agent fetch 到公共 staging"
lifecycle -> cloudClient "Search/List/Install list；Skill/Hermes Plugin cache miss Get；GetMCP/GetPlugin"
adapterRegistry -> targetAgent "Skill 访问文件根；MCP/Plugin 通过 Agent CLI；Hermes Plugin 发布本地目录" "本地文件系统 / CLI"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
cloudClient -> apm.powerCache "仅 Skill Get：经临时目录原子写入"
cloudClient -> cloudRegistry "扫描 Skill/MCP/Plugin fixture；读取 MCP 与 marketplace 定义" "本地文件系统"

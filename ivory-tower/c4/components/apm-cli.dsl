// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由；Search、List、Install 分别解析、调用专属端口并渲染结构化结果；Uninstall 仅 parse-only。" "Go (cobra)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cli-entry.mmd"
    }
}
lifecycle = component "Core Lifecycle" "提供独立的 Search、List、Install 生命周期端口，均返回结构化结果且不直接打印。Cloud List 无锁；installed List 依 ADR-003 持锁；Install 以一个锁区间覆盖 Recover、刷新、Cloud、Cache 和串行 Transaction；Uninstall 仍仅 parse-only。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/core-lifecycle.mmd"
        "journalRecovery" "apm-cli-lifecycle-diagrams/common-journal-recovery.mmd"
        "agentDiscovery" "apm-cli-lifecycle-diagrams/common-agent-discovery.mmd"
        "searchLifecycle" "apm-cli-lifecycle-diagrams/search.mmd"
        "listLifecycle" "apm-cli-lifecycle-diagrams/list.mmd"
        "installLifecycle" "apm-cli-lifecycle-diagrams/install.mmd"
    }
}
adapterRegistry = component "Adapter Registry" "管理编译期唯一 Agent 注册；RefreshInstalled 对注册副本按 Agent ID 字典序检测并返回同序快照（ADR-004）。Adapter 依据 PowerRef 从公共 staging 根定位叶子内容并处理安装位置差异；不负责 Agent 安装、卸载、实时监控或运行时感知。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/adapter-registry.mmd"
    }
}
configModel = component "Config Model" "无状态维护各 Agent 的安装列表；完整读取和写入只接受 vMAJOR.MINOR.PATCH，不迁移或清理非法历史清单（ADR-007）。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/config-model.mmd"
    }
}
lockManager = component "Lock Manager" "基于 flock 实现防重入文件锁；Cloud List 不获取，installed List 覆盖 Recover→Warn→Refresh→全部 Config reads；Install 以一次锁覆盖 Recover→Warn→Refresh→Cloud→Cache→Transaction，并在 CLI 渲染前 Release（ADR-003）。" "Go (flock)" {
    properties {
        "classDiagram" "apm-cli-diagrams/lock-manager.mmd"
    }
}
cacheManager = component "Cache Manager" "从 type/name/version 版本化缓存向干净公共 staging 物化版本已确定的 PowerRef，并返回 staging root。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudClient = component "Cloud Client" "开发期扫描本地 Power fixture，并将 Get 的版本已确定 PowerRef 原子写入缓存。" "Go (local filesystem)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}

cliEntry -> lifecycle "调用 Search/List/Install 生命周期用例"
lifecycle -> adapterRegistry "installed List/Install 刷新快照；Install 按目标 Agent 与 Power 类型解析 Adapter"
lifecycle -> configModel "installed List 读取安装记录；Install Find；Transaction Set/Remove"
lifecycle -> lockManager "Search、installed List、Install 在各自边界获取/释放；Cloud List 不获取"
lifecycle -> cacheManager "Install 为每个实际安装 Agent fetch 版本已确定 PowerRef 到公共 staging"
lifecycle -> cloudClient "Search/List/Install list 全量 PowerRef；Install 缓存 miss 时 get"
adapterRegistry -> targetAgent "检查 Agent 根目录；安装/卸载 Power 到目标位置" "本地文件系统"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读取版本化缓存"
cloudClient -> apm.powerCache "经临时目录原子写入目标 Power"
cloudClient -> cloudRegistry "扫描/get 开发期 Power fixture" "本地文件系统"

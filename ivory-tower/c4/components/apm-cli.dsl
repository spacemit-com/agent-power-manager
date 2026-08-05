// 由 containers/apm.dsl 的 !include 引用；本文件内容处于 CLI Binary 容器内。

cliEntry = component "CLI Entry" "cobra 命令路由，解析 keyword/power/version、--agent/--type/--installed 并触发对应生命周期用例。" "Go (cobra)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cli-entry.mmd"
    }
}
lifecycle = component "Core Lifecycle" "定义 Power 的发现、安装、卸载、打印、配置读写和版本管理用例。" "Go" {
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
adapterRegistry = component "Adapter Registry" "管理编译期唯一 Agent 注册，按 Agent 根目录检测安装状态，并承接 Power 格式转换与安装位置差异；不负责 Agent 安装、卸载、实时监控或运行时感知。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/adapter-registry.mmd"
    }
}
configModel = component "Config Model" "维护各类 Agent 上各类 Power 的安装列表读写模型。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/config-model.mmd"
    }
}
lockManager = component "Lock Manager" "基于 flock 实现防重入的文件锁。" "Go (flock)" {
    properties {
        "classDiagram" "apm-cli-diagrams/lock-manager.mmd"
    }
}
cacheManager = component "Cache Manager" "管理下载 Power 的本地缓存。" "Go" {
    properties {
        "classDiagram" "apm-cli-diagrams/cache-manager.mmd"
    }
}
cloudClient = component "Cloud Client" "调用云端 Power 服务的 list/get 接口。" "Go (net/http)" {
    properties {
        "classDiagram" "apm-cli-diagrams/cloud-client.mmd"
    }
}

cliEntry -> lifecycle "调用生命周期用例"
lifecycle -> adapterRegistry "刷新已安装 Agent 快照；按目标 Agent 与 Power 类型获取适配实现"
lifecycle -> configModel "读写安装记录"
lifecycle -> lockManager "获取/释放锁，防重入"
lifecycle -> cacheManager "读写下载缓存"
lifecycle -> cloudClient "list/get Power 资源"
adapterRegistry -> targetAgent "检查 Agent 根目录；安装/卸载 Power 到目标位置" "本地文件系统"
configModel -> apm.installedPower "读写 JSON 清单"
cacheManager -> apm.powerCache "读写缓存内容"
cloudClient -> cloudRegistry "list/get Power 资源" "HTTPS"

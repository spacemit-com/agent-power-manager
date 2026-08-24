workspace "Agent Power Manager" "APM：面向多 Agent，统一管理 Skill、Plugin、MCP、Agent 等 Power 的发现、安装、卸载、提交与配置治理，通过 Adapter 适配不同目标 Agent。" {
    !identifiers hierarchical

    model {
        user = person "User" "通过 CLI 使用 APM 管理 Power 的开发者/运维人员。"

        apc = softwareSystem "APC" "提供 Published Catalog、版本 metadata 与精确内容的远端匿名只读服务。"
        cloudRegistry = softwareSystem "Power Resource Fixture" "开发阶段位于 internal/cloud/resources 的本地 Power 资源；用于模拟 Cloud list/get。"
        targetAgent = softwareSystem "Target Agent" "用户本地的目标 Agent（如 Claude Code、Cursor 等），拥有自身的 Skill/Plugin/MCP 加载与运行时；不在 APM 的系统边界内。"

        apm = softwareSystem "Agent Power Manager" "统一管理 Skill、Plugin、MCP、Agent 等 Power 的发现、安装、卸载、提交与配置治理，通过 Adapter 适配不同目标 Agent。" {
            !include containers/apm.dsl
        }

        user -> apm.cli "运行APM命令"
    }

    views {
        systemContext apm "SystemContext" "APM 位于 APC/开发期 Power 资源 fixture、使用方（User）与目标 Agent 之间的系统边界；不承担目标 Agent 的运行时职责。" {
            include *
            autoLayout lr
        }

        container apm "Containers" "APM 内部容器：CLI 进程与本地持久化存储（Agent 清单、下载缓存）。" {
            include *
            autoLayout lr
        }

        component apm.cli "Components" "CLI 进程内的模块划分：入口、核心生命周期、Adapter、配置模型、锁、缓存、云端客户端。" {
            include *
            autoLayout lr
        }

        theme default
    }
}

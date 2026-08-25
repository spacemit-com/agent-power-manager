// 由 workspace.dsl 的 !include 引用；APM 系统。

installedPower = container "Installed Power List" "各目标 Agent 独立的已安装 Power 清单（JSON），按 Power 类别分类。" "本地文件系统 (JSON)"
powerCache = container "Versioned Immutable Power Cache" "按 type/name/version 保存 Skill/Agent 版本化不可变制品与 Plugin 共享不可变制品；MCP 不进入文件缓存。" "本地文件系统"
catalogCache = container "Catalog Cache" "唯一 ~/.apm/catalog.json；保存严格四分组目录原始字节，SHA-256/size 是同步标记。" "本地文件系统 (JSON, 0600)"

cli = container "CLI Binary" "apm 单进程可执行文件，基于 cobra 提供 search/list/install/uninstall 等命令。" "Go" {
    !include ../components/apm-cli.dsl
}

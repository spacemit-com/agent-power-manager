// 由 workspace.dsl 的 !include 引用；APM 系统。

installedPower = container "Installed Power List" "各目标 Agent 独立的已安装 Power 清单（JSON），按 Power 类别分类。" "本地文件系统 (JSON)"
powerCache = container "Power Cache" "下载 Power 的内容寻址缓存，避免重复下载。" "本地文件系统"

cli = container "CLI Binary" "apm 单进程可执行文件，基于 cobra 提供 search/list/install/uninstall 等命令。" "Go" {
    !include ../components/apm-cli.dsl
}

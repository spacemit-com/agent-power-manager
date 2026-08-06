# Feature Overview

## Goal

- 特性名称：开发期本地 Cloud 与版本化缓存
- 要解决的问题：在真实云端服务和完整 install lifecycle 尚未实现时，以 `internal/cloud/resources` 的本地资源验证带版本 `PowerRef` 的发现、缓存获取和 staging 物化边界。
- 可交付结果：Cloud `List/Get` 与 Cache `Fetch` 的明确契约；本地 fixture 可列出 `skill/foo-before-summary@0.0.1`，并支持 `Fetch → miss → Get → Fetch` 数据流。

## Scope

- 包含：为 `domain.PowerRef` 增加 `Version`；Cloud `List()` 返回带版本的全部 `PowerRef`，`Get(ref)` 从绝对路径指定的开发期资源根原子写入 `type/name/version` 缓存；Cache `Fetch(ref)` 每次清理 staging，并将缓存内容物化到不含版本层的 `staging/<type>/<name>`；lifecycle 将空版本解析为最新版后，传给 Get/Fetch 的版本必须非空；缓存 miss 与 Cloud not-found 可通过独立错误识别；缓存和 staging 临时目录统一由 `internal/paths` 定义且分别与目标目录位于同一文件系统。
- 不包含：CLI/lifecycle 串联与输出、search 筛选、最新版或 SemVer 选择、Adapter 安装、真实 HTTP Cloud、checksum 或内容寻址、Power 名称和复制文件类型校验、plugin/mcp 资源。

## Implementation Context

- 关联模块或服务：`internal/domain`、`internal/cloud`、`internal/cache`、`internal/paths`；开发期资源为 `internal/cloud/resources/skills/foo-before-summary/0.0.1`。
- 数据、接口或依赖变化：`PowerRef` 合并版本字段，`InstalledPower` 不再单独保存版本；`Client.List` 去除 Query 并返回 `[]domain.PowerRef`；`Client.Get` 与 `PowerCache.Fetch` 接收版本已确定的 `PowerRef`，分别保证缓存可用和返回 staging 路径；删除 `Put`、`PowerMeta`、`Checksum`、无功能 HTTP Client 和内容寻址缓存占位。不新增第三方依赖。
- 关联 C4 文档或 ADR：[ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md)、[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、Cloud Client、Cache Manager、Core Lifecycle 类图及 list/search/install lifecycle 图。

## Acceptance

- 验收条件：`List` 稳定返回 fixture 中具有 `SKILL.md` 的全部带版本 `PowerRef`，当前仅包含 `skill/foo-before-summary@0.0.1`；plugin/mcp 无结果且目标 `Get` 返回 not-found；首次 `Fetch` 返回 cache miss，`Get` 原子保留版本目录并完整复制 fixture，第二次 `Fetch` 清理旧 staging 后生成 `staging/skills/foo-before-summary`；重复 `Get` 幂等；成功后无临时目录残留，缓存源不被 Fetch 修改。
- 已知风险或待决问题：本地资源是开发阶段临时措施并默认内容合规；真实 Cloud 协议、资源完整性与安全校验后续设计。

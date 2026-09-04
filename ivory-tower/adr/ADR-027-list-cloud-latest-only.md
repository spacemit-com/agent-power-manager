# List Cloud Catalog Shows Latest Version Only

- Status：Accepted
- Date：2026-09-04
- Decision Makers：APM maintainers

## Context

默认 `apm list`（Cloud/catalog 模式）此前对目录中的每个条目都输出一行，因此同一个 Power 的多个
历史版本会平铺出现。该行为由 [ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md) 第 39 行
“保留每个版本”确立。但作为发现命令，list 的主要用途是让用户看到“现在可安装什么”，同一逻辑
Power 的多个版本同时展示会放大噪声，且与 `search` 已经采用的 keep-latest 语义不一致。

需要决策的是：默认 `list` 是继续平铺全部版本，还是对每个逻辑身份只输出最高 SemVer 版本。

## Decision

默认 Cloud `list` 对每个 `(PowerRef.Type, PowerRef.Name)` 逻辑身份只输出最高 SemVer 版本，
不再平铺历史版本；其余行为不变。

- 去重键是 `(type, name)`，catalog 路径中 Agent 恒为空，不参与键。
- 版本比较使用 `semver.MustParse(v).GreaterThan(...)`，与 `search` 的 `filterSearchPowers`
  keep-latest 逻辑一致。
- 保留的是“最高版本”那条 `CatalogEntry` 的 `description`/`tags`，因此 `--all`/`--json` 展示的
  元数据属于最新版本，旧版本元数据随旧版本一起丢弃。
- `flags.Type` 精确过滤在去重之前应用；去重后仍按 type → name 升序稳定排序。
- `list --installed` 与 `search` 语义不变：installed list 展示本地已安装的精确版本，search
  已按名称匹配并选择最新版本。

本决策取代 ADR-002 第 39 行“`list` 默认列出 Cloud 资源……并保留每个版本”中的“保留每个版本”条款；
ADR-002 其余内容仍有效。

输出示例（同一 catalog 修改前后）：

```text
修改前                      修改后
mcp/filesystem@v2.3.0       mcp/filesystem@v2.3.0
mcp/filesystem@v1.0.0       plugin/alpha@v2.0.0
plugin/alpha@v2.0.0         skill/alpha@v3.0.0
skill/alpha@v3.0.0          skill/beta@v1.10.0
skill/beta@v1.10.0
skill/beta@v1.9.0
```

## Consequences

- 正面影响：默认 `list` 输出更聚焦于可安装的当前版本，与 `search` 的 keep-latest 语义对齐，
  减少同名多版本造成的噪声。
- 正面影响：`makeCatalogListResult` 的去重逻辑与 `filterSearchPowers` 保持一致的心智模型，
  后续维护时两者共享同一套“最高 SemVer”约定。
- 代价与风险：需要显式查看某个历史版本的用户无法再通过默认 `list` 直接看到它；精确版本仍可由
  `install <power>=<version>` 表达，`search` 也只展示最新版，因此不新增单独的“全部版本”入口。
- 代价与风险：若未来需要“列出全部版本”，需另行引入 flag 或子命令，而不是恢复默认平铺。
- 后续动作：更新 Project Overview、BUILD 与 Changelog 中“默认 list 列出全部版本”的描述，
  并引用本 ADR。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保持平铺全部版本 | 与 search 语义不一致，发现场景噪声大，用户难以一眼判断最新版本。 |
| 新增 `--all-versions` flag 控制 | 本次需求未要求查看历史版本，引入新 flag 增加命令面；确有需求时再演进。 |
| 对 `list --installed` 也去重 | installed list 展示的是本地安装事实，跨 Agent 同名的精确版本不同，去重会掩盖真实状态。 |

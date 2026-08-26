# Unify Lifecycle Entry-state Semantics

- Status：Accepted
- Date：2026-08-26
- Decision Makers：APM maintainers
- Related Feature：[Feature 025](../features/025-unified-lifecycle-state-semantics/spec.md)

## Context

Skill、Agent、MCP 与 Plugin 分阶段加入 APM lifecycle，导致相同清单和目标入口状态产生不同
结果。典型差异包括：同版本 Skill、Agent 和 Plugin 不检查目标便返回 `unchanged`，MCP 却会
检查；不同版本遇到目标缺失时 Skill、Agent 和 MCP 删除清单后返回 `ErrStaleManifest`，Plugin
可以在同一命令继续安装；无清单目标冲突又分别暴露通用及 MCP/Plugin 专属错误。

Plugin 还把 Plugin 本体、专属 marketplace 和 artifact 混入同一判断。部分目标过去一律
fail closed，正常卸载则保留 marketplace。这使后续安装不同版本可能因旧 source 冲突而失败，
也使“实际目标是否存在”的宏观矩阵无法与其他 Power 对齐。

需要统一的是用户可观察的 lifecycle 策略，而不是四类 Power 的物理操作。文件系统原子发布、
Agent CLI mutation、Plugin marketplace 和恢复材料仍有不同约束，不能为了代码形式统一而合并
为一个不精确的 transaction driver。

## Decision

APM 采用共享的纯 lifecycle 状态策略，并保留 Skill、Agent、MCP 与 Plugin 类型专属的事实
检查和 transaction executor。安装按 `absent / same-version / different-version` 清单状态与
`absent / complete / incomplete / conflict` 目标状态决定宏观动作；卸载按清单有无和同一
目标状态决定宏观动作。

同版本安装直接返回 `unchanged`，不检查实际目标。显式规范 Type/Name/Version 在所有目标
Agent 清单中精确命中时，所有 Power 都在 catalog、Cloud、cache、Plugin Resource 和 Adapter
之前早退。无清单目标存在时不接管，唯一公共业务原因是 `adapter.ErrTargetConflict`。

不同版本安装遇到目标缺失或已确认属于清单但不完整时，记录一次资源缺失 warning，先通过
repair transaction 清理残留和旧清单，再以首次安装 transaction 安装新版本；成功结果是
`installed`。repair 一经提交不因后续下载、准备或安装失败而恢复陈旧状态。

卸载以 manifest 作为所有权依据。无清单时不检查或删除目标，warning 后返回 `unexecuted`；
有清单且目标缺失或不完整时记录一次 warning，清理残留与清单并返回 `completed`。事实冲突或
状态不确定继续 fail closed。

Plugin 本体是实际 Power 目标，专属 marketplace 是必要辅助事实，artifact 是非运行态缓存。
存在 manifest 时，Plugin/marketplace/source 任一缺失且残留能够证明归 APM 管理，就按目标
缺失处理；身份或来源冲突仍 fail closed。Plugin 卸载同时删除 Plugin、注销专属 marketplace
并删除 manifest，但保留共享 artifact cache。没有 manifest 时任何 Plugin 或 marketplace
残留都不得接管，按公共目标冲突拒绝安装。

每个 Agent 只产生一条 stdout 结果；每个缺失事实只记录一次 warning；失败只由一个 batch
marker 交给 CLI 记录一次 Error。`all` 的字典序、首错停止、后续 `unexecuted` 和 Release
失败抑制 stdout 的规则保持不变。

本决策定向取代 ADR-013、ADR-014 与 Feature 018 中“Plugin 正常卸载保留 marketplace”以及
“Plugin 部分状态一律保留 manifest 并失败”的规则；也取代 Feature 010 及后续实现中不同版本
安装缺失目标只清理清单并返回 `ErrStaleManifest` 的规则。未被本 ADR 明确修改的 artifact、
transaction、journal、Cloud 和安全边界继续有效。

## Consequences

- 正面影响：四类 Power 对相同入口状态给出相同 outcome、warning、公共错误和最终状态。
- 正面影响：缺失目标的版本切换一次命令即可修复并完成安装，不要求用户手工重试。
- 正面影响：Plugin 卸载后没有遗留 marketplace source 阻塞任意版本重装，artifact 缓存仍可
  避免不必要下载。
- 正面影响：共享策略可由单一状态表验证，类型专属 executor 仍保留准确的恢复能力。
- 代价与风险：同版本 `unchanged` 明确不审计目标；目标被外部删除或损坏时，用户必须先卸载
  或使用其他显式修复流程，重复安装同版本不会修复。
- 代价与风险：repair 提交后新安装失败会留下完全未安装状态，而不是恢复已经缺失或不完整的
  旧状态。
- 代价与风险：Plugin 卸载增加 marketplace mutation 和相应 journal/recovery 阶段；Agent CLI
  状态未知时仍必须保留证据并 fail closed。
- 代价与风险：旧脚本若依赖 `ErrStaleManifest`、MCP/Plugin 专属 conflict sentinel 或卸载后
  retained marketplace，需要迁移到新的公共契约。
- 后续动作：实现 Feature 025，更新 BUILD、项目概览、C4 与受影响历史文档，并在行为验证完成
  后写入一次 Changelog 记录。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保留四套独立策略，只增加对齐测试 | 规则仍散落在类型分支中，后续修改或新增 Power 容易再次漂移。 |
| 把四类 Power 合并为一个通用 transaction driver | 文件 rename、Agent CLI mutation 和 Plugin marketplace 的提交点与恢复材料不同，统一物理接口会隐藏关键差异并扩大重构风险。 |
| 缺失目标只删除清单并返回 `ErrStaleManifest` | 用户需要再次执行安装，且与 Plugin 已有单次修复能力不一致。 |
| repair 后新安装失败时恢复旧状态 | 旧状态已经确认缺失或不完整，恢复清单或残留会重新制造不一致。 |
| Plugin 卸载继续保留 marketplace | 相同版本重装较快，但旧 source 会阻塞无清单的不同版本安装，并使卸载后的运行状态不完整。 |
| 无清单时自动接管同名目标或 marketplace | 无法证明所有权，可能覆盖或删除用户及其他工具管理的资源。 |

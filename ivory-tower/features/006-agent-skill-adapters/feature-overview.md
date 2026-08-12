# Feature Overview

> **当前状态说明（2026-08-12）：** 本文保留 Feature 006 的 Skill-only 历史范围。Feature 013 已为三个 Agent 增加独立 MCP CLI Adapter，并把 Registry 拆为 `ResolveSkill`/`ResolveMCP`；参见 [Feature 013](../013-agent-mcp-adapters/spec.md) 与 [ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)。Plugin 仍不支持。

## Goal

- 特性名称：Codex、Claude Code 与 Hermes Agent Skill Adapter
- 要解决的问题：现有 Adapter 只有零值占位接口和空 Registry，无法检测目标 Agent、适配公共 staging、阻止覆盖清单外同名 Skill，或把 Skill 安全安装到不同 Agent 的全局目录。
- 可交付结果：为 `codex`、`claude-code`、`hermes-agent` 提供共享安全安装管线和各自路径配置；Adapter 依据 `PowerRef` 从公共 staging root 定位叶子内容，从原地适配一直负责到原子发布，并在成功或失败后清理 staging。

## Scope

- 包含：三个正式 Agent ID；Agent 根目录解析与检测；Skill Adapter Registry；目标路径解析；YAML frontmatter 校验；清单外目标冲突防护；公共 staging 原地适配 hook；复制安装、同目录临时目录和原子 rename；目标临时目录与 staging 清理；Agent 快照；错误分类、debug 日志和单元测试。
- 不包含：lifecycle/CLI 串联；Config Repository 读写；版本选择和清单判断；事务 journal、旧版本备份或恢复；卸载；Plugin/MCP Adapter；项目级安装；symlink 安装；真实 Agent 的安装或创建。

> **Feature 006 的卸载边界是历史记录：** 本 feature 当时不实现卸载编排。Feature 011 现已确定当前边界：Adapter 仍没有 `Uninstall` 方法，Transaction 通过 Adapter `TargetPath` 完成 quarantine 与卸载，详见 [ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

## Implementation Context

- 关联模块或服务：`internal/adapter`、`internal/domain`、`internal/paths`、`internal/utils/filesystem`，以及只依赖 Adapter Registry 的 lifecycle 接口边界。
- 数据、接口或依赖变化：正式定义三个 `AgentID`；Adapter 暴露 `DetectAgent`、`TargetPath` 和 `Install`，其中 Feature 007 追加显式发布结果供 transaction 判断提交点；`Install(PowerRef, stagingRoot)` 依据 PowerRef 定位 `staging/<type-directory>/<name>` 叶子；Registry 只解析 Skill；引入 `go.yaml.in/yaml/v3` 校验 `SKILL.md` frontmatter；Feature 010 已将 cache fetch 的返回值收敛为公共 staging root。
- 关联 C4 文档或 ADR：[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[ADR-005](../../adr/ADR-005-isolate-test-filesystem-with-apm-home.md)、[ADR-008](../../adr/ADR-008-let-adapters-own-staging-to-installation.md)、[ADR-009](../../adr/ADR-009-centralize-lifecycle-transactions.md)。

## Acceptance

- 验收条件：三个 Agent 按环境变量和有效 HOME 解析全局目录；Registry 只支持 Skill 并稳定生成已安装 Agent 快照；Adapter 在适配前拒绝任意同名目标，校验 YAML frontmatter，完整复制到目标旁临时目录并原子发布；成功和所有失败路径均清理 staging，且不会暴露半成品目标；全量测试、vet、构建和静态检查通过。
- 已知风险或待决问题：外部进程仍可能在发布前的二次检查与 rename 之间竞争；发布成功后的 staging 清理失败会返回错误但保留目标，必须由后续 lifecycle/transaction 根据 journal 处理；Plugin/MCP Adapter 仍待设计。卸载和多 Agent 串行 lifecycle 已由 Feature 011 实现，不改变本 feature 当时的历史 scope。

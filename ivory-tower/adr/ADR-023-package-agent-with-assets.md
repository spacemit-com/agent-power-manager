# Package Agent as an Archive with Assets

- Status：Accepted
- Date：2026-08-26
- Decision Makers：APM maintainers
- Related Feature：[Feature 026](../features/026-agent-package-assets/spec.md)
- Related APC contract：[APC-API.md](../../APC-API.md)、[ADR-025](ADR-025-apc-api-client-contract.md)（原 APC 仓库已停止维护，其 ADR-006 不再链接）
- Related ADR：[ADR-033](ADR-033-require-agent-id-to-equal-agent-name.md)（`agent.id` 必须与 `agent.name` 取相同值）
- Revised：2026-10-20（根闭集新增 `skills/`、`plugins/`、`mcp/` 三个可选子资源容器目录，由 [ADR-046](ADR-046-allow-expert-to-carry-exclusive-sub-resources.md) 取代本文档的根闭集一条；`assets/`、avatar 与归档形态不变）

## Context

Agent 当前以单个原始 `agent.yaml` 贯穿 Submission、APC content、Cloud Client、cache、staging
和 Bibit 安装。V0.5 模板增加可选 `agent.metadata.avatar`，其目标是与 YAML 一同发布的本地
图片资源；单文件 content 无法表达这类引用，也无法保证下载、校验和最终安装看到相同 assets。

现有实现还把 `agent.id` 同时作为 APC Power name 和 Bibit 内部 ID。V0.5 已明确提供
`agent.name`，用户在 install/uninstall 中指定的也是资源名称。继续复用 id 会混淆云端系列身份
与 Bibit 内部身份，并使安装目录不符合命令所见名称。

Skill 和 Plugin 已使用确定性 `.tar.gz`、统一 Submission multipart 和安全归档下载。Agent
需要对齐公共机制，但不需要 Plugin 的派生 manifest、marketplace 或独立资源管理器。

## Decision

Agent 的权威 content 改为确定性 `.tar.gz`，根闭集为必需 `agent.yaml` 和可选 `assets/`；
后续由 [ADR-046](ADR-046-allow-expert-to-carry-exclusive-sub-resources.md) 扩展为还可选
`skills/`、`plugins/`、`mcp/` 三个子资源容器目录（Agent 可携带只属于自己的 Skill/Plugin/MCP）。
Agent 的 `validate`/`submit` 只接受资源根目录，由 APM 固定物理快照、完整校验并自行打包；
不接受单个 YAML 或预制归档。

Agent 外部 PowerRef name 使用 `agent.name`，version 使用 `agent.version`。`agent.id` 只作为
Bibit 内部必填标识，不再与 PowerRef name 比较。cache 按 name/version 保存完整目录，Bibit
最终只发布当前版本到 `agents/<name>/`；精确版本继续由 PowerRef、manifest 和 cache 表达。

APM 只校验 V0.5 的稳定最小字段：整数 `schema_version: 1`，以及非空字符串
`agent.id/name/version/description/soul`；name/version 仍遵守 APM canonical 规则。其余可选和
未知 YAML 字段原样透传。

`agent.metadata.avatar` 可选。存在时必须引用 `assets/` 下真实 PNG 普通文件，大小不超过
1 MiB，宽高均为 128–1024 像素且为正方形。APM 不校验 DPI、色彩空间、透明度，也不校验
其他 asset 的业务格式。Agent 根和 assets 仍服从公共归档安全与阈值。

保留 Agent 专属 `DownloadAgent`、`PrepareAgent` 和 Adapter 接口，但把 destination、cache、
staging 和 target 改为目录；内部复用公共物理树、归档下载和原子目录机制。filesystem journal
schema 不变，backup/quarantine/recovery 对整目录操作并执行完整 Agent 身份校验。

APC 对 Agent 使用 `application/gzip`，content size/MD5 针对归档原始字节。Skill、Plugin、
Agent 共用同一 APC gzip/tar 最低验收，APC 不解析各类型业务内容。APM/APC 均处于开发阶段，
直接迁移所有 fixture、示例和开发数据，不保留 `application/yaml` 兼容分支。

Feature 025 的 lifecycle 状态语义保持不变。同版本仍不 probe；不同版本目标缺失仍 warning 后
清理并首次安装；无 manifest 目标冲突仍只保证公共 `adapter.ErrTargetConflict`。

本决策取代 ADR-019 与 Feature 022 中 Agent 使用单个原始 YAML、PowerRef name 来自
`agent.id`、单文件 cache/staging/target 的约定；其 Submission 治理、身份闭环和错误边界中未被
本文修改的部分继续有效。

## Consequences

- 正面影响：Agent YAML 与 assets 成为同一个不可变、可校验和可事务发布的版本化制品。
- 正面影响：Agent 与 Skill/Plugin 在目录输入、确定性归档、multipart、APC content 和下载
  机制上对齐。
- 正面影响：外部 `agent.name` 与 CLI/目录一致，`agent.id` 保持 Bibit 内部职责。
- 正面影响：一个权威目录校验器覆盖提交、下载、cache、安装和 recovery，规则不会分叉。
- 代价与风险：公共归档代码需要按 Power 类型允许 Agent assets 二进制，同时保持 Skill/Plugin
  的既有文本与安全策略。
- 代价与风险：整目录 transaction 的备份与恢复体积高于单文件，但仍受 100 MiB 总量限制。
- 代价与风险：这是不兼容迁移；旧开发数据、fixture、cache 和 API 示例必须同步替换。
- 后续动作：实现 Feature 026，更新 APM/APC 接口规范、项目文档、模板和测试，并在完成行为
  验证后记录 APM Changelog。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保留 YAML content，把 avatar 单独上传到另一个接口 | 破坏单个版本制品的完整性，增加跨对象一致性、MD5 和恢复问题。 |
| Agent 同时兼容 YAML 与 `.tar.gz` | 两套下载、cache、校验和安装分支会长期存在；项目尚未发布，无兼容收益。 |
| 把 Skill/Agent API 合并成通用 Archive Power 接口 | 会扩大稳定类型接口修改面，且类型校验和 Plugin Resource 仍无法统一。 |
| 为 Agent 新建 Plugin Resource | Agent 不派生原生 manifest 或 marketplace，引入独立 manager 属于过度设计。 |
| 最终安装到 `agents/<name>/<version>` | Bibit 需要新增版本选择或 current 指针；同一 Agent 只激活一个版本时没有必要。 |
| 继续用 `agent.id` 作为 Power name | 混淆 Bibit 内部标识与用户安装的资源系列名称，无法正确使用 V0.5 `agent.name`。 |

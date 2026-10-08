# 把 Power 类别 agent 改名为 expert

- Status：Accepted
- Date：2026-10-08
- Decision Makers：APM maintainers
- Related ADR：[ADR-002](ADR-002-cli-agent-and-power-type-as-flags.md)（CLI 的 `--agent` / `--type` 语法）、
  [ADR-015](ADR-015-agent-power-bibit.md)（Agent Power 仅 Bibit）、
  [ADR-023](ADR-023-package-agent-with-assets.md)（Agent 的 `agent.yaml` + assets 目录契约）、
  [ADR-025](ADR-025-apc-api-client-contract.md)（`APC-API.md` 是客户端唯一权威契约）、
  [ADR-033](ADR-033-require-agent-id-to-equal-agent-name.md) / [ADR-034](ADR-034-report-agent-id-name-mismatch-as-validate-warning.md)（`agent.id` 与 `agent.name` 必须相同）
- 关联仓库：`bibit-agenthub-plugin`（同批改名，见其 changelog 与
  `docs/verification/2026-10-08-agent-type-renamed-to-expert.md`）

## Context

「agent」在 APM 与 Agent Hub 插件里同时指两件毫不相干的东西：

1. **目标 Agent**：`--agent=bibit|codex|claude-code|hermes-agent`，即 Power 被安装进哪个软件。
2. **一种 Power 类别**：`--type=agent`，即 Bibit 的子代理资源（资源包是 `agent.yaml` + `assets/`）。

同一个词在一条命令里出现两次（`apm install <agent-power> --type agent --agent bibit`）时，读到的人无法
判断哪一个是哪一个；插件侧尤其明显，因为它的请求里同时有「目标 Agent」和「类型」两个字段，类型取值恰好
也是 `agent`。Agent Hub 插件已经在界面上把该类别显示为 **Expert**（`resolvePowerTypeLabel`），但那只解决
了界面文案：代码、CLI 取值与大部分文档仍然是 `agent`，歧义依旧。

两条硬约束决定了这次改名能走多远：

- **APC 是已实现、且不打算再改的服务端**。它的 HTTP 契约以仓库根目录的 `APC-API.md` 为唯一权威：提交
  metadata 的 `type` 枚举含 `agent`，目录顶层分组叫 `agents`，系列查询也用 `type=agent`。apm 只能适配。
- **磁盘与作者面已经固化**。`agents/<name>` 是 Bibit Runtime 按 `agent.id` 定位已安装子代理的约定，
  `agent.yaml` 是作者写进资源包、且已发布内容里带着的文件名。改它们要动 Bibit 与已发布内容。

## Decision

**apm 本地把该类别统一叫 `expert`**：Go 常量 `domain.PowerTypeExpert = "expert"`、CLI `--type=expert`、
apm 自己的 JSON 输出（`list --installed --json` 记录、`validate --json` 结果、submission 详情与列表）
一律是 `expert`。**跨到外部词汇时翻译**，翻译只允许出现在两个地方：

1. **APC 的 HTTP 边界**（`internal/cloud/remote/type_vocabulary.go`，唯一入口）：
   - 出站：提交 metadata 的 `type`、系列查询的 `type` 参数、内容下载路径 `resources/<type>/...` 都写成
     APC 的 `agent`。
   - 入站：submission 详情/列表响应里的 `type` 翻回 `expert`（在 presence-sensitive DTO 上保持 `nil` 透传）。
2. **目录分组名**（`internal/catalog/codec.go` 的分组表）：APC 的分组仍叫 `agents`，本地类别是 `expert`。

**`--type=agent` 作为弃用别名继续接受**，在解析时就地改写成 `expert`（`internal/cli/flags.go` 的
`resolvePowerType`），并在**非 `--json`** 时向 stderr 发一条弃用提示。`--json` 下不提示：那种模式的输出
是机器契约，一句提醒不属于结果。规范化必须发生在命令读取 `flags.Type` 之前，因为原始字符串会一路流进
`internal/lifecycle` 参与 `domain.PowerType` 比较与过滤，留着 `agent` 会让 Expert 类别静默消失。

**保留 `agent` 的清单**（这些名字指的是外部契约或目标 Agent，不是本次改名的对象）：

| 保留项 | 理由 |
| --- | --- |
| `--agent`、`AgentID`、`KnownAgentIDs`、`agentProfile`、`AgentAdapter`、`bibitAgentAdapter` | 目标 Agent |
| `agents/<name>` 安装目录、`cache/agents/...`、`staging/agents/...`、`pruneEmptyAgentDirs` | Bibit Runtime 与既有缓存布局 |
| `agent.yaml`、其 `agent:` 块、`AgentConfig`/`AgentInfo`/`AgentMetadata`/`ErrInvalidAgent`、`internal/agentartifact` | 作者可见的资源包格式，已发布内容依赖 |
| 清单文件的 `agents` 键（`config/manifest.go`）、目录分组 `agents` | 既有本地文件与 APC 目录格式 |
| APC wire 取值 `agent`（`APC-API.md` 的 `type` 与 `agents` 组） | APC 已实现且不在本次范围 |

按这条规则被改名的（Exact）：`cache.FilesystemCache.PrepareAgent` → `PrepareExpert`、
`lifecycle.installAgentPower`/`uninstallAgentPower` → `installExpertPower`/`uninstallExpertPower`
（文件一并改名）、`submission.agentSourceLoader` → `expertSourceLoader`（`agent.go` → `expert.go`）、
`cli.validateAgentTypeFlags` → `validateExpertTypeFlags`、`cli.validatePowerType` → `resolvePowerType`。
`transaction.InstallAgent`/`UninstallAgent`/`AgentInstallRequest` **保留**：那里的 `Agent` 指目标 Agent
（请求里同时有 `Agent domain.AgentID` 和 `Power domain.PowerRef`），不是类别。

`APC-API.md` 的契约内容不动（它描述的是 APC），只在其中标注客户端词汇与 wire 词汇的对应关系。

## Consequences

- 正面影响：一条命令里只会有一个 `agent`，且它总是目标 Agent；插件的类型取值、apm 的 CLI 取值与 JSON
  输出对齐，插件侧那层「把 `agent` 显示成 Expert」的映射可以删除，类型名与显示名合一。
- 正面影响：翻译被收在一个文件里，并配有单元测试（`type_vocabulary_test.go`）与目录分组测试
  （`codec_test.go` 的 `TestExpertCatalogGroupKeepsAPCName`），漏翻哪一侧都会有测试失败。
- **代价与风险：apm 与 Agent Hub 插件必须同批升级**。混合状态两个方向都坏，只是坏在不同地方：
  新 apm + 旧插件 → 旧插件解析不了 `type:"expert"`；旧 apm + 新插件 → 旧 apm 不认识 `--type=expert`。
  插件的 deb 精确锁自己的 CLI 版本，但 `agent-power-manager`（本仓库的 deb）不锁，所以这是真实风险。
  缓解：apm 接受旧入参并给出可执行提示；两边发布说明都写明必须同批升级。
- 代价与风险：仓库里长期存在两套词汇（本地 `expert`、wire 与磁盘 `agent`）。这是上面两条硬约束的直接
  结果，用本 ADR 的保留清单与 `type_vocabulary.go` 的注释把它固定下来，而不是任其自然漂移。
- 后续动作：等 APC 的 wire 与目录分组也改成 `expert`（需要平台侧配合与已发布内容迁移），删除
  `type_vocabulary.go`、`resolvePowerType` 的别名分支与 `codec.go` 的组名映射，并在本 ADR 上标记 Superseded。
- 后续动作：`agents/<name>` 目录与 `agent.yaml` 若要改名，需要 Bibit Runtime 与已发布内容一起迁移，
  属于独立决策，不在本 ADR 范围。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 只改界面文案与文档，wire 取值保持 `agent` | 「同一命令里两个 agent」的问题留在 CLI 与代码里，插件与 apm 的类型取值仍然对不上，等于没解决设计歧义 |
| 连 APC 的 wire 取值与目录分组一起改成 `expert` | APC 是已实现且不再改动的服务端，改它要平台侧配合、还要迁移已发布资源；超出 apm 与插件两个仓库的范围 |
| 连磁盘 `agents/<name>` 与 `agent.yaml` 一起改名 | 两者分别被 Bibit Runtime 与已发布内容依赖，需要第三方一起迁移；收益（内部命名一致）远小于风险 |
| 只在 APM 侧改名，不要求插件同步 | 插件的 Provider 把类型原样透传给 `apm --type=` 并解析 apm 的 `type` 输出，单侧改名会让两侧接口对不上，正是本 ADR 要避免的「无法使用出错」 |

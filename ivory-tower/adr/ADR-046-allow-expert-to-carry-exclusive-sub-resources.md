# Architecture Decision Record

- Status：Accepted
- Date：2026-10-20
- Decision Makers：liangjunzhao

## Context

Agent（Expert）资源的权威 content 是确定性 `.tar.gz`，其根闭集被
[ADR-023](ADR-023-package-agent-with-assets.md) 固定为「必需 `agent.yaml` + 可选 `assets/`」。
`internal/agentartifact` 是这份格式的唯一权威校验器：submission、Local/Remote Cloud 下载、
cache、Bibit Adapter 与 transaction recovery 全部复用它，任何根闭集之外的新条目都会让整份资源
校验失败。

但一个 Agent 往往需要**只属于自己**的能力：它自己的 Skill、它自己的 Plugin、它自己的 MCP。
当前作者只有两条路，都不理想。其一，把这些能力作为独立 Power 分别上传，由使用者逐条安装——
这要求使用者知道该装哪些配套资源，漏装就让 Agent 残缺；而且「配套」这层关系无处声明。其二，
把能力文件塞进 `assets/`——`assets/` 是 avatar 等展示资产的容器，语义上不承载可执行能力，
且不会经过 Skill/Plugin/MCP 的格式校验，坏掉的 Skill 会一路装到机器上才暴露。

同时，`agent.yaml` 的 `skills`/`mcps` 扩展字段一直被 APM 原样透传、不做校验（ADR-033/038）。
这些字段表达的是「这个 Agent 想用哪些能力」，但此前它们只能指向使用者另行安装的东西。

## Decision

**放宽 Agent 根闭集，允许资源根下出现三个具名的子资源容器目录：`skills/`、`plugins/`、`mcp/`。**

```text
<agent-root>/
├── agent.yaml            # 必需
├── assets/               # 可选（ADR-023，不变）
├── skills/<name>/        # 可选，新增：一个完整 Skill（SKILL.md + skill-card.md + …）
├── plugins/<name>/       # 可选，新增：一个完整 APC Plugin（apc-plugin.json + skills/…）
└── mcp/<name>.json       # 可选，新增：一个完整 MCP 资源（单 JSON 文件）
```

- **子资源是裸目录/裸文件，整体仍是一个 Expert `.tar.gz`。** 不引入内嵌 `.tar.gz`：既有校验
  明确拒绝嵌套归档与归档魔数，新增嵌套层会同时要求上传侧多打一层、下载侧多解一层，并绕开
  「归档根即资源根」的既有约定。子资源直接以目录形态进入同一棵树，沿用「资源文件夹 →
  确定性 tar.gz → 校验 → 解包 → 落盘」的既有链路，不新增打包或传输机制。
- **身份取自子资源自身内容，不取自目录名。** Skill 的身份是 `SKILL.md` frontmatter 的
  `name`/`version`；Plugin 的是 `apc-plugin.json` 的 `name`/`version`；MCP 的是 JSON 内的
  `name`/`version`。因此目录名只是容器，不参与身份比较——这与独立 Skill/Plugin 的既有语义
  一致（它们的目录名同样不承载身份）。目录名仍须是非空、不以点号开头的安全字符集。
- **每个子资源必须自洽。** `skills/<n>/` 缺 `SKILL.md` 或 `skill-card.md`、`plugins/<n>/` 缺
  `apc-plugin.json` 或其声明的 `skills/` 子树不齐、`mcp/<n>.json` 无法解码，整份资源即校验失败。
  APM 不接受「半成品」被装到机器上。
- **子资源复用各自类型的权威校验。** Skill 走 `skillartifact`（与独立 Skill、Plugin 内嵌 Skill
  同一实现）；Plugin 走 `pluginartifact` 的 manifest 与闭集校验（与独立 Plugin 同一实现）；
  MCP 走 `domain.DecodeMCPResourceJSON` 与 `ValidateSubmit`。不新增第二套格式。
- **同一类型内身份唯一，跨类型可同名。** 两个 Skill 不能同 `name`，两个 MCP 不能同 `name`；
  Skill `demo` 与 MCP `demo` 可以共存，因为它们在资源根下位于互不重叠的容器目录。
- **既有安全规则对子树继续生效。** 路径规范化、重复路径、symlink/特殊文件、可执行位、嵌套
  归档、单文件 10 MiB、总量 100 MiB、深度 16、路径 255 字节全部不变。子资源不是绕过这些
  规则的通道。
- **安装落盘位置不变。** 解包后 `skills/`、`plugins/`、`mcp/` 原样留在 `agents/<name>/` 内，
  APM 不把它们拆到 Bibit 的全局目录。APM 的职责是「把作者声明的资源完整、可校验地送到
  目标目录」，不猜测 Runtime 的发现规则。
- **`agent.yaml` 的 `skills`/`mcps` 字段仍不校验一致性。** 本决策不要求它们与 `skills/` `mcp/`
  目录一一对应：这些字段是 APM 原样透传的扩展字段（ADR-033/038），强制一致会让既有合法资源
  变成非法。一致性留给后续演进，见 Known Limitation。

分层上，Skill 校验从 `internal/submission` 下沉为独立叶子包 `internal/skillartifact`：`submission`
依赖 `agentartifact`，而 `agentartifact` 现在也要校验 Skill 子树，若不抽包就会成环。抽包后
`agentartifact` 只依赖 `domain` 与既有的 `pluginartifact`/`skillartifact`，依赖方向保持单向。

## Consequences

- 正面影响：Agent 可以把自己的配套能力打成**一个**资源上传，使用者一次安装即得到完整可用的
  Agent，不必知道该补装哪些 Skill/Plugin/MCP；「配套」关系由包结构直接表达，而不是靠文档约定。
- 正面影响：子资源与独立资源同构且复用同一套校验，作者可以把一个独立 Skill/Plugin/MCP 目录
  原样拷进 Agent 资源根，不需要改写格式；也不会出现两套格式各自漂移。
- 正面影响：全部既有内容边界（submission、下载解包、cache、安装、恢复）通过同一个
  `agentartifact` 入口自动获得新规则，无需在各处分别补白名单——放宽点只有一处。
- 代价与风险：Agent 归档体积会显著增长，更容易撞上 20 MiB 上传上限与 100 MiB 解包总量。
  两道边界仍是硬拒绝，不会静默截断；作者需要按此裁剪子资源。
- 代价与风险：`agentartifact` 的校验变重（要解析 Skill frontmatter、Plugin manifest 与 MCP
  JSON），每个 Agent 的安装/下载路径都会承担这份成本。相对一次网络下载与整目录复制，实测
  影响可忽略。
- 代价与风险：`skills/` 这个根目录名与 Plugin 包内的 `skills/` 同名。两者位于不同层级
  （Agent 根 vs Plugin 根），语义一致（都装内嵌 Skill），但阅读资源树时需要区分层级。
- 后续动作：Feature 033 记录实现与验证；`docs/power-template/agent.md`、`BUILD.md`、
  `ivory-tower/project-overview.md`、`docs/Changelog.md` 同步契约；ADR-023 的根闭集一条被
  本决策取代。

## Known Limitation

`agent.yaml` 的 `skills`/`mcps` 是 APM 原样透传的扩展字段。本决策**不**校验它们与
`skills/`、`mcp/` 目录是否一一对应，因此可能出现「声明了 `skills: [foo]` 但没带 `foo/`」
或「带了 `bar/` 但没声明」的资源。两种都不会让 APM 校验失败。是否强制一致需要先确定
Bibit Runtime 的发现规则（按字段引用还是按目录扫描），本仓库无法确认该规则，故不在本次
决策内猜测；待 Runtime 规则确认后再以独立 ADR 收紧。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 子资源各自打成 `.tar.gz` 放进 Agent 归档 | 既有校验明确拒绝嵌套归档与归档魔数；上传要多打一层、安装要多解一层，且绕开「归档根即资源根」的约定。 |
| 允许任意根目录（放弃根闭集） | 根闭集是 Agent 资源可被审计的基础；放弃后任何文件都能混进 Agent，avatar 与 assets 的语义也随之失守。 |
| 把子资源塞进 `assets/` | `assets/` 语义是展示资产；塞进去就不会经过 Skill/Plugin/MCP 的格式校验，坏资源要到运行时才暴露。 |
| 子资源继续作为独立 Power 分别上传 | 使用者必须知道该装哪些配套资源，漏装即残缺；「配套」关系无处声明，也无法随 Agent 版本一起演进。 |
| 子资源身份取目录名 | 与独立 Skill/Plugin 的既有语义冲突（它们的目录名同样不承载身份），会让同一份资源在不同位置得到不同身份。 |
| 安装时把子资源拆到 Bibit 全局目录 | 需要确切知道 Runtime 的发现规则，本仓库无法确认；猜错会让资源既不在 Agent 内也不被 Runtime 发现。 |
| 要求 `agent.yaml` 的 `skills`/`mcps` 与子资源目录严格一致 | 这两个字段一直是原样透传的扩展字段；强制一致会把既有合法资源变成非法，需要先确定 Runtime 语义。 |
| 在 `submission` 内单独实现子资源校验，不改 `agentartifact` | 下载解包、cache、安装、恢复都不经过 `submission`，只改那里会让「上传时校验、安装时放行」出现缺口。 |

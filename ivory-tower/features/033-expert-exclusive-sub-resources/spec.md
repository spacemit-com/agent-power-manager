# Feature 033：Expert 携带独有子资源

- 状态：Implemented
- 关联 ADR：[ADR-046](../../adr/ADR-046-allow-expert-to-carry-exclusive-sub-resources.md)
  （取代 [ADR-023](../../adr/ADR-023-package-agent-with-assets.md) 的根闭集一条）

## 1. 目标

把 Expert（Agent）资源的根闭集从「必需 `agent.yaml` + 可选 `assets/`」扩为
「+ 可选 `skills/`、`plugins/`、`mcp/`」，使一个 Agent 能把只属于自己的 Skill、Plugin、MCP
随自身打包、上传、下载、安装，沿用现有「资源文件夹 → 确定性 tar.gz → 校验 → 解包 → 落盘」
链路，不引入第二套打包或传输机制。

## 2. 资源布局

```text
<agent-root>/
├── agent.yaml            # 必需
├── assets/               # 可选（ADR-023，不变）
├── skills/<name>/        # 可选：一个完整 Skill（SKILL.md + skill-card.md + …）
├── plugins/<name>/       # 可选：一个完整 APC Plugin（apc-plugin.json + skills/…）
└── mcp/<name>.json       # 可选：一个完整 MCP 资源（单 JSON 文件）
```

- 子资源是裸目录/裸文件，整体仍是一个 Expert `.tar.gz`，归档根不包裹 `<name>/`。
- 身份取自子资源内部内容，不取目录名；目录名只是容器（非空、不以点号开头）。
- 同一类型内身份唯一；跨类型可同名。

## 3. 校验规则

| 子资源 | 标记 | 复用校验 |
| --- | --- | --- |
| `skills/<n>/` | `SKILL.md` + `skill-card.md` | `internal/skillartifact`（与独立 Skill、Plugin 内嵌 Skill 同一实现） |
| `plugins/<n>/` | `apc-plugin.json` + 声明的 `skills/` 子树 | `pluginartifact` 的 `ValidateSubmitAPCPluginManifest` 与 `ValidateRawPackageEntries` |
| `mcp/<n>.json` | 单层 `.json` 普通文件 | `domain.DecodeMCPResourceJSON` + `ValidateSubmit` |

既有安全规则对子树继续生效：路径规范化、重复路径、symlink/特殊文件、可执行位、嵌套归档、
单文件 10 MiB、总量 100 MiB、深度 16、路径 255 字节。

## 4. 分层

`submission` 依赖 `agentartifact`，而 `agentartifact` 现在也要校验 Skill 子树，因此 Skill 格式
校验从 `submission` 下沉为叶子包 `internal/skillartifact`：

- `submission` 的 `parseSkillFrontmatter`/`validateSkillCard` 改为薄委托；
- 用 `errors.Join` 把 `skillartifact.ErrInvalid` 桥接到 `submission.ErrInvalid`，保持既有
  `errors.Is` 断言与错误文案；
- `agentartifact` 只依赖 `domain`、`pluginartifact`、`skillartifact`，方向保持单向。

## 5. 落盘与链路

放宽点只有 `agentartifact.Validate` 一处，因此下列路径自动获得新规则：

- submission：`validate`/`submit` 接受并打包子资源；
- Cloud 下载：`cloud/remote` 的 Agent 解包策略只要求 `agent.yaml` marker、**无条目白名单**，
  解包后执行同一个 `ValidateDirectoryFd`；
- cache：`PrepareExpert` 的完整目录校验；
- 安装：`bibitAgentAdapter` 的 staging/target/backup/quarantine 校验；
- 恢复：filesystem 事务的目录校验。

安装后 `skills/`、`plugins/`、`mcp/` 原样保留在 `agents/<name>/` 内。

## 6. 验收

| 编号 | 场景 | 期望 |
| --- | --- | --- |
| SUB-01 | Agent 带完整 Skill/Plugin/MCP 子资源 | `validate`/`submit` 通过，打成单一 `.tar.gz` |
| SUB-02 | 两次打包同一资源 | 字节与 MD5 完全一致 |
| SUB-03 | Skill 缺 `SKILL.md` 或 `skill-card.md` | 整份资源被拒 |
| SUB-04 | Plugin 缺 `apc-plugin.json` | 整份资源被拒 |
| SUB-05 | Plugin 声明了缺失的 Skill 子树 | 整份资源被拒 |
| SUB-06 | Plugin 含未声明 Skill 目录 | 整份资源被拒 |
| SUB-07 | MCP 文件不可解码 | 整份资源被拒 |
| SUB-08 | 两个 Skill 同 frontmatter name | 被拒并点名冲突路径 |
| SUB-09 | Skill 与 MCP 同名 | 允许 |
| SUB-10 | 子资源内含可执行文件或嵌套归档 | 被拒（既有安全规则生效） |
| SUB-11 | 无子资源的既有 Agent 资源 | 继续通过（回归） |
| SUB-12 | 未知根条目（如 `extra/`） | 仍被拒 |
| SUB-13 | 安装到 Bibit | 子资源落在 `agents/<name>/` 内，不提升到根 |
| SUB-14 | 残缺子资源安装 | 发布前被拒且不留可见目标 |

测试位置：`internal/skillartifact/skill_test.go`、`internal/agentartifact/subresource_test.go`、
`internal/submission/expert_test.go`、`internal/adapter/agent_subresource_test.go`。

## 7. 已知限制

`agent.yaml` 的 `skills`/`mcps` 是原样透传的扩展字段，本 feature **不校验**它们与 `skills/`、
`mcp/` 目录是否一一对应。强制一致会把既有合法资源变成非法；是否收紧需先确定 Bibit Runtime
按字段引用还是按目录扫描，本仓库无法确认。

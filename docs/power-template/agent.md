# Agent 模板

Agent 提交对象是**资源根目录**，APM 把整棵树打包为确定性 `.tar.gz`；目录根闭集为必需普通文件 `agent.yaml`，以及四个可选目录 `assets/`、`skills/`、`plugins/`、`mcp/`。不接受单个原始 `agent.yaml`、用户预制归档或远程 URL。字段源为 `agent_yaml_配置模板_V0.5.yaml`。

```text
bibit-edge-assistant/
├── agent.yaml
├── assets/                     # 可选：展示资产
│   └── avatar.png              #   可选，被 metadata.avatar 引用
├── skills/                     # 可选：只属于本 Agent 的 Skill
│   └── <skill-name>/           #   一个完整 Skill：SKILL.md + skill-card.md + …
├── plugins/                    # 可选：只属于本 Agent 的 Plugin
│   └── <plugin-name>/          #   一个完整 APC Plugin：apc-plugin.json + skills/…
└── mcp/                        # 可选：只属于本 Agent 的 MCP
    └── <mcp-name>.json         #   一个完整 MCP 资源（单 JSON 文件）
```

子资源用于「配套能力随 Agent 一起交付」：使用者安装这个 Agent 就得到完整可用的能力组合，
不需要再单独安装配套的 Skill/Plugin/MCP（[ADR-046](../ivory-tower/adr/ADR-046-allow-expert-to-carry-exclusive-sub-resources.md)）。
每个子资源都是**裸目录/裸文件**，内容与独立的 Skill、Plugin、MCP 资源完全一致，可直接从
对应模板拷入，不需要改写格式，也不需要额外打包。

```yaml
schema_version: 2

agent:
  # ---- 必填基础信息 ----
  id: bibit-edge-assistant          # 必须与 name 相同：Bibit 按 agent.id 定位 agents/<agent.name> 目录
  version: v1.2.0                   # vMAJOR.MINOR.PATCH
  name: bibit-edge-assistant        # 外部资源名称（canonical），不允许包含空格；须与 id 相同
  name_zh: Bibit端侧智能助手         # 中文展示名，仅用于 APC 前端展示；提交必填，2–16 个字符
  name_en: Bibit Edge Assistant      # 英文展示名，仅用于 APC 前端展示；提交必填，≤ 32 个可打印 ASCII 字符且至少含一个字母
  description: Edge-first assistant powered by Bibit Intent for device operations and general tasks
  description_zh: 端侧优先的智能助手，通过 Bibit Intent 完成设备操作与通用任务。  # 中文描述；提交必填
  soul: |-
    你是 Bibit 端侧优先智能助手，负责在 Bianbu 设备上帮助用户完成系统查询、
    设备控制、故障排查和通用任务。

  # ---- 以下为可选扩展字段示例，APM 不校验、安装时原样透传 ----
  model: # [可选]
    provider: custom:chat
    name: deepseek-v4-flash
  tools: # [可选]
    - browser_navigate
  skills: # [可选]
    - test-driven-development
  mcps: # [可选]
    - fileSystem
  hooks: # [可选]
    - bianbu.audit/audit.after_tool
  permissions: # [可选]
    capabilities:
      - name: file.read
        description: Read user documents
        domain: resource
        risk: L2
        confirmation: once_per_session
        scope: "~/Documents"
    resource_limits:
      tokens:
        per_session: 100000
        per_day: 500000
        on_exceeded: pause_and_notify
    network:
      mode: allowlist
      domains:
        - api.openai.com
    data_protection:
      personal_data:
        - type: email
          risk: L2
          action: audit_only

  metadata:
    avatar: assets/avatar.png       # [可选]：assets/ 下真实方形 PNG
```

约束：

- 输入必须是资源根**目录**；`validate`/`submit` 不接受单个 `agent.yaml` 文件、预制 `.tar.gz` 或符号链接。
- 根闭集为必需普通文件 `agent.yaml` 与可选目录 `assets/`、`skills/`、`plugins/`、`mcp/`，不允许其它根条目；这些目录内只允许普通文件与目录；归档根不额外包裹 `<name>/` 版本目录。
- 子资源的**身份取自其内部内容**，不取目录名：Skill 看 `SKILL.md` frontmatter 的 `name`/`version`，Plugin 看 `apc-plugin.json`，MCP 看 JSON 内的 `name`/`version`。目录名只是容器（须非空、不以点号开头），因此可以把独立资源目录原样拷入。
- 每个子资源必须自洽：`skills/<n>/` 必须有 `SKILL.md` 与 `skill-card.md`；`plugins/<n>/` 必须有 `apc-plugin.json`，且其声明的 `skills/` 子树必须齐全、不得出现未声明目录；`mcp/<n>.json` 必须是可解码的完整 MCP 资源。缺任何一项都会让整份资源校验失败。
- 同一类型内身份唯一：不能有两个同名 Skill、两个同名 Plugin 或两个同名 MCP；不同类型可以同名（Skill `demo` 与 MCP `demo` 可共存）。
- 子资源内的 Skill/Plugin/MCP 遵循各自类型的完整 v2 提交规则（展示字段必填等），与独立提交同源。
- 既有安全规则对子资源继续生效：不允许可执行文件、嵌套归档、symlink 或特殊文件，单文件 ≤ 10 MiB、总量 ≤ 100 MiB、深度 ≤ 16、路径 ≤ 255 字节。
- 子资源计入 Agent 归档体积，整份上传仍受 20 MiB 与 100× 解包倍率约束；配套资源较多时需要自行裁剪。
- 安装后 `skills/`、`plugins/`、`mcp/` 原样保留在 `agents/<agent.name>/` 下，APM 不会把它们拆到 Bibit 全局目录。
- `agent.skills` / `agent.mcps` 仍是原样透传的扩展字段，APM **不校验**它们与 `skills/`、`mcp/` 目录是否一一对应。
- 单文档，所有 mapping 键为字符串且无重复，根与 `agent` 均为 mapping。
- 必填字段：整数 `schema_version: 2`；`agent.id`/`agent.name`/`agent.version`/`agent.description`/`agent.soul` 均为 trim 后非空字符串；`agent.name_zh`/`agent.name_en`/`agent.description_zh` 提交时必填，仅用于 APC 前端展示（`name` 仍是调用名与默认英文名）。`agent.description` 与 `agent.description_zh` 均不超过 500 个 UTF-16 码元（[ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md)）。
- `agent.zh_name` 已更名为 `agent.name_zh`；旧键会被显式拒绝，不会静默忽略。
- `agent.name` 是外部 PowerRef 名称（canonical Power name）；`agent.version` 为 `vMAJOR.MINOR.PATCH`；`agent.id` 是 Bibit 内部身份标识，但必须与 `agent.name` 取相同值——Bibit 按 `agent.id` 定位已安装的 `agents/<agent.name>` 目录，取值不同会「能列出、按 ID 查不到」。APM 不比较二者，故 `id != name` 不是校验错误；`apm validate --type agent` 会对它输出一条 warning（[ADR-033](../ivory-tower/adr/ADR-033-require-agent-id-to-equal-agent-name.md)、[ADR-034](../ivory-tower/adr/ADR-034-report-agent-id-name-mismatch-as-validate-warning.md)），按 warning 修正即可。
- `metadata.avatar` 可选：存在时必须引用 `assets/` 下以 `.png` 结尾的真实普通文件，大小 `1..1 MiB`、宽高均为 `128..1024` 像素且为正方形；不校验 DPI、色彩空间或透明度。
- 其余字段原样透传；APM 不校验 model/tools/skills/mcps/hooks/permissions 等扩展字段的业务枚举与约束。
- 提交 content 为确定性 `tar.gz`，Content-Type 为 `application/gzip`，MD5 针对归档原始字节。

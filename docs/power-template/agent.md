# Agent 模板

Agent 提交对象是**资源根目录**，APM 把整棵树打包为确定性 `.tar.gz`；目录根闭集为必需普通文件 `agent.yaml` 与可选 `assets/` 目录，不接受单个原始 `agent.yaml`、用户预制归档或远程 URL。字段源为 `agent_yaml_配置模板_V0.5.yaml`。

```text
bibit-edge-assistant/
├── agent.yaml
└── assets/               # 可选
    └── avatar.png        # 可选，被 metadata.avatar 引用
```

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
- 根闭集为必需普通文件 `agent.yaml` 与可选 `assets/` 目录，不允许其它根条目；`assets/` 内只允许普通文件与目录；归档根不额外包裹 `<name>/` 版本目录。
- 单文档，所有 mapping 键为字符串且无重复，根与 `agent` 均为 mapping。
- 必填字段：整数 `schema_version: 2`；`agent.id`/`agent.name`/`agent.version`/`agent.description`/`agent.soul` 均为 trim 后非空字符串；`agent.name_zh`/`agent.name_en`/`agent.description_zh` 提交时必填，仅用于 APC 前端展示（`name` 仍是调用名与默认英文名）。`agent.description` 与 `agent.description_zh` 均不超过 500 个 UTF-16 码元（[ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md)）。
- `agent.zh_name` 已更名为 `agent.name_zh`；旧键会被显式拒绝，不会静默忽略。
- `agent.name` 是外部 PowerRef 名称（canonical Power name）；`agent.version` 为 `vMAJOR.MINOR.PATCH`；`agent.id` 是 Bibit 内部身份标识，但必须与 `agent.name` 取相同值——Bibit 按 `agent.id` 定位已安装的 `agents/<agent.name>` 目录，取值不同会「能列出、按 ID 查不到」。APM 不比较二者，故 `id != name` 不是校验错误；`apm validate --type agent` 会对它输出一条 warning（[ADR-033](../ivory-tower/adr/ADR-033-require-agent-id-to-equal-agent-name.md)、[ADR-034](../ivory-tower/adr/ADR-034-report-agent-id-name-mismatch-as-validate-warning.md)），按 warning 修正即可。
- `metadata.avatar` 可选：存在时必须引用 `assets/` 下以 `.png` 结尾的真实普通文件，大小 `1..1 MiB`、宽高均为 `128..1024` 像素且为正方形；不校验 DPI、色彩空间或透明度。
- 其余字段原样透传；APM 不校验 model/tools/skills/mcps/hooks/permissions 等扩展字段的业务枚举与约束。
- 提交 content 为确定性 `tar.gz`，Content-Type 为 `application/gzip`，MD5 针对归档原始字节。

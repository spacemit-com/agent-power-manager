# Power 提交格式校验说明

`apm validate` 与 `apm submit` 覆盖 Skill、Plugin、MCP、Agent 四类 Power，复用 `internal/submission` 的同一套离线校验。本文件汇总各类 Power 提交时的格式检验规则，配套的四份可复制模板见：

- [skill.md](skill.md)
- [plugin.md](plugin.md)
- [mcp.md](mcp.md)
- [agent.md](agent.md)

## 公共规则（四类共用）

| 环节 | 规则 |
| --- | --- |
| 入口 | `validate` 只做完整离线校验；`submit` 复用同一套校验，并额外要求 trim 后非空的 `--message` |
| 名称 | 1–64 字节小写 kebab（首尾不能是 `-`、无 `--`、仅 `a-z0-9-`） |
| 版本 | 严格 `vMAJOR.MINOR.PATCH`（semver strict，拒绝 prerelease 与 metadata） |
| 描述 | trim 后非空 |
| 物理安全 | 拒绝符号链接、硬链接（link count ≠ 1）、设备/FIFO/socket 等非普通文件；读取前后校验 inode 与大小一致 |
| 内容安全 | 拒绝可执行二进制、嵌套压缩包、非 UTF-8、NUL 与控制字符（Agent 的 `assets/` 允许二进制普通文件，仅 `agent.yaml` 走文本内容策略） |
| 上传上限 | 打包后 ≤ 20 MiB；gzip 类型另校验解压倍率 ≤ 100× |

源目录阈值（Skill/Plugin/Agent）：总量 ≤ 100 MiB、单文件 ≤ 10 MiB、≤ 1000 节点、深度 ≤ 16、路径 ≤ 255 字节。

## 各类型专属格式检验

### Skill（目录）

- 根下必须存在 `SKILL.md` 与 `skill-card.md`。
- `SKILL.md` 首字节起为 YAML frontmatter：mapping、无重复键；`name`/`version`/`description` 均为非空字符串；`version` 须符合 `vMAJOR.MINOR.PATCH`。
- `skill-card.md` 首字节起为 YAML frontmatter：`schema_version` 必须为整数 `1`（缺失或非 1 拒绝；未知键允许忽略）。
- `skill-card.md` 正文首行为 `# Skill Card: <显示名>`，并填写 Capability、Use Cases、Inputs and Outputs、Dependencies、Limitations、Maintainer 六个非空章节；Maintainer 含非空 Owner 与 Contact/Support，拒绝 `REPLACE_OWNER`/`REPLACE_CONTACT` 模板占位。

### Plugin（目录）

- 根下存在权威索引 `apc-plugin.json`：`schema_version` 为 `1`，`name`/`version`/`description` 走公共规则，`skills` 为非空且每项为单层 `skills/<canonical-name>` 相对路径、无重复。
- `skills/` 下目录数与声明数一致（拒绝未声明 Skill 目录）；每个声明 Skill 目录含 `SKILL.md` + `skill-card.md`，并通过与独立 Skill 相同的校验；各内嵌 Skill 的 frontmatter name 全局唯一。
- 只允许 `apc-plugin.json`、`skills/` 与已声明 Skill 子树，拒绝额外根 payload 与任何 Agent 原生 manifest（`plugin.yaml` 由 APM 从 `apc-plugin.json` 派生，含 `schema_version: 1` 且不含 `author`，并按目标 Agent 落入独立落盘叶子）。

### MCP（单个完整 JSON 文件）

- `schema_version` 为 `1`；`name`/`version`/`description`/`definition` 必填；未知字段、重复键、尾随值拒绝。
- `definition.transport` ∈ {stdio, http}，字段「出现即拒绝」：
  - stdio：`command` 非空，禁止 `url`；`args` 可选。
  - http：`url` 为绝对 HTTPS 地址（host 非空、无 userinfo），禁止 `command`/`args`。

### Agent（资源根目录）

- 根闭集为必需普通文件 `agent.yaml` 与可选 `assets/` 目录；不允许其它根条目，不包裹 `<name>/` 版本目录。
- 单文档、所有 mapping key 为字符串且无重复、根为 mapping。
- 五个必填字段：整数 `schema_version: 1`；`agent.id`/`agent.name`/`agent.version`/`agent.description`/`agent.soul` 均为 trim 后非空字符串。
- `agent.name` 为 canonical Power name；`agent.version` 为 `vMAJOR.MINOR.PATCH`；`agent.id` 是 Bibit 内部身份标识，但必须与 `agent.name` 取相同值——Bibit 按 `agent.id` 定位已安装的 `agents/<agent.name>` 目录，取值不同会「能列出、按 ID 查不到」。APM 不比较二者，故 `id != name` 不是校验错误；`apm validate --type agent` 会对它输出一条 warning（[ADR-033](../ivory-tower/adr/ADR-033-require-agent-id-to-equal-agent-name.md)、[ADR-034](../ivory-tower/adr/ADR-034-report-agent-id-name-mismatch-as-validate-warning.md)），按 warning 修正即可。
- `metadata.avatar` 可选：存在时必须为 `assets/` 下规范相对路径、以 `.png` 结尾的真实普通文件，大小 `1..1 MiB`，宽高 `128..1024` 且为正方形。
- `agent.yaml` 使用文本内容策略；`assets/` 允许二进制普通文件，APM 不校验未被 avatar 引用的 asset 业务格式。
- 其余字段原样透传，不重序列化。

## 确定性产物约定

| 类型 | 提交内容 | 说明 |
| --- | --- | --- |
| Skill / Plugin | 确定性 `tar.gz` | 归档根即资源根；条目按路径升序；uid/gid=0、mtime 固定；gzip 头无时间戳/文件名；目录 mode 0755，文件保留执行位否则 0644，清除 setuid/setgid/sticky |
| MCP | 完整文件 JCS 规范化字节 | RFC 8785：键按 UTF-16 码元升序、字符串最小转义、数字按 ECMAScript 最短表示 |
| Agent | 确定性 `tar.gz` | 根含 `agent.yaml` 与可选 `assets/`；MD5 针对归档原始字节 |

`submit` 对四类统一计算打包/规范化后的字节 MD5，并在任何网络请求前拦截本地错误；`message` 属于一次 Submission，不写入资源文件、不参与 MD5。

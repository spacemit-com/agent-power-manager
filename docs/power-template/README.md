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
| 格式版本 | 四类权威文件的 `schema_version` 必须为整数 `2`；`1` 只能在安装侧读取，提交一律拒绝（[ADR-038](../ivory-tower/adr/ADR-038-power-format-v2-localized-display-fields.md)） |
| 名称 | 1–64 字节小写 kebab（首尾不能是 `-`、无 `--`、仅 `a-z0-9-`）；`name` 是唯一调用名与排序键，**不**套用展示名的长度规则 |
| 版本 | 严格 `vMAJOR.MINOR.PATCH`（semver strict，拒绝 prerelease 与 metadata） |
| 描述 | trim 后非空 |
| 展示字段 | `name_zh`、`name_en` 与 `description_zh` 提交时必填，仅用于 APC 前端展示，不参与身份、排序、缓存键与 `search` 匹配（[ADR-039](../ivory-tower/adr/ADR-039-add-name-en-and-display-name-length-limits.md)） |
| 展示名字长 | `name_zh` 为 2–16 个 Unicode 字符（按字符计，不按字节）；`name_en` ≤ 32 个字符，逐字符必须是可打印 ASCII 且至少含一个 ASCII 字母；两者均不含控制字符 |
| 中文描述 | `description_zh` 为 trim 后非空、≤ 500 个 UTF-16 码元、允许换行（[ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md)） |
| 资源描述 | `description` 为 trim 后非空、≤ 500 个 UTF-16 码元（与 `description_zh` 同一上限） |
| 物理安全 | 拒绝符号链接、硬链接（link count ≠ 1）、设备/FIFO/socket 等非普通文件；读取前后校验 inode 与大小一致 |
| 内容安全 | 拒绝可执行二进制、嵌套压缩包、非 UTF-8、NUL 与控制字符（Agent 的 `assets/` 允许二进制普通文件，仅 `agent.yaml` 走文本内容策略） |
| 上传上限 | 打包后 ≤ 20 MiB；gzip 类型另校验解压倍率 ≤ 100× |

源目录阈值（Skill/Plugin/Agent）：总量 ≤ 100 MiB、单文件 ≤ 10 MiB、≤ 1000 节点、深度 ≤ 16、路径 ≤ 255 字节。

## 各类型专属格式检验

### Skill（目录）

- 根下必须存在 `SKILL.md` 与 `skill-card.md`。
- `SKILL.md` 首字节起为 YAML frontmatter：mapping、无重复键；整数 `schema_version: 2`；`name`/`name_zh`/`name_en`/`version`/`description`/`description_zh` 均为非空字符串；`version` 须符合 `vMAJOR.MINOR.PATCH`。
- `skill-card.md` 首字节起为 YAML frontmatter：`schema_version` 必须为整数 `2`（缺失、非整数或非 2 拒绝；未知键允许忽略；结构本身未变，升版本只为与 `SKILL.md` 取值一致）。
- `skill-card.md` 正文首行为 `# Skill Card: <显示名>`，并填写 Capability、Use Cases、Inputs and Outputs、Dependencies、Limitations、Maintainer 六个非空章节；Maintainer 含非空 Owner 与 Contact/Support，拒绝 `REPLACE_OWNER`/`REPLACE_CONTACT` 模板占位。

### Plugin（目录）

- 根下存在权威索引 `apc-plugin.json`：`schema_version` 为 `2`，`name`/`version`/`description` 走公共规则，`name_zh`/`name_en`/`description_zh` 必填，`skills` 为非空且每项为单层 `skills/<canonical-name>` 相对路径、无重复。
- `skills/` 下目录数与声明数一致（拒绝未声明 Skill 目录）；每个声明 Skill 目录含 `SKILL.md` + `skill-card.md`，并通过与独立 Skill 相同的校验；各内嵌 Skill 的 frontmatter name 全局唯一。
- 只允许 `apc-plugin.json`、`skills/` 与已声明 Skill 子树，拒绝额外根 payload 与任何 Agent 原生 manifest（`plugin.yaml` 由 APM 从 `apc-plugin.json` 派生，含 `schema_version: 1`、不含 `author`、也不含 `name_zh`/`name_en`/`description_zh`，并按目标 Agent 落入独立落盘叶子）。

### MCP（单个完整 JSON 文件）

- `schema_version` 为 `2`；`name`/`version`/`description`/`name_zh`/`name_en`/`description_zh`/`definition` 必填；未知字段、重复键、尾随值拒绝。
- `definition.transport` ∈ {stdio, http}，字段「出现即拒绝」：
  - stdio：`command` 非空，禁止 `url`；`args` 可选。
  - http：`url` 为绝对 HTTPS 地址（host 非空、无 userinfo），禁止 `command`/`args`。

### Agent（资源根目录）

- 根闭集为必需普通文件 `agent.yaml` 与可选 `assets/` 目录；不允许其它根条目，不包裹 `<name>/` 版本目录。
- 单文档、所有 mapping key 为字符串且无重复、根为 mapping。
- 必填字段：整数 `schema_version: 2`；`agent.id`/`agent.name`/`agent.version`/`agent.description`/`agent.soul` 均为 trim 后非空字符串；展示字段 `agent.name_zh`/`agent.name_en`/`agent.description_zh` 提交时必填（`agent.zh_name` 已更名，旧键会被拒绝）。
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

`submit` 对四类统一计算打包/规范化后的字节 MD5，并在任何网络请求前拦截本地错误；`message` 属于一次 Submission，不写入资源文件、不参与 MD5。提交 metadata 与 `content` 的 catalog 字段对齐：`name_zh`/`name_en`/`description_zh` 与资源格式版本 `schema_version` 一并上报，取值与资源文件完全一致；`type`/`message`/`content_md5` 是传输与校验字段，只出现在 metadata（[ADR-041](../ivory-tower/adr/ADR-041-align-submission-metadata-with-resource-content.md)）。MCP 的 `definition` 不上报 metadata——它是资源实体而不是 catalog 元数据，服务端保存 catalog 信息用 metadata、读取实际定义用 `content`（[ADR-043](../ivory-tower/adr/ADR-043-mcp-definition-from-content-only.md)）。

## 版本与兼容性

- 提交侧（`validate`/`submit`）只接受 `schema_version: 2`；v1 资源被拒绝并提示 `upgrade to 2`。
- 安装侧（`install`/下载/缓存/目录解码）继续接受 `1` 与 `2`，即已发布的 v1 资源仍可安装。
- `list`、`list --all`、`show`、`search` 的输出与匹配行为不因新字段改变；`search` 只匹配 `name`。

# Feature 026：Agent 归档资源与 Assets

- Status：Accepted
- Date：2026-08-26
- Related ADRs：[ADR-023](../../adr/ADR-023-package-agent-with-assets.md)、APC 契约 [../../../APC-API.md](../../../APC-API.md)（原 APC 仓库已停止维护）
- Input Template：`agent_yaml_配置模板_V0.5.yaml`

## 1. Goal

把 Agent 从单个原始 `agent.yaml` 升级为与 Skill、Plugin 一致的确定性 `.tar.gz`
资源，使 Agent 可以携带可选 assets，并以 `agent.metadata.avatar` 引用包内 PNG。APM 负责
V0.5 最小字段、资源身份和 avatar PNG 的完整校验；APC 继续只负责统一归档传输、最低验收、
审核与发布。

本功能同时把 Agent 的外部资源身份从 `agent.id` 修正为 `agent.name`。`agent.id` 只供 Bibit
内部区分，Catalog、CLI、manifest、cache 和安装目录统一使用安装、卸载命令中的 Agent name。

## 2. Scope

### 2.1 Included

- Agent 权威 content 从原始 YAML 改为确定性 `.tar.gz`。
- 固定 `agent.yaml + assets/` 包布局与目录闭集。
- V0.5 最小必填字段、PowerRef 身份与可选 avatar PNG 校验。
- `validate`、`submit` 改为接收 Agent 资源根目录。
- Local/Remote Cloud、版本化 cache、staging 与 Bibit Adapter 改为目录语义。
- Agent 整目录的安装、升级、卸载与 recovery 校验。
- Agent 与 Skill/Plugin 的公共归档限制、Submission multipart 和 content 接口对齐。
- APM/APC 设计、接口规范、模板和项目概览同步。

### 2.2 Excluded

- 校验 model、tools、skills、mcps、hooks、permissions 等可选 YAML 字段。
- 校验未被 `metadata.avatar` 引用的 asset 内容格式。
- 为 Bibit 增加多版本同时激活或 `current` 指针。
- 为 Agent 引入 Plugin Resource、派生 manifest 或 marketplace。
- 兼容原始 `application/yaml` Agent content 或单文件 CLI 输入。
- 修改 Feature 025 已确定的 lifecycle 入口状态、outcome、warning 或公共冲突错误。

## 3. Canonical Resource Model

### 3.1 Package layout

Agent 资源根闭集为：

```text
agent.yaml
assets/          # optional
  ...
```

规则如下：

- 根目录必须直接包含一个物理普通文件 `agent.yaml`；
- 根目录除 `agent.yaml` 和可选 `assets/` 外不允许其他条目；
- `assets/` 内只允许普通文件和目录；
- 禁止符号链接、硬链接、设备、FIFO、socket、路径逃逸、绝对路径、歧义路径和嵌套归档；
- Agent 文件规范化为不可执行普通文件，目录模式规范化为 `0755`；
- 归档根不额外包裹 `<name>/` 目录。

`assets/` 可以包含 avatar 之外的资源。APM 只对 avatar 指向的 PNG 执行业务格式校验；其余
asset 仍受物理树、路径、类型、大小和归档安全限制。

### 3.2 Identity and version

Agent 外部身份为：

```text
PowerRef{
  Type:    agent,
  Name:    agent.name,
  Version: agent.version,
}
```

- `agent.name` 用于 Catalog、Submission metadata、CLI、APM manifest、cache、staging 与最终
  安装目录；
- `agent.version` 必须与请求和 Catalog 精确版本一致；
- `agent.id` 只要求为非空字符串，由 Bibit 内部消费，不与 PowerRef name 比较；
- APC 和 APM 均处于开发阶段，本次直接迁移，不保留 `agent.id == PowerRef.name` 或原始 YAML
  content 的兼容分支。

### 3.3 Versioned and active paths

版本化 cache 保存完整目录：

```text
cache/agents/<agent.name>/<agent.version>/
├── agent.yaml
└── assets/
```

Bibit 最终只保留当前激活版本：

```text
<BIANBU_AGENT_HOME>/agents/<agent.name>/
├── agent.yaml
└── assets/
```

精确版本由 cache 路径、PowerRef 和 APM manifest 表达。最终路径不增加版本层级，避免要求
Bibit 理解版本选择或 `current` 指针。

## 4. APM Validation Contract

### 4.1 Authoritative directory validator

APM 提供一个面向 Agent 目录的权威只读校验入口。以下边界必须复用它，不能各自维护字段或
avatar 规则：

- Submission `validate` 与 `submit`；
- Local Cloud fixture snapshot；
- Remote Cloud 下载解包后；
- cache miss 发布前与 cache hit；
- Bibit Adapter 安装前；
- transaction recovery 对 target、backup、quarantine 和 staging 的检查。

校验器返回稳定的 `agent.name`、`agent.version`、`agent.description` 与 Bibit `agent.id`，但
不得清洗、重排或重序列化 `agent.yaml`。

### 4.2 YAML minimum boundary

APM 接受一个合法 YAML document，拒绝多 document、重复 mapping key 和有歧义的非字符串
mapping key。只校验以下稳定边界：

- `schema_version` 必须存在且为整数 `1`；
- `agent.id`、`agent.name`、`agent.version`、`agent.description`、`agent.soul` 必须存在，
  类型为字符串且 trim 后非空；
- `agent.name` 必须是 APM canonical Power name；
- `agent.version` 必须是 canonical `vMAJOR.MINOR.PATCH`；
- 内容与 PowerRef 比较时，type 必须为 `agent`，name/version 分别与
  `agent.name`/`agent.version` 完全一致。

未知字段和其余可选字段允许存在并原样透传。APM 不校验模板注释中关于模型、权限、标签、
工具或能力装配的业务枚举和约束。

### 4.3 Avatar PNG

`agent.metadata.avatar` 可选。缺省时不要求 `assets/` 存在，也不扫描其他 PNG。

存在时必须满足：

- 是 trim 后非空的字符串；
- 是 `assets/` 下使用 `/` 分隔的规范包内相对路径；
- 禁止绝对路径、`.`/`..`、反斜杠、路径逃逸和目录目标；
- 文件名以 `.png` 结尾，目标是同一固定快照内的物理普通文件；
- 文件大小在 `1..1 MiB`；
- PNG 签名有效，图片解码器识别的格式精确为 PNG；
- 宽、高分别在 `128..1024` 像素，且宽等于高。

不校验 DPI、色彩空间、透明通道、动画或像素内容质量。

### 4.4 Common archive limits

Agent、Skill、Plugin 共享以下归档边界：

| Boundary | Limit |
| --- | ---: |
| 单个解包文件 | 10 MiB |
| 解包文件总量 | 100 MiB |
| 节点数 | 1000 |
| 目录深度 | 16 |
| 规范相对路径长度 | 255 bytes |
| 上传/下载归档 | 20 MiB |
| 解压膨胀率 | 100x |

Agent 的 `agent.yaml` 使用文本内容策略。`assets/` 允许二进制普通文件，因此公共物理树与远端
归档读取需要接受按 Power 类型提供的内容策略，不能继续对 Agent assets 套用全树文本限制。

## 5. Submission and APC Contract

### 5.1 CLI source

```text
apm validate <agent-resource-directory> --type agent [--json]
apm submit <agent-resource-directory> --type agent --message <message>
```

Agent 与 Skill/Plugin 一样只接受资源根目录。APM 不接受单个 `agent.yaml`、用户预制
`.tar.gz`、符号链接或远程 URL。Loader 在同一次物理读取中固定路径、mode 和正文，后续
Validate 与 Build 只消费不可变快照。

确定性归档使用稳定路径排序、时间、owner/group、mode 和 gzip header。同一目录内容必须生成
完全相同的字节与 MD5。

### 5.2 Multipart submission

Agent 使用既有 Submission endpoint 和两个固定 part：

| Part | Content-Type | Content |
| --- | --- | --- |
| `metadata` | `application/json` | type/name/version/description/message/content_md5 |
| `content` | `application/gzip` | 确定性 Agent `.tar.gz` |

`content` filename 为 `<name>-<version>.tar.gz`。metadata 的 name/version/description 来自已
校验的 `agent.name`、`agent.version`、`agent.description`，不由目录名或 CLI 覆盖。

### 5.3 APC bytes

Catalog 与 Submission detail 中 Agent `content_type` 固定为 `application/gzip`。content size
和 MD5 针对上传归档原始字节。APC 不解包后重打包、不修改 YAML/PNG；提交、审核和发布使用
同一字节对象。

公开精确 content endpoint 保持：

```http
GET /api/v1/resources/agent/{name}/versions/{version}/content
```

成功响应为 `application/gzip`、精确 Content-Length 和 Catalog 所列 MD5 对应的原始归档。

APC 对 Skill、Plugin、Agent 三种 gzip content 共用同一最低验收：上传大小、MD5、gzip/tar
完整可读、路径不逃逸、条目类型合法和通用阈值。APC 不解析三种 Power 的业务 marker、身份、
字段或 avatar PNG。

## 6. Cloud, Cache and Install Flow

### 6.1 Type-specific interface, shared archive mechanics

保留 `DownloadAgent(ctx, entry, destination)` 和 `PrepareAgent(entry)` 等 Agent 专属接口，避免把
Agent 校验参数泄漏给 Skill；内部复用 Skill/Plugin 的物理树快照、安全归档下载、两遍校验解包
与原子目录发布机制。

`DownloadAgent` 的 destination 从私有文件改为私有目录：

- Remote 校验 response type/length、Catalog size/MD5 和归档安全，解包后执行 Agent 目录校验；
- Local fixture 使用 `agents/<name>/<version>/agent.yaml + assets/`，固定物理树后执行相同校验；
- Local 与 Remote 成功后必须产生可归一比较的相同目录树；
- 任一失败不得留下可见 destination。

### 6.2 Cache and staging

`PrepareAgent(entry)`：

1. cache hit 时完整复验目录和 PowerRef 身份；
2. cache miss 时下载到唯一临时目录；
3. 下载后再次执行权威 Agent 目录校验；
4. 原子发布 `cache/agents/<name>/<version>`；
5. 从已校验 cache 物化 `staging/agents/<name>`。

损坏或身份不匹配的 cache fail closed 并保留现场，不静默删除后联网替换。

### 6.3 Bibit Adapter and transaction

`AgentAdapter.TargetPath(ref)` 返回 `<bibit-root>/agents/<ref.Name>` 目录。Adapter 从
`staging/agents/<ref.Name>` 复验完整目录，再以目录 rename 发布；不逐文件覆盖已安装目标。

既有 filesystem journal schema 不变，`target_path`、backup、quarantine 本来就可以指向目录：

- 升级先把旧目录移入 backup，再发布新目录；失败或中断恢复旧目录；
- 卸载先把目录移入 quarantine，再删 manifest，最后清理 quarantine；
- recovery 在决定恢复或提交前验证完整 Agent 目录及 PowerRef，不只验证 `agent.yaml`；
- 安装成功后 manifest 记录 ref version，最终目录不另建版本层级。

## 7. Lifecycle and Errors

Feature 025 的宏观状态契约不变：

- manifest 同版本直接 `unchanged`，不检查目标；
- manifest 不同版本、旧目标缺失或不完整时，记录一次缺失 warning，清理清单/残留后按首次
  安装发布新版本，成功 outcome 为 `installed`；
- 无 manifest 且目标目录存在时拒绝接管，只保证 `adapter.ErrTargetConflict`；
- 卸载无 manifest 时不 probe，warning 后 `unexecuted`；
- 卸载有 manifest、目标缺失或不完整时记录一次 warning，清理残留与清单并 `completed`。

目录内 `agent.yaml` 与 avatar 同时缺失仍只对应一个 lifecycle target-missing warning。语义、
身份或安全冲突不降级为 missing，必须 fail closed。

错误分类：

| Boundary | Error |
| --- | --- |
| 本地 validate/submit | Submission `ErrInvalid`，保留具体 Agent 原因 |
| Agent 语义/身份 | `domain.ErrInvalidAgent` |
| Remote HTTP 契约 | Cloud `ErrInvalidResponse` |
| size/MD5 | Cloud `ErrIntegrity` |
| 非法归档/物理树 | Cloud `ErrUnsafeContent` |
| 无清单目标冲突 | `adapter.ErrTargetConflict` |

APC 继续使用 `400 INVALID_REQUEST`、`413 CONTENT_TOO_LARGE`、`422 CONTENT_MD5_MISMATCH` 和
异步 `RESOURCE_VALIDATION_FAILED`。归档最低验收的 `INVALID_GZIP`、`INVALID_TAR`、
`UNSAFE_ARCHIVE_ENTRY` 等只作为可展示子错误，APM 不据此分支。

## 8. Verification

实现必须覆盖：

- 五个必填 Agent 字段逐项缺失、空白与错误类型；未知字段透传；
- PowerRef 使用 `agent.name`，不同 `agent.id` 仍合法；
- avatar 缺省、真实/伪造 PNG、空文件、1 MiB、128/1024 边界、非正方形、路径逃逸和缺失；
- 根闭集、二进制 asset、特殊文件、链接、嵌套归档与所有公共阈值；
- 固定快照和确定性 tar.gz/MD5；
- Agent multipart filename、Content-Type 与 raw bytes；
- Local/Remote 下载目录 parity 及 HTTP/integrity/archive 失败；
- cache miss/hit/损坏、staging 与最终目录；
- 整目录首次安装、升级、卸载、rollback 和 crash recovery；
- Feature 025 的 Agent install/uninstall 状态矩阵、warning 数量、outcome 和公共错误回归。

## 9. Documentation and Migration

实现阶段同步更新：

- APM `BUILD.md`、`ivory-tower/project-overview.md`、相关 C4 与历史 Feature/ADR 取代说明；
- `docs/power-template` 中的 Agent 模板和规则，以 V0.5 为输入并把 avatar 收窄为包内 PNG；
- APC `docs/APC-API.md`、两个 project overview、相关 C4、历史 ADR-001 与 Feature 001/002；
- 两边文档相互引用 APM ADR-023 与 APC ADR-006；
- 行为实现完成并验证后，APM `docs/Changelog.md` 增加一条本用户请求记录。

迁移直接替换现有开发 fixture、测试数据和 APC 开发数据。旧 `application/yaml` Agent、单文件
cache/staging/target 与旧 multipart 示例不保留兼容入口。

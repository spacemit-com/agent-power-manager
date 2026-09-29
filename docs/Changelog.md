# Changelog

记录约定：承载或维护当前 Changelog 条目的当前提交不列入自身的“相关提交”，避免提交哈希自引用。

## 2026-09-29（MCP 定义改由 content 提供，提交 metadata 不再携带 definition）

分类：契约变更（MCP 提交 metadata 删除 `definition`；`SubmissionRequest` 删除 `Definition` 字段）

用户需求：APC 的 MCP catalog 信息改为从 metadata 保存（含 `name`、描述等，但不包含定义），实际定义
从 content 读取；要求 APM 的提交按此修订同步。

背景：ADR-041 让 metadata 与 content 的资源元数据逐字段对齐，其中包含 MCP 的 `definition`。验证该组合
能否真正提交时发现两端都不接受：测试环境只要 metadata 含 `definition` 就 500（`schema_version` 1/2、
有无展示字段都一样），生产环境相反——含 `definition` 才接受、带了展示字段才 500。因此
「v2 + 展示字段 + definition」两个环境都是 500。用 CLI 原始 JCS 字节与手工 multipart 各复现一次，
结果一致，确认与客户端无关。单独验证还发现测试环境在 metadata 不带 `definition` 时收下提交，
但生成的提交详情里没有 definition，即当时并未从 content 重建。

解决方案或完成结果：`submissionMetadataDTO` 删除 `Definition` 字段（连带 `json:"definition,omitempty"`），
metadata 固定为 `schema_version`/`type`/`name`/`version`/`description`/`name_zh`/`name_en`/
`description_zh`/`message`/`content_md5`，四类资源一致；MCP 的启动定义只随 content 的 JCS 字节上传，
服务端从 content 读取。`remote.Client.Submit` 删除「`submit remote MCP resource requires definition`」
前置检查——它原本是「metadata 必须有 definition」的前提，定义已在 content 里，保留只会误拒正确调用。
`SubmissionRequest.Definition`、`submission.Content.Definition`、`source.definition` 与
`mcpSourceLoader` 中克隆并透传 `resource.Definition` 的代码一并删除，避免留下无消费者的死字段。

关键决策（见 [ADR-043](../ivory-tower/adr/ADR-043-mcp-definition-from-content-only.md)，
局部取代 ADR-041 中把 `definition` 列入两侧对齐字段的条款）：

- **区分 catalog 元数据与资源实体**：metadata 只承载 catalog 需要的信息（身份、描述、展示字段、格式版本），
  资源实体（MCP 定义）只从 content 读取。`definition` 由此不再是「对齐字段」。
- **本地校验不放宽**：`MCPResource.ValidateSubmit` 继续要求 `definition` 存在且形状合法。定义是 MCP 的
  实质内容，缺失即资源不完整，仍由 `apm validate` 拦下；本轮只改传输层，不改资源格式。
- **删除而非保留死字段**：`SubmissionRequest.Definition` 即使不再序列化也一并删除，否则后续维护者会误判
  它仍参与上传。
- **只改 MCP**：skill/plugin/agent 的 metadata 已被实测接受且没有 definition 语义，不改其字段集。
- **发布顺序仍为 APC 先、APM 紧跟**：若 APC 仍要求 metadata 携带 `definition`，新 APM 的 MCP 提交会被拒。

测试：`TestSubmitMetadataMirrorsResourceContent` 改为同时断言 metadata **不含** `definition`、content
**含** `definition`；`TestSubmitMCPIncludesDefinitionInMetadata` 改为
`TestSubmitMCPKeepsDefinitionOutOfMetadata`；`TestSubmitRejectsMCPWithoutDefinitionBeforeRequest` 改为
`TestSubmitMCPDoesNotRequireDefinitionField`（不再有该前置检查，正常提交返回 202）；
fixture server 的 `assertReceivedMCPDefinition` 改为 `assertReceivedMCPDefinitionInContent`，
并统一断言四类资源的 metadata 都不带 `definition`；`prepare_test`/`lifecycle`/`domain` 的
Definition 断言改为断言 content 字节里带定义。`go build ./...`、`go vet ./...` 与全量 `go test ./...` 通过。

边界：不改资源格式（MCP 文件仍必须有 `definition`）、不改安装/下载/缓存/目录解码读取
`MCPResource.Definition` 的路径、不改 skill/plugin/agent 的 metadata 字段集、
不改 APC 目录契约与 `list`/`show`/`search` 输出。`APC-API.md` 的 metadata 字段表、MCP 示例与说明，
以及 `docs/power-template/README.md` 同步更新。

## 2026-09-29（按 APC 服务端契约限定 description/description_zh 长度）

分类：契约变更（`description` 新增长度上限；`description_zh` 由 ≤ 1024 字节改为 ≤ 500 码元）

用户需求：升级 `../using-apm` 拉取的一批资源时，`k3-media-pipeline` 的英文译文（532 单元）通过了
`apm validate`，却在提交时被 APC 拒绝为 `400 VALIDATION_FAILED`，响应体不带字段名、CLI 只透出 trace_id，
定位成本高。用户要求在 `apm validate` 中补上 `description` 的长度上限，使离线校验与服务端一致。

解决方案或完成结果：对测试环境做了端到端阶梯探测（metadata 与 content 两侧字段一致、资源名合法、
内容体积恒定）后确定真实契约，并按实测口径实现：

- **上限是 500，不是 1024**：`description` 与 `description_zh` 共用同一个 500 上限；服务端字段级错误为
  `submit.request.description` / `submit.request.descriptionZh` 的「不能超过500个字符」，
  `1024` 从未生效。
- **单位是 UTF-16 码元，不是字节**：500 个汉字（1500 字节）被接受，501 个汉字被拒；
  250 个 emoji（500 码元）被接受，251 个 emoji（502 码元、1004 字节）被拒。客户端若按字节判断，
  两个方向都会出错。
- **两个字段此前都无有效上限**：`description` 只检查非空；`description_zh` 检查的 1024 字节
  永远不会先触发。因此超限资源能通过离线校验、到提交才失败。

实现：`maxDisplayDescriptionBytes = 1024` 替换为 `maxDescriptionUnits = 500`；新增
`domain.descriptionUnits`（`>0xFFFF` 计 2）与 `domain.ValidateDescription`；
`ValidateDisplayDescriptionZH` 改按码元判断；四类资源的提交侧入口
（`skill.parseSkillFrontmatter`、`pluginartifact.ValidateSubmitAPCPluginManifest`、
`MCPResource.ValidateSubmit`、`AgentConfig.ValidateSubmit`）接入 `ValidateDescription`。
错误信息点名字段、实际码元数与上限。

关键决策（见 [ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md)，
局部取代 ADR-039 第 4 条与 ADR-038 中同一处「`description_zh` ≤ 1024 字节」表述）：

- **客户端上限照抄服务端实测口径**：不按字面实现「500 字节 / 1024 字节」，否则会误拒服务端接受的
  500 汉字描述（1500 字节），同时放过服务端拒绝的 251 emoji（1004 字节）。
- **计数用 UTF-16 码元**：与 JVM/JS `String.length()` 一致；不使用 `utf8.RuneCountInString`，
  否则 emoji 会被计为 1 而漏放。
- **沿用 ADR-038 第 6 条的「提供时校验形状（含长度）」分层**：安装/下载/缓存/目录解码仍只在字段存在时校验、
  缺失不报错，提供的值继续走形状与长度校验；本轮只是把 `description_zh` 的长度从永不触发的 1024 字节换成
  真实的 500 码元。存量资源不受影响（目录实测 `description_zh` 最大 190 码元、`description` 最大 483 码元）。
- **失败信息可操作**：错误里带字段名与码元数，避免重现「只有 400 + trace_id」的定位成本。

测试：`TestDescriptionLengthLimit`（单位与两个字段的边界，含 emoji/é 反例）与
`TestValidateRejectsOverlongDescription`（四类资源的提交侧门禁）；`TestDecodeRejectsInvalidDisplayFields`
的 `description_zh too long` 用例由 342 个汉字（1026 字节）改为 501 个汉字（501 码元）。
全量 `go test ./...` 通过；用 532 单元的原始失败样本复跑，`apm validate` 现在报
`description is 532 UTF-16 code units, exceeds 500`。

边界：不改 `description_zh` 之外展示字段的上限（`name_zh` 2–16、`name_en` ≤ 32）、不改安装侧兼容语义、
不改 APC 目录契约与 `list`/`show`/`search` 输出、不改服务端实现。`500` 是服务端实现细节，
两侧存在耦合：服务端若调整上限，APM 需同步发版。

## 2026-09-29（提交 metadata 与资源 content 字段对齐）

分类：契约变更（提交 metadata 新增三个展示字段；metadata `schema_version` 由协议常量 `1` 改为资源格式版本 `2`）

用户需求：端到端探测发现 APC 发布 MCP 时并用的是 metadata 而不是上传的 content，导致 v2 展示字段
「提交能过审、发布即丢」，且发布件回退成 v1 骨架、无法原样再提交。用户确认 APC 会改成从 content 重建，
并要求 metadata 与 content 的字段对齐：补齐新字段，且 metadata 的 `schema_version` 与资源版本一致，
四类资源都改。

解决方案或完成结果：`submissionMetadataDTO` 新增 `name_zh`/`name_en`/`description_zh`，取值与资源文件
完全一致；`schema_version` 改为取 `SubmissionRequest.SchemaVersion`，即资源格式版本（提交侧只接受 v2，
实际恒为 2），非 v2 或缺失时在发请求前 fail closed。`type`/`message`/`content_md5`（及 MCP 的
`definition`）作为传输专有字段保留。版本来源仍是资源文件：`submission.Validation`/`submission.Content`
新增 `SchemaVersion`，四个 loader 分别从 `SKILL.md` frontmatter、`apc-plugin.json`、`MCP.json`、
`agent.yaml` 取值，lifecycle 只做透传。

关键决策（见 [ADR-041](../ivory-tower/adr/ADR-041-align-submission-metadata-with-resource-content.md)，
局部取代 ADR-038 第 7 条与 ADR-040 Context 中「提交 metadata 不上报展示字段」两处决定）：

- **metadata 与 content 同源**：资源元数据字段（`name`/`version`/`description`/`name_zh`/`name_en`/
  `description_zh`/`schema_version` 及 MCP 的 `definition`）在两侧逐字段相等；对齐由
  `TestSubmitMetadataMirrorsResourceContent` 固定。
- **`schema_version` 只表达资源格式版本**：不再与传输协议常量混用同一键名；缺失或非 2 在上传前拒绝。
- **四类一致**：不做按类型分支，避免前端与 APC 需要区分 MCP 与其余三类。

边界：不改 `../apc` 仓库与 `APC-API.md` 之外的 APC 实现（重建逻辑由 APC 侧交付）、不改目录 item 契约与
`list`/`show`/`search` 输出、不改安装侧 v1 兼容语义、不改 content 的打包与 JCS 规范化、不追溯重发历史 MCP。
发布顺序：APC 先改成从 content 重建，APM 与之同步；已发布的 MCP 条目内容不会追溯变化。

## 2026-09-28（目录与 CLI JSON 携带 v2 展示字段，打通到前端）

分类：契约变更（APC 目录 item 新增可选字段；`list --json`/`show --json` 输出新增字段）

用户需求：前端（Agent Hub 插件）已能按应用语言显示 `name_zh`/`name_en`/`description_zh`，但 apm 从不把
这三个字段送到上层：目录解码丢弃它们、安装只把 `description`/`tags` 抄进清单、`list --json`/`show` 输出
里没有它们，导致卡片与详情只能回退到 `name`/`description`，无法随语言切换。要求打通云端市场与已安装
两个范围（含 per-version 的 `description_zh`），但 `search` 与文本输出保持不变。APC 侧会另行在公共目录
item 上补齐字段。

解决方案或完成结果：三个字段作为**可选追加字段**进入目录 item 契约与本地持久化，并**恒定出现**在
`list`/`show` 的 JSON 记录中。目录层：`domain.CatalogEntry`、`catalog.itemDTO` 与 item 字段白名单支持
`name_zh`/`name_en`/`description_zh`，`CatalogEntry.Validate` 对提供的值执行 ADR-039 形状校验（缺失合法）。
Local provider 从四类权威文件解析：`SKILL.md` frontmatter、`apc-plugin.json`、`MCP.json`、`agent.yaml`；
`equalCatalogEntry` 同步比较三个字段。持久化层：`domain.InstalledPower`、`config.powerEntry`、
`transaction.Journal` 与四个安装 request 携带三字段，`installedPowerEqual` 覆盖它们，离线 recovery 重放
写回清单时不再丢字段。装配与渲染：`cli.ListedPower` 携带三字段，`listPowerJSON`（覆盖 `list --json`、
`--all`、`--installed` 与 `--agent=all` 分组模式）与 `show` 的 JSON 记录恒带三字段（空为 `""`）。

关键决策（见 [ADR-040](../ivory-tower/adr/ADR-040-catalog-and-cli-json-carry-display-fields.md)，局部取代
ADR-038 第 7 条与 ADR-039 第 7 条中「APC 目录契约与 `list`/`show` 输出不变」两处决定）：

- **目录与存储的版本号一律不升**：envelope、分组、item 与 `/resources/version` 的 `schema_version` 保持
  `1`，本地 manifest 与 journal 的 schema version 也保持 `1`。依据实测：item 级 `schema_version` 完全不
  参与旧 APM 校验（升它无作用）；envelope/分组版本不是 `1` 会让旧客户端硬拒整份目录，而目录端点路径
  硬编码 `api/v1`、APC 无匿名可读的 v2 端点可迁，结果是所有旧客户端静默冻结在陈旧快照。
- **`search` 与全部文本输出不变**：`search --json` 仍只有 `type`/`name`/`version`/`description`，
  仍只匹配 `name`；`list`/`show` 文本逐字节不变。
- **派生清单边界不变**：Codex/Claude/Hermes/Bibit 的原生清单仍不含三字段；canonical Skill 子树是原样
  复制的内容，其展示字段必须保留。守卫测试改为按「派生清单」与「复制的 canonical 内容」逐文件区分，
  并把 fixture 换成真实 v2 Skill（原实现只对 2 行 stub 通过，属于既有隐患）。

边界：不改 `../apc` 仓库与 APC-API.md（字段与文档由 APC 侧交付）、不改 wire 协议与
`APC_BASE_URL`/认证语义、不新增 CLI 参数或环境变量、不改 `search` 匹配逻辑与文本渲染。
**发布顺序是硬约束：APM 必须先于 APC 发布**，否则旧 APM 会因目录字段白名单拒绝新目录。

相关提交：本条目随本次改动一并提交。

验证结果或限制：`gofmt -l .` 无输出、`go vet ./...` 与 `go test ./...` 全绿（域层、目录 codec、Local
provider、config、transaction、lifecycle、cli、pluginresource 均有新增锁定测试，见
[Feature 031 验证记录 §1.1](../ivory-tower/features/031-power-format-v2/verification.md)）。手工端到端以
`APM_CLOUD_PROVIDER=local APM_CLOUD_LOCAL_ROOT=<repo>/internal/cloud/resources` 验证：`list --json`、
`list <name> --all --json`、`list --installed --json` 与 `show --json` 均带三字段，安装后
`~/.apm/manifests/<agent>.json` 持久化三字段，`search --json` 无变化。已知代价：单条目录数据形状非法会让
整份目录解码失败（fail closed），依赖 APC 提交侧已强制形状；旧 APM 读新 journal 会因
`DisallowUnknownFields` 拒绝恢复（与 ADR-024 同级别风险）；**已安装记录不会自动回填**——展示字段是
安装时捕获的元数据，同版本重装命中 `manifestSameVersion` 短路并返回 `unchanged`（板端实测：手写旧清单
后同版本重装结果 `unchanged` 且清单未变，卸载后重装才写入三字段），因此升级后需要重装相关 Power 才能让
已安装范围切换语言。板端构建 `agent-power-manager_0.1.6_riscv64.deb`（SHA-256 `31d15ff4…`）覆盖安装
0.1.5 成功，`go test ./...` 22 包全绿，生产目录（APC 未发布字段）下 16 条记录全部空字段且命令正常。

## 2026-09-28（Power 资源格式升级到 V2，新增本地化展示字段）

分类：契约变更（破坏性：v1 提交被拒绝）

用户需求：四类 Power 的资源格式加入仅用于 APC 前端展示的 `name_zh`、`name_en` 与 `description_zh`，`name` 继续
作为唯一调用名；`agent.yaml` 的 `zh_name` 更正为 `name_zh`；提交拒绝 v1、全面升级到 v2；展示名带长度约束——
`name_zh` 为 2–16 个字符、`name_en` ≤ 32 个字符；不改 APC 目录契约，也不改 `list`/`show`/`list --all` 的现有输出；
`apm list --all` 仍只展示全版本，`list --json`/`show` 行为不变；`search` 不匹配展示名。

解决方案或完成结果：新增 `internal/domain/display.go`，定义 `PowerSchemaVersionV1/V2` 与展示字段校验
（`name_zh` 2–16 个 Unicode 字符、`name_en` ≤ 32 个可打印 ASCII 字符且至少含一个字母、`description_zh`
≤ 1024 字节（该 `description_zh` 上限后来由 [ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md) 取代为 ≤ 500 个 UTF-16 码元），均 trim 后非空并拒绝控制字符），并提供提交侧 `ValidateSubmitSchemaVersion` 与安装侧
`ValidateInstallSchemaVersion` 两个门禁。四类权威文件升级到 `schema_version: 2`：`SKILL.md` 新增整数
`schema_version` 与三个展示字段，`skill-card.md` 的 `schema_version` 由 1 升到 2，
`apc-plugin.json`/`MCP.json`/`agent.yaml` 同步新增字段；
`MCPResource`/`AgentConfig`/`APCPluginManifest` 各自保留宽松 `Validate`（安装侧接受 v1 与 v2）并新增
严格提交入口 `ValidateSubmit`/`ValidateSubmitAPCPluginManifest`。`agent.zh_name` 更名后旧键被显式拒绝并提示新键名。
决策记录见 [ADR-039](../ivory-tower/adr/ADR-039-add-name-en-and-display-name-length-limits.md)（新增 `name_en`
与展示名长度约束，局部取代 ADR-038 的「不引入 `name_en`」与「`name_zh` ≤ 128 字节」两条决定）。
派生 Agent 原生清单与 marketplace 继续只含 `name`/`version`/`description`/`skills`，不携带展示字段。
内置资源 fixture（15 份 Skill、2 份 Plugin、3 份 MCP、1 份 Agent）全部升到 v2；`docs/power-template/*`
模板与规则文档同步，`../using-apm` 的模板与手册另在该仓库更新（含上传前必须取得用户同意的流程）。
决策记录见 [ADR-038](../ivory-tower/adr/ADR-038-power-format-v2-localized-display-fields.md) 与
[Feature 031](../ivory-tower/features/031-power-format-v2/spec.md)。

边界：只改资源文件格式、提交侧校验与文档，不改 wire 协议、APC 目录 item 字段、`APC_BASE_URL`/认证语义、
本地 manifest 与 journal 结构、CLI 渲染与 `search` 匹配逻辑，也不新增 CLI 参数或环境变量。

相关提交：本条目随 V2 格式变更一并提交。

验证结果或限制：`go build ./...`、`go vet ./...`、`go test ./...` 全绿；新增
`TestSubmitRejectsV1Resources`、`TestSubmitRequiresLocalizedDisplayFields`、
`TestSubmitAcceptsV2ResourcesWithDisplayFields`、`TestInstallPathAcceptsV1Resources`、
`TestDerivedAgentManifestsCarryNoLocalizedFields`、`TestDerivedAgentManifestsStillDeriveForV1Artifact`
锁定提交门禁、安装兼容与派生边界；领域层另有 `TestDisplayNameZHLengthBounds`、`TestDisplayNameENLengthAndCharset`、
`TestDisplayMetadataRequiredFields` 锁定 2–16 字与 ≤ 32 字符边界。远端 v2 提交验收依赖 APC 侧从 content 解析
新字段，未就绪前记为 BLOCKED。

## 2026-09-24（默认 APC 根地址切换到生产环境）

分类：行为变更

用户需求：把 APM 默认连接的 APC 从测试环境切到生产环境，并规范使用带 `www` 的主机名；随后按
`docs/testing/apm-cli-functional-test.md` 完整回归。

解决方案或完成结果：`cmd/apm/cloud.go` 的 `defaultAPCBaseURL` 由 `https://developer.bianbu.xyz` 改为
`https://www.spacemit.com`，并加中文注释说明这是生产环境根地址、只接受 host-only、网关前缀由
`remote.Client.endpoint` 拼接。`APC_BASE_URL` 的语义与校验完全不变（绝对、无凭据/query/fragment、
根路径、非 loopback 只允许 HTTPS），测试环境改为显式配置
`APC_BASE_URL=https://developer.bianbu.xyz`。契约测试同步
（`cmd/apm/cloud_test.go` 的默认值断言与 URL 前缀断言、`TestRemoteDefaultBaseURLRequestsProductionEnvironment`
改名）。文档同步：`BUILD.md` 的 Cloud Provider 段与变量表、`ivory-tower/project-overview.md` 的运行方式条目、
`APC-API.md` 升到 v1.3（生产入口主机名由 `developer.spacemit.com` 更正为 `www.spacemit.com`，补充
"客户端基址只填主机"约束，场景示例改用生产地址）。决策记录见
[ADR-037](../ivory-tower/adr/ADR-037-default-apc-root-to-production.md)，该 ADR 局部取代 ADR-026 中默认地址一条，
其余适配决策不变。

边界：只改默认根地址与其文档/测试断言，不改 wire 路径、认证方式、目录解码、缓存与事务语义，不新增 CLI 参数
或环境变量，不引入按环境切换的内置分支。选择 `www` 而非裸域是取站点规范形式（两者同 IP、证书 SAN 同时覆盖）。

相关提交：本条目随默认值切换一并提交。

验证结果或限制：`go build ./...`、`go vet ./...`、`go test ./...` 全绿。2026-09-24 用源码 HEAD 构建的二进制对
生产环境（`APC_BASE_URL=https://www.spacemit.com`）执行完整功能测试：13 PASS / 1 FAIL / 2 BLOCKED / 1 NOT_RUN。
命令级（CLI-01 参数校验、SRH-01 检索、LST-01 默认与 `--all`、LST-02 已安装清单、SHW-01 详情、VAL-01 四类型校验）
与三个 agent（codex/claude-code/hermes-agent）的 skill+plugin 安装→重装 `unchanged`→落地校验→卸载 `completed`→
重复卸载 `unexecuted`、codex/claude-code 的 mcp、`--agent all` 批量、FIX-01 中断事务恢复全部通过。唯一 FAIL 是
hermes-agent 的 MCP 首轮超时（`hermes mcp add` 的交互确认未在 30s 内结束，APM 判 indeterminate 并自动回滚；
单独重试 3 次全部成功，裸命令耗时 4.4–9.1s），定性为不稳定而非必现。bibit 三项因开发机无 `bianbu-agent` 记
BLOCKED，SUB-01 因未取得生产提交授权记 NOT_RUN。真实 agent 配置与 `~/.apm/manifests` 时间戳未变化，无残留。
限制：生产与测试环境的目录内容当前一致（同一 `catalog_sha256`，CDN 指向同一文件），默认切换本身不能证明两边
后端独立，发布后需在生产侧单独发布资源再检索验证；`apm submission list` 无过滤仍因服务端返回终态条目而整体失败
（`contains terminal status "validation_failed"`，测试环境同样存在）。

## 2026-09-16（后续：Bibit MCP 安装改用 mcp add 对齐 Hermes）

分类：缺陷修复

用户需求：开发板 `bianbu-agent-runtime` 升级后 `mcp add` 已支持 `--url`，要求把 Bibit 的 MCP 安装对齐
Hermes，改用 `add`。

解决方案或完成结果：`internal/adapter/bibit_mcp.go` 的 `Add` 改为统一经 `mcp add` 登记：HTTP 使用
`mcp add <name> --url <url> --auth none`，stdio 使用 `mcp add <name> --command <command> [--args ...]`，
与 Hermes 适配器的命令形状一致。删除只服务于旧入口的临时目录包机制（`addHTTP`、`mkdtemp`/`mkdirAll`/
`writeFile`/`removeAll` 注入字段与 `bibitHTTPTransport`、`bibitHTTPMCPDocument`）；成功判据从「退出码为 0」
收紧为「退出码为 0 且输出中的 `id` 等于 `mcp.<name>`」，抽出 `bibitMCPDocumentID` 供 `Inspect` 与 `Add`
共用，退出码为 0 但输出无法确认时按 `unknown` 保留 journal。`Inspect`（`mcp get`）与 `Remove` 语义不变。
契约版本对齐板端 `bianbu-agent 0.1.69`：该版本已移除 `mcp install` 子命令，且 stdio 的旧写法
`mcp add <name> -- <command>` 会以退出码 2 报 `one of the arguments --command --url is required`，因此
HTTP 与 stdio 两条路径都必须改。测试同步为真实契约：`bibit_mcp_test.go` 断言新 argv 并新增「只在输出
确认目标 `id` 时记 applied」的表驱动用例；`internal/lifecycle/mcp_integration_test.go` 的假 CLI 要求
`mcp add` 携带 `--command`/`--url` 否则按真实 CLI 报错退出，并删去已不存在的 `mcp install` 分支，使回退到
旧写法直接失败。文档同步：`bianbu-agent-cli-guide.md` 的 MCP 段与
[features/018-bibit-agent/spec.md](../ivory-tower/features/018-bibit-agent/spec.md) 的 MCP 契约块改为 0.1.69，
[testing/apm-cli-functional-test.md](testing/apm-cli-functional-test.md) 第 3 节预检新增「Bibit 的 `mcp add`
必须支持 `--url`」，第 7 节补入 `data_dir` 解析顺序与 `BIANBU_AGENT_CONFIG` 隔离说明。

边界：只改 Bibit MCP 适配器的命令拼装与成功判定，不改 CLI 命令面、清单格式、journal schema、`Inspect`/
`Remove` 语义与云端资源。最低 CLI 版本提高到 `0.1.69`，不做版本探测或双契约回退：0.1.65 上 `--url` 与
`--command` 都不存在，HTTP 本来即失败，回退只会引入「失败后按另一契约重试写入」的双写风险。`Remove` 仍以
退出码 0 为成功，因为 CLI 对不存在的名字返回 `{"deleted": false}` 且 exit 0，收紧会把幂等卸载变成
`unknown` 并留下 journal。

相关提交：`efa4168`（代码与测试；承载本条目的文档提交不列入自身）。

验证结果或限制：`gofmt -l .` 无输出，`go build ./...`、`go vet ./...`、`go test ./...` 全绿。板端全隔离验证
（`bianbu@10.0.91.51`，`bianbu-agent 0.1.69`，交叉编译 riscv64 APM sha256 `ee91bbed68c8d04f…`，临时
`APM_HOME`/缓存 + 临时 `BIANBU_AGENT_HOME` + `BIANBU_AGENT_CONFIG` 指向临时 `data_dir`）：安装得到
`bibit/mcp/nws@v1.0.0 installed` 且退出码 0，`<data_dir>/mcps/mcp.nws/mcp.json` 为 `streamable-http`
与 `https://nws.caseyjhand.com/mcp`，`mcp get nws` 返回 `id: mcp.nws`，`list --installed` 记录 `v1.0.0`；
同版本重装 `unchanged`，卸载 `completed`，卸载后 `mcp get` 回到 `null`、清单为空、`mcps` 目录为空、
`active.json` 不存在，重复卸载 `unexecuted` 且退出码 0。按指引第 4.2 节的真实 agent 环境复测（真实
`~/.bianbu-agent`）同样安装成功、真实 `mcps/mcp.nws/mcp.json` 内容一致、卸载后回到 `null`；真实环境只新增
CLI 自建的空目录 `~/.bianbu-agent/mcps`（安装前不存在），无其他残留。未覆盖：无云端 stdio 固定资源，
stdio 形态只有单测与板端 CLI 手测（`mcp add <name> --command npx --args -y pkg` 可登记）覆盖；`10.0.91.158`
仍为 `bianbu-agent 0.1.65`，该板的 Bibit MCP 需先升级运行时才会通过。

## 2026-09-15（后续：测试指引合入 CLI 自测用例并改名为 CLI 功能测试）

分类：功能变更

用户需求：把已写入知识库《Bianbu 5.0 自测用例》子表 `5 AgentStore` 的 CLI 自测用例合入 `docs/testing`，
重命名为一个完整的功能测试指引；报告不再要求逐项证据，只有测试失败时才记录用例项情况。

解决方案或完成结果：`docs/testing/install-uninstall.md` 改名为
[docs/testing/apm-cli-functional-test.md](testing/apm-cli-functional-test.md)，`install-uninstall-report-template.md`
改名为 [docs/testing/apm-cli-functional-test-report-template.md](testing/apm-cli-functional-test-report-template.md)。
指引新增第 5 节「用例清单」，把子表 `5 AgentStore` 的 14 条用例（CLI-01、SRH-01、LST-01/02、SHW-01、
INS-01~04、UNS-01/02、FIX-01、VAL-01、SUB-01）连同优先级、前置条件、测试步骤与预期结果一并纳入，覆盖范围
从「安装/卸载」扩展为 APM CLI 全部命令面；原第 5 节操作顺序、原第 6 节实际状态验证分别改为第 6、7 节，
并补入命令级用例的最小隔离环境、`--agent all` 批量的全隔离要求与 SUB-01 的提交授权前提。条目中的契约
措辞按代码核对修正：`search --json` 的 `description` 为 `omitempty` 输出且不含 `tags`；`list --json`
的 `description`/`tags` 恒定存在，`list --installed` 的未安装/空清单 agent 以 `{agent,state}` 状态记录出现；
`validate --json` 字段为 `type`/`name`/`version`/`description`/`file_count`/`content_size`/`warnings`；
`submission list/get` 用 `--output` 而非 `--json`，`--status` 只接受活动状态。

边界：报告模板删除「逐项证据」一节，改为只在失败或阻塞时填写「失败与阻塞记录」，成功用例仅保留矩阵中的
结果状态；判定标准（`PASS`/`FAIL`/`BLOCKED`/`N/A`/`NOT_SELECTED`/`NOT_RUN`）与「未执行不得记为通过」
不变。清理流程、固定云端 Power、目录隔离与环境分组沿用原指引，未改变任何 CLI 行为或隔离决策。运行产物
目录 `artifacts/install-uninstall/` 沿用既有名称。

相关提交：随承载本条目的文档提交落地（测试指引与报告模板更名、`AGENTS.md`/`BUILD.md`/ADR-036 的引用同步），按记录约定不列出自身哈希。

验证结果或限制：本次仅修改测试文档与引用，未执行真实安装/卸载。运行 `gofmt -l`、`go build ./...` 与
`go vet ./...` 确认文档改动未影响构建；引用同步更新于 `AGENTS.md`、`BUILD.md`、ADR-036 与本文。

## 2026-09-15（后续：共享 artifact 内按目标 Agent 落盘，plugin.yaml 去掉 author）

分类：缺陷修复

用户需求：Hermes 与 Bibit 的派生 `plugin.yaml` 统一去掉 `author`；同时发布到 Agent 的内容按目标过滤，
不属于目标 Agent 的配置文件不得出现在安装目录中。

解决方案或完成结果：派生 `plugin.yaml` 只保留 `schema_version: 1`、`name`、`version`、`description`
四个字段（`FilesystemPluginManifest` 移除 `Author`），字节输出固定为四行；生成与复验仍共用同一
struct 与 `reflect.DeepEqual`，`decodeExactRequiredYAML` 保持 `KnownFields(true)`，因此重新加入
`author` 或其他未知键（含 `skills`）仍 fail closed。按目标过滤无法在单一共享插件根上实现（Hermes
必需、Bibit 禁止 `__init__.py`；Bibit 还拒绝 `apc-plugin.json`、`.codex-plugin/`、
`.claude-plugin/`，也会以 `unknown plugin.yaml key: author` 拒绝 `author`），因此共享 artifact 内为
每个 Agent 分配独立落盘叶子，目录名即 Agent ID：

```text
cache/plugins/<name>/<version>/
├── .agents/plugins/marketplace.json      # Codex；source ./landing/codex/plugins/<name>
├── .claude-plugin/marketplace.json       # Claude；source ./landing/claude-code/plugins/<name>
├── plugins/<name>/                       # canonical 包：apc-plugin.json + skills/...
└── landing/<agent-id>/plugins/<name>/
    ├── codex/         .codex-plugin/plugin.json + skills/...
    ├── claude-code/   .claude-plugin/plugin.json + skills/...
    ├── hermes-agent/  plugin.yaml + __init__.py + skills/...
    └── bibit/         plugin.yaml + skills/...
```

固定顺序 `{codex, claude-code, hermes-agent, bibit}` 由 `pluginartifact.PluginLandingAgents()` 与
`LandingRelativeDir`/`LandingPluginPath`/`LandingMarketplaceSource` 单点定义。Codex/Claude 的过滤由
marketplace `source` 指向各自落盘叶子实现（真实 CLI 只复制 source 子树）；Hermes/Bibit 由
`Manager.StageFilesystem(agent, ref)` 只复制 `landing/<agent-id>/plugins/<name>` 实现，安装目录不再
出现其他 Agent 的配置。生成侧 `internal/pluginresource/agent_manifests.go` 的
`writeAgentLandings` 写四个叶子（`__init__.py` 只写 Hermes 叶子），复验侧
`ValidateShared` 复验 canonical 包、四个叶子与两份 marketplace 的精确相对路径闭集，并由
`validateLandingSkillPayloads` 要求每个叶子的 `skills/` 子树与 canonical 逐字节相等（禁止
symlink/hardlink 复用）。下载与派生次数不变：`Ensure`/`Lookup` 仍不携带 Agent，先装 Hermes 再装
Bibit（或任意顺序）时第二次安装命中同一不可变 artifact，既不重新下载也不重新派生，不会触发
「derived Plugin file already exists」冲突。

边界：本变更前的 cache 没有 `landing/` 目录，`ValidateShared` 返回带可操作提示的 `ErrInvalid`（提示
删除该 artifact 根以重新派生），`Lookup`/`Ensure` 不自动改写或清理不可变缓存。CLI flag、journal
schema 与卸载语义均不变。文档同步：新增
[ADR-036](ivory-tower/adr/ADR-036-per-agent-plugin-landing-filtering.md) 并将
[ADR-035](ivory-tower/adr/ADR-035-share-derived-filesystem-plugin-manifest.md) 标记为被取代，
同时更新 `BUILD.md`、`ivory-tower/project-overview.md`、Feature 018/020 的 spec 与 plan、
`docs/power-template/plugin.md`、`docs/power-template/README.md`、`docs/testing/install-uninstall.md`
与报告模板。

相关提交：`9dced12`（代码与测试；承载本条目的文档提交不列入自身）。

验证结果或限制：`gofmt -l` 干净、`go build ./...`、`go vet ./...`、`go test -count=1 -timeout 900s ./...`
全部通过。新增与更新的用例：`internal/pluginartifact` 的 `writeValidSharedArtifact` 改为生成完整
landing 布局，新增 `author` 注入、落盘叶子多余文件、Skill 漂移、marketplace source 回退到
`./plugins/<name>`、旧布局缺 `landing/` 的 fail-closed 用例；`internal/pluginresource` 的
`TestAgentLandingGenerationIsDeterministicAndUsesExactSchemas` 断言 `plugin.yaml` 逐字节为四行、
`TestAgentLandingGenerationKeepsEachAgentOwnFilesOnly` 断言每个叶子只含本 Agent 文件、
`TestStageFilesystemMaterializesOnlyTargetAgentLeaf` 断言 staging 只含目标叶子；
`internal/lifecycle` 的 `TestPluginAcceptanceFilesystemAgentsReuseSharedArtifact` 以两种顺序断言第二次
安装下载次数仍为 1 且 artifact 不变，并按 Agent 断言安装目录内容。限制：板端
（`bianbu@10.0.91.51`，Bibit 0.1.61）真实安装/卸载复测需按 `docs/testing/apm-cli-functional-test.md`
在板端执行，本轮尚未完成；Hermes↔Bibit 交叉复用无板端 `hermes` CLI，由上述本地集成用例覆盖。

## 2026-09-15（后续：派生 plugin.yaml 由 Hermes 与 Bibit 共用并新增 schema_version）

> 已被取代：该条目的 `author: APM` 字段与「单一共享插件根同时服务 Hermes 与 Bibit」布局已由上一条
> ADR-036 变更取代，`schema_version: 1` 决策继续有效。

分类：新增功能

用户需求：Bibit 安装 Plugin 时同样需要 APM 从 `apc-plugin.json` 派生的 manifest，与 Hermes 使用相同的
文件名与格式，但要在原格式上增加 `schema_version` 字段与取值；同时必须保持 Hermes 与 Bibit 在同一
制品上的复用逻辑——某个 Agent 安装时已生成的 YAML，另一个 Agent 安装时应直接复用而不是重新生成。

解决方案或完成结果：`plugin.yaml` 的路径与文件名不变（插件目录根
`plugins/<logical-name>/plugin.yaml`，ADR-031），但语义从「Hermes 原生 manifest」改为 Hermes 与 Bibit
共用的 filesystem Agent manifest，并新增 `pluginManifestSchemaVersion = 1`：

```yaml
schema_version: 1
name: <name>
version: <去 v 的原生版本>
description: <canonical description>
author: APM
```

生成侧 `internal/pluginresource/agent_manifests.go` 写
`pluginartifact.FilesystemPluginManifest{SchemaVersion: 1, ...}`（原 `HermesPluginManifest`），复验侧
`internal/pluginartifact/validation.go` 的 `expectedFilesystemPluginManifest` 返回同值并以
`reflect.DeepEqual` 比较，`decodeExactRequiredYAML` 继续使用 `KnownFields(true)`，因此缺失
`schema_version`、取值漂移（如 `2`）或类型漂移（如 `"1"`）都 fail closed；`schema_version` 是闭集内
唯一新增字段。命名同步去 Hermes 专属含义：`HermesPluginManifest` → `FilesystemPluginManifest`、
`hermesManifestPath`/`hermesInitPath`/`hermesInitContents` →
`pluginManifestPath`/`pluginInitPath`/`pluginInitContents`、`expectedHermesManifest` →
`expectedFilesystemPluginManifest`、`validateHermes` → `validateFilesystemPluginManifest`，Claude
marketplace 的 owner 改用独立常量 `apmAuthor`。

复用逻辑未新增任何按 Agent 分支：派生仍在 cache miss 的单次 pass 内完成且不依赖本次目标 Agent，
`pluginresource.Ensure` 先 `Lookup`（命中即返回），未命中才在 `pathLock(artifact.Root)` 下二次
`Lookup`，仍未命中才在新建的唯一临时根中构建并原子 `rename` 发布；因此先装 Hermes 再装 Bibit（或反
序）时第二次安装命中同一不可变 artifact，既不重新下载也不重新派生，`writePrivateFile` 的
「derived Plugin file already exists」防御分支在复用路径上不可达。Codex/Claude 的
`.codex-plugin/plugin.json`、`.claude-plugin/plugin.json` 与两份 marketplace 完全不变——它们是各自
CLI 的严格 schema，新增未知字段会被其解析器拒绝。

边界：本变更前的 cache 其 `plugin.yaml` 缺少 `schema_version`，`Lookup` 会按 `ErrInvalid` 拒绝，
`Ensure` 直接失败且不修改不可变缓存，需要人工清理 `cache/plugins/<name>/<version>`；不提供双 schema
兼容分支，与 ADR-017「缺少权威字段的旧 artifact 视为损坏」一致。文档同步：新增
[ADR-035](ivory-tower/adr/ADR-035-share-derived-filesystem-plugin-manifest.md)，并修正 ADR-014、
ADR-017、ADR-031、`BUILD.md`、`ivory-tower/project-overview.md`、Feature 018/020 的 spec 与 plan、
`docs/power-template/plugin.md`、`docs/power-template/README.md` 中过时的 `.hermes-plugin/` 布局与
「Bibit 复用 Claude 版本身份」表述。

相关提交：`9dced12`（该条目的 `schema_version`、命名与共用 manifest 改动随同一提交落地；其单根共用
布局已被上一条 ADR-036 取代）。

验证结果或限制：`gofmt -l internal/ cmd/` 干净（另修复 2 个既有未格式化测试文件）、`go build ./...`、
`go vet ./...` 通过，`go test -count=1 ./...` 通过。新增与更新的用例：
`internal/pluginartifact` 的 `writeValidSharedArtifact` 期望值改为含 `schema_version: 1`，并新增缺失/
漂移/类型漂移三个 fail-closed 用例；`internal/pluginresource` 的
`TestWriteAgentManifestsIsDeterministic`（生成两次字节一致 + `schema_version` 必须是首字段）；
`internal/lifecycle` 的 `TestPluginAcceptanceFilesystemAgentsReuseSharedArtifact` 以 Hermes→Bibit 与
Bibit→Hermes 两种顺序断言第二次安装后 Cloud 下载次数仍为 1 且 artifact 叶子树的文件集、字节、权限与
mtime 快照完全不变（证明未重新派生），并复用 `assertTreesEqual` 证明两个 Agent 目标与共享 artifact
一致。限制：本机无 bibit CLI，Bibit 侧「`plugin.yaml` 携带 `schema_version` 且容忍同目录
`__init__.py`」依据 `bianbu-agent-cli-guide.md` 与板端验收记录
`artifacts/install-uninstall/2026-09-10-k3/report.md` 的 bibit `plugin/superpowers@v6.3.0` PASS 用例，
真实安装/卸载复测需按 `docs/testing/apm-cli-functional-test.md` 在板端执行。

## 2026-09-11（后续：validate 对 agent.id 与 agent.name 不一致输出 warning）

分类：新增功能

用户需求：把 `agent.id` 与 `agent.name` 取值不一致这一情况在 `apm validate --type agent` 中报为
warning，使 ADR-033 记录的静默失效变成离线可发现的问题。

解决方案或完成结果：`agent.id` 必须等于 `agent.name`（ADR-033），但此前 APM 侧没有任何信号——
`internal/domain/agent_config.go` 的 `Validate` 只要求 `agent.id` trim 后非空，`ValidatePowerRef`
只比较 `name`/`version`，所以 `id != name` 的资源 `apm validate --type agent --json` 返回 exit 0 且
`warnings: []`，作者在本地无法自查，失效只在 Runtime 侧静默出现。现在由新增的
`internal/submission.agentIdentityWarnings` 在 Agent loader 中产出该 warning，文案为
`agent.id %q must equal agent.name %q: Bibit locates the installed agents/<name> directory by agent.id, so a mismatch makes the Agent unreachable by ID at runtime`；
CLI 已有通道直接复用（`submission.Validation.Warnings` → `cli.ValidateResult.Warnings` → 纯文本的
`warning: <text>` 行、`--json` 的 `warnings` 数组），未新增接口。

边界：**只影响 `validate`**——校验成功且 exit code 仍为 0，资源仍可提交与安装；`install`/`uninstall`/
cache/recovery 不检查该条件；`submit` 输出与退出码不变（`submission.Prepare` 只消费身份与统计字段，
不读取 `Warnings`），故 submit 输出契约未变。比较为精确字符串比较、不做 trim 与大小写归一，因为
Bibit 按字面值定位目录，`id: " demo-agent"` 与 `name: demo-agent` 实际会查找失败。无警告时返回空
切片而非 nil，Agent 的 `--json` 输出保持 `"warnings": []` 而不退化为 `null`。决策与非目标见
[ADR-034](ivory-tower/adr/ADR-034-report-agent-id-name-mismatch-as-validate-warning.md)；该 ADR 同时
取代 [ADR-033](ivory-tower/adr/ADR-033-require-agent-id-to-equal-agent-name.md) 的「不新增告警」表述，
ADR-033 的「后续动作」据此改为已完成。`docs/power-template/`、`ivory-tower/project-overview.md`、
`BUILD.md` 与 `../using-apm` 中原文「APM 不比较二者、validate 不会报错」的表述同步改为「不把它当作
校验错误，只输出一条 warning」。

相关提交：

- `488566a` feat(validate): warn when agent.id differs from agent.name

验证结果或限制：`go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部通过，
`go test -race -count=1 ./internal/submission/ ./internal/lifecycle/ ./internal/cli/ ./internal/domain/`
通过，`gofmt -l internal/ cmd/` 仅剩 2 个既有未格式化文件（本次未触碰）。新增用例：
`internal/submission` 的 `TestValidateAgentWarnsWhenIDDiffersFromName`（先失败后通过，确认断言的是
新行为）、`TestValidateAgentDoesNotWarnWhenIDEqualsName`、`TestPrepareAgentAcceptsIDNameMismatch`
（守护 submit 不受影响），`internal/lifecycle` 的 `TestValidateAgentOfflineReportsIDNameMismatch`
（守护 warning 传达到 CLI 契约，已用「删掉透传即失败」的变异验证确认非空转）与
`TestValidateAgentOfflineMapsResult` 的「同值不告警」断言。CLI 端到端实测：`id != name` 时 `--json`
的 `warnings` 含 1 条、纯文本输出 1 行 `warning:`、两次 exit 均为 0；`../test-agent`、
`../using-apm/templates/agent` 与内建 fixture `bianbu-agent-default` 三个 id 同值资源均输出
`"warnings": []`。限制：warning 只出现在 validate，只跑 `submit` 的作者仍可能把该问题带上去；
skill/plugin/mcp 三类 loader 从不初始化 `Warnings`，其 `--json` 输出本来就是 `null`，本次未统一；
Runtime 按 `agent.id` 定位目录的行为仍无法在本地复现（同 ADR-033 的限制），故未做 Runtime 侧验证。

## 2026-09-11（后续：agent.id 必须等于 agent.name）

分类：缺陷修复

用户需求：修正 Agent 资源的身份取值约定。以 `outputs/basic-agent/agent.yaml` 为样板，把
`agent.id` 与 `agent.name` 取相同值这一约定落到 `../using-apm` 的模板与提交规则、`test-agent`
资源（提交源与 `outputs/` 副本），以及仓库自身的 Agent 模板文档与内建 fixture 上。

解决方案或完成结果：隔离实测显示 Runtime 在 `agents/` 下按 `agent.id` 定位已安装的 Agent 目录，
而 APM 按 `agent.name` 发布目录（`internal/adapter/bibit_agent.go` 的 `TargetPath` 为
`root/agents/<ref.Name>`）。`test-agent` 的 `id` 为 `test-agent-84a175a0-...`、`name` 为
`test-agent`，于是安装成功、`apm list --installed --type agent` 也能列出，但 Runtime 按 ID 找不到该
Agent；而且 APM 侧没有任何检查能拦住它——`internal/domain/agent_config.go` 的 `Validate` 只要求
`agent.id` trim 后非空，`ValidatePowerRef` 只比较 `name`/`version`，四个 Agent 资源根在修正前全部
`validate` 通过且 `warnings` 为空。因此这条只能作为资源创作规则，由模板、提交规则与 fixture 承载：
新增 [ADR-033](ivory-tower/adr/ADR-033-require-agent-id-to-equal-agent-name.md)，并在
[ADR-023](ivory-tower/adr/ADR-023-package-agent-with-assets.md) 与
[project-overview](ivory-tower/project-overview.md)、`BUILD.md` 中引用。

`../using-apm`：`templates/agent/agent.yaml` 的 `id`/`name` 改用同一个 `my-agent` 占位值，并按
`basic-agent` 补齐 `zh_name`/`model`/`tools`/`skills`/`mcps`/`hooks` 可选字段骨架（标注 APM 不校验其
业务语义）；`references/submission.md` 删去「Assign a distinct internal ID」并改为说明两者必须同值、
APM 不比较、校验不会报错、不得手改已安装的 `agents/` 目录；`templates/README.md` 同步该规则并说明可选
字段为示意；`SKILL.md` 的 Common mistakes 表新增该错误与纠正。

`test-agent`：`outputs/test-agent/agent.yaml` 与提交源 `../test-agent/agent.yaml` 的 `id` 由
`test-agent-84a175a0-1223-4e0a-9c70-6f98f2769926` 改为 `test-agent`（改 id 而非改 name：旧 id 含大写
与十六进制片段，不满足 `IsCanonicalPowerName`），`version`/`description`/`soul` 逐字不变。

仓库：`docs/power-template/agent.md` 的 `id` 示例由 `e5249d373e67` 改为与 name 相同的
`bibit-edge-assistant`，其与 `docs/power-template/README.md` 的约束条由「`agent.id` 仅供 Bibit 内部
区分，不与 PowerRef name 比较」改为「是 Bibit 内部身份标识但必须与 `agent.name` 同值」；内建 local
fixture `internal/cloud/resources/agents/bianbu-agent-default/v1.0.0/agent.yaml` 的 `id` 由
`bibit-internal-bianbu-agent-default` 改为 `bianbu-agent-default`，使 `BUILD.md` 记录的
`install bianbu-agent-default=v1.0.0 --type agent` 开发流程在 Runtime 侧可用；
`internal/domain/agent_config.go` 仅更新 `AgentInfo` 注释，代码行为零变化。`ivory-tower/features/`
下的历史 spec/plan 保留原表述，不改写历史记录。

相关提交：

- `94d814e` docs(agent): require agent.id to equal agent.name

验证结果或限制：四个 Agent 资源根（`../using-apm/templates/agent`、`outputs/test-agent`、
`../test-agent`、内建 fixture）逐个断言 `agent.id == agent.name` 通过；`apm validate --type agent
--json` 四者均 exit 0、`warnings` 为空，且外部身份不变（`name` 仍为 `my-agent`/`test-agent`/
`test-agent`/`bianbu-agent-default`）；`APM_CLOUD_PROVIDER=local APM_CLOUD_LOCAL_ROOT=…` 下
`apm show bianbu-agent-default --type agent --json` 与 `apm list --type agent` 正常解析修正后的
fixture；`go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部通过，`gofmt -l internal/ cmd/`
仅剩 2 个既有未格式化文件（`internal/pluginartifact/validation_test.go`、
`internal/transaction/plugin_marketplace_recovery_test.go`，本次未触碰）；规范性旧表述
（`不与 PowerRef name 比较`、`distinct internal ID`、`Assign a new internal agent.id`、
`非外部资源名称`）在仓库文档与 `../using-apm` 中零命中。限制：Runtime 按 `agent.id`定位目录这一行为
无法在本仓库、开发机或测试设备上复现（无可用 Runtime 环境），依据是用户的隔离实测与 Bibit 侧契约旁证
（`RuntimeAgent` 以 `id` 为主键、Runtime 控制台把 agents 标为「专家」、Runtime 代理以
`/api/v1/agents/<agent_id>` 寻址），故本次未执行 Runtime 侧端到端验证；APM 不强制该约束，作者写错时
`validate`/`submit` 仍会成功。已发布的 `test-agent@v1.0.0` 与 `outputs/test-agent-1-v1.0.0.tar.gz`
保持原样（后者是已发布内容的记录，不伪造），修正需要重新提交，是否升版本未定。后续可评估在
`apm validate --type agent` 中把 `id != name` 报为 warning。

## 2026-09-11

分类：缺陷修复

用户需求：放宽 APM 对 config/cache 目录权限的限制——只要目录仅对同组可写就应正常工作，使 APM 能在
部分把家目录子目录建成宽权限的发行版镜像上开箱可用。实测 Bianbu 设备 `/home/bianbu/.cache` 为
`drwxrwxr-x bianbu:bianbu`（`0775`）、会话 umask 为 `0002`，`apm list` 与安装因此在目录信任检查处
直接失败：`verify pinned remote directory "/home/bianbu/.cache": remote directory mode 0775 permits
untrusted entry mutation: cloud unsafe content`。`~/.cache/apm` 与 `.tmp` 本身由 APM 以 `0700` 创建，
但 APM 从不改写既存父目录，而 XDG 只规定程序创建目录时用 `0700`，所以不能把“发行版把 `~/.cache`
改成 `0700`”当作使用前提。

解决方案或完成结果：`internal/cloud/remote.verifyDirectoryTrust`（覆盖 `~/.cache/apm` 的下载目标、
MCP 临时父目录与 catalog 下载目标）与 `internal/catalog.verifyRepositoryDirectoryTrust`（覆盖
`~/.apm/catalog.json` 的父目录链）的 mode 判定由「拒绝任何 `0o022` 位」收窄为「只拒绝其他人可写
（`0o002`）」：`0775`/`0770`/`2775` 等组可写目录改为放行，并通过新增的 `groupWritable` 判定结果由
持有路径的调用方输出一次 warning（新增 `logging.WarnGroupWritableDirectory`，按目录路径在进程内
去重且两条链共用去重空间，字段 `path`/`mode`）；`0777`/`0702` 等其他人可写目录（root 拥有的
sticky 例外除外）仍以 `cloud.ErrUnsafeContent` fail closed。属主检查、逐级 `O_NOFOLLOW` 固定、
fd 相对名称型操作、device/inode 复验、`rootSticky` 的“直接子项须属主为当前 euid”规则、APM 自身
创建目录的 `0700`、catalog 文件的 `0600`、非 Unix 平台 fail-closed 分支与 Agent 根目录（本就不
检查）全部不变；判定只看 mode 低 9 位，故 setgid 的 `2775` 按 `0775` 处理。决策与残留风险见
[ADR-032](ivory-tower/adr/ADR-032-accept-group-writable-trusted-directories.md)。

相关提交：

- `ccd7665` fix(security): accept group-writable trusted local directories
- `b67d0fd` fix(logging): dedupe group-writable directory warning by path

验证结果或限制：`gofmt -l` 与 `go vet ./...` 干净，`go build ./...` 通过；`go test ./...` 全量通过，
`go test -race ./internal/cloud/remote/ ./internal/catalog/ ./internal/logging/` 通过。新增用例守护
判定矩阵：`internal/cloud/remote` 的 `TestVerifyDirectoryTrustModePolicy`、
`TestVerifyDirectoryTrustStickyPolicy`、`TestOpenPinnedDirectoryAcceptsGroupWritableAncestor`，以及
`internal/catalog` 的 `TestVerifyRepositoryDirectoryTrustModePolicy`、
`TestRepositoryLoadAcceptsGroupWritableParent`、`TestRepositoryLoadRejectsOtherWritableParent`；
`internal/logging` 的 `TestWarnGroupWritableDirectoryEmitsOncePerPath` 守护按路径去重。既有依赖组
可写即拒绝的 `TestGetMCPRejectsWritableInjectedTemporaryParentWithoutRequest` 与
`TestDownloadAgentRejectsWritableDirectoryTrustViolationsWithoutRequest` 改为显式 `chmod 0777`，
使其继续覆盖 other-writable 的 fail-closed 路径。

设备端验证（Bianbu 4.0.6 / riscv64，主机 `k3`，`bianbu@10.0.91.51`；修复前后二进制同机对比，
设备上未改动任何既有目录权限）：`apm list` 判定矩阵 9 个用例全部符合预期——修复前 + `cache` `0775`
失败并复现原错误；修复后 `cache` `0775`、父目录 `0775`、`APM_HOME` `0775` 均成功且各自只输出一行
告警，`0777` 一律失败（remote 报 `remote directory mode 0777 permits untrusted entry mutation`，
catalog 报 `catalog directory mode 0777 permits untrusted entry mutation`），`0700` 成功且无告警；
设备真实 `~/.config`（`0775`）作为祖先时，修复前失败、修复后成功并对 `~/.config` 与缓存目录各输出
一行告警。修复后在 `0775` 缓存目录下完成 Skill `foo-before-summary=v0.0.1`（Codex，隔离
`APM_HOME`/`XDG_CACHE_HOME`/`CODEX_HOME`）的完整闭环：安装 `installed`、落地内容与缓存制品
`diff -r` 一致、同版本重装 `unchanged`、卸载 `completed`、目标目录与清单条目消失、重复卸载
`unexecuted`，全程无活动事务残留，真实 `~/.codex` 未产生变化。限制：残留风险为同组账号仍可改名
或删除受检目录链上的目录项（造成产物落入不可达目录或 DoS），APM 不承诺隔离同组 Byzantine 写者；
告警只按目录路径去重，不解析组成员，用户只能 `chmod g-w` 消除。本次设备验证只覆盖 `apm list` 与
Codex Skill 一类，未按 [APM CLI 功能测试指引](testing/apm-cli-functional-test.md) 跑全 agent/全类型矩阵。

## 2026-09-10

分类：缺陷修复

用户需求：修复 APM 安装 claude MCP 失败——`claude mcp get <name>` 的 not-found 消息给 name 加引号
（`No MCP server found with name: "demo".`），user scope 行是 `Scope: User config (available in all
your projects)` 而非裸 `Scope: User`；适配器却匹配不带引号的 name 并要求 scope 精确等于 `Scope: User`，
导致 absent 与 present 都落入 inspectUnknown，安装在校验阶段就失败、到不了 `mcp add`。

解决方案或完成结果：`internal/adapter/claude_mcp.go` 的 Inspect 改为匹配带引号的 not-found 消息；
scope 解析由精确等于 `Scope: User` 改为取 `Scope:` 后的首个空白分隔 token 且仅接受 `User`（兼容旧
`Scope: User`，接受 `Scope: User config (…)`，拒绝 `Local`/`Project` 与空值）。Add/Remove argv 与
其它 Agent 的 MCP adapter 不变；同步更新 adapter 单测 fixture 与 lifecycle 集成测试的 fake `claude`
脚本，使其镜像真实 CLI 输出。

相关提交：

- `4455ed0` fix(mcp): align claude MCP inspect with real CLI output

验证结果或限制：`go build ./...`、`go vet ./...`、`go test ./...` 全量通过（含 adapter 与 lifecycle
的既有与新增用例）；本机 claude v2.1.175 实测确认 `mcp get` 的 present（user/local/project 三种 scope
描述）、absent（带引号、stderr、exit 1）输出格式，以及 `mcp add`（stdio/http）与 `mcp remove`
的退出码 0。

## 2026-09-10

分类：缺陷修复

用户需求：修复 APM 安装 hermes 插件失败——真实 hermes CLI（v0.16.0）从 `~/.hermes/plugins/<name>/`
直接读取 `plugin.yaml` 与含 `register(ctx)` 的 `__init__.py`，而 APM 把两者派生到 `.hermes-plugin/`
子目录，导致 `hermes plugins list --json` 不发现插件、`hermes plugins enable` 却成功（其存在性检查
只看目录存在），插件从不加载，且后续 Inspect 把它判为 absent，破坏 install → list → reinstall →
uninstall 的一致性。

解决方案或完成结果：`pluginresource.writeAgentManifests` 与 `pluginartifact` 的派生 manifest 路径由
`.hermes-plugin/plugin.yaml`、`.hermes-plugin/__init__.py` 改为插件目录根的 `plugin.yaml`、`__init__.py`；
派生 closed-world 不再允许 `.hermes-plugin/` 目录，使旧布局成为非法路径。manifest 内容
（name/version/description/author）与 `__init__.py` 内容、hermes adapter（`plugins list --json` /
`enable` / `disable`）与 filesystem transaction 均不变。新增 ADR-031 记录该布局修正，并同步 BUILD、
Project Overview 的布局图与 ADR 引用。

相关提交：

- `cfeda59` fix(plugin): publish hermes manifest at plugin root

验证结果或限制：`go build ./...`、`go vet ./...`、`go test ./...` 全量通过（含 pluginresource、
pluginartifact、transaction、lifecycle、cloud/local 的既有与新增用例）；本机 hermes v0.16.0 实测：
根目录布局下 `hermes plugins list --json` 正确列出插件（name/version/source=user）、enable/disable
正常，旧 `.hermes-plugin/` 布局下列表为空。

## 2026-09-09

分类：缺陷修复

用户需求：APC 提交 MCP 资源时 `definition` 需随 metadata 上传，否则服务端按 metadata 重建资源文件时
把 definition 置 null，发布的 MCP 资源无法安装（实测 nws 发布文件字节与其申报 content_md5 不一致）。

解决方案或完成结果：`domain.SubmissionRequest` 新增 `Definition *MCPServer`；`internal/submission` 的
MCP loader 从严格解析后的资源中提取 definition 并经 `MCPServer.Clone()` 防御性复制后透传至 `Content`；
`lifecycle.Submit` 传入请求；`internal/cloud/remote` 的 `submissionMetadataDTO` 新增
`definition,omitempty`，MCP 时输出与资源文件内一致的嵌套定义、其余类型省略，并对远程 MCP 缺失 definition
的调用 fail loud。`content` part 与 `content_md5` 规则不变。APC-API.md 同步 metadata 契约：`definition`
仅 MCP 携带、其余类型不携带。

验证结果或限制：`gofmt`、`go vet` 与构建通过；`internal/domain`、`internal/submission` 全量测试、
`internal/lifecycle -run Submit`、`internal/cloud/remote -run Submit` 通过，新增 remote/submission/
lifecycle/domain/contract 各层用例覆盖 MCP 携带 definition、非 MCP 不携带、MCP 缺失 definition 拒绝。
`internal/cloud` 契约测试的 submit_* 子用例（含 submit_mcp 的 definition 断言）通过；其 catalog 下载段
与 `internal/cloud/remote` 的 agent 下载用例在本机沙箱因根目录属主映射（uid 65534）失败/挂起，与本次
改动无关。

## 2026-09-09

分类：功能变更

用户需求：当 `--agent` 指定非法值时，报错需打印可选的 agent 选项，与 `--type` 的报错风格对齐。

解决方案或完成结果：新增 `domain.KnownAgentIDs`/`domain.IsKnownAgentID` 枚举 `--agent` 合法取值
（`all`、`bibit`、`claude-code`、`codex`、`hermes-agent`）；CLI 新增 `validateAgentValue`，在
`validateCommon`/`validateAgentTypeFlags`/`validateListFlags` 中对未知 agent 报
`invalid --agent %q: must be all, bibit, claude-code, codex, or hermes-agent`。lifecycle 的
`installAgents`/`uninstallAgents`/`installedListAgents` 对“已知但未安装”的 agent 报错追加
`(available agents: …)`（`--type agent` 仅支持 bibit，不追加；快照为空不追加）。

相关提交：

- `e4fa923` feat(cli): list selectable agents when --agent is invalid

验证结果或限制：板端（Bianbu riscv64、Go 1.26.0）`go test ./...`、`go vet ./...`、gofmt
检查与构建均通过。

## 2026-09-09

分类：功能变更

用户需求：把 `apm show` 的版本指定方式从 `--version` flag 改为位置参数
`<name>[=<version>]`，与 `install <power>[=<version>]` 风格一致；`list` 保持不变，
`--version` 彻底移除。

解决方案或完成结果：`internal/cli/show.go` 复用 `parsePower` 拆分位置参数，name 去首尾空白后走
`domain.IsCanonicalPowerName` 校验，version 非空时走 `domain.CanonicalPowerVersion` 校验；删除
`--version` flag 注册。CLI 测试改用 `show demo=v1.2.3`，并新增空 version、空 name、多余 `=`、
`--version` 未知 flag 等前置拒绝用例；`cmd/apm` 集成测试改用 `show foo-before-summary=v0.0.1`。
BUILD、ADR-002、ADR-028、Project Overview 与 Feature 029 spec 同步新契约。

相关提交：

- `131f8c0` feat(show): specify exact version with <name>=<version>

验证结果或限制：板端（Bianbu riscv64、Go 1.26.0）`go test ./...`、`go vet ./...`、gofmt
检查与构建均通过。

## 2026-09-09

分类：新增功能

用户需求：根据项目准备 `debian/` 目录，使 APM 能在
`bianbu@10.0.91.51:/home/bianbu/Workspace/deb/apm/agent-power-manager` 构建、安装和
运行；卸载时保留用户数据。允许联网下载 Go modules。

相关提交：

- `5a0fbff` docs: design Debian packaging and user data retention
- `c69b44b` docs: plan Debian packaging and board validation
- `269a721` build: package APM CLI for Bianbu with Go modules

解决方案或完成结果：完善 debhelper compat 13 打包规则，以 Go 1.26 本机工具链联网下载并
校验 `go.mod/go.sum`，运行全量测试并生成 `/usr/bin/apm`。包只含 CLI 和标准文档，依赖
`ca-certificates`，无 maintainer script、服务或系统用户；remove 与 purge 均不触及
`~/.apm`、APM cache 或 Agent 根目录。Go 1.26 的压缩 DWARF 与板端 `dwz` 不兼容，规则
明确跳过 `dh_dwz`，保留其它 debhelper 阶段。native 源归档通过 `tar-ignore` 排除
`debian/.build`。完整决策见 ADR-030，验证见 Feature 030。

验证结果或限制：在 Bianbu 4.0.6 riscv64、Go 1.26.0、debhelper 13.24.2ubuntu1-bb1 上，
`dpkg-buildpackage -us -uc -b`（含 `go mod download`、`go mod verify`、`go test ./...`）
退出 0，锁文件未变；`go test ./...`、`go vet ./...`、gofmt 检查和本地 CLI 构建均通过。
生成包 SHA-256 为 `af17a1f623db66ce1270c8e6e5aa6d13982685a6c8ba7a9cf46895486eb89a7e`，仅含
`/usr/bin/apm` 与文档。板端实际安装、普通用户运行、remove、purge、最终重装均完成；
隔离状态、缓存、四个 Agent 根及符号链接在安装前、remove 后和 purge 后的内容、权限、
所有者与链接目标快照完全一致。最终 `agent-power-manager 0.0.1` 为已安装状态。

## 2026-09-07

分类：缺陷修复

用户需求：`submission get`/`submission list` 此前只接受严格 RFC3339 时间戳，而测试环境实际返回无时区 ISO 8601（如 `2026-09-07T10:17:58`），导致 get/list 校验 `created_at` 时失败并报 `timestamp "..." is not RFC3339: cloud invalid response`。将其解析放宽为与 catalog `published_at` 一致的「RFC3339 或无时区 ISO 8601」。契约见 [ADR-026](ivory-tower/adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)。

解决方案或完成结果：`internal/domain` 新增导出函数 `ParseAPCTimestamp`，作为 catalog 与 submission 唯一的时间戳格式定义：先尝试 `time.RFC3339`/`time.RFC3339Nano`（含小数秒与 UTC/偏移量），再匹配无时区 ISO 8601 正则并用 `time.Parse("2006-01-02T15:04:05", ...)` 解析（无时区按 UTC 解释，保证排序比较确定）；`internal/domain/catalog.go` 的 `isCatalogPublishedAt` 改为复用该函数，并删除原 `catalogTimestampWithoutZone` 正则（顺带拒绝旧正则放过的不可能日期）；`internal/cloud/remote` 的 `parseRequiredSubmissionTimestamp` 改为调用 `domain.ParseAPCTimestamp`，保留 `cloud.ErrInvalidResponse` 哨兵语义，覆盖详情 `created_at`/`updated_at`、list `created_at` 与 timeline `timestamp` 四个调用点。

验证结果或限制：`go test ./...`、`go test -race ./...`、`go vet ./...`、`gofmt -l` 与 `git diff --check` 均以退出码 0 通过；新增 `internal/domain` 的 `TestParseAPCTimestamp` 覆盖 RFC3339 Z/偏移/小数秒、无时区（含小数秒）与各类非法值，并断言无时区解析结果为 UTC；`internal/cloud/remote` 新增 list 无时区倒序接受、list 无时区升序拒绝、get 无时区时间戳（含 timeline）接受用例。

## 2026-09-07（后续：list --json 携带 description/tags）

分类：功能变更

用户需求：`apm list` 文本已按 ADR-028 精简为身份行、详情统一走 `show`，但前端通过调用 apm 批量获取本地资源（`list --installed`）与云端资源（`list`）时需要每条记录都带 description 和 tags；`show` 是 Cloud-only、单 name、latest/单版本，覆盖不了本地清单与批量场景。

解决方案或完成结果：`list --json` 改为机器接口、恒带 `description`（空为 `""`）与 `tags`（空为 `[]`、不为 `null`），与 `show --json` 的单条形状一致；Cloud 记录为 `type/name/version/description/tags`，installed 记录多 `agent`，`--agent=all` 分组的 `{agent,state}` 状态记录不变、listed 记录同样带元数据。实现上在 `internal/cli/list_render.go` 新增 list 专用 `listPowerJSON` 记录类型与 `toListPowerJSON`（nil tags 归一化为 `[]`），list 的 JSON 走 `renderListPowersJSON`、分组 JSON 改用该转换，与 search 共用的 `powerJSON`/`toPowerJSON` 保持不变，避免破坏 search「永远不输出 tags」的契约；list 文本仍身份精简、`search --json` 与 `show` 不变。契约见 [ADR-029](ivory-tower/adr/ADR-029-list-json-carries-description-and-tags.md)。

验证结果或限制：`go test ./...`、`go test -race ./...`、`go vet ./...`、`gofmt -l` 与 `git diff --check` 均以退出码 0 通过；`internal/cli` 更新/新增用例覆盖 Cloud 单条、installed 单 Agent 与 `--agent=all` 分组的 JSON 元数据输出、空 description/tags 归一化为 `""`/`[]`，并保留 list 文本身份精简与 search JSON 无 tags 的既有断言。

## 2026-09-04（后续：list --all 展开指定名称版本并新增 show）

分类：功能变更

用户需求：将 `apm list -a/--all` 改为仅对指定 name 展开全部 Cloud 版本，并新增 `apm show <name>` 详情查询，支持精确版本。

解决方案或完成结果：`list <name> --all` 按 SemVer 降序保留该名称全部版本，缺少 name 时在 lifecycle 前拒绝；`show` 默认返回最新版本，`--version` 返回精确版本，文本与 JSON 输出描述、标签等详情。新增 ADR-028，并同步 ADR-002、Project Overview、BUILD 与 bundled-cloud 集成覆盖。

相关提交：

- `47d9a38` feat: expand list all versions for named power
- `7820121` feat(list): scope all flag to named versions
- `dc1a2b2` feat(show): add catalog detail lifecycle
- `3e55fae` feat(cli): add show command and lifecycle wiring

验证结果或限制：新增 `cmd/apm` 集成测试覆盖 named list 全版本、unnamed `--all` 拒绝、show 最新/精确版本及 JSON；相关测试与全量验证随功能提交执行。

## 2026-09-04（后续：search 去掉 -a/--all，默认显示 description 不显示 tags）

分类：功能变更

用户需求：`apm search` 删除 `-a/--all` flag，默认输出即展示 description，且任何情况下都不展示 tags；`apm list` 的 `-a/--all`（追加 description + tags）保持不变。

解决方案或完成结果：`internal/cli` 的 `search` 命令移除 `-a/--all` 注册，`renderSearch` 改为固定元数据策略（description 默认、tags 永不）；`power_render.go` 把 `renderPowers`/`renderPowerText`/`renderPowerLine`/`toPowerJSON` 的 `all bool` 重构为 `powerMetadata{description, tags}`，使 description 与 tags 可独立开关，list 继续经 `allMetadata(flags.All)` 保持原语义。`--json` 与文本一致：默认含 `description`、不含 `tags`。`search --all`/`-a` 现在由 Cobra 报 unknown flag。

验证结果或限制：`go test ./...`、`go vet ./...`、`gofmt -l cmd internal` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 通过；更新用例覆盖 search 文本/JSON 默认含 description 不含 tags，以及 `--all`/`-a` 被拒绝。

## 2026-09-04（后续：list --installed 的 --agent 可选并分组输出）

分类：功能变更

用户需求：`apm list --installed` 不再强制要求 `--agent`；省略 `--agent` 或 `--agent=all` 时按 Agent ID 字典序分组缩进输出全部受支持 Agent 的已安装清单，并显式区分「Agent 未安装」与「空清单」；指定具体 `--agent` 时文本行不再重复 `<agent>/` 前缀。契约见 [ADR-002](ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md)。

解决方案或完成结果：`adapter.Registry` 新增 `SupportedAgents()`（返回按 Agent ID 字典序排序的全部受支持 Agent），`AdapterRegistry` 接口同步扩展；`internal/cli` 的 `ListResult` 新增 `Agents []InstalledAgentSummary` 承载 installed all 模式的逐 Agent 状态（not-installed/empty/listed），`list` 校验允许省略 `--agent`（显式空白仍报 `--agent must not be empty`）；`internal/lifecycle` 的 installed all 路径遍历受支持 Agent、区分三态并保留 `PartialListError` 语义；渲染层去掉 Power 行的 Agent 前缀，all 模式按 Agent 分组缩进输出，JSON 顶层数组以 `{"agent":...,"state":...}` 表达未安装/空清单。ADR-002、Project Overview 与 BUILD 同步更新。

验证结果或限制：`go test ./...`、`go vet ./...`、`gofmt -l cmd internal` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 通过；新增/更新用例覆盖 CLI 省略 `--agent`、分组文本与 JSON 渲染、all 模式三态、name/type 过滤组合与 partial 失败，以及 `cmd/apm` 省略 `--agent` 列出全部受支持 Agent 的集成用例。

## 2026-09-04（后续：list 支持 name 过滤）

分类：功能变更

用户需求：为 `apm list` 增加可选位置参数 `name`（不是 flag），对默认 Cloud 列表与 `list --installed` 都按名字精确过滤：`apm list beta` 仅显示名为 `beta` 的最新版记录。契约见 [ADR-002](ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md)。

解决方案或完成结果：`internal/cli` 的 `list` 命令 `Args` 由 `ExactArgs(0)` 改为 `MaximumNArgs(1)`、`Use` 改为 `list [name]`，RunE 解析并 trim 位置参数到 `flags.Name`（trim 后为空报 `name must not be empty`）；`internal/lifecycle` 的 `makeCatalogListResult` 与 `makeInstalledListResult` 均在 type 过滤旁增加 `flags.Name` 的区分大小写精确过滤。ADR-002 更新 `list [name]` 语法与 name 语义，Project Overview 与 BUILD 同步更新。

验证结果或限制：`go test ./...`、`go vet ./...`、`gofmt -l cmd internal` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 通过；新增/更新用例覆盖 CLI 传参、空 name 校验、Cloud 与 installed 的 name 精确过滤、以及 bundled-cloud 按 name 过滤只返回 `foo-before-summary@v0.1.0`。

## 2026-09-04

分类：功能变更

用户需求：默认 `apm list`（Cloud/catalog 模式）此前平铺输出每个 Power 的全部版本，同名多版本并列展示；改为对每个 type/name 只输出最高 SemVer 版本，`list --installed` 与 `search` 行为不变。契约见 [ADR-027](ivory-tower/adr/ADR-027-list-cloud-latest-only.md)。

解决方案或完成结果：`internal/lifecycle` 的 `makeCatalogListResult` 改为按 `(type, name)` 用 map 聚合目录条目，以 `semver.GreaterThan` 保留最高版本，再走既有 `sortListedPowers` 稳定排序；保留记录携带最新版本的 description/tags。`list --installed`（`makeInstalledListResult`）与 `search`（`filterSearchPowers`）不改。`cmd/apm` 的 bundled-cloud 集成用例改为断言 `foo-before-summary@v0.1.0` 存在且 `v0.0.1` 不再出现。ADR-027 记录该决策并取代 ADR-002 第 39 行“保留每个版本”条款，Project Overview 与 BUILD 同步更新。

验证结果或限制：`go test ./...`、`go vet ./...`、`gofmt -l cmd internal` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 通过；`internal/lifecycle` 新增/更新用例覆盖同名多版本 keep-latest、无序输入选择最高版本、完全重复折叠为一条、以及最新版本 description/tags 保留。

## 2026-09-03（后续修正：容忍 tar 结束块后的 NUL 填充）

分类：功能变更

用户需求：测试环境用 GNU tar blocking factor 20 生成 Skill 归档，解压后总长 10240 字节，tar 结束块之后还带 8192 字节 NUL 填充，导致 `apm install` 报 `extract remote skill archive: remote tar contains content after its terminator: cloud unsafe content`。将客户端适配为容忍结束块后的纯 NUL 填充，同时保持对非零尾随字节的拒绝。契约见 [ADR-026](ivory-tower/adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)，证据见 [live-probe](ivory-tower/features/028-apc-api-adaptation/live-probe.md)。

解决方案或完成结果：`internal/cloud/remote` 的 `walkArchive` 把「结束块后读到任意字节即报错」改为循环读完剩余解压字节并逐一校验必须为 `0x00`；任何非零尾随字节仍返回 `cloud.ErrUnsafeContent`，正常 `io.EOF` 结束。防拼接归档/多归档多态的安全约束不变。

验证结果或限制：新增用例 `TestExtractArchiveAcceptsNulPaddingAfterTarTerminator` 覆盖结束块后 8192 字节 NUL 填充的解包成功；既有 `stageTarGzWithTrailingContent`（512 字节 `X`）用例仍被拒绝，证明非零尾随 fail-closed 未扩大。注：测试环境 `code-helper` 归档为占位产物，只含 `./code-review-helper-v1.0.0.txt`、缺少 Skill 必需的 `SKILL.md`/`skill-card.md` marker，属服务端数据待补齐，客户端 marker 校验不做放宽。

## 2026-09-03（后续修正：接受 gzip 历史媒体类型别名）

分类：功能变更

用户需求：测试环境 CDN 对 Skill/Plugin/Agent 归档返回 `Content-Type: application/x-gzip`（历史别名），而目录声明 `application/gzip`，导致 `apm install` 报 `remote catalog entry content has media type "application/x-gzip": cloud invalid response`。将客户端适配为接受该别名，使 gzip 归档安装对真实服务可用。契约见 [ADR-026](ivory-tower/adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)，证据见 [live-probe](ivory-tower/features/028-apc-api-adaptation/live-probe.md)。

解决方案或完成结果：`internal/cloud/remote` 的 `verifyCatalogEntryContent` 在比较响应媒体类型前调用新增的 `normalizeContentType`，把精确的 `application/x-gzip` 归一化为 `application/gzip` 后再与 `entry.ContentType` 比较；其它媒体类型原样返回仍严格相等校验。归一化只作用于响应侧、只认精确的 `application/x-gzip`，目录元数据仍要求 IANA 声明 `application/gzip`（fail-closed 不扩大）；`Accept` 头、解包与 MD5/大小校验不变。

验证结果或限制：新增用例覆盖 `application/x-gzip` 响应被接受且字节精确（`TestVerifyCatalogEntryContentAcceptsLegacyXGzipAlias`）以及完整 `DownloadSkill` 路径在旧别名下发布成功（`TestDownloadSkillAcceptsLegacyXGzipContentType`）；既有「错误媒体类型」「带参数媒体类型」用例保持拒绝，证明 fail-closed 未扩大。

## 2026-09-03

分类：功能变更

用户需求：依据 APC-API.md 把 APC v1 客户端适配为 Submission 三个接口仅使用 `X-API-Key` 认证（不再发送 `Authorization`）、在 Submission multipart metadata 中补齐 `schema_version: 1`，并把 Remote 默认基址改为测试环境 `https://developer.bianbu.xyz`；同时用契约测试固定匿名目录/内容请求的无凭据约束、四类资源内容类型映射与错误映射。契约见 [Feature 028](ivory-tower/features/028-apc-api-adaptation/spec.md) 与 [ADR-025](ivory-tower/adr/ADR-025-apc-api-client-contract.md)。

相关提交：

- `ba5b83e test(apc): define X-API-Key wire contract`
- `224faa7 feat(apc): use X-API-Key and schema version metadata`
- `d1d4c9f test(apc): verify anonymous catalog and content contracts`
- `505c46b feat(apc): default Remote provider to test environment`
- `00723de test(apc): cover 404 content mapping (review fix)`
- `6e69771 docs(apc): record API v1 client contract decision`

解决方案或完成结果：`internal/cloud/remote` 的 `Submit` 与 `getSubmissionJSON` 把认证头从 `Authorization: Bearer` 改为 `X-API-Key`，不再设置 `Authorization`，且 `apiKey` 为空时经既有 preflight 返回 `cloud.ErrAuth`；Submission multipart metadata 增加 `schema_version: 1`。`cmd/apm` 的 `defaultAPCBaseURL` 改为 `https://developer.bianbu.xyz`，保留 `APC_BASE_URL` 覆盖与既有 base URL 校验。契约测试新增/更新：Submission 请求只有 `X-API-Key` 且无 `Authorization`、metadata 含 `schema_version=1`；匿名目录/内容请求在配置 key 的客户端上仍不携带任何凭据；重定向凭据注入拒绝列表补入 `X-API-Key`；四类资源内容类型映射（Skill/Plugin/Agent=`application/gzip`、MCP=`application/json`）严格校验；`endpoint()` 对 name/version 段做百分号转义；内容 404→`ErrNotFound`、410→`ErrYanked` 映射。ADR-025 记录“APC-API.md 为客户端权威契约、Submission 仅 X-API-Key、拒绝隐式 Bearer 回退”决策，并同步 BUILD、Project Overview 与 Feature 021/023/024 的历史标注。

验证结果或限制：`go test ./...`、`go test -race ./...`、`go vet ./...`、隔离目录 `go build` 与 `git diff --check` 均以退出码 0 通过。2026-09-03 对 `https://developer.bianbu.xyz` 只读探测（`curl -k`，产品 TLS 校验保持开启）得到可复现结论，详见 [live-probe](ivory-tower/features/028-apc-api-adaptation/live-probe.md)：(1) 真实 APC 基址为 `https://developer.bianbu.xyz/api-server/apc/api/v1`，APM 默认基址加 `/api/v1/...` 被网关以 `401 Full authentication is required` 拦截，且该非根路径无法经 `APC_BASE_URL` 指向；(2) 测试环境 Submission 仍要求 `Authorization`、忽略 `X-API-Key`（`401 Authorization header is required`），真实 Submission GET/POST 判定为服务端阻塞，未尝试真实 POST；(3) 目录把 MCP `content_type` 声明为 `application/gzip` 但 CDN 实际返回 `application/json`；(4) 目录与内容 `302` 重定向到跨源 CDN `cdn-resource.spacemit.com`，与 APM 同源重定向策略不符；(5) 服务端目录无顶层/分组 `schema_version` 且含 `download_url` 字段，会被严格 catalog 解码拒绝；(6) 容器缺少目标站点 CA 链，产品 TLS 校验正确拒绝。以上限制均按 spec 以 fail-closed 记录，客户端未做隐式降级或扩大接受范围。

## 2026-09-03（后续修正：按服务端实际行为适配）

分类：功能变更

用户需求：以测试环境实际行为为准（服务端行为优先于 `APC-API.md`），把 APC 客户端适配为对真实服务可用，并修复 2026-09-03 只读探测记录的六项偏差——#1 路径前缀、#2 Submission 认证、#3 MCP 内容类型、#4 跨源重定向、#5 目录 schema（#6 容器 CA 属环境问题，仅记录）。契约见 [ADR-026](ivory-tower/adr/ADR-026-adapt-apc-client-to-test-environment-behavior.md)（取代 [ADR-025](ivory-tower/adr/ADR-025-apc-api-client-contract.md)），证据见 [live-probe](ivory-tower/features/028-apc-api-adaptation/live-probe.md)。

解决方案或完成结果：`internal/cloud/remote` 的 `endpoint()` 在所有 `/api/v1/...` 相对路径前固定拼接网关前缀 `/api-server/apc`（base URL 校验仍要求根路径）；`Submit` 与 `getSubmissionJSON` 的认证头由 `X-API-Key` 改为 `Authorization: Bearer`，匿名目录/content 请求仍无凭据。`internal/catalog` 的目录解码适配服务端形状：顶层/分组 `schema_version` 改为可选（缺省视为 v1），item 容忍 `schema_version`/`download_url`（DTO 增加 `omitempty` 字段），MCP 误声明的 `application/gzip` 归一化为 `application/json`；`internal/domain` 的 MCP 条目 `message` 允许为空、`published_at` 接受 RFC3339 或无时区 ISO 8601。重定向策略允许匿名目录/content 请求跨源跳转到 HTTPS CDN（`cdn-resource.spacemit.com`），拒绝降级到 HTTP、携带 userinfo 或认证请求跨源；content 请求标记为匿名。目录下载容忍 HTTP/2 无 `Content-Length`（以 `MaxCatalogSize` 为上界，最终大小/哈希由目录版本响应兜底）。MCP 资源 JSON 容忍服务端额外返回的 `type`/`message`/`content_md5`（可能为 null）。

验证结果或限制：`go test ./...`、`go vet ./...` 均以退出码 0 通过；新增用例覆盖服务端目录 schema 解码、MCP gzip→json 归一化、无 `Content-Length` 目录下载、跨源 HTTPS 重定向跟随/HTTP 降级拒绝、MCP 资源额外字段容忍与 Submission Bearer 头。以服务端证书作为信任锚（`SSL_CERT_FILE`，仅验证环境，产品 TLS 校验保持开启）做端到端验证：`apm list` 返回 `mcp/filesystem@v1.0.0`、`skill/code-review-helper@v1.0.0`、`skill/code-test-helper@v1.0.0`；`apm list -a` 与 `apm search` 正常；`remote.GetMCP(filesystem)` 成功下载并校验（269 字节、MD5 一致）；`apm submission list`（占位 key）返回 `401 INVALID_API_KEY`，证明 `Authorization: Bearer` 被服务端识别。容器缺少目标站点 CA 链仍属环境问题，未在客户端关闭 TLS 校验。

## 2026-08-27

分类：功能变更

用户需求：把安装时捕获的 `description` 与 `tags` 作为已安装 Power 的一等元数据，持久化到清单与 journal，使离线 recovery 与 `apm list --installed` 不再丢失；同时让 `apm list` / `apm search` 通过 `-a/--all` 追加 description/tags、通过 `--json` 输出 JSON，默认文本输出与身份语义保持不变。契约见 [Feature 027](ivory-tower/features/027-installed-power-metadata/spec.md) 与 [ADR-024](ivory-tower/adr/ADR-024-persist-installed-power-description-and-tags.md)。

相关提交：

- `f34b013 docs(adr): persist installed power description and tags`
- `31d1663 docs(metadata): specify installed power metadata and list/search output`
- `0e39bf7 feat(metadata): persist and expose installed power description and tags`

解决方案或完成结果：`internal/domain` 新增 `InstalledPower{Ref, Description, Tags}`（含深拷贝 `Clone`）与 `InstalledPowerGroups`，`PowerRef` 保持纯身份，不参与 `==`、map key、排序与缓存键。`internal/config` 的 manifest `powerEntry` 追加 `description,omitempty` 与 `tags,omitempty`，并补齐读写转换、`normalizeTags` nil→`[]` 归一化与深拷贝；空元数据省略写出，旧清单无损读取为 `""`/`[]`。`internal/transaction` 的 `Journal` 追加 `description`/`tags`（`omitempty`），四类 install 事务从 `entry` 透传，filesystem/MCP/plugin 三类 recovery 经 `journal.installedPower()` 重建并写回清单；`Power` 仍为 `PowerRef`，只有新版本的元数据被 journal。`internal/lifecycle` 把 `CatalogEntry`（list 云 / search）与 `InstalledPower`（list `--installed`）的 description/tags 透传进结果，`filterSearchPowers` 在 keep-latest 去重时保留最新版本的 description/tags。`internal/cli` 为 `list`/`search` 增加 `-a/--all` 与 `--json`：文本 `-a` 在身份行下追加两空格缩进的 `description` 与 `tags`（非空才输出），`--json` 输出顶层数组，`--json` 不带 `-a` 只含身份字段、带 `-a` 才追加 description/tags；list 与 search 共用 `renderPowers` 渲染路径。身份、幂等、恢复与冲突语义不变，journal/manifest schema version 不升级。

验证结果或限制：`go build ./...`、`go vet ./...`、`go test ./...` 与 `gofmt -l .` 均以退出码 0 通过。新增用例覆盖 manifest description/tags round-trip、旧清单无字段读取、install 把元数据持久化进 manifest 与 journal、list/search 结果透传元数据、search keep-latest 保留最新版本元数据，以及 CLI 的 `-a` 文本、`--json` 身份、`--json --all` 全量渲染。description/tags 仅作为本地元数据展示，不参与身份或幂等判断，也不按元数据筛选、排序或搜索。

## 2026-08-26

分类：功能变更

用户需求：将 Agent 从单个原始 `agent.yaml` 升级为与 Skill、Plugin 一致的确定性 `.tar.gz` 目录资源，使 Agent 可以携带可选 assets，并以 `agent.metadata.avatar` 引用包内 PNG；同时把 Agent 的外部资源身份从 `agent.id` 修正为 `agent.name`（`agent.id` 只供 Bibit 内部区分）。`validate`/`submit` 只接受 Agent 资源根目录（根闭集为必需 `agent.yaml` 与可选 `assets/`，无版本包裹目录）；五个必填字段为整数 `schema_version: 1` 与非空字符串 `agent.id`/`agent.name`/`agent.version`/`agent.description`/`agent.soul`；可选 `metadata.avatar` 必须引用 `assets/` 下真实方形 PNG（大小 1..1 MiB、宽高 128..1024）。Agent 内容类型统一为 `application/gzip`，MD5 针对归档原始字节；cache 保存完整目录 `cache/agents/<name>/<version>`，Bibit 活动目标为 `agents/<name>`。字段源为 `agent_yaml_配置模板_V0.5.yaml`，契约见 [Feature 026](ivory-tower/features/026-agent-package-assets/spec.md) 与 [ADR-023](ivory-tower/adr/ADR-023-package-agent-with-assets.md)。

相关提交：

- `572fb17 feat(agent): parse v0.5 resource identity`
- `1bde00f feat(agent): validate packaged assets`
- `640e661 fix(agent): enforce root closed set for multi-segment paths`
- `e3922f3 feat(submission): upload packaged agent assets`
- `c9643d1 feat(cloud): download agent archives as directories`
- `9c12432 fix(cloud): align local agent semantic errors with ErrInvalidAgent`
- `a230bde feat(cache): cache complete agent directories`
- `ddfe097 feat(agent): publish complete bibit agent directories`
- `8357241 test(agent): enforce packaged lifecycle contract`
- `29ed248 test(lifecycle): align agent catalog fixture content type`

解决方案或完成结果：`internal/domain` 的 AgentConfig 扩展 V0.5 稳定字段（`agent.name`/`agent.soul`/`metadata.avatar`），`ValidatePowerRef` 改为以 `agent.name`/`agent.version` 与请求身份比较，`agent.id` 只要求非空且不再与 PowerRef name 比较；五个必填字段缺失、空白或错误类型统一拒绝 `domain.ErrInvalidAgent`。新增 `internal/agentartifact` 作为 Agent 目录的唯一权威只读校验器（根闭集、可执行/嵌套归档/路径安全、avatar PNG、身份），submission、Local/Remote Cloud 下载、cache、Bibit Adapter 与 transaction recovery 全部复用，不再各自维护字段或 avatar 规则。`internal/submission` 的 Agent Loader 只接受资源根目录并固定整棵树的不可变物理快照，拒绝单个 YAML、预制归档与远程 URL；Build 输出确定性 `.tar.gz`（稳定路径排序、固定 mtime/uid/gid 与 gzip header），Content-Type 为 `application/gzip`，MD5 针对归档原始字节，multipart content filename 为 `<name>-<version>.tar.gz`，metadata 身份来自已校验的 `agent.name`/`agent.version`/`agent.description`。`internal/cloud` 的 `DownloadAgent` 以私有目录为目标：Remote 校验 HTTP type/length、Catalog size/MD5 与归档安全后解包并执行权威目录校验，Local 复制 canonical fixture 目录（`agents/<name>/<version>/agent.yaml + assets/`）后执行同一校验，任一失败不留下可见 destination。`internal/cache` 的 `PrepareAgent` cache hit 复验完整目录、miss 下载到唯一临时目录、原子发布 `cache/agents/<name>/<version>` 并物化 `staging/agents/<name>`；损坏或不匹配 fail closed 并保留现场。Bibit Adapter 的 `TargetPath` 改为 `<bibit根>/agents/<name>` 完整目录，Install 从 staging 复验后以不覆盖语义原子发布；filesystem journal schema 不变，backup/quarantine/recovery 对整目录操作并执行完整身份校验。avatar 存在时必须为 `assets/` 下规范相对路径的真实 PNG 普通文件：大小 1..1 MiB、PNG 签名有效、宽高 128..1024 且为正方形，不校验 DPI/色彩/透明度，缺省时不要求 `assets/` 存在。`assets/` 允许二进制普通文件（仅 `agent.yaml` 走文本内容策略），其余 asset 只受公共物理树与归档阈值约束。Feature 025 的 lifecycle 入口状态、单次 warning、同版本不探测与唯一公共 `adapter.ErrTargetConflict` 均保持不变。这是不兼容迁移：所有本地 fixture 与开发数据直接替换，不保留 `application/yaml`、单文件输入、`agents/<agent.id>` 目标或 `agent.id == PowerRef.name` 兼容分支；Agent 内容身份闭环与 Submission 治理沿用 ADR-019/Feature 022 中未被取代的部分。

验证结果或限制：`go test ./... -count=1` 全量通过，`go vet ./...` 与 `git diff --check` 均以退出码 0 通过，`git status --short` 只包含本次 Task 8 文档变更。这是不兼容迁移：旧 `application/yaml` Agent content、单文件 validate/submit 输入、`agents/<agent.id>` 单文件 cache/staging/target 与 `agent.id == PowerRef.name` 身份均不再被接受并 fail closed；avatar 之外的 asset 业务格式不校验；Remote Agent 目录发布在 FreeBSD 与 Skill/Plugin 一样以 `ENOTSUP` fail closed；Agent 整目录事务的备份/恢复体积高于单文件，但仍受 100 MiB 总量限制。

## 2026-08-26

分类：功能变更

用户需求：统一 Skill、Agent、MCP 与 Plugin 在 `install`/`uninstall` 中面对相同入口状态时的宏观行为——同版本不探测即 `unchanged`；不同版本加缺失/不完整目标记录一次 warning 后同次修复并全新安装；无清单目标冲突只暴露公共 `adapter.ErrTargetConflict`；Plugin 卸载同时清除 Plugin 与专属 marketplace 并保留 artifact cache；显式精确 manifest 全命中快速路径扩展到四类 Power；CLI 运行时失败只记录一次 Error 不再打印 usage。契约见 [Feature 025](ivory-tower/features/025-unified-lifecycle-state-semantics/spec.md) 与 [ADR-022](ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md)。

相关提交：

- `965f795 refactor(lifecycle): add shared entry-state policy`
- `1e4523f feat(lifecycle): align file power install states`
- `775c216 feat(mcp): align install entry states`
- `eb42d27 feat(plugin): remove marketplace during uninstall`
- `a261e87 feat(lifecycle): align plugin entry states`
- `2f5757c feat(install): generalize exact manifest fast path`
- `e8f6470 test(lifecycle): enforce cross-power state contract`

解决方案或完成结果：`internal/lifecycle` 新增共享纯状态策略，把 manifest（`absent`/`same-version`/`different-version`）与目标（`absent`/`complete`/`incomplete`/`conflict`）映射为动作与 outcome，四类 Power 保留各自类型专属 probe 与 transaction。同版本在目标探测前直接返回 `unchanged`，不检查目标是否存在或完整；不同版本且目标缺失或不完整时记录一次 `installed power target is missing` warning，先经 repair transaction 清理仍存在的受管残留并删除旧清单，再以首次安装语义安装新版本，成功只输出 `installed`；无清单但存在运行目标时拒绝接管，唯一公共业务原因是 `adapter.ErrTargetConflict`，MCP/Plugin 专属 conflict sentinel 不再作为额外 `errors.Is` 类型暴露。Plugin 卸载删除 Plugin、注销专属 `apm-<name>` marketplace 并删除 manifest，但保留共享不可变 artifact cache；Plugin 部分状态按新矩阵收敛（能证明归 APM 管理的缺失/不完整按缺失清理，身份或来源冲突仍 fail closed）。显式规范 Type/Name/Version 在所有目标 Agent 清单精确命中时四类 Power 都直接返回 `unchanged`，不访问 catalog、Cloud、cache、Plugin Resource、Adapter 或 Transaction。CLI 以 `runtimeFailure` 标记区分运行时失败与参数/writer 错误，前者只记录一次 Error 且不再打印 usage。

验证结果或限制：`go test ./internal/lifecycle ./internal/transaction ./internal/cli ./cmd/apm -count=1`、`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build -o ./apm ./cmd/apm`、`gofmt -l internal/lifecycle internal/transaction internal/pluginresource cmd/apm`（无输出）与 `git diff --check` 均以退出码 0 通过。同版本 `unchanged` 明确不审计目标健康，目标被外部删除或损坏时重复安装不会修复；依赖旧 `ErrStaleManifest`、MCP/Plugin 专属 conflict sentinel 或卸载后 retained marketplace 行为的调用方需要迁移。迁移提示：旧版 Plugin 卸载 journal 若停留在 `rolling-back` 阶段，升级后会以 `ErrInvalidJournal` 暴露，需确认状态归属并删除残留 journal 后重试；spec §10 不提供兼容开关，此说明仅为可发现性，不改变行为。

## 2026-08-25

分类：功能变更

用户需求：为 skill-card.md 增加格式版本字段 `schema_version: 1`，同步更新 cloud resources 内全部 Skill/Plugin 夹具，并输出四类 Power 的提交格式校验说明与模板。

解决方案或完成结果：`internal/submission` 的 `validateSkillCard` 现在要求 `skill-card.md` 首字节起为 YAML frontmatter，且 `schema_version` 必须为整数 `1`（缺失、非整数或非 1 均拒绝，未知键与 SKILL.md frontmatter 一致允许忽略）；`extractFrontmatter` 改为同时返回关闭标记之后的正文，供标题与章节校验复用。`internal/cloud/resources/` 下 15 个 skill-card.md（13 个独立 Skill 与 2 个 demo-plugin 内嵌 Skill）全部补齐 frontmatter。`docs/power-template/` 新增一份格式校验说明与 skill/plugin/mcp/agent 四份模板。

验证结果或限制：`go test ./...` 全量通过；新增缺失/非 1/非整数 schema_version 的拒绝用例与未知键放行用例。旧格式（无 frontmatter）skill-card.md 不再被接受，与 ADR-020「旧格式不兼容」一致。

## 2026-08-25

分类：功能变更

用户需求：为 APM 增加类似 apt 的资源目录缓存：APC 以一个无分页的确定性 Catalog 文件发布 Skill、Plugin、MCP、Agent 全部版本，并提供 SHA-256/size version 接口；APM 在 list/search/install 前按内容校验码同步本地目录，相同内容即复用，按 Power 分组 schema 在本地完成筛选和版本解析。收敛 APC 公开接口并清理不再需要的调用，同时保持 `apm.lock` 串行边界、有效旧目录的 stale 可用性和已有制品离线安装。Uninstall/Fix 不应依赖 Catalog 或 Cloud，MCP 卸载恢复也不应再为删除操作获取或持久化 definition。相关决策记录于 APM ADR-021 与相邻 APC 仓库的 ADR-004。

相关提交：

APM（Feature 024 行为提交）：
- `92543af feat(catalog): add strict immutable snapshots`
- `346dfa8 fix(catalog): enforce exact JSON fields`
- `d323bf5 feat(catalog): persist snapshots securely`
- `d8d7676 fix(catalog): harden repository publication`
- `ac6209d fix(catalog): reconcile secure replacement state`
- `8aabef0 feat(catalog): synchronize cached snapshots`
- `12c71fb fix(catalog): preserve synchronization failure causes`
- `ef28101 fix(catalog): classify oversized staged catalogs`
- `625efcf feat(cloud): download remote catalog snapshots`
- `0a84732 fix(cloud): harden remote catalog transport`
- `a1b8351 feat(cloud): generate local catalog snapshots`
- `72f07b1 fix(cloud): harden local catalog snapshots`
- `7a2bc0c feat(lifecycle): query cached catalog snapshots`
- `7e77c6c fix(cmd): reject typed nil catalog managers`
- `c0b65d6 refactor(install): consume catalog metadata once`
- `159c329 refactor(mcp): uninstall without cloud definition`
- `1f4be80 fix(mcp): honor uninstall recovery cancellation`
- `5cbbf29 refactor(cloud): finalize catalog provider contract`
- `13d865f fix(cloud): remove uninstall provider dependency`
- `2e788f0 fix(cloud): enforce read-only lookup and entry validation`

APC（文档契约，未添加 APC 实现）：
- `d75bf24 docs(c4): separate plugin resource cache`
- `e64f689 docs(api): tighten catalog cache contracts`
- `5eb4eba docs(api): define catalog snapshot endpoints`

解决方案或完成结果：新增独立 `internal/catalog` 领域、Repository 与 Manager。唯一持久文件为 `~/.apm/catalog.json`：顶层和 `skills/plugins/mcps/agents` 四个分组分别维护 schema v1，Item 由分组确定类型，并携带精确版本、description、message、content type、`0 < content_size <= 20 MiB`、MD5、发布时间和 tags；`CatalogVersion.catalog_size` 仍允许为非负值并受 100 MiB 上限约束。`CatalogVersion` 与完整 Catalog JSON 均为字段闭集，扩展只能通过 schema/API 演进。tags 只作为 Catalog Entry metadata，当前 list/search 不展示、筛选、排序或搜索。Repository 对 100 MiB 上限、严格 JSON、排序、身份唯一性、`0600`、普通文件/唯一硬链、目录信任链和 SHA-256/size 执行 fail-closed 校验，以私有临时文件和平台安全原语原子替换；其 pathname 模型明确把 current EUID 视为受信任主体。Linux/macOS 支持 Repository `Load`/`Replace`；FreeBSD 只支持 `Load`，`Replace` 返回 `ENOTSUP`；Windows 与其它平台两项都 fail closed。Manager 在非阻塞 `apm.lock` 内先加载本地文件，再比较 `CatalogVersion`；hash 相同不下载，hash 不同才拉取完整 Catalog 并替换，换源但内容相同也直接复用。仅 Provider/更新失败可以在已有严格有效文件时返回一次 stale warning；锁冲突、取消和本地安全错误不降级。list/search 各消费一次 `Snapshot`，释放锁后才按 stale 状态 warning 并执行本地查询；install 在既有生命周期锁中调用 `SnapshotLocked`，显式 Plugin manifest 全命中可在同步前返回 unchanged。四类 content 接口统一接收同一个完整 `CatalogEntry`，Plugin Resource 的 `Ensure(ctx, entry)` 也消费该同一条目；Local/Remote 不再通过公开分页、series、latest 或精确 metadata 接口二次查询。Remote 只使用匿名 `GET /api/v1/resources/version`、无 query 的 `GET /api/v1/resources` 和精确 content endpoint。C4 将只管理 Skill/Agent 的 Artifact Cache Manager、独占 Plugin 持久制品的 Plugin Resource 与无制品缓存的 MCP 路径分别建模。有效 stale Snapshot 可配合 Skill/Agent/Plugin 已有制品离线安装，cache miss 仍失败。Uninstall 与 Fix 的构造和执行路径均为零 Catalog/零 Cloud；Plugin 卸载只读 Lookup 本地制品，MCP target absent 时直接清理清单，present 时按名称删除并写入不含 definition 的卸载 journal，恢复始终继续收敛到 absent，安装/切换 journal 仍保留 definition 用于安装回滚。APM/APC 当前 BUILD、Project Overview、HTTP API、ADR 引用、C4 与生命周期图已同步；APC 尚未开发，本次仅更新接口契约，不新增服务端实现。

验证结果或限制：fresh `gofmt` 检查、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...` 和私有临时目录隔离构建均退出 0，构建产物与临时目录已删除；生产 Go 过期接口/卸载依赖扫描无匹配。7 个变更 Mermaid 源均通过 Draw.io CLI 转换为权限私有的临时 `.drawio` 并在验证后清理，APM 与 APC 两个 Structurizr workspace 均校验通过；变更 Markdown 的相对链接目标、当前权威文档 stale-contract 扫描和两仓 `diff --check` 均通过。Catalog stale 模式会在 APC 不可达时允许使用已有目录与制品，因此不能即时阻止随后被下架的缓存版本；在线 content miss 仍 fail closed。Repository 的 advisory lock 和身份复验不承诺隔离恶意同 EUID 进程在 POSIX 最终 `fstatat` 到 `unlinkat` syscall 窄窗内的替换；需要覆盖同凭据 Byzantine 写者时必须使用更强的进程/目录隔离或可信设施。APC 本次只有设计与 API 文档，服务端的确定性生成和 current Catalog 原子切换仍待未来实现。

## 2026-08-25

分类：新增功能

用户需求：让 Skill、Plugin、MCP 与 Agent 的 Submission 身份和描述统一从权威资源文件读取，把提交更新说明统一为必填 `--message`，将 MCP 升级为自描述的完整 v1 资源，并新增 API Key 认证的 Submission get/list；list 仅返回当前账号最多 20 条 active Submission，get 完整展示描述、message、时间和 timeline。相关客户端决策记录于 ADR-020，APC 契约决策记录于相邻 APC 仓库的 ADR-003。

相关提交：

APM（Feature 023）：
- `396a708 docs(submission): design source-owned metadata queries`
- `22417f5 docs(submission): plan resource metadata queries`
- `44b8da1 feat(domain): define submission resource contracts`
- `23c1fd1 refactor(submission): load identity from resources`
- `3f90093 feat(cloud): consume self-describing MCP resources`
- `1576b11 feat(submission): use resource identity and message`
- `6998337 fix(submission): require remote message`
- `41fbf4d feat(cloud): query bounded authenticated submissions`
- `f1e9ba7 feat(cli): show active submission details`
- `f08f00c fix(cli): preserve empty submission errors`
- `1742c89 test(submission): verify resource and query contracts`
- `edb5098 docs(submission): document resource metadata queries`
- `993a796 docs(c4): align cli flags diagram`
- `c546650 fix(submission): freeze validated source content`
- `6f59f36 fix(submission): validate replay and quota errors`
- `7ee75ba fix(submission): validate plugin snapshot closure`

APC（文档契约，未添加 APC Changelog）：
- `da453e7 docs(submission): bound authenticated active queries`
- `192214e docs(api): simplify authenticated submissions`
- `de85629 docs(api): align skill and timeline contracts`

解决方案或完成结果：`internal/submission` 新增仅在 Submission 边界内使用的统一 Source Loader：Skill 从 `SKILL.md` frontmatter、Plugin 从 `apc-plugin.json`、MCP 从 `MCP.json`、Agent 从 `agent.yaml` 获取 name/version/description，目录名和 CLI 不再覆盖身份。单文件通过 no-follow 打开并在同一文件描述符上校验普通文件、inode、唯一硬链与大小；目录在校验读取时冻结 entry/body/mode，后续 Build 只消费不可变快照，源路径被替换、修改或新增文件均不会漂移上传字节。Plugin 复用 canonical package 闭集校验，拒绝额外根 payload、native manifest 和不同路径下同名内嵌 Skill；Skill frontmatter 开头只接受精确 `---` 加可选水平空白及 LF/CRLF。Skill/Plugin 构建后同时执行 20 MiB 上传上限和溢出安全的 100× 总解压倍率检查。`validate` 只做完整离线校验，`submit` 对四类 Power 统一要求 trim 后非空的 `--message`；旧 `--version`、`--change-summary`、`change_summary` wire 字段与兼容别名已移除，message 不进入 Power content，也不参与 MD5。MCP 统一为严格的 `schema_version/name/version/description/definition` v1 完整资源；Submission 上传其 JCS 字节，Local/Remote 校验文件内身份与 `PowerRef` 完全一致后仅返回防御性复制的 definition。Remote Submission submit/get/list 使用独立 Bearer 路径，匿名 catalog/metadata/content 不携带 Key；list 仅发送 status/type/name，并严格校验 active-only、最多 20 条、倒序、筛选匹配、唯一 ID 和 RFC 3339 时间；get 与 200 replay 共享完整 detail/timeline 校验，replay 还要求 type/name/version/description 与请求精确一致，202 receipt 状态只能为 `received`。成功 JSON 拒绝重复 key、尾随值和缺失必填字段，同时忽略未知字段以保持前向兼容。`TOO_MANY_ACTIVE_SUBMISSIONS` 同时映射为专用错误和通用 Submission conflict；CLI 保留该 sentinel 并提示先运行 `apm submission list`，同时支持 text/json 列表与完整详情输出。Local Submission 保持只读并返回 `ErrSubmissionUnsupported`，认证、网络或协议错误不回退 Local、cache 或 snapshot。APM/APC 的 BUILD、Project Overview、ADR、Feature 历史取代说明、C4 和 APC HTTP API 文档已对齐；Skill 权威格式现明确 version 与 metadata 一致，timeline message 明确为必填非空。APC 仓库没有新增服务端、持久化、队列或运行时配置。

验证结果或限制：最终修复与 Changelog 提交后以退出码 0 运行 affected package tests、`git diff --name-only --diff-filter=ACMRT "$(git merge-base main HEAD)"..HEAD -- '*.go' | xargs -r gofmt -w`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`build_dir="$(mktemp -d)"; go build -o "$build_dir/apm" ./cmd/apm; test -x "$build_dir/apm"; unlink "$build_dir/apm"; rmdir "$build_dir"; test ! -e "$build_dir"` 与 `git diff --check`；新增 source 平台设施还通过 Linux、Windows、Darwin、FreeBSD amd64 隔离交叉编译。负例覆盖 Plugin 额外 payload/native manifest/重复 Skill name，MCP/Agent 符号链接与硬链接，Load 后替换/新增/修改，100× 精确边界与超限，frontmatter 粘连 opener，replay 缺字段/坏 timeline/身份漂移、非 `received` receipt，以及 quota CLI sentinel/指引。精确过期/安全扫描确认生产 Go/JSON 无 `ChangeSummary`/`change_summary`/`--change-summary`，Submission domain/Remote/CLI 无 cursor、时间范围或 `next_cursor`，全部内置 MCP 具备五个 v1 必填键，测试无真实 home 或 Agent CLI 访问；仅剩的旧字面量均是必要的 legacy rejection 测试。APC 的 5 个变更 Markdown 文件全部通过 CommonMark AST 解析，20 个相对链接（含 2 个跨仓库链接）均可达，Structurizr workspace 校验通过，当前 Submission 契约无旧字段/分页描述，通用资源和内网工单 cursor 契约保持不变。本次不实现 APC 服务端与历史查询，不兼容旧 CLI/MCP/wire 格式，不新增取消、删除、重提、自动等待或审核操作；终态 Submission 仅能通过已知 ID get。

## 2026-08-24

分类：新增功能

用户需求：让 Agent 作为第四种 Power 进入与 Skill、Plugin、MCP 相同的 Submission 与治理流程，实现 validate、submit 与 Submission 生命周期对齐：`apm validate` 与 `apm submit` 支持 `--type agent`；Agent Submission 输入为单个原始 `agent.yaml`（不打包、不重序列化），name、version、description 分别来自 `agent.id`、`agent.version`、`agent.description`，change summary 来自必填 CLI 参数；Remote Cloud Client 支持 Agent multipart 并严格区分首次受理与幂等重放，Local Cloud Client 保持只读。同时补齐 Agent 安装内容与 `PowerRef` 的身份闭环，并让 APM `Submit` 正确表达 APC 的 `202` 首次受理与 `200` 幂等重放语义。客户端决策记录于 ADR-019，APC 业务治理决策记录于相邻 APC 仓库的 ADR-001。

相关提交：

APM（Feature 022）：
- `7e2e9a2 docs(design): specify agent submission lifecycle`
- `306102e docs(plan): detail agent submission implementation`
- `65b2f72 feat(domain): validate agent submission identity`
- `5242815 feat(submission): prepare raw agent yaml`
- `1d4a776 feat(domain): model submission outcomes`
- `d5f34d4 feat(cloud): submit agent resources remotely`
- `a65b161 feat(cli): validate and submit agent resources`
- `bc9782f fix(cache): verify exact agent identity`
- `37f14b3 feat(adapter): verify agent artifacts`
- `1739003 fix(transaction): validate agent recovery identity`
- `5655c5a test(agent): verify submission lifecycle parity`
- `0d7d402 docs(agent): document submission lifecycle parity`

APC（APC 仓库，Feature 022 对应）：
- `85d9148 docs(agent): define submission and publication contract`

解决方案或完成结果：Agent 复用现有统一管线 `submission.Validate`、`submission.Prepare`、`cloud.Submission` 与 `/api/v1/submissions`，不新增 `SubmitAgent`、Agent 专属 endpoint 或平行状态机；类型专属规则集中在 `internal/submission/agent.go`。Agent Submission 输入为单个原始 `agent.yaml` 字节，不打包、不重序列化；name、version、description 来自 `agent.id`、`agent.version`、`agent.description`，change summary 由必填 `--change-summary` 提供，禁止 `--version`。validate、submit、缓存、Bibit Adapter 与 Agent 恢复复用同一套最小稳定 YAML 解析和身份校验规则，形成 domain → cache → adapter → recovery 的身份闭环：cache 精确校验 Agent 身份，adapter 校验 Agent 制品，transaction 校验 Agent 恢复身份。Remote Cloud Client 支持 Agent multipart，`202` 首次受理映射为 accepted、`200` 幂等重放映射为 replayed、`409 ALREADY_PUBLISHED` 映射为 `ErrAlreadyPublished`（不伪造新的 Submission ID）、`409 RESOURCE_VERSION_CONFLICT` 区分同标识同版本不同 MD5；Local Cloud Client 保持只读（`Submit` 返回 `ErrSubmissionUnsupported`）。APM/APC 设计文档、C4 与 APC HTTP 接口规范已同步，ADR-019（APM 客户端决策）与 APC ADR-001（业务治理）记录相关决策。

验证结果或限制：全量门禁全部通过：gofmt、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、隔离临时目录构建 `go build -o <tmp>/apm ./cmd/apm` 与 `test -x`、`git diff --check`；docs/Structurizr/Mermaid 文档验证通过。行为与隔离扫描确认无真实 Agent CLI/home 使用、无残留临时目录、无旧签名或三类限制（仅历史/已否决措辞保留在 ADR-001 与 Feature 021 设计文档中，均显式标注为被取代）。刻意未实现 Submission 查询增强（cursor、创建时间筛选、详情 timeline）。

## 2026-08-21

分类：新增功能

用户需求：基于 APC 统一资源接口新增可切换的 Local/Remote Cloud Client provider，补齐 Agent 只读获取，并将 Plugin Cloud 资源迁移为以 apc-plugin.json 为权威、由 APM 派生 Agent 配置的 canonical 包。

相关提交：

- `b2dce66 docs(cloud): design provider clients and canonical plugins`
- `cd23b13 docs(cloud): plan provider client implementation`
- `52d3790 refactor(cloud): add context-aware catalog resolution`
- `167a228 fix(cloud): filter invalid local resource versions`
- `3d984a9 refactor(cache): own skill and agent publication`
- `5224b3f fix(cache): preserve staging during publication`
- `2665946 fix(cache): report staging cleanup failures`
- `ed1eb08 refactor(cloud): isolate local provider plugin`
- `2e9a27d fix(cloud): harden local provider snapshots`
- `8571928 fix(cloud): support portable local snapshots`
- `58e3802 fix(cloud): inspect local nodes without data access`
- `843c7db fix(cloud): restore safe Darwin local snapshots`
- `ed051a1 feat(cloud): add remote catalog provider`
- `e8f6d84 fix(cloud): harden remote response handling`
- `6688a26 fix(cloud): preserve remote transport errors`
- `e06ee77 feat(cloud): verify remote mcp and agent content`
- `8e56fc4 fix(cloud): secure remote content publication`
- `329dbef fix(cloud): enforce remote directory trust`
- `574f94c feat(cloud): safely unpack remote power archives`
- `fadcdda fix(cloud): bound remote archive parsing`
- `dcd6939 fix(cloud): separate archive stream limits`
- `81fb210 refactor(plugin): derive agent manifests from apc index`
- `f8a2b50 fix(plugin): close canonical artifact schema`
- `cf7de56 test(cloud): verify local and remote provider parity`
- `c5ed545 fix(cloud): normalize local publication modes`
- `9b2e11d fix(cloud): keep local agent staging private`
- `23a3226 test(cloud): verify local staging write order`
- `3bc7e19 test(cloud): strengthen provider parity evidence`
- `3ddc16b feat(cloud): select local or remote providers`
- `a9bf414 fix(cloud): validate provider configuration`
- `eaa8440 fix(cloud): harden provider construction`
- `9ffe171 chore(cloud): exclude task review report`
- `08f2c49 docs(cloud): document provider runtime and apc plugins`
- `267619c docs(cloud): correct provider architecture details`
- `07d7205 docs(cloud): align c4 interface signatures`
- `088acd4 chore(repo): remove task review artifact`
- `c86030a test(cloud): isolate trusted test roots`
- `b1b9fe9 fix(cloud): trust private sticky-root children`
- `17e1860 test(cloud): honor gotmpdir isolation`
- `f9d0787 fix(cloud): enforce local provider identity contract`
- `6bb3237 fix(cloud): validate remote JSON media types`
- `0cd67d6 fix(cloud): align archive text safety policy`
- `4b2e8f0 docs(cloud): mark provider implementation complete`
- `f470470 docs(agent): define agent power read interfaces`
- `f22f2f4 docs(apm): clarify MCP uninstall cloud dependency`

解决方案或完成结果：建立统一、context-aware 的 Cloud Client 虚拟端口和静态 provider registry，由 composition root 默认选择 Remote，并允许通过环境变量显式切换 Local；两种 provider 对 Skill、Plugin、MCP、Agent 四类资源提供一致的 List、Resolve 和精确获取语义，Remote 额外执行严格协议、size、MD5、归档与文本完整性校验且不自动 fallback。Skill 与 Agent 的版本化发布归 cache 管理；Plugin 以 canonical `apc-plugin.json` 和共享资源树为权威，由 APM 校验后派生各 Agent native manifest 与 marketplace。Plugin Uninstall/Fix 复用已有 canonical artifact 离线恢复；目标仍存在的 MCP Uninstall 按无缓存约束读取精确定义。

验证结果或限制：最终审查前及修复后均通过 gofmt、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、隔离临时目录构建与 `git diff --check`，并通过 Local/Remote 的 Darwin、FreeBSD、Windows 相关包交叉编译以及 provider 负面等价、归档攻击和恢复测试。网络测试只使用 `httptest`，HOME、cache、Agent 根和 CLI 均使用 fake home/fake executable，未访问真实 Agent 环境。Remote 配置、网络、协议或内容错误不会 fallback 到 Local；Agent 仅提供只读获取，不包含 Submission；旧 Cloud API、旧 cache 与缺少 canonical 索引的 Plugin artifact 不兼容并 fail closed。

## 2026-08-20

分类：新增功能

用户需求：新增第四种 Power 类型 `agent`（仅 Bibit 适用，由 `agent.yaml` 配置的 Bibit 子代理），并把 Bibit 默认资源根修正为 `~/.bianbu-agent`。

相关提交：

- `39f9dd1 docs(agent): add bibit-only agent power spec and versioned cloud resource`
- `46e7680 docs(adr): record bibit-only agent power and bibit root correction`
- `22d96ee docs(agent): add implementation plan for bibit-only agent power`
- `b93a4ab feat(domain): add agent power type and agent config`
- `e9bfdd4 fix(adapter): correct bibit default root to ~/.bianbu-agent`
- `4953365 feat(cloud): list and download versioned agent yaml`
- `e4ec3f4 test(lifecycle): satisfy cloud.Client interface after DownloadAgent addition`
- `0addd38 feat(cache): stage single-file agent yaml`
- `5dc3961 test(lifecycle): satisfy cache.PowerCache interface after StageAgent addition`
- `05542b0 feat(adapter): add bibit filesystem agent adapter`
- `9669c27 feat(transaction): add recoverable filesystem agent install/uninstall`
- `2213942 test(lifecycle): satisfy transaction.Manager after InstallAgent/UninstallAgent addition`
- `d298939 feat(config): add agents manifest group`
- `78d9a41 feat(lifecycle): add agent install/uninstall and bibit default`
- `9d24c37 test(lifecycle): satisfy AdapterRegistry after ResolveAgent addition`
- `abea656 feat(cli): accept --type agent with bibit-only agent rule`
- `2fc7f93 docs(agent): document agent power across build and overview`
- `a49349e fix(lifecycle): default list --installed agent to bibit`
- `ae4edb2 fix(transaction): prune empty agent dirs and add file-target recovery tests`

解决方案或完成结果：Cloud 资源布局对齐 skill（`agents/<name>/<version>/agent.yaml`），版本目录为版本真相；下载单元是单个 `agent.yaml`，走版本化缓存 `cache/agents/<name>/<version>/agent.yaml` + staging，由 Bibit 专属 `AgentAdapter` 原样字节原子发布到 `<bibit根>/agents/<agent.id>/agent.yaml`（本地不版本化，版本仅记 manifest）。安装/卸载复用 `DriverFilesystem` 可恢复事务（target 为文件路径）与 `fix`，卸载后裁剪空的 `agents/<id>/` 与 `agents/` 父目录；config manifest 增加 `agents` 分组。CLI `--type agent` 支持省略 `--agent` 默认 `bibit`、显式 `bibit` 允许、其它值（含 `all`）报错。Bibit 默认根由 `bianbu-agent` 修正为 `.bianbu-agent`（`BIANBU_AGENT_HOME` 与 CLI 可执行名不变）。详见 Feature 019 与 ADR-015。

验证结果或限制：六条门禁全部通过（gofmt、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、临时目录构建、`git diff --check`）；覆盖文件级 target 的 install/uninstall/rollback/fix 中断恢复。测试将 PATH、APM home、cache 与所有 Agent home 隔离在 `t.TempDir()`，未调用真实 `bianbu-agent`。Cloud 资源对齐由其管理方保证，APM 不交叉校验 id/version 与目录名；不迁移旧 cache/journal。

## 2026-08-20

分类：新增功能

用户需求：新增 Bibit 的 Skill/MCP/Plugin 适配，并把 Plugin Cloud/缓存迁移为共享多 Agent 制品。

相关提交：
- `f1b9c33 docs(bibit): design shared multi-agent plugins`
- `603a9e0 docs(bibit): plan shared plugin implementation`
- `c68d95e feat(domain): define bibit and logical plugin names`
- `3e8b8ad feat(adapter): add dedicated bibit mcp support`
- `537c406 feat(adapter): add bibit filesystem plugins`
- `247baea refactor(cloud): share plugin packages across agents`
- `f0082e3 refactor(plugin): define shared multi-agent artifacts`
- `77fa64b feat(plugin): publish shared immutable artifacts`
- `6d36813 fix(plugin): preserve staging during publication`
- `e6fc45a feat(transaction): recover shared plugin artifacts`
- `a55d655 fix(transaction): remove dead validation parameter`
- `dc46a99 refactor(plugin): use logical targets across agents`
- `dc14b77 fix(plugin): harden filesystem recovery boundaries`
- `4c0f613 fix(plugin): preserve removing-old recovery phase`
- `bfb2c43 feat(agent): register bibit power adapters`
- `4989fb4 test(agent): isolate bibit MCP harness`
- `6e6315e test(plugin): cover shared bibit lifecycle recovery`
- `8f24d11 fix(plugin): preserve retained marketplace recovery`
- `6f1e5bb docs(bibit): align shared plugin architecture`
- `7b0d6e8 docs(bibit): clarify retained marketplace flows`
- `890bd2a fix(plugin): validate filesystem recovery targets`
- `19c2d0b fix(plugin): reject unsafe uninstall recovery targets`

解决方案或完成结果：引入 logical selector 与共享不可变 cache，使用单份多 Agent Plugin 原包；APM 生成 Codex/Claude 双 marketplace，并支持 Hermes/Bibit 通过 `plugins/<name>` filesystem 部署；新增 Bibit 独立 MCP Adapter；filesystem/marketplace offline journal recovery 采用 fail-closed physical safety，并支持 retained exact marketplace 重装复用。

验证结果或限制：fresh gates 全部通过（gofmt、full/race/vet/build、Bibit/Plugin/Fix lifecycle）。验证仅使用 fake-bin-only PATH、临时 HOME/APM/cache/Agent roots，未使用真实 `bianbu-agent`；Cloud package completeness is trusted；不自动迁移旧 cache/journal；损坏或歧义状态 fail closed。

## 2026-08-17

分类：功能变更

用户需求：

以每个 Agent 的原始 Plugin 文件替换 Cloud 托管 marketplace 的权威来源：持久化按 Agent/Plugin/版本划分的本地不可变制品，并由 APM 为 Codex、Claude Code 按 Agent/Plugin 生成本地 marketplace，Hermes 保持 filesystem 部署；安装和卸载改为 manifest-first，版本切换和离线恢复必须事务化；删除旧 API，并将设计、架构和使用文档与新模型对齐。

相关提交：

- `4557a2a docs(plugin): design local marketplace artifacts`
- `45716f2 docs(plugin): plan local marketplace implementation`
- `b36105f feat(plugin): define local artifact contracts`
- `4e7bd42 feat(plugin): persist agent artifacts immutably`
- `879fe8c fix(plugin): harden artifact staging`
- `045bba4 feat(plugin): generate local marketplaces`
- `2398586 fix(plugin): validate downloads before marketplace build`
- `c82c074 feat(cloud): download agent plugin packages`
- `9e7e372 fix(cloud): reject plugin self-copy destinations`
- `552199b feat(adapter): expose marketplace mutations`
- `5091795 feat(transaction): model marketplace switching`
- `c31feeb fix(transaction): tighten marketplace validation`
- `f363d68 feat(transaction): switch local marketplaces safely`
- `a300600 test(transaction): cover marketplace rollback failures`
- `6c76bf9 test(transaction): use reachable first-install rollback`
- `63b9106 feat(transaction): recover marketplace plugin state`
- `d97f6e9 fix(transaction): validate recovery cache by direction`
- `6a97944 feat(lifecycle): install plugins from local artifacts`
- `9e580aa fix(lifecycle): validate absent plugin identities`
- `ff4c425 refactor(plugin): remove remote marketplace APIs`
- `e0be847 test(plugin): cover local marketplace recovery`
- `9a1e9b1 test(plugin): strengthen interruption acceptance`
- `b2d6014 test(plugin): prevent fake CLI path fallback`
- `e17f66e docs(plugin): document local marketplace artifacts`
- `47c5768 docs(plugin): split marketplace builders`
- `a263a0c fix(plugin): close final marketplace validation gaps`
- `fc0be17 fix(plugin): share Codex manifest validation schema`

解决方案或完成结果：

Cloud 只下载调用方目标目录中的单个 Agent 原始 Plugin 包；Plugin Resource 以 Agent/名称/版本保存并先校验后命中，构建和缓存命中均拒绝损坏或不符合语义的制品。Codex 与 Claude Code 各自生成只含当前 Plugin 的本地 marketplace，Hermes 不生成 marketplace。Install 在锁内恢复并取得 Agent 快照、完成选择后，全部 manifest 精确命中时不访问 Cloud、Plugin Resource 或 Plugin Adapter，且不启动安装事务；首装和版本切换通过可恢复的 Plugin transaction 写入 marketplace、Plugin 和 manifest。Fix 不连接 Cloud 或 Plugin Resource、不重建缓存，使用 journal、已有缓存、manifest 与 Agent Adapter/CLI 离线收敛。旧 Cloud marketplace/API 已移除，ADR-013、Feature、C4、BUILD 和项目概览均已同步。

验证结果或限制：

以下新鲜命令均以退出码 0 完成：`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`build_verify_dir="$(mktemp -d)"; go build -o "$build_verify_dir/apm" ./cmd/apm; test -x "$build_verify_dir/apm"`、`go test -count=1 -v ./internal/lifecycle -run 'Test(Plugin|Fix.*Plugin)'`、`git diff --check`，以及旧 Go API、旧 Cloud 资源路径、当前文档/图中的旧 marketplace/API/FETCH 文字和 Transaction 对 Cloud/Plugin Resource 直接依赖扫描。Plugin/Fix 集成测试将 PATH、APM home、cache 和所有 Agent home 隔离在 `t.TempDir()`，所有记录的可执行文件均位于 fake-bin，未调用真实 `/home/liangff/.local/bin/hermes`，也未修改真实 Agent home 或 cache。

现有限制：已有不匹配或损坏的 marketplace/cache 状态严格 fail closed；同版本 manifest 命中会返回 unchanged，不再与 Agent 状态核对；缓存没有 refresh/GC，同版本内容不可变。

## 2026-08-14

分类：新增功能

用户需求：

将 journal 的“观察恢复需求”与“执行恢复”分离：新增显式 `apm fix` 命令；Install/Uninstall 保持全局锁内恢复和写事务；Search、默认 List 和 Installed List 改为无锁只读 Inspect，对合法活动 journal 提示运行 Fix，对损坏或不可读 journal 只警告并继续原查询；同步 ADR、Project Overview 和 C4，明确无锁 best-effort 观测不是跨 Agent、manifest、journal 与目标状态的全局一致快照。

相关提交：

- `ed49463 docs(fix): design explicit journal recovery command`
- `b1e7aa0 docs(fix): plan explicit recovery lifecycle`
- `f35ad8f feat(transaction): separate journal inspection from recovery`
- `286c32f test(transaction): guard recovery journal inspection`
- `406f730 refactor(search): inspect journal without recovery lock`
- `7cc6764 fix(search): wire default factory without lock`
- `d321b71 refactor(list): use unlocked journal observation`
- `edda969 feat(lifecycle): add one-shot journal fix`
- `b5a5921 feat(cli): expose explicit fix command`
- `b5279d4 test(fix): cover isolated cross-driver recovery`
- `6a85b07 test(fix): cover read-only active journals`
- `e6e4859 docs(fix): define explicit recovery lifecycle`
- `18a2671 docs(fix): align recovery ports and current semantics`
- `99e1758 test(lifecycle): lock down recovery boundaries`

解决方案或完成结果：

Transaction 新增对外分离的 `Inspector` 与 `Recoverer`，`Inspect` 和 `Recover` 复用一次 Store 读取、统一校验和结果投影的私有原语；只读 Inspect 不创建、更新或删除 journal，不访问 manifest、Adapter 或目标文件。Search 与两种 List 共用无锁检查和 Warn/Debug 记录，检查失败不改变原业务结果或退出状态。Fix 在单次 Acquire/Release 区间内恰好 Recover 一次，不接受 Agent/Type/方向等选择；Install/Uninstall 仍直接在锁内 Recover，不先调用公开 Inspect。既有 schema v1、driver、phase、恢复方向、actions、批处理和输出语义保持不变。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`verify_dir="$(mktemp -d)"`后的 `go build -o "$verify_dir/apm" ./cmd/apm`与 `test -x "$verify_dir/apm"`、`git diff --check`、`go test -count=1 -v ./internal/lifecycle -run 'Test(Fix|MCP|Plugin|Search|List)'` 均以退出码 0 完成。MCP/Plugin 隔离集成测试把 `PATH`、APM/Cache/Agent home 限定在 `t.TempDir()`，并逐条断言实际可执行路径位于临时 fake-bin；未调用或修改真实 Codex、Claude Code 或 Hermes 环境。

当前限制：损坏、未知或不可读 journal 可被只读命令警告，但无法自动恢复；Fix 和写命令继续严格校验、保留现场并 fail closed，不删除、跳过校验或猜测恢复方向。

## 2026-08-14

分类：其他

用户需求：

在不改变 Skill、MCP、Plugin 现有实现和业务流程的前提下，统一 Power 与 Agent 的代码文件布局、接口名称和职责边界；公共 Agent CLI 执行设施保持为单包内独立的 `cli_runner`，主要接口与业务流程补充中文注释，并使结构便于后续新增 Power 类型和 Agent 适配。

相关提交：

- `5b50685 docs(refactor): specify code structure normalization`
- `9ba0675 docs(refactor): plan structure normalization`
- `9c5f869 refactor(adapter): name skill contract explicitly`
- `2485476 refactor(adapter): align power and agent layout`
- `8c11720 refactor(resource): expose power-specific cloud and cache operations`
- `ebe758a refactor(lifecycle): align power handlers`
- `3182992 refactor(transaction): name skill driver explicitly`
- `52b425f refactor(transaction): separate driver validation`
- `849ec90 fix(refactor): complete accepted structure layout`

解决方案或完成结果：

Adapter 保持单包并形成 Skill/MCP/Plugin 并列契约和 Codex/Claude/Hermes Agent×Power 文件矩阵，公共 CLI runner 独立承载进程调用；Agent 注册改为声明式 profile，检测职责收归 Registry。Cloud、Cache、Lifecycle 和 Transaction 分别改为类型显式的资源接口、命令优先处理器及 driver 优先恢复布局，删除未使用的通用 Result，并为主要接口、加锁边界、安装卸载流程、恢复和校验补充简要中文注释。重构保留既有 manifest、journal schema v1、事务阶段、恢复顺序、CLI 参数和错误语义；本次未引入新的架构决策，因此未新增 ADR，也未改动 C4、BUILD 或项目概览。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o <temp>/apm ./cmd/apm`、`git diff --check` 均以退出码 0 完成。MCP/Plugin lifecycle 隔离集成测试覆盖 Codex、Claude Code 和 Hermes Agent，CLI 均来自 `t.TempDir()` 下的 fake-bin，测试后工作树保持干净，未调用或修改真实 Agent 环境。代码复审确认 443 个测试函数保持不变，且无 Critical/Important 问题。

## 2026-08-13

分类：新增功能

用户需求：

在 APM 现有 Skill/MCP lifecycle 基础上增加 Plugin：Cloud 以 `.claude-plugin/marketplace.json` 为权威索引托管稳定全量 marketplace；Codex/Claude 注册或更新 marketplace 后安装版本化实际插件，Hermes 复用 Cache/Staging 发布到 `~/.hermes/plugins/` 并通过 CLI enable/disable；安装、升级、降级、离线卸载和中断恢复复用现有锁、manifest、批处理与唯一 schema v1 journal，模块职责和文件布局保持类型专属且清晰。

相关提交：

- `5953d09 docs(plugin): design lifecycle support`
- `6955ecd docs(plugin): plan lifecycle implementation`
- `d15f199 feat(plugin): add cloud marketplace discovery`
- `3f0cf3b feat(plugin): stage hermes plugin resources`
- `0c7baf8 refactor(adapter): share agent cli runner`
- `eed855a feat(plugin): add codex and claude adapters`
- `fb28504 feat(plugin): add hermes adapter registry`
- `503a0c2 feat(plugin): define recoverable transactions`
- `19fb350 feat(plugin): transact marketplace installs`
- `c39a22a feat(plugin): publish hermes plugin files`
- `ce4adfd feat(plugin): wire lifecycle install uninstall`
- `d446eb2 test(plugin): cover isolated agent lifecycle`
- `b8bad3f test(plugin): align lifecycle test layout`
- `5f9ae45 fix(plugin): fail closed on missing commit target`
- `3022995 fix(plugin): reenable previous hermes plugin`
- `abcb7fa fix(plugin): reenable rolled back hermes uninstall`
- `410a8f3 test(plugin): cover lifecycle edge cases`

解决方案或完成结果：

新增版本化实际名、marketplace definition 与双部署模式领域规则；Local Cloud 只解析 Claude marketplace 权威索引并提供 `GetPlugin`，Codex/Claude 不进入 Plugin Cache，Hermes 才按逻辑名和精确版本缓存并物化 staging。Registry 保持 `ResolveSkill`、`ResolveMCP`、`ResolvePlugin` 三张独立映射；三 Agent Plugin Adapter 各自封装原生 argv、JSON 和 source/selector 判定，共用有界超时 CLI runner，不执行写后 list 验证。

Plugin 使用独立 transaction driver/payload，与 filesystem Skill 和 agent-cli MCP 共用唯一 schema v1 journal。marketplace 安装、Hermes 文件发布、版本切换、离线卸载和恢复都只删除精确清单项或受控事务产物；卸载不连接 Cloud、不更新 marketplace。ADR-012 记录稳定 marketplace、版本化实际名、双部署模式和 Plugin 专属 driver 决策。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、focused package tests、`go test -count=1 ./...`、`go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`git diff --check` 及 schema/边界静态扫描均以退出码 0 完成。三 Agent 集成测试将 CLI、APM home、Cache 和 Agent home 全部隔离到 `t.TempDir()`；Codex、Claude、Hermes 均覆盖首装、unchanged、升级、降级和卸载，并覆盖未纳管目标、marketplace 异源、目标缺失清单修复、Search 恢复中断 journal 与多 Agent 首错停止；所有记录的可执行路径均位于 fake-bin，未调用 `/home/liangff/.local/bin/hermes` 或改动真实 Agent 环境。

当前 Local Cloud 使用仓库内最小双版本 Plugin fixture；生产 Cloud 的发布、签名、授权、认证与 marketplace 内容规范由 Cloud 管理方负责。

## 2026-08-13

分类：功能变更

用户需求：

APM 尚未发布，无需为开发期 journal 保留向后兼容；将现有 driver-based shared envelope 重置为首个正式 schema，并严格拒绝旧平铺格式及其他无效格式。

相关提交：

- `1bdb41c docs(transaction): design journal schema reset`
- `cee6da1 docs(transaction): plan journal schema reset`
- `c9a8b1d refactor(transaction): reset journal envelope to schema v1`
- `99b4390 test(transaction): align recovery fixture with schema v1`
- `a23e1c4 refactor(transaction): write one journal schema`
- `8373062 docs(changelog): defer journal schema record`
- `11f7044 docs(transaction): record single journal schema`
- `9a8e411 docs(adr): name journal schema reset decision`

解决方案或完成结果：

删除旧平铺 Skill journal 的兼容类型、探测、归一化和成功恢复路径，将带显式 `filesystem`/`agent-cli` driver 及互斥类型载荷的 shared envelope 定为唯一 schema v1。Skill Install、Skill Uninstall、MCP Install/Update 和 MCP Uninstall 四类 producer 均只写 schema v1。解码继续严格拒绝未知字段、尾随 JSON、未知版本、非法 driver/payload/phase 等无效输入，所有格式和语义错误均保持包装 `ErrInvalidJournal`；`Inspect` 与 `Recover` fail closed，并原样保留无效活动 journal。ADR-011 记录该发布前 schema reset，当前 Feature、C4、BUILD 和项目概览已同步引用。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./internal/transaction ./internal/lifecycle`、`git diff --check`、`go test -count=1 ./...`、`go test -race -count=1 ./internal/transaction ./internal/lifecycle`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`! rg -n 'legacyFilesystemJournal|legacyJournalJSON|journal_v2|SchemaVersion:\s*2|SchemaVersion != 2' internal --glob '*.go'`、`rg -n 'SchemaVersion:\s*1|SchemaVersion != 1' internal/transaction internal/lifecycle --glob '*.go'` 与 `git status --short` 均以退出码 0 完成。MCP 集成测试把 `codex`、`claude`、`hermes` fake CLI 建在 `t.TempDir()/fake-bin`，隔离 Agent 配置目录，并逐条断言实际执行路径没有逃逸 fake-bin。

发布前遗留的旧开发 journal 需要开发者检查后人工处理；本变更不迁移、不改写也不删除这些文件。

## 2026-08-12

分类：新增功能

用户需求：

在 APM 现有 Skill 生命周期基础上增加 MCP 适配：Cloud 按 MCP 名称和精确版本返回 stdio 启动命令或 HTTP URL；APM 不为 MCP 使用 Cache/Staging，通过 Codex、Claude Code、Hermes Agent CLI 完成用户级安装、升级、卸载和状态检查；manifest 只记录由 APM 安装的 MCP 条目，Skill/MCP 共用可串行恢复的唯一 journal，并确保自动化测试不影响真实 Agent 环境。

相关提交：

- `b6066e8 docs(mcp): define adapters and recoverable transactions`
- `2ed36a8 docs(mcp): plan Agent CLI lifecycle implementation`
- `525b002 feat(mcp): add versioned Cloud definitions`
- `97cd079 feat(mcp): add safe Agent CLI runner`
- `1070b1b feat(mcp): add Agent-specific adapters`
- `8d12b2c refactor(transaction): add typed journal drivers`
- `8465ff8 feat(mcp): add recoverable install transactions`
- `5007a57 feat(mcp): add uninstall and cross-type recovery`
- `afca331 feat(mcp): wire install lifecycle`
- `e3fcff4 feat(mcp): wire uninstall lifecycle`
- `cc8ac74 feat(mcp): enable end-to-end CLI lifecycle`
- `848f7d0 fix(mcp): enforce fail-closed adapter boundaries`

解决方案或完成结果：

新增严格互斥的 stdio/HTTP `MCPServer` 领域模型和 Cloud `GetMCP` 精确版本接口；MCP 定义直接从 Cloud 进入 lifecycle，不写 Cache/Staging。Registry 分别提供 Skill 与 MCP Adapter，三个 MCP Adapter 使用 argv 直接调用目标 Agent CLI，其中 Claude Code 固定 user scope，Hermes 使用受控 stdin。CLI runner 动态解析 PATH、限制 30 秒和输出大小；结构、scope、重复项、截断或退出结果不明确时 fail closed。未纳管同名 MCP 返回稳定的 `ErrMCPConflict`，同时保留旧 `ErrTargetConflict` 的 `errors.Is` 兼容性；Plugin 明确返回不支持，不进入 Skill 文件系统事务。

manifest 的 `mcps` 分组只保存 APM 成功安装的精确 `PowerRef`；卸载和 stale 修复只删除对应 Type/Name 条目，不删除整个清单。唯一 journal 升级为 schema v2 信封，由 `filesystem` 与 `agent-cli` driver 分别承载 Skill 和 MCP 状态，并继续读取 schema v1 Skill journal。每条需要恢复的后续命令按 journal driver 串行恢复；MCP 更新采用先移除旧定义、再添加新定义的可恢复阶段，已应用但报错时保留确定的恢复方向。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./internal/adapter ./internal/cloud ./internal/lifecycle ./internal/transaction`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 与 `git diff --check` 均以退出码 0 完成。集成测试把 `PATH` 指向临时 fake-bin，并把 `APM_HOME`、`XDG_CACHE_HOME`、`CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`HERMES_HOME` 全部隔离到 `t.TempDir()`，还断言所有已执行程序均来自 fake-bin；未调用 `/home/liangff/.local/bin/hermes` 或其他真实 Agent CLI，也未改动真实 Agent 配置。

当前限制：Plugin Adapter 不在本功能范围内；MCP 正常写操作按设计不追加 list 验证；认证、环境变量、preset 和自定义工具筛选尚不支持；Agent CLI 或其他进程绕过 APM 并发修改配置不在一致性保证范围内。

## 2026-08-12

分类：功能变更

用户需求：

扩充 `internal/cloud/resources/skills/`，提供包含 `foo` 在内的 10 个简单 Skill，并覆盖多版本测试场景。

相关提交：

- `87e47c7 test(cloud): expand bundled skill fixtures`

解决方案或完成结果：

新增 `brief-status`、`checklist-first`、`code-comment-cn`、`commit-message-helper`、`error-context`、`meeting-notes`、`plain-language`、`test-case-namer` 与 `worklog-entry`，并为 `foo-before-summary`、`commit-message-helper` 和 `test-case-namer` 提供 `v0.0.1`、`v0.1.0` 两个版本。资源总计为 10 个 Skill 名称和 13 个版本目录；每个版本均含带名称和版本 frontmatter 的 `SKILL.md` 与 `skill-card.md`。Cloud fixture 测试断言完整的稳定排序资源矩阵及两个文件的存在性；依赖默认最新版本的 CLI 集成测试已对齐 `foo-before-summary@v0.1.0`，显式 `v0.0.1` 获取测试仍保留。

验证结果或限制：

`go test -count=1 ./cmd/apm ./internal/cloud`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`test -z "$(gofmt -l cmd internal)"` 与 `git diff --check` 均以退出码 0 完成。

## 2026-08-12

分类：新增功能

用户需求：

完成 Lifecycle Uninstall，并明确本地清单解析、同步 Warn、批量 fail-fast 和结构化结果的边界；在完成实现与文档对齐后执行全量、竞态、静态和隔离 CLI 黑盒验收。

相关提交：

- `2f9dd8b docs(uninstall): define lifecycle design`
- `1d73ed4 docs(uninstall): add implementation plan`
- `afbca08 refactor(transaction): report missing uninstall targets`
- `2201a27 docs(changelog): record uninstall result contract`
- `4215714 docs(changelog): defer uninstall entry`
- `9bc9d1f feat(cli): define uninstall result contract`
- `8248e9c feat(lifecycle): resolve uninstall manifests`
- `e0b6f1c feat(lifecycle): orchestrate uninstall transactions`
- `62402ce test(lifecycle): protect uninstall error boundaries`
- `e59ed19 feat(cli): execute and render uninstall lifecycle`
- `f968264 feat(apm): wire default uninstall lifecycle`
- `cfe9046 docs(uninstall): record lifecycle implementation`
- `1c15d93 docs(uninstall): align remaining architecture boundaries`
- `0574c05 docs(uninstall): record completed feature`
- `e4a285e docs(uninstall): correct agent-only outcomes`

解决方案或完成结果：

Transaction 继续独占 target 检查、journal/quarantine 和 manifest 变更，并以 `UninstallResult{TargetMissing bool}` 把已经确认的 target 缺失事实返回给 lifecycle，即使后续同时返回 error 也不丢失该事实；Adapter 只提供 `TargetPath`，不拥有 Uninstall。Uninstall lifecycle 在一次 Acquire/Release 锁区间内依次完成 Recover、同步恢复 Warn、RefreshInstalled、逐 Agent 清单读取与本地 Type/Version 解析、Adapter Resolve 和串行 Transaction；省略 `--type` 时，每个 Agent 独立按自己的清单消歧，因此同一名称可以在不同 Agent 解析为不同 Type/Version。清单无匹配记录时同步输出 `power is not installed` Warn、生成 Agent-only `unexecuted` 并继续；清单存在但 transaction 确认 target 缺失时同步输出 `installed power target is missing` Warn，成功修复清单后仍生成完整引用 `completed`。首个 Config/解析/Resolve/Transaction 业务错误把当前 Agent 标为 `failed`、后续 Agent 标为 `unexecuted` 并停止访问其依赖。只有成功 Release 后，CLI 才渲染完整形式 `<agent>/<type>/<name>@<version> <outcome>` 或 Agent-only 形式 `<agent> <failed|unexecuted>`；成功 Release 后的 batch error 先渲染再失败，其他 fatal/Release error 抑制业务 stdout。普通 Error 与 Usage 仅由 CLI 统一写入 stderr，transaction、lifecycle 和 adapter 不直接记录 Error。

Config Repository 的既有契约允许同一 Type/Name 重复并保留清单顺序，`Find` 返回第一条，`Remove` 清除全部。本轮修复使 Uninstall 在显式或省略 `--type` 时都确定性选择目标类别的第一条同名记录；省略 Type 的歧义按匹配到的不同类别判断，错误中的类别标签去重并稳定排序。真实 JSONRepository 到默认 lifecycle/transaction 的集成覆盖验证了首条引用进入结果，且成功 transaction 会清除该 Type/Name 的全部重复记录。本轮生产代码只修改 Uninstall resolver，Search、List、Install 生产行为未变。

验证结果或限制：

以下命令均以退出码 0 完成：`cp go.mod /tmp/apm-uninstall-go.mod.before`、`cp go.sum /tmp/apm-uninstall-go.sum.before`、`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`go mod tidy`、`cmp go.mod /tmp/apm-uninstall-go.mod.before`、`cmp go.sum /tmp/apm-uninstall-go.sum.before`、`git diff --check`；tidy 后两个 module 文件均保持不变。静态边界扫描 `! rg -n 'slog\.Error|fmt\.Print|log\.Print' internal/lifecycle internal/transaction internal/adapter --glob '*.go'` 与 `! rg -n 'func \([^)]*\) Uninstall\(' internal/adapter --glob '*.go'` 无匹配并成功，正向扫描 `rg -n 'Uninstall\(context\.Context, UninstallRequest\) \(UninstallResult, error\)' internal/transaction/manager.go` 和 `rg -n 'newUninstallLifecycle|NewUninstall|UninstallLifecycleFactory' cmd internal --glob '*.go'` 均成功显示预期契约与独立接线。

隔离 CLI 黑盒使用 `accept_root=$(mktemp -d)`、显式 `accept_bin`/`accept_home`/`accept_cache`，执行 `go build -o "$accept_bin" ./cmd/apm`；随后在 `APM_HOME="$accept_home" XDG_CACHE_HOME="$accept_cache" CODEX_HOME= CLAUDE_CONFIG_DIR= HERMES_HOME=` 下依次执行 `"$accept_bin" install foo-before-summary --agent codex --type skill`、`"$accept_bin" uninstall foo-before-summary --agent codex`、再次执行同一 Uninstall，以及 `"$accept_bin" uninstall foo-before-summary=v0.0.1 --agent codex`。安装 stdout 精确为 `codex/skill/foo-before-summary@v0.0.1 installed\n`、stderr 为空；首次卸载 stdout 精确为 `codex/skill/foo-before-summary@v0.0.1 completed\n`、stderr 为空，target 与对应 manifest 项均消失且无 active journal。第二次卸载状态为 0，stdout 精确为 `codex unexecuted\n`，stderr 精确一行且恰有一个 `level=WARN`、`msg="power is not installed"`、`operation=uninstall`、`agent=codex` 和 `power=foo-before-summary`。版本卸载状态为 1、stdout 为空，stderr 恰有一个 `level=ERROR`、`uninstall does not accept a version` 和 `Usage:`。

本轮回归先观察到 `go test -count=1 ./internal/lifecycle -run '^TestResolveUninstallPower'` 因错误的 `[skill skill]`/`[plugin skill skill]` 类别列表失败，且真实 Repository 集成测试因同类别重复被误判歧义而失败；最小 resolver 修复后两者通过。随后 `go test -count=1 ./internal/lifecycle ./cmd/apm`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`test -z "$(gofmt -l cmd internal)"` 与 `git diff --check` 均以退出码 0 完成。

当前仅提供内置 Agent 的 Skill Adapter，Plugin 与 MCP 尚无可执行 Adapter；外部进程在 APM 文件系统操作期间造成的竞态仍不在 APM 的一致性保证范围内。

## 2026-08-11

分类：新增功能

用户需求：

实现并交付完整的 Install lifecycle，并对齐 ADR、C4、Feature、BUILD 与 Changelog；在完成实现后执行全量、竞态、静态和隔离 CLI 黑盒验收。

相关提交：

- `de999d7 docs(install): define lifecycle design`
- `02218df docs(install): add implementation plan`
- `7cf3986 refactor(cache): return staging root from fetch`
- `f26e45b feat(version): select exact or latest power version`
- `466f1f7 fix(transaction): remove stale install manifest entries`
- `0e7de3c feat(cli): define install result contract`
- `6b0c53f feat(lifecycle): resolve install power references`
- `0853899 fix(lifecycle): trust canonical inferred power types`
- `dd426d7 feat(lifecycle): orchestrate successful installs`
- `1c33afc feat(lifecycle): enforce install fail-fast results`
- `04da8d3 feat(cli): execute and render install lifecycle`
- `07dd3bd feat(apm): wire default install lifecycle`
- `4d7932e test(apm): verify unchanged install cleanup`
- `feb51b5 test: cover canonical cache and stale repair`
- `afa8187 docs(install): record lifecycle implementation`
- `c5f718d docs(install): correct lifecycle boundaries`

解决方案或完成结果：

提供独立的 Install CLI 端口、生命周期、批处理结果和 renderer。显式版本只接受规范 `vMAJOR.MINOR.PATCH`，Name 保持区分大小写的精确匹配；跨 Type 同名在版本选择前要求 `--type`。Install 在一次锁内依次完成恢复、刷新、Cloud、Cache 与 serial Transaction；成功 Release 后才渲染。每个结果精确输出 `<agent>/<type>/<name>@<version> <outcome>`，包含 installed、upgraded、downgraded、unchanged，以及 fail-fast 的 failed/unexecuted。旧清单存在而受管 target 缺失时，Transaction 只 Remove 对应条目、清理 canonical staging、返回 `ErrStaleManifest` 并中止，不创建 journal 或调用 Adapter。Cache Fetch 的公共契约为 staging-root 字符串，Adapter 依据 PowerRef 定位叶子内容。Search、List 和 Install 已实现；Uninstall 仍为 parse-only。

验证结果或限制：

以下命令均以退出码 0 完成：`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`go mod tidy`、`cmp go.mod /tmp/apm-go.mod.before`、`cmp go.sum /tmp/apm-go.sum.before`、`git diff --check`，以及 lifecycle/transaction/cache/version 的 Error/Print 静态扫描和 `cache.Content`/`Content.Path`/旧 install 签名扫描。文档扫描 `rg -n 'parse-only|自愈|self-heal|Fetch\(.*Content' BUILD.md ivory-tower --glob '*.md' --glob '*.mmd' --glob '*.dsl'` 也以退出码 0 完成：逐行确认当前文档的 parse-only 仅指 Uninstall，Feature 008/009 与计划中的旧 Install 边界均显式标为历史或 superseded，Install 自愈与 `Fetch(...Content...)` 只出现在明确 superseded 的历史文本，剩余自愈仅描述保持不变的 Uninstall target-missing 语义。隔离 CLI 黑盒验收的首次调用 stdout 精确为 `codex/skill/foo-before-summary@v0.0.1 installed`、stderr 为空；第二次规范版本调用 stdout 精确为 `codex/skill/foo-before-summary@v0.0.1 unchanged`、stderr 为空，并确认目标 `SKILL.md`、manifest 存在且无 active journal。无效 `0.0.1` 以状态 1 退出、stdout 为空、stderr 恰有一条 `level=ERROR`，且包含 `must be vMAJOR.MINOR.PATCH` 和 `Usage:`。

## 2026-08-11

分类：新增功能

用户需求：

实现并交付独立的 List lifecycle：默认 Cloud List 无锁列出全部版本，installed List 在一个锁区间内恢复、刷新已安装 Agent 并读取清单；同步 ADR、C4、概览、历史 feature 文档和构建指南，并完成全量与黑盒验收。

相关提交：

- `316a367 docs(list): define lifecycle design`
- `62d29ae docs(list): add implementation plan`
- `e37b913 feat(config): require canonical power versions`
- `4f9010f fix(adapter): order installed agent refresh`
- `15f27c4 test(adapter): protect refresh ordering regression`
- `1143e1f feat(list): define CLI result contract`
- `e66f484 docs(list): clarify partial error contract`
- `36afaf8 feat(list): filter and sort power results`
- `7f665ea feat(list): implement lifecycle orchestration`
- `2c51cb4 feat(list): render lifecycle results`
- `c53ccf1 docs(list): record lifecycle implementation`
- `e715ea6 docs(list): correct lifecycle documentation`
- `a3bf252 fix(lifecycle): reject typed nil dependencies`

解决方案或完成结果：

默认 Cloud List 不获取锁、不调用 Recover，直接查询 Cloud、计算并渲染全部版本；installed List 在同一把锁内依次执行 Recover、实时 Warn、Registry 刷新和全部 Config reads，只有成功 Release 后才过滤、排序和渲染。Cloud 精确输出 `<type>/<name>@<version>`，installed 始终输出 `<agent>/<type>/<name>@<version>`。`--agent=all` 的单 Agent 读取失败会继续读取；只有成功 Release 后的 `PartialListError` 可先输出成功记录再非零退出，Release 或其他 fatal error 不输出业务行。Registry 对注册 Agent 副本按字典序 Detect 并返回同序快照。Config 的完整读写只接受 `vMAJOR.MINOR.PATCH`，读取非法历史清单会失败且不迁移或清理。Search 与 List 采用独立端口，并在 constructor 和 CLI factory 边界拒绝 typed-nil interface 依赖或服务，保持原有上下文错误且避免 panic；install/uninstall 继续仅解析参数。ADR-002/003/004/007（并同步 ADR-009 的历史接线说明）、C4、项目概览、Feature 005/008/009 与 BUILD 已对齐。

验证结果或限制：

`go mod tidy` 后 `go.mod`/`go.sum` 无 diff；`test -z "$(gofmt -l cmd internal)"`、typed-nil lifecycle/CLI focused tests、`go test -count=1 ./...`、`go test -race -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、`git diff --check`、aggregate/Search no-op 扫描和 lifecycle/config/adapter 的直接 Error/Print 静态扫描均以退出码 0 完成。typed-nil constructor 测试覆盖 NewSearch 三类与 NewList 五类依赖的精确上下文错误；CLI 测试覆盖 factory 返回 typed-nil Search/List service 时不 panic、stdout 为空且 stderr 恰有一条 Error 和 Usage。隔离目录黑盒验收中，`list --type skill` 的 stdout 为 `skill/foo-before-summary@v0.0.1` 且 stderr 为空；无效 `list --agent all` 以状态 1 退出、stdout 为空、stderr 恰有一条 `level=ERROR` 并包含 `--agent requires --installed` 与 `Usage:`；创建空 Codex 根目录后 `list --installed --agent codex` 的 stdout/stderr 均为空。历史 feature/plan 中保留的旧边界均已标注为历史或 superseded；install/uninstall lifecycle 尚未实现。

## 2026-08-11

分类：新增功能

用户需求：

实现并交付 `search <keyword>` 的完整 lifecycle：在查询 Cloud 前完成上次 transaction 恢复，按名称查询并渲染每种 Power 的最新版本；同时同步 ADR、架构图、概览和构建文档，并保持尚未实现的命令边界清晰。

相关提交：

- `86a4eb5 docs(search): define lifecycle design`
- `1520d16 docs(search): add implementation plan`
- `53d1157 test(cloud): align v-prefixed fixture`
- `1e7101a feat(search): select latest matching powers`
- `0651245 feat(search): recover before cloud query`
- `e20277c docs: record search lifecycle recovery`
- `86deaf9 fix(search): assert recovery warn before unlock`
- `8117eac feat(search): render lifecycle results`
- `afe0f7a docs(search): revise lifecycle lock boundary`
- `1a440e2 docs(search): clarify recovery concurrency scope`
- `fcf11d4 test(search): strengthen lifecycle regressions`

解决方案或完成结果：

`search` 在全局锁内执行 `Recover`；恢复成功会实时输出 Warn，且该 Warn 发生在 Release 之前。恢复完成后先释放锁，再调用 Cloud，随后才执行过滤、最新 SemVer 选择、稳定排序和精确 `<type>/<name>@<version>` 渲染。恢复错误仍会尝试 Release，并仅由 CLI 外层记录一次。查询只对 `Name` 进行 Unicode 大小写无关匹配；提供 `--type` 时只与 `PowerRef.Type` 精确相等的引用匹配。同类型同名的多个版本选择最大 SemVer；最终结果按原始 `Type`、再按原始 `Name` 的稳定字典序升序排列。lifecycle 返回 `SearchResult` 而不接收 Renderer，CLI 负责渲染；`--agent` 未接入 search，仍作为未知 flag 拒绝。ADR-002、修订后的 ADR-003、ADR-009、C4/流程图、项目概览和 `BUILD.md` 已对齐。`list`、`install`、`uninstall` 的 lifecycle 接口和命令 wiring 仍未连接。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、两次 `git diff --check`、`go mod tidy`、`go test -count=1 ./internal/cloud ./internal/lifecycle ./internal/cli ./cmd/apm`、`go test -count=1 ./...`、`go vet ./...` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 完成，且 tidy 未产生模块文件变更。构建后的隔离验收中，`search summary` 的 stdout 精确为 `skill/foo-before-summary@v0.0.1` 且 stderr 为空；`search summary --agent=all` 非零退出，stdout 为空，stderr 恰有一条 `level=ERROR`、包含 `unknown flag: --agent` 和 `Usage:`。生产代码直接打印/独立 logger 扫描及 lifecycle 的 `slog.Error` 扫描均无匹配。

## 2026-08-10

分类：新增功能

用户需求：

实现集中式 lifecycle transaction 接口，使 APM 任意后续运行都能恢复上次失败的单 Agent 安装或卸载，而无需再次执行相同目标；同时明确 transaction、lifecycle 与 Adapter 的交互时序和实现边界，并为重要接口添加简要中文用途注释。

相关提交：

- `73487d5 docs(transaction): define centralized recovery design`
- `89464d8 docs(transaction): add implementation plan`
- `d8692a4 refactor(adapter): report publication outcome`
- `764c81a refactor(filesystem): share physical path safety`
- `aa760ec feat(transaction): define recovery contracts`
- `64da617 feat(transaction): persist atomic journal`
- `f17ed8d feat(transaction): guard transaction artifacts`
- `5583593 feat(transaction): implement install transaction`
- `0b141b1 feat(transaction): implement uninstall transaction`
- `0c1c8a0 feat(transaction): recover centralized transactions`
- `1fa2630 fix(transaction): reject short journal writes`

解决方案或完成结果：

新增唯一活动的集中 journal、严格 schema 校验和原子持久化，并在目标同文件系统中管理随机事务 ID 隔离的 backup/quarantine。Adapter 以 `InstallResult.Published` 明确发布点；transaction 在目标变更前记录意图、在提交点后幂等写入清单，并处理首次安装、升级、目标漂移、卸载 quarantine、提交前回滚及提交后续提交。`NewManager` 可在新进程中仅依赖集中 journal 和清单 Repository，按记录的精确路径恢复任意上次事务；损坏 journal、矛盾路径、符号链接事务目录和物理 staging/target 重叠均 fail closed 并保留证据。lifecycle 仅新增 `TransactionManager` 高层依赖边界，本 feature 不接入 lifecycle 或 CLI 命令编排。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`git diff --check`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、无缓存聚焦测试 `go test -count=1 ./internal/adapter ./internal/utils/filesystem ./internal/paths ./internal/transaction ./internal/lifecycle` 和全量测试 `go test -count=1 ./...` 均以退出码 0 完成。恢复测试覆盖全新 Manager、安装与卸载方向表、损坏或矛盾状态、取消、部分动作结果、符号链接目录，以及 journal 删除失败后的重复恢复。调用方仍须持有全局 APM 锁；实际 lifecycle/CLI 恢复预检、Cloud/Cache、版本选择、多 Agent 调度、Plugin 和 MCP 不在本 feature 范围内。

## 2026-08-07

分类：新增功能

用户需求：

为 Codex、Claude Code 和 Hermes Agent 实现全局 Skill Adapter：定义稳定的 Agent 快照和精简 Adapter 契约，解析 Agent 路径和安装检测，校验 `SKILL.md` frontmatter，并安全地从公共 staging 复制、原子发布与清理 Skill。

相关提交：

- `543ad83 docs: design agent skill adapters`
- `328c996 docs: plan agent skill adapters`
- `8f3085e docs: enforce pre-rename conflict check`
- `c578390 feat(domain): define supported agent snapshots`
- `13058ae docs: record agent snapshot domain changes`
- `b8e53a9 refactor(adapter): define skill installation contract`
- `43efb07 feat(adapter): resolve agent skill targets`
- `bfa3da9 feat(adapter): validate skill frontmatter`
- `b8c0d46 feat(adapter): install skills from staging`
- `cca68f7 fix(adapter): validate staging root`
- `9b515c5 fix(adapter): preserve atomic install failures`
- `a714018 fix(adapter): add install error context`
- `de2fcb9 test(adapter): cover install error phases`
- `e9e849c feat(adapter): register built-in skill adapters`
- `d8e787e fix(adapter): reject overlapping install roots`

解决方案或完成结果：

新增 `codex`、`claude-code` 和 `hermes-agent` 三个稳定 Agent ID；Registry 只解析这三者的 Skill Adapter，并以固定顺序刷新不可变快照。三个 Agent 共享一条全局、仅复制的安装管线：先按 Agent 根目录解析目标，拒绝任意未纳管的既有目标，再校验公共 staging 中 `SKILL.md` 的首字节 YAML frontmatter（精确名称、非空字符串 `name`/`description`，允许未知字段以及 LF/CRLF），复制完整目录到同一 Skill 根内的隐藏临时目录，并以 rename 原子发布；不创建指向 staging 的 symlink。安装在注册 staging 清理和创建目标临时目录前解析 staging、Agent 根与 Skill 根的物理路径，以分隔符感知的包含检查拒绝 Skill 根等于或位于 staging 下的布局；重叠错误不会复制、发布或清理重叠树。物理根不重叠后，有效 staging 在成功或失败时都会清理，临时目录和 staging 清理错误保留路径、阶段、Agent/Power 上下文并与主错误合并。

验证结果或限制：

无缓存聚焦测试 `go test ./internal/adapter ./internal/domain ./internal/lifecycle -count=1 -v` 通过，Adapter 与 domain 测试无 SKIP；lifecycle 以 `[no test files]` 编译通过。重叠回归覆盖物理子目录、staging/Agent 根/Skill 根 symlink、递归复制输出、既有目标保留和相邻前缀非重叠安装。`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。Adapter 直接输出/非 Debug 日志扫描无匹配；正确限定在 `internal/adapter` 的旧符号扫描未发现 `InstallPath`、`domain.Power` 或 Adapter `Uninstall`，`TargetPath`、`DetectAgent` 和 `Install` 新契约均可见。APM 全局锁仅串行 APM 自身；外部进程仍可在物理根检查后改变 symlink，或在发布前第二次目标检查与 rename 之间创建目标，因而不提供无竞态的 no-replace 保证。

## 2026-08-07

分类：新增功能

用户需求：

定义 Agent Power 安装清单格式并实现 config 接口，使 lifecycle 后续只调用无状态 Repository 的 List、Find、Set、Remove。

相关提交：

- `4ca8df6 docs: design agent power config repository`
- `27d4143 docs: plan agent power config repository`
- `27d65b6 docs: keep config log test behavior-focused`
- `6b5daff feat(config): add power groups and manifest paths`
- `546d9ad feat(config): add manifest list and find`
- `46a98db docs: align config permission test with spec`
- `1845e71 feat(config): atomically set and remove powers`
- `f2e652c refactor(config): replace manifest store with repository`
- `97d80d3 docs: fix config CLI smoke command`
- `a9df144 fix(config): preserve repository error context`

解决方案或完成结果：

每个 Agent 使用固定 skills/plugins/mcps 分组 JSON；清单条目统一为版本已确定的 PowerRef。JSONRepository 在四个业务接口内部封装读取和原子读改写，不保留跨调用状态，也不获取全局锁；缺失文件、幂等 Set/Remove、重复项归并和输入错误按 Feature 005 规格处理。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。无缓存聚焦测试 `go test ./internal/config ./internal/paths ./internal/domain -count=1 -v` 全部通过且无 SKIP；旧 config 符号和直接输出/独立 logger 静态扫描均无匹配，JSONRepository 仅保留 Debug 调用。CLI help 包含 search/list/install/uninstall，提供必填 `--agent all` 的普通 search 无业务输出。lifecycle/CLI 尚未串联，Repository 写入依赖调用方持有全局 APM 锁。

## 2026-08-07

分类：功能变更

用户需求：

参考 Helm，把 APM 改为预解析 verbose、安装全局 slog，并为四级日志配置独立 Handler。

相关提交：

- `6fa452d docs: design global slog logging`
- `4433d3a docs: plan global slog logging`
- `cf5d1a1 refactor(logging): add global level routing`
- `30607ae refactor(cli): install global slog before execution`
- `b615d3c docs: record global slog CLI migration`
- `5479fc0 fix(docs): defer global slog changelog entry`
- `efc073c test(logging): strengthen handler acceptance coverage`

解决方案或完成结果：

删除自定义 Logger 门面；Debug 由 verbose Handler 控制；Info 输出到 stdout，Warn/Error 输出到 stderr；错误只在 CLI 外层记录一次；Cobra help/usage 保持原生。

验证结果或限制：

直接打印及独立 logger 静态扫描均无匹配。日志和 CLI 聚焦测试通过且无跳过测试；`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。不实现自定义格式，也不支持并发使用不同配置的 CLI Entry。

## 2026-08-06

分类：新增功能

用户需求：

实现本地 Cloud/Cache 能力，并以端到端测试验证本地资源目录到版本化缓存、再到无版本 staging 的完整链路。

相关提交：

- `56d9c84 docs: specify local cloud cache behavior`
- `ebe9832 docs: clarify cache fetch cleanup order`
- `323cf4b docs: plan local cloud cache implementation`
- `8ef8092 refactor(domain): consolidate power version metadata`
- `0ec72a7 feat(paths): add cache and staging paths`
- `bdb37ee feat(utils): add recursive directory copy`
- `cda38e9 fix(utils): populate read-only directory copies`
- `6a035be feat(cloud): add local resource client`
- `6214b92 fix(cloud): preserve local client contract`
- `2e24422 feat(cache): add versioned filesystem cache`
- `6a9ebc4 test: cover local cloud cache flow`
- `c07d8ac test: assert versionless local staging`
- `d257a12 fix(cloud): validate skill marker entries`

解决方案或完成结果：

提供本地 Skill 资源目录，按版本原子写入缓存，并将命中的版本复制为无版本 staging；空版本会返回防御性错误。`SKILL.md` marker 按目录项本身检查且仅接受普通文件，List 会跳过 symlink marker，Get 会将其映射为 `ErrNotFound`，marker 的非 `ENOENT` 文件系统错误则保留路径和底层错误。功能边界仅覆盖本地 Cloud/Cache，不包含 CLI、lifecycle、Adapter 或 HTTP。

验证结果或限制：

`go test ./internal/cloud -run TestLocalCloudAndCacheFetchGetFetch -count=1` 通过，验证 `Fetch → ErrMiss → Get → Fetch`、缓存与 staging 文件以及 HOME 未被写入。Cloud 回归测试覆盖 symlink marker 和非 `ENOENT` marker 错误；Cache 回归测试覆盖空版本保留 staging、miss/非 Skill 清理 staging 与临时 staging，以及缓存源非 `ENOENT` 错误保留上下文且不映射为 `ErrMiss`。`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均通过。

## 2026-08-06

分类：缺陷修复

用户需求：

修复缓存目录环境变量的覆盖顺序，使其符合 `XDG_CACHE_HOME > APM_HOME > 系统默认`。

相关提交：

- `2f46cba fix(paths): honor XDG cache precedence`
- `7cdf98f test(paths): use system cache directory expectation`

解决方案或完成结果：

`CachePath()` 现在优先使用非空 `$XDG_CACHE_HOME/apm`，其次使用非空 `$APM_HOME/.cache/apm`，两者均为空或未设置时使用系统默认缓存目录下的 `apm`，并取代上一条记录中“`APM_HOME` 覆盖 XDG 缓存”的旧语义。`APM_HOME` 对状态、Agent、Power 和默认 lock 等 HOME 派生路径的作用保持不变。ADR-005、Project Overview 和 feature 文档已同步新的优先级。

验证结果或限制：

- 单元测试分别覆盖 XDG 覆盖 APM、XDG 为空时回退 APM，以及两者均为空时使用系统默认缓存目录。
- `test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。

## 2026-08-06

分类：新增功能

用户需求：

增加仅供测试使用的 `APM_HOME`，使 APM 可在临时目录模拟 Agent 和 Power 状态而不影响用户目录。

相关提交：

- `3320f3a docs: design APM_HOME test isolation`
- `37a7181 docs(feature): plan APM_HOME test isolation`
- `b46ec74 docs(plan): preserve XDG cache fallback`
- `fa73e56 feat(paths): isolate test filesystem with APM_HOME`
- `67e9f3d docs: record APM home path isolation`
- `5e4fdfd fix: defer APM home changelog entry`

解决方案或完成结果：

当 `APM_HOME` 为非空值时，APM 的有效 HOME 为该目录；状态目录为 `$APM_HOME/.apm`，缓存目录为 `$APM_HOME/.cache/apm`，默认全局锁也随之隔离在 `$APM_HOME/.apm/apm.lock`。未设置或设置为空时，HOME 仍使用系统默认解析，缓存仍遵循 XDG 缓存目录覆盖。

验证结果或限制：

- `rg -n 'os\.(UserHomeDir|UserCacheDir|Getenv|LookupEnv)\(' internal --glob '*.go'` 确认与该功能相关的生产 HOME、缓存和环境访问集中于 `internal/paths/paths.go`；测试辅助环境访问可见于 `paths_test.go`。
- `test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成；测试包全部报告 `ok`，无测试包报告 `[no test files]`。
- 当前仅集成路径和既有全局锁；Agent 检测、安装/卸载、配置、缓存写入及事务恢复仍由各自功能负责。

## 2026-08-05

分类：新增功能

用户需求：

建立 ADR-001 整体框架，实现只解析命令的 CLI、logging 和全局 lock

相关提交：

- `baea8bc docs(feature): define CLI foundation scope`
- `97a738b chore: initialize APM Go module`
- `c5aeac3 docs: record module initialization`
- `64facaf docs: defer changelog entry`
- `c563618 feat: add unified logging`
- `bbe794c feat: add parse-only CLI`
- `cb4b008 fix: route CLI help and errors correctly`
- `6d4902f feat: add global APM file lock`
- `7eaa75c chore: scaffold lifecycle transactions`
- `ec3ec26 chore: scaffold APM components`
- `609e72b fix: reset CLI verbose logging state`

解决方案或完成结果：

基于 ADR-001 建立 Go 模块和领域、适配器、缓存、云端、配置、生命周期、路径、事务、版本等基础包；实现只解析及校验参数的 Cobra CLI、按 verbose 分流的统一结构化日志，以及进程间全局文件锁。CLI 提供 install、uninstall、search、list 四个业务命令，为后续生命周期实现保留稳定边界。

验证结果或限制：

- `test -z "$(gofmt -l cmd internal)"`、`go test ./...`、`go vet ./...`、`go build -o ./apm ./cmd/apm` 均以退出码 0 完成；测试覆盖的包全部通过，其余包报告 `[no test files]`。
- `./apm --help` 展示 install、uninstall、search、list；普通 search 静默并以退出码 0 完成；verbose search 仅向 stderr 输出解析诊断并以退出码 0 完成；无效 type 仅向 stderr 输出错误和 usage，并以退出码 1 结束。
- Adapter 目录没有子目录，源码扫描未发现 TODO、TBD 或 panic；cli、logging、lock、domain 四个包均显示中文 GoDoc；`git diff --check` 以退出码 0 完成。
- 当前阶段只完成命令解析、校验和基础组件脚手架，尚未执行 install、uninstall、search、list 的实际业务操作。

# Changelog

记录约定：承载或维护当前 Changelog 条目的当前提交不列入自身的“相关提交”，避免提交哈希自引用。

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

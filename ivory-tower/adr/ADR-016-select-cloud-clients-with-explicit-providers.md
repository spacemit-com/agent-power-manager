# Select Cloud Clients with Explicit Providers

- Status：Accepted
- Date：2026-08-21
- Decision Makers：APM maintainers

## Context

APM 当前由 `cmd/apm` 硬编码创建 `internal/cloud.LocalClient`。Local Client 同时读取仓库 fixture、发现资源并直接写入 Skill/Agent 全局 cache；Plugin 下载却写调用方目录，MCP 返回领域对象。该边界既不能对接 APC，也使测试 provider、cache 发布和网络 transport 相互耦合。

APM 仍要求交付单个静态二进制，并需要在开发阶段使用本地 canonical fixture、生产阶段使用 Remote APC。运行时加载 Go `.so` 会引入 ABI、平台和部署约束；Remote 失败后自动回退 Local 则会把测试资源误描述为当前云端状态。

本决策关联 [Feature 020](../features/020-cloud-client-providers/spec.md)。

## Decision

`internal/cloud` 只定义由 `Catalog`、`SkillSource`、`PluginSource`、`MCPSource` 与 `AgentSource` 组合的虚拟 `Client` 端口，以及显式 `ProviderRegistry`。Local 与 Remote 位于独立子包，完整实现同一端口；它们作为静态链接的 provider 插件存在，不使用动态加载、全局 `init()` 或隐式注册。

composition root 显式注册 `local`、`remote` factory，并通过运行配置选择：

- `APM_CLOUD_PROVIDER` 默认 `remote`；
- `APC_BASE_URL` 覆盖代码默认值 `http://127.0.0.1:8080`；
- 非 loopback Remote 地址必须使用 HTTPS；
- `APM_CLOUD_LOCAL_ROOT` 是 Local provider 必填的绝对资源根；
- 配置、网络或协议失败不自动回退另一 provider。

Cloud provider 只负责发现、解析版本、获取、完整性验证和把内容写入调用方临时目标。它不解析 APM cache/staging 路径，不写 manifest/journal，也不发布 Agent 目标。Skill/Agent 的 cache 原子发布归 `internal/cache`，Plugin artifact 归 `internal/pluginresource`，MCP 继续直接返回领域对象。

所有文件与网络方法接收 `context.Context`。Local 与 Remote 共享错误分类和 provider contract tests，但各自保留适合来源的实现：Local 校验并复制物理 fixture；Remote 使用 APC HTTP DTO、分页、精确 metadata、size/MD5 和安全解包。

## Consequences

- 正面影响：lifecycle、cache 与 Plugin Resource 依赖稳定端口，不依赖开发或生产 transport。
- 正面影响：Local/Remote 可运行同一 contract suite，切换发生在唯一 composition root。
- 正面影响：cache 发布职责回到资源所有者，Remote Client 不需要知道用户全局路径。
- 正面影响：未来新增 Power 时可增加一个小型能力接口及两个 provider 实现，不必修改无关 transport。
- 代价与风险：现有 Cloud API、构造函数、测试 fake 和 Skill/Agent cache 流程必须一次性迁移。
- 代价与风险：默认 provider 从硬编码 Local 变为 Remote；APC 尚未部署时默认地址不可用，开发者必须显式选择 Local。
- 代价与风险：两个 provider 都静态链接进二进制，不能独立热更新；这是为可移植和确定性接受的约束。
- 后续动作：Feature 020、Project Overview、BUILD 与 C4 引用本 ADR；行为完成并验证后记录一次 Changelog。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Go `.so` 动态插件 | ABI、平台、构建链和部署约束过重，不符合单静态二进制目标。 |
| 完全通用的 `OpenContent` Blob Client | Local 是目录 fixture、Remote 是 tar/JSON/YAML，统一原始字节会迫使 Local 重打包并把解析职责扩散到 lifecycle。 |
| 保留当前混合接口并新增 RemoteClient | transport 继续拥有全局 cache，四类 Power 方法语义不一致，Local/Remote 难以做真正 contract equivalence。 |
| Remote 失败后回退 Local | 会把开发 fixture 当作当前 Cloud 状态，破坏 list/search/install 的来源真实性。 |
| 通过 build tag 选择 provider | 需要维护多种二进制且无法在同一构建中切换预发布或本地集成环境。 |

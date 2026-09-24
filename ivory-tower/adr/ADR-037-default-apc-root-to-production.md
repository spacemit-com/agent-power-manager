# 默认 APC 根地址切换到生产环境

- Status：Accepted
- Date：2026-09-24
- Decision Makers：APM maintainers

> 局部取代说明：本 ADR 取代 [ADR-026](ADR-026-adapt-apc-client-to-test-environment-behavior.md) 中「Remote 默认根地址为测试环境
> `https://developer.bianbu.xyz`」这一条；ADR-026 的其余适配决策（网关前缀固定拼接、目录解码容忍服务端形状、
> 匿名目录/content 不携带凭据、Submission 使用 `Authorization: Bearer`、跨源 CDN 重定向策略、fail-closed 无降级）
> 全部继续有效。本 ADR 同时更正 [ADR-025](ADR-025-apc-api-client-contract.md)/ADR-026 沿用至今的生产入口主机名。

## Context

APM 此前把默认 Remote 根地址设为测试环境 `https://developer.bianbu.xyz`（ADR-026），公开发布后所有未显式配置
`APC_BASE_URL` 的用户都会连到测试环境。发布准备阶段要求默认指向生产环境。

2026-09-24 对生产环境做了只读探测与一轮完整功能测试，结论如下：

- `spacemit.com` 与 `www.spacemit.com` 均返回 200（`GET /api-server/apc/api/v1/resources/version` 连续 8 次稳定，
  约 0.15s），目录 302 到 `cdn-resource.spacemit.com`，匿名 Submission 返回 401 problem+json `INVALID_API_KEY`。
- 文档早期记载的 `developer.spacemit.com` **不是**本环境 APC 入口：其任意路径（含 `/api-server/apc/...`）均
  `301` 跳转到 `https://www.spacemit.com/community/`。
- 生产证书为公开可信证书（`*.spacemit.com`，Xcc Trust DV SSL CA），客户端无需额外 CA；测试环境是自签 CA，需要
  用户自行装入系统信任库。这是默认切到生产后用户侧的一处简化。
- `APC_BASE_URL` 只接受 host-only 根地址；网关前缀 `/api-server/apc` 由 `remote.Client.endpoint` 拼接。把完整
  Base URL（含 `/api-server/apc/api/v1`）直接配给 `APC_BASE_URL` 会被 base URL 校验拒绝。

## Decision

1. `cmd/apm` 的 `defaultAPCBaseURL` 从 `https://developer.bianbu.xyz` 改为 `https://www.spacemit.com`，即默认
   Remote 指向生产环境，并使用带 `www` 的规范主机名。选择 `www` 而非裸域 `spacemit.com`：两者当前行为一致
  （同一 IP、同一证书 SAN 覆盖两者），取站点自身的规范形式，避免后续裸域跳转策略变化影响 API 调用。

2. 基址语义不变：仍是 host-only 根地址，`/api-server/apc` 网关前缀由客户端固定拼接，wire 路径与
   [ADR-026](ADR-026-adapt-apc-client-to-test-environment-behavior.md) 完全一致；`APC_BASE_URL` 的校验规则
   （绝对、无凭据/query/fragment、根路径、非 loopback 只允许 HTTPS）不变。

3. 测试环境降级为显式配置：`APC_BASE_URL=https://developer.bianbu.xyz` 仍可完整访问测试环境，文档与
   `docs/testing/apm-cli-functional-test.md` 的测试流程保持不变。

4. 认证与凭据边界不变：`APC_API_KEY` 只用于 Submission，匿名目录/content 请求不携带任何凭据；生产环境下使用
   有效 API Key 的 Submission 查询/提交行为与测试环境一致（`Authorization: Bearer`）。

5. `APC-API.md` 同步更正生产入口主机名并升到 v1.3，记录 2026-09-24 的双环境实测与"客户端基址只填主机"约束。

## Consequences

- 正面影响：默认安装面向真实用户的生产资源目录，不再把测试环境当作产品默认；生产证书公开可信，用户无需手动
  安装自签 CA。
- 正面影响：`www` 规范主机与站点一致，且证书 SAN 同时覆盖裸域，任一形式都可用；显式切换测试环境的能力保留。
- 代价与风险：测试环境入口不再是默认值，开发/联调必须显式设置 `APC_BASE_URL`；文档与测试指引若遗漏该变量会
  实际打到生产（只读操作安全，但 `submit` 会向生产提交）。
- 代价与风险：生产与测试环境的目录内容当前一致（同一 `catalog_sha256`，CDN 指向同一文件），默认切换本身不能
  证明两边后端独立；发布后应通过在生产侧单独发布资源再检索来验证。
- 后续动作：发布说明中提示"默认地址已切换到生产，联调请显式设置 `APC_BASE_URL`"；若 APC 侧后续为生产启用
  独立网关鉴权，需重新评估匿名只读接口的可达性。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 保持默认指向测试环境，由用户显式配置生产 | 公开发布后绝大多数用户不会配置 `APC_BASE_URL`，默认即产品行为；测试环境作为默认不符合发布目标。 |
| 默认用裸域 `https://spacemit.com` | 与 `www` 行为一致但非站点规范形式；裸域后续若改为跳转 `www` 会引入一次重定向并可能触发重定向策略歧义。 |
| 默认 `https://developer.spacemit.com`（文档旧值） | 2026-09-24 实测该主机任意路径 301 到社区页，不是 APC 入口，会直接导致客户端解析失败。 |
| 默认值写入完整 Base URL（含 `/api-server/apc/api/v1`） | 与既有 base URL 校验冲突（路径必须为根），且会让网关前缀出现两处定义，破坏单点拼接。 |
| 同时保留测试/生产两个内置地址并按 flag 切换 | 引入第二处环境定义与新的 CLI 面，超出"默认值调整"范围；`APC_BASE_URL` 已能表达该选择。 |

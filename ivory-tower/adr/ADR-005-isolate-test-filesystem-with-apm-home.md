# Architecture Decision Record

- Status：Accepted
- Date：2026-08-06
- Decision Makers：liangjunzhao

## Context

APM 的 Agent 检测、Power 安装与卸载、清单、文件锁、缓存和事务恢复都会读写用户主目录。如果测试直接使用真实 HOME，则无法安全模拟 Agent 已安装状态或覆盖完整的 Power 生命周期，也存在污染用户数据的风险。

本决策补充 [ADR-001](ADR-001-use-go-as-implementation-language.md) 中 `internal/paths` 作为所有本地状态路径唯一来源的边界，并为 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md) 的 Agent 根目录检测提供可隔离的测试文件系统。项目级语义详见 [project-overview.md](../project-overview.md#test-filesystem-isolation)。

## Decision

APM 引入仅供测试使用的 `APM_HOME` 环境变量，并由 `internal/paths` 统一解析有效 HOME 和所有本地文件路径。

- `APM_HOME` 非空时直接作为有效 HOME；未定义或为空时回退到 `os.UserHomeDir()`。
- APM 不对 `APM_HOME` 增加路径合法性、绝对路径、符号链接、权限或逃逸校验；测试负责传入规范目录。
- 隔离模式下，APM 状态位于 `$APM_HOME/.apm`，Agent 根目录和 Power 目标从该有效 HOME 派生。
- 缓存路径的优先级固定为 `XDG_CACHE_HOME > APM_HOME > 系统默认`：非空 `XDG_CACHE_HOME` 使用 `$XDG_CACHE_HOME/apm`；否则非空 `APM_HOME` 使用 `$APM_HOME/.cache/apm`；两者均为空或未设置时，在 `os.UserCacheDir()` 返回的系统默认目录下使用 `apm`。
- APM 不改写进程环境；CLI、Adapter、Config、Lock、Cache 和 Transaction 均不直接读取 HOME 类环境变量，只消费 `internal/paths` 生成的路径。
- 不增加 CLI flag 或 help 文本，也不将 `APM_HOME` 定义为普通用户的数据迁移或多配置目录功能。

## Consequences

- 正面影响：测试可以在临时根目录中构造 Agent 和 APM 状态，安全验证 Power 安装、卸载、锁竞争、配置、事务和恢复；同时保留 XDG 对缓存位置的标准覆盖能力。
- 代价与风险：进程环境是全局状态，修改 `APM_HOME` 或 `XDG_CACHE_HOME` 的测试不能并行；若测试要求缓存也位于 `APM_HOME`，必须清空 `XDG_CACHE_HOME`；`APM_HOME` 仅靠测试规范保证安全，错误值不会被 APM 额外拒绝。
- 后续动作：先实现有效 HOME、APM 状态根和缓存路径，并将现有默认锁接入；后续 Adapter、Config、Cache 和 Transaction feature 必须复用该路径边界。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 在 `main` 中把 `HOME` 改为 `APM_HOME` | 会修改进程全局环境，绕过 `main` 的包测试无法获得相同语义，也不能覆盖已设置的 `XDG_CACHE_HOME`。 |
| 通过完整 `PathLayout` 向所有组件注入路径 | 可测试性更强，但当前大部分业务组件仍为占位，大量改动依赖接口超出所需范围。 |
| 每个组件分别读取 `APM_HOME` | 路径优先级和错误语义容易分叉，违背 `internal/paths` 作为唯一路径来源的 ADR-001 约束。 |

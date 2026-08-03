# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Decision Makers：liangjunzhao

## Context

Agent Power Manager（APM）是一个面向多 Agent 的 Power（Skill、Plugin、MCP 等）管理工具，需要提供 `search`/`list`/`install`/`uninstall` 等 CLI 能力，并满足：

- 生命周期安全：任意执行阶段中断，系统都可以识别并纠正。
- 类 apt 的文件锁机制，不支持并发，通过锁防重入。
- 缓存下载的 Power，维护已安装 Power 记录。
- 可扩展的 Agent Power Adapter，适配不同目标 Agent 的安装位置、注册机制与配置格式。
- 面向 bianbu（含 RISC-V）等多平台目标环境分发。

详见 [project-overview.md](../project-overview.md) 的 Introduction、Scope 与 Architecture Snapshot。待决事项中的「语言」「技术栈」「代码目录结构」由本 ADR 一并决策。

## Decision

APM 使用 **Go** 实现，作为覆盖 CLI、Adapter、生命周期与配置治理的唯一实现语言，交付为单个静态二进制。

技术栈：

| 关注点 | 选型 | 说明 |
| --- | --- | --- |
| CLI 框架 | `spf13/cobra` | `search/list/install/uninstall` 子命令、自动补全、help 生成 |
| 文件锁 | `gofrs/flock` | 基于 `flock(2)`，进程崩溃后内核自动释放，满足防重入 + 可恢复 |
| 安装记录/配置 | 每个 Agent 一份 JSON 清单（`encoding/json`） | 对应「各 Agent 单独维护安装列表」的要求 |
| 云端 API 客户端 | 标准库 `net/http` | 仅需 `list`/`get` 两个接口，不引入重框架 |
| 下载缓存 | 内容寻址目录（按 name@version 或 sha256 分桶） | 遵循 XDG 缓存目录约定，便于去重与清理 |
| 中断恢复 | staging 目录 + 原子 rename + 操作 journal | 启动时检测残留 staging/journal 即可识别并纠正 |
| 日志/输出 | 标准库 `log/slog` | 用户可见输出与内部日志分离，满足失败可打印要求 |
| 测试 | 标准 `testing` + `rogpeppe/go-internal/testscript` | testscript 便于端到端验证 CLI 的装/断/修复行为 |
| 版本管理 | `Masterminds/semver` | 为后续版本选择与兼容性规则预留 |

代码目录结构（`apm` 工程根目录下）：

```text
apm/
  cmd/apm/            # CLI 入口，cobra 命令注册
  internal/
    lifecycle/        # 发现、安装、卸载、配置读写、版本管理
    adapter/           # 各目标 Agent 的 Adapter 实现与注册表
    config/            # 各 Agent 的安装清单模型与读写
    lock/              # 文件锁封装
    cache/              # 下载缓存管理
    cloud/              # 云端 Power 资源 list/get 客户端
  ivory-tower/        # 设计和开发文档
  go.mod
```

## Consequences

- 正面影响：
  - 单静态二进制分发，无需目标机器预装运行时，天然适合类 apt 工具的定位。
  - Go 对 `linux/riscv64` 一级支持，契合 bianbu/RISC-V 环境。
  - Helm、krew、gh CLI 等同类「发现-下载-缓存-安装-记录」工具均为 Go 实现，锁、原子安装、状态恢复等模式有成熟先例可循。
  - `flock` + staging/rename + journal 的组合可以直接满足「任意阶段中断都可识别并纠正」与「不支持并发、通过锁防重入」的约束。
  - Adapter 可用 Go interface + 编译期注册表实现，不需要引入运行时插件机制。
- 代价与风险：
  - 团队若无 Go 背景，需要一定学习成本。
  - 若未来需要支持第三方在不重新编译 APM 的情况下动态注册 Adapter，当前「编译期注册表」方案需要重新评估（例如引入插件机制或子进程协议）。
- 后续动作：
  - 在 `apm` 工程根目录下按上述目录结构初始化 Go module。
  - 后续 feature 设计（CLI 命令与 Power 类别交织、配置格式、冲突处理等）在此技术栈约束下展开。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Rust | 可靠性上限更高（uv、cargo 等先例），RISC-V 支持也好，但开发迭代慢、团队门槛高；APM 的复杂度（文件操作 + HTTP + JSON）用不到 Rust 的优势，MVP 阶段性价比低。 |
| TypeScript/Node | 与 Agent 生态（MCP 配置、Skill 多为 JSON/Markdown）贴近，npm 分发方便，但引入 Node 运行时依赖，与「类 apt 工具」的定位冲突，RISC-V 上 Node 可用性/性能不如原生二进制。 |
| Python | 开发最快，但分发是硬伤（依赖解释器和打包工具），包管理器自身依赖另一个包管理器不合适。 |

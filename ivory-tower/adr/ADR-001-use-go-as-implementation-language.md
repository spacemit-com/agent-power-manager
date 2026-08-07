# Architecture Decision Record

- Status：Accepted
- Date：2026-08-03
- Decision Makers：liangjunzhao

## Context

Agent Power Manager（APM）是一个面向多 Agent 的 Power（Skill、Plugin、MCP 等）管理工具，需要提供 `search`/`list`/`install`/`uninstall` 等 CLI 能力，并满足：

- 生命周期安全：任意执行阶段中断，系统都可以识别并纠正。
- 类 apt 的文件锁机制，不支持并发，通过锁防重入。
- 缓存下载的 Power，维护已安装 Power 记录。
- 可扩展的 Agent Power Adapter，适配不同目标 Agent 的安装位置与 Power 格式。
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
| Cloud 客户端 | 开发期本地文件系统实现 | 以绝对路径指定 `internal/cloud/resources`；`list` 返回带版本的全部 `PowerRef`，`get` 将版本已确定的 `PowerRef` 原子写入缓存；真实 HTTP 服务后续设计 |
| 下载缓存 | 按 `type/name/version` 分层的版本化文件系统目录 | 遵循 XDG 缓存目录约定；fetch 时去掉版本层并物化到干净 staging |
| 中断恢复 | staging 目录 + 原子 rename + 操作 journal | 启动时检测残留 staging/journal 即可识别并纠正 |
| 日志/输出 | 基于标准库 `log/slog` 的统一 `logging` 模块 | 所有打印统一管理；默认输出稳定的 CLI 信息，内部诊断通过全局 `--verbose` 开启 |
| 测试 | 标准 `testing` + `rogpeppe/go-internal/testscript` | testscript 便于端到端验证 CLI 的装/断/修复行为 |
| 版本管理 | `Masterminds/semver` | 为后续版本选择与兼容性规则预留 |

代码目录结构（`apm` 工程根目录下）：

```text
apm/
├── cmd/
│   └── apm/
│       └── main.go                 # 组装依赖、初始化编译期 Adapter Registry 并启动 CLI
├── internal/
│   ├── cli/
│   │   ├── root.go                 # Cobra 根命令和全局 --verbose 参数
│   │   ├── flags.go                # --agent、--type、--installed 等公共参数
│   │   ├── search.go               # search 子命令及参数校验
│   │   ├── list.go                 # list 子命令及参数校验
│   │   ├── install.go              # install 子命令及 power[=version] 解析
│   │   └── uninstall.go            # uninstall 子命令及参数校验
│   ├── lifecycle/
│   │   ├── service.go              # Lifecycle 接口及服务实现
│   │   ├── dependencies.go         # 生命周期依赖接口集合
│   │   ├── preflight.go            # 获取全局锁并触发公共 journal 预检
│   │   ├── batch.go                # --agent=all 的排序、fail-fast 与结果汇总
│   │   ├── search.go               # search 用例编排
│   │   ├── list.go                 # 云端列表和本地安装清单用例编排
│   │   ├── install.go              # 版本解析、幂等判断及安装事务编排
│   │   └── uninstall.go            # 本地消歧、幂等判断及卸载事务编排
│   ├── transaction/
│   │   ├── journal.go              # journal 模型、操作类型和事务阶段
│   │   ├── journal_store.go        # 唯一活动 journal 的原子读写和删除
│   │   ├── recovery.go             # 判断提交点并执行回滚或完成提交
│   │   ├── install.go              # staging、backup、publish 和清理
│   │   ├── uninstall.go            # removing、quarantine 和清理
│   │   └── path_state.go           # 检查事务路径状态，覆盖原子操作中断窗口
│   ├── adapter/
│   │   ├── adapter.go              # Adapter 接口及共享类型
│   │   ├── registry.go             # 编译期唯一 Agent Registry、Adapter 解析及已安装 Agent 快照刷新
│   │   ├── codex/
│   │   │   ├── adapter.go          # Codex Adapter 实现
│   │   │   ├── detect.go           # 检查 Codex Agent 根目录是否存在
│   │   │   ├── paths.go            # Codex Power 安装位置
│   │   │   └── transform.go        # Codex Power 格式转换
│   │   ├── claude_code/
│   │   │   ├── adapter.go          # Claude Code Adapter 实现
│   │   │   ├── detect.go           # 检查 Claude Code Agent 根目录是否存在
│   │   │   ├── paths.go            # Claude Code Power 安装位置
│   │   │   └── transform.go        # Claude Code Power 格式转换
│   │   └── hermes/
│   │       ├── adapter.go          # Hermes Adapter 实现
│   │       ├── detect.go           # 检查 Hermes Agent 根目录是否存在
│   │       ├── paths.go            # Hermes Power 安装位置
│   │       └── transform.go        # Hermes Power 格式转换
│   ├── config/
│   │   ├── manifest.go             # 各 Agent 的已安装 Power 清单模型
│   │   ├── store.go                # ConfigStore 接口
│   │   ├── json_store.go           # 每个 Agent 一份 JSON 清单的实现
│   │   └── atomic_write.go         # 清单临时写入、同步和原子替换
│   ├── lock/
│   │   ├── locker.go               # Locker 和 Unlock 接口
│   │   └── flock.go                # 全局 APM flock 实现
│   ├── cache/
│   │   ├── cache.go                # PowerCache 接口及缓存条目
│   │   └── filesystem.go           # 版本化文件系统缓存及 staging 物化
│   ├── cloud/
│   │   ├── client.go               # CloudClient 接口
│   │   ├── local_client.go         # 开发期本地资源 list/get 实现
│   │   └── resources/              # 开发期云端资源 fixture
│   ├── logging/
│   │   ├── logger.go               # 统一输出器及 --verbose 配置
│   │   ├── output.go               # 稳定的 CLI 正常、恢复、警告和错误输出
│   │   ├── verbose.go              # verbose 诊断日志
│   │   └── fields.go               # Agent、Power、版本、阶段等结构化字段
│   ├── domain/
│   │   ├── agent.go                # AgentID、AgentSnapshot 及 all 目标语义
│   │   ├── power.go                # PowerRef、PowerType 和 InstalledPower
│   │   └── result.go               # completed、failed、unexecuted 等结果
│   ├── version/
│   │   └── selector.go             # 最新版、指定版本及版本比较规则
│   └── paths/
│       └── paths.go                # 锁、journal、清单、缓存、临时目录及事务目录约定
├── ivory-tower/                    # 架构、ADR、C4 和 lifecycle 设计文档
├── go.mod
└── go.sum
```

采用该结构的理由：

1. `cmd/apm/main.go` 仅负责依赖装配和进程启动，Cobra 命令定义放入 `internal/cli`，避免入口文件同时承担业务编排。
2. `lifecycle` 与四条命令的 lifecycle 流程对应，负责用例顺序、公共预检和多 Agent 调度；文件按命令拆分但保持在同一 package，便于共享锁内状态和结果模型。
3. `transaction` 单独承载 [ADR-003](ADR-003-serialize-lifecycle-and-recover-journal.md) 的 journal、提交点、回滚和完成提交规则。当前命令在提交点后的清单更新或清理失败时保留 journal 并退出，由下一条命令的 `recovery.go` 完成遗留事务，不在当前命令中重复更新清单或清理。
4. `transaction/path_state.go` 集中处理“原子文件操作已经完成、journal 阶段尚未更新”的中断窗口，使正常事务与恢复事务采用相同的路径状态判断。
5. `adapter` 展开首批 `codex`、`claude_code`、`hermes` 内置实现。Registry 在编译期保证 Agent ID 唯一；每个 Adapter 除目标安装位置与格式转换外，还按 [ADR-004](ADR-004-discover-installed-agents-before-local-operations.md) 检查对应 Agent 根目录是否存在。APM 不负责 Agent 安装、卸载、实时监控、运行时感知或更新通知；新增 Agent 遵循 `internal/adapter/<agent>/` 约定。
6. `config` 独立管理各 Agent 的 JSON 安装清单。`atomic_write.go` 保证清单不会暴露半写入状态，事务完成与 journal 删除顺序仍由 `transaction` 控制。
7. 所有用户输出和内部诊断统一经过 `logging`，其他 package 不直接调用 `fmt.Print*` 或自行创建 logger。稳定的资源数据和成功结果写入 `stdout`；journal 恢复过程、警告和失败写入 `stderr`；内部执行步骤、Cloud 资源读取细节和事务路径状态仅在全局 `--verbose` 开启后写入 `stderr`。默认 CLI 输出格式保持稳定，便于用户阅读和脚本处理。logger 的装配方式已由 [ADR-006](ADR-006-use-global-slog-with-level-routing.md) 调整为预解析 verbose 后安装全局 `slog`，不再向各模块注入自定义 logging 实例。
8. `domain` 保存跨组件共享的 Agent、Power 和执行结果类型，避免相同概念分别在 CLI、Cloud、Config 和 Lifecycle 中重复定义。
9. `paths` 是所有本地状态路径的唯一来源，保证 lock、journal、manifest、cache、staging、各目标文件系统内的临时目录、backup 和 quarantine 在正常执行与恢复时使用同一套位置规则。Cloud get 使用缓存根内的临时目录，Cache fetch 使用状态根内的临时目录，避免原子 rename 跨文件系统。
10. Cloud Client 当前只提供开发期本地实现。`PowerRef` 同时保存类别、名称和版本；解析最新版前版本可为空，lifecycle 从 `List()` 结果选择目标后版本必须非空。`Get(ref)` 成功即表示目标 Power 已原子写入 `type/name/version` 缓存。Cache 不提供 `Put`，`Fetch(ref)` 在每次调用开头清理 staging，并把缓存内容物化为不含版本层的 staging 路径。
11. 目录树只描述生产代码和主要文件，不展开测试布局，也不增加 APM 职责之外的 Agent runtime、daemon 或动态插件机制。

## Consequences

- 正面影响：
  - 单静态二进制分发，无需目标机器预装运行时，天然适合类 apt 工具的定位。
  - Go 对 `linux/riscv64` 一级支持，契合 bianbu/RISC-V 环境。
  - Helm、krew、gh CLI 等同类「发现-下载-缓存-安装-记录」工具均为 Go 实现，锁、原子安装、状态恢复等模式有成熟先例可循。
  - `flock` + staging/rename + journal 的组合可以直接满足「任意阶段中断都可识别并纠正」与「不支持并发、通过锁防重入」的约束。
  - Adapter 可用 Go interface + 编译期注册表实现，不需要引入运行时插件机制。
  - Adapter Registry 能以命令内快照响应 Agent 的外部安装或卸载，不需要持久化第二份 Agent 状态。
  - 生命周期编排与事务恢复分包，journal 不变量和提交点规则具有明确的实现边界。
  - 所有打印由统一 logging 模块管理，默认 CLI 输出保持稳定，同时可通过 `--verbose` 获取诊断信息。
- 代价与风险：
  - 团队若无 Go 背景，需要一定学习成本。
  - 若未来需要支持第三方在不重新编译 APM 的情况下动态注册 Adapter，当前「编译期注册表」方案需要重新评估（例如引入插件机制或子进程协议）。
- 后续动作：
  - 在 `apm` 工程根目录下按上述目录结构初始化 Go module。
  - CLI 和各内部模块统一使用 [ADR-006](ADR-006-use-global-slog-with-level-routing.md) 配置的全局 `slog`，禁止绕过 logging 直接打印；在根命令中配置全局 `--verbose`。
  - 后续 feature 设计（CLI 命令与 Power 类别交织、配置格式、冲突处理等）在此技术栈约束下展开。
  - 真实 Cloud HTTP 协议、资源校验、checksum 和内容寻址策略在本地 Cloud/Cache 流程验证后另行设计。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| Rust | 可靠性上限更高（uv、cargo 等先例），RISC-V 支持也好，但开发迭代慢、团队门槛高；APM 的复杂度（文件操作 + JSON + 后续网络访问）用不到 Rust 的优势，MVP 阶段性价比低。 |
| TypeScript/Node | 与 Agent 生态（MCP 配置、Skill 多为 JSON/Markdown）贴近，npm 分发方便，但引入 Node 运行时依赖，与「类 apt 工具」的定位冲突，RISC-V 上 Node 可用性/性能不如原生二进制。 |
| Python | 开发最快，但分发是硬伤（依赖解释器和打包工具），包管理器自身依赖另一个包管理器不合适。 |

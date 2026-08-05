# CLI 基础骨架规格

## 目标

本 feature 建立 APM 的首个可编译 Go 工程基线，交付一个能够显示帮助、解析并校验命令，但不执行 Power 生命周期业务的 `apm` 二进制。CLI、logging 和 lock 提供真实能力；ADR-001 中其余生产模块只建立后续实现所需的主要对象、接口和方法占位。

## 架构边界

### 工程框架

- Go module 名称为 `apm`。
- 按 [ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md) 创建 `cmd/apm` 及 `internal` 下列出的生产目录和文件。
- 不创建 `internal/adapter/codex`、`internal/adapter/claude_code` 和 `internal/adapter/hermes` 三个内置 Adapter 实现目录；`internal/adapter/adapter.go` 和 `internal/adapter/registry.go` 保留在框架内。
- CLI、logging、lock 及 lock 所需的 paths 逻辑为真实实现。
- CLI 本阶段只与 logging 串联，不创建或调用 Lifecycle，也不获取全局锁。
- Lifecycle、Transaction、Adapter Registry、Config、Cache、Cloud、Domain 和 Version 等其余模块只定义现有 ADR 或 C4 类图已经明确的主要类型、接口、构造入口和方法占位，不补充未经设计的 API。
- 接口方法只有签名。必须存在的具体占位方法不包含业务逻辑：无返回值时使用空方法体，有返回值时返回对应零值。占位方法不会被 CLI 调用。
- 重要对象、接口、构造函数和方法使用简洁的中文 GoDoc 注释说明职责与边界。

### 明确不实现

- Power 的搜索、云端列举、本地清单列举、安装和卸载。
- CLI 到 Lifecycle、lock、cloud、cache、config、transaction 或 adapter 的调用链。
- journal 预检、事务提交点、中断恢复和多 Agent 批处理。
- 云端 HTTP 请求、下载缓存、JSON 清单持久化、原子安装、版本选择和 Agent 根目录检测。
- 三个内置 Adapter，以及 Power 路径计算、格式转换和文件操作。
- default-agent 配置兜底、动态插件、daemon 或目标 Agent runtime。

## CLI 规格

### 命令树

根命令注册以下四个子命令，并提供持久化全局 flag `--verbose`：

```text
apm search <keyword> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm list --agent=<agent-id|all> [--type=<skill|plugin|mcp>] [--installed]
apm install <power>[=<version>] --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
apm uninstall <power> --agent=<agent-id|all> [--type=<skill|plugin|mcp>]
```

- `--agent` 在四个子命令中均为必填项。空字符串无效；字面量 `all` 和非空 Agent ID 都可通过解析。本阶段不读取 default-agent 配置，也不判断 Agent 是否受支持或已安装。
- `--type` 可省略；提供时只接受 `skill`、`plugin` 或 `mcp`。
- `search` 必须且只能接收一个非空 keyword。
- `list` 不接收位置参数，并支持布尔 flag `--installed`。
- `install` 必须且只能接收一个 `<power>[=<version>]`。Power 名称不能为空；出现 `=` 时显式版本也不能为空。解析只拆分名称和版本，不进行语义版本校验或云端解析。
- `uninstall` 必须且只能接收一个非空 Power 名称。

### 输出与退出

- 根命令和子命令的 help、usage 由 Cobra 原生生成；logging 只提供统一的 stdout/stderr 输出目标，不重新拼装帮助文本。
- help 写入 stdout 并以状态 0 退出。
- 未知命令、未知 flag、缺少必填参数、参数过多、非法 `--type` 或非法 install 表达式写入 stderr，显示 Cobra usage，并以非零状态退出。
- 合法命令解析完成后默认保持静默并返回成功，不打印可能被误解为业务已执行的结果。
- 启用 `--verbose` 时，合法命令解析完成后通过 logging 向 stderr 写一条结构化诊断，至少包含 command、agent、type 和解析后的位置参数；仍不执行业务。
- 本 feature 不增加 `--version`，因为当前设计没有定义版本值的构建注入方式。

## Logging 规格

- `Logger` 统一持有 stdout、stderr、verbose 状态和内部 `slog.Logger`。
- 正常信息和稳定资源输出写 stdout；警告、错误、恢复信息及 verbose 诊断写 stderr。
- 构造时允许注入 `io.Writer`，生产默认值为 `os.Stdout` 和 `os.Stderr`。
- CLI 使用 logging 提供的 writer 配置 Cobra 输出，并使用 logging 方法输出 verbose 解析诊断。
- 不实现 JSON 格式、颜色、日志文件、轮转、额外日志级别 flag 或业务输出格式。

## Lock 与路径规格

- `paths` 中与锁有关的实现从当前用户 home 目录生成固定路径 `$HOME/.apm/apm.lock`。home 目录无法解析时返回错误，不回退到当前目录或临时目录。
- lock 获取前创建 `$HOME/.apm`；目录创建或文件访问失败时返回底层上下文错误。
- `Locker.Acquire()` 使用 `gofrs/flock` 的非阻塞获取能力。锁已被另一实例占用时立即返回可用 `errors.Is` 识别的 `ErrLocked`，不等待、不重试。
- 获取成功后返回独立 `Unlock` 句柄；`Release()` 释放底层 flock 并返回释放错误。
- 本阶段不实现超时、重试、锁持有者元数据或 CLI 锁接入。

## 测试与验收

### 自动化测试

- 使用表驱动测试覆盖四个命令的合法解析和各项参数错误。
- 验证根命令与子命令 help、Cobra usage、全局 `--verbose`、stdout/stderr 路由和 Execute 返回状态。
- 验证合法命令默认无业务输出，verbose 模式仅产生解析诊断。
- 验证 lock 默认路径、父目录自动创建、两个实例竞争同一路径时返回 `ErrLocked`，以及释放后能够重新获取。
- 验证 logging 的正常、警告、错误、恢复和 verbose 输出路由，以及关闭 verbose 时不产生诊断输出。
- 不测试占位模块的业务行为，也不为零值返回编写形式化测试。

### 完成条件

以下检查全部成功：

```text
go test ./...
go vet ./...
go build ./cmd/apm
```

代表性行为：

- `apm --help` 展示四个子命令并成功退出。
- `apm search foo --agent=all` 成功退出且无业务输出。
- `apm search foo --agent=all --verbose` 成功退出，并只在 stderr 输出解析诊断。
- 缺少 `--agent` 或使用非法 `--type` 时显示 usage 并非零退出。

## 文档同步

- 实现完成后，由于源码新增了可观察行为，按项目规则在 `docs/Changelog.md` 添加一条记录。
- 本 feature 只落实 ADR-001～004 已有决策，没有引入新的领域术语或难以逆转的架构取舍，因此不新增 `CONTEXT.md` 或 ADR。

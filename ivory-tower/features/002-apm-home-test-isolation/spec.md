# APM_HOME 测试文件系统隔离规格

## 目标

建立 APM 本地文件路径的测试隔离入口。测试设置非空 `APM_HOME` 后，已实现的 APM 状态和默认 lock 必须位于该目录；缓存遵循 `XDG_CACHE_HOME > APM_HOME > 系统默认` 的独立优先顺序。

项目级设计见 [Project Overview](../../project-overview.md#test-filesystem-isolation)，决策记录见 [ADR-005](../../adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。

## 路径语义

- `internal/paths` 是 `APM_HOME`、HOME 和 XDG 缓存语义的唯一解析入口。
- `APM_HOME` 非空时直接作为有效 HOME。
- `APM_HOME` 未设置或为空字符串时，使用 `os.UserHomeDir()`。
- 有效 HOME 无法解析或为空时返回带上下文的错误。
- APM 状态根为 `<effective-home>/.apm`，默认 lock 为 `<effective-home>/.apm/apm.lock`。
- `XDG_CACHE_HOME` 非空时，缓存为 `$XDG_CACHE_HOME/apm`，不再读取 `APM_HOME` 或系统默认缓存目录。
- `XDG_CACHE_HOME` 为空或未设置且 `APM_HOME` 非空时，缓存为 `$APM_HOME/.cache/apm`。
- `XDG_CACHE_HOME` 和 `APM_HOME` 均为空或未设置时，缓存使用 `os.UserCacheDir()` 的系统默认结果并追加 `apm`。

## 组件边界

- CLI 不解析或展示 `APM_HOME`，不增加 flag 或 help 文本。
- APM 不改写 `HOME` 或 `XDG_CACHE_HOME`，不缓存环境解析结果。
- Lock 只消费 `paths.LockPath()`。后续 Adapter、Config、Cache 和 Transaction 实现必须消费 `internal/paths`，不得自行读取 HOME 类环境变量。
- 本 feature 只实现有效 HOME、状态根、缓存路径和现有默认 lock 接入；不展开其他模块的占位方法。

## 明确不实现

- 不校验 `APM_HOME` 是否为绝对路径、是否含符号链接、是否有权限或是否逃逸。
- 不将 `APM_HOME` 转换为绝对路径或清理路径。
- 不将 `APM_HOME` 定义为普通用户的数据迁移、多账户或多配置目录功能。
- 不实现 Agent 检测、Power 安装卸载、配置持久化、缓存写入或事务恢复。

## 测试与验收

- 单元测试覆盖 `APM_HOME` 非空优先、空值回退、未设置回退，以及 HOME 无法解析时返回错误。
- 单元测试覆盖隔离模式的状态根和 lock，并分别验证 `XDG_CACHE_HOME`、`APM_HOME` 和系统默认缓存目录三个分支。
- 默认 lock 集成测试必须在不存在的隔离根下创建 `$APM_HOME/.apm/apm.lock`，并证明测试 HOME 下未创建 `.apm`。
- 修改环境变量的测试不使用 `t.Parallel()`。
- `go test ./...`、`go vet ./...` 和 `go build -o /dev/null ./cmd/apm` 全部成功。

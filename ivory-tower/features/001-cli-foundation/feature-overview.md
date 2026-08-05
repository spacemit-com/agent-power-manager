# Feature Overview

## Goal

- 特性名称：CLI 基础骨架
- 要解决的问题：为 APM 建立与 ADR-001 一致的可编译 Go 工程框架，并交付只解析命令、不执行业务用例的 CLI，同时提供可独立使用的 logging 和全局文件锁。
- 可交付结果：一个支持 `search`、`list`、`install`、`uninstall` 命令解析、Cobra 原生帮助和 `--verbose` 诊断的 `apm` 二进制。

## Scope

- 包含：ADR-001 生产代码目录和主要对象/方法占位；CLI 参数解析与校验；logging；基于 `$HOME/.apm/apm.lock` 的非阻塞 flock；锁路径解析；重要对象和方法的中文 GoDoc 注释。
- 不包含：三个内置 Adapter 实现；CLI 与 Lifecycle 或 lock 的串联；Power 搜索、列举、安装和卸载业务；云端、缓存、配置、事务、恢复、版本选择和 Agent 检测的实际逻辑。

## Implementation Context

- 关联模块或服务：`cmd/apm`、`internal/cli`、`internal/logging`、`internal/lock`、`internal/paths`，以及 ADR-001 中其余内部模块的可编译占位。
- 数据、接口或依赖变化：初始化 `module apm`；引入 `spf13/cobra` 和 `gofrs/flock`；不产生业务数据和外部服务调用。
- 关联 C4 文档或 ADR：[ADR-001](../../adr/ADR-001-use-go-as-implementation-language.md)、[ADR-002](../../adr/ADR-002-cli-agent-and-power-type-as-flags.md)、[ADR-003](../../adr/ADR-003-serialize-lifecycle-and-recover-journal.md)、[ADR-004](../../adr/ADR-004-discover-installed-agents-before-local-operations.md)、[APM CLI Components](../../c4/components/apm-cli.dsl)。

## Acceptance

- 验收条件：`apm` 可构建；四个子命令能按 ADR-002 完成解析和校验但不执行业务；help、usage、错误和 verbose 输出符合 spec；lock 与 logging 可独立工作；`go test ./...` 和 `go vet ./...` 通过。

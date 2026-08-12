# 构建指南

本指南说明如何在本地构建、验证和运行 APM 命令行工具。

## 前置条件

- Git
- Go 1.26.5（版本以 [`go.mod`](go.mod) 为准）

在仓库根目录确认 Go 版本：

```bash
go version
```

## 快速构建

在仓库根目录执行：

```bash
go mod download
go build -o ./apm ./cmd/apm
./apm --help
```

构建完成后，可执行文件位于 `./apm`。

## 开发验证

提交前建议依次执行以下检查：

```bash
# 检查 Go 源码是否已格式化；无输出表示通过。
test -z "$(gofmt -l cmd internal)"

# 运行全部单元测试。
go test ./...

# 执行 Go 静态检查。
go vet ./...

# 重新构建命令行程序。
go build -o ./apm ./cmd/apm
```

如需自动格式化源代码，执行：

```bash
gofmt -w cmd internal
```

## 本地运行

构建后可通过下列命令验证命令行参数解析：

```bash
./apm --help
./apm search summary
./apm search summary --type skill
./apm --verbose search summary
./apm list
./apm list --type skill
./apm list --installed --agent codex
./apm list --installed --agent all --type skill
./apm install foo-before-summary --agent codex
./apm install foo-before-summary=v0.0.1 --agent codex --type skill
./apm install foo-before-summary --agent all
./apm uninstall foo-before-summary --agent codex
./apm uninstall foo-before-summary --agent all --type skill
```

Search 已实现：它按名称搜索 Cloud Power，为每个类别和名称选择最新版本，并输出精确格式 `<type>/<name>@<version>`。例如：

```text
skill/foo-before-summary@v0.0.1
```

Search 只接受可选的 `--type`，不接受 `--agent`。全局选项 `--verbose` 输出解析和恢复诊断到标准错误；帮助信息可查看每个子命令支持的参数。

List 已实现两种独立模式（命令与锁边界见 [ADR-002](ivory-tower/adr/ADR-002-cli-agent-and-power-type-as-flags.md) 和 [ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md)）：默认 Cloud List 不加锁、不 Recover，输出每个版本的精确行：

```text
<type>/<name>@<version>
```

Installed List 必须同时提供 `--installed --agent=<agent-id|all>`，在成功 Release 后输出精确行：

```text
<agent>/<type>/<name>@<version>
```

例如 `./apm list --agent all` 是无效组合：它会非零退出，并在 stderr 报告 `--agent requires --installed` 和 usage。`--agent=all` 的 installed 读取若出现部分失败，会输出成功记录后以非零状态报告错误；fatal 或 Release error 不输出业务行。已安装清单版本必须精确为 `vMAJOR.MINOR.PATCH`，APM 不会迁移或清理非法旧清单（[ADR-007](ivory-tower/adr/ADR-007-use-stateless-config-repository.md)）；Agent 刷新顺序见 [ADR-004](ivory-tower/adr/ADR-004-discover-installed-agents-before-local-operations.md)。

Install 已实现完整生命周期：显式版本必须是规范 `vMAJOR.MINOR.PATCH`，Name 按区分大小写的原始值精确匹配；同名 Cloud 候选跨 Type 时必须在版本选择前提供 `--type`。Install 从一次 Acquire 开始，持锁完成 Recover、Warn、Agent 刷新、Cloud、Cache 和串行 Transaction；只有成功 Release 后 CLI 才渲染。

每个目标 Agent 的 stdout 精确为以下六种 outcome 之一：

```text
<agent>/<type>/<name>@<version> installed
<agent>/<type>/<name>@<version> upgraded
<agent>/<type>/<name>@<version> downgraded
<agent>/<type>/<name>@<version> unchanged
<agent>/<type>/<name>@<version> failed
<agent>/<type>/<name>@<version> unexecuted
```

`--agent=all` 按 Agent ID 字典序 fail-fast；只有业务失败后又成功 Release 的批处理结果会先渲染 `failed/unexecuted` 行、再非零退出。fatal 或 Release error 不输出业务行。

Uninstall 已实现完整生命周期。它不接受版本；例如 `./apm uninstall foo-before-summary=v0.0.1 --agent codex` 会在创建 lifecycle 前报告 `uninstall does not accept a version`。省略 `--type` 时，每个 Agent 分别从自己的本地清单解析 Type 和 Version；同一 Agent 内存在跨 Type 同名记录时，必须显式提供 `--type`。

Uninstall 在一次 Acquire/Release 之间完成 Recover、恢复 Warn、Agent 刷新、逐 Agent 清单解析和串行 Transaction。清单无记录时同步 Warn、记为 Agent-only `unexecuted` 并继续；清单有记录但 target 已缺失时，Transaction 仍移除该清单项，lifecycle 同步 Warn 并记为完整引用 `completed`。决策边界见 [ADR-003](ivory-tower/adr/ADR-003-serialize-lifecycle-and-recover-journal.md) 与 [ADR-009](ivory-tower/adr/ADR-009-centralize-lifecycle-transactions.md)。

stdout 只有两种精确行形式：

```text
<agent>/<type>/<name>@<version> <completed|failed|unexecuted>
<agent> <failed|unexecuted>
```

例如成功卸载可输出 `codex/skill/foo-before-summary@v0.0.1 completed`；重复执行时可输出 `codex unexecuted`。`--agent=all` 按 Agent ID 字典序 fail-fast：首个业务失败后，当前 Agent 记为 `failed`、剩余 Agent 记为 `unexecuted`。只有成功 Release 后才渲染汇总；Release error 抑制全部业务行。当前只有 Skill Adapter 可执行真实安装/卸载；Plugin/MCP Adapter 仍不支持。

## 清理本地构建产物

删除由本指南生成的可执行文件：

```bash
rm -f ./apm
```

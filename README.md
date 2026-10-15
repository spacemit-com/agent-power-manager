<div align="center">

# Agent Power Manager

**为 AI Agent 统一管理 Skill、Plugin、MCP 与 Expert 四类 Power 的命令行工具**

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

</div>

---

## 这是什么

APM（Agent Power Manager）是一个 Go 编写的命令行工具，把 AI Agent 的能力扩展——**Skill、Plugin、MCP、Expert** 四类 Power——的检索、安装、升级、卸载与提交收敛到同一套命令。

它从 APC（Agent Power Cloud）同步资源元数据，按版本安装到目标 Agent，并把这些操作记录成可恢复的事务。支持的目标 Agent 包括 `bibit`、`codex`、`claude-code` 与 `hermes-agent`。

每类 Power 格式不同，每个 Agent 又各有自己的资源目录、清单格式与注册方式，手工维护很容易失去一致性：版本散落各处，安装失败只留下一半文件。APM 把这些操作变成可重复、可回滚、可查询的，并在本地保留一份精确到版本的已安装清单。

## 核心能力

| 命令 | 用途 |
| --- | --- |
| `search` | 按关键字检索云端可安装的 Power |
| `list` / `list --installed` | 浏览云端目录，或查看某个 Agent 已安装的清单 |
| `show` | 查看指定 Power 的详情与可用版本 |
| `install` | 安装指定版本的 Power 到目标 Agent |
| `upgrade` | 将已安装的 Power 升到最新版本 |
| `uninstall` | 卸载 Power 并清理 Agent 侧落地状态 |
| `fix` | 恢复被中断的事务 |
| `validate` / `submit` | 离线校验资源格式，并提交到 APC |

重复安装同一版本返回 `unchanged`，重复卸载返回 `unexecuted`，都不会产生第二份文件。`--agent all` 可对整个 Agent 列表批量执行，逐 Agent 输出结果，失败即停。

## 支持的 Agent 与 Power 类型

| 项目 | 取值 |
| --- | --- |
| Agent（`--agent`） | `bibit`、`codex`、`claude-code`、`hermes-agent`，以及批量值 `all` |
| Power 类型（`--type`） | `skill`、`plugin`、`mcp`、`expert` |

`expert` 是本地类别名，对应 APC 接口中的 `agent`/`agents` 字段。旧拼写 `--type agent` 仍可使用但会输出弃用警告；`--type expert` 仅支持 `bibit`。

## 快速开始

需要 Go 1.26.0（版本以 [`go.mod`](go.mod) 为准）：

```bash
go mod download
go build -o ./apm ./cmd/apm
./apm --help
```

Bianbu 开发板也可以构建 Debian 软件包（产物为 `/usr/bin/apm`）：

```bash
sudo apt-get install debhelper golang-go ca-certificates
dpkg-buildpackage -us -uc -b
sudo apt-get install ../agent-power-manager_<版本>_<架构>.deb
```

## 常用命令

```bash
# 检索与浏览
apm search superpowers
apm list
apm list superpowers --all
apm show superpowers=v6.3.0

# 安装到指定 Agent（四类 Power 语法一致）
apm install superpowers=v6.3.0 --type plugin --agent codex
apm install foo-before-summary=v0.0.1 --type skill --agent claude-code
apm install nws=v1.0.0 --type mcp --agent hermes-agent
apm install test-agent=v1.0.0 --type expert --agent bibit

# 查看已安装清单
apm list --installed --agent codex --type plugin --json

# 卸载只传名称，不接受版本
apm uninstall superpowers --type plugin --agent codex

# 升级
apm upgrade --dry-run
apm upgrade -y
```

`list`、`search`、`show`、`upgrade`、`validate` 支持 `--json` 输出；`submission get/list` 使用 `--output json`。

脚本与 CI 中调用 `apm upgrade` 需要显式加 `-y`：非交互终端或 `--json` 下没有 `-y` 会直接失败，且不会取锁或同步目录。

## 配置

默认使用 Remote Provider（生产环境 `https://www.spacemit.com`），配置错误或远端失败时不会自动回退到 Local。

| 环境变量 | 默认值与约束 |
| --- | --- |
| `APM_CLOUD_PROVIDER` | 默认 `remote`；只接受 `remote` 或 `local` |
| `APC_BASE_URL` | 默认 `https://www.spacemit.com`；必须是无凭据、无 query/fragment 的 host-only 根地址；非 loopback 只允许 HTTPS |
| `APC_API_KEY` | 仅 `submit` 与 `submission` 需要 |
| `APM_CLOUD_LOCAL_ROOT` | 选择 `local` 时必填，需为 clean absolute path |
| `APM_HOME` | 状态根父目录，默认取用户主目录；状态写入其下的 `.apm/` |

```bash
# 指向测试环境
APC_BASE_URL=https://developer.bianbu.xyz ./apm list

# 使用本地 fixture，离线运行
APM_CLOUD_PROVIDER=local \
APM_CLOUD_LOCAL_ROOT="$(pwd)/internal/cloud/resources" \
./apm list
```

`APC_BASE_URL` 只填主机；网关前缀 `/api-server/apc` 由客户端统一拼接。

## 文档

| 文档 | 内容 |
| --- | --- |
| [BUILD.md](BUILD.md) | 构建、测试、Provider 与目录同步说明 |
| [APC-API.md](APC-API.md) | APC 服务端接口契约 |
| [docs/power-template/README.md](docs/power-template/README.md) | 四类 Power 的提交格式规则与模板 |
| [CONTEXT.md](CONTEXT.md) | 领域词汇表 |
| [docs/Changelog.md](docs/Changelog.md) | 变更记录 |
| [docs/testing/apm-cli-functional-test.md](docs/testing/apm-cli-functional-test.md) | CLI 功能测试指引 |
| [ivory-tower/adr](ivory-tower/adr) | 架构决策记录（ADR） |

## 许可证

[Apache License 2.0](LICENSE)

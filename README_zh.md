<div align="center">

# Agent Power Manager

**为 AI Agent 统一管理 Skill、Plugin、MCP 与 Expert 四类 Power 的命令行工具**

[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[English](README.md) | [简体中文](README_zh.md)

</div>

---

## 项目概览

APM（Agent Power Manager）是一个 Go 编写的命令行工具，把 AI Agent 的能力扩展——**Skill、Plugin、MCP、Expert** 四类 Power——的检索、安装、升级、卸载与提交收敛到同一套命令。

它从 APC（Agent Power Cloud）同步资源元数据，按版本安装到目标 Agent，并把这些操作记录成可恢复的事务。支持的目标 Agent 包括 `bibit`、`codex`、`claude-code` 与 `hermes-agent`。

每类 Power 格式不同，每个 Agent 又各有自己的资源目录、清单格式与注册方式，手工维护很容易失去一致性：版本散落各处，安装失败只留下一半文件。APM 把这些操作变成可重复、可回滚、可查询的，并在本地保留一份精确到版本的已安装清单。

## 功能架构

APM 不是孤立工具，而是一条端到端资源流水线中的**客户端一环**。理解它的功能，关键是看清它与
另外两个系统的分工：

| 组件 | 角色 | 部署位置 |
| --- | --- | --- |
| **APM**（本仓库） | 命令行客户端：校验、打包、上传、检索、下载、安装、升级、卸载、事务恢复 | 开发机与 Bianbu 板端 |
| **APC**（Agent Power Cloud） | SpacemiT 的资源分发平台：接收提交、人工审核、发布、维护资源目录、托管资源文件 | 服务端（`www.spacemit.com` + CDN） |
| [**bibit-agenthub-plugin**](https://github.com/spacemit-com/bibit-agenthub-plugin) | Bibit 的 Agent Hub 前端插件：在 Bibit 内提供浏览与安装界面，调用 APM 并消费其输出 | 板端 Bibit 插件 |

```text
① 调用入口：两条路径都进入同一个 APM 客户端
┌─────────────────────────────────────────────────────────────┐
│  User CLI 直接执行 apm：search / list / install / upgrade     │
└─────────────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────────────┐
│  bibit-agenthub-plugin：Bibit 内浏览 / 安装 UI，调用 apm       │
└─────────────────────────────────────────────────────────────┘
   │
   │  两路合并
   ▼
② APM 客户端（本仓库：CLI + lifecycle，唯一改动本地状态的一侧）
┌─────────────────────────────────────────────────────────────┐
│  校验 / 确定性打包：apm validate、apm submit                    │
│  目录同步 / 版本对账：apm search、list、show                      │
│  下载 / size+MD5 校验 / 版本化缓存                             │
│  journal 事务与恢复：install、upgrade、uninstall、fix             │
└─────────────────────────────────────────────────────────────┘
   │ 上传：HTTPS + Bearer                     ▲ 获取：目录 + 内容（302 → CDN）
   ▼                                         │
③ 服务端与存储（APC + CDN）
┌─────────────────────────────────────────────────────────────┐
│  APC：接收提交 → 人工审核 → 发布 → 维护资源目录                   │
│  CDN：cdn-resource.spacemit.com 托管 tar.gz / JSON           │
└─────────────────────────────────────────────────────────────┘
   │ 落地：APM 原子发布 + 更新本机清单
   ▼
④ 落地结果
┌─────────────────────────────────────────────────────────────┐
│  目标 Agent：Bibit / Codex / Claude Code / Hermes            │
│  本地状态：.apm/manifests · cache · staging · transactions    │
└─────────────────────────────────────────────────────────────┘
```

### 资源存储

APC 是资源的唯一权威存储，且**目录与文件分离**：

- **资源目录（catalog）**：一个 JSON 文档，顶层按 `skills`/`plugins`/`mcps`/`agents` 四组，列出每个
  资源的最新版本、描述、`content_size`、`content_md5`、`download_url`、`published_at` 与 `tags`。
  它**不含文件内容**，因此体积小、可频繁同步。
- **资源文件**：托管在 CDN（`cdn-resource.spacemit.com`），按类型与版本寻址。Skill/Plugin/Expert
  是确定性 `.tar.gz`，MCP 是 JCS 规范化的单个 JSON。

APM 本地另存一份状态，供离线运行与事务恢复使用：

| 路径 | 内容 |
| --- | --- |
| `.apm/manifests/<agent>.json` | 已安装清单，精确到版本，是 `list --installed` 与 `upgrade` 的唯一输入 |
| `.apm/cache/<type>/<name>/<version>` | 版本化内容缓存，命中即复用，不重复下载 |
| `.apm/staging/` | 安装前的物化与校验区 |
| `.apm/transactions/` | 事务 journal，崩溃后用 `apm fix` 恢复 |

### 上传（发布资源）

```text
资源目录 ──> apm validate --type <t>   离线完整校验（不联网）
        ──> apm submit --type <t>     确定性打包 + MD5 + HTTP 提交
        ──> APC 返回 submission_id    状态 received
        ──> 平台审核                  review_pending → approved / rejected
        ──> 发布                      publishing → published（进入目录）
```

- **校验在前**：`validate` 纯离线完成，把格式错误挡在网络请求之前；`submit` 复用同一套校验。
- **确定性打包**：同样输入产生逐字节相同的归档（稳定排序、固定 mtime/uid/gid、无 gzip 时间戳），
  所以 MD5 稳定、可校验、可复现。
- **仅提交需要凭据**：只有 `submit` 与 `submission` 需要 `APC_API_KEY`；查询与下载匿名即可。
- **审核是人工环节**：APM 只负责提交与查询状态，发布由 APC 平台完成。

### 获取（安装与升级）

```text
apm list / search ──> 同步资源目录（一次快照）──> 展示可安装资源
apm install       ──> 解析版本 ──> 取内容（CDN，校验 size/MD5）──> 校验格式
                  ──> 写入 staging ──> journal 事务 ──> 落地目标 Agent
                  ──> 更新 .apm/manifests
apm upgrade       ──> 读本地清单 ──> 一次目录快照 ──> 逐条规划 ──> 确认 ──> 逐条执行
```

- **按需下载**：`list`/`search` 只取目录，不下载内容；只有 `install`/`upgrade` 才取文件。
- **内容必须自证**：下载后核对目录声明的 `content_size` 与 `content_md5`，再做归档安全与格式校验，
  任一不符即 fail closed，不留半个文件。
- **缓存复用**：同版本二次安装命中缓存，不再下载。
- **事务保障**：落地由 journal 驱动，中途中断可用 `apm fix` 回到一致状态；重复安装同版本返回
  `unchanged`，不产生第二份文件。

### 三个组件的边界

**APM 只做客户端，不改服务端契约。** APC 的 HTTP 契约以 [`APC-API.md`](APC-API.md) 为唯一权威；
服务端线缆取值是 `agent`/`agents`，而 APM 本地统一叫 `expert`，翻译只发生在 HTTP 边界与目录分组名
两处（[ADR-044](ivory-tower/adr/ADR-044-rename-agent-power-type-to-expert.md)）。

**bibit-agenthub-plugin 消费 APM，而不是替代它。** 插件在 Bibit 内提供浏览与安装界面，读取 APM 的
命令输出与 JSON 字段（如 `name_zh`/`name_en`/`description_zh`）。因为两侧共享那份 JSON 契约，
**APM 与插件必须同批升级**：新 APM 配旧插件解析不了新字段，反之亦然。仓库见
[spacemit-com/bibit-agenthub-plugin](https://github.com/spacemit-com/bibit-agenthub-plugin)。

**目标 Agent 由各自的 Adapter 适配。** 四个 Agent 的资源目录、清单格式与注册方式各不相同，
APM 用 `internal/adapter` 隔离这些差异；Expert 仅 Bibit 支持。

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

Expert 还可以在同一个归档中携带属于自己的 Skill、Plugin 与 MCP，分别位于 `skills/`、`plugins/` 与 `mcp/`——一次安装即可同时交付 Agent 及其所需能力。

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

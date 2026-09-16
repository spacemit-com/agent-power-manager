# bianbu-agent 使用指南

`bianbu-agent` 保留本地资源管理命令，以及 Runtime 服务启动需要的 `serve`、`worker` 和 `--role`。
资源命令直接操作配置指定的资源文件，不启动或连接 Runtime、Worker，不加载模型、执行工具代码或启动 MCP 进程。

## 帮助与配置

```bash
bianbu-agent
bianbu-agent --help
bianbu-agent --version
bianbu-agent agent --help
bianbu-agent agent create --help
```

不带参数只显示帮助。所有资源和子命令都支持 `--help`；帮助与版本查询不读取 Runtime 配置、不初始化资源目录。

全局参数放在资源名称之前：

```bash
bianbu-agent --config /home/bianbu/.bianbu-agent/config.yaml agent list
bianbu-agent --debug --config /home/bianbu/.bianbu-agent/config.yaml tool list
```

未指定 `--config` 时，依次选择当前目录的 `runtime.yaml`、环境变量 `BIANBU_AGENT_CONFIG`、
`~/.bianbu-agent/config.yaml`。配置中的相对 `data_dir` 按配置文件所在目录解析。
配置文件必须已经存在；可以直接创建最小配置：

```yaml
# ~/.bianbu-agent/config.yaml
data_dir: .
```

上例的数据目录就是 `~/.bianbu-agent/`。也可以复制已有部署配置后修改 `data_dir`。

## 资源管理速查

以下命令均放在 `bianbu-agent [--config 文件]` 后面。

| 资源 | 子命令 |
| --- | --- |
| `agent` | `list`、`create`、`show`、`verify`、`remove` |
| `tool` / `skill` / `hook` | `list`、`install`、`show`、`verify`、`remove` |
| `plugin` | 上述包命令，以及 `members`、`effective`、`rollback` |
| `mcp` | `list`、`add`、`get`、`remove`，以及目录包的 `install`、`show`、`verify` |
| `model` | `list`、`create`、`show`、`remove`、`bootstrap-chat` |
| `session` | `list`、`create`、`show`、`remove` |

资源命令通常输出 JSON。操作失败会在标准错误输出原因；`--debug` 显示完整异常。

### Agent

```bash
bianbu-agent agent list
bianbu-agent agent create Writer --provider my-provider --model my-model
bianbu-agent agent show com.bianbu.实际ID
bianbu-agent agent verify com.bianbu.实际ID
bianbu-agent agent remove com.bianbu.实际ID
```

`create` 将模型选择写入 `agents/<ID>/agent.yaml`，生成 `com.bianbu.` 加十二位随机十六进制字符的 ID。
以返回的 ID 执行后续命令。可以重复传入 `--tool`、`--skill`、`--mcp`、`--hook` 声明资源引用。
创建不调用模型、不启动专家。单个专家的查询和校验只检查该专家的文件，不执行旧数据迁移或预先扫描全部专家。

### Tool、Skill、Hook 与 Plugin

```bash
bianbu-agent tool install /路径/echo
bianbu-agent tool list
bianbu-agent tool show echo
bianbu-agent tool verify echo
bianbu-agent tool remove echo

bianbu-agent skill install /路径/example.skill
bianbu-agent hook install /路径/example.hook

bianbu-agent plugin install /路径/plugin包
bianbu-agent plugin list
bianbu-agent plugin show vendor.bundle
bianbu-agent plugin verify vendor.bundle
bianbu-agent plugin members
bianbu-agent plugin effective
bianbu-agent plugin install /路径/plugin新版本 --upgrade
bianbu-agent plugin rollback vendor.bundle
bianbu-agent plugin remove vendor.bundle
```

`skill`、`hook` 和 `plugin` 同样支持 `list/show/verify/remove`。
目录包分别包含 `tool.py`、`SKILL.md`、`hook.py`、`plugin.yaml`。
CLI 要求 Hook 的 `hook.py` 直接定义 `create_hook` 函数。
`install` 校验并复制本地文件；`remove` 按存储实现移除或移入恢复目录，Tool 删除会实际执行并返回 `deleted`。
`members` 读取插件成员声明；`effective` 合并配置默认值、Runtime 配置和数据目录的 `plugins.yaml`，不激活插件。
`rollback` 恢复升级前保留的版本；没有可恢复版本时返回错误。

CLI 的安装和 `verify` 使用静态文件检查，包括结构、清单、适用的 Python 源码检查和内容摘要。
它们不会导入包代码、调用工厂函数或验证实际运行结果。`requirements.txt` 仍按本机已安装的 Python 依赖校验，不运行 pip。
CLI 安装不请求 securityd 签名审批，包括不执行 `BIANBU_SECURITY_CODESIGN_REQUIRE_DAEMON` 的在线检查；
需要安装期签名策略的流程应使用原有 Runtime/SDK 安装接口，其默认校验行为保留。

### MCP

```bash
bianbu-agent mcp list
bianbu-agent mcp add filesystem --command npx --args -y @modelcontextprotocol/server-filesystem /tmp
bianbu-agent mcp add weather --url https://mcp.example.com/mcp
bianbu-agent mcp get filesystem
bianbu-agent mcp remove filesystem
bianbu-agent mcp login weather
```

`add` 只把服务器定义写入 MCP 配置，不运行命令、不下载依赖；`--command` 与 `--url` 互斥且必须给出其一，
`--args`、`--env` 只适用于 stdio，`--auth {none,header,oauth}` 只适用于 URL（默认 `none`；`header`/`oauth`
会交互式读取 token 并写入本机凭据，非交互场景必须显式传 `--auth none`）。
因此上述 `npx` 示例在配置时不需要 Node.js 或网络；实际连接 MCP 时才需要相应环境。
`get/remove` 接受短名称或完整 ID；短名归一化为公开 ID `mcp.<name>`。

本节按板端 `bianbu-agent 0.1.69` 核对：更早版本以 `mcp add <name> -- <command> [args...]` 表示 stdio，
并以 `mcp install <目录包>` 安装 HTTP 服务器，当前版本已改为上面的 `--command`/`--url` 形式，
`mcp install` 子命令不再存在。

### Model

```bash
bianbu-agent model bootstrap-chat
bianbu-agent model list
bianbu-agent model create local openai-compatible example-model --base-url http://127.0.0.1:8000/v1
bianbu-agent model show local
bianbu-agent model remove local
```

`bootstrap-chat` 在终端交互填写 Provider ID、Base URL 和 API Key，并保存到模型目录。
它不探测模型服务；已配置 Provider 时立即退出，非交互终端返回错误。
`create/show/list/remove` 操作模型 YAML 文件，输出隐藏凭据；本地 Provider 主机白名单校验继续生效。
Agent 使用的具体模型选择仍由 `agent create --provider ... --model ...` 写入专家声明。

### Session

```bash
bianbu-agent session list
bianbu-agent session create session-001 --agent com.bianbu.实际ID --title 写作
bianbu-agent session show session-001
bianbu-agent session remove session-001
```

`create` 必须用 `--agent` 指定所属专家，直接写入 `sessions/<ID>.json`，不会创建 Worker 或发起对话。
`remove` 删除会话记录，不承担停止正在运行的任务或清理全部日志、检查点的职责。

## 响应时间与生效范围

帮助、查询、增删和包管理均无模型或 Worker 启动开销。小型资源的耗时主要来自 Python 启动和本地文件读写。
2026-09-08 在本机 x86_64、Python 3.14.5、小型临时资源上测量，3 轮独立进程调用的各资源命令中位耗时为 **112–199 ms**，
各级帮助的总体中位耗时约 **68 ms**。测量涵盖表中全部子命令；`bootstrap-chat` 测的是非交互终端立即返回，
不包含用户输入或服务启动时间。

同日 K3（riscv64、Python 3.14.4）在延迟加载 JSON Schema 校验器后，使用同样的小型资源完成三轮测量：
各资源命令中位耗时为 **643–940 ms**，各级帮助的总体中位耗时约 **302 ms**。
这些数据包含每次启动独立 Python 进程的开销。

`bootstrap-chat` 等待用户输入的时间，以及大包复制、摘要校验、大量资源列表和文件锁等待时间，随实际输入和存储状态变化。

文件修改不会主动调用在线重载接口。正在运行的任务可能仍持有原有资源；后续使用由 Runtime 的读取、重载逻辑决定。
`verify` 成功只表示文件检查通过，不表示模型、工具或 MCP 已能成功运行。

## Runtime 服务启动

```bash
# 前台启动控制面，Ctrl+C 停止
bianbu-agent --config /路径/config.yaml serve

# Debian 安装的 systemd 服务（以 bianbu 用户为例）
systemctl status bianbu-agent-runtime@bianbu.service
journalctl -u bianbu-agent-runtime@bianbu.service -f
sudo systemctl restart bianbu-agent-runtime@bianbu.service
sudo systemctl stop bianbu-agent-runtime@bianbu.service
```

`serve`、`serve --role control`、全局 `--role control` 以及内部 `worker` 参数保留。
兼容已有启动脚本，指定 `--config` 而省略子命令仍启动控制面；日常使用建议明确写 `serve`。
`worker` 和 `--role agent-worker` 由服务管理，不用于资源维护。服务启动和模型执行不属于资源命令的快速响应路径。

已删除顶层 `init`、`check`、`build`、`install`、`run`、`stop`、`logs`、`status`、`uninstall`、
`config`、`process`。资源子命令（例如 `tool install`）继续保留。
服务管理使用 systemctl/journalctl；资源管理使用上表命令。

# APM 实际安装/卸载功能测试指引

本指引供执行任务的 agent 阅读并操作真实 APM/agent CLI，交付测试报告，不需要开发测试程序。只有用户要求执行功能测试时才运行。各机器本地执行，开发机与板端分别出具报告，不自动调度 SSH。

## 1. 确定范围

从用户请求提取目标 agents、被测 APM 路径、执行机器和报告位置。目标 agents 未指定时，先只读探测并向用户询问目标列表；不得自行选择所有真实环境。APM 路径未指定时使用 `command -v apm`，找不到则报告阻塞。用户明确要测当前源码时，才按 [构建指南](../../BUILD.md) 构建并记录产物路径。

示例请求：

> 按安装/卸载功能测试指引，使用 /usr/bin/apm 测试 bibit、claude-code、hermes-agent，将报告写到指定目录。

这里的目标列表是给执行 agent 的任务参数，不是新 CLI 参数。APM 每条命令使用单一 `--agent <ID>`，不传 `--agents`、逗号列表或 `--agent all`。

| APM Agent ID | 真实 CLI | agent 目录变量 | 未覆盖时的真实目录 | 支持的测试 |
| --- | --- | --- | --- | --- |
| `bibit` | `bianbu-agent` | `BIANBU_AGENT_HOME` | 用户主目录下 `.bianbu-agent` | Skill、Plugin、MCP、Agent；仅板端 |
| `codex` | `codex` | `CODEX_HOME` | 用户主目录下 `.codex` | Skill、Plugin、MCP；开发机，板端不能安装 |
| `claude-code` | `claude` | `CLAUDE_CONFIG_DIR` | 用户主目录下 `.claude` | Skill、Plugin、MCP；以本机 CLI 可用性为准 |
| `hermes-agent` | `hermes` | `HERMES_HOME` | 用户主目录下 `.hermes` | Skill、Plugin、MCP；以本机 CLI 可用性为准 |

Bibit 的真实 CLI `bianbu-agent` 由板端 Bianbu 系统包 `bianbu-agent-runtime` 提供，默认镜像不一定包含该包；执行 bibit 用例前须确认该包已安装并能提供 `bianbu-agent`。包或 CLI 缺失的阻塞处理见第 3 节。

不要用目录存在、模拟可执行文件或宿主架构推断 CLI 可用。Bibit 在开发机、Codex 在板端被显式选中时记为阻塞，不尝试安装它们。未选择的 agent 记为未选择；非 Bibit 的 Agent Power 记为不适用。

## 2. 固定云端资源

| 类型 | 云端 Power | APM 安装参数 | APM 卸载参数 |
| --- | --- | --- | --- |
| `agent` | `agent/test-agent@v1.0.0` | `test-agent=v1.0.0 --type agent --agent bibit` | `test-agent --type agent --agent bibit` |
| `mcp` | `mcp/nws@v1.0.0` | `nws=v1.0.0 --type mcp --agent <ID>` | `nws --type mcp --agent <ID>` |
| `plugin` | `plugin/superpowers@v6.3.0` | `superpowers=v6.3.0 --type plugin --agent <ID>` | `superpowers --type plugin --agent <ID>` |
| `skill` | `skill/foo-before-summary@v0.0.1` | `foo-before-summary=v0.0.1 --type skill --agent <ID>` | `foo-before-summary --type skill --agent <ID>` |

强制使用 `APM_CLOUD_PROVIDER=remote`。保留用户指定的 `APC_BASE_URL`；未指定时采用 APM 默认地址，并记录实际地址。不得回退 Local provider、假 CLI、不同版本或替代 Power。APM 安装使用 `name=version`，卸载只传名称，不能直接把表中的云端标识当作命令参数。

## 3. 全局预检：在首次安装前完成

1. 记录时间、主机、操作系统、架构、用户、工作目录、所选 agents、仓库 commit（若有）。解析 APM 和目标 CLI 的绝对路径，记录版本；APM 当前没有 `--version`，可用 `sha256sum` 标识被测二进制，不能将缺失 `--version` 视为不可用。
2. 实际运行 APM 的 `--help`、`install --help`、`uninstall --help`、`show --help`、`list --help`、`fix --help`。运行每个所选 agent CLI 的版本或帮助命令，并检查 MCP 所需的 `get/list`、`add`、`remove` 子命令。Codex/Claude 还需 Plugin 与 marketplace 查询、添加、移除能力；Hermes 需 `plugins list/enable/disable`。Bibit 不要求 Plugin CLI。Bibit 的真实 CLI `bianbu-agent` 由板端系统包 `bianbu-agent-runtime` 提供，先确认该包已安装（如 `dpkg -l bianbu-agent-runtime`），再记录其绝对路径；不能把 `bibit`/`bibit-gui` 等包装命令或宿主上残留的其他同名可执行文件当作 `bianbu-agent`。
3. 目录隔离前，记录各 agent 的真实目录变量值及解析后的绝对路径，只记录有关变量，不输出整份环境或凭据。CLI 找不到、不能启动、子命令不兼容或所选平台不支持时，整个执行批次在首次安装前阻塞；提供缺失项（Bibit 情形明确到 `bianbu-agent-runtime` 系统包），不自动安装或升级软件，也不悄悄减少目标范围。
4. 在项目或用户指定的可写物理目录中，以 `mktemp -d` 创建本次专用根目录，限制权限为 `0700`。记录路径，日志与报告放在将来删除的测试工作目录之外。不重用旧测试状态，不把真实配置目录链接进临时目录。
5. 用专用临时 `APM_HOME` 和 `XDG_CACHE_HOME` 查询精确云端版本。以下命令分别执行并保存 stdout、stderr、退出码；四项都应成功且返回对应类型、名称、版本：

```bash
"$APM_BIN" show test-agent=v1.0.0 --type agent --json
"$APM_BIN" show nws=v1.0.0 --type mcp --json
"$APM_BIN" show superpowers=v6.3.0 --type plugin --json
"$APM_BIN" show foo-before-summary=v0.0.1 --type skill --json
```

上述命令必须携带本节的临时环境和 `APM_CLOUD_PROVIDER=remote`，不能在真实 APM 状态中直接执行。检查 stderr 中的 stale catalog/网络警告；缓存降级不能证明本次云端可用。云端预检失败时，报告资源或网络阻塞，不开始安装。目录查询通过不等于内容下载已通过，下载由安装步骤验证。

`APM_BIN` 是执行 agent 设置的已验证绝对路径。本文命令片段是逐步操作参考，不是可直接整段运行的脚本；环境、超时、证据保存和错误分支由执行 agent 显式处理。不要使用 `set -e` 使首次失败绕过清理，也不要通过管道丢失被测命令的退出码。

## 4. 环境分组

### 4.1 Skill、Plugin、Bibit Agent：临时目录

每个 `(agent, Power 类型)` 使用独立且全新的工作目录。使用子进程 `env` 覆盖下列变量，对 APM 和直接验证用的 agent CLI 使用相同环境；不修改父会话的 `HOME`、`PATH` 或原有 agent 配置。

| 变量 | 用例内取值 |
| --- | --- |
| `APM_HOME` | `<用例目录>/apm-home` |
| `XDG_CACHE_HOME` | `<用例目录>/cache` |
| `CODEX_HOME` | `<用例目录>/agents/codex` |
| `CLAUDE_CONFIG_DIR` | `<用例目录>/agents/claude` |
| `HERMES_HOME` | `<用例目录>/agents/hermes` |
| `BIANBU_AGENT_HOME` | `<用例目录>/agents/bibit` |
| `APM_CLOUD_PROVIDER` | `remote` |

创建必要目录；APM 依赖 agent 根目录存在来发现 agent，但这不能替代第 3 节的真实 CLI 预检。检查这些路径均位于本次用例目录，未经过指向真实配置的链接。所有资源安装都经由 APM，不能手动复制资源伪造安装成功。

Plugin 的隔离必须同时覆盖真实 CLI。写入前，根据所安装 CLI 的帮助、随附文档或官方说明确认对应变量确实控制 Plugin/marketplace 配置；在临时环境执行只读 Plugin 查询，核对其配置位置及初始列表。空列表本身不足以证明隔离。若 CLI 忽略覆盖、实际位置无法确认或隔离环境要求初始化而无法完成，该项阻塞，不改用真实目录。不要复制真实认证配置来初始化测试目录。

保留有关真实 Plugin 注册和配置的测试前后摘要，验证临时环境中的操作未改变真实安装；既有 `superpowers` 尤其不能卸载。不要因本会话正在使用同名插件而操作它。

### 4.2 MCP：真实 agent 环境

每个 agent 的 MCP 用例也使用全新的临时 `APM_HOME` 和 `XDG_CACHE_HOME`，但 agent 目录变量必须指向第 3 节记录的真实目录，直接验证 CLI 与 APM 子进程使用同一组值。保留原来的真实 `HOME`、CLI 身份和配置上下文。显式设置真实目录是为了防止 APM 因临时 `APM_HOME` 把 agent 根目录解析到临时位置。

这只隔离 APM 的清单、缓存和事务；`nws` 的注册、查询、卸载必须通过真实 CLI 在真实 agent 环境完成，不使用虚拟 MCP 配置。工作目录选本次空白用例目录，避免项目级 MCP 同名覆盖；Claude 的安装和卸载由适配器使用 user scope。

写入前通过第 6 节的真实 CLI 查询 `nws`，并保存完整 MCP 列表的脱敏基线。真实 APM 清单若已有受管 `nws` 也视为冲突，可只读检查相关 manifest，不运行真实环境的 `fix`。如果 `nws` 已存在，或无法确定是否存在，该 agent 的 MCP 项记为阻塞；不得覆盖、卸载后重装或临时改名。其他独立用例可以继续。

查询非零退出码不一定表示不存在；必须区分 CLI 明确的 not-found 结果与启动、认证、超时、解析错误。MCP 测试串行运行，不与其他 APM/agent 配置写入同时执行。

## 5. 每项测试的操作顺序

所选每个 agent 执行 Skill、Plugin、MCP；Bibit 额外执行 Agent。建议先完成文件型资源，再测试真实 MCP。每项执行以下步骤，`<安装参数>`、`<卸载参数>` 来自第 2 节，所有命令使用第 4 节为该项设置的环境。

| 步骤 | 操作 | 通过条件 |
| --- | --- | --- |
| 1. 基线 | `apm list --installed --agent <ID> --type <类型> --json`，并检查实际目标 | APM 无该项清单，实际目标不存在，隔离/真实环境符合约定 |
| 2. 安装 | `apm install <安装参数>` | 退出码 0，准确的 agent/type/name/version，结果为 `installed` |
| 3. 安装验证 | 再次查询 installed JSON，并按第 6 节检查实际目标 | 清单精确版本正确，文件或真实 CLI 状态与安装一致；不能只看输出或缓存 |
| 4. 同版本重装 | 再执行相同安装命令，并复查状态 | 退出码 0，结果 `unchanged`，资源无重复注册 |
| 5. 卸载 | `apm uninstall <卸载参数>` | 退出码 0，结果 `completed` |
| 6. 卸载验证 | 查询 installed JSON，并复查实际目标 | 该清单项和实际安装均消失，无待恢复事务 |
| 7. 重复卸载 | 再执行相同卸载命令，并复查状态 | 退出码 0，结果 `unexecuted`，资源仍不存在 |

执行命令时用已验证的 `"$APM_BIN"` 替代上表的 `apm`。例如 Codex Skill 安装为：

```bash
"$APM_BIN" install foo-before-summary=v0.0.1 --type skill --agent codex
"$APM_BIN" list --installed --agent codex --type skill --json
"$APM_BIN" uninstall foo-before-summary --type skill --agent codex
```

以上示例同样需要完整的临时环境前缀。用例不得省略 `--agent` 或 `--type`。APM 清单位于 `$APM_HOME/.apm/manifests/<ID>.json`，必要时可只读检查；主判据仍包括实际落地状态。

每步记录开始/结束时间、环境组、工作目录、实际命令、stdout/stderr 和退出码。为命令设定合理超时（例如 APM 安装 180 秒、直接 CLI 查询 60 秒），超时后确认相关子进程已退出，再查询状态和清理。不得因命令失败跳过状态检查，也不得用重试成功掩盖首次失败。

本指引固定单一版本，不覆盖升级/降级、故障注入、Skill 内容执行、Agent 对话或 MCP 工具调用。云端下载和真实安装协议属于覆盖范围。

## 6. 实际状态验证

### 文件型资源

- **Skill**：检查 `<agent根>/skills/foo-before-summary/SKILL.md` 及下载制品中附带文件确实落地，内容与本次制品一致；不能只检查目录存在。卸载后整个目标目录不存在。
- **Bibit Agent**：检查 `<agent根>/agents/test-agent/agent.yaml`，以及云端包实际含有的 `assets/` 等内容；比较安装文件与本次缓存/制品内容，不强行要求源包没有的可选文件。卸载后整个 `test-agent` 目录不存在。
- **Bibit Plugin**：检查 `<agent根>/plugins/superpowers` 下本次制品包含的 manifest 和 skills，确认内容完整；Bibit 不调用 Plugin CLI。卸载后目标目录不存在。
- **Hermes Plugin**：检查 `<agent根>/plugins/superpowers` 内容，并用相同临时环境的 `hermes plugins list --json` 确认安装及启用状态。卸载后目录移除、CLI 无有效安装或启用记录。

### Codex / Claude Plugin

在用例临时环境执行：

| Agent | Plugin 查询 | Marketplace 查询 |
| --- | --- | --- |
| Codex | `codex plugin list --json` | `codex plugin marketplace list --json` |
| Claude | `claude plugin list --json` | `claude plugin marketplace list --json` |

安装后应存在逻辑名 `superpowers`，原生版本与 `v6.3.0` 对应，且绑定本次 APM 生成的 `apm-superpowers` 本地 marketplace；核对 source、selector、安装位置和原生 manifest/skills，路径应落在本次隔离范围。原生格式可能省略版本的 `v` 前缀，但不能接受不同版本。无法查询或解析实际状态时不判通过。

卸载后 Plugin 和专属 `apm-superpowers` marketplace 都应消失。APM 保留下载缓存/共享 artifact 是正常行为，不要求卸载删除 cache；缓存保留不能被误判为仍安装。

### 真实 MCP

以下命令按原始参数调用真实 CLI，使用第 4.2 节的真实配置环境；MCP 列表基线可用该 CLI 经帮助确认的 `mcp list` 命令采集。

| Agent | 精确状态查询 |
| --- | --- |
| Bibit | `bianbu-agent mcp get nws` |
| Codex | `codex mcp get --json nws` |
| Claude | `claude mcp get nws` |
| Hermes | `hermes mcp list`，从结果精确匹配名称 `nws` |

安装后确认 `nws` 存在，并核对 CLI 可展示的传输类型、URL 或启动命令与本次云端 MCP 定义一致；APM 的清单还必须记录 `v1.0.0`。若 CLI 不展示完整定义，报告可验证字段及限制，不猜测；不得打印 token 或敏感环境变量值。

Bibit 的公开 ID 为 `mcp.<name>`；`mcp get` 不存在时输出字面量 `null`（exit 0），存在时输出含 `id` 的对象，两者都不报错，因此不能以非零退出码判断不存在。`mcp get` 不展示 transport/url，核对 HTTP 定义时可只读检查 `<agent根>/mcps/mcp.<name>/mcp.json`。

卸载后确认 `nws` 不存在，并比较基线中的其他 MCP 配置未发生变化。`list` 可能连接服务，耗时或连接失败应记录；本项不以天气工具调用成功为通过条件，但查询结果不明确时不能宣称完成验证。

## 7. 异常与清理

1. 任一步失败，先保存错误、清单、实际状态和 `$APM_HOME/.apm/transactions/active.json`（若存在）的证据。报告中区分预检阻塞和已开始执行后的失败，不把清理成功改写成测试通过。
2. 清理只针对本次创建的资源，沿用完全相同的 APM_HOME、agent 根、缓存、工作目录及 CLI 路径。优先执行对应的 APM 卸载；若存在本次专用状态下的活动事务，可执行 `"$APM_BIN" fix`，复查后再卸载。不能在真实 APM 状态根执行 `fix`。
3. APM 无法清理真实 MCP 时，只有基线已证明原先不存在、当前定义也能确认属于本次安装，才能用真实 CLI 定点移除 `nws`。实际参数先由本机帮助确认：Bibit/Codex/Hermes 为 `mcp remove nws`，Claude 为 `mcp remove --scope user nws`。移除后重新查询并核对其他条目。此操作是异常补救，原卸载用例仍失败。
4. 不恢复整份真实配置快照，不手改真实 JSON/TOML，不删除原有 MCP 或 Plugin。无法确认归属、有并发配置变更或清理失败时，停止该环境后续写入，保留专用工作目录和事务文件，并在报告给出具体残留路径、状态及带环境上下文的后续命令。
5. 只有实际资源已移除且证据已保存，才能删除本次临时工作目录。删除前验证其为本次创建的专用目录，不是用户主目录、仓库根或真实 agent 根。日志和报告保留；缓存随临时目录删除即可。

清理是一项独立结果，记录成功、失败或无需清理。即使安装命令超时或返回失败，也必须检查是否已产生资源。残留真实 MCP、真实配置变化或清理失败时，整体不能判通过。

## 8. 输出报告

使用 [报告模板](install-uninstall-report-template.md)，默认将报告与脱敏日志保存在用户指定位置；未指定时可使用仓库 `artifacts/install-uninstall/<时间>-<主机>/`，不要自动提交运行产物。

- 每个所选 agent 的每类 Power 独立一行，包括不适用的 Agent Power。
- `PASS`：全部适用步骤和清理验证通过。
- `FAIL`：已执行的命令、状态断言或清理不符合要求。
- `BLOCKED`：平台、CLI、云端、目录隔离或既有资源冲突阻止开始相应测试。
- `N/A`：适配器不支持该类型；`NOT_SELECTED`：用户未选择；`NOT_RUN`：原计划执行但尚未执行，注明原因。

整体结论先报 FAIL，其次 BLOCKED/NOT_RUN；仅当全部所选适用项 PASS 且无残留时才报 PASS。开发机报告不能声称板端 Bibit 已通过，板端报告不能声称 Codex 已通过。合并报告时保留每台机器的证据链接与环境信息。

## 9. 现有约定

本指引复用现有能力，不新增 CLI、测试框架或运行时接口。相关依据：

- [ADR-005：APM_HOME 隔离](../../ivory-tower/adr/ADR-005-isolate-test-filesystem-with-apm-home.md)。
- [ADR-016：显式选择 Cloud provider](../../ivory-tower/adr/ADR-016-select-cloud-clients-with-explicit-providers.md)。
- [ADR-022：生命周期入口状态语义](../../ivory-tower/adr/ADR-022-unify-lifecycle-entry-state-semantics.md)。
- [构建指南及 Adapter 约定](../../BUILD.md)、`internal/adapter` 中对应适配器。

仓库单元/集成测试继续使用假 CLI；本指引不接入默认 CI。若以后改变隔离、真实环境写入或测试调度等架构边界，应在 ADR 中记录决策，并在本指引引用对应 ID。

本流程不依赖 Python。若在远程设备额外使用 Python 辅助处理，须遵守项目 AGENTS.md 引用的 `~/.codex/docs/k3/remote-device-python.md`：仅 Python 3.13、项目本地 `.venv313`，激活后按规定配置包索引再安装依赖。

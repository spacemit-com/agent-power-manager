# APM CLI 生命周期流程图设计

## 背景与目标

`ivory-tower/c4/components/apm-cli.dsl` 中的 `Core Lifecycle` component 已有类图，但尚未描述 `search`、`list`、`install`、`uninstall` 四条命令的完整执行流程，也没有展开各阶段失败或进程中断后的处理措施。

本次工作为四条命令分别增加 Mermaid 流程图，并通过 component properties 挂载。流程图同时固化以下已确认的生命周期语义：

- 四条命令进入生命周期后均持有同一把全局锁，APM 串行执行。
- 每条命令先检查并修复唯一活动 journal，再执行用户请求。
- install/uninstall 根据原子提交点选择回滚或完成提交。
- 恢复行为必须通过命令行日志明确告知用户。
- `--agent=all` 下 install/uninstall 任一 Agent 失败后立即停止剩余 Agent。

本任务只维护架构与设计文档，不实现 Go 代码，也不创建 feature 目录。

## 文件组织与 Component Properties

新建独立目录，按命令保存纯 Mermaid `flowchart` 源文件：

```text
ivory-tower/c4/components/apm-cli-lifecycle-diagrams/
├── search.mmd
├── list.mmd
├── install.mmd
└── uninstall.mmd
```

`Core Lifecycle` component 保留现有 `classDiagram` property，并新增：

```text
"searchLifecycle" "apm-cli-lifecycle-diagrams/search.mmd"
"listLifecycle" "apm-cli-lifecycle-diagrams/list.mmd"
"installLifecycle" "apm-cli-lifecycle-diagrams/install.mmd"
"uninstallLifecycle" "apm-cli-lifecycle-diagrams/uninstall.mmd"
```

路径相对于 `apm-cli.dsl` 所在目录。Structurizr Local 的 Properties 面板只显示文件路径，不直接渲染 Mermaid；流程图通过支持 Mermaid 的编辑器或查看器打开。

## 公共启动与 Journal 恢复

四张流程图从 CLI 完成命令路由并调用生命周期接口后开始，共用以下前置流程：

1. 获取全局 APM 文件锁，并持有至本条命令完全结束。
2. 检查唯一活动 journal。
3. journal 不存在时执行用户命令。
4. journal 未完成时，根据事务阶段和文件路径状态选择回滚或完成提交。
5. 修复成功后删除 journal，继续执行用户原命令。
6. 修复失败时保留 journal，打印错误并以非零状态退出，不执行用户原命令。

全局串行和修复失败即退出共同保证：正常受支持的运行状态中，任一时刻至多存在一个未完成 journal。本设计不处理手工篡改或复制状态目录、旧版本缺陷、磁盘损坏等外部异常导致的多个 journal。

### 提交点与中断窗口

install 和 uninstall 均在执行原子文件操作前，把意图阶段持久化到 journal：

- install 使用 `publishing`，随后将 staging 原子 rename 到正式安装位置。
- uninstall 使用 `removing`，随后将正式安装内容原子移动到事务隔离目录。

原子文件操作是事务提交点。为覆盖“原子操作已完成、但 journal 尚未来得及写入下一阶段”的中断窗口，恢复逻辑结合 journal 阶段与 staging、正式路径、备份或隔离路径的存在状态判断提交点是否已经越过。在正常串行状态下，这些路径组合必须具有唯一解释。

- 提交点前中断或失败：回滚到操作前状态。
- 提交点后中断或失败：补全剩余提交步骤。

### 恢复日志

恢复日志至少包含：

- 原始操作类型；
- Agent、Power 和版本；
- journal 记录的原始阶段；
- 选择的恢复方向：回滚或完成提交；
- 已恢复、补写、删除或保留的内容；
- 恢复结果及失败原因。

锁获取失败同样打印明确错误并以非零状态退出。只读命令在公共预检之后不创建 journal；若进程中断，仅由内核释放全局锁。

## Search 生命周期

`search` 必须提供关键词，只查询云端 Power 资源：

1. 完成公共锁与 journal 预检。
2. 校验关键词以及 `--agent`、`--type` 等参数。
3. 调用云端列表接口执行关键词搜索并应用过滤条件。
4. 打印匹配资源并正常退出。

参数校验、Agent/Adapter 解析、网络请求、协议解析或云端响应失败时，日志指出失败阶段与原因，并以非零状态退出。搜索阶段不修改本地状态，也不创建新 journal。

## List 生命周期

`list` 有两条读取路径：

- 默认调用云端接口列出资源，并应用 `--agent`、`--type` 过滤。
- `--installed` 读取目标 Agent 的本地安装清单。

两条路径都先完成公共锁与 journal 预检。云端请求失败时打印错误并非零退出。本地清单不存在时视为空列表；清单存在但无法读取或解析时视为失败。

`list --installed --agent=all` 按 Agent 分组读取并输出。单个 Agent 清单失败时记录该 Agent 的错误并继续读取其他 Agent，最终按 Agent 汇总；存在任一失败时整体返回非零状态。list 的读取阶段不创建新 journal。

## Install 生命周期

### 版本与既有安装

install 参数支持 apt 风格的 `<power>[=<version>]`：

- 未指定版本时选择云端最新版。
- 显式版本必须能从云端解析；显式指定旧版本时允许降级。
- 目标 Agent 已安装相同版本时，提示已是目标版本并视为成功。
- 已安装版本不同则进入升级或降级事务。
- 尚未安装则进入首次安装事务。

公共预检后，生命周期获取 Power 元数据、推断或校验 Power 类型并解析 Adapter。这些步骤尚未产生事务写入，失败时直接打印错误并退出。

### 安装事务

首次本地临时写入发生前创建 install journal。下载和缓存阶段属于安装事务：

1. 缓存命中时校验缓存内容。
2. 缓存未命中时下载到临时文件。
3. 校验下载内容后原子写入内容寻址缓存。
4. Adapter 在 staging 中完成格式转换与安装内容准备。
5. 升级或降级时，把旧版本原子移动到事务备份目录；首次安装跳过该步骤。
6. 持久化 `publishing` 意图。
7. 将 staging 原子 rename 到正式安装位置，越过提交点。
8. 补全 Adapter 注册。
9. 原子更新安装清单。
10. 删除事务备份、下载临时文件和 journal。

共享缓存中已经完整写入且校验通过的条目在回滚时保留；未完成的缓存临时文件由事务回滚清理。

提交点前失败时立即回滚：清理临时下载和 staging，并在升级场景恢复旧版本。提交点后失败时立即尝试完成 Adapter 注册、清单更新和清理。恢复仍失败则保留 journal，打印错误并非零退出。进程中断由下一条任意命令的公共预检执行相同的回滚或提交补全。

命令行输出明确区分首次安装、升级、降级、相同版本跳过、回滚、完成提交及具体失败阶段。

### 多 Agent 语义

`--agent=all` 只在生命周期开头执行一次公共 journal 检查，然后按确定顺序串行处理 Agent。任一 Agent 出现安装错误，命令立即停止，剩余 Agent 标记为未执行；输出已完成、失败和未执行的汇总，并以非零状态退出。该 fail-fast 规则避免在错误 journal 尚未解决时启动另一事务。

## Uninstall 生命周期

uninstall 不依赖云端服务：

1. 完成公共锁与 journal 预检。
2. 读取目标 Agent 清单，以名称和可选 `--type` 定位安装记录。
3. 未找到记录时提示“未安装、无需处理”，视为幂等成功。
4. 同名跨类型产生歧义时，要求显式指定 `--type`。
5. 解析 Adapter。
6. 在首次本地变更前创建 uninstall journal，记录 Agent、Power、安装路径、注册信息和隔离路径。
7. 持久化 `removing` 意图。
8. 将正式安装内容原子移动到事务隔离目录，越过提交点。
9. 补全 Adapter 注销。
10. 原子删除安装清单记录。
11. 清理隔离内容和 journal。

提交点前失败时恢复原状态；提交点后失败时尝试完成卸载。修复仍失败则保留 journal，打印错误并非零退出。进程中断由下一条任意命令的公共预检按相同规则恢复。

`uninstall --agent=all` 同样按 Agent 串行且 fail-fast：任一 Agent 卸载失败后停止并将剩余 Agent 标记为未执行。日志明确区分未安装跳过、开始卸载、回滚卸载、完成卸载提交和修复失败。

## 相关设计文档同步

本次澄清产生了现有 Ivory Tower 未记录的重大决策，并改变了 ADR-002 的部分既有语义。因此实施流程图时同步维护：

- 新建 ADR-003，记录统一全局锁、公共 journal 预检、原子提交点恢复、单活动 journal、恢复失败退出，以及 install/uninstall 多 Agent fail-fast 语义。
- 更新 ADR-002：
  - install 支持 `<power>=<version>`；
  - search 必须提供关键词；
  - list 默认列出云端资源，`--installed` 列出本地清单；
  - 用 ADR-003 的 fail-fast 规则替换当前“单个 Agent 失败后继续”的描述。
- 更新 `ivory-tower/project-overview.md`，引用 ADR-003 并同步命令和恢复语义。
- 更新现有 CLI Entry、Core Lifecycle 和 Lock Manager 类图中已经失效的 flag、锁范围及生命周期说明。

此处选择新建 ADR-003，而不是把事务恢复细节全部放入 ADR-002，因为 ADR-002 的主题是命令行维度表达，ADR-003 的主题是跨命令事务与恢复语义。ADR-002 只保留必要的语法和交叉引用。

## Mermaid 表达约束

四张图采用一致的视觉语法：

- 矩形表示操作或阶段。
- 菱形表示判断与成功/失败分支。
- 明确标出公共预检、journal 创建、原子提交点和 journal 删除。
- 失败边标注错误类型或恢复方向。
- 终止节点区分正常退出与非零退出。
- install/uninstall 用子图或样式区分提交点前与提交点后。
- 每个可能失败或中断的阶段必须连接到直接退出、回滚、完成提交或下次启动恢复之一，不允许悬空分支。

图只表达已确认的正常状态与可预期运行错误，不表达手工篡改、旧版本缺陷、磁盘损坏或多个异常 journal。

## 验证与验收

实施完成后执行以下验证：

1. 四个 `.mmd` 文件通过 Mermaid 语法解析。
2. `apm-cli.dsl` 中四个 lifecycle property 的相对路径均指向实际文件。
3. Structurizr DSL 能解析更新后的 workspace。
4. 四张图均从全局锁和公共 journal 预检开始。
5. search/list 的运行阶段不创建 journal。
6. install/uninstall 明确标出 journal 创建、意图记录、原子提交点、回滚、完成提交和 journal 清理。
7. 所有失败和中断分支都有明确处理措施及退出状态。
8. install/uninstall 的 `--agent=all` 均表达 fail-fast 和剩余 Agent 未执行。
9. `list --installed --agent=all` 表达逐 Agent 继续读取、最终汇总和部分失败非零退出。
10. ADR、项目概览、类图与流程图之间不存在锁范围、CLI 语义或多 Agent 行为冲突。

本任务仅修改设计文档，不改变源代码、构建/运行配置或脚本，因此不写 `docs/Changelog.md`。

## 范围外事项

- Go 代码实现与测试代码。
- Structurizr 内直接渲染 Mermaid。
- 并行或并发执行 APM 命令。
- 多个异常 journal 的恢复。
- 手工篡改、旧版本缺陷、磁盘损坏等外部异常恢复。
- Power 兼容版本选择规则；除“未指定版本选择云端最新版”外，不定义兼容性求解。

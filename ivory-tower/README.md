# Ivory Tower

Ivory Tower 是一个面向软件设计的文档预设目录。它将系统级架构、关键决策和特性级设计放在同一处，使人和 AI Agent 能以同一份上下文协作实现。

工程代码应在 `ivory-tower` 的同级目录中开发；工程和本文档目录同步维护。

## Document Types

| 目录 | 用途 |
| --- | --- |
| `project-overview.md` | 项目设计起点：目标、范围、架构快照、约束、风险和待决事项。 |
| `c4/` | C4 架构模型的 Structurizr DSL 骨架。`workspace.dsl` 是入口，并通过 `!include` 引用 containers 与 components 的分片。 |
| `adr/` | 重大架构或产品决策记录。其他文档以 ADR ID 引用。 |
| `features/` | 特性边界、概览、规格和实施计划。每个特性独立建目录。 |

## Dependencies

Markdown 文档无需运行时依赖。以下工具仅在需要本地可视化或 AI Agent 工作流时安装：

| 工具 | 用途 | 安装方式 |
| --- | --- | --- |
| Docker | 运行 Structurizr Local 并浏览 C4 图。 | 安装 Docker Engine 或 Docker Desktop。 |
| Node.js（含 `npx`） | 安装 grill 技能。 | 从 Node.js 官方发行版安装。 |
| Superpowers | 生成特性规格和实施计划。 | Agent CLI 运行 `/plugins` 后搜索并安装。 |
| grill-with-docs | 通过提问梳理设计，并将术语与 ADR 写入文档。 | `npx skills@latest add mattpocock/skills --skill=grill-with-docs` |

## DSL Local Visualization

Structurizr Local 使用 Docker 提供本地 DSL 可视化。`c4/workspace.dsl` 是唯一入口。在 `ivory-tower` 目录执行：

```bash
docker pull structurizr/structurizr
docker run -it --rm --user "$(id -u):$(id -g)" -p 127.0.0.1:8080:8080 \
  -v "$(pwd)/c4:/usr/local/structurizr" \
  structurizr/structurizr local
```

访问 <http://localhost:8080>，选择图后可在浏览器中调整布局。修改 `workspace.dsl` 或任一被引用分片并保存，刷新页面即可查看整体更新；停止服务请在终端按 `Ctrl+C`。

详细步骤见 [Structurizr Local Quickstart](https://docs.structurizr.com/local/quickstart)。

## Usage

先创建工程并进入其根目录，再将本预设作为文档目录引入：

```bash
git clone git@gitlab.dc.com:liangjunzhao/ivory-tower.git ivory-tower
rm -rf ivory-tower/.git
mv ivory-tower/AGENTS.md ./AGENTS.md
```

`rm -rf` 的目标必须是克隆副本的 `ivory-tower/.git`；保留工程根目录自己的 `.git`。完成后，`ivory-tower/` 是工程内的设计文档目录，项目根目录的 `AGENTS.md` 则为 AI Agent 的入口说明。

## Workflow

```text
初始化系统:
    填写 project-overview.md，作为项目设计起点
    维护 c4/workspace.dsl 及其 DSL 分片

for 人工拆分的独立 feature:
    创建 features/NNN-feature-name/
    先填写 feature-overview.md，明确目标、范围和实现上下文
    通过 grill-with-docs 补充设计并保存结果
    使用 Superpowers brainstorming 生成 spec 和 plan

    if 产生重大决策:
        在 adr/ 创建 ADR，并在相关文档引用 ADR ID

    为 feature 创建独立 worktree
    在同级工程中实现计划，并同步更新文档
```

`spec.md` 和 `plan.md` 仅作为流程占位；实际格式由 Superpowers 在对应 feature 中生成。

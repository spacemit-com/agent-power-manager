# Plugin 模板

Plugin 提交对象是一个**目录**，含权威索引 `apc-plugin.json` 与 `skills/` 下声明的内嵌 Skill：

```text
my-plugin/
├── apc-plugin.json
└── skills/
    └── my-plugin/
        ├── SKILL.md
        └── skill-card.md
```

## apc-plugin.json

```json
{
  "schema_version": 2,
  "name": "my-plugin",
  "name_zh": "我的插件",
  "name_en": "My Plugin",
  "version": "v1.0.0",
  "description": "一句话说明这个 Plugin 做什么。",
  "description_zh": "一句话中文说明这个 Plugin 做什么。",
  "skills": ["skills/my-plugin"]
}
```

- `schema_version` 为 `2`；`name`/`version`/`description` 走公共规则；`name_zh`/`name_en`/`description_zh` 必填且仅用于展示（长度规则见公共规则表）。
- `skills` 每项是单层 `skills/<canonical-name>` 相对路径；`skills/` 下目录数必须与声明数一致。

## skills/my-plugin/SKILL.md

```markdown
---
schema_version: 2
name: my-plugin
name_zh: 我的插件
name_en: My Plugin
version: "v1.0.0"
description: 内嵌 Skill 的一句话说明。
description_zh: 内嵌 Skill 的中文说明。
---
# My Plugin Skill

正文内容，可自由编写。
```

## skills/my-plugin/skill-card.md

```markdown
---
schema_version: 2
---
# Skill Card: My Plugin

## Capability

这个内嵌 Skill 提供的能力说明。

## Use Cases

- 适用场景一。

## Inputs and Outputs

- Input: 输入说明。
- Output: 输出说明。

## Dependencies

- 无。

## Limitations

- 已知限制说明。

## Maintainer

Owner: your-team
Support: your-team@example.com
```

- 内嵌 Skill 遵循与独立 Skill 相同的 `SKILL.md`/`skill-card.md` 规则（`SKILL.md` 含 `schema_version: 2` 与 `name_zh`/`name_en`/`description_zh`，`skill-card.md` 的 `schema_version` 为 `2`）：`# Skill Card:` 必须紧跟在 frontmatter 结束的 `---` 之后成为正文首行，Maintainer 同样写成裸 `Owner:` / `Support:` 行。
- 目录内只允许 `apc-plugin.json`、`skills/` 与已声明 Skill 子树，不要放入额外文件或 Agent 原生 manifest；`plugin.yaml` 由 APM 在安装时从 `apc-plugin.json` 派生（含 `schema_version: 1`，只含 `schema_version`/`name`/`version`/`description` 四个字段，不含 `author`），且按目标 Agent 落入独立落盘叶子，不得随包提交（见 [ADR-036](../ivory-tower/adr/ADR-036-per-agent-plugin-landing-filtering.md)）。

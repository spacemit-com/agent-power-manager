# Skill 模板

Skill 提交对象是一个**目录**，内含 `SKILL.md` 与 `skill-card.md` 两个文件：

```text
my-skill/
├── SKILL.md
└── skill-card.md
```

## SKILL.md

```markdown
---
name: my-skill
version: "v1.0.0"
description: 一句话说明这个 Skill 做什么。
---
# My Skill

正文内容，可自由编写。
```

- `name` 为 1–64 字节小写 kebab（如 `my-skill`）。
- `version` 为 `vMAJOR.MINOR.PATCH`。
- `description` 非空。

## skill-card.md

```markdown
---
schema_version: 1
---
# Skill Card: My Skill

## Capability

这个 Skill 提供的能力说明。

## Use Cases

- 适用场景一。
- 适用场景二。

## Inputs and Outputs

- Input: 输入说明。
- Output: 输出说明。

## Dependencies

- 无，或列出依赖。

## Limitations

- 已知限制说明。

## Maintainer

Owner: your-team
Support: your-team@example.com
```

- `schema_version` 必须为整数 `1`。
- 标题为 `# Skill Card: <显示名>`，必须紧跟在 frontmatter 结束的 `---` 之后成为正文首行；中间留空行会被判为「must start with `# Skill Card: <display name>`」。
- 六个章节均须非空。
- Maintainer 需有非空 `Owner` 与 `Support`（或 `Contact`），且写成裸 `Owner:` / `Support:` 行；写成 `- Owner:` 列表项会被判为「no non-empty Owner」。不要使用 `REPLACE_OWNER` / `REPLACE_CONTACT` 占位符。

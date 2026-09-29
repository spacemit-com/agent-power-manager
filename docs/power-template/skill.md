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
schema_version: 2
name: my-skill
name_zh: 我的技能
name_en: My Skill
version: "v1.0.0"
description: 一句话说明这个 Skill 做什么。
description_zh: 一句话中文说明这个 Skill 做什么。
---
# My Skill

正文内容，可自由编写。
```

- `schema_version` 必须为整数 `2`（提交侧只接受 v2，见 [ADR-038](../ivory-tower/adr/ADR-038-power-format-v2-localized-display-fields.md)）。
- `name` 为 1–64 字节小写 kebab（如 `my-skill`），同时是调用名与默认英文名。
- `version` 为 `vMAJOR.MINOR.PATCH`。
- `description` 非空，用于 Agent 发现（写清触发场景）。
- `name_zh` 与 `name_en` 提交时必填，仅用于 APC 前端展示：`name_zh` 为 2–16 个字符（按 Unicode 字符计），`name_en` ≤ 32 个字符且必须是可打印 ASCII、至少含一个字母（[ADR-039](../ivory-tower/adr/ADR-039-add-name-en-and-display-name-length-limits.md)）。
- `description_zh` 提交时必填，≤ 500 个 UTF-16 码元（与 `description` 共用同一上限；单位是 UTF-16 码元，不是字节，见 [ADR-042](../ivory-tower/adr/ADR-042-cap-description-length-to-apc-server-limit.md)）。

## skill-card.md

```markdown
---
schema_version: 2
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

- `schema_version` 必须为整数 `2`（结构未变，升版本只为与 `SKILL.md` 一致）。
- 标题为 `# Skill Card: <显示名>`，必须紧跟在 frontmatter 结束的 `---` 之后成为正文首行；中间留空行会被判为「must start with `# Skill Card: <display name>`」。
- 六个章节均须非空。
- Maintainer 需有非空 `Owner` 与 `Support`（或 `Contact`），且写成裸 `Owner:` / `Support:` 行；写成 `- Owner:` 列表项会被判为「no non-empty Owner」。不要使用 `REPLACE_OWNER` / `REPLACE_CONTACT` 占位符。

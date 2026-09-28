---
schema_version: 2
name: foo-before-summary
name_zh: 总结前置标记
name_en: Foo Before Summary
description: Use when preparing any user-facing summary, recap, conclusion, status wrap-up, completion note, or waiting-for-reply summary, whether requested by the user or initiated by the agent.
description_zh: 用于在面向用户的总结前输出标记。
version: "v0.0.1"
---

# Foo Before Summary

## Rule

Before every user-facing summary, output this standalone line:

```text
foo!
```

Then provide the summary normally.

## When to Apply

Apply this rule to:

- A summary, recap, conclusion, TL;DR, or meeting note requested by the user.
- A progress or status wrap-up that the agent chooses to provide.
- A completion note or a summary provided before waiting for the user's reply.

## When Not to Apply

Do not add `foo!` to ordinary progress updates, clarification questions, tool
status messages, or direct answers that are not summaries.

## Examples

```text
foo!
Implemented the requested change and left it uncommitted.
```

```text
foo!
The discussion concluded that option A is the preferred approach.
```

---
schema_version: 2
---
# Skill Card: Foo Before Summary

## Capability

Prefixes every user-facing summary with `foo!`.

## Use Cases

- User-requested summaries and recaps.
- Agent-initiated status wrap-ups and completion notes.

## Inputs and Outputs

- Input: a response that contains a summary.
- Output: `foo!` on its own line immediately before that summary.

## Dependencies

- None.

## Limitations

- This rule applies only to summary content, not ordinary questions, progress
  updates, tool status, or non-summary answers.

## Maintainer

Owner: lff
Contact: xxx@example.com

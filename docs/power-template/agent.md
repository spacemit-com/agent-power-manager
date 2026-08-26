# Agent 模板

Agent 提交对象是**单个原始 `agent.yaml` 文件**，APM 只解析最小字段，其余字段安装时原样透传。

```yaml
schema_version: 1

agent:
  id: my-agent
  version: v1.0.0
  description: 一句话说明这个 Agent 做什么。
  # 以下为示例扩展字段，按需保留，APM 原样透传。
  model: your-model
  tools:
    - "*"
```

约束：

- 单文档，所有 mapping 键为字符串且无重复，根与 `agent` 均为 mapping。
- `schema_version` 为整数 `1`。
- `agent.id` 为 canonical 名称且是安全单路径段（拒绝 `/`、`\`、绝对路径、`.`、`..`）。
- `agent.version` 为 `vMAJOR.MINOR.PATCH`。
- `agent.description` 非空。
- 其余字段原样透传，提交时不做重序列化，MD5 针对原始字节。

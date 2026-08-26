# MCP 模板

MCP 提交对象是**单个完整 JSON 文件**（如 `my-mcp.json`），包含 `schema_version`/`name`/`version`/`description`/`definition` 五个必填键。

## stdio 示例

```json
{
  "schema_version": 1,
  "name": "my-mcp",
  "version": "v1.0.0",
  "description": "一句话说明这个 MCP 服务做什么。",
  "definition": {
    "transport": "stdio",
    "command": "npx",
    "args": ["-y", "some-mcp-server"]
  }
}
```

## http 示例

```json
{
  "schema_version": 1,
  "name": "my-mcp",
  "version": "v1.0.0",
  "description": "一句话说明这个 MCP 服务做什么。",
  "definition": {
    "transport": "http",
    "url": "https://example.com/mcp"
  }
}
```

约束：

- `transport` 只能是 `stdio` 或 `http`，且两种模式字段互斥：
  - stdio 要求非空 `command`，禁止 `url`；`args` 可选。
  - http 要求绝对 HTTPS `url`（无 userinfo），禁止 `command`/`args`。
- 字段「出现即拒绝」：即使值为 `null`、空串或空数组，只要字段出现即按出现处理。
- 不要写入 `env`、`headers`、`token`、`password`、`cookie` 等秘密字段。

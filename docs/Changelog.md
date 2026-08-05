# Changelog

## 2026-08-05

分类：新增功能

用户需求：

初始化 APM Go module，并提供 CLI 基础设施使用的共享领域类型。

相关提交：
- `97a738b chore: initialize APM Go module`

解决方案或完成结果：

创建 `apm` Go module，加入 Cobra 与 flock 依赖，并提供智能体、能力和执行结果领域类型。

验证结果或限制：

`go test ./...` 和 `go vet ./...` 均通过。`Contains` 与 `Ordered` 按需求保留零值占位实现。

# Feature 013 Amendment：Journal Schema 开发期重置设计

日期：2026-08-13  
状态：已确认，待实施  
关联：[Feature 013](spec.md)、[ADR-010](../../adr/ADR-010-use-type-specific-drivers-for-recoverable-power-transactions.md)

## 背景

Feature 013 为 Skill 文件系统事务和 MCP Agent CLI 事务引入了共享 journal 信封。当前实现把旧的平铺 Skill journal 称为 schema v1，把带显式 `driver` 和类型专属载荷的共享信封称为 schema v2，并保留 v1 只读恢复逻辑。

APM 仍处于开发阶段，尚不需要对已经生成的 journal 提供向下兼容。继续保留旧结构会增加解码分支、测试矩阵和长期解释成本，并使正式格式从 v2 起步。项目决定在发布前重置格式编号，只保留一种正式 journal 格式。

## 决策

当前共享信封重新编号为唯一正式的 schema v1。项目删除旧平铺 Skill journal 的类型、解码、归一化、fixture 和兼容性文档。所有新建 Skill/MCP journal 都写入：

```json
{
  "schema_version": 1,
  "transaction_id": "...",
  "driver": "filesystem | agent-cli",
  "operation": "install | uninstall",
  "phase": "...",
  "agent": "...",
  "power": {},
  "filesystem": {}
}
```

上例是 filesystem 信封；`driver=agent-cli` 时只包含 `agent_cli`，不包含 `filesystem`。两种载荷仍然互斥；结构、事务阶段、恢复方向和唯一活动 journal 约束不变。未来只有在正式结构发生不兼容变化时才引入 schema v2。

该决策将由 ADR-011 记录，并局部取代 ADR-010 中“旧 schema v1 作为 legacy filesystem journal 继续可读、新事务写 schema v2”的兼容条款。ADR-010 的类型专属 driver、共享信封和单 journal 决策继续有效。

## 严格读取与错误处理

`decodeJournal` 直接通过严格 JSON 解码器读取当前 `Journal`，不再先宽松探测版本，也不再尝试旧结构归一化。读取必须满足：

- JSON 语法正确且只有一个顶层值；
- 不包含未知字段；
- `schema_version` 精确等于 `1`；
- `driver` 只能是 `filesystem` 或 `agent-cli`；
- 必须且只能包含与 driver 匹配的一份载荷；
- operation、phase、Agent、Power、路径和 MCP 定义满足现有语义校验。

任一条件不满足均返回包装 `ErrInvalidJournal` 的错误。旧平铺 journal、缺失版本、版本 `0/2/3`、未知 driver、双载荷、错载荷、非法 phase、未知字段和尾随 JSON 都属于格式错误。

非法 journal 必须原样保留：`Inspect` 或 `Recover` 不自动删除、不重写、不猜测恢复方向。后续需要恢复的命令继续 fail closed，直到开发者人工处理该开发环境中的无效文件。

## 代码范围

- 删除 `internal/transaction/journal_legacy.go`；
- 将所有新 journal 的 `SchemaVersion` 从 `2` 改为 `1`；
- 简化 `decodeJournal`，只严格解码当前 `Journal`；
- 保留现有 `validateJournal` 及 filesystem/agent-cli 专属验证，并把唯一合法版本改为 `1`；
- 删除旧平铺 fixture/helper；
- 将 `journal_v2_test.go` 改名或替换为只描述正式 schema 的测试文件。

不修改 MCP/Skill lifecycle、Adapter CLI 映射、Cloud、manifest、事务阶段或恢复算法。

## 测试设计

单元测试必须证明：

- 当前 filesystem 和 agent-cli 信封以 schema v1 成功解码；
- 所有新建 Skill/MCP install/uninstall journal 都写 schema v1；
- 缺失版本和版本 `0/2/3` 返回 `ErrInvalidJournal`；
- 旧平铺结构即使字段完整也返回 `ErrInvalidJournal`；
- 未知字段、未知或缺失 driver、缺失载荷、双载荷、错载荷、非法 phase 和尾随 JSON 返回 `ErrInvalidJournal`；
- 非法 journal 经 `Inspect/Recover` 后仍保留原始文件；
- filesystem 与 agent-cli 的既有恢复用例继续通过，证明编号重置没有改变恢复业务语义。

最终验证执行：

```bash
test -z "$(gofmt -l cmd internal)"
go test -count=1 ./...
go test -race -count=1 ./internal/transaction ./internal/lifecycle
go vet ./...
go build -o /dev/null ./cmd/apm
git diff --check
```

测试继续使用临时目录和 fake Agent CLI，不调用或修改真实 Agent 环境。

## 文档与交付

- 新增 ADR-011，记录开发期 schema reset；
- 更新 ADR-010 的局部 supersession 说明；
- 更新 Feature 013 主 spec、实施计划、验收审计、C4、项目概览和 BUILD；
- 更新 Feature 007 的当前状态说明，但保留其原始历史范围；
- 已完成任务的旧 Changelog 条目保持历史原文，本次行为变更新增一条独立记录，说明原 v2 在发布前被重置为正式 v1；
- 完成后所有当前态文档只能描述一个受支持的 journal schema。

## 非目标

- 不迁移或自动删除开发机上残留的旧 journal；
- 不兼容任何旧平铺格式；
- 不改变唯一活动 journal、driver、payload、phase 或恢复方向；
- 不改变 Plugin 支持范围；
- 不增加真实 Agent CLI 验收。

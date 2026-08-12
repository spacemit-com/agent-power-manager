# Lifecycle 代码结构整理规格

## 目标

重整 `internal/lifecycle` 中 Search、List、Install、Uninstall 四个命令的生产代码与测试文件，使文件名能够直接表达所属命令和职责，使公开入口、主流程、单 Agent 操作、Power 解析及结果整理更容易定位。

本 feature 是纯结构重构。它保持 Feature 008–011 已确定并实现的四套命令专属 lifecycle，不合并 Service、不建立跨命令 runner，也不改变 CLI、组合根、依赖接口或任何可观察行为。

## 范围与非目标

### 包含

- 统一 `internal/lifecycle` 生产文件的命令前缀与职责边界；
- 把含义宽泛的 `service.go`、`preflight.go` 改为 Search 专属布局；
- 把 Search/List 的纯结果计算文件统一命名为 `*_result.go`；
- 保持 Install/Uninstall 的主流程、单 Agent 操作和解析三类文件；
- 将 package 注释、公共依赖接口和内部 typed-nil 检查放到稳定位置；
- 删除无调用者且恒定返回 `nil` 的 `batch.go/orderAgents`；
- 按生产职责拆分四个大型测试文件，同时保持测试语义和 fixture 行为；
- 在四个 lifecycle 主流程补充简要中文注释，解释重要业务边界和非显然约束。

### 不包含

- 修改 `internal/cli`、`cmd/apm` 或其他 package 的文件布局；
- 修改 `NewSearch`、`NewList`、`NewInstall`、`NewUninstall` 的签名或返回类型；
- 修改四个 CLI lifecycle 接口、结果类型、factory 或默认依赖接线；
- 合并四个 Service、引入应用容器、公共 runner、公共批处理器或公共持锁模板；
- 改变依赖集合、调用顺序、锁区间、恢复规则、日志、错误、结果或排序；
- 借重构修复、简化或重新解释既有业务规则；
- 更新 ADR、C4、BUILD 或项目概览；本 feature 不产生新的架构决策或行为说明。

## 结构原则

### 命令边界优先

所有命令专属实现文件使用 `search`、`list`、`install` 或 `uninstall` 命令名及其前缀。读者从文件名即可判断代码属于哪条命令，不再使用无法表达所属命令的 `service.go` 或 `preflight.go`。

四个 Service 继续各自拥有独立构造函数和 CLI 端口。相似的恢复、Release、Agent 选择或批处理代码不因为本次整理而跨命令抽象；这些代码承载的锁、错误和结果语义不同，保持命令内聚比减少少量重复更重要。

### 主文件承载完整入口

每个 `<command>.go` 保存：

- Service 结构体；
- 构造函数及依赖校验；
- 对应 CLI lifecycle 接口的编译期实现断言；
- 对外公开 lifecycle 方法；
- 只服务该主流程、篇幅较短的 Agent 选择、Release 和恢复日志 helper。

不为两三个短 helper 单独创建 `*_recovery.go`、`*_lock.go` 或 `*_constructor.go`。主文件应让读者连续看到“依赖是什么、如何构造、命令按什么顺序执行”。

### 次要职责独立

- `*_agent.go` 保存一次 Agent 操作需要的具体 Config、Adapter、Cache 或 Transaction 交互；
- `*_resolve.go` 保存从可信 Cloud 或 Config 输入解析目标 Power 的纯业务规则；
- `*_result.go` 保存输入复制、过滤、展平、SemVer 选择和稳定排序；
- `list_installed.go` 独立保存 installed 分支，因为它与无锁 Cloud List 有明确不同的共享状态和锁边界；
- 不创建没有对应职责的空文件，也不为了四命令形式完全对称而制造占位文件。

## 目标生产文件

```text
internal/lifecycle/
├── doc.go
├── dependencies.go
├── search.go
├── search_result.go
├── list.go
├── list_installed.go
├── list_result.go
├── install.go
├── install_agent.go
├── install_resolve.go
├── uninstall.go
├── uninstall_agent.go
└── uninstall_resolve.go
```

### 公共文件

`doc.go` 只保存 `lifecycle` package 的规范中文注释。

`dependencies.go` 继续保存 `AdapterRegistry`、`ConfigRepository`、`TransactionManager`、`Locker`、`PowerCache`、`CloudClient` 和 `VersionSelector`。构造函数共用的私有 `isNilInterface` 也放在此文件，作为依赖有效性检查实现；不新建只有该函数的文件。

### Search

`search.go` 合并当前 `service.go` 中的 `SearchService`、`NewSearch`、接口断言，以及当前 `search.go` 和 `preflight.go` 的主流程与恢复流程。合并只改变文件归属和声明顺序；Search 仍先锁内 Recover 并同步记录恢复日志，成功 Release 后才查询 Cloud。

`search_result.go` 由 `search_filter.go` 改名而来，保存 Unicode 大小写折叠、Name 子串匹配、可选精确 Type 过滤、同 Type/Name 最大 SemVer 选择和最终稳定排序。

### List

`list.go` 保存 `ListService`、`NewList`、接口断言和 Cloud/installed 分支入口。Cloud 分支继续不访问锁或其他共享状态依赖。

`list_installed.go` 保存 installed 分支的锁内 Recover、恢复日志、Registry 刷新、Agent 选择、Config 读取、部分失败收集与 Release。`agentPowerGroups` 是 installed 分支与结果转换之间的私有传递结构，保存在本文件，不提升为公共类型。

`list_result.go` 由 `list_filter.go` 改名而来，保存 Cloud/installed 结果复制、Type 过滤、展平及排序。它不读取依赖或修改输入。

### Install

`install.go` 继续保存 `InstallService`、`NewInstall`、接口断言、顶层单锁编排、Agent 选择、Release 与恢复日志。只调整声明顺序和主流程注释，不拆出短小的恢复或锁文件。

`install_agent.go` 继续保存 Cache miss 后 Cloud Get/再次 Fetch，以及单 Agent 的 Config Find、Adapter Resolve、Transaction Install 和 outcome 分类。

`install_resolve.go` 继续保存名称匹配、类别消歧和版本选择。

### Uninstall

`uninstall.go` 继续保存 `UninstallService`、`NewUninstall`、接口断言、顶层单锁编排、Agent 选择、Release 与恢复日志。只调整声明顺序和主流程注释。

`uninstall_agent.go` 继续保存单 Agent 的 Config List、Power 解析、未安装/目标缺失 Warn、Adapter Resolve、Transaction Uninstall 和 outcome 分类。

`uninstall_resolve.go` 继续保存显式 Type 查找、跨类别歧义判断及同类别清单首项选择。

## 主流程中文注释

中文注释解释业务原因和约束，不逐行翻译代码。至少覆盖以下边界：

- Search：Recover 为什么必须持锁，以及 Cloud 查询为什么必须在 Release 成功后执行；
- List：Cloud 分支为什么无锁，installed 分支的恢复、快照和所有 Config 读取为什么属于同一锁区间，以及为何 Release 成功后才生成可渲染结果；
- Install：恢复、Agent/Power 解析、Cache/Transaction 和串行批处理为什么处于同一锁区间，首错后为何补齐 unexecuted，以及为何 Release 失败抑制结果；
- Uninstall：恢复、本地逐 Agent 解析和 Transaction 为什么处于同一锁区间，事实 Warn 为什么实时记录，首错后为何停止，以及为何 Release 成功后才返回汇总。

已有准确的接口和方法中文注释保留。新增注释应简短、规范，并紧邻其约束的代码段。

## 行为保持契约

本次重构必须逐项保持：

1. 四个 Service、构造函数、CLI lifecycle 接口和依赖集合；
2. nil 与 typed-nil 依赖拒绝顺序及精确错误文本；
3. `Acquire`、`Recover`、Registry、Config、Cloud、Cache、Adapter、Transaction 和 `Release` 的调用条件、次数与顺序；
4. Search、Cloud List、installed List、Install、Uninstall 各自既有锁区间；
5. `slog.Debug`/`slog.Warn` 的消息、级别、字段、字段值和相对于 Release/后续业务的实时位置；
6. 错误包装文本、`errors.Join` 组成、`errors.Is/As` 可见原因及 batch/partial marker；
7. fatal error、业务失败和 Release 失败时结构化结果是否可返回；
8. Agent 顺序、fail-fast、后续 unexecuted、部分读取继续及 Power 指针复制；
9. Search/List 的输入不变、SemVer 选择、过滤、稳定排序和 non-nil 空 slice；
10. 所有 CLI 可见输出、usage 和退出状态。

`batch.go/orderAgents` 不属于可观察契约：该私有函数没有调用者，且实现恒定返回 `nil`。删除它不得伴随任何调用点替换或新批处理抽象。

## 测试文件布局

目标测试布局为：

```text
internal/lifecycle/
├── search_test.go
├── search_result_test.go
├── list_test.go
├── list_installed_test.go
├── list_result_test.go
├── install_test.go
├── install_agent_test.go
├── install_resolve_test.go
├── uninstall_test.go
├── uninstall_agent_test.go
└── uninstall_resolve_test.go
```

职责如下：

- `search_test.go`：构造、恢复、锁顺序、Cloud 调用和错误边界；
- `search_result_test.go`：匹配、版本选择、排序、空结果和输入不变；
- `list_test.go`：构造和无锁 Cloud 分支；
- `list_installed_test.go`：installed 分支恢复、锁、清单读取、部分失败和日志；
- `list_result_test.go`：Cloud/installed 结果转换、过滤、排序、空结果和输入不变；
- `install_test.go`：构造、顶层编排、Agent 集合、恢复、Release、batch/fatal 错误边界；
- `install_agent_test.go`：Cache、首次安装、幂等、升级/降级及单 Agent 失败；
- `install_resolve_test.go`：Power/Type/Version 解析；
- `uninstall_test.go`：构造、顶层编排、Agent 集合、恢复、Release 和 fail-fast；
- `uninstall_agent_test.go`：清单、未安装、目标缺失、Adapter/Transaction、outcome 和实时事实 Warn；
- `uninstall_resolve_test.go`：本地 Power 解析和输入不变。

测试 fixture 与断言 helper 保存在对应命令的主测试文件，供同 package 的命令测试复用。不创建跨命令 fixture 框架或只有少量 helper 的 `*_test_support.go`。拆分应保留原测试名称、断言、输入和事件顺序；允许补充防止结构整理破坏既有行为的回归断言，但不允许调整产品语义来迁就测试布局。

## 文档与决策边界

Feature 008–011 和 ADR-002/003/004/007/008/009 继续定义当前业务与架构边界。本 feature 只改变同一个 Go package 内声明所在文件，不改变组件关系、公开接口或重大决策，因此不创建或修订 ADR，也不更新 C4、BUILD 或项目概览。

根据项目 Changelog 规则，只有通过源码、构建/运行配置或脚本实质改变项目行为的任务才新增记录。本 feature 不改变行为，因此不新增 `docs/Changelog.md` 条目。

## 验收条件

1. 生产文件与测试文件符合本 spec 的目标布局，不保留 `service.go`、`preflight.go`、`search_filter.go`、`list_filter.go` 或 `batch.go`。
2. 四个 lifecycle 仍由独立 Service、构造函数和 CLI 接口实现，不存在跨命令 runner 或共享流程模板。
3. 四个主流程具有简要中文业务边界注释，且注释与现有锁、恢复、日志和结果语义一致。
4. 所有既有测试保持通过；拆分后的测试职责与目标布局一致。
5. 定向 lifecycle 测试、全量测试、race、vet、CLI build、gofmt 和 `git diff --check` 全部成功。
6. 静态检查确认公开构造函数签名、CLI lifecycle 实现断言、日志消息和关键错误文本没有改变。
7. `internal/cli`、`cmd/apm`、ADR、C4、BUILD、项目概览及 `docs/Changelog.md` 没有因本次结构整理发生修改。

## 风险与控制

- 移动同 package 声明本身不改变 Go 语义，但大文件拆分可能遗漏 build 所需的 import、fixture 或 helper；通过小步移动、gofmt 和每步定向测试控制。
- 测试重新分组可能误删看似重复但实际保护不同边界的用例；实现时保留所有原测试函数名，并对移动前后测试清单做静态比对。
- 新增注释可能错误描述流程；注释必须以既有测试和 Feature 008–011 当前规格为依据，不用注释引入新行为承诺。
- 文件数量过多会降低导航效率；本设计明确把短小恢复、Release 和构造 helper 留在主文件，仅对独立业务职责拆文件。

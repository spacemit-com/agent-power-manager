# Changelog

## 2026-08-11

分类：新增功能

用户需求：

实现并交付 `search <keyword>` 的完整 lifecycle：在查询 Cloud 前完成上次 transaction 恢复，按名称查询并渲染每种 Power 的最新版本；同时同步 ADR、架构图、概览和构建文档，并保持尚未实现的命令边界清晰。

相关提交：

- `86a4eb5 docs(search): define lifecycle design`
- `1520d16 docs(search): add implementation plan`
- `53d1157 test(cloud): align v-prefixed fixture`
- `1e7101a feat(search): select latest matching powers`
- `0651245 feat(search): recover before cloud query`
- `e20277c docs: record search lifecycle recovery`
- `86deaf9 fix(search): assert recovery warn before unlock`
- `8117eac feat(search): render lifecycle results`
- `afe0f7a docs(search): revise lifecycle lock boundary`
- `1a440e2 docs(search): clarify recovery concurrency scope`
- `fcf11d4 test(search): strengthen lifecycle regressions`

解决方案或完成结果：

`search` 在全局锁内执行 `Recover`；恢复成功会实时输出 Warn，且该 Warn 发生在 Release 之前。恢复完成后先释放锁，再调用 Cloud，随后才执行过滤、最新 SemVer 选择、稳定排序和精确 `<type>/<name>@<version>` 渲染。恢复错误仍会尝试 Release，并仅由 CLI 外层记录一次。查询只对 `Name` 进行 Unicode 大小写无关匹配；提供 `--type` 时只与 `PowerRef.Type` 精确相等的引用匹配。同类型同名的多个版本选择最大 SemVer；最终结果按原始 `Type`、再按原始 `Name` 的稳定字典序升序排列。lifecycle 返回 `SearchResult` 而不接收 Renderer，CLI 负责渲染；`--agent` 未接入 search，仍作为未知 flag 拒绝。ADR-002、修订后的 ADR-003、ADR-009、C4/流程图、项目概览和 `BUILD.md` 已对齐。`list`、`install`、`uninstall` 的 lifecycle 接口和命令 wiring 仍未连接。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、两次 `git diff --check`、`go mod tidy`、`go test -count=1 ./internal/cloud ./internal/lifecycle ./internal/cli ./cmd/apm`、`go test -count=1 ./...`、`go vet ./...` 与 `go build -o /dev/null ./cmd/apm` 均以退出码 0 完成，且 tidy 未产生模块文件变更。构建后的隔离验收中，`search summary` 的 stdout 精确为 `skill/foo-before-summary@v0.0.1` 且 stderr 为空；`search summary --agent=all` 非零退出，stdout 为空，stderr 恰有一条 `level=ERROR`、包含 `unknown flag: --agent` 和 `Usage:`。生产代码直接打印/独立 logger 扫描及 lifecycle 的 `slog.Error` 扫描均无匹配。

## 2026-08-10

分类：新增功能

用户需求：

实现集中式 lifecycle transaction 接口，使 APM 任意后续运行都能恢复上次失败的单 Agent 安装或卸载，而无需再次执行相同目标；同时明确 transaction、lifecycle 与 Adapter 的交互时序和实现边界，并为重要接口添加简要中文用途注释。

相关提交：

- `73487d5 docs(transaction): define centralized recovery design`
- `89464d8 docs(transaction): add implementation plan`
- `d8692a4 refactor(adapter): report publication outcome`
- `764c81a refactor(filesystem): share physical path safety`
- `aa760ec feat(transaction): define recovery contracts`
- `64da617 feat(transaction): persist atomic journal`
- `f17ed8d feat(transaction): guard transaction artifacts`
- `5583593 feat(transaction): implement install transaction`
- `0b141b1 feat(transaction): implement uninstall transaction`
- `0c1c8a0 feat(transaction): recover centralized transactions`
- `1fa2630 fix(transaction): reject short journal writes`

解决方案或完成结果：

新增唯一活动的集中 journal、严格 schema 校验和原子持久化，并在目标同文件系统中管理随机事务 ID 隔离的 backup/quarantine。Adapter 以 `InstallResult.Published` 明确发布点；transaction 在目标变更前记录意图、在提交点后幂等写入清单，并处理首次安装、升级、目标漂移、卸载 quarantine、提交前回滚及提交后续提交。`NewManager` 可在新进程中仅依赖集中 journal 和清单 Repository，按记录的精确路径恢复任意上次事务；损坏 journal、矛盾路径、符号链接事务目录和物理 staging/target 重叠均 fail closed 并保留证据。lifecycle 仅新增 `TransactionManager` 高层依赖边界，本 feature 不接入 lifecycle 或 CLI 命令编排。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`git diff --check`、`go vet ./...`、`go build -o /dev/null ./cmd/apm`、无缓存聚焦测试 `go test -count=1 ./internal/adapter ./internal/utils/filesystem ./internal/paths ./internal/transaction ./internal/lifecycle` 和全量测试 `go test -count=1 ./...` 均以退出码 0 完成。恢复测试覆盖全新 Manager、安装与卸载方向表、损坏或矛盾状态、取消、部分动作结果、符号链接目录，以及 journal 删除失败后的重复恢复。调用方仍须持有全局 APM 锁；实际 lifecycle/CLI 恢复预检、Cloud/Cache、版本选择、多 Agent 调度、Plugin 和 MCP 不在本 feature 范围内。

## 2026-08-07

分类：新增功能

用户需求：

为 Codex、Claude Code 和 Hermes Agent 实现全局 Skill Adapter：定义稳定的 Agent 快照和精简 Adapter 契约，解析 Agent 路径和安装检测，校验 `SKILL.md` frontmatter，并安全地从公共 staging 复制、原子发布与清理 Skill。

相关提交：

- `543ad83 docs: design agent skill adapters`
- `328c996 docs: plan agent skill adapters`
- `8f3085e docs: enforce pre-rename conflict check`
- `c578390 feat(domain): define supported agent snapshots`
- `13058ae docs: record agent snapshot domain changes`
- `b8e53a9 refactor(adapter): define skill installation contract`
- `43efb07 feat(adapter): resolve agent skill targets`
- `bfa3da9 feat(adapter): validate skill frontmatter`
- `b8c0d46 feat(adapter): install skills from staging`
- `cca68f7 fix(adapter): validate staging root`
- `9b515c5 fix(adapter): preserve atomic install failures`
- `a714018 fix(adapter): add install error context`
- `de2fcb9 test(adapter): cover install error phases`
- `e9e849c feat(adapter): register built-in skill adapters`
- `d8e787e fix(adapter): reject overlapping install roots`

解决方案或完成结果：

新增 `codex`、`claude-code` 和 `hermes-agent` 三个稳定 Agent ID；Registry 只解析这三者的 Skill Adapter，并以固定顺序刷新不可变快照。三个 Agent 共享一条全局、仅复制的安装管线：先按 Agent 根目录解析目标，拒绝任意未纳管的既有目标，再校验公共 staging 中 `SKILL.md` 的首字节 YAML frontmatter（精确名称、非空字符串 `name`/`description`，允许未知字段以及 LF/CRLF），复制完整目录到同一 Skill 根内的隐藏临时目录，并以 rename 原子发布；不创建指向 staging 的 symlink。安装在注册 staging 清理和创建目标临时目录前解析 staging、Agent 根与 Skill 根的物理路径，以分隔符感知的包含检查拒绝 Skill 根等于或位于 staging 下的布局；重叠错误不会复制、发布或清理重叠树。物理根不重叠后，有效 staging 在成功或失败时都会清理，临时目录和 staging 清理错误保留路径、阶段、Agent/Power 上下文并与主错误合并。

验证结果或限制：

无缓存聚焦测试 `go test ./internal/adapter ./internal/domain ./internal/lifecycle -count=1 -v` 通过，Adapter 与 domain 测试无 SKIP；lifecycle 以 `[no test files]` 编译通过。重叠回归覆盖物理子目录、staging/Agent 根/Skill 根 symlink、递归复制输出、既有目标保留和相邻前缀非重叠安装。`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。Adapter 直接输出/非 Debug 日志扫描无匹配；正确限定在 `internal/adapter` 的旧符号扫描未发现 `InstallPath`、`domain.Power` 或 Adapter `Uninstall`，`TargetPath`、`DetectAgent` 和 `Install` 新契约均可见。APM 全局锁仅串行 APM 自身；外部进程仍可在物理根检查后改变 symlink，或在发布前第二次目标检查与 rename 之间创建目标，因而不提供无竞态的 no-replace 保证。

## 2026-08-07

分类：新增功能

用户需求：

定义 Agent Power 安装清单格式并实现 config 接口，使 lifecycle 后续只调用无状态 Repository 的 List、Find、Set、Remove。

相关提交：

- `4ca8df6 docs: design agent power config repository`
- `27d4143 docs: plan agent power config repository`
- `27d65b6 docs: keep config log test behavior-focused`
- `6b5daff feat(config): add power groups and manifest paths`
- `546d9ad feat(config): add manifest list and find`
- `46a98db docs: align config permission test with spec`
- `1845e71 feat(config): atomically set and remove powers`
- `f2e652c refactor(config): replace manifest store with repository`
- `97d80d3 docs: fix config CLI smoke command`
- `a9df144 fix(config): preserve repository error context`

解决方案或完成结果：

每个 Agent 使用固定 skills/plugins/mcps 分组 JSON；清单条目统一为版本已确定的 PowerRef。JSONRepository 在四个业务接口内部封装读取和原子读改写，不保留跨调用状态，也不获取全局锁；缺失文件、幂等 Set/Remove、重复项归并和输入错误按 Feature 005 规格处理。

验证结果或限制：

`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。无缓存聚焦测试 `go test ./internal/config ./internal/paths ./internal/domain -count=1 -v` 全部通过且无 SKIP；旧 config 符号和直接输出/独立 logger 静态扫描均无匹配，JSONRepository 仅保留 Debug 调用。CLI help 包含 search/list/install/uninstall，提供必填 `--agent all` 的普通 search 无业务输出。lifecycle/CLI 尚未串联，Repository 写入依赖调用方持有全局 APM 锁。

## 2026-08-07

分类：功能变更

用户需求：

参考 Helm，把 APM 改为预解析 verbose、安装全局 slog，并为四级日志配置独立 Handler。

相关提交：

- `6fa452d docs: design global slog logging`
- `4433d3a docs: plan global slog logging`
- `cf5d1a1 refactor(logging): add global level routing`
- `30607ae refactor(cli): install global slog before execution`
- `b615d3c docs: record global slog CLI migration`
- `5479fc0 fix(docs): defer global slog changelog entry`
- `efc073c test(logging): strengthen handler acceptance coverage`

解决方案或完成结果：

删除自定义 Logger 门面；Debug 由 verbose Handler 控制；Info 输出到 stdout，Warn/Error 输出到 stderr；错误只在 CLI 外层记录一次；Cobra help/usage 保持原生。

验证结果或限制：

直接打印及独立 logger 静态扫描均无匹配。日志和 CLI 聚焦测试通过且无跳过测试；`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。不实现自定义格式，也不支持并发使用不同配置的 CLI Entry。

## 2026-08-06

分类：新增功能

用户需求：

实现本地 Cloud/Cache 能力，并以端到端测试验证本地资源目录到版本化缓存、再到无版本 staging 的完整链路。

相关提交：

- `56d9c84 docs: specify local cloud cache behavior`
- `ebe9832 docs: clarify cache fetch cleanup order`
- `323cf4b docs: plan local cloud cache implementation`
- `8ef8092 refactor(domain): consolidate power version metadata`
- `0ec72a7 feat(paths): add cache and staging paths`
- `bdb37ee feat(utils): add recursive directory copy`
- `cda38e9 fix(utils): populate read-only directory copies`
- `6a035be feat(cloud): add local resource client`
- `6214b92 fix(cloud): preserve local client contract`
- `2e24422 feat(cache): add versioned filesystem cache`
- `6a9ebc4 test: cover local cloud cache flow`
- `c07d8ac test: assert versionless local staging`
- `d257a12 fix(cloud): validate skill marker entries`

解决方案或完成结果：

提供本地 Skill 资源目录，按版本原子写入缓存，并将命中的版本复制为无版本 staging；空版本会返回防御性错误。`SKILL.md` marker 按目录项本身检查且仅接受普通文件，List 会跳过 symlink marker，Get 会将其映射为 `ErrNotFound`，marker 的非 `ENOENT` 文件系统错误则保留路径和底层错误。功能边界仅覆盖本地 Cloud/Cache，不包含 CLI、lifecycle、Adapter 或 HTTP。

验证结果或限制：

`go test ./internal/cloud -run TestLocalCloudAndCacheFetchGetFetch -count=1` 通过，验证 `Fetch → ErrMiss → Get → Fetch`、缓存与 staging 文件以及 HOME 未被写入。Cloud 回归测试覆盖 symlink marker 和非 `ENOENT` marker 错误；Cache 回归测试覆盖空版本保留 staging、miss/非 Skill 清理 staging 与临时 staging，以及缓存源非 `ENOENT` 错误保留上下文且不映射为 `ErrMiss`。`test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均通过。

## 2026-08-06

分类：缺陷修复

用户需求：

修复缓存目录环境变量的覆盖顺序，使其符合 `XDG_CACHE_HOME > APM_HOME > 系统默认`。

相关提交：

- `2f46cba fix(paths): honor XDG cache precedence`
- `7cdf98f test(paths): use system cache directory expectation`

解决方案或完成结果：

`CachePath()` 现在优先使用非空 `$XDG_CACHE_HOME/apm`，其次使用非空 `$APM_HOME/.cache/apm`，两者均为空或未设置时使用系统默认缓存目录下的 `apm`，并取代上一条记录中“`APM_HOME` 覆盖 XDG 缓存”的旧语义。`APM_HOME` 对状态、Agent、Power 和默认 lock 等 HOME 派生路径的作用保持不变。ADR-005、Project Overview 和 feature 文档已同步新的优先级。

验证结果或限制：

- 单元测试分别覆盖 XDG 覆盖 APM、XDG 为空时回退 APM，以及两者均为空时使用系统默认缓存目录。
- `test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成。

## 2026-08-06

分类：新增功能

用户需求：

增加仅供测试使用的 `APM_HOME`，使 APM 可在临时目录模拟 Agent 和 Power 状态而不影响用户目录。

相关提交：

- `3320f3a docs: design APM_HOME test isolation`
- `37a7181 docs(feature): plan APM_HOME test isolation`
- `b46ec74 docs(plan): preserve XDG cache fallback`
- `fa73e56 feat(paths): isolate test filesystem with APM_HOME`
- `67e9f3d docs: record APM home path isolation`
- `5e4fdfd fix: defer APM home changelog entry`

解决方案或完成结果：

当 `APM_HOME` 为非空值时，APM 的有效 HOME 为该目录；状态目录为 `$APM_HOME/.apm`，缓存目录为 `$APM_HOME/.cache/apm`，默认全局锁也随之隔离在 `$APM_HOME/.apm/apm.lock`。未设置或设置为空时，HOME 仍使用系统默认解析，缓存仍遵循 XDG 缓存目录覆盖。

验证结果或限制：

- `rg -n 'os\.(UserHomeDir|UserCacheDir|Getenv|LookupEnv)\(' internal --glob '*.go'` 确认与该功能相关的生产 HOME、缓存和环境访问集中于 `internal/paths/paths.go`；测试辅助环境访问可见于 `paths_test.go`。
- `test -z "$(gofmt -l cmd internal)"`、`go test -count=1 ./...`、`go vet ./...`、`go build -o /dev/null ./cmd/apm` 和 `git diff --check` 均以退出码 0 完成；测试包全部报告 `ok`，无测试包报告 `[no test files]`。
- 当前仅集成路径和既有全局锁；Agent 检测、安装/卸载、配置、缓存写入及事务恢复仍由各自功能负责。

## 2026-08-05

分类：新增功能

用户需求：

建立 ADR-001 整体框架，实现只解析命令的 CLI、logging 和全局 lock

相关提交：

- `baea8bc docs(feature): define CLI foundation scope`
- `97a738b chore: initialize APM Go module`
- `c5aeac3 docs: record module initialization`
- `64facaf docs: defer changelog entry`
- `c563618 feat: add unified logging`
- `bbe794c feat: add parse-only CLI`
- `cb4b008 fix: route CLI help and errors correctly`
- `6d4902f feat: add global APM file lock`
- `7eaa75c chore: scaffold lifecycle transactions`
- `ec3ec26 chore: scaffold APM components`
- `609e72b fix: reset CLI verbose logging state`

解决方案或完成结果：

基于 ADR-001 建立 Go 模块和领域、适配器、缓存、云端、配置、生命周期、路径、事务、版本等基础包；实现只解析及校验参数的 Cobra CLI、按 verbose 分流的统一结构化日志，以及进程间全局文件锁。CLI 提供 install、uninstall、search、list 四个业务命令，为后续生命周期实现保留稳定边界。

验证结果或限制：

- `test -z "$(gofmt -l cmd internal)"`、`go test ./...`、`go vet ./...`、`go build -o ./apm ./cmd/apm` 均以退出码 0 完成；测试覆盖的包全部通过，其余包报告 `[no test files]`。
- `./apm --help` 展示 install、uninstall、search、list；普通 search 静默并以退出码 0 完成；verbose search 仅向 stderr 输出解析诊断并以退出码 0 完成；无效 type 仅向 stderr 输出错误和 usage，并以退出码 1 结束。
- Adapter 目录没有子目录，源码扫描未发现 TODO、TBD 或 panic；cli、logging、lock、domain 四个包均显示中文 GoDoc；`git diff --check` 以退出码 0 完成。
- 当前阶段只完成命令解析、校验和基础组件脚手架，尚未执行 install、uninstall、search、list 的实际业务操作。

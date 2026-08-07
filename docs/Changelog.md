# Changelog

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

# Changelog

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

# ADR-030：联网构建 Debian CLI 包并保留用户数据

- Status：Accepted
- Date：2026-09-09
- Decision Makers：APM maintainers

## Context

项目需要在 Bianbu riscv64 开发板上从源码构建、安装和运行 APM。现有 Debian
目录为生成的模板；APM 已将状态、缓存及 Agent 资源保存在用户目录中。
用户明确允许构建时联网下载依赖，并要求卸载保留用户数据；已确认 remove 和
purge 均保留数据的设计。

## Decision

- 使用 debhelper compat 13 和 Go modules 联网构建，依赖版本及校验依据为
  `go.mod/go.sum`，默认运行 Go 测试，失败时停止打包。
- 保留 `agent-power-manager` 包名、native source format 与 Bianbu suite，
  将 CLI 安装到 `/usr/bin/apm`，随包文档安装到标准包文档目录。
- CLI 由普通用户按需运行，不安装后台服务，不在包安装阶段初始化用户状态。
- 包只管理程序和随包文档。remove、purge 和重装均不删除或重置用户状态、缓存、
  Agent 资源及配置，不调用应用层 `apm uninstall`。
- 板端以隔离数据验证安装、remove、purge、重装，最终保持包已安装可运行。

完整范围与验收见 [Feature 030](../features/030-debian-packaging/spec.md)。

## Consequences

开发板可复用现有 Go 工具链和标准 Debian 打包命令。运行时无需 Go；保留原有用户
路径语义，无需数据迁移。卸载后数据继续占用空间，由用户独立管理。

构建依赖网络或已准备的 module 缓存，不能直接承诺离线构建；后续若接入禁止联网的
构建环境，需要另行确定依赖供应方式。数据保留不等于任意版本间的 schema 兼容。

## Alternatives Considered

- 提交 vendor：可以降低打包时对网络的依赖，但增加仓库体积和依赖同步工作；
  当前允许联网，暂不采用。
- 手工组装二进制包：可快速产包，但需自行维护更多打包约定；选择现有 debhelper 流程。
- purge 时清理用户目录：违反用户数据保留要求，且包管理不应接管多个用户的应用数据。

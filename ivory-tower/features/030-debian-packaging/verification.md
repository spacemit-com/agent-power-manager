# Feature 030 板端验证记录

## 环境

- 日期：2026-09-09
- 开发板：`bianbu@10.0.91.51`
- 系统：Bianbu 4.0.6（riscv64）
- Go：`go version go1.26.0 linux/riscv64`
- debhelper：`13.24.2ubuntu1-bb1`
- 源码目录：`/home/bianbu/Workspace/deb/apm/agent-power-manager`
- 软件包：`/home/bianbu/Workspace/deb/apm/agent-power-manager_0.0.1_riscv64.deb`
- 软件包 SHA-256：`af17a1f623db66ce1270c8e6e5aa6d13982685a6c8ba7a9cf46895486eb89a7e`

## 构建

在板端执行 `dpkg-buildpackage -us -uc -b`。该命令完成 `go mod download`、
`go mod verify`、`go test ./...`、riscv64 编译和 debhelper 二进制打包，退出码为 0。
构建前后 `go.mod` 与 `go.sum` 的 SHA-256 一致。

Go 1.26 输出压缩 DWARF；板端 `dwz` 对 `.debug_abbrev` 报
`Found compressed .debug_abbrev section` 并退出 1。`debian/rules` 因此只跳过
`dh_dwz`，其余 debhelper 阶段仍执行。该原因由 `readelf -SW` 的压缩段标志与直接
执行 `/usr/bin/dwz` 复现确认。

最终控制字段为：

```text
Package: agent-power-manager
Version: 0.0.1
Architecture: riscv64
Depends: ca-certificates
```

包内容仅为 `/usr/bin/apm` 和 `/usr/share/doc/agent-power-manager/` 下的
`README.Debian`、`changelog.gz`、`copyright`。控制归档仅含 `control` 和
`md5sums`，没有安装、删除或 purge maintainer script。

使用 `dpkg-source -b .` 检查 native 源码归档；向未清理的 `debian/.build` 写入
marker 后生成归档，归档成员中不存在该目录。随后 `debian/rules clean` 删除专用
构建目录。`tar-ignore = "debian/.build"` 负责归档排除，clean 负责构建结束后的清理。

## 运行与数据保留

为不触及真实用户数据，在 `/home/bianbu/apm-deb-system-verify.ulPNeL` 创建隔离
状态、缓存、Codex、Claude、Hermes 与 Bibit 根目录，并记录内容 SHA-256、类型、
权限、所有者和符号链接目标。系统 dpkg 过程验证：

1. 通过 `apt-get install` 安装包后存在可执行的 `/usr/bin/apm`。
2. 使用六个隔离环境变量运行 `--help`、`list --installed --json` 与 MCP
   `validate --json`；不访问真实 Agent CLI。
3. `remove` 后程序已移除，状态、缓存、Codex、Claude、Hermes 与 Bibit 目录的
   内容 SHA-256、类型、权限、所有者及符号链接目标均与基线一致。
4. 重装后 `purge` 仍保持同一快照，最后重装并由 dpkg 状态确认
   `install ok installed`。

最终系统状态为 `agent-power-manager 0.0.1` 已安装，`/usr/bin/apm --help` 退出 0。
隔离验证目录保留在开发板上作为证据；未修改真实用户状态或 Agent 资源。

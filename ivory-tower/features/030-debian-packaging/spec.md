# Feature 030：Debian 打包与用户数据保留

- Status：Approved
- Date：2026-09-09
- Related ADRs：[ADR-030](../../adr/ADR-030-package-cli-and-preserve-user-data.md)

## 1. 目标与范围

完善现有 `debian/`，使项目能够在 `bianbu@10.0.91.51` 的
`/home/bianbu/Workspace/deb/apm/agent-power-manager` 中原生构建 Debian 包，
安装后由普通用户直接运行 `apm`。普通卸载、purge 和重装均保留用户数据。

范围包括打包元数据、Go 构建与测试接入、安装清单、包使用说明和开发板验收。
不增加后台服务，不改动 CLI 的数据路径、Cloud provider 默认值或 Power 生命周期。
不发布软件仓库，不推送远端 Git，不打包 Agent 程序和开发工具链。

## 2. 已检查的环境

- 源码入口为 `cmd/apm`，`go.mod` 要求 Go 1.26.0。
- 现有 `debian/` 是 debmake 模板，尚未实现有效的构建和安装流程。
- 当前包名为 `agent-power-manager`，版本为 `0.0.1`，source format 为
  `3.0 (native)`；Bianbu suite 为 `resolute-porting`。
- 开发板为 Bianbu 4.0.6、riscv64，已有 `/usr/bin/go`（Go 1.26.0）和
  debhelper 13.24.2ubuntu1-bb1。
- 检查时板上目标源码目录不存在，实施时创建并同步任务所需源码。
- 本地未跟踪的 `APC-API.md` 属于既有用户文件，不纳入本任务提交或构建同步。

## 3. 构建设计

使用 debhelper compat 13，在 `debian/rules` 中显式接入 Go modules 下载、测试、
编译和安装。保留 native source format、现有包名和 Bianbu suite，更新本次打包说明。
二进制包声明 `Architecture: any`，本次验收目标为原生 riscv64。

在源码根目录执行：

```bash
dpkg-buildpackage -us -uc -b
```

构建依赖声明 debhelper 和满足 `go.mod` 的 Go 工具链。允许联网下载 modules，
以 `go.mod` 和 `go.sum` 为依赖版本及完整性依据；构建不得隐式更新这两个文件。
允许调用者通过 `GOPROXY` 等标准 Go 环境变量选择网络来源，不关闭校验和验证。
使用本机工具链，版本不足时明确失败，不隐式下载另一个 Go 工具链。

构建工作目录、Go 编译缓存和 module 缓存使用专用构建目录，clean 仅删除该目录及
debhelper 管理的产物，不清理用户的全局 Go 缓存。源码包不携带这些下载或编译产物。

默认执行 `go test ./...`，遵循 `DEB_BUILD_OPTIONS=nocheck` 的显式跳过约定；
最终验收不跳过测试。编译目标为 `./cmd/apm`，不引入 CGO 依赖。
依赖下载、校验、测试或编译失败均使打包失败，不能使用旧二进制继续产包。
联网构建是本次板端工作流的明确选择，不承诺离线构建环境可直接使用。

## 4. 安装和运行

| 内容 | 安装位置或处理方式 |
| --- | --- |
| CLI 二进制 | `/usr/bin/apm` |
| 包说明、版权信息、changelog | `/usr/share/doc/agent-power-manager/` |
| TLS 根证书 | 声明 `ca-certificates` 运行依赖，供默认 HTTPS Cloud 使用 |
| Go 工具链、源码、测试 fixture | 不安装到运行包 |
| 用户配置、缓存、Agent 资源 | 由现有 CLI 在用户运行时管理 |

不创建系统用户、不设置 systemd 服务，也不在安装脚本中启动 CLI、访问 Cloud 或
写入用户目录。用户正常运行 `apm`，无需 `sudo`；提权仅用于系统包管理。
默认 Remote provider 保持现有行为，Local provider 仍可由用户显式指定外部 fixture。

清理无效的 debmake 模板项并完善包描述、安装清单和使用说明，保留有效的 Bianbu CI
配置。版权说明只记录可核实事实：当前模板含 UNKNOWN，仓库未发现项目许可证文件，
不能自行宣称 MIT、Apache 等授权。依赖许可信息按实际模块核实并随包说明。

## 5. 卸载与数据保留

系统包只拥有程序和随包文档，不将用户数据列入包文件清单，不添加清理用户数据的
maintainer script，也不调用 `apm uninstall`。

`apt remove`、`apt purge` 和对应 dpkg 操作均保留以下内容：

- `~/.apm` 下的 catalog、manifest、事务日志、staging 和其他已有状态。
- `$XDG_CACHE_HOME/apm` 或默认 `~/.cache/apm`，包括已发布且可能被资源引用的制品。
- 各 Agent 根目录中的资源、配置和 APM 管理的链接。
- 通过现有环境变量重定向的用户数据路径。

卸载后暂时不能执行 `apm`；重装本包后继续使用原有状态，安装过程不重置、不迁移、
不自动修复用户数据。这一约定不承诺不同应用版本之间未经验证的 schema 兼容性。

## 6. 开发板验证与错误处理

实施前重新检查目标目录、板上 Git 状态、已安装同名包以及 `/usr/bin/apm` 的归属；
同步不得覆盖不属于本任务的改动。记录实际源码提交、工具链、构建命令和包文件名。

验收包含：

1. 在指定目录完成默认测试与 `dpkg-buildpackage -us -uc -b`，得到 riscv64 包。
2. 检查包元数据、依赖和文件清单，确认无用户数据、源码、缓存或 fixture 混入。
3. 安装生成的包，从源码目录之外以普通用户运行 `/usr/bin/apm --help`，并在隔离
   数据目录中执行 installed list 与离线 validate，确认安装后的程序独立可用。
4. 使用隔离的 `APM_HOME`、`XDG_CACHE_HOME` 及 Agent 根目录，准备代表性的状态、
   缓存、资源、配置和链接，记录内容校验和、权限及链接目标。
5. 执行 remove，核对程序移除且上述数据保持；重装后执行 purge，再次核对数据。
6. 最后重装本包，确认 CLI 可运行且保留的数据仍可读取。最终保持已安装状态。

测试不得调用真实 Agent CLI 或修改用户实际资源；需要 Agent CLI 时使用隔离 fake-bin。
网络 Cloud 查询可作为补充检查，服务端不可达须单独记录，不替代本地运行验收。
系统包安装或卸载失败时报告实际错误和板上状态，不将部分验收描述为成功。

## 7. 文档与完成条件

设计与后续实施计划分别保存在本 feature 的 `spec.md`、`plan.md`，打包和保留策略
引用 ADR-030。实施时更新 `BUILD.md`、包内 `README.Debian`，保存板端验证记录。
所有构建、安装、卸载保留和最终重装检查通过后，按项目模板增加一条
`docs/Changelog.md`，列出本请求的相关提交。本轮纯设计文档不增加行为变更记录。

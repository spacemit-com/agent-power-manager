# Feature 030 Debian Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Subagent execution is an alternative only if the user selects it.

**Goal:** 在指定 Bianbu riscv64 开发板目录完成 APM 的 Debian 构建、安装及数据保留验收，最终保持已安装可运行。

**Architecture:** 使用 debhelper 调用 Go modules 下载、测试和编译，包仅安装 CLI 和文档。应用继续管理原有用户路径，系统包卸载不触发应用层清理。以隔离数据验证 remove、purge 和重装。

**Tech Stack:** Go 1.26.0、debhelper compat 13、dpkg、Bash、SSH、Bianbu 4.0.6 riscv64。

**Spec:** [spec.md](spec.md)，决策依据 [ADR-030](../../adr/ADR-030-package-cli-and-preserve-user-data.md)。用户已于 2026-09-09 确认书面设计。

## Global Constraints

- 包名 `agent-power-manager`，source format `3.0 (native)`，suite `resolute-porting`，`Architecture: any`。
- 板端路径 `/home/bianbu/Workspace/deb/apm/agent-power-manager`，身份 `bianbu@10.0.91.51`。
- 允许联网下载 modules，不修改 `go.mod/go.sum`，不关闭 checksum 验证，不自动切换 Go 工具链。
- 默认运行 `go test ./...`；支持显式 `DEB_BUILD_OPTIONS=nocheck`，正式验收不可跳过测试。
- CLI 安装为 `/usr/bin/apm`，不安装服务、不自动初始化用户状态。
- remove 和 purge 都保留状态、缓存、Agent 资源及配置；最终重装并保持可运行。
- 不调用真实 Agent CLI；所有验收数据使用隔离目录；不覆盖远端既有源码改动。
- 不操作用户未跟踪文件 `APC-API.md`。所有本地编辑使用 apply_patch。
- 如使用远端 Python，必须先读机器规范并使用项目 `.venv313` 的 Python 3.13；本计划无需 Python。
- 仅本次行为工作全部完成后增加一条项目 Changelog，引用所有相关提交；设计提交为 `5a0fbff`。

## File Structure

- 修改 `debian/control`：构建依赖、运行依赖与包描述。
- 修改 `debian/rules`、`debian/install`、`debian/clean`：构建、安装和专用产物清理。
- 修改 `debian/changelog`、`debian/copyright`、`debian/README.Debian`、`debian/README.source`：版本记录、真实许可信息和构建/运行说明。
- 修改 `debian/tests/control`，创建可执行 `debian/tests/smoke`：安装后离线 CLI 验证。
- 删除仅含模板注释的 `debian/dirs`、`debian/links`、`debian/docs`、`debian/examples`、`debian/manpages`、`debian/upstream/metadata`；删除与当前 native Bianbu 流程不符的模板 `debian/gbp.conf`。
- 保留 `debian/source/format`、`debian/bianbu.conf`、`.bianbu-ci.yml` 及现有 `debian/salsa-ci.yml`，不扩大 CI 修改范围。
- 修改 `BUILD.md`：添加板端打包说明并引用 ADR-030。
- 创建 `ivory-tower/features/030-debian-packaging/verification.md`：实际命令、结果、产物及数据保留证据。
- 最后修改 `docs/Changelog.md`：一条已完成任务记录。

## Task 1：实现可构建的 Debian 包

**Interfaces:** 输入为 `go.mod/go.sum` 与 `./cmd/apm`；输出为原生二进制包，包含 `/usr/bin/apm`。Task 2 使用该包的安装清单和命令入口。

- [x] **Step 1：核实工作区和板端状态。**

按 using-git-worktrees 技能在执行阶段选择隔离工作区；先检查当前分支及未提交改动。只读检查板端：

```bash
ssh -o BatchMode=yes -o ConnectTimeout=8 bianbu@10.0.91.51 \
  'id; command -v go; go version; dpkg --print-architecture; dpkg-query -W agent-power-manager debhelper golang-go ca-certificates; dpkg-query -S /usr/bin/apm; ls -ld /home/bianbu/Workspace/deb/apm/agent-power-manager; sudo -n true'
```

分别解释缺包、缺目录和 sudo 结果，不把预期的查询非零视为连通性失败。若目标目录已存在，读取其 Git 状态与内容，确认同步范围。若 `/usr/bin/apm` 属于其他包或是未管理的现有文件，先保留该现场并报告冲突，不能覆盖。

- [x] **Step 2：替换构建与安装模板。**

`debian/control` 使用以下主要内容，保留已核实的 maintainer：

```debcontrol
Source: agent-power-manager
Section: utils
Priority: optional
Maintainer: bianbu <bianbu@localhost>
Build-Depends: debhelper-compat (= 13), golang-go (>= 2:1.26~), ca-certificates
Standards-Version: 4.7.3
Rules-Requires-Root: no

Package: agent-power-manager
Architecture: any
Depends: ${misc:Depends}, ${shlibs:Depends}, ca-certificates
Description: command-line manager for AI agent powers
 Manage skills, plugins, MCP definitions and agent resources for supported
 AI agents. User state and installed resources are preserved on removal.
```

`debian/rules` 基础实现如下，recipe 使用真实 tab；构建目录为精确的 `debian/.build`：

```make
#!/usr/bin/make -f
export GOTOOLCHAIN := local
export CGO_ENABLED := 0
export GOFLAGS := -mod=readonly
export GOCACHE := $(CURDIR)/debian/.build/go-cache
export GOMODCACHE := $(CURDIR)/debian/.build/go-mod

%:
	dh $@ --buildsystem=none

override_dh_auto_configure:
	mkdir -p debian/.build
	go mod download
	go mod verify

override_dh_auto_build:
	go build -trimpath -buildvcs=false -o debian/.build/apm ./cmd/apm

override_dh_auto_test:
ifeq (,$(filter nocheck,$(DEB_BUILD_OPTIONS)))
	go test ./...
endif

override_dh_auto_clean:
	if test -d debian/.build; then chmod -R u+w debian/.build; fi
	dh_clean
```

`debian/install` 写入 `debian/.build/apm usr/bin`，`debian/clean` 写入 `debian/.build/`。
clean 的 chmod 仅限已核实的专用构建目录，处理 Go modules 缓存中的只读目录。
保留 source format 并核验 native source 打包默认忽略整个 `debian/.build`；若未忽略，
增加 `debian/source/options` 的 `extend-diff-ignore = "(^|/)debian/\.build(/|$)"` 并以
源码归档内容实测，不仅依赖规则文本。

- [x] **Step 3：完善包元数据和文档。**

将当前 `0.0.1` changelog 条目补全为本次首次可用打包记录，保留 suite。
按 File Structure 删除已检查的无效模板文件。`README.Debian` 明确普通用户运行、
无服务、remove/purge 保留数据、重装不重置状态；`README.source` 记录联网构建及
`GOPROXY` 可覆盖、工具链要求和 native 格式。

版权不创造授权：读取当前仓库历史、模块缓存中的 LICENSE/COPYING，使用
`go list -m -json all` 确认模块路径和版本，在版权文件中记录项目未声明许可证这一
事实并附上实际静态链接模块的许可文本。无法核实的许可明确记录在 verification，
不能把 licensecheck 模板的 `Copyright: NONE` 当成已核实结论。

在 `BUILD.md` 添加以下命令及依赖说明，解释安装使用实际生成包路径：

```bash
sudo apt-get install debhelper golang-go ca-certificates
dpkg-buildpackage -us -uc -b
sudo apt-get install ../agent-power-manager_0.0.1_riscv64.deb
/usr/bin/apm --help
```

- [x] **Step 4：同步并验证真实构建。**

提交前检查 `git diff --check`。创建板端目标目录，仅同步本次构建所需的已跟踪源码与
明确新增的打包文件；不得使用带删除的 rsync，不携带 `.git`、本地构建缓存和
`APC-API.md`。可使用明确路径的文件列表与 `rsync --files-from`，先 dry-run 检查。

在板端目标目录执行并保存完整日志：

```bash
sha256sum go.mod go.sum
dpkg-buildpackage -us -uc -b
sha256sum go.mod go.sum
dpkg-deb -f ../agent-power-manager_0.0.1_riscv64.deb Package Version Architecture Depends
dpkg-deb -c ../agent-power-manager_0.0.1_riscv64.deb
```

预期：构建退出 0、全部 Go 测试通过、两个依赖文件校验和不变，包只有 CLI 和文档，
没有源码、fixture、Go cache 或用户路径。再运行 `dpkg-source -b .` 并检查生成 tar
成员，确认无 `debian/.build`；在这之后执行 clean，确认专用目录消失且源码未改变。
若网络失败，用标准 `GOPROXY` 选定可用来源重试并记录，不关闭 TLS 或 checksum。

- [x] **Step 5：提交构建交付。**

仅 stage File Structure 中本任务修改的 Debian 文件及 `BUILD.md`，提交
`build: package APM CLI for Bianbu with Go modules`。记录提交号和包 SHA-256。

## Task 2：验证安装与完整的数据保留闭环

**Interfaces:** 消费 Task 1 生成的包及 `/usr/bin/apm`；输出离线 smoke test 和 remove/purge/reinstall 验证记录。

- [x] **Step 1：添加可复用离线 smoke test。**

`debian/tests/control` 内容：

```debcontrol
Tests: smoke
Depends: @
Restrictions: superficial
```

创建可执行 `debian/tests/smoke`，核心内容如下：

```sh
#!/bin/sh
set -eu
apm_test_root=$(mktemp -d)
# 使用专用目录隔离全部 APM 与 Agent 数据，禁止访问真实 Agent CLI。
export APM_HOME="$apm_test_root/state-home"
export XDG_CACHE_HOME="$apm_test_root/cache"
export CODEX_HOME="$apm_test_root/codex"
export CLAUDE_CONFIG_DIR="$apm_test_root/claude"
export HERMES_HOME="$apm_test_root/hermes"
export BIANBU_AGENT_HOME="$apm_test_root/bibit"
mkdir -p "$APM_HOME" "$XDG_CACHE_HOME"
apm_source_root=$(pwd -P)
cd "$apm_test_root"
/usr/bin/apm --help
/usr/bin/apm list --installed --json
/usr/bin/apm validate \
  "$apm_source_root/internal/cloud/resources/mcps/company-tools/v0.0.1/MCP.json" \
  --type mcp --json
```

这里仅将源码 fixture 作为用户输入传入，不将其打入包；程序运行目录在源码树之外。
为临时目录补充退出时清理：通过创建时取得的绝对路径和专用前缀验证后再清理，
不得使用 HOME 或宽泛目录为删除目标。先安装 Task 1 的包并从仓库根运行
`sh debian/tests/smoke`；确认 CLI 不要求真实 Agent 安装或访问 Cloud。若已有 CLI
契约使空 installed list 非零，先检查原因并只调整 fixture，不改变应用行为。

- [x] **Step 2：创建保留验证数据与快照。**

在板端用 `mktemp -d /home/bianbu/apm-deb-verify.XXXXXX` 创建私有目录，记录其解析后的
绝对路径。沿用 smoke 的六个隔离环境变量，运行一次 installed list 创建合法初始状态。
用 apply_patch 准备测试输入再传到板端：state、cache 和四个 Agent 目录各一个
`retention-marker.txt`，内容为 `apm-deb-retention-v1`；另外复制一个有效 MCP fixture
作为配置样本，在隔离 cache 中放资源文件，在隔离 Agent 目录创建指向该文件的链接。
整个验收不执行实际 Agent 命令。

在隔离数据父目录下生成基线清单，清单输出保存在数据目录之外：

```bash
find data -type f -exec sha256sum {} + | LC_ALL=C sort
find data -printf '%P %y %m %u %g %l\n' | LC_ALL=C sort
```

baseline 包含内容、类型、权限、所有者和链接目标，不包含 atime/mtime。
快照之间不运行 APM，避免应用自身写状态干扰包卸载验证。

- [x] **Step 3：执行 remove、purge 和重装验证。**

使用已记录的精确包路径，逐条执行，任何失败均停止后续破坏性步骤并报告当前状态：

```bash
sudo -n apt-get install /home/bianbu/Workspace/deb/apm/agent-power-manager_0.0.1_riscv64.deb
sudo -n dpkg --remove agent-power-manager
test ! -e /usr/bin/apm
```

重新生成两份快照并与 baseline 使用 `diff -u` 对比，应完全一致。
再次安装同一包，不运行应用，然后：

```bash
sudo -n dpkg --purge agent-power-manager
test ! -e /usr/bin/apm
```

再次核对快照完全一致；最终重装该包，运行 smoke 和隔离环境中的 installed list。
记录 `dpkg-query -W agent-power-manager`、命令输出和包 SHA-256。
若 sudo 不可用，报告需要用户完成的确切命令和原因，不尝试获取或输出密码。
测试目录可保留为验收证据并报告路径，不清理真实用户资源。

- [x] **Step 4：纳入测试文件后重建最终包并安装。**

将 smoke 文件同步到板端，重新执行默认 `dpkg-buildpackage -us -uc -b`，安装最终包。
执行 `sh debian/tests/smoke`；记录最终产物校验和。仅因文件变更进行本次重建，
不增加与打包无关的重复测试。提交 `test: verify packaged CLI in isolated user directories`。

## Task 3：完成证据与任务记录

**Interfaces:** 消费前两项的提交号、包文件和验证日志；输出可复查的完成记录。

- [x] **Step 1：填写 verification.md。**

记录日期、开发板 OS/架构/Go/debhelper 版本、源码提交、联网配置、构建命令和退出码、
全部 Go 测试结果、包路径及 SHA-256、包文件清单、smoke 输出、remove/purge 的快照
比较结果、最终安装状态和保留的测试证据路径。任何实际限制单列，不伪造成功结果。

- [x] **Step 2：完成计划检查并提交证据。**

对照 spec §1–7 检查覆盖，勾选已完成步骤。运行 `git diff --check` 并确认文档相对链接
指向现存文件；纯文档修改不重复构建。提交 `docs: record Debian package validation`。

- [x] **Step 3：记录一次完整任务 Changelog。**

只有所有必须验收均成功时，读取 `/home/liangff/.codex/docs/Changelog.md`，在项目
`docs/Changelog.md` 顶部增加一条记录，填写用户需求、所有已有设计/计划/实现/测试/
验证提交、结果和限制。提交 `docs: record completed Debian packaging task`，该记录自身
提交号在交付消息中给出，避免自引用。若任何必须验收阻塞，不添加完成记录。

- [x] **Step 4：交付。**

提供最终包的板端绝对路径、构建和安装命令、测试结果、卸载保留范围以及 Git 提交号。
明确板上最终已安装版本，说明只清理过哪些任务产物；不推送或发布。

## Plan Self-Review

- Spec §1–3：Task 1 覆盖目标环境、Go 工具链、联网、只读依赖锁定、测试和 clean。
- Spec §4：Task 1 覆盖包布局、证书、版权与文档；Task 2 验证独立运行。
- Spec §5–6：Task 2 覆盖隔离数据及 remove/purge/reinstall，Task 1 先检查远端现场。
- Spec §7：Task 3 覆盖证据、ADR 引用和单条完成 Changelog。
- 不引入生产 Go 接口变化；所有任务复用同一包名、版本、路径和环境变量约定。

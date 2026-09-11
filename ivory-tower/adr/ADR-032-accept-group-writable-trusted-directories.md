# 接受组可写的本地受信目录，仅拒绝其他用户可写

- Status：Accepted
- Date：2026-09-11
- Decision Makers：APM maintainers
- Related ADR：[ADR-021](ADR-021-cache-versioned-resource-catalog.md)

## Context

APM 有两条本地目录链需要建立“父目录不可被其他主体写入”的不变量：

| 目录链 | 检查点 | 覆盖范围 |
| --- | --- | --- |
| `~/.cache/apm` | `internal/cloud/remote.verifyDirectoryTrust` | 内容与目录下载目标、MCP 临时父目录、catalog 下载目标 |
| `~/.apm/catalog.json` 的父目录链 | `internal/catalog.verifyRepositoryDirectoryTrust` | Catalog Repository 的读取与原子发布 |

两者都从 `/` 起逐级 `openat(..., O_NOFOLLOW)` 固定物理目录，并对每一级要求：属主为 root 或当前 euid，且 mode 不含任何 `0o022` 位（组可写或其他人可写），唯一例外是 root 拥有的 sticky 目录（如 `/tmp`）。

该规则在真实设备上直接阻断安装。实测 Bianbu 设备 `/home/bianbu/.cache` 为 `drwxrwxr-x bianbu:bianbu`（`0775`），会话 umask 为 `0002`：

```text
snapshot catalog powers: download catalog: open remote catalog destination parent: cloud catalog destination failure
verify pinned remote directory "/home/bianbu/.cache": remote directory mode 0775 permits untrusted entry mutation: cloud unsafe content
```

`~/.cache/apm`、`~/.cache/apm/.tmp` 本身由 APM 以 `0700` 创建，但 APM 从不 chmod 既存父目录，因此 `~/.cache` 的 mode 只由系统/发行版/用户脚本决定。XDG 只规定程序**创建**目录时使用 `0700`，不要求程序改写既存目录，所以“让 APM 工作”不能以要求发行版把 `~/.cache` 改成 `0700` 为前提。`~/.config`、`~/.cache` 一类的家目录子目录被发行版安装脚本或 `umask 0002` 建成 `0775` 是常见部署现实。

同时，该检查的原始意图是“阻止其他**用户**植入或替换目录项”，而组可写与其他人可写在威胁模型上并不同等：组可写只对同组成员开放，而其他人可写对任意本地账号开放。

## Decision

把 mode 判定从“拒绝任何 `0o022` 位”收窄为“**只拒绝其他人可写（`0o002`）**”：

- `owner` 必须为 root 或当前 euid（不变）。
- `perm&0o002 != 0` 的非 root-sticky 目录仍然 fail closed（`0777`、`0702` 等）。
- `perm&0o020 != 0` 的目录（`0775`、`0770`、`2775` 等）放行，并通过新的 `groupWritable` 判定结果上报；由持有路径的调用方经 `logging.WarnOnce` 输出一次 warning（`path` + `mode`），同一目录每进程只提示一次。
- root 拥有的 sticky 目录仍是唯一允许 `0o002` 的例外（继续受 `directoryTrustPolicy.allowRootOwnedSticky` 与 catalog 的“最终父目录不得为 sticky”约束）。
- 只看 mode 低 9 位；`setuid`/`setgid`/sticky 不影响判定，故 Debian 风格 `2775` 的目录按 `0775` 处理。

以下内容明确不变：属主检查、逐级 `O_NOFOLLOW` 与符号链接拒绝、fd 固定后的 fd 相对名称型操作、device/inode 身份复验、`O_EXCL`/no-replace 发布语义、APM 自身创建目录的 `0700` 与 catalog 文件的 `0600`、非 Unix 平台的 fail-closed 分支，以及 Agent 根目录（本就不做信任检查）。

不放行组可写的理由：该残留风险已在 ADR-021 的信任边界内——pathname 操作把 current EUID 视为受信主体，而两条目录链上所有后续操作都作用在已固定的 fd 上，逐级 owner 检查会拒绝被替换成其他用户所有的目录，因此组内写者无法把操作重定向到自己的目录。真正的残留能力是**改名或删除既有目录项**（例如把 `~/.cache/apm` 移走），使产物落入不可达目录或造成可用性故障，而不构成对其他用户内容的读写。

## Consequences

- 正面影响：`~/.cache`、`~/.apm`、`~/.config` 等既存目录为 `0775` 的系统（含默认 `umask 0002` 的发行版镜像）不再需要人工 chmod 即可使用 APM。
- 正面影响：其他人可写仍是硬失败，同时放行条件会输出一次告警，权限边界变化对用户可见而不是静默。
- 正面影响：两条目录链的判定矩阵由同一 ADR 固定，并由 `internal/cloud/remote` 与 `internal/catalog` 各自的表驱动用例守护，避免两侧漂移。
- 代价与风险：root 拥有的非 sticky 组可写目录（此前拒绝）现在放行；同组账号可改名或删除受检目录链上的目录项，造成安装产物落入不可达目录或 DoS。需要覆盖同组 Byzantine 写者时仍必须采用进程/目录隔离或具备 fd-bound conditional unlink 能力的可信设施。
- 代价与风险：告警只按目录路径去重，不解析组成员；目录属主无法通过 APM 侧配置消除该告警，只能 `chmod g-w`。
- 后续动作：`project-overview.md` 的“安全与合规”条目与 ADR-021 引用本 ADR；`docs/Changelog.md` 记录一次。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 仅对“属主为当前 euid”的目录放行组可写 | 覆盖 `~/.cache` 属主为用户的情形，但 root 拥有的组可写目录链（如某些发行版的共享路径）仍会失败；而 root 拥有的组可写目录在两条目录链上极少出现，收益低于规则复杂度。 |
| 新增 `APM_ALLOW_INSECURE_DIRS=1` 之类的开关，默认行为不变 | 每台宽权限机器都要用户显式设置，等于把发行版默认值变成 APM 的使用门槛；同时扩大了“关闭安全检查”的配置面。 |
| mode 检查完全降级为 warning（`0777` 也放行） | 其他人可写的目录任何本地账号都能植入目录项，放宽到这一步会真正削弱 fail-closed 边界。 |
| 要求 `/usr/bin/apm` 或安装脚本 chmod `~/.cache` | APM 无权改写用户既有目录；XDG 只约束创建行为，把它作为前置条件会让 CLI 在标准镜像上开箱不可用。 |
| 改为在私有子目录下工作（如 `~/.cache/apm` 已私有即可） | 祖先目录的 mode 仍会被逐级检查，不解决根因。 |

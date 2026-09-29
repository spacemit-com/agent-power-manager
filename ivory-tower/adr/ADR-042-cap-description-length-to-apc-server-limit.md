# 按 APC 服务端契约限定 description 长度

- Status：Accepted
- Date：2026-09-29
- Decision Makers：APM maintainers
- Related Feature：[Feature 031](../features/031-power-format-v2/spec.md)
- 局部取代说明：本 ADR 取代 [ADR-039](ADR-039-add-name-en-and-display-name-length-limits.md) 第 4 条
  （“`description_zh` trim 后非空、≤ 1024 字节、允许换行”）以及 [ADR-038](ADR-038-power-format-v2-localized-display-fields.md)
  中同一处 1024 字节表述。两个 ADR 的其余决定继续有效。

## Context

`../using-apm` 升级一批资源时，`k3-media-pipeline` 的英文译文（532 码元）通过了
`apm validate`，却在 `apm submit` 时被 APC 以 `400 VALIDATION_FAILED` 拒绝，且响应体不带字段名，
CLI 只透出 trace_id。定位后发现是长度问题，于是对测试环境做了端到端阶梯探测
（metadata 与 content 两侧字段完全一致、资源名合法、内容体积恒定）：

| 样例 | Unicode 码点 | UTF-16 码元 | UTF-8 字节 | 结果 |
| --- | --- | --- | --- | --- |
| `a` × 500 | 500 | 500 | 500 | 202 接受 |
| `a` × 501 | 501 | 501 | 501 | 400 拒绝 |
| 汉字 × 500 | 500 | 500 | 1500 | 202 接受 |
| 汉字 × 501 | 501 | 501 | 1503 | 400 拒绝 |
| `é` × 500 | 500 | 500 | 1000 | 202 接受 |
| emoji × 250 | 250 | 500 | 1000 | 202 接受 |
| emoji × 251 | 251 | 502 | 1004 | 400 拒绝 |

服务端返回的字段级错误：

```
{"code":"VALIDATION_FAILED","path":"submit.request.description","message":"description 不能超过500个字符"}
{"code":"VALIDATION_FAILED","path":"submit.request.descriptionZh","message":"description_zh 不能超过500个字符"}
```

结论有三点与既有契约不同：

1. **上限是 500 而不是 1024**：`description_zh` 与 `description` 共用同一个 500 上限，`1024` 从未生效。
2. **单位是字符而不是字节**：500 个汉字（1500 字节）被接受，而按字节解释的 1024 会让本地误拒；
   反过来 251 个 emoji 只有 1004 字节却仍被拒。计数口径与 JVM/JS 的 `String.length()` 一致，
   即 **UTF-16 码元**（BMP 内码点计 1，星光平面码点以代理对计 2）。
3. **两个字段此前都没有客户端上限**：`description` 在 `validateCommonMetadata` 里只检查非空，
   `description_zh` 检查的是永远不会先触发的 1024 字节，因此超限资源能通过离线校验、
   到提交时才失败。

需要决策的约束：

- APC 服务端是权威；客户端上限必须与它一致，否则要么放过必败的提交（欠校验），
  要么拒绝服务端本来接受的资源（过校验，如 500 汉字的正常中文描述）。
- 提交侧与安装侧的校验分层由 [ADR-038](ADR-038-power-format-v2-localized-display-fields.md) 确立，
  本轮必须继续沿用：新上限只收紧提交侧，安装、下载、缓存与目录解码路径不得因此拒绝历史资源。
- 探测到的 500 是服务端**本轮**的取值，未来可能调整；客户端硬编码该数字意味着两侧耦合。

## Decision

`description` 与 `description_zh` 在提交侧共享同一个长度上限：**500 个 UTF-16 码元**。

具体条款：

1. **新增 `maxDescriptionUnits = 500`**，替换 `maxDisplayDescriptionBytes = 1024`。
2. **计数单位是 UTF-16 码元**：新增 `descriptionUnits(value)`，对 `> 0xFFFF` 的码点计 2，其余计 1；
   不使用 UTF-8 字节数，也不使用 `utf8.RuneCountInString`（后者会把 emoji 计为 1）。
3. **`description` 纳入校验**：新增 `domain.ValidateDescription`，只校验长度（非空仍由各资源既有逻辑负责），
   并在四类资源的提交侧入口接入：`skill.parseSkillFrontmatter`、
   `pluginartifact.ValidateSubmitAPCPluginManifest`、`MCPResource.ValidateSubmit`、
   `AgentConfig.ValidateSubmit`。
4. **`description_zh` 改口径**：`ValidateDisplayDescriptionZH` 由 ≤ 1024 字节改为 ≤ 500 码元；
   错误信息点名字段、实际码元数与上限，便于资源作者直接定位。
5. **沿用 ADR-038 第 6 条的「提供时校验形状（含长度）」分层**：`ValidateOptionalDisplayMetadata`
   （安装/下载/缓存/目录解码）仍只在字段存在时校验，缺失不报错；提供的值继续走形状与长度校验，
   因此 `description_zh` 的 500 码元上限在安装侧同样生效——本轮只是把那个长度从永不触发的
   1024 字节换成真实的 500 码元，没有新增分层语义。存量资源不受影响：实测已发布目录中
   `description_zh` 最大 190 码元、`description` 最大 483 码元，均低于 500。
6. **失败信息必须可操作**：错误里带字段名与码元数，避免重现「只知道 400 + trace_id」的定位成本。
7. **不改的范围**：`name`/`version`/`name_zh`/`name_en` 的上限、APC 目录契约、
   `list`/`show`/`search` 输出与匹配语义均不变。

## Consequences

- 正面影响：超限资源在离线 `apm validate` 阶段即被拦下，不再消耗一次必然失败的提交。
- 正面影响：本地判定与服务端判定一致，500 个汉字这类正常中文描述不会被误拒
  （按字节实现会误拒，这是本轮选择实测口径的直接价值）。
- 代价与风险：`500` 是服务端实现细节，客户端硬编码后两侧存在耦合；服务端若调整上限，
  APM 需同步发版。缓解方式是错误信息写明单位与实测来源，便于比对。
- 代价与风险：单位是 UTF-16 码元而非 Unicode 码点，对含 emoji 的描述略严于直觉
  （251 个 emoji 就会被拒）；这是与服务端对齐的必然代价。
- 代价与风险：`description_zh` 曾长期以 1024 字节出现在 ADR-038/039 与 `APC-API.md` 中，
  本轮必须同步修正文档，否则文档会与实现相反。
- 后续动作：若 APC 明确把上限写入公开契约（含单位），应改为引用该契约而不是重复数字。

## Alternatives Considered

| 方案 | 未选择原因 |
| --- | --- |
| 按字面实现 `description ≤ 500 字节` / `description_zh ≤ 1024 字节` | 与服务端实测不符：500 个汉字（1500 字节）服务端接受却会被本地拒绝，251 个 emoji（1004 字节）服务端拒绝却会被本地放过；两个方向都会出错。 |
| 只在 `description_zh` 上改口径，`description` 保持无上限 | 原始故障就发生在 `description` 上；不补该字段等于不修问题。 |
| 用 Unicode 码点数（`utf8.RuneCountInString`）计数 | 251 个 emoji 会被计为 251 而放行，服务端按代理对计 502 并拒绝，仍会放过必败提交。 |
| 在安装侧彻底取消 `description_zh` 长度校验（只留形状） | 会偏离 ADR-038 第 6 条「v2 提供展示字段时校验形状（含长度）」的既有分层；且实测存量资源最大 190 码元，保留上限不会拒绝任何东西。 |
| 只在 submit 前拦截，不动 validate | 用户明确要求加在 `validate` 中；`validate` 与 `submit` 共用同一校验链，分开反而产生两条真相。 |
| 不设客户端上限，仅改进错误信息 | 无法提前发现问题，用户仍要消耗一次提交；且 APC 的 400 响应不指出资源类型，定位成本高。 |

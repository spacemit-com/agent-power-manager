# Agent Submission 与 Lifecycle 对齐实现计划

> APC 仓库说明（2026-10-08）：相邻的 APC 仓库已停止维护，本文中的 `../apc` 路径与命令只是当时的执行
> 记录，不再可用；APC 的服务端契约以仓库根目录的 `APC-API.md` 为准（见 ADR-025）。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Agent 与其它 Power 一样支持 validate、submit、审核发布治理，并补齐 Agent 内容身份闭环和 Submission 首次受理/幂等重放结果契约。

**Architecture:** APM 继续使用统一 Submission 管线，只增加聚焦的 Agent 类型处理器；`internal/domain` 提供唯一的 Agent YAML 解码和身份校验，cache、Adapter、recovery 复用它。Remote provider 严格按 HTTP 202/200 分流，Local provider 保持只读；APC 本次只更新权威业务与 HTTP 规范，不实现服务端。

**Tech Stack:** Go 1.26、Cobra、`go.yaml.in/yaml/v3`、标准库 multipart/MD5/HTTP、Structurizr DSL、Mermaid、Markdown。

**Spec:** `ivory-tower/features/022-agent-submission/spec.md`

## Global Constraints

- 执行前使用 `superpowers:using-git-worktrees`，为 APM 与 APC 分别创建隔离 worktree；不得在用户当前工作树覆盖改动。
- APM 的 Agent 业务校验是唯一内容规则；APC 只做 metadata 通用语法、Content-Type、非空/≤20 MiB、MD5 与 YAML 可解析检查。
- Agent content 是原始 `agent.yaml` 字节，Content-Type 为 `application/yaml`；不得打包、规范化或重序列化。
- Agent id 必须同时满足 canonical Power name 和安全单路径段；version 必须精确 `vMAJOR.MINOR.PATCH`；description 必须非空。
- Agent 只适用于 Bibit；不得扩展 Codex、Claude Code、Hermes 的 Agent Power Adapter。
- Local provider 始终对 Submission 返回 `cloud.ErrSubmissionUnsupported`；不得模拟本地工单。
- 损坏或身份不匹配的持久 Agent cache 必须 fail closed，不删除、不回源覆盖。
- manifest 已精确命中时 install 保持 `unchanged`，不得借本 feature 扩展为状态审计。
- 不实现 Submission cursor、时间筛选、timeline 或其它查询增强。
- 重要导出接口添加简短、规范的中文注释；不引入无关重构。
- 测试必须使用 `t.TempDir()`、fake provider/fake adapter，不读取真实 home，不调用真实 Agent CLI。
- APM 行为全部完成并全量验证后才写一次 `docs/Changelog.md`；APC 文档变更不单独写 APM Changelog。
- 每个任务提交前执行 `gofmt`（Go 任务）和 `git diff --cached --check`；文件恰好以一个换行结尾。

---

## 文件结构

### APM 生产代码

- `internal/domain/agent_config.go`：Agent YAML 严格解码、最小字段校验、`PowerRef` 身份匹配。
- `internal/domain/submission.go`：Submission accepted/replayed 结果模型。
- `internal/submission/agent.go`：Agent 单文件读取、内容安全与元数据提取。
- `internal/submission/validate.go`、`submission.go`：统一入口增加 Agent。
- `internal/cloud/submission.go`、`errors.go`：Submit 新签名与 `ErrAlreadyPublished`。
- `internal/cloud/remote/submission.go`、`client.go`：Agent multipart、HTTP 202/200 分流、problem code 解析。
- `internal/cloud/local/submission.go`：同步接口签名，继续 unsupported。
- `internal/cache/filesystem.go`：Agent 下载发布前及 cache hit 身份校验。
- `internal/adapter/agent.go`、`bibit_agent.go`：共享解析与只读 artifact 校验。
- `internal/transaction/manager.go`、`recovery.go`、`filesystem_recovery.go`：Agent resolver 与恢复前身份校验；删除原 `skill_recovery.go`。
- `internal/cli/validate.go`、`submit.go`、`submit_contract.go`：Agent flag 与 outcome 输出。
- `internal/lifecycle/submit.go`：Cloud 结果映射。

### APM 设计与项目文档

- `ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md`
- `ivory-tower/features/021-submission-validate/spec.md`
- `ivory-tower/project-overview.md`
- `BUILD.md`
- `ivory-tower/c4/workspace.dsl`
- `ivory-tower/c4/components/apm-cli.dsl`
- `ivory-tower/c4/components/apm-cli-diagrams/{cloud-client,core-lifecycle,cache-manager,adapter-registry,cli-entry}.mmd`
- `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/{install,common-journal-recovery}.mmd`

### APC 文档

- `ivory-tower/adr/ADR-001-unify-agent-submission-governance.md`
- `ivory-tower/project-overview.md`
- `ivory-tower/project-overview-apm.md`
- `docs/APC-API.md`（当前未跟踪；只显式纳入此文件）。

---

### Task 1: Agent YAML 领域解析与身份契约

**Files:**
- Modify: `internal/domain/agent_config.go`
- Modify: `internal/domain/agent_config_test.go`
- Modify valid Agent YAML fixtures in:
  - `internal/adapter/bibit_agent_test.go`
  - `internal/cache/prepare_test.go`
  - `internal/cloud/remote/agent_test.go`
  - `internal/cloud/remote/content_test.go`

**Interfaces:**
- Consumes: `domain.PowerRef`、`domain.IsCanonicalPowerName`。
- Produces:

```go
func DecodeAgentConfigYAML(raw []byte) (AgentConfig, error)
func (AgentConfig) Validate() error
func (AgentConfig) ValidatePowerRef(PowerRef) error
```

- [ ] **Step 1: 写严格 YAML 与身份 RED 测试**

在 `internal/domain/agent_config_test.go` 增加表驱动测试，至少包含：有效完整 YAML、未知字段、重复根键、重复 `agent.id`、第二个 document、根非 mapping、agent 非 mapping、description 缺失/空/非字符串、非 canonical id、非法版本，以及 ref type/name/version 不一致。

```go
func TestDecodeAgentConfigYAMLAcceptsUnknownFields(t *testing.T) {
    raw := []byte("schema_version: 1\nagent:\n  id: demo-agent\n  version: v1.2.3\n  description: demo\n  future: {enabled: true}\n")
    got, err := DecodeAgentConfigYAML(raw)
    if err != nil {
        t.Fatalf("DecodeAgentConfigYAML() error = %v", err)
    }
    if got.Agent.ID != "demo-agent" || got.Agent.Version != "v1.2.3" || got.Agent.Description != "demo" {
        t.Fatalf("DecodeAgentConfigYAML() = %+v", got)
    }
}

func TestDecodeAgentConfigYAMLRejectsDuplicateAndMultipleDocuments(t *testing.T) {
    cases := [][]byte{
        []byte("schema_version: 1\nschema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n"),
        []byte("schema_version: 1\nagent: {id: demo, id: other, version: v1.0.0, description: demo}\n"),
        []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n---\n{}\n"),
    }
    for _, raw := range cases {
        if _, err := DecodeAgentConfigYAML(raw); !errors.Is(err, ErrInvalidAgent) {
            t.Fatalf("DecodeAgentConfigYAML(%q) error = %v, want ErrInvalidAgent", raw, err)
        }
    }
}

func TestAgentConfigValidatePowerRefRequiresExactIdentity(t *testing.T) {
    config := AgentConfig{SchemaVersion: 1, Agent: AgentInfo{ID: "demo", Version: "v1.2.3", Description: "demo"}}
    if err := config.ValidatePowerRef(PowerRef{Type: PowerTypeAgent, Name: "demo", Version: "v1.2.3"}); err != nil {
        t.Fatalf("ValidatePowerRef() error = %v", err)
    }
    for _, ref := range []PowerRef{
        {Type: PowerTypeSkill, Name: "demo", Version: "v1.2.3"},
        {Type: PowerTypeAgent, Name: "other", Version: "v1.2.3"},
        {Type: PowerTypeAgent, Name: "demo", Version: "v1.2.4"},
    } {
        if err := config.ValidatePowerRef(ref); !errors.Is(err, ErrInvalidAgent) {
            t.Fatalf("ValidatePowerRef(%+v) error = %v, want ErrInvalidAgent", ref, err)
        }
    }
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/domain -run 'Test(DecodeAgentConfigYAML|AgentConfigValidatePowerRef)'`

Expected: FAIL，缺少 `DecodeAgentConfigYAML`、`Description` 或 `ValidatePowerRef`。

- [ ] **Step 3: 实现严格 node 解码与校验**

使用 `yaml.Decoder` 解第一个 `yaml.Node`，第二次 Decode 必须得到 `io.EOF`。递归遍历所有 mapping node，要求 key 为字符串 scalar，并以 tag+value 检查重复键；只从根 mapping 和直接 `agent` mapping 读取稳定字段。

```go
type AgentInfo struct {
    ID          string `yaml:"id"`
    Version     string `yaml:"version"`
    Description string `yaml:"description"`
}

func DecodeAgentConfigYAML(raw []byte) (AgentConfig, error) {
    decoder := yaml.NewDecoder(bytes.NewReader(raw))
    var document yaml.Node
    if err := decoder.Decode(&document); err != nil {
        return AgentConfig{}, fmt.Errorf("decode agent YAML: %w", errors.Join(ErrInvalidAgent, err))
    }
    var trailing yaml.Node
    if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
        if err == nil {
            err = errors.New("multiple YAML documents")
        }
        return AgentConfig{}, fmt.Errorf("decode agent YAML tail: %w", errors.Join(ErrInvalidAgent, err))
    }
    if err := validateUniqueYAMLMappingKeys(&document); err != nil {
        return AgentConfig{}, err
    }
    // 使用聚焦 helper 取得根 schema_version 与 agent mapping 的三个字符串字段。
    config, err := decodeAgentConfigNode(&document)
    if err != nil {
        return AgentConfig{}, err
    }
    if err := config.Validate(); err != nil {
        return AgentConfig{}, err
    }
    return config, nil
}

func (config AgentConfig) ValidatePowerRef(ref PowerRef) error {
    if err := config.Validate(); err != nil {
        return err
    }
    if ref.Type != PowerTypeAgent || ref.Name != config.Agent.ID || ref.Version != config.Agent.Version {
        return fmt.Errorf("agent identity does not match power %+v: %w", ref, ErrInvalidAgent)
    }
    return nil
}
```

`Validate` 改为调用 `IsCanonicalPowerName(config.Agent.ID)`、现有严格 version helper，并检查 description trim 后非空；保留 `isSafeAgentID` 作为额外路径约束。

同一任务把上述四个测试文件中意图表示“合法 Agent”的 YAML fixture 补上非空 `description`；意图测试缺字段/损坏内容的 fixture 保持原样。这样 Task 1 提交后全仓测试仍保持绿色。

- [ ] **Step 4: 运行 GREEN 与领域回归**

Run:

```bash
gofmt -w internal/domain/agent_config.go internal/domain/agent_config_test.go internal/adapter/bibit_agent_test.go internal/cache/prepare_test.go internal/cloud/remote/agent_test.go internal/cloud/remote/content_test.go
go test -count=1 ./...
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/domain/agent_config.go internal/domain/agent_config_test.go internal/adapter/bibit_agent_test.go internal/cache/prepare_test.go internal/cloud/remote/agent_test.go internal/cloud/remote/content_test.go
git diff --cached --check
git commit -m "feat(domain): validate agent submission identity"
```

---

### Task 2: Agent Validate 与原始 YAML Prepare

**Files:**
- Create: `internal/submission/agent.go`
- Create: `internal/submission/agent_test.go`
- Modify: `internal/submission/validate.go`
- Modify: `internal/submission/submission.go`
- Modify: `internal/submission/validate_test.go`
- Modify: `internal/submission/prepare_test.go`

**Interfaces:**
- Consumes: `domain.DecodeAgentConfigYAML`、`AgentConfig.ValidatePowerRef`、`contentpolicy.IsUnsafeText`。
- Produces: Agent support through existing `Validate(context.Context, Request)` and `Prepare(context.Context, Request)`.

- [ ] **Step 1: 写 Agent submission RED 测试**

```go
func TestValidateAgentReturnsRawFileMetadata(t *testing.T) {
    path := filepath.Join(t.TempDir(), "agent.yaml")
    raw := []byte("schema_version: 1\nagent:\n  id: demo-agent\n  version: v1.2.3\n  description: Demo agent\n  tools: ['*']\n")
    writeTestFile(t, path, string(raw))
    got, err := Validate(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path})
    if err != nil {
        t.Fatalf("Validate() error = %v", err)
    }
    if got.Name != "demo-agent" || got.Version != "v1.2.3" || got.Description != "Demo agent" || got.FileCount != 1 || got.ContentSize != int64(len(raw)) {
        t.Fatalf("Validate() = %+v", got)
    }
}

func TestPrepareAgentPreservesRawYAML(t *testing.T) {
    path := filepath.Join(t.TempDir(), "agent.yaml")
    raw := []byte("# comment\nschema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
    if err := os.WriteFile(path, raw, 0o644); err != nil { t.Fatal(err) }
    got, err := Prepare(context.Background(), Request{Type: domain.PowerTypeAgent, Path: path, ChangeSummary: "first"})
    if err != nil { t.Fatal(err) }
    sum := md5.Sum(raw)
    if !bytes.Equal(got.Bytes, raw) || got.ContentType != "application/yaml" || got.MD5 != hex.EncodeToString(sum[:]) {
        t.Fatalf("Prepare() = type=%q md5=%q bytes=%q", got.ContentType, got.MD5, got.Bytes)
    }
}
```

另加：空文件、目录、symlink、二进制/归档、>10 MiB、context cancelled、缺 change summary；确保两个仅空白不同的合法 YAML 得到不同 MD5。

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/submission -run 'Test(ValidateAgent|PrepareAgent)'`

Expected: FAIL，当前统一入口返回 unsupported type。

- [ ] **Step 3: 实现单次读取的 Agent source helper**

```go
type validatedAgentSource struct {
    validation Validation
    raw        []byte
}

func validateAgentSource(ctx context.Context, request Request) (validatedAgentSource, error) {
    if err := ctx.Err(); err != nil { return validatedAgentSource{}, err }
    if strings.TrimSpace(request.Version) != "" {
        return validatedAgentSource{}, fmt.Errorf("agent version comes from agent.yaml; --version is not allowed: %w", ErrInvalid)
    }
    info, err := os.Lstat(request.Path)
    if err != nil { return validatedAgentSource{}, fmt.Errorf("stat Agent source: %w", err) }
    if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
        return validatedAgentSource{}, fmt.Errorf("Agent source is not a physical regular file: %w", ErrInvalid)
    }
    if info.Size() <= 0 || info.Size() > maxSingleFileSize {
        return validatedAgentSource{}, fmt.Errorf("Agent source size %d is invalid: %w", info.Size(), ErrInvalid)
    }
    raw, err := os.ReadFile(request.Path)
    if err != nil { return validatedAgentSource{}, err }
    if contentpolicy.IsUnsafeText(raw) {
        return validatedAgentSource{}, fmt.Errorf("Agent source contains binary or archive content: %w", ErrInvalid)
    }
    config, err := domain.DecodeAgentConfigYAML(raw)
    if err != nil { return validatedAgentSource{}, errors.Join(ErrInvalid, err) }
    return validatedAgentSource{validation: Validation{
        Type: domain.PowerTypeAgent, Name: config.Agent.ID, Version: config.Agent.Version,
        Description: config.Agent.Description, ChangeSummary: request.ChangeSummary,
        FileCount: 1, ContentSize: int64(len(raw)), Warnings: []string{},
    }, raw: raw}, nil
}
```

`Validate` 的 Agent 分支只返回 helper 的 validation。`Prepare` 在进入通用 `Validate` 前给 Agent 独立分支，要求 change summary，调用同一个 helper 并用同一份已校验 raw 计算 MD5，防止校验后再次读取不同字节。

- [ ] **Step 4: 运行 GREEN 与 submission 全包测试**

Run: `gofmt -w internal/submission/*.go && go test -count=1 ./internal/submission`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/submission
git diff --cached --check
git commit -m "feat(submission): prepare raw agent yaml"
```

---

### Task 3: Submission 结果领域模型与错误分类

**Files:**
- Modify: `internal/domain/submission.go`
- Modify: `internal/domain/submission_test.go`
- Modify: `internal/cloud/errors.go`
- Modify: `internal/cloud/submission_test.go`

**Interfaces:**
- Produces:

```go
type SubmissionOutcome string
const SubmissionAccepted SubmissionOutcome = "accepted"
const SubmissionReplayed SubmissionOutcome = "replayed"

type SubmissionResult struct {
    Outcome SubmissionOutcome
    SubmissionID string
    Status SubmissionStatus
    StatusURL string
}

```

- [ ] **Step 1: 写结果常量与错误层级 RED 测试**

```go
func TestSubmissionOutcomeConstantsAreStable(t *testing.T) {
    if SubmissionAccepted != "accepted" || SubmissionReplayed != "replayed" {
        t.Fatalf("outcomes = %q/%q", SubmissionAccepted, SubmissionReplayed)
    }
}

func TestAlreadyPublishedCanJoinSubmissionConflict(t *testing.T) {
    err := errors.Join(ErrAlreadyPublished, ErrSubmissionConflict)
    if !errors.Is(err, ErrAlreadyPublished) || !errors.Is(err, ErrSubmissionConflict) {
        t.Fatalf("joined error = %v", err)
    }
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/domain ./internal/cloud -run 'Test(SubmissionOutcome|AlreadyPublished)'`

Expected: FAIL，缺少新类型与 `ErrAlreadyPublished`。

- [ ] **Step 3: 实现领域类型与错误 sentinel**

新增带中文注释的 `SubmissionOutcome` 与 `SubmissionResult`；现有 `SubmissionReceipt` 暂时保留到 Task 4 完成 Cloud 接口迁移，避免本任务产生跨包编译中间态。

```go
// ErrAlreadyPublished 表示相同资源版本和内容已发布，不会创建 Submission。
ErrAlreadyPublished = errors.New("cloud power already published")
```

- [ ] **Step 4: 运行 GREEN**

Run: `gofmt -w internal/domain/submission.go internal/domain/submission_test.go internal/cloud/errors.go internal/cloud/submission_test.go && go test -count=1 ./internal/domain ./internal/cloud`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/domain/submission.go internal/domain/submission_test.go internal/cloud/errors.go internal/cloud/submission_test.go
git diff --cached --check
git commit -m "feat(domain): model submission outcomes"
```

---

### Task 4: Remote Agent multipart、202/200 分流与 409 分类

**Files:**
- Modify: `internal/cloud/submission.go`
- Modify: `internal/cloud/local/submission.go`
- Modify: `internal/cloud/local/submission_test.go`
- Modify: `internal/cloud/remote/submission.go`
- Modify: `internal/cloud/remote/submission_test.go`
- Modify: `internal/cloud/remote/client.go`
- Modify: `internal/cloud/remote/client_test.go`
- Modify interface fakes in:
  - `internal/cloud/provider_test.go`
  - `internal/lifecycle/install_test_helpers_test.go`
  - `internal/lifecycle/list_test.go`
  - `internal/lifecycle/search_test.go`
  - `internal/lifecycle/submission_test_helpers_test.go`
  - `internal/lifecycle/uninstall_test_helpers_test.go`

**Interfaces:**
- Consumes: `domain.SubmissionResult`、`cloud.ErrAlreadyPublished`。
- Produces: `Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionResult, error)` and strict Local/Remote implementations.

- [ ] **Step 1: 写 HTTP 契约 RED 测试**

新增 httptest matrix：

- Agent request 的 metadata.type、content part `application/yaml`、raw bytes 和 MD5 精确；
- `202` receipt → accepted，StatusURL 非空；
- `200` full Submission → replayed，StatusURL 为空；
- `201`、`204`、`206` 即使 body 合法也 `ErrInvalidResponse`；
- `200` receipt body 或 `202` Submission body 均失败；
- `409 application/problem+json` code `ALREADY_PUBLISHED` 同时匹配两个 sentinel；
- 同状态其它 code 只匹配 `ErrSubmissionConflict`；
- 错误 Content-Type、重复键、未知状态、空 ID 失败。

```go
func TestSubmitAgentUsesRawYAMLPart(t *testing.T) {
    raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseMultipartForm(1 << 20); err != nil { t.Fatal(err) }
        file, header, err := r.FormFile("content")
        if err != nil { t.Fatal(err) }
        defer file.Close()
        got, _ := io.ReadAll(file)
        if header.Header.Get("Content-Type") != "application/yaml" || !bytes.Equal(got, raw) {
            t.Fatalf("content type/bytes = %q/%q", header.Header.Get("Content-Type"), got)
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusAccepted)
        _, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/api/v1/submissions/sub_1"}`))
    }))
    defer server.Close()
    client := newTestSubmissionClient(t, server)
    got, err := client.Submit(context.Background(), domain.SubmissionRequest{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0", Description: "demo", ChangeSummary: "first", ContentType: "application/yaml", Content: raw, ContentMD5: md5Hex(raw), IdempotencyKey: "7d7e6d70-13ec-4cd5-9ad6-37e49e861ac2"})
    if err != nil || got.Outcome != domain.SubmissionAccepted { t.Fatalf("Submit() = %+v, %v", got, err) }
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/cloud/remote -run 'TestSubmit'`

Expected: FAIL，现有实现把任意 2xx 当 receipt，且不能识别 `ALREADY_PUBLISHED`。

- [ ] **Step 3: 抽取安全 problem DTO 解码**

在 `client.go` 把现有重复逻辑收敛为：

```go
func decodeProblem(response *http.Response, body []byte) (problemDTO, bool)
func safeProblemContext(response *http.Response, body []byte) string
```

`decodeProblem` 保持现有 Content-Type、status、code、trace_id 校验；`safeProblemContext` 调用它格式化。Submission classifier 调用同一 decoder 读取稳定 code，不解析 detail。

- [ ] **Step 4: 迁移 Cloud 接口、Local 和全部 fake**

把 `cloud.Submission.Submit` 改为返回 `domain.SubmissionResult`，删除不再被生产接口使用的 `domain.SubmissionReceipt`。Local 返回 `domain.SubmissionResult{}, cloud.ErrSubmissionUnsupported`；上述全部 fake 同步签名并返回零值或其测试需要的明确 outcome。

- [ ] **Step 5: 实现状态分流和请求内容类型验证**

```go
switch response.StatusCode {
case http.StatusAccepted:
    receipt, err := decodeSubmissionReceipt(response)
    if err != nil { return domain.SubmissionResult{}, err }
    return domain.SubmissionResult{Outcome: domain.SubmissionAccepted, SubmissionID: receipt.SubmissionID, Status: receipt.Status, StatusURL: receipt.StatusURL}, nil
case http.StatusOK:
    replay, err := decodeSubmission(response)
    if err != nil { return domain.SubmissionResult{}, err }
    return domain.SubmissionResult{Outcome: domain.SubmissionReplayed, SubmissionID: replay.SubmissionID, Status: replay.Status}, nil
default:
    if response.StatusCode >= 200 && response.StatusCode < 300 {
        return domain.SubmissionResult{}, fmt.Errorf("unexpected remote submission status %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
    }
    return domain.SubmissionResult{}, c.classifySubmissionError(ctx, response)
}
```

提交前验证 type 与 ContentType 的精确映射：Skill/Plugin gzip、MCP JSON、Agent YAML；未知 type 或不匹配在创建 HTTP request 前失败。

`ALREADY_PUBLISHED` 返回：

```go
errors.Join(cloud.ErrAlreadyPublished, cloud.ErrSubmissionConflict)
```

并保留 problem code/trace_id 上下文。

- [ ] **Step 6: 运行 GREEN、race 与 Cloud/Lifecycle 编译回归**

Run:

```bash
gofmt -w $(rg --files internal/domain internal/cloud internal/lifecycle -g '*.go')
go test -count=1 ./internal/domain ./internal/cloud/... ./internal/lifecycle
go test -race -count=1 ./internal/cloud/remote
```

Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/domain/submission.go internal/cloud internal/lifecycle
git diff --cached --check
git commit -m "feat(cloud): submit agent resources remotely"
```

---

### Task 5: CLI 与 Lifecycle Agent Submission

**Files:**
- Modify: `internal/cli/validate.go`
- Modify: `internal/cli/validate_test.go`
- Modify: `internal/cli/submit.go`
- Modify: `internal/cli/submit_contract.go`
- Modify: `internal/cli/submit_test.go`
- Modify: `internal/lifecycle/validate_test.go`
- Modify: `internal/lifecycle/submit.go`
- Modify: `internal/lifecycle/submit_test.go`
- Modify: `internal/lifecycle/submission_test_helpers_test.go`
- Modify: `cmd/apm/main_test.go`

**Interfaces:**
- Consumes: Agent support in `submission.Validate/Prepare` and `domain.SubmissionResult`.
- Produces: `apm validate|submit --type agent` and stable `outcome` rendering.

- [ ] **Step 1: 写 CLI flag 与结果 RED 测试**

```go
func TestValidateAllowsAgentAndRejectsVersionOverride(t *testing.T) {
    var stdout, stderr bytes.Buffer
    lifecycle := &recordingValidateLifecycle{result: cli.ValidateResult{Type: "agent", Name: "demo", Version: "v1.0.0", Description: "demo", FileCount: 1, ContentSize: 10}}
    entry := newSubmissionEntry(t, &stdout, &stderr, func() (cli.ValidateLifecycle, error) { return lifecycle, nil }, unexpectedSubmit(t), unexpectedSubmission(t))
    if err := entry.Execute([]string{"validate", "agent.yaml", "--type=agent"}); err != nil {
        t.Fatalf("Execute(validate agent) error = %v", err)
    }
    if lifecycle.calls != 1 || lifecycle.flags.Type != "agent" { t.Fatalf("lifecycle = %+v", lifecycle) }

    lifecycle.calls = 0
    err := entry.Execute([]string{"validate", "agent.yaml", "--type=agent", "--version=v1.0.0"})
    if err == nil || !strings.Contains(err.Error(), "--version") { t.Fatalf("version override error = %v", err) }
    if lifecycle.calls != 0 { t.Fatalf("lifecycle calls after invalid flags = %d", lifecycle.calls) }
}

func TestSubmitAgentRequiresChangeSummary(t *testing.T) {
    var stdout, stderr bytes.Buffer
    lifecycle := &recordingSubmitLifecycle{}
    entry := newSubmissionEntry(t, &stdout, &stderr, unexpectedValidate(t), func() (cli.SubmitLifecycle, error) { return lifecycle, nil }, unexpectedSubmission(t))
    err := entry.Execute([]string{"submit", "agent.yaml", "--type=agent"})
    if err == nil || !strings.Contains(err.Error(), "--change-summary") { t.Fatalf("Execute() error = %v", err) }
    if lifecycle.calls != 0 { t.Fatalf("lifecycle calls = %d, want 0", lifecycle.calls) }
}

func TestRenderSubmitIncludesOutcome(t *testing.T) {
    var out bytes.Buffer
    err := renderSubmit(&out, SubmitResult{Outcome: "replayed", SubmissionID: "sub_1", Status: "validating"})
    if err != nil { t.Fatal(err) }
    want := "outcome: replayed\nsubmission_id: sub_1\nstatus: validating\ntrack: apm submission get sub_1\n"
    if out.String() != want { t.Fatalf("output = %q, want %q", out.String(), want) }
}
```

Lifecycle 测试分别注入 accepted/replayed fake result，断言 Type/Name/Version/ContentType/raw bytes 传给 Cloud，错误前不调用 Cloud。

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/cli ./internal/lifecycle ./cmd/apm -run 'Test(Validate|Submit|RenderSubmit)'`

Expected: FAIL，类型白名单仍限三类且结果无 Outcome。

- [ ] **Step 3: 实现 flag、结果与生命周期映射**

`requireSubmissionType` 接受 agent；`validateSubmitFlags` 对 Agent 要求 change summary 并拒绝 version。Validate 同样在 CLI 层拒绝 Agent version override，确保生命周期 factory 尚未创建。

```go
type SubmitResult struct {
    Outcome      string `json:"outcome"`
    SubmissionID string `json:"submission_id"`
    Status       string `json:"status"`
}
```

Lifecycle：

```go
result, err := s.cloud.Submit(ctx, request)
if err != nil { return cli.SubmitResult{}, err }
return cli.SubmitResult{Outcome: string(result.Outcome), SubmissionID: result.SubmissionID, Status: string(result.Status)}, nil
```

- [ ] **Step 4: 运行 GREEN 与 CLI 全包测试**

Run: `gofmt -w $(rg --files internal/cli internal/lifecycle cmd/apm -g '*.go') && go test -count=1 ./internal/cli ./internal/lifecycle ./cmd/apm`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cli internal/lifecycle cmd/apm
git diff --cached --check
git commit -m "feat(cli): validate and submit agent resources"
```

---

### Task 6: Agent Cache 身份校验与损坏缓存 fail closed

**Files:**
- Modify: `internal/cache/filesystem.go`
- Modify: `internal/cache/prepare_test.go`
- Modify: `internal/cache/agent_test.go`

**Interfaces:**
- Consumes: `domain.DecodeAgentConfigYAML`、`AgentConfig.ValidatePowerRef`。
- Produces: only validated exact Agent versions can be published or staged.

- [ ] **Step 1: 写首次下载和 hit 损坏 RED 测试**

```go
func TestPrepareAgentRejectsDownloadedIdentityBeforeCachePublication(t *testing.T) {
    source := &recordingSource{agentContents: []byte("schema_version: 1\nagent: {id: other, version: v1.0.0, description: other}\n")}
    _, cacheHome := setCacheEnvironment(t)
    store, err := cache.NewFilesystem(source)
    if err != nil { t.Fatal(err) }
    ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
    if _, err := store.PrepareAgent(context.Background(), ref); !errors.Is(err, domain.ErrInvalidAgent) {
        t.Fatalf("PrepareAgent() error = %v, want ErrInvalidAgent", err)
    }
    if _, err := os.Lstat(filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
        t.Fatalf("published invalid cache: %v", err)
    }
}

func TestPrepareAgentKeepsInvalidCacheAndDoesNotCallCloud(t *testing.T) {
    source := &recordingSource{}
    _, cacheHome := setCacheEnvironment(t)
    store, err := cache.NewFilesystem(source)
    if err != nil { t.Fatal(err) }
    cached := filepath.Join(cacheHome, "apm", "agents", "demo", "v1.0.0", "agent.yaml")
    if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil { t.Fatal(err) }
    raw := []byte("schema_version: 1\nagent: {id: demo, version: v2.0.0, description: demo}\n")
    if err := os.WriteFile(cached, raw, 0o644); err != nil { t.Fatal(err) }
    ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
    if _, err := store.PrepareAgent(context.Background(), ref); !errors.Is(err, domain.ErrInvalidAgent) { t.Fatalf("PrepareAgent() error = %v", err) }
    got, err := os.ReadFile(cached)
    if err != nil || !bytes.Equal(got, raw) { t.Fatalf("preserved cache = %q, %v", got, err) }
    if source.agentCalls != 0 { t.Fatalf("agent calls = %d, want 0", source.agentCalls) }
}
```

为支持测试，在 `recordingSource` 增加 `agentContents []byte`；为空时写入当前合法默认 Agent YAML，非空时原样写入该字段。

另加 malformed YAML、缺 description、valid hit 成功、downloaded valid identity 成功。

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/cache -run 'TestPrepareAgent'`

Expected: FAIL，当前 `cachedAgent` 只检查物理 marker。

- [ ] **Step 3: 实现精确 cache validator**

```go
func cachedAgent(root string, ref domain.PowerRef) (bool, error) {
    // 保留现有物理目录/marker 检查。
    raw, err := os.ReadFile(filepath.Join(root, "agent.yaml"))
    if err != nil { return false, err }
    config, err := domain.DecodeAgentConfigYAML(raw)
    if err != nil { return false, fmt.Errorf("validate Agent cache %q for %+v: %w", root, ref, err) }
    if err := config.ValidatePowerRef(ref); err != nil { return false, fmt.Errorf("validate Agent cache identity %q for %+v: %w", root, ref, err) }
    return true, nil
}
```

所有 call site 传入 exact ref。首次下载在临时目录内校验失败时只清理本次 temporary；命中失败不得调用 `removeAll` 或 source。

- [ ] **Step 4: 运行 GREEN、race 与 cache 回归**

Run:

```bash
gofmt -w internal/cache/*.go
go test -count=1 ./internal/cache
go test -race -count=1 ./internal/cache
```

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/cache
git diff --cached --check
git commit -m "fix(cache): verify exact agent identity"
```

---

### Task 7: Bibit Agent Adapter 共享校验接口

**Files:**
- Modify: `internal/adapter/agent.go`
- Modify: `internal/adapter/bibit_agent.go`
- Modify: `internal/adapter/bibit_agent_test.go`
- Modify AgentAdapter test fakes in:
  - `internal/lifecycle/install_test_helpers_test.go`
  - `internal/lifecycle/list_test.go`
  - `internal/lifecycle/uninstall_test_helpers_test.go`
  - `internal/transaction/agent_test.go`

**Interfaces:**
- Consumes: domain Agent decoder/identity.
- Produces:

```go
type AgentAdapter interface {
    TargetPath(domain.PowerRef) (string, error)
    ValidateArtifact(domain.PowerRef, string) error
    Install(domain.PowerRef, string) (AgentInstallResult, error)
}
```

- [ ] **Step 1: 写 artifact 与 staging 替换 RED 测试**

```go
func TestBibitAgentValidateArtifactRequiresExactIdentity(t *testing.T) {
    root, _ := newBibitAgentFixture(t, true)
    path := filepath.Join(root, "candidate.yaml")
    raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
    if err := os.WriteFile(path, raw, 0o644); err != nil { t.Fatal(err) }
    a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
    ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
    if err := a.ValidateArtifact(ref, path); err != nil { t.Fatalf("ValidateArtifact() error = %v", err) }
    mismatch := ref
    mismatch.Version = "v2.0.0"
    if err := a.ValidateArtifact(mismatch, path); !errors.Is(err, domain.ErrInvalidAgent) { t.Fatalf("mismatch error = %v", err) }
}

func TestBibitAgentInstallRejectsStagingIdentityMismatchWithoutPublishing(t *testing.T) {
    root, staging := newBibitAgentFixture(t, true)
    ref := domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0"}
    writeAgentStaging(t, staging, ref.Name, []byte("schema_version: 1\nagent: {id: demo, version: v2.0.0, description: demo}\n"))
    a := newBibitAgentAdapter(agentProfile{id: domain.AgentBibit, homeEnv: "BIANBU_AGENT_HOME", defaultRoot: ".bianbu-agent"})
    if result, err := a.Install(ref, staging); err == nil || result.Published { t.Fatalf("Install() = %+v, %v", result, err) }
    if _, err := os.Lstat(filepath.Join(root, "agents", "demo", "agent.yaml")); !errors.Is(err, os.ErrNotExist) { t.Fatalf("target exists: %v", err) }
}
```

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/adapter -run 'TestBibitAgent'`

Expected: FAIL，接口与方法不存在，Install 只校验 YAML 自身。

- [ ] **Step 3: 实现只读 artifact 校验并复用**

```go
func (a *bibitAgentAdapter) ValidateArtifact(ref domain.PowerRef, path string) error {
    info, err := os.Lstat(path)
    if err != nil { return fmt.Errorf("stat Agent artifact %q: %w", path, err) }
    if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
        return fmt.Errorf("Agent artifact %q is not physical: %w", path, ErrInvalidAgent)
    }
    raw, err := a.readFile(path)
    if err != nil { return err }
    config, err := domain.DecodeAgentConfigYAML(raw)
    if err != nil { return err }
    return config.ValidatePowerRef(ref)
}
```

Install 必须对 staging source 调用同一领域规则；为避免校验后再次读取不同文件，内部使用返回 raw 的非导出 helper，`ValidateArtifact` 丢弃 raw，`Install` 使用同一 helper 返回的 raw 发布。

更新所有 AgentAdapter fake，测试 fake 的 `ValidateArtifact` 默认返回 nil 并可记录调用。

- [ ] **Step 4: 运行 GREEN 与 adapter/lifecycle 编译回归**

Run: `gofmt -w $(rg --files internal/adapter internal/lifecycle internal/transaction -g '*.go') && go test -count=1 ./internal/adapter ./internal/lifecycle ./internal/transaction`

Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/adapter internal/lifecycle internal/transaction/agent_test.go
git diff --cached --check
git commit -m "feat(adapter): verify agent artifacts"
```

---

### Task 8: Filesystem Recovery 的 Agent 身份闭环

**Files:**
- Modify: `internal/transaction/manager.go`
- Modify: `internal/transaction/recovery.go`
- Rename: `internal/transaction/skill_recovery.go` → `internal/transaction/filesystem_recovery.go`
- Modify: `internal/transaction/agent_recovery_test.go`
- Modify: `internal/transaction/recovery_test.go`
- Modify: `internal/transaction/skill_artifacts_test.go`
- Modify resolver fakes in:
  - `internal/transaction/inspection_test.go`
  - `internal/transaction/mcp_recovery_test.go`
  - `internal/transaction/plugin_marketplace_recovery_test.go`
  - `internal/transaction/plugin_recovery_test.go`

**Interfaces:**
- Consumes: `adapter.AgentAdapter.ValidateArtifact`.
- Produces:

```go
type AgentAdapterResolver interface {
    ResolveAgent(domain.AgentID) (adapter.AgentAdapter, error)
}

type AdapterResolver interface {
    MCPAdapterResolver
    PluginAdapterResolver
    AgentAdapterResolver
}
```

- [ ] **Step 1: 写恢复材料验证 RED 测试**

为 Agent install commit、install rollback、uninstall rollback 分别增加成功与拒绝用例。拒绝 fake：

```go
type recoveryAgentAdapter struct {
    calls []struct{ ref domain.PowerRef; path string }
    err   error
}

func (a *recoveryAgentAdapter) ValidateArtifact(ref domain.PowerRef, path string) error {
    a.calls = append(a.calls, struct{ ref domain.PowerRef; path string }{ref, path})
    return a.err
}
```

每个错误用例断言：`errors.Is(err, ErrIndeterminateState)`、manifest 未写、journal 仍存在、target/backup/quarantine 未改名或删除、校验 ref/path 精确。另加 resolver error/typed nil 测试。

- [ ] **Step 2: 运行 RED**

Run: `go test -count=1 ./internal/transaction -run 'TestAgentRecovery'`

Expected: FAIL，filesystem recovery 尚不解析 Agent Adapter。

- [ ] **Step 3: 泛化文件名和入口，不改变 Skill 分支**

执行 `git mv internal/transaction/skill_recovery.go internal/transaction/filesystem_recovery.go`，将 `recoverSkill` 改名为 `recoverFilesystem`，`recovery.go` 调用同步更新。先运行现有 Skill recovery tests，保证纯命名阶段无行为变化。

Run: `go test -count=1 ./internal/transaction -run 'Test(Skill|Recovery)'`

Expected: PASS（新 Agent RED 仍可单独失败）。

- [ ] **Step 4: 实现恢复方向对应的 Agent artifact 校验**

增加聚焦 helper：

```go
func (s *Service) validateAgentRecoveryArtifact(journal Journal, ref domain.PowerRef, path string) error {
    if journal.Power.Type != domain.PowerTypeAgent { return nil }
    if isNilInterface(s.agentAdapters) {
        return fmt.Errorf("resolve Agent recovery adapter: %w", ErrIndeterminateState)
    }
    resolved, err := s.agentAdapters.ResolveAgent(journal.Agent)
    if err != nil || isNilInterface(resolved) {
        return errors.Join(fmt.Errorf("resolve Agent recovery adapter for %q: %w", journal.Agent, err), ErrIndeterminateState)
    }
    if err := resolved.ValidateArtifact(ref, path); err != nil {
        return errors.Join(fmt.Errorf("validate Agent recovery artifact %+v at %q: %w", ref, path, err), ErrIndeterminateState)
    }
    return nil
}
```

调用点必须位于任何 manifest 或 rename/remove 变更之前：commit target 用 journal.Power；install rollback backup 用 `*journal.Previous`；uninstall rollback quarantine 用 journal.Power。Previous 缺失但 backup 存在视为 invalid journal/indeterminate，不猜测。

- [ ] **Step 5: 运行 GREEN、transaction race 与全包回归**

Run:

```bash
gofmt -w internal/transaction/*.go
go test -count=1 ./internal/transaction
go test -race -count=1 ./internal/transaction
go test -count=1 ./internal/lifecycle
```

Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add internal/transaction
git diff --cached --check
git commit -m "fix(transaction): validate agent recovery identity"
```

---

### Task 9: APM 端到端契约与回归闭环

**Files:**
- Modify: `internal/cloud/contract_test.go`
- Modify: `internal/cloud/remote_test_server_test.go`
- Modify: `internal/lifecycle/submit_test.go`
- Modify: `internal/lifecycle/install_agent_test.go`
- Modify: `internal/lifecycle/fix_integration_test.go`
- Modify: `internal/cloud/resources/agents/bianbu-agent-default/v1.0.0/agent.yaml` only if existing fixture lacks a field required by the final domain contract; preserve all unrelated bytes.

**Interfaces:**
- Consumes: Tasks 1–8 complete behavior.
- Produces: independent Local/Remote contract proof and lifecycle integration proof.

- [ ] **Step 1: 写跨层 contract 与回归测试**

增加 Remote fake server Agent Submission：validate raw fixture → submit → assert server receives exact identity/content；用 `200 Submission` 重放验证 lifecycle/CLI outcome。Local contract 明确 Agent read works而 Submission unsupported。

Fix integration 构造 Agent committing journal 与错误 target YAML，断言 Fix 保留 journal/manifest；换回合法 target 后第二次 Fix 收敛。

- [ ] **Step 2: 运行聚焦测试并修正遗漏的 wiring**

Run:

```bash
go test -count=1 ./internal/cloud/... -run 'TestProviderContract|TestRemote'
go test -count=1 ./internal/lifecycle -run 'Test(Submit|InstallAgent|Fix)'
```

Expected: PASS。若新 contract 暴露失败，只允许修正 Tasks 1–8 范围内的真实 wiring/fixture 漏项；先记录失败输出作为补充 RED 证据，再做最小修复，不得扩展 Submission 查询。

- [ ] **Step 3: 运行全量测试和 race，确认四类回归**

Run:

```bash
gofmt -w $(rg --files cmd internal -g '*.go')
go test -count=1 ./...
go test -race -count=1 ./...
```

Expected: PASS。

- [ ] **Step 4: 提交集成测试与必要 wiring**

```bash
git add internal cmd
git diff --cached --check
git commit -m "test(agent): verify submission lifecycle parity"
```

---

### Task 10: APC 权威业务、API 与 ADR

**Repository:** sibling `../apc` isolated worktree.

**Files:**
- Create: `ivory-tower/adr/ADR-001-unify-agent-submission-governance.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/project-overview-apm.md`
- Add/Modify: `docs/APC-API.md`

**Interfaces:**
- Consumes: Feature 022 spec；不依赖 APM Go 内部类型名称作为 APC 权威。
- Produces: Agent-enabled APC business and HTTP contract.

- [ ] **Step 1: 记录 APC worktree 基线和用户未跟踪文件边界**

Run:

```bash
git status --short --branch
git log -5 --oneline
find docs -maxdepth 1 -type f -print 2>/dev/null | sort
```

Expected: 只识别现有 `docs/APC-API.md`；不得添加或修改其它未跟踪文件。若隔离 worktree 不携带未跟踪 API 文件，从用户工作树只读复制其确切内容到隔离 worktree后再用 `apply_patch` 修改，并在报告中记录来源。

- [ ] **Step 2: 写 ADR-001**

ADR 必须明确：四类统一治理；Agent 原始 YAML；APM 独占业务校验；APC 保留 YAML 可解析的传输级最低验收；202 accepted、200 replayed、409 ALREADY_PUBLISHED；Agent review/yank/restore；Local provider 不是 APC 业务实现。

ADR 标题为“统一 Agent Submission 与发布治理”，Status 为 Accepted，Date 为 2026-08-24，Decision Makers 为 APC maintainers；正文完整包含 Context、Decision、Consequences、Alternatives Considered 四节，不使用省略号或占位内容。

- [ ] **Step 3: 更新 project overview 的全部 Agent 排除条款**

逐项修改资源类型、角色、版本、名称治理、Submission 单位、Agent content、MD5、校验矩阵、工作流、内网接口、核心不变量和后续风险。删除 `UNSUPPORTED_RESOURCE_TYPE` 对 Agent 的描述，但未知类型仍返回该 code。

明确 Agent 最低验收只判断 YAML parser 是否接受，不读取业务字段；人工评审仍可判断内容质量。

- [ ] **Step 4: 更新 APM 对接文档**

加入 Agent validate/submit 命令、字段来源表、原始 YAML/MD5、`--change-summary`、禁止 `--version`、accepted/replayed 输出、cache/Adapter/recovery 身份闭环，以及 Local provider 只读边界。删除“Agent Submission 后续”的旧条款。

- [ ] **Step 5: 更新 `docs/APC-API.md` 接口规范**

精确更新：

- Submission metadata type/list filter/detail 支持 agent；
- multipart content matrix加入 `agent → application/yaml`；
- 202 receipt、200 idempotent Submission、409 ALREADY_PUBLISHED；删除“已发布同内容 200 metadata”；
- review/yank/restore 类型支持 agent；
- `ResourceMetadata.change_summary` 对 Agent 也必填；
- 错误码表加入 ALREADY_PUBLISHED；
- 所有仍声称 Agent unsupported/后续设计的语句删除。

- [ ] **Step 6: 文档一致性扫描与提交 APC**

Run:

```bash
rg -n 'Agent.*(不进入|后续|UNSUPPORTED_RESOURCE_TYPE)|type=agent.*UNSUPPORTED|仅支持.*skill.*plugin.*mcp|已发布.*200 OK' ivory-tower docs/APC-API.md
rg -n 'ALREADY_PUBLISHED|application/yaml|type=agent|ADR-001' ivory-tower docs/APC-API.md
git diff --check
```

Expected: 第一条只允许出现在 ADR Alternatives/历史说明且必须显式标为被 ADR-001 取代；第二条覆盖 overview、APM 对接、API 与 ADR。

```bash
git add ivory-tower/adr/ADR-001-unify-agent-submission-governance.md ivory-tower/project-overview.md ivory-tower/project-overview-apm.md docs/APC-API.md
git diff --cached --check
git commit -m "docs(agent): define submission and publication contract"
```

---

### Task 11: APM ADR、项目文档与 C4

**Files:**
- Create: `ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md`
- Modify: `ivory-tower/features/021-submission-validate/spec.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `BUILD.md`
- Modify: `ivory-tower/c4/workspace.dsl`
- Modify: `ivory-tower/c4/components/apm-cli.dsl`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/core-lifecycle.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cache-manager.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/adapter-registry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cli-entry.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/install.mmd`
- Modify: `ivory-tower/c4/components/apm-cli-lifecycle-diagrams/common-journal-recovery.mmd`

**Interfaces:**
- Consumes: final APM production behavior and APC ADR-001.
- Produces: current APM architecture documentation and ADR-019.

- [ ] **Step 1: 写 ADR-019**

记录统一 Submission 管线、domain 共享 Agent parser、cache fail closed、Adapter/recovery 身份闭环、Local 只读、202/200 分流、409 ALREADY_PUBLISHED；引用 Feature 022、ADR-015、ADR-018 和 APC ADR-001。

- [ ] **Step 2: 标记 Feature 021 的扩展关系**

只在 Feature 021 顶部增加说明：Agent Submission 由 Feature 022/ADR-019 扩展，Feature 021 主体保留历史三类范围。不要全文改写 021。

- [ ] **Step 3: 更新 overview 与 BUILD**

overview 将 Submission 描述改为四类，补充 Agent 原始 YAML、身份校验层次和 Submit outcome；Cloud Client 六端口描述必须包含 Submission。BUILD 增加 Agent validate/submit 示例和 accepted/replayed/ALREADY_PUBLISHED 解释。

- [ ] **Step 4: 更新 C4/diagram 源**

- `workspace.dsl`：Power 描述包含 Agent。
- `apm-cli.dsl`：Cloud Client Ports 明确含 Submission；domain/submission/cache/adapter/transaction 关系与生产调用方向一致。
- `cloud-client.mmd`：Submission Submit 新签名、Local unsupported、Remote 202/200。
- `core-lifecycle.mmd`：Validate/Prepare Agent 原始 YAML。
- `cache-manager.mmd`：Agent exact identity validation before publish/hit stage。
- `adapter-registry.mmd`：`ValidateArtifact` 与 Agent resolver。
- `cli-entry.mmd`：agent type、change summary、outcome。
- install/recovery 图：staging/target/backup/quarantine 校验点和 fail-closed 分支。

- [ ] **Step 5: 验证链接、Structurizr 与 Mermaid**

使用仓库既有 Structurizr/Draw.io CLI；输出只写 `mktemp -d`，完成后精确删除临时目录。至少执行：

```bash
git diff --check
rg -n 'SubmissionReceipt|Submit\(.*SubmissionRequest.*SubmissionReceipt|skill/plugin/mcp|Skill、Plugin、MCP' BUILD.md ivory-tower/project-overview.md ivory-tower/c4
```

Expected: 旧签名和把 Submission 限为三类的当前态描述为零；历史引用必须显式标记 Feature 021 范围。

- [ ] **Step 6: 提交 APM 文档**

```bash
git add BUILD.md ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md ivory-tower/features/021-submission-validate/spec.md ivory-tower/project-overview.md ivory-tower/c4
git diff --cached --check
git commit -m "docs(agent): document submission lifecycle parity"
```

---

### Task 12: 最终验证、双仓审计与 Changelog

**Files:**
- Modify: `docs/Changelog.md`

**Interfaces:**
- Consumes: all APM/APC commits from Tasks 1–11 plus Feature 022 spec/plan commits.
- Produces: one verified feature-level Changelog record and merge-ready branches.

- [ ] **Step 1: 执行 APM 新鲜全量门禁**

Run:

```bash
test -z "$(gofmt -l $(rg --files cmd internal -g '*.go'))"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
build_dir="$(mktemp -d /tmp/apm-feature-022-build.XXXXXX)"
go build -o "$build_dir/apm" ./cmd/apm
test -x "$build_dir/apm"
rm -f "$build_dir/apm"
rmdir "$build_dir"
git diff --check
```

Expected: 全部 exit 0，临时 build 目录不存在。

- [ ] **Step 2: 执行精确行为与隔离扫描**

Run:

```bash
rg -n 'os\.UserHomeDir|/home/liangff|bianbu-agent mcp|codex mcp|claude mcp|hermes mcp' internal --glob '*_test.go'
find /tmp /home/liangff /run/user/1000 -maxdepth 3 \( -name '.apm-*' -o -name 'apm-feature-022-*' \) -print 2>/dev/null
rg -n 'Submit\([^)]*SubmissionRequest\) \(domain\.SubmissionReceipt|--type must be skill, plugin, or mcp|Agent.*Submission.*(不支持|后续)' cmd internal BUILD.md ivory-tower --glob '*.go' --glob '*.md' --glob '*.mmd' --glob '*.dsl'
```

Expected: 没有真实 Agent CLI/home 使用；没有残留；旧签名/当前态三类限制为零。若测试文件中的字符串属于显式负向断言，在最终报告逐条分类，不机械删除。

- [ ] **Step 3: 审计 APC 文档 worktree**

Run in APC worktree:

```bash
git status --short --branch
git diff --check
rg -n 'Agent.*(不进入|后续|UNSUPPORTED_RESOURCE_TYPE)|type=agent.*UNSUPPORTED|已发布.*200 OK' ivory-tower docs/APC-API.md
rg -n 'ADR-001|ALREADY_PUBLISHED|application/yaml' ivory-tower docs/APC-API.md
```

Expected: tracked worktree clean；旧当前态条款为零；新契约在三份权威文档和 ADR 中一致。用户原工作树中非本 feature 内容保持原样。

- [ ] **Step 4: 按模板写唯一 Changelog 条目**

先完整读取 `/home/liangff/.codex/docs/Changelog.md` 模板。用以下命令取得实际相关提交，不手写猜测哈希：

```bash
git log --reverse --format='%h %s' 7e2e9a2^..HEAD
```

另在 APC feature worktree 中运行：

```bash
git log --reverse --format='%h %s' "$(git merge-base main HEAD)"..HEAD
```

在 `docs/Changelog.md` 顶部增加日期 `2026-08-24` 的一个“新增功能”条目，原样记录本用户请求、Feature 022/ADR-019/APC ADR-001、所有实际 APM/APC 相关提交及验证结果。不要为测试、文档或修复轮次拆多个条目。

- [ ] **Step 5: 提交 Changelog 并重复最终关键门禁**

```bash
git add docs/Changelog.md
git diff --cached --check
git commit -m "docs(changelog): record agent submission parity"
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
git diff --check
git status --short --branch
```

Expected: 全部 PASS，APM tracked worktree clean。

- [ ] **Step 6: 请求最终双阶段代码审查并按审查流程处理**

使用 `superpowers:requesting-code-review` 对 Feature 022 完整 APM branch 与 APC 文档 branch 做 spec compliance 和 code quality 审查。若有 finding，先使用 `superpowers:receiving-code-review` 验证事实，再以独立 RED/GREEN 修复提交处理；修复后重新执行 Step 1–5 的适用门禁。

- [ ] **Step 7: 集成前交付信息**

报告：APM/APC 最终 HEAD、提交列表、测试/race/vet/build/文档验证、两个工作树状态、未实现的 Submission 查询增强，以及建议在相关 overview/C4 中长期保留 ADR-019 与 APC ADR-001 引用。只有用户明确要求合入时才使用 `superpowers:finishing-a-development-branch` 执行集成。

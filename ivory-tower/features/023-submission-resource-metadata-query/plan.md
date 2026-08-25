# Submission Resource Metadata and Authenticated Query Implementation Plan

> 历史计划说明：本文记录 Feature 023 当时的实现步骤；其 Submission 契约仍有效，但关于保留
> 公开资源 cursor/latest/exact-metadata 的步骤已由 [Feature 024](../024-catalog-cache/spec.md) 与
> [ADR-021](../../adr/ADR-021-cache-versioned-resource-catalog.md) 取代。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Power submission derive identity from its canonical resource, rename the submission note to `message`, adopt the self-describing MCP v1 file, and expose an authenticated active-only Submission query capped at 20 records.

**Architecture:** `internal/submission` gains a private Source Loader boundary whose four implementations reuse canonical resource parsers and hand a deterministic content builder to common Validate/Prepare orchestration. The Cloud boundary keeps returning `MCPServer` to lifecycle while Local/Remote decode full `MCPResource`; Remote Submission uses a separate authenticated GET path and validates the bounded active response. APC has no server implementation in this scope, so its sibling repository receives only the approved API/ADR/C4 documentation.

**Tech Stack:** Go 1.23, Cobra, `encoding/json`, `gopkg.in/yaml.v3`, `net/http/httptest`, existing `jsonstrict`, existing deterministic tar/JCS helpers, Markdown, Mermaid, Structurizr DSL.

**Spec:** `ivory-tower/features/023-submission-resource-metadata-query/spec.md`

## Global Constraints

- Follow strict RED → verify RED → minimal GREEN → verify GREEN for every behavior change; no production code before the covering failing test.
- Four canonical sources are fixed: Skill `SKILL.md`, Plugin `apc-plugin.json`, MCP `MCP.json`, Agent `agent.yaml`; CLI and directory names never override identity.
- `submit` accepts only `--type` plus required trimmed `--message`; `validate` accepts no version/message; old `--version` and `--change-summary` are unsupported without compatibility aliases.
- MCP schema v1 is exactly `schema_version/name/version/description/definition`; old definition-only and old wrapper shapes fail closed.
- `message` is Submission metadata only, does not enter Power content, and does not change content MD5.
- Active status is exactly `received`, `validating`, `review_pending`, `approved`, `publishing`; list has no limit/cursor/time range/next_cursor and returns at most 20 records.
- Submission submit/get/list require `APC_API_KEY`; anonymous resource list/resolve/download must never receive the Key; no fallback to Local/cache/snapshot.
- Remote success JSON rejects duplicate keys, trailing values and missing required fields while ignoring unknown fields for forward compatibility.
- Tests use `httptest`, `t.TempDir()` and fixtures only; never invoke real APC, Agent CLI, user home, cache, manifest or journal.
- Important exported interfaces and non-obvious constraints receive concise Chinese comments.
- Do not modify Adapter, Cache, Config, Transaction, install/uninstall lifecycle behavior.
- APC repository work is documentation only; do not create server, persistence, auth or queue code.
- Do not add `docs/Changelog.md` until all behavioral tasks and final verification pass; add exactly one entry for this user request.

---

### Task 1: Domain MCP Resource and Submission Contracts

**Files:**
- Modify: `internal/domain/mcp.go`
- Modify: `internal/domain/mcp_test.go`
- Modify: `internal/domain/submission.go`
- Modify: `internal/domain/submission_test.go`

**Interfaces:**
- Consumes: existing `MCPServer.Validate`, `DecodeMCPServerJSON`, `CanonicalPowerVersion`, `IsCanonicalPowerName`, `PowerRef`.
- Produces: `MCPResource`, `DecodeMCPResourceJSON([]byte) (MCPResource, error)`, `(MCPResource).Validate() error`, `(MCPResource).ValidatePowerRef(PowerRef) error`, `IsActiveSubmissionStatus(SubmissionStatus) bool`, `SubmissionEvent`, new `Message` fields, and list types without pagination.

- [ ] **Step 1: Write failing MCP resource tests**

Add table tests that demonstrate the exact desired API:

```go
func TestDecodeMCPResourceJSONValidatesCanonicalResource(t *testing.T) {
    raw := []byte(`{"schema_version":1,"name":"filesystem","version":"v1.0.0","description":"files","definition":{"transport":"stdio","command":"npx","args":["-y","pkg"]}}`)
    got, err := DecodeMCPResourceJSON(raw)
    if err != nil { t.Fatalf("DecodeMCPResourceJSON() error = %v", err) }
    if got.Name != "filesystem" || got.Version != "v1.0.0" || got.Definition.Command != "npx" {
        t.Fatalf("DecodeMCPResourceJSON() = %+v", got)
    }
}

func TestDecodeMCPResourceJSONRejectsOldAndOpenShapes(t *testing.T) {
    cases := []string{
        `{"transport":"stdio","command":"npx"}`,
        `{"schema_version":1,"name":"demo","name":"shadow","version":"v1.0.0","description":"d","definition":{"transport":"stdio","command":"npx"}}`,
        `{"schema_version":1,"name":"demo","version":"v1.0.0","description":"d","definition":{"transport":"stdio","command":"npx"},"extra":true}`,
    }
    for _, raw := range cases {
        if _, err := DecodeMCPResourceJSON([]byte(raw)); err == nil { t.Fatalf("accepted %s", raw) }
    }
}
```

Also cover schema version, canonical name/version, non-empty description, HTTP/stdio union rules, tail JSON, and exact `ValidatePowerRef` match.

- [ ] **Step 2: Run MCP domain RED**

Run: `go test -count=1 ./internal/domain -run 'TestDecodeMCPResource|TestMCPResource'`

Expected: compile failure because `MCPResource` and `DecodeMCPResourceJSON` do not exist.

- [ ] **Step 3: Implement MCPResource minimally**

Use a closed wire type decoded through existing strict JSON infrastructure, then validate:

```go
type MCPResource struct {
    SchemaVersion int       `json:"schema_version"`
    Name          string    `json:"name"`
    Version       string    `json:"version"`
    Description   string    `json:"description"`
    Definition    MCPServer `json:"definition"`
}

func (r MCPResource) ValidatePowerRef(ref PowerRef) error {
    if ref != (PowerRef{Type: PowerTypeMCP, Name: r.Name, Version: r.Version}) {
        return fmt.Errorf("MCP resource identity does not match %s/%s@%s", ref.Type, ref.Name, ref.Version)
    }
    return nil
}
```

Decode the outer object through `jsonstrict.Decode(..., true)` into a closed wire type whose definition is a required non-null `json.RawMessage`, then pass those bytes to `DecodeMCPServerJSON`. Do not marshal/redecode through `map[string]any`, because that would weaken duplicate-key and forbidden-field checks.

- [ ] **Step 4: Write failing Submission domain tests**

Assert the new field names and bounded list model compile and behave:

```go
func TestActiveSubmissionStatusesAreExact(t *testing.T) {
    active := []SubmissionStatus{SubmissionReceived, SubmissionValidating, SubmissionReviewPending, SubmissionApproved, SubmissionPublishing}
    for _, status := range active {
        if !IsActiveSubmissionStatus(status) { t.Errorf("%q is not active", status) }
    }
    terminal := []SubmissionStatus{SubmissionValidationFailed, SubmissionRejected, SubmissionPublishFailed, SubmissionPublished, ""}
    for _, status := range terminal {
        if IsActiveSubmissionStatus(status) { t.Errorf("%q is active", status) }
    }
}

func TestSubmissionModelsCarryMessageAndTimeline(t *testing.T) {
    detail := Submission{Message: "first", Timeline: []SubmissionEvent{{EventID: "evt_1", EventType: "received", StatusTo: SubmissionReceived, Timestamp: "2026-08-24T10:00:00Z"}}}
    request := SubmissionRequest{Message: "first"}
    list := SubmissionList{Items: []SubmissionSummary{{SubmissionID: "sub_1"}}}
    if detail.Message != request.Message || len(detail.Timeline) != 1 || len(list.Items) != 1 { t.Fatal("new contracts lost data") }
}
```

- [ ] **Step 5: Run Submission domain RED**

Run: `go test -count=1 ./internal/domain -run 'TestActiveSubmission|TestSubmissionModels'`

Expected: compile failure for missing `Message`, `Timeline`, `SubmissionEvent`, or helper.

- [ ] **Step 6: Implement the new Submission types**

Replace `ChangeSummary` with `Message`, add `Description/CreatedAt/UpdatedAt/Timeline`, define event fields exactly as the spec, remove `Cursor/Limit` from `SubmissionListQuery`, and remove `NextCursor` from `SubmissionList`. Preserve all nine status constants.

- [ ] **Step 7: Run domain GREEN and commit**

Run: `gofmt -w internal/domain/mcp.go internal/domain/mcp_test.go internal/domain/submission.go internal/domain/submission_test.go && go test -count=1 ./internal/domain`

Expected: PASS.

Commit:

```bash
git add internal/domain/mcp.go internal/domain/mcp_test.go internal/domain/submission.go internal/domain/submission_test.go
git commit -m "feat(domain): define submission resource contracts"
```

---

### Task 2: Unified Submission Source Loaders

**Files:**
- Create: `internal/submission/source.go`
- Modify: `internal/submission/result.go`
- Modify: `internal/submission/submission.go`
- Modify: `internal/submission/validate.go`
- Modify: `internal/submission/skill.go`
- Modify: `internal/submission/plugin.go`
- Modify: `internal/submission/mcp.go`
- Modify: `internal/submission/agent.go`
- Modify: `internal/submission/{skill,plugin,mcp,agent,prepare,validate}_test.go`

**Interfaces:**
- Consumes: Task 1 `MCPResource` parser; existing `BuildTarGz`, `CanonicalizeJSON`, Plugin/Agent parsers and validation limits.
- Produces: private `sourceLoader`, private `source`, `Request{Type,Path,Message}`, `Validation` without message, `Content.Message`; public `Validate` and `Prepare` signatures stay unchanged.

- [ ] **Step 1: Write failing identity-source tests**

Update fixtures so Skill frontmatter includes `version: "v1.0.0"`, MCP uses the full v1 shape, and requests contain no Version/ChangeSummary. Add explicit tests:

```go
func TestValidateSkillReadsVersionFromFrontmatter(t *testing.T) {
    dir := writeSkillFixture(t, "demo", "v1.2.3", "does things")
    got, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: dir})
    if err != nil { t.Fatalf("Validate() error = %v", err) }
    if got.Version != "v1.2.3" { t.Fatalf("Version = %q", got.Version) }
}

func TestPrepareAllTypesUseRequestMessageOnly(t *testing.T) {
    // table fixtures for skill/plugin/mcp/agent
    got, err := Prepare(context.Background(), Request{Type: tc.powerType, Path: tc.path, Message: "  first release  "})
    if err != nil { t.Fatalf("Prepare() error = %v", err) }
    if got.Message != "first release" { t.Fatalf("Message = %q", got.Message) }
}
```

Add failures for missing Skill version, legacy MCP wrapper, missing message in Prepare, and prove Validate does not require message.

- [ ] **Step 2: Run Source Loader RED**

Run: `go test -count=1 ./internal/submission`

Expected: compile failures for removed Request fields/new Message or behavioral failures because Skill still reads CLI version and MCP still reads the wrapper.

- [ ] **Step 3: Introduce the loader registry**

Create the private contract:

```go
type sourceLoader interface {
    Load(context.Context, string) (source, error)
}

type source struct {
    validation  Validation
    contentType string
    build       func() ([]byte, error)
}

func loaderFor(powerType domain.PowerType) (sourceLoader, error) {
    switch powerType {
    case domain.PowerTypeSkill: return skillSourceLoader{}, nil
    case domain.PowerTypePlugin: return pluginSourceLoader{}, nil
    case domain.PowerTypeMCP: return mcpSourceLoader{}, nil
    case domain.PowerTypeAgent: return agentSourceLoader{}, nil
    default: return nil, fmt.Errorf("unsupported type %q: %w", powerType, ErrInvalid)
    }
}
```

Each Load performs the existing full offline validation exactly once and provides a closure that builds content from the validated source/path. Do not expose this abstraction outside `internal/submission`.

- [ ] **Step 4: Convert each type-specific parser**

- Skill frontmatter parser returns `(name, version, description)` and rejects missing/invalid version.
- Plugin reuses canonical `apc-plugin.json` identity and raw-tree validation.
- MCP reads `domain.DecodeMCPResourceJSON`; its build closure JCS-canonicalizes the full resource object, not only definition.
- Agent reuses `domain.DecodeAgentConfigYAML`; its build closure returns a defensive copy of the validated raw YAML.

- [ ] **Step 5: Simplify Validate and Prepare**

Use one flow:

```go
func Prepare(ctx context.Context, request Request) (Content, error) {
    message := strings.TrimSpace(request.Message)
    if message == "" { return Content{}, fmt.Errorf("submit requires --message: %w", ErrInvalid) }
    loaded, err := loadSource(ctx, request.Type, request.Path)
    if err != nil { return Content{}, err }
    data, err := loaded.build()
    // size, MD5, defensive result assembly
}
```

`Validate` calls the same `loadSource` but never invokes `build` and never inspects Message.

- [ ] **Step 6: Run Submission GREEN and commit**

Run: `gofmt -w internal/submission/*.go && go test -count=1 ./internal/submission`

Expected: PASS, including deterministic content/MD5 tests.

Commit:

```bash
git add internal/submission
git commit -m "refactor(submission): load identity from resources"
```

---

### Task 3: MCP Local and Remote Cloud Migration

**Files:**
- Modify: `internal/cloud/local/mcp.go`
- Modify: `internal/cloud/local/mcp_test.go`
- Modify: `internal/cloud/remote/mcp.go`
- Modify: `internal/cloud/remote/mcp_test.go`
- Modify: `internal/cloud/resources/mcps/company-tools/v0.0.1/MCP.json`
- Modify: `internal/cloud/resources/mcps/filesystem/v0.0.1/MCP.json`
- Modify: `internal/cloud/resources/mcps/filesystem/v0.1.0/MCP.json`

**Interfaces:**
- Consumes: Task 1 `DecodeMCPResourceJSON` and `ValidatePowerRef`; existing physical snapshot/download verification.
- Produces: unchanged `GetMCP(context.Context, PowerRef) (MCPServer, error)` behavior backed by full resource files.

- [ ] **Step 1: Write failing provider tests**

For both Local and Remote, serve/write a full MCP resource and assert only Definition is returned. Add identity mismatch, old definition-only, duplicate metadata and mutation/cancellation cases without weakening existing physical-file tests:

```go
func TestGetMCPRejectsEmbeddedIdentityMismatch(t *testing.T) {
    // requested demo@v1.0.0, payload says shadow@v1.0.0
    _, err := client.GetMCP(context.Background(), domain.PowerRef{Type: domain.PowerTypeMCP, Name: "demo", Version: "v1.0.0"})
    if err == nil { t.Fatal("GetMCP() error = nil") }
}
```

- [ ] **Step 2: Run provider RED**

Run: `go test -count=1 ./internal/cloud/local ./internal/cloud/remote -run 'Test.*MCP'`

Expected: failures because providers decode payload as bare `MCPServer`.

- [ ] **Step 3: Update Local and Remote decoding**

Decode the captured/download-verified bytes into `MCPResource`, call `ValidatePowerRef(ref)`, and return `resource.Definition.Clone()`. Local format errors remain `ErrUnsafeContent`; Remote service content errors join `ErrInvalidResponse` according to existing package taxonomy. Preserve context checks and cleanup ordering.

- [ ] **Step 4: Migrate all built-in MCP resources**

Use exact file identity from directory names and retain existing definition values:

```json
{
  "schema_version": 1,
  "name": "filesystem",
  "version": "v0.1.0",
  "description": "Filesystem MCP server",
  "definition": {"transport": "stdio", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem"]}
}
```

Descriptions must be meaningful and non-empty; do not introduce message into these files.

- [ ] **Step 5: Run provider GREEN and commit**

Run: `gofmt -w internal/cloud/local/mcp.go internal/cloud/local/mcp_test.go internal/cloud/remote/mcp.go internal/cloud/remote/mcp_test.go && go test -count=1 ./internal/cloud/local ./internal/cloud/remote`

Expected: PASS.

Commit:

```bash
git add internal/cloud/local/mcp.go internal/cloud/local/mcp_test.go internal/cloud/remote/mcp.go internal/cloud/remote/mcp_test.go internal/cloud/resources/mcps
git commit -m "feat(cloud): consume self-describing MCP resources"
```

---

### Task 4: Submit and Validate CLI/Wire Migration

**Files:**
- Modify: `internal/cli/flags.go`
- Modify: `internal/cli/validate.go`
- Modify: `internal/cli/submit.go`
- Modify: `internal/cli/{flags,validate,submit,submit_contract}_test.go`
- Modify: `internal/lifecycle/validate.go`
- Modify: `internal/lifecycle/submit.go`
- Modify: `internal/lifecycle/{validate,submit}_test.go`
- Modify: `internal/cloud/remote/submission.go`
- Modify: `internal/cloud/remote/submission_test.go`
- Modify: `internal/cloud/remote/catalog.go`
- Modify: `internal/cloud/remote/{catalog,metadata,content,client}_test.go`

**Interfaces:**
- Consumes: Task 2 `Request{Type,Path,Message}` and `Content.Message`; Task 1 `SubmissionRequest.Message`.
- Produces: submit CLI with `--message`; validate with no version; multipart/catalog wire field `message`; existing install version flag remains available to install commands.

- [ ] **Step 1: Write failing CLI contract tests**

Assert accepted commands and rejected legacy flags:

```go
func TestSubmitRequiresMessageForEveryPowerType(t *testing.T) {
    for _, powerType := range []string{"skill", "plugin", "mcp", "agent"} {
        err := entry.Execute([]string{"submit", "fixture", "--type=" + powerType})
        if err == nil || !strings.Contains(err.Error(), "--message is required") { t.Fatalf("type %s error = %v", powerType, err) }
    }
}

func TestSubmissionCommandsRejectLegacyMetadataFlags(t *testing.T) {
    for _, args := range [][]string{
        {"validate", "fixture", "--type=skill", "--version=v1.0.0"},
        {"submit", "fixture", "--type=skill", "--change-summary=first"},
        {"submit", "fixture", "--type=skill", "--version=v1.0.0", "--message=first"},
    } {
        if err := entry.Execute(args); err == nil { t.Fatalf("accepted %v", args) }
    }
}
```

Also assert lifecycle receives trimmed Message and no Version override.

- [ ] **Step 2: Run CLI/lifecycle RED**

Run: `go test -count=1 ./internal/cli ./internal/lifecycle -run 'Test(Submit|Validate|SubmissionCommands)'`

Expected: compile/behavior failures because old flags and ChangeSummary remain.

- [ ] **Step 3: Update CLI and lifecycle**

Keep `Flags.Version` because install still uses it; remove `ChangeSummary`, and add both `Message string` and `Output string` in this task. Task 6 binds Output on query commands. Do not bind Version in validate/submit. Bind:

```go
command.Flags().StringVar(&flags.Message, "message", "", "describe this submission")
```

Validate it once for every type, then pass it through lifecycle to `submission.Prepare` and `domain.SubmissionRequest`.

- [ ] **Step 4: Write failing wire/catalog tests**

Assert multipart metadata contains `message` and no `change_summary`; exact/list resource metadata accepts `message` and rejects missing/duplicate message. Include a same-content 200 replay fixture to prove message decoding does not alter outcome semantics.

- [ ] **Step 5: Run wire RED**

Run: `go test -count=1 ./internal/cloud/remote -run 'Test(Submit|List|Resolve|Metadata|Download)'`

Expected: failures because DTOs still use `change_summary`.

- [ ] **Step 6: Rename wire and catalog DTO fields**

Use:

```go
type submissionMetadataDTO struct { /* ... */ Message string `json:"message"` }
type resourceMetadataDTO struct { /* ... */ Message string `json:"message"` }
```

Require a trimmed non-empty message wherever existing code required ChangeSummary. Update all Remote fixtures to the new field; do not retain dual decoding.

- [ ] **Step 7: Run migration GREEN and commit**

Run: `gofmt -w internal/cli/*.go internal/lifecycle/validate.go internal/lifecycle/submit.go internal/lifecycle/*submit*_test.go internal/lifecycle/validate_test.go internal/cloud/remote/*.go && go test -count=1 ./internal/cli ./internal/lifecycle ./internal/cloud/remote`

Expected: PASS.

Commit:

```bash
git add internal/cli internal/lifecycle/validate.go internal/lifecycle/submit.go internal/lifecycle/validate_test.go internal/lifecycle/submit_test.go internal/cloud/remote
git commit -m "feat(submission): use resource identity and message"
```

---

### Task 5: Authenticated Active Submission Remote Query

**Files:**
- Modify: `internal/cloud/errors.go`
- Modify: `internal/cloud/errors_test.go`
- Modify: `internal/cloud/remote/submission.go`
- Modify: `internal/cloud/remote/submission_test.go`

**Interfaces:**
- Consumes: Task 1 bounded `SubmissionListQuery`, `SubmissionList`, `Submission`, `SubmissionEvent`, `IsActiveSubmissionStatus`; existing Client API key, endpoint, strict response limits and problem decoder.
- Produces: authenticated `GetSubmission`/`ListSubmissions`; full domain details; stable `ErrTooManyActiveSubmissions` joined with `ErrSubmissionConflict`.

- [ ] **Step 1: Write failing authentication and query-shape tests**

Use `httptest.Server` to capture query/header and assert:

```go
func TestListSubmissionsUsesBearerAndBoundedFilters(t *testing.T) {
    // server asserts Authorization == "Bearer official-key"
    // RawQuery contains only name=demo&status=review_pending&type=skill
    got, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{
        Status: domain.SubmissionReviewPending, Type: domain.PowerTypeSkill, Name: "demo",
    })
    if err != nil || len(got.Items) != 1 { t.Fatalf("ListSubmissions() = %+v, %v", got, err) }
}

func TestAnonymousCatalogNeverReceivesSubmissionBearer(t *testing.T) {
    // one Client has apiKey; call List/Resolve/GetMCP and assert Authorization is empty
}
```

Add empty-key failures for get/list before transport is called.

- [ ] **Step 2: Write failing response-invariant tests**

Table-test list responses for 21 items, terminal status, duplicate ID, filter mismatch, invalid RFC3339, increasing created_at, missing items and duplicate JSON keys. Table-test get for missing description/message/times/timeline, response ID mismatch, decreasing timeline, unknown fields accepted, and cross-account 404 mapping.

- [ ] **Step 3: Run Remote query RED**

Run: `go test -count=1 ./internal/cloud/remote -run 'Test(Get|List)Submissions|TestAnonymousCatalog|TestSubmissionQuery'`

Expected: failures because getJSON sends no Authorization, list still sends limit/cursor, and DTO is incomplete.

- [ ] **Step 4: Implement a Submission-only authenticated GET**

Do not change anonymous `getJSON`. Add a helper in `submission.go` that checks `c.apiKey`, creates GET with Accept + Authorization, uses `c.http`, strict `readSubmissionJSON` for 2xx, and `classifySubmissionError` for non-2xx. Preserve cancellation classification.

- [ ] **Step 5: Implement full DTO and invariant validation**

Define event/detail DTOs with pointer fields where absence must differ from zero. Validate:

```go
const maxActiveSubmissions = 20

if dto.Items == nil || len(dto.Items) > maxActiveSubmissions { return invalidResponse }
if !domain.IsActiveSubmissionStatus(status) { return invalidResponse }
if query.Status != "" && status != query.Status { return invalidResponse }
if previousCreated.Before(currentCreated) { return invalidResponse }
```

Require canonical type/name/version, non-empty IDs/descriptions/messages/event identifiers and RFC3339 timestamps. Timeline permits empty `status_from` but every non-empty status must be known; timestamps are non-decreasing. Ignore unknown JSON fields.

- [ ] **Step 6: Add quota error mapping RED/GREEN**

Test a 409 problem with code `TOO_MANY_ACTIVE_SUBMISSIONS`, then add:

```go
var ErrTooManyActiveSubmissions = errors.New("too many active submissions")
```

Return `errors.Join(cloud.ErrTooManyActiveSubmissions, cloud.ErrSubmissionConflict)` for that code. Preserve `ALREADY_PUBLISHED` behavior.

- [ ] **Step 7: Run Remote query GREEN and commit**

Run: `gofmt -w internal/cloud/errors.go internal/cloud/errors_test.go internal/cloud/remote/submission.go internal/cloud/remote/submission_test.go && go test -count=1 ./internal/cloud/remote ./internal/cloud`

Expected: PASS.

Commit:

```bash
git add internal/cloud/errors.go internal/cloud/errors_test.go internal/cloud/remote/submission.go internal/cloud/remote/submission_test.go
git commit -m "feat(cloud): query bounded authenticated submissions"
```

---

### Task 6: Submission Query CLI and Lifecycle Rendering

**Files:**
- Modify: `internal/cli/submission.go`
- Modify: `internal/cli/submission_contract.go`
- Modify: `internal/cli/submission_test.go`
- Modify: `internal/cli/submission_test_helpers_test.go`
- Modify: `internal/lifecycle/submission_query.go`
- Modify: `internal/lifecycle/submission_query_test.go`

**Interfaces:**
- Consumes: Task 1 domain query/detail/event models; Task 4 `Flags.Output`; Task 5 Remote response contract.
- Produces: validated list flags, `--output text|json` on get/list, full text/JSON render, lifecycle pass-through of only status/type/name.

- [ ] **Step 1: Write failing flag and lifecycle tests**

Assert list rejects terminal/unknown status, invalid type/name/output and old `--limit`; get rejects invalid output and empty trimmed ID. Assert valid lifecycle input is exactly:

```go
domain.SubmissionListQuery{
    Status: domain.SubmissionReviewPending,
    Type: domain.PowerTypeSkill,
    Name: "demo",
}
```

- [ ] **Step 2: Write failing render tests**

Text list empty must equal `No active submissions.\n`; populated line must equal `sub_1 skill/demo@v1.0.0 review_pending 2026-08-24T10:00:00Z\n`. Text detail must include description, message, timestamps and ordered timeline. JSON list must decode to an object with only `items`; JSON get must expose snake_case full fields.

- [ ] **Step 3: Run CLI query RED**

Run: `go test -count=1 ./internal/cli ./internal/lifecycle -run 'TestSubmission'`

Expected: compile/behavior failures for Output/full detail and old limit behavior.

- [ ] **Step 4: Implement local argument validation**

Bind `--output` with default `text` on get/list. Validate active status with `IsActiveSubmissionStatus`, type against four Power values, name with `domain.IsCanonicalPowerName`, and output against `text|json`. Remove `--limit` binding.

- [ ] **Step 5: Implement explicit output DTOs**

Do not add JSON tags to domain solely for CLI. Define private output structs whose fields exactly mirror APC snake_case; convert domain values before encoding with indentation. Text rendering must not infer status transitions and must preserve timeline order.

- [ ] **Step 6: Run CLI query GREEN and commit**

Run: `gofmt -w internal/cli/submission.go internal/cli/submission_contract.go internal/cli/submission*_test.go internal/lifecycle/submission_query.go internal/lifecycle/submission_query_test.go && go test -count=1 ./internal/cli ./internal/lifecycle`

Expected: PASS.

Commit:

```bash
git add internal/cli/submission.go internal/cli/submission_contract.go internal/cli/submission_test.go internal/cli/submission_test_helpers_test.go internal/lifecycle/submission_query.go internal/lifecycle/submission_query_test.go
git commit -m "feat(cli): show active submission details"
```

---

### Task 7: Cross-Layer Contract and Composition Verification

**Files:**
- Modify: `internal/cloud/contract_test.go`
- Modify: `internal/cloud/remote_test_server_test.go`
- Modify: `internal/cloud/local/submission_test.go`
- Modify: `internal/cloud/submission_test.go`
- Modify: `internal/cloud/provider_test.go`
- Modify: `cmd/apm/main_test.go`
- Modify: `cmd/apm/cloud_test.go`
- Modify: any compile-only test double in `internal/lifecycle/*_test.go` or `internal/cli/*_test.go` required by the changed domain interface

**Interfaces:**
- Consumes: all Tasks 1–6 production interfaces.
- Produces: one coherent Local/Remote/client/CLI contract with no stale fields, real paths or credential leakage.

- [ ] **Step 1: Add failing end-to-end contract cases**

Extend the fake Remote APC so a real `remote.Client` through lifecycle can:

1. submit each Power with resource-derived identity and message;
2. list one active record with Bearer auth and no pagination query;
3. get its full detail/timeline;
4. return `TOO_MANY_ACTIVE_SUBMISSIONS`;
5. download a full MCPResource and yield only Definition.

Assert Local continues returning `ErrSubmissionUnsupported` for all Submission methods.
Add `TestBundledMCPResourcesUseCanonicalV1Schema`, which walks `internal/cloud/resources/mcps`, decodes every `MCP.json` with `domain.DecodeMCPResourceJSON`, and verifies embedded name/version against its directory path.

- [ ] **Step 2: Run cross-layer RED**

Run: `go test -count=1 ./cmd/apm ./internal/cloud/... ./internal/lifecycle ./internal/cli`

Expected: failures identify stale test doubles/fixtures or missing composition coverage.

- [ ] **Step 3: Update test doubles and composition assertions**

Update only tests/fixtures unless a proven production integration gap appears. Confirm `newSubmitLifecycle` and `newSubmissionQueryLifecycle` reject blank `APC_API_KEY`, while anonymous install/search/list can create a Remote Client with no Key. Assert no captured anonymous request contains Authorization.

- [ ] **Step 4: Run cross-layer GREEN, stale scans and commit**

Run:

```bash
gofmt -w cmd/apm/*_test.go internal/cloud/*_test.go internal/cloud/local/*_test.go internal/cloud/remote/*_test.go internal/lifecycle/*_test.go internal/cli/*_test.go
go test -count=1 ./cmd/apm ./internal/cloud/... ./internal/lifecycle ./internal/cli
! rg -n 'ChangeSummary|change_summary|--change-summary' --glob '*.go' --glob '*.json' cmd internal
go test -count=1 ./internal/cloud -run 'TestBundledMCPResourcesUseCanonicalV1Schema'
```

Expected: all tests PASS; stale application-code scan empty; the exact fixture walk decodes every bundled MCP file as canonical v1 and verifies path identity.

Commit:

```bash
git add cmd/apm/*_test.go internal/cloud internal/lifecycle internal/cli
git commit -m "test(submission): verify resource and query contracts"
```

---

### Task 8: APM and APC Documentation Alignment

**Files (APM worktree):**
- Modify: `BUILD.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/c4/components/apm-cli-diagrams/cloud-client.mmd`
- Modify: `ivory-tower/adr/ADR-018-submission-and-local-validation.md`
- Modify: `ivory-tower/adr/ADR-019-align-agent-submission-lifecycle.md`
- Modify: `ivory-tower/features/021-submission-validate/spec.md`
- Modify: `ivory-tower/features/022-agent-submission/spec.md`

**Files (APC worktree):**
- Modify: `docs/APC-API.md`
- Modify: `ivory-tower/project-overview.md`
- Modify: `ivory-tower/project-overview-apm.md`
- Modify: `ivory-tower/c4/components/apm_cli.dsl`
- Modify if relationship text changes: `ivory-tower/c4/containers/apm.dsl`
- Modify: `ivory-tower/adr/ADR-001-unify-agent-submission-governance.md`
- Modify: `ivory-tower/adr/ADR-002-normalize-interface-contract.md`

**Interfaces:**
- Consumes: completed APM behavior and both approved Feature specs/ADRs.
- Produces: current user-facing commands, exact APC interface authority, and explicit historical supersession links.

- [ ] **Step 1: Record the documentation RED scan**

Run in both worktrees and save matches in the task report:

```bash
rg -n -- '--change-summary|change_summary|submit .*--version|MCP definition|next_cursor|created_before|created_after|submission.*cursor|limit=100' BUILD.md ivory-tower ../apc/docs ../apc/ivory-tower
```

Expected: current docs contain stale command, field, MCP and Submission pagination descriptions.

- [ ] **Step 2: Update APM current docs and supersession notices**

- BUILD uses the exact four commands in Feature 023.
- Project Overview documents Source Loader, full MCPResource, authenticated active-only query and 20 cap.
- Mermaid class diagram includes `MCPResource`, `Message`, `SubmissionEvent`; Submission query has no pagination.
- ADR-018/019 and Feature 021/022 retain historical text but add a top notice: metadata/CLI/MCP/query portions are superseded by ADR-020/Feature 023.

- [ ] **Step 3: Update APC API authority**

Replace current Submission-specific `change_summary` with `message`; replace MCP definition-only content with full v1 resource; replace §6.2 pagination contract with status/type/name active-only response; add the five active statuses, 20-account cap, creation check order and `TOO_MANY_ACTIVE_SUBMISSIONS`. Do not remove cursor from general resource or internal review-ticket APIs.

- [ ] **Step 4: Update APC overview, C4 and supersession notices**

APC current design must distinguish developer active list from internal/history queries. ADR-001/002 retain history but link ADR-003 as the current Submission list/message rule. C4 describes API Key-authenticated submit/get/list and full MCP content. Do not invent storage tables or service implementation.

- [ ] **Step 5: Verify docs and commit both repositories**

Run:

```bash
git diff --check
git -C ../apc diff --check
rg -n 'ADR-020|Feature 023' BUILD.md ivory-tower/project-overview.md ivory-tower/adr ivory-tower/features/02*
rg -n 'ADR-003|Feature 001' ../apc/docs/APC-API.md ../apc/ivory-tower
```

Render/parse the changed Mermaid source with the repository's available draw.io CLI, and validate APC Structurizr workspace with the available Structurizr command/container. Classify remaining old terms: only explicitly superseded historical text may remain; current API/current docs must be empty.

Commit APM:

```bash
git add BUILD.md ivory-tower
git commit -m "docs(submission): document resource metadata queries"
```

Commit APC in its worktree:

```bash
git add docs/APC-API.md ivory-tower
git commit -m "docs(api): simplify authenticated submissions"
```

---

### Task 9: Changelog, Full Verification, and Completion Audit

**Files:**
- Modify: `docs/Changelog.md`
- Test only: whole APM repository
- Inspect only: whole APC documentation diff

**Interfaces:**
- Consumes: every completed/reviewed Task 1–8 commit.
- Produces: one user-request Changelog entry and authoritative completion evidence.

- [ ] **Step 1: Audit every spec requirement against current files**

Create a report table mapping Feature 023 §§3–12 and APC Feature 001 §§2–9 to concrete code/tests/docs. Any missing or indirect evidence is a defect to fix through a dedicated RED/GREEN commit before continuing; do not treat a green narrow test as proof of a broad requirement.

- [ ] **Step 2: Run focused security and stale-state checks**

Run:

```bash
! rg -n 'ChangeSummary|change_summary|--change-summary' --glob '*.go' --glob '*.json' cmd internal
! rg -n 'Cursor|NextCursor|created_before|created_after' internal/domain/submission.go internal/cloud/remote/submission.go internal/cli/submission.go
rg -n 'schema_version|name|version|description|definition' internal/cloud/resources/mcps --glob MCP.json
! rg -n 'os\.UserHomeDir|/home/|bianbu-agent mcp|hermes mcp|codex mcp|claude mcp' internal/*/*submission*test.go internal/cloud/*/*mcp_test.go
```

Expected: no stale production contract or real-environment test reference; every MCP fixture exposes all five required keys.

- [ ] **Step 3: Run full APM gates**

Run:

```bash
git diff --name-only --diff-filter=ACMRT "$(git merge-base main HEAD)"..HEAD -- '*.go' | xargs -r gofmt -w
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
build_dir=$(mktemp -d)
go build -o "$build_dir/apm" ./cmd/apm
test -x "$build_dir/apm"
rm -f "$build_dir/apm"
rmdir "$build_dir"
git diff --check
```

Expected: every command exits 0 and the temporary build directory is removed exactly.

- [ ] **Step 4: Verify APC docs and repository hygiene**

Run APC Markdown link, Structurizr and stale-current-contract checks; verify both worktrees are clean except the pending APM Changelog. Confirm APC contains no new server source or runtime configuration.

- [ ] **Step 5: Add the single Changelog entry**

Read `/home/liangff/.codex/docs/Changelog.md` and add one reverse-chronological Feature 023 entry covering all APM behavior commits, Remote/Local validation, tests, docs, limitations and exact verification commands. Do not add an APC Changelog unless that repository's own instructions require one.

- [ ] **Step 6: Commit and re-run final gates**

Commit:

```bash
git add docs/Changelog.md
git commit -m "docs(changelog): record submission metadata queries"
```

Re-run `go test -count=1 ./...`, `go test -race -count=1 ./...`, `go vet ./...`, isolated `go build`, both diff checks, and final clean-status checks. Expected: PASS and clean worktrees.

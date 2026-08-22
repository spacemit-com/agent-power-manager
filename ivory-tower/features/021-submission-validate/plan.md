# Submission 与本地校验实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 APM 实现 validate / submit / submission get|list 命令与 cloud 的 Submission 接口。

**Architecture:** 校验、确定性打包与 MCP JCS 收进新包 `internal/submission`（纯逻辑、内存内）；`internal/domain` 新增提交领域类型；`internal/cloud.Client` 新增 Submission 端口，remote 完整实现、local 空实现；CLI 沿用 contract + lifecycle factory 接线，`cmd/apm` 注入 API Key 与 base URL。

**Tech Stack:** Go 1.26、spf13/cobra、Masterminds/semver、`archive/tar` + `compress/gzip` + `mime/multipart`（标准库）。

**Spec:** `ivory-tower/features/021-submission-validate/spec.md`

## Global Constraints

- 所有网络/文件方法接收 `context.Context`；provider 只验证并写调用方私有目标，不解释 cache/staging/manifest/journal。
- 校验与打包全程内存内，不写缓存/临时文件，不进入 `internal/transaction` journal。
- 名称用 `domain.IsCanonicalPowerName`（1–64 小写 kebab）；版本严格 `vMAJOR.MINOR.PATCH`（semver，无 prerelease/metadata）。
- 提交方向 `.tar.gz` 由 APM 生成，属受信产物：校验在压缩前完成，打包后不解包复核。
- MCP 只允许 HTTPS、禁止秘密字段，definition 用 RFC 8785 JCS 规范化。
- 每包一个聚合 export；包内注释用中文说明用途与约束（APM AGENTS.md）。
- 测试用 `APM_HOME` 隔离，不读写用户目录；修改环境变量的测试不 `t.Parallel()`。
- 文件以恰好一个换行结尾；`git diff --cached --check` 必须通过。

---

## 文件结构

- 新增 `internal/domain/submission.go`：提交领域类型与状态。
- 新增 `internal/cloud/submission.go`：Submission 端口 + `Client` 嵌入；`internal/cloud/errors.go` 增补错误。
- 新增 `internal/submission/`：`validate.go`（校验入口与通用元数据/文件安全）、`skill.go`、`plugin.go`、`mcp.go`、`archive.go`（确定性打包）、`jcs.go`（RFC 8785）、`submission.go`（Prepare 编排）、`result.go`（校验结果与错误）。
- 新增 `internal/cloud/remote/submission.go`：remote 提交实现 + 私有 DTO。
- 新增 `internal/cloud/local/submission.go`：local 空实现。
- 新增 `internal/lifecycle/validate.go`、`submit.go`、`submission_query.go`。
- 新增 `internal/cli/validate_contract.go`、`validate.go`、`submit_contract.go`、`submit.go`、`submission_contract.go`、`submission.go`。
- 修改 `cmd/apm/main.go`、`cmd/apm/cloud.go`。
- 新增 `ivory-tower/adr/ADR-018-submission-and-local-validation.md`。

## 错误码约定

沿用 `cloud` 包的 `ErrNotFound`/`ErrYanked`/`ErrInvalidResponse`/`ErrIntegrity`/`ErrProviderConfig`，新增：`ErrSubmissionUnsupported`（provider 不支持提交）、`ErrSubmissionConflict`（409 冲突）、`ErrAuth`（401/403）、`ErrUnavailable`（503）。

## 类型契约（各任务统一使用）

`internal/domain/submission.go` 最终导出：

~~~go
type SubmissionStatus string

const (
    SubmissionReceived         SubmissionStatus = "received"
    SubmissionValidating       SubmissionStatus = "validating"
    SubmissionValidationFailed SubmissionStatus = "validation_failed"
    SubmissionReviewPending    SubmissionStatus = "review_pending"
    SubmissionRejected         SubmissionStatus = "rejected"
    SubmissionApproved         SubmissionStatus = "approved"
    SubmissionPublishing       SubmissionStatus = "publishing"
    SubmissionPublishFailed    SubmissionStatus = "publish_failed"
    SubmissionPublished        SubmissionStatus = "published"
)

type SubmissionRequest struct {
    Type           PowerType
    Name           string
    Version        string
    Description    string
    ChangeSummary  string
    ContentType    string // "application/gzip" | "application/json"
    Content        []byte
    ContentMD5     string
    IdempotencyKey string
}

type SubmissionReceipt struct {
    SubmissionID string
    Status       SubmissionStatus
    StatusURL    string
}

type Submission struct {
    SubmissionID     string
    Type             PowerType
    Name             string
    Version          string
    Status           SubmissionStatus
    ContentAvailable bool
    ValidationErrors []string
    ReviewComment    string
    PublishedVersion string
}

type SubmissionSummary struct {
    SubmissionID string
    Type         PowerType
    Name         string
    Version      string
    Status       SubmissionStatus
    CreatedAt    string
}

type SubmissionListQuery struct {
    Status SubmissionStatus
    Type   PowerType
    Name   string
    Cursor string
    Limit  int
}

type SubmissionList struct {
    Items      []SubmissionSummary
    NextCursor string
}
~~~

`internal/cloud/submission.go`：

~~~go
type Submission interface {
    Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error)
    GetSubmission(context.Context, string) (domain.Submission, error)
    ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error)
}

type Client interface {
    Catalog; SkillSource; PluginSource; MCPSource; AgentSource; Submission
}
~~~

`internal/submission` 的对外函数（Task 3–9 产出）：

~~~go
// result.go
var ErrInvalid = errors.New("invalid submission content")

type Request struct {
    Type         domain.PowerType // skill | plugin | mcp
    Path         string           // skill/plugin 源目录，或 mcp JSON 文件
    Version      string           // 仅 skill 必填；plugin/mcp 从源读取
    ChangeSummary string          // skill/plugin 必填；mcp 从源读取
}

type Validation struct {
    Type, Name, Version, Description string
    FileCount int
    ContentSize int64
    Warnings []string
}

type Content struct {
    Type domain.PowerType
    Name, Version, Description, ChangeSummary string
    ContentType string
    Bytes []byte
    MD5   string
}

func Validate(ctx context.Context, req Request) (Validation, error)
func Prepare(ctx context.Context, req Request) (Content, error)
func BuildTarGz(root string) ([]byte, error)
func CanonicalizeJSON(raw json.RawMessage) ([]byte, error)
~~~

---

### Task 1: 提交领域类型

**Files:** Create `internal/domain/submission.go`；Test `internal/domain/submission_test.go`

**Interfaces:** Produces 上方类型契约里的 `SubmissionStatus` 常量与全部提交结构。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/domain/submission_test.go
package domain

import "testing"

func TestSubmissionStatusConstantsAreStable(t *testing.T) {
    if string(SubmissionReceived) != "received" || string(SubmissionPublished) != "published" {
        t.Fatal("status constants must be stable")
    }
}

func TestSubmissionRequestRoundTrip(t *testing.T) {
    r := SubmissionRequest{Type: PowerTypeSkill, Name: "demo", Version: "v1.0.0",
        ContentType: "application/gzip", Content: []byte("b"), ContentMD5: "8c7dd922ad47494fc02c388e12c00eac"}
    if r.Type != PowerTypeSkill || r.ContentMD5 == "" { t.Fatal("fields not preserved") }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/domain/ -run TestSubmissionStatus -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — 按类型契约写入 `internal/domain/submission.go`（`package domain`，简短中文注释）。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/domain/ -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/domain/submission.go internal/domain/submission_test.go && git commit -m "feat(domain): add submission domain types"`

---

### Task 2: cloud Submission 端口与错误

**Files:** Create `internal/cloud/submission.go`；Modify `internal/cloud/errors.go`；Test `internal/cloud/submission_test.go`

**Interfaces:** Consumes Task 1；Produces `cloud.Submission`、`cloud.Client` 嵌入 Submission、4 个新错误。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/cloud/submission_test.go
package cloud

import (
    "context"
    "errors"
    "testing"

    "apm/internal/domain"
)

type stubSubmission struct{ err error }

func (s stubSubmission) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error) {
    return domain.SubmissionReceipt{}, s.err
}
func (s stubSubmission) GetSubmission(context.Context, string) (domain.Submission, error) {
    return domain.Submission{}, s.err
}
func (s stubSubmission) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
    return domain.SubmissionList{}, s.err
}

func TestClientEmbedsSubmission(t *testing.T) {
    var c Client = struct{ stubSubmission }{stubSubmission{err: ErrSubmissionUnsupported}}
    if _, err := c.Submit(context.Background(), domain.SubmissionRequest{}); !errors.Is(err, ErrSubmissionUnsupported) {
        t.Fatalf("err = %v, want ErrSubmissionUnsupported", err)
    }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/cloud/ -run TestClientEmbedsSubmission -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — `internal/cloud/submission.go` 定义接口并让 `Client` 嵌入；`errors.go` 追加 `ErrSubmissionUnsupported`/`ErrSubmissionConflict`/`ErrAuth`/`ErrUnavailable` 四个 `errors.New` 哨兵。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/cloud/ -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/cloud/submission.go internal/cloud/errors.go internal/cloud/submission_test.go && git commit -m "feat(cloud): add Submission port and errors"`

---

### Task 3: RFC 8785 JCS 规范化

**Files:** Create `internal/submission/jcs.go`；Test `internal/submission/jcs_test.go`

**Interfaces:** Produces `CanonicalizeJSON(raw json.RawMessage) ([]byte, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/jcs_test.go
package submission

import "testing"

func TestCanonicalizeJSONSortsKeysAndMinimalEscapes(t *testing.T) {
    input := []byte("{\"z\":1,\"a\":\"b\",\"nested\":{\"y\":2,\"x\":\"<&>\"}}")
    got, err := CanonicalizeJSON(input)
    if err != nil { t.Fatalf("CanonicalizeJSON: %v", err) }
    want := "{\"a\":\"b\",\"nested\":{\"x\":\"<&>\",\"y\":2},\"z\":1}"
    if string(got) != want { t.Fatalf("got %s, want %s", got, want) }
}

func TestCanonicalizeJSONEscapesQuoteAndBackslash(t *testing.T) {
    got, err := CanonicalizeJSON([]byte("{\"s\":\"a\\\"b\\\\c\"}"))
    if err != nil { t.Fatal(err) }
    want := "{\"s\":\"a\\\"b\\\\c\"}"
    if string(got) != want { t.Fatalf("got %s, want %s", got, want) }
}

func TestCanonicalizeJSONRejectsTrailingValue(t *testing.T) {
    if _, err := CanonicalizeJSON([]byte("{} {}")); err == nil {
        t.Fatal("want error for trailing JSON value")
    }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestCanonicalizeJSON -count=1`，Expected: 编译失败（包不存在）。
- [ ] **Step 3: 实现** — 用 `encoding/json` 的 `Decoder.UseNumber()` 解析为 `any`，递归规范序列化：对象键按 UTF-16 码元升序；字符串仅转义 `\\` 与 `\\"`，控制字符转义为 `\\uXXXX`；数字按 `json.Number` 规范化（整数去前导零，浮点用最短表示，指数 `e`→`E` 且去正号）；无多余空白。前置 `jsonstrict.Decode` 拒绝重复键与尾随值。实现骨架：

~~~go
// internal/submission/jcs.go
package submission

import (
    "bytes"
    "encoding/json"
    "sort"
    "strconv"
    "strings"

    "apm/internal/utils/jsonstrict"
)

// CanonicalizeJSON 按 RFC 8785 生成确定 UTF-8 字节；先拒绝重复键与尾随值。
func CanonicalizeJSON(raw json.RawMessage) ([]byte, error) {
    var probe any
    if err := jsonstrict.Decode(raw, &probe, false); err != nil { return nil, err }
    dec := json.NewDecoder(bytes.NewReader(raw))
    dec.UseNumber()
    var value any
    if err := dec.Decode(&value); err != nil { return nil, err }
    var buf bytes.Buffer
    writeJCS(&buf, value)
    return buf.Bytes(), nil
}

func writeJCS(buf *bytes.Buffer, value any) {
    switch v := value.(type) {
    case map[string]any:
        keys := make([]string, 0, len(v))
        for k := range v { keys = append(keys, k) }
        sort.Strings(keys)
        buf.WriteByte('{')
        for i, k := range keys {
            if i > 0 { buf.WriteByte(',') }
            writeJCSString(buf, k)
            buf.WriteByte(':')
            writeJCS(buf, v[k])
        }
        buf.WriteByte('}')
    case []any:
        buf.WriteByte('[')
        for i, item := range v {
            if i > 0 { buf.WriteByte(',') }
            writeJCS(buf, item)
        }
        buf.WriteByte(']')
    case json.Number:
        buf.WriteString(canonicalNumber(string(v)))
    case string:
        writeJCSString(buf, v)
    case bool:
        if v { buf.WriteString("true") } else { buf.WriteString("false") }
    case nil:
        buf.WriteString("null")
    }
}

func writeJCSString(buf *bytes.Buffer, s string) {
    buf.WriteByte('"')
    for _, r := range s {
        switch r {
        case '"': buf.WriteString("\\\"")
        case '\\': buf.WriteString("\\\\")
        default:
            if r < 0x20 { buf.WriteString("\\u") } else { buf.WriteRune(r) }
        }
    }
    buf.WriteByte('"')
}

func canonicalNumber(number string) string {
    if !strings.ContainsAny(number, ".eE") {
        if i, err := strconv.ParseInt(number, 10, 64); err == nil { return strconv.FormatInt(i, 10) }
    }
    return number
}
~~~

> 实现时对照 RFC 8785 完整向量补齐数字指数序列化与 `\\u` 代理对/大小写细节，以能通过官方向量为准。

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestCanonicalizeJSON -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/jcs.go internal/submission/jcs_test.go && git commit -m "feat(submission): add RFC 8785 JSON canonicalization"`

---

### Task 4: 确定性 tar.gz 打包

**Files:** Create `internal/submission/archive.go`；Test `internal/submission/archive_test.go`

**Interfaces:** Produces `BuildTarGz(root string) ([]byte, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/archive_test.go
package submission

import (
    "bytes"
    "os"
    "path/filepath"
    "testing"
)

func TestBuildTarGzIsDeterministic(t *testing.T) {
    root := writeTree(t)
    first, err := BuildTarGz(root)
    if err != nil { t.Fatal(err) }
    second, err := BuildTarGz(root)
    if err != nil { t.Fatal(err) }
    if !bytes.Equal(first, second) { t.Fatal("same directory must produce identical bytes") }
}

func TestBuildTarGzChangesOnContentEdit(t *testing.T) {
    root := writeTree(t)
    first, _ := BuildTarGz(root)
    os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("changed"), 0o644)
    second, _ := BuildTarGz(root)
    if bytes.Equal(first, second) { t.Fatal("bytes must change when a file changes") }
}

func writeTree(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    files := map[string]string{"SKILL.md": "hello", "a.txt": "A", "sub/b.txt": "B"}
    for rel, body := range files {
        p := filepath.Join(root, filepath.FromSlash(rel))
        os.MkdirAll(filepath.Dir(p), 0o755)
        os.WriteFile(p, []byte(body), 0o644)
    }
    return root
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestBuildTarGz -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — 递归收集目录与普通文件，按 `filepath.ToSlash` 相对路径排序；tar header 固定 `Uid=0, Gid=0, ModTime=time.Unix(0,0)`，目录 `Mode=0755`、文件 `Mode` 保留执行位并清 setuid/setgid/sticky；gzip `Header.ModTime=time.Time{}`、`Header.Name=""`；归档根即资源根。实现骨架：

~~~go
// internal/submission/archive.go
package submission

import (
    "archive/tar"
    "bytes"
    "compress/gzip"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
    "time"
)

// BuildTarGz 从资源根目录确定性打包为 .tar.gz；根目录即归档根。
func BuildTarGz(root string) ([]byte, error) {
    entries, err := collectEntries(root)
    if err != nil { return nil, err }
    var buf bytes.Buffer
    gz := gzip.NewWriter(&buf)
    gz.Header.ModTime = time.Time{}
    gz.Header.Name = ""
    tw := tar.NewWriter(gz)
    for _, e := range entries {
        h := &tar.Header{Name: e.name, Mode: e.mode, Uid: 0, Gid: 0, ModTime: time.Unix(0, 0),
            Typeflag: e.typeflag, Size: int64(len(e.body))}
        if err := tw.WriteHeader(h); err != nil { return nil, err }
        if e.typeflag == tar.TypeReg { if _, err := tw.Write(e.body); err != nil { return nil, err } }
    }
    if err := tw.Close(); err != nil { return nil, err }
    if err := gz.Close(); err != nil { return nil, err }
    return buf.Bytes(), nil
}

type archiveEntry struct {
    name string
    mode int64
    typeflag byte
    body []byte
}

func collectEntries(root string) ([]archiveEntry, error) {
    var entries []archiveEntry
    err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        rel, err := filepath.Rel(root, path)
        if err != nil { return err }
        if rel == "." { return nil }
        name := filepath.ToSlash(rel)
        if d.IsDir() {
            entries = append(entries, archiveEntry{name: name + "/", mode: 0o755, typeflag: tar.TypeDir})
            return nil
        }
        info, err := d.Info()
        if err != nil { return err }
        body, err := os.ReadFile(path)
        if err != nil { return err }
        mode := int64(info.Mode().Perm() & 0o755)
        if mode&0o111 == 0 { mode = 0o644 }
        entries = append(entries, archiveEntry{name: name, mode: mode, typeflag: tar.TypeReg, body: body})
        return nil
    })
    if err != nil { return nil, err }
    sort.SliceStable(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
    return entries, nil
}
~~~

- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestBuildTarGz -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/archive.go internal/submission/archive_test.go && git commit -m "feat(submission): add deterministic tar.gz packaging"`

---

### Task 5: 校验结果与通用元数据/文件安全

**Files:** Create `internal/submission/result.go`、`validate.go`；Test `validate_test.go`

**Interfaces:** Produces `Request`/`Validation`/`Content`/`ErrInvalid`/`Validate(ctx, Request) (Validation, error)`（类型契约已定义）。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/validate_test.go
package submission

import (
    "context"
    "testing"

    "apm/internal/domain"
)

func TestValidateRejectsUnsupportedType(t *testing.T) {
    if _, err := Validate(context.Background(), Request{Type: "agent", Path: t.TempDir()}); err == nil {
        t.Fatal("want error for unsupported type")
    }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestValidateRejectsUnsupportedType -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — `Validate` 按 type 分发到 skill/plugin/mcp（Task 6–8）；通用元数据（名称 `IsCanonicalPowerName`、版本 `vMAJOR.MINOR.PATCH`、description/change_summary 非空）与目录/文件安全（拒 symlink/hardlink/特殊文件/绝对路径/路径逃逸/ELF-PE-MachO-Wasm-共享库-目标文件/嵌套压缩包/超阈值）做成 `validate.go` 内辅助函数供三类复用。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestValidateRejectsUnsupportedType -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/result.go internal/submission/validate.go internal/submission/validate_test.go && git commit -m "feat(submission): add validation entry and common metadata"`

---

### Task 6: Skill 校验

**Files:** Create `internal/submission/skill.go`；Test `skill_test.go`

**Interfaces:** Consumes Task 5；Produces 包内 `validateSkill(ctx, Request) (Validation, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/skill_test.go
package submission

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "apm/internal/domain"
)

func TestValidateSkillRequiresSkillCard(t *testing.T) {
    root := t.TempDir()
    os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\n# Skill"), 0o644)
    _, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: root, Version: "v1.0.0", ChangeSummary: "x"})
    if err == nil { t.Fatal("want error when skill-card.md missing") }
}

func TestValidateSkillSucceeds(t *testing.T) {
    root := writeValidSkill(t)
    got, err := Validate(context.Background(), Request{Type: domain.PowerTypeSkill, Path: root, Version: "v1.0.0", ChangeSummary: "first"})
    if err != nil { t.Fatal(err) }
    if got.Name != "demo" || got.Version != "v1.0.0" { t.Fatalf("unexpected %+v", got) }
}

func writeValidSkill(t *testing.T) string {
    t.Helper()
    root := t.TempDir()
    skill := "---\nname: demo\ndescription: does things\n---\n# Skill\n"
    os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skill), 0o644)
    card := "# Skill Card: Demo\n\n## Capability\nx\n## Use Cases\nx\n## Inputs and Outputs\nx\n## Dependencies\nx\n## Limitations\nx\n## Maintainer\nOwner: team\nContact: team@example.com\n"
    os.WriteFile(filepath.Join(root, "skill-card.md"), []byte(card), 0o644)
    return root
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestValidateSkill -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `validateSkill`：根目录存在 `SKILL.md`/`skill-card.md` 普通文件；`SKILL.md` 首字节起为 YAML frontmatter，解析为 mapping 且有唯一非空字符串 name/description；`skill-card.md` 一级标题 `# Skill Card: <显示名>`、含六个已填章节、Maintainer 含非空 Owner 与 Contact；拒 `REPLACE_OWNER`/`REPLACE_CONTACT`。先跑通用目录/文件安全，再跑 frontmatter/card。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestValidateSkill -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/skill.go internal/submission/skill_test.go && git commit -m "feat(submission): validate skill content"`

---

### Task 7: Plugin 校验

**Files:** Create `internal/submission/plugin.go`；Test `plugin_test.go`

**Interfaces:** Consumes `pluginartifact.LoadAPCPluginManifest`、`pluginartifact.ValidatePhysicalTree`、`domain.PowerRef`、Task 6；Produces 包内 `validatePlugin(ctx, Request) (Validation, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/plugin_test.go
package submission

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "apm/internal/domain"
)

func TestValidatePluginRequiresManifest(t *testing.T) {
    if _, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: t.TempDir(), ChangeSummary: "x"}); err == nil {
        t.Fatal("want error when apc-plugin.json missing")
    }
}

func TestValidatePluginRejectsUndeclaredSkill(t *testing.T) {
    root := writePlugin(t, "{\"schema_version\":1,\"name\":\"p\",\"version\":\"v1.0.0\",\"description\":\"d\",\"skills\":[\"skills/a\"]}")
    os.MkdirAll(filepath.Join(root, "skills", "b"), 0o755)
    if _, err := Validate(context.Background(), Request{Type: domain.PowerTypePlugin, Path: root, ChangeSummary: "x"}); err == nil {
        t.Fatal("want error for undeclared skill dir")
    }
}

func writePlugin(t *testing.T, manifest string) string {
    t.Helper()
    root := t.TempDir()
    os.WriteFile(filepath.Join(root, "apc-plugin.json"), []byte(manifest), 0o644)
    return root
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestValidatePlugin -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `validatePlugin`：先严格读取 `apc-plugin.json` 拿 name/version/description，构造 `domain.PowerRef{Type: plugin, Name, Version}`，再调 `pluginartifact.LoadAPCPluginManifest(ref, root)` 做结构+闭集校验；随后对每个声明内嵌 Skill 复用 Task 6 的完整 Skill 校验。version 来自清单，不读 `Request.Version`。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestValidatePlugin -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/plugin.go internal/submission/plugin_test.go && git commit -m "feat(submission): validate plugin v1 content"`

---

### Task 8: MCP 校验

**Files:** Create `internal/submission/mcp.go`；Test `mcp_test.go`

**Interfaces:** Consumes `domain.DecodeMCPServerJSON`、`CanonicalizeJSON`、Task 5；Produces 包内 `validateMCP(ctx, Request) (Validation, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/mcp_test.go
package submission

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "apm/internal/domain"
)

func TestValidateMCPRejectsHTTPURL(t *testing.T) {
    file := writeMCP(t, "{\"name\":\"m\",\"version\":\"v1.0.0\",\"description\":\"d\",\"change_summary\":\"c\",\"definition\":{\"transport\":\"http\",\"url\":\"http://x/mcp\"}}")
    if _, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: file}); err == nil {
        t.Fatal("want error for plain HTTP URL")
    }
}

func TestValidateMCPSucceeds(t *testing.T) {
    file := writeMCP(t, "{\"name\":\"m\",\"version\":\"v1.0.0\",\"description\":\"d\",\"change_summary\":\"c\",\"definition\":{\"transport\":\"stdio\",\"command\":\"npx\",\"args\":[\"-y\",\"pkg\"]}}")
    got, err := Validate(context.Background(), Request{Type: domain.PowerTypeMCP, Path: file})
    if err != nil { t.Fatal(err) }
    if got.Name != "m" || got.Version != "v1.0.0" { t.Fatalf("unexpected %+v", got) }
}

func writeMCP(t *testing.T, body string) string {
    t.Helper()
    file := filepath.Join(t.TempDir(), "server.json")
    os.WriteFile(file, []byte(body), 0o644)
    return file
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestValidateMCP -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `validateMCP`：`jsonstrict.Decode` 严格读外层（拒未知字段/重复键/尾随值），name/version/description/change_summary 走通用规则；`definition` 用 `domain.DecodeMCPServerJSON`（stdio/http 互斥、仅 HTTPS、禁秘密字段）；对 `definition` 调 `CanonicalizeJSON` 得到确定字节计入 `ContentSize`。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestValidateMCP -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/mcp.go internal/submission/mcp_test.go && git commit -m "feat(submission): validate MCP content"`

---

### Task 9: Prepare 编排（校验 → 打包 → MD5）

**Files:** Create `internal/submission/submission.go`；Test `submission_test.go`

**Interfaces:** Consumes Task 3–8；Produces `Prepare(ctx, Request) (Content, error)`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/submission/submission_test.go
package submission

import (
    "context"
    "testing"

    "apm/internal/domain"
)

func TestPrepareSkillComputesDeterministicMD5(t *testing.T) {
    root := writeValidSkill(t)
    req := Request{Type: domain.PowerTypeSkill, Path: root, Version: "v1.0.0", ChangeSummary: "first"}
    content, err := Prepare(context.Background(), req)
    if err != nil { t.Fatal(err) }
    if content.ContentType != "application/gzip" || len(content.Bytes) == 0 || len(content.MD5) != 32 {
        t.Fatalf("unexpected %+v", content)
    }
    again, _ := Prepare(context.Background(), req)
    if content.MD5 != again.MD5 { t.Fatal("MD5 must be deterministic") }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/submission/ -run TestPrepare -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `Prepare` 先 `Validate` 得身份，再按 type：skill/plugin 调 `BuildTarGz`（ContentType=application/gzip），mcp 调 `CanonicalizeJSON(definition)`（ContentType=application/json）；对字节算 32 位小写十六进制 MD5（`crypto/md5`+`encoding/hex`）填 `Content`。全程内存内。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/submission/ -run TestPrepare -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/submission/submission.go internal/submission/submission_test.go && git commit -m "feat(submission): orchestrate validate, package, and MD5"`

---

### Task 10: Remote 提交客户端

**Files:** Create `internal/cloud/remote/submission.go`；Test `submission_test.go`

**Interfaces:** Consumes `cloud.Submission`、`domain.*`、现有 `c.endpoint`/`c.getJSON`/`safeProblemContext`；Produces `(*Client).Submit/GetSubmission/ListSubmissions` 与私有 DTO。

> 注：Remote Client 需要 API Key。在 `Client` 增加 `apiKey string` 字段与注入方式（Task 14 composition 时从 `APC_API_KEY` 读入并传入构造）。

- [ ] **Step 1: 写失败测试（httptest）**

~~~go
// internal/cloud/remote/submission_test.go
package remote

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "apm/internal/domain"
)

func TestSubmitReturns202Receipt(t *testing.T) {
    var gotAuth, gotIdem string
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        gotAuth, gotIdem = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]string{"submission_id": "sub_1", "status": "received", "status_url": "/api/v1/submissions/sub_1"})
    }))
    defer server.Close()
    client, err := New(server.URL, &http.Client{})
    if err != nil { t.Fatal(err) }
    receipt, err := client.Submit(context.Background(), domain.SubmissionRequest{
        Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0",
        ContentType: "application/gzip", Content: []byte("bytes"), ContentMD5: "md5", IdempotencyKey: "k",
    })
    if err != nil { t.Fatal(err) }
    if receipt.SubmissionID != "sub_1" || receipt.Status != domain.SubmissionReceived { t.Fatalf("bad receipt %+v", receipt) }
    if gotAuth != "Bearer key" { t.Fatalf("auth = %q", gotAuth) }
    if gotIdem != "k" { t.Fatalf("idempotency = %q", gotIdem) }
}

func TestSubmitMaps401(t *testing.T) {
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/problem+json")
        w.WriteHeader(http.StatusUnauthorized)
        w.Write([]byte("{\"status\":401,\"code\":\"INVALID_API_KEY\",\"trace_id\":\"tr_1\"}"))
    }))
    defer server.Close()
    client, _ := New(server.URL, &http.Client{})
    if _, err := client.Submit(context.Background(), domain.SubmissionRequest{Content: []byte("x")}); err == nil {
        t.Fatal("want error for 401")
    }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/cloud/remote/ -run TestSubmit -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — `Submit` 用 `POST /api/v1/submissions`，`mime/multipart` 写两 part（`metadata` JSON + `content` 字节），携带 `Authorization: Bearer <key>`、`Idempotency-Key`、`Accept`；`GetSubmission`/`ListSubmissions` 用 `getJSON` 严格解码为 `domain.Submission`/`domain.SubmissionList`。非 2xx 用 `safeProblemContext` 提 code/trace_id，按状态码映射 `401/403→ErrAuth`、`409→ErrSubmissionConflict`、`503→ErrUnavailable`、`404→ErrNotFound`、其余→`ErrInvalidResponse`。私有 DTO（submissionReceiptDTO 等）不泄漏。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/cloud/remote/ -run TestSubmit -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/cloud/remote/submission.go internal/cloud/remote/submission_test.go && git commit -m "feat(cloud/remote): implement submission client"`

---

### Task 11: Local 空提交实现

**Files:** Create `internal/cloud/local/submission.go`；Test `submission_test.go`

**Interfaces:** Consumes `cloud.Submission`、`cloud.ErrSubmissionUnsupported`；Produces `(*Client)` 三方法（均返回 `ErrSubmissionUnsupported`）。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/cloud/local/submission_test.go
package local

import (
    "context"
    "errors"
    "testing"

    "apm/internal/cloud"
    "apm/internal/domain"
)

func TestLocalSubmissionUnsupported(t *testing.T) {
    client, err := New(t.TempDir())
    if err != nil { t.Fatal(err) }
    if _, err := client.Submit(context.Background(), domain.SubmissionRequest{}); !errors.Is(err, cloud.ErrSubmissionUnsupported) {
        t.Fatalf("err = %v, want ErrSubmissionUnsupported", err)
    }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/cloud/local/ -run TestLocalSubmissionUnsupported -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — 三方法均 `return zero, cloud.ErrSubmissionUnsupported`，注释说明 Local 只做 fixture 只读、不提供提交。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/cloud/local/ -run TestLocalSubmissionUnsupported -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/cloud/local/submission.go internal/cloud/local/submission_test.go && git commit -m "feat(cloud/local): stub submission as unsupported"`

---

### Task 12: Lifecycle 服务

**Files:** Create `internal/lifecycle/validate.go`、`submit.go`、`submission_query.go`；Test `submission_test.go`

**Interfaces:** Consumes `submission.Prepare/Validate`、`cloud.Submission`、`cli` contract（Task 13）；Produces `lifecycle.NewValidate`/`NewSubmit`/`NewSubmissionQuery`。

- [ ] **Step 1: 写失败测试**

~~~go
// internal/lifecycle/submission_test.go
package lifecycle

import (
    "context"
    "testing"

    "apm/internal/cli"
    "apm/internal/cloud"
    "apm/internal/domain"
)

type fakeSubmission struct{ cloud.Client }

func (fakeSubmission) Submit(context.Context, domain.SubmissionRequest) (domain.SubmissionReceipt, error) {
    return domain.SubmissionReceipt{SubmissionID: "sub_1", Status: domain.SubmissionReceived}, nil
}
func (fakeSubmission) GetSubmission(context.Context, string) (domain.Submission, error) { return domain.Submission{}, nil }
func (fakeSubmission) ListSubmissions(context.Context, domain.SubmissionListQuery) (domain.SubmissionList, error) {
    return domain.SubmissionList{}, nil
}

func TestNewSubmitRejectsNilCloud(t *testing.T) {
    if _, err := NewSubmit(nil); err == nil { t.Fatal("want error for nil cloud") }
}

func TestSubmitReturnsReceipt(t *testing.T) {
    svc, err := NewSubmit(fakeSubmission{})
    if err != nil { t.Fatal(err) }
    result, err := svc.Submit(context.Background(), cli.Flags{Type: "skill", Version: "v1.0.0"}, []string{"testdata"})
    if err != nil { t.Fatal(err) }
    if result.SubmissionID != "sub_1" { t.Fatalf("bad result %+v", result) }
}
~~~

> 注：`svc.Submit` 需要真实源目录；用 `internal/submission` 的测试 helper 或临时目录造最小 Skill。测试里 `testdata` 需替换为真实临时目录。

- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/lifecycle/ -run TestNewSubmitRejectsNilCloud -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `ValidateService` 调 `submission.Validate`；`SubmitService` 调 `submission.Prepare` 后组装 `domain.SubmissionRequest`（含 UUID IdempotencyKey）并调 `Submit`；`SubmissionQueryService` 调 `GetSubmission`/`ListSubmissions`。均校验 nil 依赖并返回 `cli` 结果类型。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/lifecycle/ -run TestNewSubmitRejectsNilCloud -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/lifecycle/validate.go internal/lifecycle/submit.go internal/lifecycle/submission_query.go internal/lifecycle/submission_test.go && git commit -m "feat(lifecycle): add validate, submit, submission query"`

---

### Task 13: CLI 命令与 contract

**Files:** Create `internal/cli/validate_contract.go`、`validate.go`、`submit_contract.go`、`submit.go`、`submission_contract.go`、`submission.go`；Modify `root.go`；Test 三个 `*_test.go`

**Interfaces:** Consumes Task 12、现有 `Flags`/`bindTypeFlag`/`logParsedCommand`；Produces 三个 Lifecycle 接口 + factory + result 类型。

~~~go
// validate_contract.go
type ValidateResult struct {
    Type, Name, Version, Description string
    FileCount int
    ContentSize int64
    Warnings []string
}
type ValidateLifecycle interface { Validate(context.Context, Flags, []string) (ValidateResult, error) }
type ValidateLifecycleFactory func() (ValidateLifecycle, error)

// submit_contract.go
type SubmitResult struct { SubmissionID string; Status string }
type SubmitLifecycle interface { Submit(context.Context, Flags, []string) (SubmitResult, error) }
type SubmitLifecycleFactory func() (SubmitLifecycle, error)

// submission_contract.go
type SubmissionQueryResult struct { Summaries []domain.SubmissionSummary; Detail *domain.Submission }
type SubmissionQueryLifecycle interface {
    Get(context.Context, string) (SubmissionQueryResult, error)
    List(context.Context, Flags) (SubmissionQueryResult, error)
}
type SubmissionQueryLifecycleFactory func() (SubmissionQueryLifecycle, error)
~~~

- [ ] **Step 1: 写失败测试** — 仿照 `search_test.go` 用 recording lifecycle 断言三个命令的解析、渲染与 error 路径（含 `--json` 机器输出）。
- [ ] **Step 2: 运行确认失败** — Run: `go test ./internal/cli/ -run 'TestValidate|TestSubmit|TestSubmission' -count=1`，Expected: 编译失败。
- [ ] **Step 3: 实现** — 按 `search.go` 模式实现三命令：`validate` 绑 `--type`/`--version`/`--json`；`submit` 绑 `--type`/`--version`/`--change-summary`；`submission` 为子命令组（get/list）。`newRootCommand` 增三个 factory 参数并 `AddCommand`。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./internal/cli/ -run 'TestValidate|TestSubmit|TestSubmission' -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add internal/cli/ && git commit -m "feat(cli): add validate, submit, submission commands"`

---

### Task 14: Composition 接线与 API Key

**Files:** Modify `cmd/apm/main.go`、`cmd/apm/cloud.go`；Test `cloud_test.go` 增补

**Interfaces:** Produces `newValidateLifecycle`/`newSubmitLifecycle`/`newSubmissionQueryLifecycle`，以及从 `APC_API_KEY` 注入 Key。

- [ ] **Step 1: 写失败测试**

~~~go
// cmd/apm/cloud_test.go（增补）
func TestNewSubmitLifecycleRequiresAPIKey(t *testing.T) {
    t.Setenv("APC_API_KEY", "")
    if _, err := newSubmitLifecycle(); err == nil { t.Fatal("want error when API key missing") }
}
~~~

- [ ] **Step 2: 运行确认失败** — Run: `go test ./cmd/apm/ -run TestNewSubmitLifecycleRequiresAPIKey -count=1`，Expected: 失败。
- [ ] **Step 3: 实现** — `newValidateLifecycle` 无 Cloud 依赖；`newSubmitLifecycle`/`newSubmissionQueryLifecycle` 复用 `newCloudClient()`，从 `os.Getenv("APC_API_KEY")` 读 Key（缺省报错、日志不打印完整 Key）并注入 remote Submission Client；`main.go` 把三个 factory 传入 `cli.New`。
- [ ] **Step 4: 运行确认通过** — Run: `go test ./cmd/apm/ -count=1`，Expected: PASS。
- [ ] **Step 5: 提交** — `git add cmd/apm/main.go cmd/apm/cloud.go cmd/apm/cloud_test.go && git commit -m "feat(cmd): wire validate, submit, submission with API key"`

---

### Task 15: 文档与 ADR

**Files:** Create `ivory-tower/adr/ADR-018-submission-and-local-validation.md`；Modify `ivory-tower/project-overview.md`

- [ ] **Step 1: 写 ADR** — 记录提交客户端端口、本地校验/确定性打包/JCS 决策、local 空实现与 APC 最低验收收敛。
- [ ] **Step 2: 更新 overview** — 「当前状态」与「Scope」补 validate/submit/submission 能力与 `APC_API_KEY` 配置。
- [ ] **Step 3: 运行检查** — Run: `go build ./... && go test ./... -count=1`，Expected: 全绿。
- [ ] **Step 4: 提交** — `git add ivory-tower/adr/ADR-018-submission-and-local-validation.md ivory-tower/project-overview.md && git commit -m "docs(adr): record submission and local validation decisions"`

---

## 自检

- Spec 覆盖：命令设计→Task 13；完整校验→Task 5–8；确定性打包→Task 4；JCS→Task 3；cloud.Submission→Task 2/10/11；领域类型→Task 1；配置→Task 14；错误模型→Task 2/10；中断语义→全局约束；验收场景→各 Task 测试；APC 设计变更→已在 spec + APC 文档完成（本计划不再改 APC）。
- 占位符：无 TBD/TODO；Task 3 JCS 数字/代理对细节以 RFC 8785 官方向量为准，已注明。
- 类型一致：`SubmissionRequest/Receipt/Submission/ListQuery/List` 字段各任务一致；`Prepare/Validate/BuildTarGz/CanonicalizeJSON` 签名一致；CLI contract 名与 lifecycle 方法一致。

package lifecycle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"apm/internal/cli"
	"apm/internal/domain"
)

func TestNewSubmitRejectsNilCloud(t *testing.T) {
	if _, err := NewSubmit(nil); err == nil {
		t.Fatal("NewSubmit(nil) error = nil, want error")
	}
	if _, err := NewSubmit((*recordingSubmissionClient)(nil)); err == nil {
		t.Fatal("NewSubmit(typed nil) error = nil, want error")
	}
}

func TestSubmitPreparesAndUploads(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{SubmissionID: "sub_1", Status: domain.SubmissionReceived},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	result, err := service.Submit(context.Background(), cli.Flags{Type: "skill", Version: "v9.9.9", Message: "  first release  "}, []string{dir})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.SubmissionID != "sub_1" || result.Status != string(domain.SubmissionReceived) {
		t.Fatalf("Submit() = %#v", result)
	}
	if len(client.submitCalls) != 1 {
		t.Fatalf("cloud Submit calls = %d, want 1", len(client.submitCalls))
	}
	request := client.submitCalls[0]
	if request.Type != domain.PowerTypeSkill || request.Name != "demo" || request.Version != "v1.0.0" || request.Message != "first release" {
		t.Fatalf("SubmissionRequest = %#v", request)
	}
	// metadata 的 schema_version 与展示字段必须来自资源文件，与 content 对齐（ADR-041）。
	if request.SchemaVersion != domain.PowerSchemaVersionV2 ||
		request.NameZH != "演示" || request.NameEN != "Demo" || request.DescriptionZH != "演示描述" {
		t.Fatalf("SubmissionRequest metadata alignment = %#v, want v2 plus SKILL.md display fields", request)
	}
	if request.ContentType == "" || request.ContentMD5 == "" || request.IdempotencyKey == "" || len(request.Content) == 0 {
		t.Fatalf("SubmissionRequest missing content fields: %#v", request)
	}
}

func TestSubmitMCPKeepsDefinitionInContent(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{SubmissionID: "sub_1", Status: domain.SubmissionReceived},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeVersionedLifecycleMCP(t, t.TempDir())

	result, err := service.Submit(context.Background(), cli.Flags{Type: "mcp", Message: "first"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.SubmissionID != "sub_1" || result.Status != string(domain.SubmissionReceived) {
		t.Fatalf("Submit() = %#v", result)
	}
	if len(client.submitCalls) != 1 {
		t.Fatalf("cloud Submit calls = %d, want 1", len(client.submitCalls))
	}
	request := client.submitCalls[0]
	if request.Type != domain.PowerTypeMCP || request.ContentType != "application/json" || len(request.Content) == 0 {
		t.Fatalf("SubmissionRequest = %#v", request)
	}
	// 定义只在 content 里；请求结构不再有 Definition 字段，服务端从 content 读取（ADR-043）。
	wantContent := `{"definition":{"transport":"http","url":"https://mcp.example.com/mcp"}`
	if !bytes.Contains(request.Content, []byte(wantContent)) {
		t.Fatalf("SubmissionRequest content = %s, want it to carry the MCP definition", request.Content)
	}
	if request.SchemaVersion != domain.PowerSchemaVersionV2 ||
		request.NameZH != "演示" || request.NameEN != "Demo" || request.DescriptionZH != "演示 MCP" {
		t.Fatalf("MCP SubmissionRequest metadata alignment = %#v, want v2 plus MCP.json display fields", request)
	}
}

func writeVersionedLifecycleMCP(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "mcp.json")
	contents := `{"schema_version":2,"name":"demo","name_zh":"演示","name_en":"Demo","version":"v1.0.0","description":"demo mcp","description_zh":"演示 MCP","definition":{"transport":"http","url":"https://mcp.example.com/mcp"}}`
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSubmitForwardsCloudError(t *testing.T) {
	uploadErr := errors.New("upload failed")
	client := &recordingSubmissionClient{submitErr: uploadErr}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	_, err = service.Submit(context.Background(), cli.Flags{Type: "skill", Message: "x"}, []string{dir})
	if !errors.Is(err, uploadErr) {
		t.Fatalf("Submit() error = %v, want %v", err, uploadErr)
	}
}

func TestSubmitRejectsMissingMessageBeforeUpload(t *testing.T) {
	client := &recordingSubmissionClient{}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeVersionedLifecycleSkill(t, dir)

	_, err = service.Submit(context.Background(), cli.Flags{Type: "skill"}, []string{dir})
	if err == nil {
		t.Fatal("Submit() error = nil, want error for missing message")
	}
	if len(client.submitCalls) != 0 {
		t.Fatalf("cloud Submit calls = %d, want 0 (validation precedes upload)", len(client.submitCalls))
	}
}

func writeVersionedLifecycleSkill(t *testing.T, dir string) {
	t.Helper()
	writeLifecycleSkill(t, dir)
	contents := "---\nschema_version: 2\nname: demo\nname_zh: 演示\nname_en: Demo\ndescription: does things\ndescription_zh: 演示描述\nversion: \"v1.0.0\"\n---\n# Demo Skill\n\nDetails here.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertLifecycleAgentArchive 校验目录型 Agent 的确定性 gzip 归档内容：
// 恰好一个普通文件条目 agent.yaml，正文与源 fixture 完全一致。
func assertLifecycleAgentArchive(t *testing.T, content []byte) {
	t.Helper()
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("agent submission is not a gzip archive: %v", err)
	}
	tarReader := tar.NewReader(gzipReader)
	files := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read agent submission tar: %v", err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		files++
		if files != 1 || header.Name != "agent.yaml" {
			t.Fatalf("agent submission tar entry %d = %q, want exactly agent.yaml", files, header.Name)
		}
		body, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("read agent.yaml from submission tar: %v", err)
		}
		if !bytes.Equal(body, []byte(lifecycleAgentYAML)) {
			t.Fatalf("agent.yaml in submission = %q, want %q", body, lifecycleAgentYAML)
		}
	}
	if files != 1 {
		t.Fatalf("agent submission tar file entries = %d, want 1", files)
	}
}

func TestSubmitAgentMapsAcceptedOutcome(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{Outcome: domain.SubmissionAccepted, SubmissionID: "sub_1", Status: domain.SubmissionReceived},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	result, err := service.Submit(context.Background(), cli.Flags{Type: "agent", Message: "first release"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Outcome != string(domain.SubmissionAccepted) || result.SubmissionID != "sub_1" || result.Status != string(domain.SubmissionReceived) {
		t.Fatalf("Submit() = %#v", result)
	}
	if len(client.submitCalls) != 1 {
		t.Fatalf("cloud Submit calls = %d, want 1", len(client.submitCalls))
	}
	request := client.submitCalls[0]
	if request.Type != domain.PowerTypeAgent || request.Name != "demo-agent" || request.Version != "v1.0.0" || request.Message != "first release" || request.ContentType != "application/gzip" {
		t.Fatalf("SubmissionRequest = %#v", request)
	}
	if request.ContentMD5 == "" || request.IdempotencyKey == "" {
		t.Fatalf("SubmissionRequest missing content fields: %#v", request)
	}
	assertLifecycleAgentArchive(t, request.Content)
}

func TestSubmitAgentMapsReplayedOutcome(t *testing.T) {
	client := &recordingSubmissionClient{
		receipt: domain.SubmissionResult{Outcome: domain.SubmissionReplayed, SubmissionID: "sub_2", Status: domain.SubmissionPublished},
	}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	result, err := service.Submit(context.Background(), cli.Flags{Type: "agent", Message: "first release"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Outcome != string(domain.SubmissionReplayed) || result.SubmissionID != "sub_2" || result.Status != string(domain.SubmissionPublished) {
		t.Fatalf("Submit() = %#v", result)
	}
}

func TestSubmitAgentIgnoresVersionOverrideAtLifecycleBoundary(t *testing.T) {
	client := &recordingSubmissionClient{}
	service, err := NewSubmit(client)
	if err != nil {
		t.Fatal(err)
	}
	path := writeLifecycleAgent(t, t.TempDir())

	_, err = service.Submit(context.Background(), cli.Flags{Type: "agent", Version: "v9.9.9", Message: "x"}, []string{path})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if len(client.submitCalls) != 1 || client.submitCalls[0].Version != "v1.0.0" {
		t.Fatalf("cloud Submit calls = %#v, want source-owned version v1.0.0", client.submitCalls)
	}
}

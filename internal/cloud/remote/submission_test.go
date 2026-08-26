package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func newTestSubmissionClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	return newTestSubmissionClientWithKey(t, server, "key")
}

func newTestSubmissionClientWithKey(t *testing.T, server *httptest.Server, apiKey string) *Client {
	t.Helper()
	client, err := NewWithAPIKey(server.URL, &http.Client{Timeout: 5 * time.Second}, apiKey)
	if err != nil {
		t.Fatalf("NewWithAPIKey() error = %v", err)
	}
	return client
}

// readSubmissionParts 读取 multipart 请求，返回 metadata JSON map、content part 的
// Content-Type、Content-Disposition filename 与原始字节。
func readSubmissionParts(t *testing.T, r *http.Request) (map[string]any, string, string, []byte) {
	t.Helper()
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, err = %v", r.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	var metadata map[string]any
	var contentType string
	var filename string
	var content []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart() error = %v", err)
		}
		body, _ := io.ReadAll(part)
		switch part.FormName() {
		case "metadata":
			if err := json.Unmarshal(body, &metadata); err != nil {
				t.Fatalf("metadata unmarshal: %v", err)
			}
		case "content":
			contentType = part.Header.Get("Content-Type")
			filename = part.FileName()
			content = body
		}
	}
	return metadata, contentType, filename, content
}

func TestSubmitAgentUsesGzipPart(t *testing.T) {
	raw := []byte("schema_version: 1\nagent: {id: demo, name: demo, version: v1.0.0, description: demo, soul: help}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata, contentType, filename, content := readSubmissionParts(t, r)
		if metadata["type"] != "agent" || metadata["name"] != "demo" || metadata["version"] != "v1.0.0" ||
			metadata["description"] != "demo" || metadata["message"] != "first" || metadata["content_md5"] != md5Hex(raw) {
			t.Fatalf("metadata = %#v", metadata)
		}
		if _, legacy := metadata["change_summary"]; legacy {
			t.Fatalf("metadata contains legacy change_summary: %#v", metadata)
		}
		if contentType != "application/gzip" || filename != "demo-v1.0.0.tar.gz" || !bytes.Equal(content, raw) {
			t.Fatalf("content type/filename/bytes = %q/%q/%q", contentType, filename, content)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/api/v1/submissions/sub_1"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0", Description: "demo",
		Message: "first", ContentType: "application/gzip", Content: raw,
		ContentMD5: md5Hex(raw), IdempotencyKey: "7d7e6d70-13ec-4cd5-9ad6-37e49e861ac2",
	})
	if err != nil || got.Outcome != domain.SubmissionAccepted {
		t.Fatalf("Submit() = %+v, %v", got, err)
	}
	if got.SubmissionID != "sub_1" || got.Status != domain.SubmissionReceived || got.StatusURL != "/api/v1/submissions/sub_1" {
		t.Fatalf("Submit() = %+v", got)
	}
}

func TestSubmitReturnsAcceptedResult(t *testing.T) {
	var gotAuth, gotIdem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		metadata, contentType, _, content := readSubmissionParts(t, r)
		if metadata["type"] != "skill" || metadata["name"] != "demo" || metadata["version"] != "v1.0.0" ||
			metadata["description"] != "does things" || metadata["message"] != "first" || metadata["content_md5"] != "md5" {
			t.Fatalf("metadata = %#v", metadata)
		}
		if _, legacy := metadata["change_summary"]; legacy {
			t.Fatalf("metadata contains legacy change_summary: %#v", metadata)
		}
		if contentType != "application/gzip" || string(content) != "bytes" {
			t.Fatalf("content type/bytes = %q/%q", contentType, content)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/api/v1/submissions/sub_1"}`))
	}))
	defer server.Close()

	client := newTestSubmissionClient(t, server)
	got, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0",
		Description: "does things", Message: "  first  ",
		ContentType: "application/gzip", Content: []byte("bytes"), ContentMD5: "md5", IdempotencyKey: "k",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Outcome != domain.SubmissionAccepted || got.SubmissionID != "sub_1" ||
		got.Status != domain.SubmissionReceived || got.StatusURL == "" {
		t.Fatalf("Submit() = %+v", got)
	}
	if gotAuth != "Bearer key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotIdem != "k" {
		t.Fatalf("Idempotency-Key = %q", gotIdem)
	}
}

func TestSubmitRejectsMissingOrBlankMessageBeforeRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)

	for _, message := range []string{"", " \t\n"} {
		_, err := client.Submit(context.Background(), domain.SubmissionRequest{
			Type: domain.PowerTypeSkill, Message: message, ContentType: "application/gzip", Content: []byte("x"),
		})
		if err == nil || !strings.Contains(err.Error(), "message is required") {
			t.Fatalf("Submit(message=%q) error = %v, want required-message error", message, err)
		}
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0", requests)
	}
}

func TestSubmitReturnsSameContentReplayedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validSubmissionDetailJSON))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Description: "does things",
		Message: "first release", ContentType: "application/gzip", Content: []byte("x"),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Outcome != domain.SubmissionReplayed || got.SubmissionID != "sub_1" ||
		got.Status != domain.SubmissionReviewPending || got.StatusURL != "" {
		t.Fatalf("Submit() = %+v", got)
	}
}

func TestSubmitAcceptsUnknownResponseFields(t *testing.T) {
	t.Run("202 receipt with extra field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s","future_field":true}`))
		}))
		defer server.Close()
		client := newTestSubmissionClient(t, server)
		got, err := client.Submit(context.Background(), domain.SubmissionRequest{
			Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
		})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		if got.Outcome != domain.SubmissionAccepted {
			t.Fatalf("Submit() = %+v, want accepted", got)
		}
	})
	t.Run("200 submission with extra field", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			body := strings.TrimSuffix(validSubmissionDetailJSON, "}") + `,"future_field":true}`
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		client := newTestSubmissionClient(t, server)
		got, err := client.Submit(context.Background(), domain.SubmissionRequest{
			Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Description: "does things",
			Message: "first release", ContentType: "application/gzip", Content: []byte("x"),
		})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		if got.Outcome != domain.SubmissionReplayed {
			t.Fatalf("Submit() = %+v, want replayed", got)
		}
	})
}

func TestSubmitRejectsOther2xxStatus(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusNoContent, http.StatusPartialContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s"}`))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
			})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

// TestSubmitRejectsShapeMismatch 验证客户端严格按 HTTP 状态分流，不按字段形状猜测语义：
// 202 必须携带 receipt 的 status_url，Submission 形状（缺少 status_url）被拒绝。
// 200 必须是完整 Submission 详情，其必填字段和 timeline 负例由 TestSubmitRejectsInvalidReplayDetail 覆盖。
func TestSubmitRejectsShapeMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","content_available":true}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
	}
}

func TestSubmitRejectsMalformedAcceptedResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "wrong content type", contentType: "text/plain", body: `{"submission_id":"sub_1","status":"received","status_url":"/s"}`},
		{name: "duplicate key", contentType: "application/json", body: `{"submission_id":"sub_1","submission_id":"sub_2","status":"received","status_url":"/s"}`},
		{name: "unknown status", contentType: "application/json", body: `{"submission_id":"sub_1","status":"weird","status_url":"/s"}`},
		{name: "non-received status", contentType: "application/json", body: `{"submission_id":"sub_1","status":"validating","status_url":"/s"}`},
		{name: "empty id", contentType: "application/json", body: `{"submission_id":"","status":"received","status_url":"/s"}`},
		{name: "empty status url", contentType: "application/json", body: `{"submission_id":"sub_1","status":"received","status_url":""}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
			})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestSubmitRejectsContentTypeMismatchBeforeRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	tests := []struct {
		name    string
		request domain.SubmissionRequest
	}{
		{name: "unknown type", request: domain.SubmissionRequest{Type: domain.PowerType("workflow"), Message: "fixture", ContentType: "application/gzip", Content: []byte("x")}},
		{name: "skill with yaml", request: domain.SubmissionRequest{Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/yaml", Content: []byte("x")}},
		{name: "plugin with json", request: domain.SubmissionRequest{Type: domain.PowerTypePlugin, Message: "fixture", ContentType: "application/json", Content: []byte("x")}},
		{name: "mcp with gzip", request: domain.SubmissionRequest{Type: domain.PowerTypeMCP, Message: "fixture", ContentType: "application/gzip", Content: []byte("x")}},
		{name: "agent with yaml", request: domain.SubmissionRequest{Type: domain.PowerTypeAgent, Message: "fixture", ContentType: "application/yaml", Content: []byte("x")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := client.Submit(context.Background(), tt.request); err == nil {
				t.Fatalf("Submit() error = nil, want content type validation error")
			}
		})
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0 (validation precedes request)", requests)
	}
}

func TestSubmitAcceptsValidTypeContentTypeMappings(t *testing.T) {
	valid := []struct {
		name        string
		requestType domain.PowerType
		contentType string
	}{
		{"skill", domain.PowerTypeSkill, "application/gzip"},
		{"plugin", domain.PowerTypePlugin, "application/gzip"},
		{"mcp", domain.PowerTypeMCP, "application/json"},
		{"agent", domain.PowerTypeAgent, "application/gzip"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			var gotContentType string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, contentType, _, _ := readSubmissionParts(t, r)
				gotContentType = contentType
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s"}`))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: tt.requestType, Message: "fixture", ContentType: tt.contentType, Content: []byte("x"),
				ContentMD5: "x", IdempotencyKey: "k",
			})
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if gotContentType != tt.contentType {
				t.Fatalf("content part type = %q, want %q", gotContentType, tt.contentType)
			}
		})
	}
}

func TestSubmissionFilename(t *testing.T) {
	tests := []struct {
		name    string
		request domain.SubmissionRequest
		want    string
	}{
		{name: "skill", request: domain.SubmissionRequest{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"}, want: "demo-v1.0.0.tar.gz"},
		{name: "plugin", request: domain.SubmissionRequest{Type: domain.PowerTypePlugin, Name: "demo", Version: "v1.0.0"}, want: "demo-v1.0.0.tar.gz"},
		{name: "agent", request: domain.SubmissionRequest{Type: domain.PowerTypeAgent, Name: "demo-agent", Version: "v1.2.3"}, want: "demo-agent-v1.2.3.tar.gz"},
		{name: "mcp", request: domain.SubmissionRequest{Type: domain.PowerTypeMCP, Name: "demo", Version: "v1.0.0"}, want: "demo-v1.0.0.json"},
		{name: "unknown type", request: domain.SubmissionRequest{Type: domain.PowerType("workflow"), Name: "demo", Version: "v1.0.0"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := submissionFilename(tt.request); got != tt.want {
				t.Fatalf("submissionFilename(%+v) = %q, want %q", tt.request, got, tt.want)
			}
		})
	}
}

func TestSubmitWritesMultipartContentFilenames(t *testing.T) {
	tests := []struct {
		name        string
		requestType domain.PowerType
		contentType string
		wantFile    string
	}{
		{"skill", domain.PowerTypeSkill, "application/gzip", "demo-v1.0.0.tar.gz"},
		{"plugin", domain.PowerTypePlugin, "application/gzip", "demo-v1.0.0.tar.gz"},
		{"agent", domain.PowerTypeAgent, "application/gzip", "demo-v1.0.0.tar.gz"},
		{"mcp", domain.PowerTypeMCP, "application/json", "demo-v1.0.0.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFilename string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _, filename, _ := readSubmissionParts(t, r)
				gotFilename = filename
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s"}`))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: tt.requestType, Name: "demo", Version: "v1.0.0", Message: "fixture",
				ContentType: tt.contentType, Content: []byte("x"), ContentMD5: "x", IdempotencyKey: "k",
			})
			if err != nil {
				t.Fatalf("Submit() error = %v", err)
			}
			if gotFilename != tt.wantFile {
				t.Fatalf("content filename = %q, want %q", gotFilename, tt.wantFile)
			}
		})
	}
}

func TestSubmitMultipartFilenameEscaping(t *testing.T) {
	weirdName := `demo"quote\back`
	var gotFilename string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, filename, _ := readSubmissionParts(t, r)
		gotFilename = filename
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Name: weirdName, Version: "v1.0.0", Message: "fixture",
		ContentType: "application/gzip", Content: []byte("x"), ContentMD5: "x", IdempotencyKey: "k",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if want := weirdName + "-v1.0.0.tar.gz"; gotFilename != want {
		t.Fatalf("content filename = %q, want %q", gotFilename, want)
	}
}

func TestSubmitRequiresAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	client, err := New(server.URL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Submit(context.Background(), domain.SubmissionRequest{Content: []byte("x"), ContentType: "application/gzip"}); !errors.Is(err, cloud.ErrAuth) {
		t.Fatalf("Submit() error = %v, want ErrAuth", err)
	}
}

func TestSubmitMaps401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"code":"INVALID_API_KEY","trace_id":"tr_1"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrAuth) {
		t.Fatalf("Submit() error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "INVALID_API_KEY") {
		t.Fatalf("Submit() error = %v, want problem code", err)
	}
}

func TestSubmitMaps409(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":409,"code":"RESOURCE_VERSION_CONFLICT","trace_id":"tr_2"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("Submit() error = %v, want ErrSubmissionConflict", err)
	}
	if errors.Is(err, cloud.ErrAlreadyPublished) {
		t.Fatalf("Submit() error = %v, must not match ErrAlreadyPublished", err)
	}
}

func TestSubmitMaps409AlreadyPublished(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":409,"code":"ALREADY_PUBLISHED","trace_id":"tr_already"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrAlreadyPublished) || !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("Submit() error = %v, want both ErrAlreadyPublished and ErrSubmissionConflict", err)
	}
	if !strings.Contains(err.Error(), "ALREADY_PUBLISHED") || !strings.Contains(err.Error(), "tr_already") {
		t.Fatalf("Submit() error = %v, want problem code/trace_id context", err)
	}
}

func TestSubmitMaps409TooManyActiveSubmissions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":409,"code":"TOO_MANY_ACTIVE_SUBMISSIONS","trace_id":"tr_quota"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrTooManyActiveSubmissions) || !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("Submit() error = %v, want both ErrTooManyActiveSubmissions and ErrSubmissionConflict", err)
	}
	if errors.Is(err, cloud.ErrAlreadyPublished) {
		t.Fatalf("Submit() error = %v, must not match ErrAlreadyPublished", err)
	}
	if !strings.Contains(err.Error(), "TOO_MANY_ACTIVE_SUBMISSIONS") || !strings.Contains(err.Error(), "tr_quota") {
		t.Fatalf("Submit() error = %v, want problem code/trace_id context", err)
	}
}

func TestSubmitMaps503(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":503,"code":"AUTH_DOWN","trace_id":"tr_3"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Message: "fixture", ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrUnavailable) {
		t.Fatalf("Submit() error = %v, want ErrUnavailable", err)
	}
}

const validSubmissionDetailJSON = `{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","description":"does things","message":"first release","status":"review_pending","created_at":"2026-08-24T10:00:00Z","updated_at":"2026-08-24T10:02:00Z","content_available":true,"validation_errors":["missing card"],"review_comment":"","published_version":"","timeline":[{"event_id":"evt_1","event_type":"submission_received","status_from":"","status_to":"received","timestamp":"2026-08-24T10:00:00Z","message":"submission received"},{"event_id":"evt_2","event_type":"review_requested","status_from":"received","status_to":"review_pending","timestamp":"2026-08-24T10:02:00Z","message":"review requested"}]}`

func TestSubmitRejectsInvalidReplayDetail(t *testing.T) {
	timeline := `,"timeline":[{"event_id":"evt_1","event_type":"submission_received","status_from":"","status_to":"received","timestamp":"2026-08-24T10:00:00Z","message":"submission received"},{"event_id":"evt_2","event_type":"review_requested","status_from":"received","status_to":"review_pending","timestamp":"2026-08-24T10:02:00Z","message":"review requested"}]`
	tests := []struct {
		name string
		body string
	}{
		{name: "missing description", body: strings.Replace(validSubmissionDetailJSON, `"description":"does things",`, "", 1)},
		{name: "missing message", body: strings.Replace(validSubmissionDetailJSON, `"message":"first release",`, "", 1)},
		{name: "missing created at", body: strings.Replace(validSubmissionDetailJSON, `"created_at":"2026-08-24T10:00:00Z",`, "", 1)},
		{name: "missing updated at", body: strings.Replace(validSubmissionDetailJSON, `"updated_at":"2026-08-24T10:02:00Z",`, "", 1)},
		{name: "missing timeline", body: strings.Replace(validSubmissionDetailJSON, timeline, "", 1)},
		{name: "missing validation errors", body: strings.Replace(validSubmissionDetailJSON, `"validation_errors":["missing card"],`, "", 1)},
		{name: "decreasing timeline", body: strings.Replace(validSubmissionDetailJSON, `"timestamp":"2026-08-24T10:02:00Z"`, `"timestamp":"2026-08-24T09:59:00Z"`, 1)},
		{name: "type drift", body: strings.Replace(validSubmissionDetailJSON, `"type":"skill"`, `"type":"mcp"`, 1)},
		{name: "name drift", body: strings.Replace(validSubmissionDetailJSON, `"name":"demo"`, `"name":"shadow"`, 1)},
		{name: "version drift", body: strings.Replace(validSubmissionDetailJSON, `"version":"v1.0.0"`, `"version":"v1.0.1"`, 1)},
		{name: "description drift", body: strings.Replace(validSubmissionDetailJSON, `"description":"does things"`, `"description":"changed"`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0", Description: "does things",
				Message: "first release", ContentType: "application/gzip", Content: []byte("x"),
			})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestGetSubmissionUsesBearerAndReturnsFullDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v1/submissions/sub_1" {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer official-key" {
			t.Fatalf("Authorization = %q, want Bearer official-key", authorization)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Fatalf("Accept = %q, want application/json", accept)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validSubmissionDetailJSON))
	}))
	defer server.Close()
	client := newTestSubmissionClientWithKey(t, server, "official-key")
	got, err := client.GetSubmission(context.Background(), "sub_1")
	if err != nil {
		t.Fatalf("GetSubmission() error = %v", err)
	}
	if got.SubmissionID != "sub_1" || got.Type != domain.PowerTypeSkill || got.Name != "demo" || got.Version != "v1.0.0" ||
		got.Description != "does things" || got.Message != "first release" || got.Status != domain.SubmissionReviewPending ||
		got.CreatedAt != "2026-08-24T10:00:00Z" || got.UpdatedAt != "2026-08-24T10:02:00Z" || !got.ContentAvailable ||
		got.ReviewComment != "" || got.PublishedVersion != "" {
		t.Fatalf("GetSubmission() = %+v", got)
	}
	if len(got.ValidationErrors) != 1 || got.ValidationErrors[0] != "missing card" {
		t.Fatalf("ValidationErrors = %#v", got.ValidationErrors)
	}
	if len(got.Timeline) != 2 || got.Timeline[0] != (domain.SubmissionEvent{
		EventID: "evt_1", EventType: "submission_received", StatusTo: domain.SubmissionReceived,
		Timestamp: "2026-08-24T10:00:00Z", Message: "submission received",
	}) || got.Timeline[1].StatusFrom != domain.SubmissionReceived || got.Timeline[1].StatusTo != domain.SubmissionReviewPending {
		t.Fatalf("Timeline = %#v", got.Timeline)
	}
}

func TestListSubmissionsUsesBearerAndBoundedFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api/v1/submissions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer official-key" {
			t.Fatalf("Authorization = %q, want Bearer official-key", authorization)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Fatalf("Accept = %q, want application/json", accept)
		}
		if r.URL.RawQuery != "name=demo&status=review_pending&type=skill" {
			t.Fatalf("RawQuery = %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"review_pending","created_at":"2026-08-24T10:00:00Z"}]}`))
	}))
	defer server.Close()
	client := newTestSubmissionClientWithKey(t, server, "official-key")
	got, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{
		Status: domain.SubmissionReviewPending,
		Type:   domain.PowerTypeSkill,
		Name:   "demo",
	})
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("ListSubmissions() = %+v, %v", got, err)
	}
	if got.Items[0] != (domain.SubmissionSummary{
		SubmissionID: "sub_1", Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0",
		Status: domain.SubmissionReviewPending, CreatedAt: "2026-08-24T10:00:00Z",
	}) {
		t.Fatalf("ListSubmissions() item = %+v", got.Items[0])
	}
}

func TestSubmissionQueryRequiresAPIKeyBeforeTransport(t *testing.T) {
	requests := 0
	httpClient := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("transport must not be called")
		}),
	}
	client, err := New("https://apc.example.com", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetSubmission(context.Background(), "sub_1"); !errors.Is(err, cloud.ErrAuth) {
		t.Fatalf("GetSubmission() error = %v, want ErrAuth", err)
	}
	if _, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{}); !errors.Is(err, cloud.ErrAuth) {
		t.Fatalf("ListSubmissions() error = %v, want ErrAuth", err)
	}
	if requests != 0 {
		t.Fatalf("transport requests = %d, want 0", requests)
	}
}

func TestSubmissionQueryPreservesCancellationIdentity(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
		want      error
	}{
		{
			name: "transport cancellation",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			},
			want: context.Canceled,
		},
		{
			name: "response read deadline",
			transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       submissionErrorBody{err: context.DeadlineExceeded},
				}, nil
			},
			want: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewWithAPIKey("https://apc.example.com", &http.Client{Timeout: time.Second, Transport: tt.transport}, "key")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.GetSubmission(context.Background(), "sub_1"); !errors.Is(err, tt.want) || errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("GetSubmission() error = %v, want only %v identity", err, tt.want)
			}
			if _, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{}); !errors.Is(err, tt.want) || errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("ListSubmissions() error = %v, want only %v identity", err, tt.want)
			}
		})
	}
}

type submissionErrorBody struct {
	err error
}

func (body submissionErrorBody) Read([]byte) (int, error) {
	return 0, body.err
}

func (submissionErrorBody) Close() error {
	return nil
}

func TestAnonymousCatalogNeverReceivesSubmissionBearer(t *testing.T) {
	var paths []string
	content := []byte(`{"schema_version":1,"name":"demo","version":"v1.0.0","description":"demo MCP","definition":{"transport":"stdio","command":"demo-mcp","args":["--stdio"]}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		if authorization := r.Header.Values("Authorization"); len(authorization) != 0 {
			t.Fatalf("%s Authorization = %q, want absent", r.URL.EscapedPath(), authorization)
		}
		if r.URL.EscapedPath() == "/api/v1/resources/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"schema_version":1,"catalog_sha256":"0000000000000000000000000000000000000000000000000000000000000000","catalog_size":0}`))
			return
		}
		if r.URL.EscapedPath() == "/api/v1/resources/mcp/demo/versions/v1.0.0/content" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			_, _ = w.Write(content)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client := newTestSubmissionClientWithKey(t, server, "official-key")
	if _, err := client.CatalogVersion(context.Background()); err != nil {
		t.Fatalf("CatalogVersion() error = %v", err)
	}
	ref := domain.PowerRef{Type: domain.PowerTypeMCP, Name: "demo", Version: "v1.0.0"}
	got, err := client.GetMCP(context.Background(), validRemoteCatalogEntry(ref, content))
	if err != nil {
		t.Fatalf("GetMCP() error = %v", err)
	}
	want := domain.MCPServer{Transport: domain.MCPTransportStdio, Command: "demo-mcp", Args: []string{"--stdio"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetMCP() = %#v, want %#v", got, want)
	}
	wantPaths := []string{
		"/api/v1/resources/version",
		"/api/v1/resources/mcp/demo/versions/v1.0.0/content",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("anonymous resource request paths = %v, want %v", paths, wantPaths)
	}
}

func TestListSubmissionsRejectsInvalidResponses(t *testing.T) {
	twentyOneItems := make([]string, 21)
	for index := range twentyOneItems {
		twentyOneItems[index] = fmt.Sprintf(
			`{"submission_id":"sub_%02d","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"%s"}`,
			index, time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC).Add(-time.Duration(index)*time.Minute).Format(time.RFC3339),
		)
	}
	tests := []struct {
		name  string
		query domain.SubmissionListQuery
		body  string
	}{
		{name: "more than twenty", body: `{"items":[` + strings.Join(twentyOneItems, ",") + `]}`},
		{name: "terminal status", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"published","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "duplicate id", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"},{"submission_id":"sub_1","type":"mcp","name":"tools","version":"v1.0.0","status":"validating","created_at":"2026-08-24T09:00:00Z"}]}`},
		{name: "status filter mismatch", query: domain.SubmissionListQuery{Status: domain.SubmissionReviewPending}, body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "type filter mismatch", query: domain.SubmissionListQuery{Type: domain.PowerTypeMCP}, body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "name filter mismatch", query: domain.SubmissionListQuery{Name: "other"}, body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "invalid timestamp", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"yesterday"}]}`},
		{name: "increasing created at", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T09:00:00Z"},{"submission_id":"sub_2","type":"skill","name":"demo","version":"v1.0.1","status":"validating","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "missing items", body: `{}`},
		{name: "duplicate JSON key", body: `{"items":[],"items":[]}`},
		{name: "trailing JSON value", body: `{"items":[]} {}`},
		{name: "empty id", body: `{"items":[{"submission_id":"","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "unknown type", body: `{"items":[{"submission_id":"sub_1","type":"workflow","name":"demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "noncanonical name", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"Demo","version":"v1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
		{name: "noncanonical version", body: `{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"1.0.0","status":"received","created_at":"2026-08-24T10:00:00Z"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			if _, err := client.ListSubmissions(context.Background(), tt.query); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("ListSubmissions() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestListSubmissionsAcceptsActiveStatusesAndForwardFields(t *testing.T) {
	for _, status := range []domain.SubmissionStatus{
		domain.SubmissionReceived,
		domain.SubmissionValidating,
		domain.SubmissionReviewPending,
		domain.SubmissionApproved,
		domain.SubmissionPublishing,
	} {
		t.Run(string(status), func(t *testing.T) {
			body := fmt.Sprintf(`{"items":[{"submission_id":"sub_1","type":"agent","name":"demo","version":"v1.0.0","status":%q,"created_at":"2026-08-24T10:00:00Z","future_item":true}],"future_page":{"value":1}}`, status)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			got, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{})
			if err != nil || len(got.Items) != 1 || got.Items[0].Status != status {
				t.Fatalf("ListSubmissions() = %+v, %v", got, err)
			}
		})
	}
}

func TestListSubmissionsAcceptsTwentyItemsWithEqualCreatedAt(t *testing.T) {
	items := make([]string, 20)
	for index := range items {
		items[index] = fmt.Sprintf(
			`{"submission_id":"sub_%02d","type":"plugin","name":"demo","version":"v1.0.0","status":"approved","created_at":"2026-08-24T10:00:00Z"}`,
			index,
		)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[` + strings.Join(items, ",") + `]}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{})
	if err != nil || len(got.Items) != 20 {
		t.Fatalf("ListSubmissions() = %+v, %v", got, err)
	}
}

func TestGetSubmissionRejectsInvalidResponses(t *testing.T) {
	timeline := `[{"event_id":"evt_1","event_type":"submission_received","status_from":"","status_to":"received","timestamp":"2026-08-24T10:00:00Z","message":"submission received"},{"event_id":"evt_2","event_type":"review_requested","status_from":"received","status_to":"review_pending","timestamp":"2026-08-24T10:02:00Z","message":"review requested"}]`
	tests := []struct {
		name string
		body string
	}{
		{name: "missing description", body: strings.Replace(validSubmissionDetailJSON, `"description":"does things",`, "", 1)},
		{name: "empty description", body: strings.Replace(validSubmissionDetailJSON, `"description":"does things"`, `"description":""`, 1)},
		{name: "missing message", body: strings.Replace(validSubmissionDetailJSON, `"message":"first release",`, "", 1)},
		{name: "empty message", body: strings.Replace(validSubmissionDetailJSON, `"message":"first release"`, `"message":""`, 1)},
		{name: "missing created at", body: strings.Replace(validSubmissionDetailJSON, `"created_at":"2026-08-24T10:00:00Z",`, "", 1)},
		{name: "missing updated at", body: strings.Replace(validSubmissionDetailJSON, `"updated_at":"2026-08-24T10:02:00Z",`, "", 1)},
		{name: "missing timeline", body: strings.Replace(validSubmissionDetailJSON, `,"timeline":`+timeline, "", 1)},
		{name: "missing identity type", body: strings.Replace(validSubmissionDetailJSON, `"type":"skill",`, "", 1)},
		{name: "missing status", body: strings.Replace(validSubmissionDetailJSON, `"status":"review_pending",`, "", 1)},
		{name: "response id mismatch", body: strings.Replace(validSubmissionDetailJSON, `"submission_id":"sub_1"`, `"submission_id":"sub_other"`, 1)},
		{name: "invalid identity", body: strings.Replace(validSubmissionDetailJSON, `"version":"v1.0.0"`, `"version":"latest"`, 1)},
		{name: "unknown status", body: strings.Replace(validSubmissionDetailJSON, `"status":"review_pending"`, `"status":"waiting"`, 1)},
		{name: "invalid detail timestamp", body: strings.Replace(validSubmissionDetailJSON, `"updated_at":"2026-08-24T10:02:00Z"`, `"updated_at":"soon"`, 1)},
		{name: "decreasing timeline", body: strings.Replace(validSubmissionDetailJSON, `"timestamp":"2026-08-24T10:02:00Z"`, `"timestamp":"2026-08-24T09:59:00Z"`, 1)},
		{name: "unknown status from", body: strings.Replace(validSubmissionDetailJSON, `"status_from":"received"`, `"status_from":"waiting"`, 1)},
		{name: "missing status from", body: strings.Replace(validSubmissionDetailJSON, `"status_from":"",`, "", 1)},
		{name: "empty status to", body: strings.Replace(validSubmissionDetailJSON, `"status_to":"received"`, `"status_to":""`, 1)},
		{name: "empty event id", body: strings.Replace(validSubmissionDetailJSON, `"event_id":"evt_1"`, `"event_id":""`, 1)},
		{name: "missing event type", body: strings.Replace(validSubmissionDetailJSON, `,"event_type":"submission_received"`, "", 1)},
		{name: "empty event message", body: strings.Replace(validSubmissionDetailJSON, `"message":"submission received"`, `"message":""`, 1)},
		{name: "missing event message", body: strings.Replace(validSubmissionDetailJSON, `,"message":"submission received"`, "", 1)},
		{name: "missing event timestamp", body: strings.Replace(validSubmissionDetailJSON, `,"timestamp":"2026-08-24T10:00:00Z"`, "", 1)},
		{name: "missing content available", body: strings.Replace(validSubmissionDetailJSON, `"content_available":true,`, "", 1)},
		{name: "missing validation errors", body: strings.Replace(validSubmissionDetailJSON, `"validation_errors":["missing card"],`, "", 1)},
		{name: "missing review comment", body: strings.Replace(validSubmissionDetailJSON, `"review_comment":"",`, "", 1)},
		{name: "missing published version", body: strings.Replace(validSubmissionDetailJSON, `"published_version":"",`, "", 1)},
		{name: "duplicate nested key", body: strings.Replace(validSubmissionDetailJSON, `"event_id":"evt_1"`, `"event_id":"evt_1","event_id":"shadow"`, 1)},
		{name: "trailing JSON value", body: validSubmissionDetailJSON + ` []`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			if _, err := client.GetSubmission(context.Background(), "sub_1"); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("GetSubmission() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestGetSubmissionAcceptsPresentZeroValuesAndUnknownFields(t *testing.T) {
	body := `{"submission_id":"sub_1","type":"mcp","name":"demo","version":"v1.0.0","description":"does things","message":"first release","status":"published","created_at":"2026-08-24T10:00:00Z","updated_at":"2026-08-24T10:02:00Z","content_available":false,"validation_errors":[],"review_comment":"","published_version":"","timeline":[{"event_id":"evt_1","event_type":"published","status_from":"publishing","status_to":"published","timestamp":"2026-08-24T10:00:00Z","message":"published","future_event":true},{"event_id":"evt_2","event_type":"catalog_updated","status_from":"published","status_to":"published","timestamp":"2026-08-24T10:00:00Z","message":"catalog updated"}],"future_detail":{"enabled":true}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.GetSubmission(context.Background(), "sub_1")
	if err != nil {
		t.Fatalf("GetSubmission() error = %v", err)
	}
	if got.ContentAvailable || got.ValidationErrors == nil || got.Timeline == nil || len(got.ValidationErrors) != 0 || len(got.Timeline) != 2 {
		t.Fatalf("GetSubmission() present zero values = %+v", got)
	}
}

func TestGetSubmissionMapsCrossAccountNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"code":"NOT_FOUND","trace_id":"tr_hidden"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.GetSubmission(context.Background(), "sub_other_account")
	if !errors.Is(err, cloud.ErrNotFound) || errors.Is(err, cloud.ErrAuth) {
		t.Fatalf("GetSubmission() error = %v, want only ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "NOT_FOUND") || !strings.Contains(err.Error(), "tr_hidden") {
		t.Fatalf("GetSubmission() error = %v, want safe problem context", err)
	}
}

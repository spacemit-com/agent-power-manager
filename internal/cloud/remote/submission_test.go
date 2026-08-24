package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func newTestSubmissionClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewWithAPIKey(server.URL, &http.Client{Timeout: 5 * time.Second}, "key")
	if err != nil {
		t.Fatalf("NewWithAPIKey() error = %v", err)
	}
	return client
}

// readSubmissionParts 读取 multipart 请求，返回 metadata JSON map、content part 的 Content-Type 与原始字节。
func readSubmissionParts(t *testing.T, r *http.Request) (map[string]any, string, []byte) {
	t.Helper()
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, err = %v", r.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	var metadata map[string]any
	var contentType string
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
			content = body
		}
	}
	return metadata, contentType, content
}

func TestSubmitAgentUsesRawYAMLPart(t *testing.T) {
	raw := []byte("schema_version: 1\nagent: {id: demo, version: v1.0.0, description: demo}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadata, contentType, content := readSubmissionParts(t, r)
		if metadata["type"] != "agent" || metadata["name"] != "demo" || metadata["version"] != "v1.0.0" ||
			metadata["description"] != "demo" || metadata["change_summary"] != "first" || metadata["content_md5"] != md5Hex(raw) {
			t.Fatalf("metadata = %#v", metadata)
		}
		if contentType != "application/yaml" || !bytes.Equal(content, raw) {
			t.Fatalf("content type/bytes = %q/%q", contentType, content)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/api/v1/submissions/sub_1"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeAgent, Name: "demo", Version: "v1.0.0", Description: "demo",
		ChangeSummary: "first", ContentType: "application/yaml", Content: raw,
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
		metadata, contentType, content := readSubmissionParts(t, r)
		if metadata["type"] != "skill" || metadata["name"] != "demo" || metadata["version"] != "v1.0.0" ||
			metadata["description"] != "does things" || metadata["change_summary"] != "first" || metadata["content_md5"] != "md5" {
			t.Fatalf("metadata = %#v", metadata)
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
		Description: "does things", ChangeSummary: "first",
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

func TestSubmitReturnsReplayedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"published","content_available":true,"validation_errors":[],"review_comment":"","published_version":"v1.0.0"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Outcome != domain.SubmissionReplayed || got.SubmissionID != "sub_1" ||
		got.Status != domain.SubmissionPublished || got.StatusURL != "" {
		t.Fatalf("Submit() = %+v", got)
	}
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
				Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
			})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestSubmitRejectsShapeMismatch(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "200 with receipt body", status: http.StatusOK, body: `{"submission_id":"sub_1","status":"received","status_url":"/s"}`},
		{name: "202 with submission body", status: http.StatusAccepted, body: `{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","content_available":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
			})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Submit() error = %v, want ErrInvalidResponse", err)
			}
		})
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
				Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
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
		{name: "unknown type", request: domain.SubmissionRequest{Type: domain.PowerType("workflow"), ContentType: "application/gzip", Content: []byte("x")}},
		{name: "skill with yaml", request: domain.SubmissionRequest{Type: domain.PowerTypeSkill, ContentType: "application/yaml", Content: []byte("x")}},
		{name: "plugin with json", request: domain.SubmissionRequest{Type: domain.PowerTypePlugin, ContentType: "application/json", Content: []byte("x")}},
		{name: "mcp with gzip", request: domain.SubmissionRequest{Type: domain.PowerTypeMCP, ContentType: "application/gzip", Content: []byte("x")}},
		{name: "agent with gzip", request: domain.SubmissionRequest{Type: domain.PowerTypeAgent, ContentType: "application/gzip", Content: []byte("x")}},
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
		{"agent", domain.PowerTypeAgent, "application/yaml"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			var gotContentType string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, contentType, _ := readSubmissionParts(t, r)
				gotContentType = contentType
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"submission_id":"sub_1","status":"received","status_url":"/s"}`))
			}))
			defer server.Close()
			client := newTestSubmissionClient(t, server)
			_, err := client.Submit(context.Background(), domain.SubmissionRequest{
				Type: tt.requestType, ContentType: tt.contentType, Content: []byte("x"),
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
		Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
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
		Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
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
		Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrAlreadyPublished) || !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("Submit() error = %v, want both ErrAlreadyPublished and ErrSubmissionConflict", err)
	}
	if !strings.Contains(err.Error(), "ALREADY_PUBLISHED") || !strings.Contains(err.Error(), "tr_already") {
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
		Type: domain.PowerTypeSkill, ContentType: "application/gzip", Content: []byte("x"),
	})
	if !errors.Is(err, cloud.ErrUnavailable) {
		t.Fatalf("Submit() error = %v, want ErrUnavailable", err)
	}
}

func TestGetSubmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"review_pending","content_available":true,"validation_errors":["missing card"],"review_comment":"","published_version":""}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.GetSubmission(context.Background(), "sub_1")
	if err != nil {
		t.Fatalf("GetSubmission() error = %v", err)
	}
	if got.SubmissionID != "sub_1" || got.Name != "demo" || got.Status != domain.SubmissionReviewPending {
		t.Fatalf("GetSubmission() = %+v", got)
	}
	if len(got.ValidationErrors) != 1 || got.ValidationErrors[0] != "missing card" {
		t.Fatalf("ValidationErrors = %#v", got.ValidationErrors)
	}
}

func TestListSubmissions(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"submission_id":"sub_1","type":"skill","name":"demo","version":"v1.0.0","status":"received","created_at":"2024-01-01T00:00:00Z"}],"next_cursor":"next"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	got, err := client.ListSubmissions(context.Background(), domain.SubmissionListQuery{Status: domain.SubmissionReceived, Type: domain.PowerTypeSkill, Name: "demo", Limit: 10})
	if err != nil {
		t.Fatalf("ListSubmissions() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].SubmissionID != "sub_1" || got.NextCursor != "next" {
		t.Fatalf("ListSubmissions() = %+v", got)
	}
	for _, want := range []string{"status=received", "type=skill", "name=demo", "limit=10"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("query %q missing %q", gotQuery, want)
		}
	}
}

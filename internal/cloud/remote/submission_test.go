package remote

import (
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

func TestSubmitReturnsReceipt(t *testing.T) {
	var gotAuth, gotIdem, gotMetadataType, gotContentType string
	var gotMetadata map[string]string
	var gotContent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("Content-Type = %q, err = %v", r.Header.Get("Content-Type"), err)
			return
		}
		reader := multipart.NewReader(r.Body, parameters["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("NextPart() error = %v", err)
				return
			}
			body, _ := io.ReadAll(part)
			switch part.FormName() {
			case "metadata":
				gotMetadataType = part.Header.Get("Content-Type")
				_ = json.Unmarshal(body, &gotMetadata)
			case "content":
				gotContentType = part.Header.Get("Content-Type")
				gotContent = string(body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"submission_id": "sub_1", "status": "received", "status_url": "/api/v1/submissions/sub_1"})
	}))
	defer server.Close()

	client := newTestSubmissionClient(t, server)
	receipt, err := client.Submit(context.Background(), domain.SubmissionRequest{
		Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0",
		Description: "does things", ChangeSummary: "first",
		ContentType: "application/gzip", Content: []byte("bytes"), ContentMD5: "md5", IdempotencyKey: "k",
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if receipt.SubmissionID != "sub_1" || receipt.Status != domain.SubmissionReceived {
		t.Fatalf("receipt = %+v", receipt)
	}
	if gotAuth != "Bearer key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotIdem != "k" {
		t.Fatalf("Idempotency-Key = %q", gotIdem)
	}
	if gotMetadataType != "application/json" || gotContentType != "application/gzip" {
		t.Fatalf("part types = metadata %q, content %q", gotMetadataType, gotContentType)
	}
	if gotMetadata["name"] != "demo" || gotMetadata["version"] != "v1.0.0" || gotMetadata["change_summary"] != "first" || gotMetadata["content_md5"] != "md5" {
		t.Fatalf("metadata = %#v", gotMetadata)
	}
	if gotContent != "bytes" {
		t.Fatalf("content = %q", gotContent)
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
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{Content: []byte("x"), ContentType: "application/gzip"})
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
		_, _ = w.Write([]byte(`{"status":409,"code":"ACTIVE_SUBMISSION","trace_id":"tr_2"}`))
	}))
	defer server.Close()
	client := newTestSubmissionClient(t, server)
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{Content: []byte("x"), ContentType: "application/gzip"})
	if !errors.Is(err, cloud.ErrSubmissionConflict) {
		t.Fatalf("Submit() error = %v, want ErrSubmissionConflict", err)
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
	_, err := client.Submit(context.Background(), domain.SubmissionRequest{Content: []byte("x"), ContentType: "application/gzip"})
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

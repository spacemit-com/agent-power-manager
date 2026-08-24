package remote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestResolveRejectsDuplicateKeysInLatestAndExactResponseDTOs(t *testing.T) {
	tests := []struct {
		name  string
		query domain.PowerRef
		body  string
	}{
		{
			name:  "latest",
			query: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo"},
			body:  `{"type":"skill","name":"demo","name":"shadow","version":"v1.0.0"}`,
		},
		{
			name:  "exact",
			query: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"},
			body:  `{"type":"skill","name":"demo","version":"v1.0.0","description":"Description","message":"First","message":"Second","content_type":"application/gzip","content_size":42,"content_md5":"8c7dd922ad47494fc02c388e12c00eac","published_at":"2026-08-18T09:30:12Z","status":"published"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)
			if _, err := client.Resolve(context.Background(), tt.query); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Resolve() error = %v, want duplicate-key ErrInvalidResponse", err)
			}
		})
	}
}

func TestResolveExactRejectsLegacySummaryWithoutMessage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		metadata := validMetadata("skill", "demo", "v1.0.0")
		delete(metadata, "message")
		metadata["change_summary"] = "legacy"
		metadata["status"] = "published"
		writeJSON(t, w, metadata)
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"})
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("Resolve() error = %v, want legacy-only metadata rejected", err)
	}
}

func TestResolveLatestReturnsExactIdentity(t *testing.T) {
	t.Parallel()

	want := domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v2.3.4"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/resources/skill/demo/latest" {
			t.Errorf("path = %q, want latest path", r.URL.EscapedPath())
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty", r.URL.RawQuery)
		}
		writeJSON(t, w, map[string]any{
			"type":         "skill",
			"name":         "demo",
			"version":      "v2.3.4",
			"future_field": true,
		})
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	got, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveExactValidatesPublishedMetadata(t *testing.T) {
	t.Parallel()

	want := domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.2.3"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/resources/plugin/demo-plugin/versions/v1.2.3" {
			t.Errorf("path = %q, want exact metadata path", r.URL.EscapedPath())
		}
		metadata := validMetadata("plugin", "demo-plugin", "v1.2.3")
		metadata["status"] = "published"
		metadata["future_field"] = "ignored"
		writeJSON(t, w, metadata)
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	got, err := client.Resolve(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolvePublishedMetadataRejectsYankDetails(t *testing.T) {
	tests := []struct {
		name       string
		yankedAt   any
		yankReason any
		wantErr    bool
	}{
		{name: "absent"},
		{name: "empty", yankedAt: "", yankReason: ""},
		{name: "yanked time", yankedAt: "2026-08-19T01:02:03Z", wantErr: true},
		{name: "yank reason", yankReason: "Security issue", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				metadata := validMetadata("skill", "demo", "v1.0.0")
				metadata["status"] = "published"
				if tt.yankedAt != nil {
					metadata["yanked_at"] = tt.yankedAt
				}
				if tt.yankReason != nil {
					metadata["yank_reason"] = tt.yankReason
				}
				writeJSON(t, w, metadata)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"})
			if tt.wantErr && !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Resolve() error = %v, want ErrInvalidResponse", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Resolve() error = %v, want nil", err)
			}
		})
	}
}

func TestResolveRejectsInvalidQueryWithoutRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)
	tests := []domain.PowerRef{
		{Type: "workflow", Name: "demo"},
		{Type: domain.PowerTypeSkill, Name: "../demo"},
		{Type: domain.PowerTypeSkill, Name: "Demo"},
		{Type: domain.PowerTypeSkill, Name: "demo", Version: "1.0.0"},
	}

	for _, query := range tests {
		if _, err := client.Resolve(context.Background(), query); !errors.Is(err, cloud.ErrNotFound) {
			t.Fatalf("Resolve(%#v) error = %v, want ErrNotFound", query, err)
		}
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0", requests)
	}
}

func TestResolvePrefersCancelledContextOverInvalidQuery(t *testing.T) {
	t.Parallel()

	client := newRemoteTestClient(t, "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Resolve(ctx, domain.PowerRef{Type: "workflow", Name: "../demo"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}
	if errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("Resolve() error = %v, cancelled context must take precedence over invalid query", err)
	}
}

func TestResolveRejectsLatestIdentityMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response map[string]any
	}{
		{name: "type", response: map[string]any{"type": "mcp", "name": "demo", "version": "v1.0.0"}},
		{name: "name", response: map[string]any{"type": "skill", "name": "other", "version": "v1.0.0"}},
		{name: "version", response: map[string]any{"type": "skill", "name": "demo", "version": "not-semver"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, tt.response)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo"})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Resolve() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestResolveRejectsExactIdentityMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "type", mutate: func(metadata map[string]any) { metadata["type"] = "mcp" }},
		{name: "name", mutate: func(metadata map[string]any) { metadata["name"] = "other" }},
		{name: "version", mutate: func(metadata map[string]any) { metadata["version"] = "v2.0.0" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				metadata := validMetadata("skill", "demo", "v1.0.0")
				metadata["status"] = "published"
				tt.mutate(metadata)
				writeJSON(t, w, metadata)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"})
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("Resolve() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestResolveMapsYankedMetadataAndHTTPStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   map[string]any
		want   error
	}{
		{name: "HTTP 404", status: http.StatusNotFound, want: cloud.ErrNotFound},
		{name: "HTTP 410", status: http.StatusGone, want: cloud.ErrYanked},
		{name: "metadata yanked", status: http.StatusOK, body: map[string]any{"status": "yanked", "yanked_at": "2026-08-19T01:02:03Z", "yank_reason": "Security issue"}, want: cloud.ErrYanked},
		{name: "unknown metadata status", status: http.StatusOK, body: map[string]any{"status": "draft"}, want: cloud.ErrInvalidResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.status != http.StatusOK {
					w.WriteHeader(tt.status)
					return
				}
				metadata := validMetadata("skill", "demo", "v1.0.0")
				for key, value := range tt.body {
					metadata[key] = value
				}
				writeJSON(t, w, metadata)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestResolveValidatesYankedMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		yankedAt   any
		yankReason any
		want       error
	}{
		{name: "valid", yankedAt: "2026-08-19T01:02:03Z", yankReason: "Security issue", want: cloud.ErrYanked},
		{name: "valid zero offset", yankedAt: "2026-08-19T01:02:03+00:00", yankReason: "Security issue", want: cloud.ErrYanked},
		{name: "missing yanked time", yankReason: "Security issue", want: cloud.ErrInvalidResponse},
		{name: "non-UTC yanked time", yankedAt: "2026-08-19T09:02:03+08:00", yankReason: "Security issue", want: cloud.ErrInvalidResponse},
		{name: "unknown-offset yanked time", yankedAt: "2026-08-19T01:02:03-00:00", yankReason: "Security issue", want: cloud.ErrInvalidResponse},
		{name: "missing public reason", yankedAt: "2026-08-19T01:02:03Z", want: cloud.ErrInvalidResponse},
		{name: "blank public reason", yankedAt: "2026-08-19T01:02:03Z", yankReason: " \t\n", want: cloud.ErrInvalidResponse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				metadata := validMetadata("skill", "demo", "v1.0.0")
				metadata["status"] = "yanked"
				if tt.yankedAt != nil {
					metadata["yanked_at"] = tt.yankedAt
				}
				if tt.yankReason != nil {
					metadata["yank_reason"] = tt.yankReason
				}
				writeJSON(t, w, metadata)
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.Resolve(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo", Version: "v1.0.0"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, tt.want)
			}
			if tt.want == cloud.ErrInvalidResponse && errors.Is(err, cloud.ErrYanked) {
				t.Fatalf("Resolve() error = %v, malformed yanked metadata must not map to ErrYanked", err)
			}
		})
	}
}

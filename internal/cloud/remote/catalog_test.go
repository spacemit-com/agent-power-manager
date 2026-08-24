package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"apm/internal/cloud"
	"apm/internal/domain"
)

const (
	testSnapshot  = "2026-08-18T09:30:12.123456Z"
	testPublished = "2026-08-18T09:30:12Z"
	testMD5       = "8c7dd922ad47494fc02c388e12c00eac"
)

func TestListTraversesOneSnapshotAndSorts(t *testing.T) {
	t.Parallel()

	pages := map[string]any{
		"": validCatalogPage(testSnapshot, []any{
			validMetadata("skill", "zeta", "v2.0.0"),
			validMetadata("mcp", "alpha", "v1.0.0"),
		}, "opaque +/? cursor"),
		"opaque +/? cursor": validCatalogPage(testSnapshot, []any{
			validMetadata("skill", "zeta", "v1.0.0"),
			validMetadata("plugin", "demo", "v1.0.0"),
			validMetadata("agent", "agent-one", "v1.0.0"),
		}, ""),
	}
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/resources" {
			t.Errorf("path = %q, want /api/v1/resources", r.URL.Path)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q, want application/json", accept)
		}
		query := r.URL.Query()
		if query.Get("limit") != "100" || query.Has("view") || query.Has("type") {
			t.Errorf("query = %q, want only limit and optional cursor", r.URL.RawQuery)
		}
		cursor := query.Get("cursor")
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		page, ok := pages[cursor]
		if !ok {
			t.Errorf("unexpected cursor %q", cursor)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(t, w, page)
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{
		{Name: "agent-one", Type: domain.PowerTypeAgent, Version: "v1.0.0"},
		{Name: "alpha", Type: domain.PowerTypeMCP, Version: "v1.0.0"},
		{Name: "demo", Type: domain.PowerTypePlugin, Version: "v1.0.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "v1.0.0"},
		{Name: "zeta", Type: domain.PowerTypeSkill, Version: "v2.0.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) != 2 || queries[0] != "limit=100" || queries[1] != "cursor=opaque+%2B%2F%3F+cursor&limit=100" {
		t.Fatalf("queries = %#v, want encoded opaque cursor after first page", queries)
	}
}

func TestListRejectsInvalidPagination(t *testing.T) {
	t.Parallel()

	duplicate := validMetadata("skill", "demo", "v1.0.0")
	tests := []struct {
		name  string
		pages map[string]any
	}{
		{
			name: "snapshot changes",
			pages: map[string]any{
				"":     validCatalogPage(testSnapshot, nil, "next"),
				"next": validCatalogPage("2026-08-18T09:31:12Z", nil, ""),
			},
		},
		{
			name: "cursor loops",
			pages: map[string]any{
				"":     validCatalogPage(testSnapshot, nil, "next"),
				"next": validCatalogPage(testSnapshot, nil, "next"),
			},
		},
		{
			name: "duplicate ref",
			pages: map[string]any{
				"":     validCatalogPage(testSnapshot, []any{duplicate}, "next"),
				"next": validCatalogPage(testSnapshot, []any{duplicate}, ""),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := newCatalogServer(t, tt.pages)
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("List() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestListRejectsMalformedCatalogData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown type", mutate: func(item map[string]any) { item["type"] = "workflow" }},
		{name: "unsafe name", mutate: func(item map[string]any) { item["name"] = "../demo" }},
		{name: "uppercase name", mutate: func(item map[string]any) { item["name"] = "Demo" }},
		{name: "invalid version", mutate: func(item map[string]any) { item["version"] = "1.0.0" }},
		{name: "prerelease version", mutate: func(item map[string]any) { item["version"] = "v1.0.0-rc.1" }},
		{name: "missing message", mutate: func(item map[string]any) { delete(item, "message") }},
		{name: "blank message", mutate: func(item map[string]any) { item["message"] = " \t\n" }},
		{name: "uppercase MD5", mutate: func(item map[string]any) { item["content_md5"] = strings.ToUpper(testMD5) }},
		{name: "negative size", mutate: func(item map[string]any) { item["content_size"] = -1 }},
		{name: "wrong content type", mutate: func(item map[string]any) { item["content_type"] = "application/json" }},
		{name: "invalid published time", mutate: func(item map[string]any) { item["published_at"] = "yesterday" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := validMetadata("skill", "demo", "v1.0.0")
			tt.mutate(item)
			server := newCatalogServer(t, map[string]any{"": validCatalogPage(testSnapshot, []any{item}, "")})
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("List() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestListRejectsInvalidSnapshotTimestamp(t *testing.T) {
	t.Parallel()

	server := newCatalogServer(t, map[string]any{"": validCatalogPage("not-a-time", nil, "")})
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want ErrInvalidResponse", err)
	}
}

func TestListRejectsNonUTCTimestamps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		snapshot  string
		published string
	}{
		{name: "snapshot offset", snapshot: "2026-08-18T17:30:12.123456+08:00", published: testPublished},
		{name: "published offset", snapshot: testSnapshot, published: "2026-08-18T17:30:12+08:00"},
		{name: "snapshot unknown offset", snapshot: "2026-08-18T09:30:12.123456-00:00", published: testPublished},
		{name: "published unknown offset", snapshot: testSnapshot, published: "2026-08-18T09:30:12-00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := validMetadata("skill", "demo", "v1.0.0")
			item["published_at"] = tt.published
			server := newCatalogServer(t, map[string]any{"": validCatalogPage(tt.snapshot, []any{item}, "")})
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("List() error = %v, want non-UTC ErrInvalidResponse", err)
			}
		})
	}
}

func TestListAcceptsZeroOffsetUTCTimestamps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		snapshot  string
		published string
	}{
		{name: "snapshot", snapshot: "2026-08-18T09:30:12.123456+00:00", published: testPublished},
		{name: "published", snapshot: testSnapshot, published: "2026-08-18T09:30:12+00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := validMetadata("skill", "demo", "v1.0.0")
			item["published_at"] = tt.published
			server := newCatalogServer(t, map[string]any{"": validCatalogPage(tt.snapshot, []any{item}, "")})
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			got, err := client.List(context.Background())
			if err != nil {
				t.Fatalf("List() error = %v, want zero-offset UTC accepted", err)
			}
			want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("List() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestListIgnoresUnknownOptionalResponseFields(t *testing.T) {
	t.Parallel()

	item := validMetadata("skill", "demo", "v1.0.0")
	item["future_item_field"] = map[string]any{"nested": true}
	page := validCatalogPage(testSnapshot, []any{item}, "")
	page["future_page_field"] = []string{"ignored"}
	server := newCatalogServer(t, map[string]any{"": page})
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.PowerRef{{Name: "demo", Type: domain.PowerTypeSkill, Version: "v1.0.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %#v, want %#v", got, want)
	}
}

func TestListRejectsTrailingJSON(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, validCatalogPage(testSnapshot, nil, ""))
		_, _ = w.Write([]byte(` {"extra":true}`))
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want trailing-data ErrInvalidResponse", err)
	}
}

func TestListRejectsDuplicateKeysInResponseDTO(t *testing.T) {
	t.Parallel()

	const body = `{"snapshot_updated_at":"2026-08-18T09:30:12.123456Z","items":[{"type":"skill","name":"demo","version":"v1.0.0","description":"Description","message":"First","message":"Second","content_type":"application/gzip","content_size":42,"content_md5":"8c7dd922ad47494fc02c388e12c00eac","published_at":"2026-08-18T09:30:12Z"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want duplicate-key ErrInvalidResponse", err)
	}
}

func TestListRejectsResponseOverHardLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"snapshot_updated_at":"`+testSnapshot+`","items":[],"future_padding":"`)
		_, _ = io.CopyN(w, repeatedByteReader('x'), maxJSONResponse)
		_, _ = io.WriteString(w, `"}`)
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want over-limit ErrInvalidResponse", err)
	}
}

func TestListPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List() error = %v, want context.Canceled", err)
	}
}

func validMetadata(powerType, name, version string) map[string]any {
	contentType := map[string]string{
		"skill":  "application/gzip",
		"plugin": "application/gzip",
		"mcp":    "application/json",
		"agent":  "application/yaml",
	}[powerType]
	return map[string]any{
		"type":         powerType,
		"name":         name,
		"version":      version,
		"description":  "Description",
		"message":      "Submission message",
		"content_type": contentType,
		"content_size": 42,
		"content_md5":  testMD5,
		"published_at": testPublished,
	}
}

func validCatalogPage(snapshot string, items []any, nextCursor string) map[string]any {
	if items == nil {
		items = []any{}
	}
	page := map[string]any{
		"snapshot_updated_at": snapshot,
		"items":               items,
	}
	if nextCursor != "" {
		page["next_cursor"] = nextCursor
	}
	return page
}

func newCatalogServer(t *testing.T, pages map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, ok := pages[r.URL.Query().Get("cursor")]
		if !ok {
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("cursor"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		writeJSON(t, w, page)
	}))
}

func newRemoteTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(baseURL, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

type repeatedByteReader byte

func (reader repeatedByteReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = byte(reader)
	}
	return len(buffer), nil
}

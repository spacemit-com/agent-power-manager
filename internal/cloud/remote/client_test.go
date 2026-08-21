package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"apm/internal/cloud"
)

func TestNewValidatesBaseURLAndHTTPClient(t *testing.T) {
	t.Parallel()

	bounded := &http.Client{Timeout: time.Second}
	tests := []struct {
		name    string
		baseURL string
		client  *http.Client
		wantErr bool
	}{
		{name: "https", baseURL: "https://apc.example.com", client: bounded},
		{name: "https root path", baseURL: "https://apc.example.com/", client: bounded},
		{name: "localhost HTTP", baseURL: "http://localhost:8080", client: bounded},
		{name: "IPv4 loopback HTTP", baseURL: "http://127.0.0.1:8080", client: bounded},
		{name: "IPv6 loopback HTTP", baseURL: "http://[::1]:8080", client: bounded},
		{name: "relative URL", baseURL: "apc.example.com", client: bounded, wantErr: true},
		{name: "missing host", baseURL: "https:///api", client: bounded, wantErr: true},
		{name: "unsupported scheme", baseURL: "ftp://apc.example.com", client: bounded, wantErr: true},
		{name: "non-loopback HTTP", baseURL: "http://apc.example.com", client: bounded, wantErr: true},
		{name: "userinfo", baseURL: "https://user:secret@apc.example.com", client: bounded, wantErr: true},
		{name: "query", baseURL: "https://apc.example.com?token=secret", client: bounded, wantErr: true},
		{name: "fragment", baseURL: "https://apc.example.com#fragment", client: bounded, wantErr: true},
		{name: "non-root path", baseURL: "https://apc.example.com/root", client: bounded, wantErr: true},
		{name: "nil client", baseURL: "https://apc.example.com", client: nil, wantErr: true},
		{name: "unbounded client", baseURL: "https://apc.example.com", client: &http.Client{}, wantErr: true},
		{name: "negative timeout", baseURL: "https://apc.example.com", client: &http.Client{Timeout: -time.Second}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.baseURL, tt.client)
			if tt.wantErr {
				if !errors.Is(err, cloud.ErrProviderConfig) {
					t.Fatalf("New() error = %v, want ErrProviderConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestNewCopiesInjectedClient(t *testing.T) {
	t.Parallel()

	injected := &http.Client{Timeout: time.Second}
	client, err := New("https://apc.example.com/", injected)
	if err != nil {
		t.Fatal(err)
	}
	injected.Timeout = 0

	if client.http == injected {
		t.Fatal("New() retained injected client pointer, want value copy")
	}
	if client.http.Timeout != time.Second {
		t.Fatalf("copied Timeout = %v, want %v", client.http.Timeout, time.Second)
	}
	if client.baseURL.Path != "" {
		t.Fatalf("normalized base path = %q, want empty", client.baseURL.Path)
	}
}

func TestClientRejectsCrossOriginRedirectBeforeCallerPolicy(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()

	var callerChecks atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer source.Close()

	client, err := New(source.URL, &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			callerChecks.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.List(context.Background())
	if err == nil || !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want cross-origin ErrInvalidResponse", err)
	}
	if got := callerChecks.Load(); got != 0 {
		t.Fatalf("caller redirect checks = %d, want 0", got)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("cross-origin target requests = %d, want 0", got)
	}
}

func TestClientRejectsCallerRedirectMutationToCrossOrigin(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests.Add(1)
		writeJSON(t, w, validCatalogPage(testSnapshot, nil, ""))
	}))
	defer target.Close()
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatal(err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer source.Close()
	client, err := New(source.URL, &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.List(context.Background())
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want post-policy cross-origin ErrInvalidResponse", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("caller-mutated cross-origin target requests = %d, want 0", got)
	}
}

func TestClientDelegatesSameOriginRedirectToCopiedCallerPolicy(t *testing.T) {
	t.Parallel()

	var callerChecks atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			writeJSON(t, w, validCatalogPage("2026-08-18T09:30:12.123456Z", nil, ""))
			return
		}
		http.Redirect(w, r, server.URL+"/redirected", http.StatusFound)
	}))
	defer server.Close()

	injected := &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			callerChecks.Add(1)
			return nil
		},
	}
	client, err := New(server.URL, injected)
	if err != nil {
		t.Fatal(err)
	}
	injected.CheckRedirect = func(*http.Request, []*http.Request) error {
		t.Fatal("mutated caller redirect policy was used")
		return errors.New("unreachable")
	}

	got, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("List() = %#v, want empty", got)
	}
	if checks := callerChecks.Load(); checks != 1 {
		t.Fatalf("caller redirect checks = %d, want 1", checks)
	}
}

func TestClientPreservesDefaultSameOriginRedirectLimit(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "/redirect-loop", http.StatusFound)
	}))
	defer server.Close()
	client, err := New(server.URL, &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want redirect-loop error")
	}
	if got := requests.Load(); got != 10 {
		t.Fatalf("same-origin redirect requests = %d, want standard limit 10", got)
	}
}

func TestListMapsStatusAndBoundsProblemBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		want       error
		forbidText string
	}{
		{name: "not found", status: http.StatusNotFound, body: `{"code":"NOT_FOUND"}`, want: cloud.ErrNotFound},
		{name: "yanked", status: http.StatusGone, body: `{"code":"YANKED"}`, want: cloud.ErrYanked},
		{name: "bounded server problem", status: http.StatusInternalServerError, body: strings.Repeat("a", 8<<10) + "SECRET_TAIL", want: cloud.ErrInvalidResponse, forbidText: "SECRET_TAIL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.List(context.Background())
			if !errors.Is(err, tt.want) {
				t.Fatalf("List() error = %v, want %v", err, tt.want)
			}
			if tt.forbidText != "" && strings.Contains(err.Error(), tt.forbidText) {
				t.Fatalf("List() error leaked response tail: %v", err)
			}
		})
	}
}

func TestListExposesOnlySafeProblemFields(t *testing.T) {
	t.Parallel()

	const body = `{
		"type":"https://apc.example.com/problems/validation-failed",
		"title":"TOP_SECRET_TITLE",
		"status":422,
		"code":"RESOURCE_VALIDATION_FAILED",
		"detail":"TOP_SECRET_DETAIL",
		"instance":"/TOP_SECRET_INSTANCE",
		"trace_id":"tr_01JABCDEF",
		"errors":[{"message":"TOP_SECRET_ERROR"}]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	client := newRemoteTestClient(t, server.URL)

	_, err := client.List(context.Background())
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want ErrInvalidResponse", err)
	}
	for _, safe := range []string{"HTTP 422", "RESOURCE_VALIDATION_FAILED", "tr_01JABCDEF"} {
		if !strings.Contains(err.Error(), safe) {
			t.Fatalf("List() error = %v, want safe problem field %q", err, safe)
		}
	}
	for _, secret := range []string{"TOP_SECRET_TITLE", "TOP_SECRET_DETAIL", "TOP_SECRET_INSTANCE", "TOP_SECRET_ERROR"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("List() error leaked private problem field %q: %v", secret, err)
		}
	}
}

func TestListDoesNotExposeMalformedOrUnexpectedProblemBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		body        string
		forbidden   []string
	}{
		{
			name:        "unexpected media type",
			contentType: "text/plain",
			body:        "TOP_SECRET_RAW",
			forbidden:   []string{"TOP_SECRET_RAW"},
		},
		{
			name:        "malformed JSON",
			contentType: "application/problem+json",
			body:        `{"code":"TOP_SECRET_MALFORMED"`,
			forbidden:   []string{"TOP_SECRET_MALFORMED"},
		},
		{
			name:        "unsafe stable field",
			contentType: "application/problem+json",
			body:        `{"status":500,"code":"UNSAFE\nCODE","trace_id":"tr_01JABCDEF"}`,
			forbidden:   []string{"UNSAFE", "tr_01JABCDEF"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)

			_, err := client.List(context.Background())
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("List() error = %v, want ErrInvalidResponse", err)
			}
			if !strings.Contains(err.Error(), "HTTP 500") {
				t.Fatalf("List() error = %v, want stable HTTP status", err)
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("List() error leaked problem body field %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestListPrefersContextCancellationDuringResponseBodyRead(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		partialBody string
	}{
		{name: "success body", status: http.StatusOK, contentType: "application/json", partialBody: `{"snapshot_updated_at":"`},
		{name: "problem body", status: http.StatusInternalServerError, contentType: "application/problem+json", partialBody: `{"status":500,"code":"SERVER_`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.partialBody))
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			client := newRemoteTestClient(t, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				_, err := client.List(ctx)
				done <- err
			}()

			<-started
			cancel()
			err := <-done
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("List() error = %v, want context.Canceled", err)
			}
			if errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("List() error = %v, context cancellation must not join ErrInvalidResponse", err)
			}
		})
	}
}

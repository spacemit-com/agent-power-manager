package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/cloud/remote"
)

func clearCloudEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APM_CLOUD_PROVIDER", "")
	t.Setenv("APC_BASE_URL", "")
	t.Setenv("APC_API_KEY", "")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", "")
}

func TestNewSubmitLifecycleRequiresAPIKey(t *testing.T) {
	for _, apiKey := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("key_%q", apiKey), func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APC_API_KEY", apiKey)
			if _, err := newSubmitLifecycle(); err == nil {
				t.Fatal("newSubmitLifecycle() error = nil, want error when APC_API_KEY blank")
			}
		})
	}
}

func TestNewSubmissionQueryLifecycleRequiresAPIKey(t *testing.T) {
	for _, apiKey := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("key_%q", apiKey), func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APC_API_KEY", apiKey)
			if _, err := newSubmissionQueryLifecycle(); err == nil {
				t.Fatal("newSubmissionQueryLifecycle() error = nil, want error when APC_API_KEY blank")
			}
		})
	}
}

func TestNewSubmitLifecycleWithAPIKey(t *testing.T) {
	clearCloudEnvironment(t)
	t.Setenv("APC_API_KEY", "test-key")
	service, err := newSubmitLifecycle()
	if err != nil {
		t.Fatalf("newSubmitLifecycle() error = %v", err)
	}
	if service == nil {
		t.Fatal("newSubmitLifecycle() = nil, want service")
	}
}

func TestNewCloudClientConfigurationContract(t *testing.T) {
	if defaultCloudProvider != cloud.ProviderRemote || defaultAPCBaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("defaults = (%q, %q), want Remote and loopback APC", defaultCloudProvider, defaultAPCBaseURL)
	}
	if cloudProviderEnv != "APM_CLOUD_PROVIDER" || apcBaseURLEnv != "APC_BASE_URL" || localRootEnv != "APM_CLOUD_LOCAL_ROOT" {
		t.Fatalf("environment names = (%q, %q, %q), want documented Cloud variables", cloudProviderEnv, apcBaseURLEnv, localRootEnv)
	}
}

func TestNewCloudClientDefaultsToRemote(t *testing.T) {
	clearCloudEnvironment(t)
	// 未选中的 Local 配置无效时，默认 Remote 仍必须能够独立构造。
	t.Setenv("APM_CLOUD_LOCAL_ROOT", "relative/resources")

	first, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	firstRemote, firstOK := first.(*remote.Client)
	secondRemote, secondOK := second.(*remote.Client)
	if !firstOK || !secondOK {
		t.Fatalf("clients = (%T, %T), want two Remote clients", first, second)
	}
	if firstRemote == secondRemote {
		t.Fatal("newCloudClient() reused a Remote client across calls")
	}
}

func TestNewCloudClientUsesRemoteBaseURLOverride(t *testing.T) {
	clearCloudEnvironment(t)
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.RequestURI()
		if authorization := r.Header.Values("Authorization"); len(authorization) != 0 {
			t.Errorf("anonymous catalog Authorization = %q, want absent", authorization)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"snapshot_updated_at":"2026-08-21T00:00:00Z","items":[],"next_cursor":""}`))
	}))
	defer server.Close()
	t.Setenv("APC_BASE_URL", server.URL)

	client, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("List() = %#v, want empty overridden catalog", refs)
	}
	if requestPath != "/api/v1/resources?limit=100" {
		t.Fatalf("request URI = %q, want overridden APC catalog path", requestPath)
	}
}

func TestNewCloudClientAcceptsSupportedRemoteURLs(t *testing.T) {
	tests := []string{
		"http://127.0.0.1:18080",
		"http://localhost:18080",
		"http://[::1]:18080",
		"https://apc.example.com",
	}
	for _, baseURL := range tests {
		t.Run(baseURL, func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APC_BASE_URL", baseURL)
			client, err := newCloudClient()
			if err != nil {
				t.Fatalf("newCloudClient() error = %v", err)
			}
			if _, ok := client.(*remote.Client); !ok {
				t.Fatalf("client = %T, want Remote", client)
			}
		})
	}
}

func TestNewCloudClientSelectsExplicitLocalRootLazily(t *testing.T) {
	clearCloudEnvironment(t)
	root := t.TempDir()
	t.Setenv("APM_CLOUD_PROVIDER", "local")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", root)
	// 未选中的 Remote 配置无效时，Local 构造不得触发 Remote factory。
	t.Setenv("APC_BASE_URL", "http://apc.example.com")

	client, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.(*local.Client); !ok {
		t.Fatalf("client = %T, want Local", client)
	}
}

func TestNewCloudClientRejectsInvalidLocalRoot(t *testing.T) {
	cleanRoot := t.TempDir()
	uncleanRoot := cleanRoot + string(os.PathSeparator) + ".." + string(os.PathSeparator) + filepath.Base(cleanRoot)
	tests := []struct {
		name string
		root string
	}{
		{name: "missing"},
		{name: "relative", root: "relative/resources"},
		{name: "unclean absolute", root: uncleanRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APM_CLOUD_PROVIDER", "local")
			t.Setenv("APM_CLOUD_LOCAL_ROOT", tt.root)
			client, err := newCloudClient()
			if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("newCloudClient() = (%T, %v), want nil/ErrProviderConfig", client, err)
			}
			if !strings.Contains(err.Error(), "APM_CLOUD_LOCAL_ROOT") {
				t.Fatalf("newCloudClient() error = %q, want Local environment context", err)
			}
		})
	}
}

func TestNewCloudClientRejectsUnknownProvider(t *testing.T) {
	clearCloudEnvironment(t)
	t.Setenv("APM_CLOUD_PROVIDER", "automatic")

	client, err := newCloudClient()
	if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
		t.Fatalf("newCloudClient() = (%T, %v), want nil/ErrProviderConfig", client, err)
	}
	if !strings.Contains(err.Error(), "APM_CLOUD_PROVIDER") {
		t.Fatalf("newCloudClient() error = %q, want provider environment context", err)
	}
}

func TestNewCloudClientDoesNotFallbackAfterRemoteConfigurationFailure(t *testing.T) {
	clearCloudEnvironment(t)
	t.Setenv("APM_CLOUD_PROVIDER", "remote")
	t.Setenv("APC_BASE_URL", "http://apc.example.com")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", t.TempDir())

	client, err := newCloudClient()
	if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
		t.Fatalf("newCloudClient() = (%T, %v), want nil/ErrProviderConfig", client, err)
	}
	if !strings.Contains(err.Error(), "APC_BASE_URL") {
		t.Fatalf("newCloudClient() error = %q, want Remote environment context", err)
	}
}

func TestNewCloudClientRedactsInvalidRemoteBaseURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		forbidden []string
	}{
		{
			name:      "userinfo secret",
			baseURL:   "https://operator:password-secret@apc.example.com",
			forbidden: []string{"operator", "password-secret"},
		},
		{
			name:      "query token",
			baseURL:   "https://apc.example.com?token=query-secret",
			forbidden: []string{"token", "query-secret"},
		},
		{
			name:      "malformed input secret",
			baseURL:   "https://apc.example.com/%zz-malformed-secret",
			forbidden: []string{"zz-malformed-secret"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APM_CLOUD_PROVIDER", "remote")
			t.Setenv("APC_BASE_URL", tt.baseURL)

			client, err := newCloudClient()
			if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("newCloudClient() = (%T, %v), want nil/ErrProviderConfig", client, err)
			}
			message := err.Error()
			if !strings.Contains(message, "APC_BASE_URL") || !strings.Contains(message, cloud.ErrProviderConfig.Error()) {
				t.Fatalf("newCloudClient() error = %q, want environment and provider sentinel context", message)
			}
			for _, secret := range append(tt.forbidden, tt.baseURL) {
				if strings.Contains(message, secret) {
					t.Fatalf("newCloudClient() error = %q, leaked %q", message, secret)
				}
			}
		})
	}
}

func TestNewCloudClientDoesNotFallbackAfterRemoteRequestFailure(t *testing.T) {
	clearCloudEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("APM_CLOUD_PROVIDER", "remote")
	t.Setenv("APC_BASE_URL", server.URL)
	t.Setenv("APM_CLOUD_LOCAL_ROOT", t.TempDir())

	client, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(context.Background()); !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("List() error = %v, want Remote ErrInvalidResponse without Local fallback", err)
	}
}

func TestNewCloudClientRejectsNonPhysicalLocalRoot(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "missing")
	regularFile := filepath.Join(parent, "regular-file")
	if err := os.WriteFile(regularFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	physicalFinal := filepath.Join(parent, "physical-final")
	if err := os.Mkdir(physicalFinal, 0o700); err != nil {
		t.Fatal(err)
	}
	finalLink := filepath.Join(parent, "final-link")
	createTestSymlink(t, physicalFinal, finalLink)
	physicalAncestor := filepath.Join(parent, "physical-ancestor")
	ancestorRoot := filepath.Join(physicalAncestor, "resources")
	if err := os.MkdirAll(ancestorRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	ancestorLink := filepath.Join(parent, "ancestor-link")
	createTestSymlink(t, physicalAncestor, ancestorLink)

	tests := []struct {
		name string
		root string
	}{
		{name: "missing", root: missing},
		{name: "regular file", root: regularFile},
		{name: "terminal symlink", root: finalLink},
		{name: "ancestor symlink", root: filepath.Join(ancestorLink, "resources")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearCloudEnvironment(t)
			t.Setenv("APM_CLOUD_PROVIDER", "local")
			t.Setenv("APM_CLOUD_LOCAL_ROOT", tt.root)

			client, err := newCloudClient()
			if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("newCloudClient() = (%T, %v), want nil/ErrProviderConfig", client, err)
			}
			if !strings.Contains(err.Error(), "APM_CLOUD_LOCAL_ROOT") {
				t.Fatalf("newCloudClient() error = %q, want Local environment context", err)
			}
		})
	}
}

func TestNewCloudClientAcceptsPhysicalLocalRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "resources")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	clearCloudEnvironment(t)
	t.Setenv("APM_CLOUD_PROVIDER", "local")
	t.Setenv("APM_CLOUD_LOCAL_ROOT", root)

	client, err := newCloudClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.(*local.Client); !ok {
		t.Fatalf("client = %T, want Local", client)
	}
}

func TestNewRemoteHTTPClientIsFreshBoundedAndSameOriginOnly(t *testing.T) {
	first := newRemoteHTTPClient()
	second := newRemoteHTTPClient()
	if first == second {
		t.Fatal("newRemoteHTTPClient() reused a client")
	}
	if first.Timeout != 2*time.Minute || second.Timeout != 2*time.Minute {
		t.Fatalf("timeouts = (%v, %v), want 2m", first.Timeout, second.Timeout)
	}

	originalURL, err := url.Parse("https://apc.example.com/catalog")
	if err != nil {
		t.Fatal(err)
	}
	sameOriginURL, err := url.Parse("https://APC.EXAMPLE.COM:443/next")
	if err != nil {
		t.Fatal(err)
	}
	crossOriginURL, err := url.Parse("https://other.example.com/next")
	if err != nil {
		t.Fatal(err)
	}
	via := []*http.Request{{URL: originalURL}}
	if err := first.CheckRedirect(&http.Request{URL: sameOriginURL}, via); err != nil {
		t.Fatalf("same-origin redirect error = %v", err)
	}
	if err := first.CheckRedirect(&http.Request{URL: crossOriginURL}, via); err == nil {
		t.Fatal("cross-origin redirect error = nil")
	}
}

func TestSameOriginOnlyRejectsNilAndCredentialedRedirects(t *testing.T) {
	safeURL := &url.URL{Scheme: "https", Host: "apc.example.com", Path: "/next"}
	credentialedURL := &url.URL{
		Scheme: "https",
		Host:   "apc.example.com",
		Path:   "/next",
		User:   url.UserPassword("operator", "redirect-secret"),
	}
	tests := []struct {
		name    string
		request *http.Request
		via     []*http.Request
	}{
		{name: "nil request", via: []*http.Request{{URL: safeURL}}},
		{name: "nil request URL", request: &http.Request{}, via: []*http.Request{{URL: safeURL}}},
		{name: "request userinfo", request: &http.Request{URL: credentialedURL}, via: []*http.Request{{URL: safeURL}}},
		{name: "missing original", request: &http.Request{URL: safeURL}},
		{name: "nil original", request: &http.Request{URL: safeURL}, via: []*http.Request{nil}},
		{name: "nil original URL", request: &http.Request{URL: safeURL}, via: []*http.Request{{}}},
		{name: "original userinfo", request: &http.Request{URL: safeURL}, via: []*http.Request{{URL: credentialedURL}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := sameOriginOnly(tt.request, tt.via); err == nil {
				t.Fatal("sameOriginOnly() error = nil")
			}
		})
	}
}

func TestSameOriginOnlyTreatsDefaultPortsAsEquivalent(t *testing.T) {
	tests := []struct {
		name     string
		original string
		target   string
	}{
		{name: "implicit HTTP to explicit", original: "http://apc.example.com/start", target: "http://APC.EXAMPLE.COM:80/next"},
		{name: "explicit HTTPS to implicit", original: "https://apc.example.com:443/start", target: "https://APC.EXAMPLE.COM/next"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original, err := url.Parse(tt.original)
			if err != nil {
				t.Fatal(err)
			}
			target, err := url.Parse(tt.target)
			if err != nil {
				t.Fatal(err)
			}
			if err := sameOriginOnly(&http.Request{URL: target}, []*http.Request{{URL: original}}); err != nil {
				t.Fatalf("sameOriginOnly() error = %v", err)
			}
		})
	}
}

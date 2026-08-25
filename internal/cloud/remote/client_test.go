package remote

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"apm/internal/cloud"
)

var _ cloud.Client = (*Client)(nil)

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

func TestNewRedactsInvalidBaseURLInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		baseURL   string
		forbidden []string
	}{
		{
			name:      "malformed escape",
			baseURL:   "https://apc.example.com/%zz-malformed-secret",
			forbidden: []string{"%zz", "malformed-secret"},
		},
		{
			name:      "non-root path",
			baseURL:   "https://apc.example.com/path-secret",
			forbidden: []string{"path-secret"},
		},
		{
			name:      "userinfo",
			baseURL:   "https://user-secret:password-secret@apc.example.com",
			forbidden: []string{"user-secret", "password-secret"},
		},
		{
			name:      "query",
			baseURL:   "https://apc.example.com?token=query-secret",
			forbidden: []string{"token", "query-secret"},
		},
		{
			name:      "fragment",
			baseURL:   "https://apc.example.com#fragment-secret",
			forbidden: []string{"fragment-secret"},
		},
		{
			name:      "unsupported scheme",
			baseURL:   "scheme-secret://apc.example.com",
			forbidden: []string{"scheme-secret"},
		},
		{
			name:      "non-loopback HTTP host",
			baseURL:   "http://host-secret.example.com",
			forbidden: []string{"host-secret"},
		},
		{
			name:      "malformed host",
			baseURL:   "https://[host-secret.example.com",
			forbidden: []string{"host-secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(tt.baseURL, &http.Client{Timeout: time.Second})
			if client != nil || !errors.Is(err, cloud.ErrProviderConfig) {
				t.Fatalf("New() = (%T, %v), want nil/ErrProviderConfig", client, err)
			}
			message := err.Error()
			if !strings.Contains(message, "remote base URL") {
				t.Fatalf("New() error = %q, want safe base URL context", message)
			}
			for _, secret := range append(tt.forbidden, tt.baseURL) {
				if strings.Contains(message, secret) {
					t.Fatalf("New() error = %q, leaked %q", message, secret)
				}
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

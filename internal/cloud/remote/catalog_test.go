package remote

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/domain"
)

const (
	testPublished = "2026-08-18T09:30:12Z"
	testSHA256    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestCatalogVersionRequestsExactAnonymousEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api-server/apc/api/v1/resources/version" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s, want exact GET /api-server/apc/api/v1/resources/version", r.Method, r.URL.RequestURI())
		}
		if values := r.Header.Values("Authorization"); len(values) != 0 {
			t.Errorf("Authorization = %q, want absent", values)
		}
		if values := r.Header.Values("X-API-Key"); len(values) != 0 {
			t.Errorf("X-API-Key = %q, want absent", values)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q, want application/json", accept)
		}
		if r.Body != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("request body = %q, want empty", body)
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, validCatalogVersionJSON(42))
	}))
	defer server.Close()
	client := newRemoteTestClientWithAPIKey(t, server.URL, "submission-secret")

	got, err := client.CatalogVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := domain.CatalogVersion{SHA256: testSHA256, Size: 42}
	if got != want {
		t.Fatalf("CatalogVersion() = %#v, want %#v", got, want)
	}
}

func TestCatalogVersionAcceptsSizeBoundaries(t *testing.T) {
	t.Parallel()

	for _, size := range []int64{0, catalog.MaxCatalogSize} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, validCatalogVersionJSON(size))
			}))
			defer server.Close()

			got, err := newRemoteTestClient(t, server.URL).CatalogVersion(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Size != size {
				t.Fatalf("CatalogVersion().Size = %d, want %d", got.Size, size)
			}
		})
	}
}

func TestCatalogVersionRejectsNon2xxAndInvalidContentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      int
		contentType string
	}{
		{name: "non-2xx", status: http.StatusInternalServerError, contentType: "application/json"},
		{name: "missing content type", status: http.StatusOK},
		{name: "wrong content type", status: http.StatusOK, contentType: "text/plain"},
		{name: "malformed content type parameters", status: http.StatusOK, contentType: "application/json; charset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, validCatalogVersionJSON(1))
			}))
			defer server.Close()

			_, err := newRemoteTestClient(t, server.URL).CatalogVersion(context.Background())
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("CatalogVersion() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestCatalogVersionRejectsInvalidExactSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"schema_version":`},
		{name: "trailing JSON", body: validCatalogVersionJSON(1) + ` {}`},
		{name: "duplicate key", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `","catalog_sha256":"` + testSHA256 + `","catalog_size":1}`},
		{name: "case-folded key", body: `{"SCHEMA_VERSION":1,"catalog_sha256":"` + testSHA256 + `","catalog_size":1}`},
		{name: "unknown key", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `","catalog_size":1,"etag":"opaque"}`},
		{name: "missing schema version", body: `{"catalog_sha256":"` + testSHA256 + `","catalog_size":1}`},
		{name: "missing hash", body: `{"schema_version":1,"catalog_size":1}`},
		{name: "missing size", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `"}`},
		{name: "null schema version", body: `{"schema_version":null,"catalog_sha256":"` + testSHA256 + `","catalog_size":1}`},
		{name: "null hash", body: `{"schema_version":1,"catalog_sha256":null,"catalog_size":1}`},
		{name: "null size", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `","catalog_size":null}`},
		{name: "unsupported schema version", body: `{"schema_version":2,"catalog_sha256":"` + testSHA256 + `","catalog_size":1}`},
		{name: "uppercase hash", body: `{"schema_version":1,"catalog_sha256":"` + strings.ToUpper(testSHA256) + `","catalog_size":1}`},
		{name: "short hash", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256[:63] + `","catalog_size":1}`},
		{name: "non-hex hash", body: `{"schema_version":1,"catalog_sha256":"g` + testSHA256[1:] + `","catalog_size":1}`},
		{name: "negative size", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `","catalog_size":-1}`},
		{name: "oversized", body: validCatalogVersionJSON(catalog.MaxCatalogSize + 1)},
		{name: "fractional size", body: `{"schema_version":1,"catalog_sha256":"` + testSHA256 + `","catalog_size":1.5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			_, err := newRemoteTestClient(t, server.URL).CatalogVersion(context.Background())
			if !errors.Is(err, cloud.ErrInvalidResponse) {
				t.Fatalf("CatalogVersion() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestCatalogVersionPreservesSameOriginAndRejectsCrossOriginRedirects(t *testing.T) {
	t.Run("same origin", func(t *testing.T) {
		var requests atomic.Int32
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if values := r.Header.Values("Authorization"); len(values) != 0 {
				t.Errorf("Authorization = %q, want absent", values)
			}
			if values := r.Header.Values("X-API-Key"); len(values) != 0 {
				t.Errorf("X-API-Key = %q, want absent", values)
			}
			if r.URL.Path == "/api-server/apc/api/v1/resources/version" {
				if r.URL.RawQuery != "" {
					t.Errorf("initial query = %q, want empty", r.URL.RawQuery)
				}
				http.Redirect(w, r, server.URL+"/current-version", http.StatusFound)
				return
			}
			if r.URL.Path != "/current-version" {
				t.Errorf("redirected path = %q, want /current-version", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, validCatalogVersionJSON(1))
		}))
		defer server.Close()

		client := newRemoteTestClientWithAPIKey(t, server.URL, "submission-secret")
		if _, err := client.CatalogVersion(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := requests.Load(); got != 2 {
			t.Fatalf("requests = %d, want initial plus redirect", got)
		}
	})

	t.Run("cross origin HTTP downgrade", func(t *testing.T) {
		var targetRequests atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			targetRequests.Add(1)
		}))
		defer target.Close()
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
		}))
		defer source.Close()

		_, err := newRemoteTestClient(t, source.URL).CatalogVersion(context.Background())
		if !errors.Is(err, cloud.ErrInvalidResponse) {
			t.Fatalf("CatalogVersion() error = %v, want cross-origin HTTP downgrade ErrInvalidResponse", err)
		}
		if got := targetRequests.Load(); got != 0 {
			t.Fatalf("cross-origin target requests = %d, want 0", got)
		}
	})

	t.Run("cross origin HTTPS followed", func(t *testing.T) {
		var targetRequests atomic.Int32
		target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, validCatalogVersionJSON(1))
		}))
		defer target.Close()
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
		}))
		defer source.Close()

		pool := x509.NewCertPool()
		pool.AddCert(target.Certificate())
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		client, err := New(source.URL, &http.Client{Transport: transport, Timeout: 5 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.CatalogVersion(context.Background()); err != nil {
			t.Fatalf("CatalogVersion() error = %v, want cross-origin HTTPS redirect followed", err)
		}
		if got := targetRequests.Load(); got != 1 {
			t.Fatalf("cross-origin HTTPS target requests = %d, want 1", got)
		}
	})
}

func TestDownloadCatalogRequestsExactAnonymousEndpointAndWritesOpaqueBytes(t *testing.T) {
	contents := []byte(`{"opaque":"transport must not decode catalog schema"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/api-server/apc/api/v1/resources" || r.URL.RawQuery != "" {
			t.Errorf("request = %s %s, want exact GET /api-server/apc/api/v1/resources", r.Method, r.URL.RequestURI())
		}
		if values := r.Header.Values("Authorization"); len(values) != 0 {
			t.Errorf("Authorization = %q, want absent", values)
		}
		if values := r.Header.Values("X-API-Key"); len(values) != 0 {
			t.Errorf("X-API-Key = %q, want absent", values)
		}
		if accept := r.Header.Get("Accept"); accept != "application/json" {
			t.Errorf("Accept = %q, want application/json", accept)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(contents)
	}))
	defer server.Close()
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "catalog.json")
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists before call: %v", err)
	}

	client := newRemoteTestClientWithAPIKey(t, server.URL, "submission-secret")
	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("catalog contents = %q, want exact opaque bytes %q", got, contents)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("catalog mode = %04o, want 0600", mode)
	}
}

func TestDownloadCatalogRejectsProtocolAndSizeFailuresWithoutDestinationMarker(t *testing.T) {
	contents := "opaque catalog bytes"
	remoteReadErr := errors.New("remote catalog read failed")
	tests := []struct {
		name          string
		status        int
		contentType   string
		contentLength int64
		body          io.Reader
		want          error
	}{
		{name: "non-2xx", status: http.StatusInternalServerError, contentType: "application/json", contentLength: 0, body: strings.NewReader(""), want: cloud.ErrInvalidResponse},
		{name: "missing content type", status: http.StatusOK, contentLength: int64(len(contents)), body: strings.NewReader(contents), want: cloud.ErrInvalidResponse},
		{name: "wrong content type", status: http.StatusOK, contentType: "text/plain", contentLength: int64(len(contents)), body: strings.NewReader(contents), want: cloud.ErrInvalidResponse},
		{name: "malformed content type", status: http.StatusOK, contentType: "application/json; charset", contentLength: int64(len(contents)), body: strings.NewReader(contents), want: cloud.ErrInvalidResponse},
		{name: "Content-Length over maximum", status: http.StatusOK, contentType: "application/json", contentLength: catalog.MaxCatalogSize + 1, body: strings.NewReader(""), want: cloud.ErrInvalidResponse},
		{name: "short body", status: http.StatusOK, contentType: "application/json", contentLength: int64(len(contents) + 1), body: strings.NewReader(contents), want: cloud.ErrIntegrity},
		{name: "short body unexpected EOF", status: http.StatusOK, contentType: "application/json", contentLength: int64(len(contents)), body: io.MultiReader(strings.NewReader("partial"), readerError{err: io.ErrUnexpectedEOF}), want: cloud.ErrIntegrity},
		{name: "trailing body", status: http.StatusOK, contentType: "application/json", contentLength: int64(len(contents)), body: strings.NewReader(contents + "!"), want: cloud.ErrIntegrity},
		{name: "body read error", status: http.StatusOK, contentType: "application/json", contentLength: int64(len(contents)), body: io.MultiReader(strings.NewReader("partial"), readerError{err: remoteReadErr}), want: remoteReadErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
				return catalogHTTPResponse(request, tt.status, tt.contentType, tt.contentLength, tt.body)
			})
			parent := trustedTestDir(t)
			sibling := filepath.Join(parent, "keep.txt")
			if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(parent, "catalog.json")

			err := client.DownloadCatalog(context.Background(), destination)
			if !errors.Is(err, tt.want) {
				t.Fatalf("DownloadCatalog() error = %v, want %v", err, tt.want)
			}
			if errors.Is(err, cloud.ErrCatalogDestination) {
				t.Fatalf("DownloadCatalog() error = %v, remote/protocol/content failure must not be ErrCatalogDestination", err)
			}
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("destination remains after failure: %v", statErr)
			}
			kept, readErr := os.ReadFile(sibling)
			if readErr != nil || string(kept) != "keep" {
				t.Fatalf("unrelated sibling = %q, %v; want preserved", kept, readErr)
			}
		})
	}
}

func TestDownloadCatalogRequiresSingleExplicitContentLengthHeader(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		headerValues  []string
		contentLength int64
		body          string
		wantErr       bool
	}{
		{name: "explicit zero", status: http.StatusOK, headerValues: []string{"0"}, contentLength: 0},
		{name: "multiple", status: http.StatusOK, headerValues: []string{"1", "1"}, contentLength: 1, body: "x", wantErr: true},
		{name: "signed decimal", status: http.StatusOK, headerValues: []string{"+1"}, contentLength: 1, body: "x", wantErr: true},
		{name: "comma joined", status: http.StatusOK, headerValues: []string{"1, 1"}, contentLength: 1, body: "x", wantErr: true},
		{name: "differs from parsed response", status: http.StatusOK, headerValues: []string{"2"}, contentLength: 1, body: "x", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
				response := catalogHTTPResponse(request, tt.status, "application/json", tt.contentLength, strings.NewReader(tt.body))
				response.Header.Del("Content-Length")
				for _, value := range tt.headerValues {
					response.Header.Add("Content-Length", value)
				}
				return response
			})
			destination := filepath.Join(trustedTestDir(t), "catalog.json")

			err := client.DownloadCatalog(context.Background(), destination)
			if !tt.wantErr {
				if err != nil {
					t.Fatal(err)
				}
				info, statErr := os.Stat(destination)
				if statErr != nil || info.Size() != 0 || info.Mode().Perm() != 0o600 {
					t.Fatalf("explicit zero destination = %+v, %v; want empty 0600 file", info, statErr)
				}
				return
			}
			if !errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrCatalogDestination) {
				t.Fatalf("DownloadCatalog() error = %v, want only raw Content-Length ErrInvalidResponse", err)
			}
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("destination exists after invalid Content-Length: %v", statErr)
			}
		})
	}
}

func TestDownloadCatalogToleratesMissingContentLength(t *testing.T) {
	// 测试环境 CDN 经 HTTP/2 返回 catalog 时不带 Content-Length 头；
	// 客户端应以 MaxCatalogSize 为上界读取并接受完整字节。
	contents := "opaque catalog bytes without Content-Length"
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		return catalogHTTPResponse(request, http.StatusOK, "application/json", -1, strings.NewReader(contents))
	})
	destination := filepath.Join(trustedTestDir(t), "catalog.json")

	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatalf("DownloadCatalog() error = %v, want success without Content-Length", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != contents {
		t.Fatalf("catalog contents = %q, want exact %q", got, contents)
	}
}

func TestDownloadCatalogCancellationAfterCopyPrecedesIntegrity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		body := &cancelAtEOFReader{data: []byte("short"), cancel: cancel}
		return catalogHTTPResponse(request, http.StatusOK, "application/json", 6, body)
	})
	destination := filepath.Join(trustedTestDir(t), "catalog.json")

	err := client.DownloadCatalog(ctx, destination)
	if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("DownloadCatalog() error = %v, want post-copy context.Canceled before integrity", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after post-copy cancellation: %v", statErr)
	}
}

func TestDownloadCatalogJoinsShortStreamWithAllCloseFailures(t *testing.T) {
	outputErr := errors.New("catalog output close failed")
	bodyErr := errors.New("catalog response close failed")
	parentErr := errors.New("catalog parent close failed")
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		return catalogHTTPResponse(request, http.StatusOK, "application/json", 6, strings.NewReader("short"))
	})
	destination := filepath.Join(trustedTestDir(t), "catalog.json")

	err := client.downloadCatalogWithHooks(context.Background(), destination, catalogDownloadHooks{
		closeOutput: func(file *os.File) error {
			return errors.Join(file.Close(), outputErr)
		},
		closeResponse: func(_ context.Context, response *http.Response) error {
			return errors.Join(response.Body.Close(), bodyErr)
		},
		closeParent: func(parent *pinnedDirectory) error {
			return errors.Join(parent.close(), parentErr)
		},
	})
	for _, want := range []error{cloud.ErrIntegrity, cloud.ErrCatalogDestination, outputErr, bodyErr, parentErr} {
		if !errors.Is(err, want) {
			t.Errorf("DownloadCatalog() error = %v, want identity %v", err, want)
		}
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after joined failures: %v", statErr)
	}
}

func TestDownloadCatalogRejectsDestinationNameSubstitution(t *testing.T) {
	contents := []byte("opaque catalog")
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		return catalogHTTPResponse(request, http.StatusOK, "application/json", int64(len(contents)), strings.NewReader(string(contents)))
	})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "catalog.json")
	ownedSibling := filepath.Join(parent, "catalog-owned")
	substitute := []byte("caller-owned substitute")

	err := client.downloadCatalogWithHooks(context.Background(), destination, catalogDownloadHooks{
		beforeFinalize: func(*stagedContent) {
			if renameErr := os.Rename(destination, ownedSibling); renameErr != nil {
				t.Fatal(renameErr)
			}
			if writeErr := os.WriteFile(destination, substitute, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if !errors.Is(err, cloud.ErrCatalogDestination) || !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadCatalog() error = %v, want destination substitution classification", err)
	}
	gotSubstitute, readErr := os.ReadFile(destination)
	if readErr != nil || string(gotSubstitute) != string(substitute) {
		t.Fatalf("substitute = %q, %v; want preserved %q", gotSubstitute, readErr, substitute)
	}
	gotOwned, readErr := os.ReadFile(ownedSibling)
	if readErr != nil || string(gotOwned) != string(contents) {
		t.Fatalf("owned sibling = %q, %v; want retained downloaded bytes %q", gotOwned, readErr, contents)
	}
}

func TestCatalogRedirectRejectsCallerCredentialInjection(t *testing.T) {
	credentialHeaders := []string{"Authorization", "Proxy-Authorization", "Cookie", "X-API-Key"}
	operations := []struct {
		name string
		run  func(context.Context, *Client, string) error
	}{
		{
			name: "version",
			run: func(ctx context.Context, client *Client, _ string) error {
				_, err := client.CatalogVersion(ctx)
				return err
			},
		},
		{
			name: "catalog",
			run: func(ctx context.Context, client *Client, destination string) error {
				return client.DownloadCatalog(ctx, destination)
			},
		},
	}
	for _, operation := range operations {
		for _, credentialHeader := range credentialHeaders {
			t.Run(operation.name+"/"+credentialHeader, func(t *testing.T) {
				var redirectedRequests atomic.Int32
				var server *httptest.Server
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/redirected-") {
						redirectedRequests.Add(1)
						if strings.HasSuffix(r.URL.Path, "version") {
							w.Header().Set("Content-Type", "application/json")
							_, _ = io.WriteString(w, validCatalogVersionJSON(1))
							return
						}
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Content-Length", "1")
						_, _ = io.WriteString(w, "x")
						return
					}
					target := "/redirected-catalog"
					if strings.HasSuffix(r.URL.Path, "/version") {
						target = "/redirected-version"
					}
					http.Redirect(w, r, server.URL+target, http.StatusFound)
				}))
				defer server.Close()
				client, err := New(server.URL, &http.Client{
					Timeout: time.Second,
					CheckRedirect: func(request *http.Request, _ []*http.Request) error {
						request.Header.Set(credentialHeader, "injected-secret")
						*request = *request.WithContext(context.Background())
						return nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				destination := filepath.Join(trustedTestDir(t), "catalog.json")

				err = operation.run(context.Background(), client, destination)
				if !errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrCatalogDestination) {
					t.Fatalf("%s error = %v, want anonymous redirect ErrInvalidResponse", operation.name, err)
				}
				if got := redirectedRequests.Load(); got != 0 {
					t.Fatalf("redirected requests = %d, want credential rejected before target", got)
				}
				if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("catalog destination exists after redirect credential rejection: %v", statErr)
				}
			})
		}
	}
}

func TestDownloadCatalogAcceptsExactMaximumSize(t *testing.T) {
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		body := io.LimitReader(repeatedByteReader('x'), catalog.MaxCatalogSize)
		return catalogHTTPResponse(request, http.StatusOK, "application/json", catalog.MaxCatalogSize, body)
	})
	destination := filepath.Join(trustedTestDir(t), "catalog.json")

	if err := client.DownloadCatalog(context.Background(), destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != catalog.MaxCatalogSize {
		t.Fatalf("catalog size = %d, want %d", info.Size(), catalog.MaxCatalogSize)
	}
}

func TestDownloadCatalogRejectsPreexistingOrInvalidDestinationBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
		requests.Add(1)
		return catalogHTTPResponse(request, http.StatusOK, "application/json", 0, strings.NewReader(""))
	})
	parent := trustedTestDir(t)
	preexisting := filepath.Join(parent, "catalog.json")
	if err := os.WriteFile(preexisting, []byte("existing"), 0o640); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		destination string
	}{
		{name: "preexisting", destination: preexisting},
		{name: "relative", destination: "catalog.json"},
		{name: "unclean", destination: parent + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "catalog-new.json"},
		{name: "missing parent", destination: filepath.Join(parent, "missing", "catalog.json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.DownloadCatalog(context.Background(), tt.destination)
			if !errors.Is(err, cloud.ErrCatalogDestination) {
				t.Fatalf("DownloadCatalog() error = %v, want ErrCatalogDestination", err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want zero before destination validation", got)
	}
	contents, err := os.ReadFile(preexisting)
	if err != nil || string(contents) != "existing" {
		t.Fatalf("preexisting destination = %q, %v; want unchanged", contents, err)
	}
}

func TestDownloadCatalogPreservesCancellationAndTransportErrorWithoutDestinationMarker(t *testing.T) {
	t.Run("pre-cancelled", func(t *testing.T) {
		var requests atomic.Int32
		client := newCatalogTransportClient(t, func(request *http.Request) *http.Response {
			requests.Add(1)
			return catalogHTTPResponse(request, http.StatusOK, "application/json", 0, strings.NewReader(""))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		destination := filepath.Join(trustedTestDir(t), "catalog.json")

		err := client.DownloadCatalog(ctx, destination)
		if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrCatalogDestination) {
			t.Fatalf("DownloadCatalog() error = %v, want only context.Canceled classification", err)
		}
		if got := requests.Load(); got != 0 {
			t.Fatalf("requests = %d, want 0", got)
		}
		if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("destination exists after cancellation: %v", statErr)
		}
	})

	t.Run("transport", func(t *testing.T) {
		transportErr := errors.New("catalog transport failed")
		client, err := New("https://apc.example.com", &http.Client{
			Timeout: time.Second,
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, transportErr
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(trustedTestDir(t), "catalog.json")

		err = client.DownloadCatalog(context.Background(), destination)
		if !errors.Is(err, transportErr) || errors.Is(err, cloud.ErrCatalogDestination) {
			t.Fatalf("DownloadCatalog() error = %v, want only transport identity", err)
		}
		if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("destination exists after transport failure: %v", statErr)
		}
	})
}

func TestDownloadCatalogRejectsCrossOriginRedirectAndCleansDestination(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer source.Close()
	destination := filepath.Join(trustedTestDir(t), "catalog.json")

	err := newRemoteTestClient(t, source.URL).DownloadCatalog(context.Background(), destination)
	if !errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrCatalogDestination) {
		t.Fatalf("DownloadCatalog() error = %v, want only cross-origin ErrInvalidResponse", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("cross-origin target requests = %d, want 0", got)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after redirect failure: %v", statErr)
	}
}

func validCatalogVersionJSON(size int64) string {
	return fmt.Sprintf(`{"schema_version":1,"catalog_sha256":"%s","catalog_size":%d}`, testSHA256, size)
}

func newRemoteTestClientWithAPIKey(t *testing.T, baseURL, apiKey string) *Client {
	t.Helper()
	client, err := NewWithAPIKey(baseURL, &http.Client{Timeout: 5 * time.Second}, apiKey)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func newCatalogTransportClient(t *testing.T, response func(*http.Request) *http.Response) *Client {
	t.Helper()
	client, err := New("https://apc.example.com", &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.EscapedPath() != "/api-server/apc/api/v1/resources" || request.URL.RawQuery != "" {
				t.Errorf("request = %s %s, want exact GET /api-server/apc/api/v1/resources", request.Method, request.URL.RequestURI())
			}
			if values := request.Header.Values("Authorization"); len(values) != 0 {
				t.Errorf("Authorization = %q, want absent", values)
			}
			return response(request), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func catalogHTTPResponse(request *http.Request, status int, contentType string, contentLength int64, body io.Reader) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	if contentLength >= 0 {
		header.Set("Content-Length", fmt.Sprint(contentLength))
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(body),
		ContentLength: contentLength,
		Request:       request,
	}
}

type readerError struct {
	err error
}

func (readerError readerError) Read([]byte) (int, error) {
	return 0, readerError.err
}

type cancelAtEOFReader struct {
	data   []byte
	cancel context.CancelFunc
}

func (reader *cancelAtEOFReader) Read(buffer []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := copy(buffer, reader.data)
	reader.data = reader.data[count:]
	if len(reader.data) == 0 {
		reader.cancel()
		return count, io.EOF
	}
	return count, nil
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

func TestEndpointEscapesNameAndVersionSegments(t *testing.T) {
	client, err := New("https://apc.example.com", &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.endpoint("api", "v1", "resources", "skill", "my skill/name", "versions", "v1.0.0", "content")
	const wantRawPath = "/api-server/apc/api/v1/resources/skill/my%20skill%2Fname/versions/v1.0.0/content"
	if endpoint.RawPath != wantRawPath {
		t.Fatalf("endpoint RawPath = %q, want %q", endpoint.RawPath, wantRawPath)
	}
	if strings.Contains(endpoint.RawPath, " ") || strings.Contains(endpoint.RawPath, "my skill/name") {
		t.Fatalf("endpoint RawPath = %q contains unescaped name characters", endpoint.RawPath)
	}
}

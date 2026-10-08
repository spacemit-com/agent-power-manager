package remote

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
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

	"apm/internal/cloud"
	"apm/internal/domain"
)

var (
	remoteAgentRef = domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo-agent", Version: "v1.2.3"}
	remoteMCPRef   = domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v1.2.3"}
)

func TestVerifyCatalogEntryContentAcceptsExactBytesWithKnownOrUnknownLength(t *testing.T) {
	contents := []byte("schema_version: 1\nopaque:  value\n")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	for _, contentLength := range []int64{int64(len(contents)), -1} {
		t.Run(fmt.Sprint(contentLength), func(t *testing.T) {
			response := contentVerificationResponse(entry.ContentType, contentLength, bytes.NewReader(contents))
			var destination bytes.Buffer

			if err := verifyCatalogEntryContent(context.Background(), response, entry, &destination); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(destination.Bytes(), contents) {
				t.Fatalf("verified contents = %q, want exact %q", destination.Bytes(), contents)
			}
		})
	}
}

func TestVerifyCatalogEntryContentAcceptsLegacyXGzipAlias(t *testing.T) {
	contents := []byte("schema_version: 1\nopaque:  value\n")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	response := contentVerificationResponse("application/x-gzip", int64(len(contents)), bytes.NewReader(contents))
	var destination bytes.Buffer

	if err := verifyCatalogEntryContent(context.Background(), response, entry, &destination); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(destination.Bytes(), contents) {
		t.Fatalf("verified contents = %q, want exact %q", destination.Bytes(), contents)
	}
}

func TestVerifyCatalogEntryContentRejectsProtocolAndIntegrityFailures(t *testing.T) {
	contents := []byte("verified entry bytes")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	tests := []struct {
		name          string
		contentType   string
		contentLength int64
		body          string
		contentMD5    string
		want          error
	}{
		{name: "missing content type", contentLength: int64(len(contents)), body: string(contents), want: cloud.ErrInvalidResponse},
		{name: "wrong content type", contentType: "text/plain", contentLength: int64(len(contents)), body: string(contents), want: cloud.ErrInvalidResponse},
		{name: "content type parameters", contentType: "application/yaml; charset=utf-8", contentLength: int64(len(contents)), body: string(contents), want: cloud.ErrInvalidResponse},
		{name: "malformed content type", contentType: "application/yaml; charset", contentLength: int64(len(contents)), body: string(contents), want: cloud.ErrInvalidResponse},
		{name: "invalid negative Content-Length", contentType: entry.ContentType, contentLength: -2, body: string(contents), want: cloud.ErrInvalidResponse},
		{name: "Content-Length mismatch", contentType: entry.ContentType, contentLength: int64(len(contents) + 1), body: string(contents), want: cloud.ErrIntegrity},
		{name: "short actual bytes", contentType: entry.ContentType, contentLength: int64(len(contents)), body: string(contents[:4]), want: cloud.ErrIntegrity},
		{name: "trailing actual bytes", contentType: entry.ContentType, contentLength: int64(len(contents)), body: string(contents) + "!", want: cloud.ErrIntegrity},
		{name: "MD5 mismatch", contentType: entry.ContentType, contentLength: int64(len(contents)), body: string(contents), contentMD5: md5Hex([]byte("different")), want: cloud.ErrIntegrity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testEntry := entry
			if tt.contentMD5 != "" {
				testEntry.ContentMD5 = tt.contentMD5
			}
			response := contentVerificationResponse(tt.contentType, tt.contentLength, strings.NewReader(tt.body))

			err := verifyCatalogEntryContent(context.Background(), response, testEntry, io.Discard)
			if !errors.Is(err, tt.want) {
				t.Fatalf("verifyCatalogEntryContent() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyCatalogEntryContentPreservesCancellation(t *testing.T) {
	contents := []byte("verified entry bytes")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := contentVerificationResponse(entry.ContentType, int64(len(contents)), bytes.NewReader(contents))

	err := verifyCatalogEntryContent(ctx, response, entry, io.Discard)
	if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("verifyCatalogEntryContent() error = %v, want only context.Canceled classification", err)
	}
}

func TestVerifyCatalogEntryContentClassifiesUnexpectedEOFAsIntegrity(t *testing.T) {
	contents := []byte("verified entry bytes")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	response := contentVerificationResponse(
		entry.ContentType,
		entry.ContentSize,
		io.MultiReader(strings.NewReader("partial"), readerError{err: io.ErrUnexpectedEOF}),
	)

	err := verifyCatalogEntryContent(context.Background(), response, entry, io.Discard)
	if !errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("verifyCatalogEntryContent() error = %v, want short-stream ErrIntegrity", err)
	}
}

func TestVerifyCatalogEntryContentCancellationAfterCopyPrecedesIntegrity(t *testing.T) {
	contents := []byte("verified entry bytes")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	ctx, cancel := context.WithCancel(context.Background())
	response := contentVerificationResponse(
		entry.ContentType,
		entry.ContentSize,
		&cancelAtEOFReader{data: []byte("short"), cancel: cancel},
	)

	err := verifyCatalogEntryContent(ctx, response, entry, io.Discard)
	if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("verifyCatalogEntryContent() error = %v, want post-copy context.Canceled before integrity", err)
	}
}

func validRemoteCatalogEntry(ref domain.PowerRef, contents []byte) domain.CatalogEntry {
	return domain.CatalogEntry{
		Ref:         ref,
		Description: "Remote fixture",
		Message:     "published fixture",
		ContentType: expectedContentTypeForTest(ref.Type),
		ContentSize: int64(len(contents)),
		ContentMD5:  md5Hex(contents),
		PublishedAt: testPublished,
		Tags:        []string{},
	}
}

func contentVerificationResponse(contentType string, contentLength int64, body io.Reader) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        header,
		Body:          io.NopCloser(body),
		ContentLength: contentLength,
	}
}

func TestDownloadVerifiedContentStagesExactBytesWithPrivateMode(t *testing.T) {
	contents := []byte("schema_version: 1\nopaque:  value\n")
	var requests []string
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	directory := openTestPinnedDirectory(t, trustedTestDir(t))
	temporary, err := client.downloadVerifiedContent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), "application/gzip", directory, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = temporary.remove() })

	opened, err := temporary.openRead()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(opened)
	if err != nil {
		_ = opened.Close()
		t.Fatal(err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("temporary contents = %q, want exact %q", got, contents)
	}
	info, err := opened.Stat()
	if err != nil {
		_ = opened.Close()
		t.Fatal(err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("temporary mode = %o, want 600", mode)
	}
	wantRequests := []string{"/api-server/apc/api/v1/resources/agent/demo-agent/versions/v1.2.3/content"}
	if fmt.Sprint(requests) != fmt.Sprint(wantRequests) {
		t.Fatalf("requests = %v, want catalog-entry content only %v", requests, wantRequests)
	}
}

func TestDownloadVerifiedContentUsesCatalogEntryWithoutMetadataRequest(t *testing.T) {
	contents := []byte("schema_version: 1\nagent: {id: demo-agent, version: v1.2.3, description: demo}\n")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	var paths []string
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if !strings.HasSuffix(r.URL.Path, "/content") {
			http.Error(w, "metadata lookup forbidden", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", entry.ContentType)
		_, _ = w.Write(contents)
	})
	directory := openTestPinnedDirectory(t, trustedTestDir(t))

	staged, err := client.downloadVerifiedContent(context.Background(), entry, entry.ContentType, directory, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staged.remove() })
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/content") {
		t.Fatalf("request paths = %v, want one exact content request and zero metadata requests", paths)
	}
}

func TestDownloadVerifiedContentRejectsIntegrityFailuresAndCleansTemporaryFile(t *testing.T) {
	contents := []byte("verified bytes")
	tests := []struct {
		name  string
		serve func(*testing.T, http.ResponseWriter, *http.Request)
	}{
		{
			name: "Content-Length differs from metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/gzip")
					w.Header().Set("Content-Length", "99")
					return
				}
				rejectRemovedMetadata(t, w)
			},
		},
		{
			name: "actual bytes shorter than metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/gzip")
					w.Header().Set("Content-Length", fmt.Sprint(len(contents)))
					_, _ = w.Write(contents[:4])
					return
				}
				rejectRemovedMetadata(t, w)
			},
		},
		{
			name: "actual bytes longer than metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/gzip")
					w.(http.Flusher).Flush()
					_, _ = w.Write(append(append([]byte(nil), contents...), '!'))
					return
				}
				rejectRemovedMetadata(t, w)
			},
		},
		{
			name: "MD5 differs from metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/gzip")
					_, _ = w.Write([]byte("tampered bytes"))
					return
				}
				rejectRemovedMetadata(t, w)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := trustedTestDir(t)
			directory := openTestPinnedDirectory(t, parent)
			client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
				tt.serve(t, w, r)
			})
			_, err := client.downloadVerifiedContent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), "application/gzip", directory, 0o600)
			if !errors.Is(err, cloud.ErrIntegrity) {
				t.Fatalf("downloadVerifiedContent() error = %v, want ErrIntegrity", err)
			}
			assertDirectoryEmpty(t, parent)
		})
	}
}

func TestDownloadVerifiedContentRejectsInvalidCatalogEntryAndProtocol(t *testing.T) {
	contents := []byte("verified bytes")
	tests := []struct {
		name     string
		mutate   func(*domain.CatalogEntry)
		content  func(http.ResponseWriter)
		wantHits int32
		want     error
	}{
		{name: "uppercase MD5", mutate: func(entry *domain.CatalogEntry) { entry.ContentMD5 = strings.ToUpper(entry.ContentMD5) }, want: cloud.ErrInvalidResponse},
		{name: "invalid MD5", mutate: func(entry *domain.CatalogEntry) { entry.ContentMD5 = "not-an-md5" }, want: cloud.ErrInvalidResponse},
		{name: "over 20 MiB metadata", mutate: func(entry *domain.CatalogEntry) { entry.ContentSize = (20 << 20) + 1 }, want: cloud.ErrUnsafeContent},
		{
			name: "wrong response media type",
			content: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write(contents)
			},
			wantHits: 1,
			want:     cloud.ErrInvalidResponse,
		},
		{
			name: "response media type parameters",
			content: func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
				_, _ = w.Write(contents)
			},
			wantHits: 1,
			want:     cloud.ErrInvalidResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := validRemoteCatalogEntry(remoteAgentRef, contents)
			if tt.mutate != nil {
				tt.mutate(&entry)
			}
			var contentHits atomic.Int32
			parent := trustedTestDir(t)
			directory := openTestPinnedDirectory(t, parent)
			client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					contentHits.Add(1)
					tt.content(w)
					return
				}
				http.Error(w, "metadata lookup forbidden", http.StatusTeapot)
			})
			_, err := client.downloadVerifiedContent(context.Background(), entry, "application/gzip", directory, 0o600)
			if !errors.Is(err, tt.want) {
				t.Fatalf("downloadVerifiedContent() error = %v, want %v", err, tt.want)
			}
			if got := contentHits.Load(); got != tt.wantHits {
				t.Fatalf("content requests = %d, want %d", got, tt.wantHits)
			}
			assertDirectoryEmpty(t, parent)
		})
	}
}

func TestDownloadVerifiedContentClassifiesContentLengthSentinels(t *testing.T) {
	contents := []byte("verified bytes")
	tests := []struct {
		name          string
		contentLength int64
		want          error
	}{
		{name: "unknown minus one", contentLength: -1},
		{name: "invalid negative", contentLength: -2, want: cloud.ErrInvalidResponse},
		{name: "nonnegative mismatch", contentLength: int64(len(contents) + 1), want: cloud.ErrIntegrity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := validRemoteCatalogEntry(remoteAgentRef, contents)
			client, err := New("https://apc.example.com", &http.Client{
				Timeout: time.Second,
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					body := string(contents)
					contentType := "application/gzip"
					contentLength := tt.contentLength
					return &http.Response{
						StatusCode:    http.StatusOK,
						Header:        http.Header{"Content-Type": []string{contentType}},
						Body:          io.NopCloser(strings.NewReader(body)),
						ContentLength: contentLength,
						Request:       request,
					}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			parent := trustedTestDir(t)
			directory := openTestPinnedDirectory(t, parent)
			temporary, err := client.downloadVerifiedContent(context.Background(), entry, "application/gzip", directory, 0o600)
			if tt.want == nil {
				if err != nil {
					t.Fatal(err)
				}
				if removeErr := temporary.remove(); removeErr != nil {
					t.Fatal(removeErr)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("downloadVerifiedContent() error = %v, want %v", err, tt.want)
			}
			assertDirectoryEmpty(t, parent)
		})
	}
}

func TestDownloadVerifiedContentPreservesCancellationAndTransportErrors(t *testing.T) {
	t.Run("cancelled before request", func(t *testing.T) {
		client := newRemoteTestClient(t, "http://127.0.0.1:1")
		entry := validRemoteCatalogEntry(remoteAgentRef, []byte("x"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		directory := openTestPinnedDirectory(t, trustedTestDir(t))
		_, err := client.downloadVerifiedContent(ctx, entry, "application/gzip", directory, 0o600)
		if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrIntegrity) {
			t.Fatalf("downloadVerifiedContent() error = %v, want only context.Canceled classification", err)
		}
	})

	t.Run("cancelled while streaming", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/content") {
				rejectRemovedMetadata(t, w)
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("0"))
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
		}))
		defer server.Close()
		client := newRemoteTestClient(t, server.URL)
		entry := validRemoteCatalogEntry(remoteAgentRef, []byte("0123456789"))
		parent := trustedTestDir(t)
		directory := openTestPinnedDirectory(t, parent)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := client.downloadVerifiedContent(ctx, entry, "application/gzip", directory, 0o600)
			done <- err
		}()
		<-started
		cancel()
		err := <-done
		if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrIntegrity) {
			t.Fatalf("downloadVerifiedContent() error = %v, want only context.Canceled classification", err)
		}
		assertDirectoryEmpty(t, parent)
	})

	t.Run("content transport", func(t *testing.T) {
		transportErr := errors.New("content transport failed")
		entry := validRemoteCatalogEntry(remoteAgentRef, []byte("x"))
		client, err := New("https://apc.example.com", &http.Client{
			Timeout: time.Second,
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/content") {
					return nil, transportErr
				}
				return nil, errors.New("unexpected non-content request")
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		directory := openTestPinnedDirectory(t, trustedTestDir(t))
		_, err = client.downloadVerifiedContent(context.Background(), entry, "application/gzip", directory, 0o600)
		if !errors.Is(err, transportErr) || errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrIntegrity) {
			t.Fatalf("downloadVerifiedContent() error = %v, want only transport identity", err)
		}
	})
}

func newContentClient(t *testing.T, _ domain.PowerRef, _ []byte, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newRemoteTestClient(t, server.URL)
}

func serveValidContent(t *testing.T, w http.ResponseWriter, r *http.Request, ref domain.PowerRef, contents []byte) {
	t.Helper()
	if strings.HasSuffix(r.URL.Path, "/content") {
		w.Header().Set("Content-Type", expectedContentTypeForTest(ref.Type))
		_, _ = w.Write(contents)
		return
	}
	rejectRemovedMetadata(t, w)
}

func expectedContentTypeForTest(powerType domain.PowerType) string {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		return "application/gzip"
	case domain.PowerTypeMCP:
		return "application/json"
	case domain.PowerTypeExpert:
		return "application/gzip"
	default:
		return ""
	}
}

func rejectRemovedMetadata(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	http.Error(w, "resource metadata endpoint is removed", http.StatusNotFound)
}

func md5Hex(contents []byte) string {
	digest := md5.Sum(contents)
	return hex.EncodeToString(digest[:])
}

func assertDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory %q contains temporary residue: %v", directory, entries)
	}
}

func openTestPinnedDirectory(t *testing.T, path string) *pinnedDirectory {
	t.Helper()
	directory, err := openPinnedDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := directory.close(); err != nil {
			t.Errorf("close pinned test directory: %v", err)
		}
	})
	return directory
}

func TestTrustedTestDirUsesTestingTempRoot(t *testing.T) {
	fakeRoot := t.TempDir()
	if err := os.Chmod(fakeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	missingHome := filepath.Join(fakeRoot, "missing-home")
	var root string
	t.Run("create", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		t.Setenv("HOME", missingHome)
		t.Setenv("GOTMPDIR", fakeRoot)
		t.Setenv("TMPDIR", fakeRoot)
		root = trustedTestDir(t)
		assertPathWithinTestRoot(t, fakeRoot, root)
	})
	if root == "" {
		return
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trusted test root remains after cleanup: %v", err)
	}
}

func assertPathWithinTestRoot(t *testing.T, root, path string) {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("test path %q is outside fake root %q", path, root)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o700 {
		t.Fatalf("test path permissions = %04o, want 0700", permissions)
	}
}

func trustedTestDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", directory)
	t.Setenv("XDG_RUNTIME_DIR", directory)
	t.Setenv("TMPDIR", directory)
	return directory
}

func TestCatalogEntryContentTypeMapping(t *testing.T) {
	cases := []struct {
		name string
		ref  domain.PowerRef
		want string
	}{
		{name: "skill", ref: domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo-skill", Version: "v1.0.0"}, want: "application/gzip"},
		{name: "plugin", ref: domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.0.0"}, want: "application/gzip"},
		{name: "mcp", ref: domain.PowerRef{Type: domain.PowerTypeMCP, Name: "demo-mcp", Version: "v1.0.0"}, want: "application/json"},
		{name: "agent", ref: domain.PowerRef{Type: domain.PowerTypeExpert, Name: "demo-agent", Version: "v1.0.0"}, want: "application/gzip"},
	}
	contents := []byte("opaque content")
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			entry := validRemoteCatalogEntry(tt.ref, contents)
			if entry.ContentType != tt.want {
				t.Fatalf("ContentType = %q, want %q", entry.ContentType, tt.want)
			}
			if err := entry.Validate(); err != nil {
				t.Fatalf("Validate() with correct content type = %v", err)
			}
			wrong := entry
			if tt.want == "application/json" {
				wrong.ContentType = "application/gzip"
			} else {
				wrong.ContentType = "application/json"
			}
			if err := wrong.Validate(); err == nil {
				t.Fatalf("Validate() with content type %q = nil, want mismatch error", wrong.ContentType)
			}
		})
	}
}

func TestDownloadVerifiedContentNeverSendsSubmissionCredentials(t *testing.T) {
	contents := []byte("schema_version: 1\nopaque: value\n")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values := r.Header.Values("X-API-Key"); len(values) != 0 {
			t.Errorf("X-API-Key = %q, want absent", values)
		}
		if values := r.Header.Values("Authorization"); len(values) != 0 {
			t.Errorf("Authorization = %q, want absent", values)
		}
		w.Header().Set("Content-Type", entry.ContentType)
		_, _ = w.Write(contents)
	}))
	defer server.Close()

	client, err := NewWithAPIKey(server.URL, &http.Client{Timeout: 5 * time.Second}, "submission-secret")
	if err != nil {
		t.Fatal(err)
	}
	directory := openTestPinnedDirectory(t, trustedTestDir(t))
	temporary, err := client.downloadVerifiedContent(context.Background(), entry, entry.ContentType, directory, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = temporary.remove() })
}

func TestDownloadVerifiedContentMapsGoneToYanked(t *testing.T) {
	contents := []byte("gone resource")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer server.Close()

	client := newRemoteTestClient(t, server.URL)
	directory := openTestPinnedDirectory(t, trustedTestDir(t))
	_, err := client.downloadVerifiedContent(context.Background(), entry, entry.ContentType, directory, 0o600)
	if !errors.Is(err, cloud.ErrYanked) || errors.Is(err, cloud.ErrNotFound) || errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("downloadVerifiedContent() error = %v, want only ErrYanked", err)
	}
}

func TestDownloadVerifiedContentMapsNotFoundToErrNotFound(t *testing.T) {
	contents := []byte("missing resource")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := newRemoteTestClient(t, server.URL)
	directory := openTestPinnedDirectory(t, trustedTestDir(t))
	_, err := client.downloadVerifiedContent(context.Background(), entry, entry.ContentType, directory, 0o600)
	if !errors.Is(err, cloud.ErrNotFound) || errors.Is(err, cloud.ErrYanked) || errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("downloadVerifiedContent() error = %v, want only ErrNotFound", err)
	}
}

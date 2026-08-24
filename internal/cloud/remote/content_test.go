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
	remoteAgentRef = domain.PowerRef{Type: domain.PowerTypeAgent, Name: "demo-agent", Version: "v1.2.3"}
	remoteMCPRef   = domain.PowerRef{Type: domain.PowerTypeMCP, Name: "filesystem", Version: "v1.2.3"}
)

func TestDownloadVerifiedContentStagesExactBytesWithPrivateMode(t *testing.T) {
	contents := []byte("schema_version: 1\nopaque:  value\n")
	var requests []string
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	directory := openTestPinnedDirectory(t, trustedTestDir(t))
	temporary, err := client.downloadVerifiedContent(context.Background(), remoteAgentRef, "application/yaml", directory, 0o600)
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
	wantRequests := []string{
		"/api/v1/resources/agent/demo-agent/versions/v1.2.3",
		"/api/v1/resources/agent/demo-agent/versions/v1.2.3/content",
	}
	if fmt.Sprint(requests) != fmt.Sprint(wantRequests) {
		t.Fatalf("requests = %v, want metadata first %v", requests, wantRequests)
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
					w.Header().Set("Content-Type", "application/yaml")
					w.Header().Set("Content-Length", "99")
					return
				}
				writeExactMetadata(t, w, remoteAgentRef, contents, nil)
			},
		},
		{
			name: "actual bytes shorter than metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/yaml")
					w.Header().Set("Content-Length", fmt.Sprint(len(contents)))
					_, _ = w.Write(contents[:4])
					return
				}
				writeExactMetadata(t, w, remoteAgentRef, contents, nil)
			},
		},
		{
			name: "actual bytes longer than metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/yaml")
					w.(http.Flusher).Flush()
					_, _ = w.Write(append(append([]byte(nil), contents...), '!'))
					return
				}
				writeExactMetadata(t, w, remoteAgentRef, contents, nil)
			},
		},
		{
			name: "MD5 differs from metadata",
			serve: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/yaml")
					_, _ = w.Write([]byte("tampered bytes"))
					return
				}
				writeExactMetadata(t, w, remoteAgentRef, []byte("tampered bytes"), map[string]any{
					"content_size": len("tampered bytes"),
					"content_md5":  md5Hex(contents),
				})
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
			_, err := client.downloadVerifiedContent(context.Background(), remoteAgentRef, "application/yaml", directory, 0o600)
			if !errors.Is(err, cloud.ErrIntegrity) {
				t.Fatalf("downloadVerifiedContent() error = %v, want ErrIntegrity", err)
			}
			assertDirectoryEmpty(t, parent)
		})
	}
}

func TestDownloadVerifiedContentRejectsInvalidMetadataAndProtocol(t *testing.T) {
	contents := []byte("verified bytes")
	tests := []struct {
		name     string
		override map[string]any
		content  func(http.ResponseWriter)
		wantHits int32
		want     error
	}{
		{name: "uppercase MD5", override: map[string]any{"content_md5": strings.ToUpper(md5Hex(contents))}, want: cloud.ErrInvalidResponse},
		{name: "invalid MD5", override: map[string]any{"content_md5": "not-an-md5"}, want: cloud.ErrInvalidResponse},
		{name: "over 20 MiB metadata", override: map[string]any{"content_size": (20 << 20) + 1}, want: cloud.ErrUnsafeContent},
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
			var contentHits atomic.Int32
			parent := trustedTestDir(t)
			directory := openTestPinnedDirectory(t, parent)
			client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					contentHits.Add(1)
					tt.content(w)
					return
				}
				writeExactMetadata(t, w, remoteAgentRef, contents, tt.override)
			})
			_, err := client.downloadVerifiedContent(context.Background(), remoteAgentRef, "application/yaml", directory, 0o600)
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
			metadata := fmt.Sprintf(`{"type":"agent","name":"demo-agent","version":"v1.2.3","description":"demo","message":"fixture","content_type":"application/yaml","content_size":%d,"content_md5":"%s","published_at":"%s","status":"published"}`, len(contents), md5Hex(contents), testPublished)
			client, err := New("https://apc.example.com", &http.Client{
				Timeout: time.Second,
				Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					body := metadata
					contentType := "application/json"
					contentLength := int64(len(metadata))
					if strings.HasSuffix(request.URL.Path, "/content") {
						body = string(contents)
						contentType = "application/yaml"
						contentLength = tt.contentLength
					}
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
			temporary, err := client.downloadVerifiedContent(context.Background(), remoteAgentRef, "application/yaml", directory, 0o600)
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
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		directory := openTestPinnedDirectory(t, trustedTestDir(t))
		_, err := client.downloadVerifiedContent(ctx, remoteAgentRef, "application/yaml", directory, 0o600)
		if !errors.Is(err, context.Canceled) || errors.Is(err, cloud.ErrInvalidResponse) || errors.Is(err, cloud.ErrIntegrity) {
			t.Fatalf("downloadVerifiedContent() error = %v, want only context.Canceled classification", err)
		}
	})

	t.Run("cancelled while streaming", func(t *testing.T) {
		started := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/content") {
				writeExactMetadata(t, w, remoteAgentRef, []byte("0123456789"), nil)
				return
			}
			w.Header().Set("Content-Type", "application/yaml")
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("0"))
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
		}))
		defer server.Close()
		client := newRemoteTestClient(t, server.URL)
		parent := trustedTestDir(t)
		directory := openTestPinnedDirectory(t, parent)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := client.downloadVerifiedContent(ctx, remoteAgentRef, "application/yaml", directory, 0o600)
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
		metadata := fmt.Sprintf(`{"type":"agent","name":"demo-agent","version":"v1.2.3","description":"demo","message":"fixture","content_type":"application/yaml","content_size":1,"content_md5":"%s","published_at":"%s","status":"published"}`, md5Hex([]byte("x")), testPublished)
		client, err := New("https://apc.example.com", &http.Client{
			Timeout: time.Second,
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/content") {
					return nil, transportErr
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(metadata)), ContentLength: int64(len(metadata)), Request: request}, nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		directory := openTestPinnedDirectory(t, trustedTestDir(t))
		_, err = client.downloadVerifiedContent(context.Background(), remoteAgentRef, "application/yaml", directory, 0o600)
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
		w.Header().Set("Content-Type", expectedContentType(ref.Type))
		_, _ = w.Write(contents)
		return
	}
	writeExactMetadata(t, w, ref, contents, nil)
}

func writeExactMetadata(t *testing.T, w http.ResponseWriter, ref domain.PowerRef, contents []byte, overrides map[string]any) {
	t.Helper()
	metadata := validMetadata(string(ref.Type), ref.Name, ref.Version)
	metadata["content_size"] = len(contents)
	metadata["content_md5"] = md5Hex(contents)
	metadata["status"] = "published"
	for key, value := range overrides {
		metadata[key] = value
	}
	writeJSON(t, w, metadata)
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

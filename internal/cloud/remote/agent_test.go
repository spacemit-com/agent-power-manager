package remote

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

func TestDownloadAgentPreservesRawBytesAndPublishesMode(t *testing.T) {
	contents := []byte("schema_version: 1\r\nopaque:  value  \r\n")
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), remoteAgentRef, destination); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, contents) {
		t.Fatalf("destination bytes = %q, want raw %q", got, contents)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o644 {
		t.Fatalf("destination mode = %o, want 644", mode)
	}
}

func TestDownloadAgentRejectsMD5MismatchWithoutDestination(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(contents)
			return
		}
		writeExactMetadata(t, w, remoteAgentRef, contents, map[string]any{"content_md5": md5Hex([]byte("different"))})
	})
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	err := client.DownloadAgent(context.Background(), remoteAgentRef, destination)
	if !errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("DownloadAgent() error = %v, want ErrIntegrity", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains: %v", statErr)
	}
	assertNoSiblingTemporary(t, destination)
}

func TestDownloadAgentRejectsUnsafeOrExistingDestinationWithoutRequest(t *testing.T) {
	parent := t.TempDir()
	symlinkParent := filepath.Join(parent, "linked")
	if err := os.Symlink(t.TempDir(), symlinkParent); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(parent, "existing.yaml")
	if err := os.WriteFile(existing, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	tests := []string{
		"relative.yaml",
		parent + string(filepath.Separator) + "." + string(filepath.Separator) + "agent.yaml",
		filepath.Join(symlinkParent, "agent.yaml"),
		existing,
	}
	for _, destination := range tests {
		if err := client.DownloadAgent(context.Background(), remoteAgentRef, destination); !errors.Is(err, cloud.ErrUnsafeContent) {
			t.Fatalf("DownloadAgent(%q) error = %v, want ErrUnsafeContent", destination, err)
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want unsafe destinations rejected before network", got)
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "preserve" {
		t.Fatalf("existing destination = %q, %v, want preserved", got, err)
	}
}

func TestDownloadAgentRejectsCrossOriginContentRedirect(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	contents := []byte("schema_version: 1\n")
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
			return
		}
		writeExactMetadata(t, w, remoteAgentRef, contents, nil)
	}))
	defer source.Close()
	client := newRemoteTestClient(t, source.URL)
	destination := filepath.Join(t.TempDir(), "agent.yaml")
	err := client.DownloadAgent(context.Background(), remoteAgentRef, destination)
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("DownloadAgent() error = %v, want redirect ErrInvalidResponse", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("cross-origin content requests = %d, want 0", got)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination exists after redirect failure: %v", err)
	}
}

func TestDownloadAgentRejectsWrongReferenceBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	err := client.DownloadAgent(context.Background(), domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo-agent", Version: "v1.2.3"}, filepath.Join(t.TempDir(), "agent.yaml"))
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func assertNoSiblingTemporary(t *testing.T, destination string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "."+filepath.Base(destination)+".tmp-") {
			t.Fatalf("temporary residue remains beside destination: %s", entry.Name())
		}
	}
}

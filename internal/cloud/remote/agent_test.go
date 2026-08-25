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
	destination := filepath.Join(trustedTestDir(t), "agent.yaml")
	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); err != nil {
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

func TestDownloadAgentPublishesBelowTestingTempDirWithMissingHome(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "missing-home"))
	t.Setenv("XDG_RUNTIME_DIR", "")
	destination := filepath.Join(root, "agent.yaml")

	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); err != nil {
		t.Fatalf("DownloadAgent() below t.TempDir with nonexistent HOME error = %v", err)
	}
	if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("published Agent = %q, %v, want %q", got, err, contents)
	}
}

func TestDownloadAgentRejectsSymlinkedAncestorWithoutRequest(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	physicalRoot := trustedTestDir(t)
	physicalParent := filepath.Join(physicalRoot, "private", "tmp")
	if err := os.MkdirAll(physicalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(trustedTestDir(t), "var")
	if err := os.Symlink(filepath.Join(physicalRoot, "private"), aliasRoot); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(aliasRoot, "tmp", "agent.yaml")

	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want symlink ancestor ErrUnsafeContent", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want symlink ancestor rejected before network", got)
	}
	assertDirectoryEmpty(t, physicalParent)
}

func TestDownloadAgentKeepsPinnedParentAfterSameProcessRename(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	contentStarted := make(chan struct{})
	releaseContent := make(chan struct{})
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			close(contentStarted)
			<-releaseContent
		}
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	root := trustedTestDir(t)
	parent := filepath.Join(root, "destination")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	originalParent := filepath.Join(root, "pinned-original")
	replacementParent := filepath.Join(root, "replacement")
	if err := os.Mkdir(replacementParent, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "agent.yaml")
	done := make(chan error, 1)
	go func() {
		done <- client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	}()
	<-contentStarted
	if err := os.Rename(parent, originalParent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacementParent, parent); err != nil {
		t.Fatal(err)
	}
	close(releaseContent)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(originalParent, "agent.yaml")); err != nil || !bytes.Equal(got, contents) {
		t.Fatalf("pinned destination = %q, %v, want %q", got, err, contents)
	}
	if entries, err := os.ReadDir(replacementParent); err != nil || len(entries) != 0 {
		t.Fatalf("same-process replacement directory changed: %v, %v", entries, err)
	}
}

func TestDownloadAgentChmodsStagedDescriptorBeforeNoReplacePublication(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "agent.yaml")
	client.beforeAgentPublish = func(staged *stagedContent) {
		opened, err := staged.openRead()
		if err != nil {
			t.Fatal(err)
		}
		info, err := opened.Stat()
		if closeErr := opened.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o644 {
			t.Fatalf("staged Agent mode before publication = %o, want 644", mode)
		}
		if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination visible before no-replace publication: %v", err)
		}
		if err := os.WriteFile(destination, []byte("competitor"), 0o640); err != nil {
			t.Fatal(err)
		}
	}

	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want no-replace ErrUnsafeContent", err)
	}
	if got, readErr := os.ReadFile(destination); readErr != nil || string(got) != "competitor" {
		t.Fatalf("competitor destination = %q, %v, want preserved", got, readErr)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 || entries[0].Name() != "agent.yaml" {
		t.Fatalf("Agent failure cleanup left residue: %v", entries)
	}
}

func TestDownloadAgentDetectsStagedIdentityCorruptionWithoutDeletingReplacement(t *testing.T) {
	contents := []byte("schema_version: 1\n")
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	parent := trustedTestDir(t)
	destination := filepath.Join(parent, "agent.yaml")
	sentinel := filepath.Join(parent, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	var replacement string
	client.beforeAgentPublish = func(staged *stagedContent) {
		replacement = filepath.Join(parent, staged.name)
		if err := os.Rename(replacement, replacement+".owned-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sentinel, replacement); err != nil {
			t.Fatal(err)
		}
	}

	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want substituted-stage ErrUnsafeContent", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination published from substituted entry: %v", statErr)
	}
	if info, statErr := os.Lstat(replacement); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replacement entry was deleted or changed: %v, %v", info, statErr)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "preserve" {
		t.Fatalf("replacement target = %q, %v, want preserved", got, readErr)
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
	destination := filepath.Join(trustedTestDir(t), "agent.yaml")
	entry := validRemoteCatalogEntry(remoteAgentRef, contents)
	entry.ContentMD5 = md5Hex([]byte("different"))
	err := client.DownloadAgent(context.Background(), entry, destination)
	if !errors.Is(err, cloud.ErrIntegrity) {
		t.Fatalf("DownloadAgent() error = %v, want ErrIntegrity", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadAgentRejectsUnsafeOrExistingDestinationWithoutRequest(t *testing.T) {
	parent := trustedTestDir(t)
	symlinkParent := filepath.Join(parent, "linked")
	if err := os.Symlink(trustedTestDir(t), symlinkParent); err != nil {
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
	entry := validRemoteCatalogEntry(remoteAgentRef, []byte("fixture"))
	tests := []string{
		"relative.yaml",
		parent + string(filepath.Separator) + "." + string(filepath.Separator) + "agent.yaml",
		filepath.Join(symlinkParent, "agent.yaml"),
		existing,
	}
	for _, destination := range tests {
		if err := client.DownloadAgent(context.Background(), entry, destination); !errors.Is(err, cloud.ErrUnsafeContent) {
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

func TestDownloadAgentRejectsWritableDirectoryTrustViolationsWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	entry := validRemoteCatalogEntry(remoteAgentRef, []byte("fixture"))
	tests := []struct {
		name    string
		prepare func(*testing.T) string
	}{
		{
			name: "writable terminal parent",
			prepare: func(t *testing.T) string {
				parent := trustedTestDir(t)
				if err := os.Chmod(parent, 0o770); err != nil {
					t.Fatal(err)
				}
				return parent
			},
		},
		{
			name: "writable ancestor",
			prepare: func(t *testing.T) string {
				root := trustedTestDir(t)
				ancestor := filepath.Join(root, "writable")
				parent := filepath.Join(ancestor, "destination")
				if err := os.MkdirAll(parent, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(ancestor, 0o707); err != nil {
					t.Fatal(err)
				}
				return parent
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(tt.prepare(t), "agent.yaml")
			if err := client.DownloadAgent(context.Background(), entry, destination); !errors.Is(err, cloud.ErrUnsafeContent) {
				t.Fatalf("DownloadAgent() error = %v, want directory trust ErrUnsafeContent", err)
			}
		})
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want untrusted directories rejected before network", got)
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
	destination := filepath.Join(trustedTestDir(t), "agent.yaml")
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
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
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo-agent", Version: "v1.2.3"}, []byte("fixture")), filepath.Join(trustedTestDir(t), "agent.yaml"))
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

package remote

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
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

// remoteAgentFixtureYAML 是 V0.5 有效 Agent fixture：name 与 remoteAgentRef 一致，
// avatar 指向 assets/avatar.png。
const remoteAgentFixtureYAML = "schema_version: 1\nagent:\n  id: bibit-demo-agent\n  name: demo-agent\n  version: v1.2.3\n  description: demo agent\n  soul: help safely\n  metadata:\n    avatar: assets/avatar.png\n"

func remotePNGBytes(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	canvas := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	for index := 0; index < len(canvas.Pix); index += 4 {
		canvas.Pix[index+3] = 0xff
	}
	if err := png.Encode(&output, canvas); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// remoteAgentArchiveBytes 构造 Remote Agent 归档：agent.yaml + assets/avatar.png，
// 并允许调用方追加或替换条目。
func remoteAgentArchiveBytes(t *testing.T, additional ...testTarEntry) []byte {
	t.Helper()
	entries := []testTarEntry{
		{name: "agent.yaml", body: []byte(remoteAgentFixtureYAML)},
		{name: "assets", typeflag: tar.TypeDir, mode: 0o755},
		{name: "assets/avatar.png", body: remotePNGBytes(t)},
	}
	entries = append(entries, additional...)
	return testTarGzBytes(t, entries...)
}

func TestDownloadAgentPublishesVerifiedArchiveDirectory(t *testing.T) {
	contents := remoteAgentArchiveBytes(t,
		testTarEntry{name: "assets/data.bin", body: []byte{'a', 0, 'b'}},
	)
	var accepts []string
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			accepts = append(accepts, r.Header.Get("Accept"))
		}
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "agent.yaml"), remoteAgentFixtureYAML, 0o644)
	avatar, err := os.ReadFile(filepath.Join(destination, "assets", "avatar.png"))
	if err != nil || !bytes.HasPrefix(avatar, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("downloaded avatar = %d bytes, %v; want PNG", len(avatar), err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "assets", "data.bin"), "a\x00b", 0o644)
	if info, err := os.Stat(filepath.Join(destination, "assets")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
		t.Fatalf("assets directory = %v, %v, want physical mode 0755", info, err)
	}
	if len(accepts) != 1 || accepts[0] != "application/gzip" {
		t.Fatalf("content Accept headers = %v, want application/gzip", accepts)
	}
}

func TestDownloadAgentPublishesBelowTestingTempDirWithMissingHome(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "missing-home"))
	t.Setenv("XDG_RUNTIME_DIR", "")
	destination := filepath.Join(root, "agent")

	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); err != nil {
		t.Fatalf("DownloadAgent() below t.TempDir with nonexistent HOME error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "agent.yaml")); err != nil || string(got) != remoteAgentFixtureYAML {
		t.Fatalf("published Agent = %q, %v, want fixture yaml", got, err)
	}
}

func TestDownloadAgentRejectsSymlinkedAncestorWithoutRequest(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
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
	destination := filepath.Join(aliasRoot, "tmp", "agent")

	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want symlink ancestor ErrUnsafeContent", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("requests = %d, want symlink ancestor rejected before network", got)
	}
	assertDirectoryEmpty(t, physicalParent)
}

func TestDownloadAgentKeepsPinnedParentAfterSameProcessRename(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
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
	destination := filepath.Join(parent, "agent")
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
	if got, err := os.ReadFile(filepath.Join(originalParent, "agent", "agent.yaml")); err != nil || string(got) != remoteAgentFixtureYAML {
		t.Fatalf("pinned destination = %q, %v, want fixture yaml", got, err)
	}
	if entries, err := os.ReadDir(replacementParent); err != nil || len(entries) != 0 {
		t.Fatalf("same-process replacement directory changed: %v, %v", entries, err)
	}
}

func TestDownloadAgentAllowsBinaryNonAvatarAsset(t *testing.T) {
	contents := remoteAgentArchiveBytes(t, testTarEntry{name: "assets/data.bin", body: []byte{0x00, 0xff}})
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	if err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination); err != nil {
		t.Fatalf("DownloadAgent() with binary non-avatar asset error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "assets", "data.bin"))
	if err != nil || !bytes.Equal(got, []byte{0x00, 0xff}) {
		t.Fatalf("published binary asset = %v, %v, want original bytes", got, err)
	}
}

func TestDownloadAgentRejectsInvalidAvatarWithoutDestination(t *testing.T) {
	entries := []testTarEntry{
		{name: "agent.yaml", body: []byte(remoteAgentFixtureYAML)},
		{name: "assets", typeflag: tar.TypeDir, mode: 0o755},
		{name: "assets/avatar.png", body: []byte("not a png")},
	}
	contents := testTarGzBytes(t, entries...)
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("DownloadAgent() error = %v, want ErrInvalidAgent classification", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after semantic failure: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadAgentRejectsExtraRootFileWithoutDestination(t *testing.T) {
	contents := remoteAgentArchiveBytes(t, testTarEntry{name: "README.md", body: []byte("extra")})
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, domain.ErrInvalidAgent) {
		t.Fatalf("DownloadAgent() error = %v, want ErrInvalidAgent classification", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after semantic failure: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadAgentRejectsUnsafeArchivePathWithoutDestination(t *testing.T) {
	contents := testTarGzBytes(t, testTarEntry{name: "../escape", body: []byte("bad")})
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want unsafe archive path ErrUnsafeContent", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains after unsafe archive: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadAgentRejectsMD5MismatchWithoutDestination(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(contents)
			return
		}
		rejectRemovedMetadata(t, w)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
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

func TestDownloadAgentRejectsWrongContentTypeWithoutDestination(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(contents)
			return
		}
		rejectRemovedMetadata(t, w)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("DownloadAgent() error = %v, want ErrInvalidResponse", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination remains: %v", statErr)
	}
	assertDirectoryEmpty(t, filepath.Dir(destination))
}

func TestDownloadAgentRejectsUnsafeOrExistingDestinationWithoutRequest(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	})
	parent := trustedTestDir(t)
	symlinkParent := filepath.Join(parent, "linked")
	if err := os.Symlink(trustedTestDir(t), symlinkParent); err != nil {
		t.Fatal(err)
	}
	existingFile := filepath.Join(parent, "existing-file")
	if err := os.WriteFile(existingFile, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	existingDir := filepath.Join(parent, "existing-dir")
	if err := os.Mkdir(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		destination string
		want        error
	}{
		{destination: "relative", want: cloud.ErrUnsafeContent},
		{destination: parent + string(os.PathSeparator) + "unclean" + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "agent", want: cloud.ErrUnsafeContent},
		{destination: filepath.Join(symlinkParent, "agent"), want: cloud.ErrUnsafeContent},
		{destination: existingFile, want: cloud.ErrUnsafeContent},
		{destination: existingDir, want: cloud.ErrUnsafeContent},
	}
	for _, tt := range tests {
		err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), tt.destination)
		if !errors.Is(err, tt.want) {
			t.Fatalf("DownloadAgent(%q) error = %v, want %v", tt.destination, err, tt.want)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want unsafe destination rejected before network", requests.Load())
	}
	if got, readErr := os.ReadFile(existingFile); readErr != nil || string(got) != "preserve" {
		t.Fatalf("existing file changed: %q, %v", got, readErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(existingDir, "keep")); readErr != nil || string(got) != "keep" {
		t.Fatalf("existing directory changed: %q, %v", got, readErr)
	}
}

func TestDownloadAgentRejectsWritableDirectoryTrustViolationsWithoutRequest(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	})
	parent := trustedTestDir(t)
	untrusted := filepath.Join(parent, "untrusted")
	if err := os.Mkdir(untrusted, 0o700); err != nil {
		t.Fatal(err)
	}
	// 显式 Chmod，避免进程 umask 把目录软化成 0775（ADR-032 下可接受）。
	if err := os.Chmod(untrusted, 0o777); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(untrusted, "agent")

	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrUnsafeContent) {
		t.Fatalf("DownloadAgent() error = %v, want writable trust violation ErrUnsafeContent", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want trust violation rejected before network", requests.Load())
	}
	assertDirectoryEmpty(t, untrusted)
}

func TestDownloadAgentRejectsCrossOriginContentRedirect(t *testing.T) {
	contents := remoteAgentArchiveBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveValidContent(t, w, r, remoteAgentRef, contents)
	}))
	defer server.Close()
	// 目录元数据由 cross-origin 内容服务器提供；content 重定向必须被拒绝。
	client := newContentClient(t, remoteAgentRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			http.Redirect(w, r, server.URL+"/content", http.StatusFound)
			return
		}
		serveValidContent(t, w, r, remoteAgentRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "agent")

	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(remoteAgentRef, contents), destination)
	if !errors.Is(err, cloud.ErrInvalidResponse) {
		t.Fatalf("DownloadAgent() error = %v, want cross-origin redirect ErrInvalidResponse", err)
	}
	if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("destination exists after redirect failure: %v", statErr)
	}
}

func TestDownloadAgentRejectsWrongReferenceBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := newContentClient(t, remoteAgentRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	err := client.DownloadAgent(context.Background(), validRemoteCatalogEntry(domain.PowerRef{Type: domain.PowerTypeSkill, Name: "demo-agent", Version: "v1.2.3"}, []byte("fixture")), filepath.Join(trustedTestDir(t), "agent"))
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadAgent() error = %v, want ErrNotFound", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

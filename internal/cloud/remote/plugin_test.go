package remote

import (
	"archive/tar"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"apm/internal/cloud"
	"apm/internal/domain"
)

var remotePluginRef = domain.PowerRef{Type: domain.PowerTypePlugin, Name: "demo-plugin", Version: "v1.2.3"}

func TestDownloadPluginPublishesCanonicalBytesWithoutSemanticParsing(t *testing.T) {
	contents := testTarGzBytes(t,
		testTarEntry{name: "apc-plugin.json", body: []byte("Task 7 owns this schema\n")},
		testTarEntry{name: "skills", typeflag: tar.TypeDir},
		testTarEntry{name: "skills/demo", typeflag: tar.TypeDir},
		testTarEntry{name: "skills/demo/SKILL.md", body: []byte("# Demo\n")},
	)
	var accepts []string
	client := newContentClient(t, remotePluginRef, contents, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content") {
			accepts = append(accepts, r.Header.Get("Accept"))
		}
		serveValidContent(t, w, r, remotePluginRef, contents)
	})
	destination := filepath.Join(trustedTestDir(t), "plugin")
	if err := client.DownloadPlugin(context.Background(), validRemoteCatalogEntry(remotePluginRef, contents), destination); err != nil {
		t.Fatal(err)
	}
	assertFileContentsAndMode(t, filepath.Join(destination, "apc-plugin.json"), "Task 7 owns this schema\n", 0o644)
	assertFileContentsAndMode(t, filepath.Join(destination, "skills", "demo", "SKILL.md"), "# Demo\n", 0o644)
	if len(accepts) != 1 || accepts[0] != "application/gzip" {
		t.Fatalf("content Accept headers = %v, want application/gzip", accepts)
	}
}

func TestDownloadPluginRejectsMissingOrNonRegularRootMarker(t *testing.T) {
	tests := []struct {
		name    string
		entries []testTarEntry
	}{
		{name: "missing", entries: []testTarEntry{{name: "README.md", body: []byte("invalid")}}},
		{name: "directory", entries: []testTarEntry{{name: "apc-plugin.json", typeflag: tar.TypeDir}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contents := testTarGzBytes(t, tt.entries...)
			client := newContentClient(t, remotePluginRef, contents, func(w http.ResponseWriter, r *http.Request) {
				serveValidContent(t, w, r, remotePluginRef, contents)
			})
			destination := filepath.Join(trustedTestDir(t), "plugin")
			err := client.DownloadPlugin(context.Background(), validRemoteCatalogEntry(remotePluginRef, contents), destination)
			if !errors.Is(err, cloud.ErrUnsafeContent) || errors.Is(err, cloud.ErrNotFound) {
				t.Fatalf("DownloadPlugin() error = %v, want only ErrUnsafeContent marker classification", err)
			}
			if _, statErr := os.Lstat(destination); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("invalid Plugin destination remains: %v", statErr)
			}
		})
	}
}

func TestDownloadPluginRejectsWrongReferenceBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := newContentClient(t, remotePluginRef, nil, func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	})
	err := client.DownloadPlugin(
		context.Background(),
		validRemoteCatalogEntry(domain.PowerRef{Type: domain.PowerTypeSkill, Name: remotePluginRef.Name, Version: remotePluginRef.Version}, []byte("fixture")),
		filepath.Join(trustedTestDir(t), "plugin"),
	)
	if !errors.Is(err, cloud.ErrNotFound) {
		t.Fatalf("DownloadPlugin() error = %v, want ErrNotFound", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

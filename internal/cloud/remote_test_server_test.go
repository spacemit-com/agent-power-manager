package cloud_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"apm/internal/cloud/local"
	"apm/internal/domain"
)

const canonicalSnapshotTime = "2026-08-01T00:00:00Z"

type canonicalRemoteResource struct {
	ref         domain.PowerRef
	contentType string
	content     []byte
}

type canonicalRemoteServer struct {
	server    *httptest.Server
	resources []canonicalRemoteResource
}

func newCanonicalRemoteServer(t *testing.T, root string) *canonicalRemoteServer {
	t.Helper()
	client, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fixture := &canonicalRemoteServer{resources: make([]canonicalRemoteResource, 0, len(refs))}
	for _, ref := range refs {
		contentType, content := canonicalRemoteContent(t, root, ref)
		fixture.resources = append(fixture.resources, canonicalRemoteResource{
			ref: ref, contentType: contentType, content: content,
		})
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func canonicalRemoteContent(t *testing.T, root string, ref domain.PowerRef) (string, []byte) {
	t.Helper()
	switch ref.Type {
	case domain.PowerTypeSkill:
		return "application/gzip", deterministicArchive(t, filepath.Join(root, "skills", ref.Name, ref.Version))
	case domain.PowerTypePlugin:
		return "application/gzip", deterministicArchive(t, filepath.Join(root, "plugins", ref.Name, ref.Version))
	case domain.PowerTypeMCP:
		return "application/json", readCanonicalFile(t, filepath.Join(root, "mcps", ref.Name, ref.Version, "MCP.json"))
	case domain.PowerTypeAgent:
		return "application/yaml", readCanonicalFile(t, filepath.Join(root, "agents", ref.Name, ref.Version, "agent.yaml"))
	default:
		t.Fatalf("unsupported canonical resource type %q", ref.Type)
		return "", nil
	}
}

func readCanonicalFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func deterministicArchive(t *testing.T, root string) []byte {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)

	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	gzipWriter.Header.ModTime = time.Unix(0, 0).UTC()
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		header := &tar.Header{
			Name: filepath.ToSlash(relative), Uid: 0, Gid: 0,
			ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Time{}, ChangeTime: time.Time{},
			Format: tar.FormatUSTAR,
		}
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o755
		} else if info.Mode().IsRegular() {
			header.Typeflag = tar.TypeReg
			header.Mode = int64(0o644 | info.Mode().Perm()&0o111)
			header.Size = info.Size()
		} else {
			t.Fatalf("canonical fixture path %q is not physical", path)
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(tarWriter, file)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				t.Fatal(fmt.Errorf("archive %q: %w", path, err))
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func (fixture *canonicalRemoteServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(segments) == 3 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "resources" {
		fixture.serveList(writer, request)
		return
	}
	if len(segments) < 6 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "resources" {
		http.NotFound(writer, request)
		return
	}
	ref := domain.PowerRef{Type: domain.PowerType(segments[3]), Name: segments[4]}
	if len(segments) == 6 && segments[5] == "latest" {
		fixture.serveLatest(writer, request, ref)
		return
	}
	if len(segments) == 7 && segments[5] == "versions" {
		ref.Version = segments[6]
		fixture.serveExact(writer, request, ref)
		return
	}
	if len(segments) == 8 && segments[5] == "versions" && segments[7] == "content" {
		ref.Version = segments[6]
		fixture.serveContent(writer, request, ref)
		return
	}
	http.NotFound(writer, request)
}

func (fixture *canonicalRemoteServer) serveList(writer http.ResponseWriter, request *http.Request) {
	const pageSize = 5
	start := 0
	if cursor := request.URL.Query().Get("cursor"); cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 || parsed > len(fixture.resources) {
			http.Error(writer, "invalid cursor", http.StatusBadRequest)
			return
		}
		start = parsed
	}
	end := start + pageSize
	if end > len(fixture.resources) {
		end = len(fixture.resources)
	}
	items := make([]map[string]any, 0, end-start)
	for _, resource := range fixture.resources[start:end] {
		items = append(items, resource.metadata(false))
	}
	next := ""
	if end < len(fixture.resources) {
		next = strconv.Itoa(end)
	}
	writeCanonicalJSON(writer, map[string]any{
		"snapshot_updated_at": canonicalSnapshotTime,
		"items":               items,
		"next_cursor":         next,
	})
}

func (fixture *canonicalRemoteServer) serveLatest(writer http.ResponseWriter, request *http.Request, query domain.PowerRef) {
	var latest *canonicalRemoteResource
	for index := range fixture.resources {
		resource := &fixture.resources[index]
		if resource.ref.Type == query.Type && resource.ref.Name == query.Name {
			latest = resource
		}
	}
	if latest == nil {
		http.NotFound(writer, request)
		return
	}
	writeCanonicalJSON(writer, map[string]any{
		"type": latest.ref.Type, "name": latest.ref.Name, "version": latest.ref.Version,
	})
}

func (fixture *canonicalRemoteServer) serveExact(writer http.ResponseWriter, request *http.Request, ref domain.PowerRef) {
	resource := fixture.find(ref)
	if resource == nil {
		http.NotFound(writer, request)
		return
	}
	writeCanonicalJSON(writer, resource.metadata(true))
}

func (fixture *canonicalRemoteServer) serveContent(writer http.ResponseWriter, request *http.Request, ref domain.PowerRef) {
	resource := fixture.find(ref)
	if resource == nil {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", resource.contentType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(resource.content)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(resource.content)
}

func (fixture *canonicalRemoteServer) find(ref domain.PowerRef) *canonicalRemoteResource {
	for index := range fixture.resources {
		if fixture.resources[index].ref == ref {
			return &fixture.resources[index]
		}
	}
	return nil
}

func (resource canonicalRemoteResource) metadata(exact bool) map[string]any {
	digest := md5.Sum(resource.content)
	metadata := map[string]any{
		"type": resource.ref.Type, "name": resource.ref.Name, "version": resource.ref.Version,
		"description": "canonical test fixture", "change_summary": "fixture",
		"content_type": resource.contentType, "content_size": len(resource.content),
		"content_md5": hex.EncodeToString(digest[:]), "published_at": canonicalSnapshotTime,
	}
	if exact {
		metadata["status"] = "published"
		metadata["yanked_at"] = ""
		metadata["yank_reason"] = ""
	}
	return metadata
}

func writeCanonicalJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(value)
}

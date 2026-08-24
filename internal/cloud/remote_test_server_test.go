package cloud_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"apm/internal/domain"
)

const canonicalSnapshotTime = "2026-08-01T00:00:00Z"

type canonicalRemoteResource struct {
	ref         domain.PowerRef
	contentType string
	content     []byte
}

type canonicalRemoteServer struct {
	server             *httptest.Server
	resources          []canonicalRemoteResource
	mu                 sync.Mutex
	protocolViolations []string
}

func newCanonicalRemoteServer(t *testing.T, root string) *canonicalRemoteServer {
	t.Helper()
	fixture := newCanonicalRemoteFixture(t, root)
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(func() {
		fixture.server.Close()
		if violations := fixture.violations(); len(violations) != 0 {
			t.Errorf("canonical Remote server protocol violations: %v", violations)
		}
	})
	return fixture
}

func newCanonicalRemoteFixture(t *testing.T, root string) *canonicalRemoteServer {
	t.Helper()
	fixture := &canonicalRemoteServer{resources: make([]canonicalRemoteResource, 0, len(canonicalPowerOracle))}
	for _, ref := range canonicalPowerOracle {
		contentType, content := canonicalRemoteContent(t, root, ref)
		fixture.resources = append(fixture.resources, canonicalRemoteResource{
			ref: ref, contentType: contentType, content: content,
		})
	}
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
			header.Mode = int64(info.Mode().Perm())
		} else if info.Mode().IsRegular() {
			header.Typeflag = tar.TypeReg
			header.Mode = int64(info.Mode().Perm())
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
		fixture.rejectProtocol(writer, request, "method = %q, want GET", request.Method)
		return
	}
	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(segments) == 3 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "resources" {
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources"}, "application/json", true) {
			return
		}
		fixture.serveList(writer, request)
		return
	}
	if len(segments) < 6 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "resources" {
		fixture.rejectProtocol(writer, request, "unknown endpoint")
		return
	}
	ref := domain.PowerRef{Type: domain.PowerType(segments[3]), Name: segments[4]}
	if len(segments) == 6 && segments[5] == "latest" {
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources", segments[3], segments[4], "latest"}, "application/json", false) {
			return
		}
		fixture.serveLatest(writer, request, ref)
		return
	}
	if len(segments) == 7 && segments[5] == "versions" {
		ref.Version = segments[6]
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources", segments[3], segments[4], "versions", segments[6]}, "application/json", false) {
			return
		}
		fixture.serveExact(writer, request, ref)
		return
	}
	if len(segments) == 8 && segments[5] == "versions" && segments[7] == "content" {
		ref.Version = segments[6]
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources", segments[3], segments[4], "versions", segments[6], "content"}, canonicalContentType(ref.Type), false) {
			return
		}
		fixture.serveContent(writer, request, ref)
		return
	}
	fixture.rejectProtocol(writer, request, "unknown endpoint shape")
}

func (fixture *canonicalRemoteServer) serveList(writer http.ResponseWriter, request *http.Request) {
	const pageSize = 5
	start := 0
	if cursor := request.URL.Query().Get("cursor"); cursor != "" {
		start, _ = parseCanonicalCursor(cursor, len(fixture.resources))
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
		next = fmt.Sprintf("page-%d", end)
	}
	writeCanonicalJSON(writer, map[string]any{
		"snapshot_updated_at": canonicalSnapshotTime,
		"items":               items,
		"next_cursor":         next,
	})
}

func (fixture *canonicalRemoteServer) serveLatest(writer http.ResponseWriter, request *http.Request, query domain.PowerRef) {
	latestRefs, err := latestPowerRefs(canonicalPowerOracle)
	if err != nil {
		fixture.rejectProtocol(writer, request, "invalid oracle SemVer: %v", err)
		return
	}
	var latest domain.PowerRef
	for _, ref := range latestRefs {
		if ref.Type == query.Type && ref.Name == query.Name {
			latest = ref
			break
		}
	}
	if latest == (domain.PowerRef{}) {
		http.NotFound(writer, request)
		return
	}
	writeCanonicalJSON(writer, map[string]any{
		"type": latest.Type, "name": latest.Name, "version": latest.Version,
	})
}

func (fixture *canonicalRemoteServer) requireRequest(writer http.ResponseWriter, request *http.Request, segments []string, accept string, list bool) bool {
	expectedPath := escapedAPIPath(segments...)
	if request.URL.EscapedPath() != expectedPath {
		fixture.rejectProtocol(writer, request, "escaped path = %q, want %q", request.URL.EscapedPath(), expectedPath)
		return false
	}
	acceptValues := request.Header.Values("Accept")
	if len(acceptValues) != 1 || acceptValues[0] != accept {
		fixture.rejectProtocol(writer, request, "Accept = %q, want exactly %q", acceptValues, accept)
		return false
	}
	if list {
		query, err := url.ParseQuery(request.URL.RawQuery)
		if err != nil || !validCanonicalListQuery(query, len(fixture.resources)) {
			fixture.rejectProtocol(writer, request, "list query = %q, want limit=100 and optional valid opaque cursor", request.URL.RawQuery)
			return false
		}
		return true
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		fixture.rejectProtocol(writer, request, "endpoint query = %q, want none", request.URL.RawQuery)
		return false
	}
	return true
}

func validCanonicalListQuery(query url.Values, resourceCount int) bool {
	if len(query) < 1 || len(query) > 2 || len(query["limit"]) != 1 || query.Get("limit") != "100" {
		return false
	}
	for key := range query {
		if key != "limit" && key != "cursor" {
			return false
		}
	}
	if values, found := query["cursor"]; found {
		if len(values) != 1 {
			return false
		}
		_, valid := parseCanonicalCursor(values[0], resourceCount)
		return valid
	}
	return true
}

func parseCanonicalCursor(cursor string, resourceCount int) (int, bool) {
	if !strings.HasPrefix(cursor, "page-") {
		return 0, false
	}
	start, err := strconv.Atoi(strings.TrimPrefix(cursor, "page-"))
	return start, err == nil && start > 0 && start < resourceCount && start%5 == 0
}

func escapedAPIPath(segments ...string) string {
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	return "/" + strings.Join(escaped, "/")
}

func canonicalContentType(powerType domain.PowerType) string {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		return "application/gzip"
	case domain.PowerTypeMCP:
		return "application/json"
	case domain.PowerTypeAgent:
		return "application/yaml"
	default:
		return ""
	}
}

func (fixture *canonicalRemoteServer) rejectProtocol(writer http.ResponseWriter, request *http.Request, format string, arguments ...any) {
	message := fmt.Sprintf(format, arguments...)
	fixture.mu.Lock()
	fixture.protocolViolations = append(fixture.protocolViolations, fmt.Sprintf("%s %s: %s", request.Method, request.URL.RequestURI(), message))
	fixture.mu.Unlock()
	http.Error(writer, "protocol violation", http.StatusBadRequest)
}

func (fixture *canonicalRemoteServer) violations() []string {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]string(nil), fixture.protocolViolations...)
}

func testCanonicalRemoteServerProtocolMutations(t *testing.T) {
	t.Helper()
	root, err := filepath.Abs("resources")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method, target, accept string
	}{
		{name: "method", method: http.MethodPost, target: "/api/v1/resources?limit=100", accept: "application/json"},
		{name: "missing_limit", method: http.MethodGet, target: "/api/v1/resources", accept: "application/json"},
		{name: "duplicate_limit", method: http.MethodGet, target: "/api/v1/resources?limit=100&limit=100", accept: "application/json"},
		{name: "unexpected_query", method: http.MethodGet, target: "/api/v1/resources?limit=100&extra=1", accept: "application/json"},
		{name: "latest_query", method: http.MethodGet, target: "/api/v1/resources/agent/bianbu-agent-default/latest?limit=100", accept: "application/json"},
		{name: "content_accept", method: http.MethodGet, target: "/api/v1/resources/agent/bianbu-agent-default/versions/v1.0.0/content", accept: "application/json"},
		{name: "noncanonical_escape", method: http.MethodGet, target: "/api/v1/resources/%61gent/bianbu-agent-default/versions/v1.0.0", accept: "application/json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCanonicalRemoteFixture(t, root)
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header.Set("Accept", test.accept)
			response := httptest.NewRecorder()
			fixture.serveHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if violations := fixture.violations(); len(violations) != 1 {
				t.Fatalf("protocol violations = %v, want exactly one", violations)
			}
		})
	}
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

// agentSubmissionServer 记录收到的 multipart 提交请求（metadata 与 content part），
// 并返回固定状态码与响应体，供跨层 Agent 提交契约测试复用。
type agentSubmissionServer struct {
	server *httptest.Server
	mu     sync.Mutex
	status int
	body   string
	got    []recordedAgentSubmission
}

type recordedAgentSubmission struct {
	metadata    map[string]any
	contentType string
	content     []byte
	parseErr    error
}

func newAgentSubmissionServer(t *testing.T, status int, body string) *agentSubmissionServer {
	t.Helper()
	fixture := &agentSubmissionServer{status: status, body: body}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.serveHTTP))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *agentSubmissionServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	metadata, contentType, content, parseErr := readAgentSubmissionParts(request)
	fixture.mu.Lock()
	fixture.got = append(fixture.got, recordedAgentSubmission{
		metadata: metadata, contentType: contentType, content: content, parseErr: parseErr,
	})
	fixture.mu.Unlock()
	if parseErr != nil {
		http.Error(writer, "invalid submission", http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(fixture.status)
	_, _ = writer.Write([]byte(fixture.body))
}

// readAgentSubmissionParts 解析 multipart 提交请求，返回 metadata JSON map、content part 的
// Content-Type 与原始字节；该函数运行在服务器 goroutine，因此只返回错误而非调用 t.Fatal。
func readAgentSubmissionParts(request *http.Request) (map[string]any, string, []byte, error) {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return nil, "", nil, fmt.Errorf("Content-Type = %q: %v", request.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(request.Body, parameters["boundary"])
	var metadata map[string]any
	var contentType string
	var content []byte
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("read multipart part: %w", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			return nil, "", nil, fmt.Errorf("read %q part: %w", part.FormName(), err)
		}
		switch part.FormName() {
		case "metadata":
			if err := json.Unmarshal(body, &metadata); err != nil {
				return nil, "", nil, fmt.Errorf("decode metadata part: %w", err)
			}
		case "content":
			contentType = part.Header.Get("Content-Type")
			content = body
		}
	}
	return metadata, contentType, content, nil
}

// assertReceivedAgentSubmission 断言恰好收到一次提交，且其元数据身份、content part 媒体类型与原始
// agent.yaml 字节完全一致。
func (fixture *agentSubmissionServer) assertReceivedAgentSubmission(t *testing.T, want domain.PowerType, name, version, description, changeSummary string, raw []byte) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.got) != 1 {
		t.Fatalf("submissions received = %d, want 1", len(fixture.got))
	}
	got := fixture.got[0]
	if got.parseErr != nil {
		t.Fatalf("submission parse error: %v", got.parseErr)
	}
	wantMD5 := md5.Sum(raw)
	if got.metadata["type"] != string(want) ||
		got.metadata["name"] != name ||
		got.metadata["version"] != version ||
		got.metadata["description"] != description ||
		got.metadata["change_summary"] != changeSummary ||
		got.metadata["content_md5"] != hex.EncodeToString(wantMD5[:]) {
		t.Fatalf("metadata = %#v, want type=%q name=%q version=%q description=%q change_summary=%q content_md5=%q",
			got.metadata, want, name, version, description, changeSummary, hex.EncodeToString(wantMD5[:]))
	}
	if got.contentType != "application/yaml" {
		t.Fatalf("content part Content-Type = %q, want application/yaml", got.contentType)
	}
	if !bytes.Equal(got.content, raw) {
		t.Fatalf("content part bytes = %q, want exact raw agent.yaml %q", got.content, raw)
	}
}

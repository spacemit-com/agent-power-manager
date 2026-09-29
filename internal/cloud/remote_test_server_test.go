package cloud_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha256"
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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"apm/internal/domain"
)

const canonicalSnapshotTime = "2026-08-01T00:00:00Z"
const canonicalSubmissionAPIKey = "contract-key"

type canonicalRemoteResource struct {
	ref         domain.PowerRef
	contentType string
	content     []byte
}

type canonicalRemoteServer struct {
	server             *httptest.Server
	resources          []canonicalRemoteResource
	catalog            []byte
	mu                 sync.Mutex
	protocolViolations []string
	submissions        []recordedSubmission
	tooManyActive      bool
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
	fixture.catalog = canonicalRemoteCatalog(t, fixture.resources)
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
		return "application/gzip", deterministicArchive(t, filepath.Join(root, "agents", ref.Name, ref.Version))
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
	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	// 剥离网关挂载前缀，模拟网关在路由到 /api/v1 前去掉 /api-server/apc。
	if len(segments) >= 2 && segments[0] == "api-server" && segments[1] == "apc" {
		segments = segments[2:]
	}
	if len(segments) >= 3 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "submissions" {
		fixture.serveSubmissionAPI(writer, request, segments)
		return
	}
	if request.Method != http.MethodGet {
		fixture.rejectProtocol(writer, request, "method = %q, want GET", request.Method)
		return
	}
	if len(segments) == 4 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "resources" && segments[3] == "version" {
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources", "version"}, "application/json") {
			return
		}
		fixture.serveCatalogVersion(writer)
		return
	}
	if len(segments) == 3 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "resources" {
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources"}, "application/json") {
			return
		}
		fixture.serveCatalog(writer)
		return
	}
	if len(segments) < 6 || segments[0] != "api" || segments[1] != "v1" || segments[2] != "resources" {
		fixture.rejectProtocol(writer, request, "unknown endpoint")
		return
	}
	ref := domain.PowerRef{Type: domain.PowerType(segments[3]), Name: segments[4]}
	if len(segments) == 8 && segments[5] == "versions" && segments[7] == "content" {
		ref.Version = segments[6]
		if !fixture.requireRequest(writer, request, []string{"api", "v1", "resources", segments[3], segments[4], "versions", segments[6], "content"}, canonicalContentType(ref.Type)) {
			return
		}
		fixture.serveContent(writer, request, ref)
		return
	}
	fixture.rejectProtocol(writer, request, "unknown endpoint shape")
}

func (fixture *canonicalRemoteServer) serveCatalogVersion(writer http.ResponseWriter) {
	digest := sha256.Sum256(fixture.catalog)
	writeCanonicalJSON(writer, map[string]any{
		"schema_version": 1,
		"catalog_sha256": hex.EncodeToString(digest[:]),
		"catalog_size":   len(fixture.catalog),
	})
}

func (fixture *canonicalRemoteServer) serveCatalog(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Content-Length", strconv.Itoa(len(fixture.catalog)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(fixture.catalog)
}

func (fixture *canonicalRemoteServer) requireRequest(writer http.ResponseWriter, request *http.Request, segments []string, accept string) bool {
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
	if authorization := request.Header.Values("Authorization"); len(authorization) != 0 {
		fixture.rejectProtocol(writer, request, "resource Authorization = %q, want absent", authorization)
		return false
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		fixture.rejectProtocol(writer, request, "endpoint query = %q, want none", request.URL.RawQuery)
		return false
	}
	return true
}

func escapedAPIPath(segments ...string) string {
	all := make([]string, 0, len(segments)+2)
	all = append(all, "api-server", "apc")
	all = append(all, segments...)
	escaped := make([]string, len(all))
	for index, segment := range all {
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
		return "application/gzip"
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
		{name: "removed_cursor_list", method: http.MethodGet, target: "/api/v1/resources?limit=100", accept: "application/json"},
		{name: "removed_cursor_parameter", method: http.MethodGet, target: "/api/v1/resources?cursor=legacy-page", accept: "application/json"},
		{name: "duplicate_limit", method: http.MethodGet, target: "/api/v1/resources?limit=100&limit=100", accept: "application/json"},
		{name: "unexpected_query", method: http.MethodGet, target: "/api/v1/resources?limit=100&extra=1", accept: "application/json"},
		{name: "removed_version_series", method: http.MethodGet, target: escapedAPIPath("api", "v1", "resources", "agent", "bianbu-agent-default", "versions"), accept: "application/json"},
		{name: "removed_latest", method: http.MethodGet, target: escapedAPIPath("api", "v1", "resources", "agent", "bianbu-agent-default", "latest"), accept: "application/json"},
		{name: "latest_query", method: http.MethodGet, target: escapedAPIPath("api", "v1", "resources", "agent", "bianbu-agent-default", "latest") + "?limit=100", accept: "application/json"},
		{name: "removed_exact_metadata", method: http.MethodGet, target: "/api/v1/resources/agent/bianbu-agent-default/versions/v1.0.0", accept: "application/json"},
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

// serveSubmissionAPI 模拟提交、本人活动列表与详情三个 APC v1 端点，并严格校验
// Authorization: Bearer、无分页查询及提交幂等键，避免跨层测试被宽松 fake 掩盖。
func (fixture *canonicalRemoteServer) serveSubmissionAPI(writer http.ResponseWriter, request *http.Request, segments []string) {
	if values := request.Header.Values("Authorization"); len(values) != 1 || values[0] != "Bearer "+canonicalSubmissionAPIKey {
		fixture.rejectProtocol(writer, request, "Authorization = %q, want exactly one Bearer contract key", values)
		return
	}
	if apiKey := request.Header.Values("X-API-Key"); len(apiKey) != 0 {
		fixture.rejectProtocol(writer, request, "X-API-Key = %q, want absent", apiKey)
		return
	}
	if values := request.Header.Values("Accept"); len(values) != 1 || values[0] != "application/json" {
		fixture.rejectProtocol(writer, request, "Accept = %q, want exactly application/json", values)
		return
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery {
		fixture.rejectProtocol(writer, request, "submission query = %q, want none", request.URL.RawQuery)
		return
	}
	switch {
	case len(segments) == 3 && request.Method == http.MethodPost:
		fixture.serveSubmissionCreate(writer, request)
	case len(segments) == 3 && request.Method == http.MethodGet:
		fixture.serveSubmissionList(writer)
	case len(segments) == 4 && request.Method == http.MethodGet && segments[3] == "sub_active":
		fixture.serveSubmissionDetail(writer)
	default:
		fixture.rejectProtocol(writer, request, "unknown submission endpoint")
	}
}

func (fixture *canonicalRemoteServer) serveSubmissionCreate(writer http.ResponseWriter, request *http.Request) {
	if strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
		fixture.rejectProtocol(writer, request, "Idempotency-Key is empty")
		return
	}
	fixture.mu.Lock()
	tooManyActive := fixture.tooManyActive
	fixture.mu.Unlock()
	if tooManyActive {
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"status":409,"code":"TOO_MANY_ACTIVE_SUBMISSIONS","trace_id":"tr_contract_quota"}`))
		return
	}
	metadata, contentType, content, parseErr := readCanonicalSubmissionParts(request)
	fixture.mu.Lock()
	fixture.submissions = append(fixture.submissions, recordedSubmission{
		metadata: metadata, contentType: contentType, content: content, parseErr: parseErr,
	})
	fixture.mu.Unlock()
	if parseErr != nil {
		http.Error(writer, "invalid submission", http.StatusBadRequest)
		return
	}
	submissionID := "sub_" + fmt.Sprint(metadata["type"])
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"submission_id": submissionID,
		"status":        "received",
		"status_url":    "/api/v1/submissions/" + submissionID,
	})
}

func (fixture *canonicalRemoteServer) serveSubmissionList(writer http.ResponseWriter) {
	writeCanonicalJSON(writer, map[string]any{"items": []map[string]any{{
		"submission_id": "sub_active",
		"type":          "skill",
		"name":          "contract-skill",
		"version":       "v1.2.3",
		"status":        "review_pending",
		"created_at":    "2026-08-25T10:00:00Z",
	}}})
}

func (fixture *canonicalRemoteServer) serveSubmissionDetail(writer http.ResponseWriter) {
	writeCanonicalJSON(writer, map[string]any{
		"submission_id":     "sub_active",
		"type":              "skill",
		"name":              "contract-skill",
		"version":           "v1.2.3",
		"description":       "Cross-layer Skill",
		"message":           "publish skill",
		"status":            "review_pending",
		"created_at":        "2026-08-25T10:00:00Z",
		"updated_at":        "2026-08-25T10:01:00Z",
		"content_available": true,
		"validation_errors": []string{},
		"review_comment":    "",
		"published_version": "",
		"timeline": []map[string]any{
			{
				"event_id": "evt_received", "event_type": "status_changed", "status_from": "", "status_to": "received",
				"timestamp": "2026-08-25T10:00:00Z", "message": "submission received",
			},
			{
				"event_id": "evt_review", "event_type": "status_changed", "status_from": "received", "status_to": "review_pending",
				"timestamp": "2026-08-25T10:01:00Z", "message": "review requested",
			},
		},
	})
}

func writeCanonicalJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(value)
}

type recordedSubmission struct {
	metadata    map[string]any
	contentType string
	content     []byte
	parseErr    error
}

// readCanonicalSubmissionParts 解析 multipart 提交请求，返回 metadata JSON map、content part 的
// Content-Type 与原始字节；该函数运行在服务器 goroutine，因此只返回错误而非调用 t.Fatal。
func readCanonicalSubmissionParts(request *http.Request) (map[string]any, string, []byte, error) {
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

func (fixture *canonicalRemoteServer) assertReceivedSubmission(t *testing.T, want domain.PowerType, name, version, description, message, contentType, nameZH, nameEN, descriptionZH string) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submissions) == 0 {
		t.Fatal("submissions received = 0, want at least 1")
	}
	got := fixture.submissions[len(fixture.submissions)-1]
	if got.parseErr != nil {
		t.Fatalf("submission parse error: %v", got.parseErr)
	}
	if got.metadata["schema_version"] != float64(2) {
		t.Fatalf("metadata schema_version = %#v, want 2 (resource format version)", got.metadata["schema_version"])
	}
	if got.metadata["type"] != string(want) || got.metadata["name"] != name ||
		got.metadata["version"] != version || got.metadata["description"] != description ||
		got.metadata["message"] != message {
		t.Fatalf("metadata = %#v, want %s/%s@%s description=%q message=%q", got.metadata, want, name, version, description, message)
	}
	// metadata 必须与资源内容携带同一组展示字段，APC 才可能从任一侧重建（ADR-041）。
	if got.metadata["name_zh"] != nameZH || got.metadata["name_en"] != nameEN ||
		got.metadata["description_zh"] != descriptionZH {
		t.Fatalf("metadata display fields = %v/%v/%v, want %q/%q/%q",
			got.metadata["name_zh"], got.metadata["name_en"], got.metadata["description_zh"],
			nameZH, nameEN, descriptionZH)
	}
	if got.contentType != contentType || len(got.content) == 0 {
		t.Fatalf("content = type %q size %d, want %q and non-empty", got.contentType, len(got.content), contentType)
	}
	wantMD5 := md5.Sum(got.content)
	if got.metadata["content_md5"] != hex.EncodeToString(wantMD5[:]) {
		t.Fatalf("metadata content_md5 = %v, want %q", got.metadata["content_md5"], hex.EncodeToString(wantMD5[:]))
	}
	// definition 是 MCP 的资源实体，不属于 catalog metadata：四类资源的 metadata 都不携带它，
	// 服务端只从 content 读取（ADR-043）。
	if _, present := got.metadata["definition"]; present {
		t.Fatalf("metadata contains definition for %s: %#v", want, got.metadata["definition"])
	}
}

// assertReceivedMCPDefinitionInContent 断言 MCP 的定义出现在上传的 content 里（而不是 metadata）。
func (fixture *canonicalRemoteServer) assertReceivedMCPDefinitionInContent(t *testing.T, want domain.MCPServer) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.submissions) == 0 {
		t.Fatal("submissions received = 0, want at least 1")
	}
	got := fixture.submissions[len(fixture.submissions)-1]
	if got.parseErr != nil {
		t.Fatalf("submission parse error: %v", got.parseErr)
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var wantMap map[string]any
	if err := json.Unmarshal(raw, &wantMap); err != nil {
		t.Fatal(err)
	}
	var contentMap map[string]any
	if err := json.Unmarshal(got.content, &contentMap); err != nil {
		t.Fatalf("decode content: %v", err)
	}
	definition, ok := contentMap["definition"].(map[string]any)
	if !ok || !reflect.DeepEqual(definition, wantMap) {
		t.Fatalf("content definition = %#v, want %#v", contentMap["definition"], wantMap)
	}
}

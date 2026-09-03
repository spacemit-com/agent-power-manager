// Package remote 提供基于 APC 只读 HTTP API 的 Cloud 目录实现。
package remote

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"

	"apm/internal/cloud"
	"apm/internal/utils/jsonstrict"
)

const (
	maxJSONResponse = 20 << 20
	maxProblemBody  = 8 << 10
	maxProblemField = 128
)

type problemDTO struct {
	Status  *int   `json:"status"`
	Code    string `json:"code"`
	TraceID string `json:"trace_id"`
}

// Client 通过已验证的 APC base URL 查询远端目录；HTTP Client 必须设置整体超时。
type Client struct {
	baseURL          *url.URL
	http             *http.Client
	apiKey           string
	mcpTempParent    string
	beforeMCPCleanup func(*ownedDirectory)
}

var _ cloud.Client = (*Client)(nil)

// New 创建匿名远端目录 Client；非回环 HTTP、含路径歧义的 URL 和无界 HTTP Client 均被拒绝。
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	return NewWithAPIKey(baseURL, httpClient, "")
}

// NewWithAPIKey 创建远端目录 Client 并注入 Authorization: Bearer 凭据；apiKey 为空表示匿名（仅资源读取与下载）。
func NewWithAPIKey(baseURL string, httpClient *http.Client, apiKey string) (*Client, error) {
	if httpClient == nil || httpClient.Timeout <= 0 {
		return nil, fmt.Errorf("create remote Client with bounded HTTP client: %w", cloud.ErrProviderConfig)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse remote base URL: invalid syntax: %w", cloud.ErrProviderConfig)
	}
	if err := validateBaseURL(parsed); err != nil {
		return nil, err
	}

	normalized := *parsed
	normalized.Path = ""
	normalized.RawPath = ""

	clientCopy := *httpClient
	callerRedirect := httpClient.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		anonymous := anonymousCatalogRedirect(req.Context())
		if !validRedirectRequest(&normalized, req, anonymous) {
			return fmt.Errorf("remote redirect crosses configured origin: %w", cloud.ErrInvalidResponse)
		}
		if callerRedirect != nil {
			if err := callerRedirect(req, via); err != nil {
				return err
			}
			if !validRedirectRequest(&normalized, req, anonymous) {
				return fmt.Errorf("remote redirect policy changed configured origin: %w", cloud.ErrInvalidResponse)
			}
		} else if len(via) >= 10 {
			return fmt.Errorf("remote request stopped after 10 redirects: %w", cloud.ErrInvalidResponse)
		}
		if anonymous {
			for name, values := range req.Header {
				if len(values) > 0 && isCredentialRequestHeader(name) {
					return fmt.Errorf("remote anonymous catalog redirect contains credential header %q: %w", name, cloud.ErrInvalidResponse)
				}
			}
		}
		return nil
	}

	return &Client{baseURL: &normalized, http: &clientCopy, apiKey: apiKey}, nil
}

type anonymousCatalogRedirectContextKey struct{}

// markAnonymousCatalogRedirect 标记匿名目录/内容请求：它们不携带凭据，允许跟随跨源 CDN 重定向。
func markAnonymousCatalogRedirect(ctx context.Context) context.Context {
	return context.WithValue(ctx, anonymousCatalogRedirectContextKey{}, true)
}

func anonymousCatalogRedirect(ctx context.Context) bool {
	marked, _ := ctx.Value(anonymousCatalogRedirectContextKey{}).(bool)
	return marked
}

func isCredentialRequestHeader(name string) bool {
	normalized := strings.ToLower(name)
	switch normalized {
	case "authorization", "proxy-authorization", "cookie", "cookie2", "api-key", "x-api-key", "x-auth-token", "x-access-token":
		return true
	}
	return strings.HasSuffix(normalized, "-authorization") ||
		strings.HasSuffix(normalized, "-api-key") ||
		strings.HasSuffix(normalized, "-auth-token") ||
		strings.HasSuffix(normalized, "-access-token") ||
		strings.HasSuffix(normalized, "-credential") ||
		strings.HasSuffix(normalized, "-secret")
}

// validRedirectRequest 允许同源重定向；跨源重定向仅允许匿名目录/内容请求跳转到 HTTPS CDN，
// 防止 Submission 凭据被转发到第三方。
func validRedirectRequest(baseURL *url.URL, request *http.Request, anonymous bool) bool {
	if request == nil || request.URL == nil || request.URL.User != nil {
		return false
	}
	if sameOrigin(baseURL, request.URL) {
		return true
	}
	if !anonymous || !strings.EqualFold(request.URL.Scheme, "https") {
		return false
	}
	return true
}

func validateBaseURL(baseURL *url.URL) error {
	if baseURL == nil || !baseURL.IsAbs() || baseURL.Opaque != "" || baseURL.Host == "" || baseURL.Hostname() == "" {
		return fmt.Errorf("remote base URL must be absolute and hierarchical: %w", cloud.ErrProviderConfig)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return fmt.Errorf("remote base URL uses an unsupported scheme: %w", cloud.ErrProviderConfig)
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.ForceQuery || baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return fmt.Errorf("remote base URL must not contain credentials, query, or fragment: %w", cloud.ErrProviderConfig)
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return fmt.Errorf("remote base URL path is not root: %w", cloud.ErrProviderConfig)
	}
	if baseURL.Scheme == "http" && !isLoopbackHost(baseURL.Hostname()) {
		return fmt.Errorf("remote base URL requires a loopback host for HTTP: %w", cloud.ErrProviderConfig)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, right.Scheme) || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	return originPort(left) == originPort(right)
}

func originPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "http") {
		return "80"
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return ""
}

// gatewayPathPrefix 是 APC 服务在网关后挂载的固定路径前缀；测试与生产环境均为 /api-server/apc。
// endpoint() 在其后拼接调用方传入的 /api/v1/... 相对路径。
const gatewayPathPrefix = "api-server/apc"

func (c *Client) endpoint(segments ...string) *url.URL {
	endpoint := *c.baseURL
	decoded := make([]string, len(segments)+1)
	escaped := make([]string, len(segments)+1)
	decoded[0] = gatewayPathPrefix
	escaped[0] = gatewayPathPrefix
	for index, segment := range segments {
		decoded[index+1] = segment
		escaped[index+1] = url.PathEscape(segment)
	}
	endpoint.Path = "/" + strings.Join(decoded, "/")
	endpoint.RawPath = "/" + strings.Join(escaped, "/")
	return &endpoint
}

func classifyResponseReadError(ctx context.Context, readErr error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded) {
		return readErr
	}
	var timeout net.Error
	if errors.As(readErr, &timeout) && timeout.Timeout() {
		return readErr
	}
	return errors.Join(cloud.ErrInvalidResponse, readErr)
}

// decodeProblem 解析 application/problem+json 响应体为稳定的 problemDTO；
// Content-Type、status、code、trace_id 任一不合法时返回 ok=false，且绝不暴露 title/detail 等私有字段。
func decodeProblem(response *http.Response, body []byte) (problemDTO, bool) {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/problem+json" {
		return problemDTO{}, false
	}

	var problem problemDTO
	if err := jsonstrict.Decode(body, &problem, false); err != nil {
		return problemDTO{}, false
	}
	if problem.Status == nil || *problem.Status != response.StatusCode || !isStableProblemCode(problem.Code) || !isStableProblemTraceID(problem.TraceID) {
		return problemDTO{}, false
	}
	return problem, true
}

// safeProblemContext 通过 decodeProblem 读取稳定 code/trace_id 并格式化；解码失败或字段不安全时返回空串。
func safeProblemContext(response *http.Response, body []byte) string {
	problem, ok := decodeProblem(response, body)
	if !ok {
		return ""
	}
	return formatProblemContext(problem)
}

func formatProblemContext(problem problemDTO) string {
	if problem.TraceID == "" {
		return fmt.Sprintf("code=%s", problem.Code)
	}
	return fmt.Sprintf("code=%s trace_id=%s", problem.Code, problem.TraceID)
}

func isStableProblemCode(value string) bool {
	if len(value) < 1 || len(value) > maxProblemField {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
}

func isStableProblemTraceID(value string) bool {
	if len(value) > maxProblemField {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

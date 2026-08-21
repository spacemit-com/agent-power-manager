// Package remote 提供基于 APC 只读 HTTP API 的 Cloud 目录实现。
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	baseURL            *url.URL
	http               *http.Client
	mcpTempParent      string
	beforeAgentPublish func(*stagedContent)
	beforeMCPCleanup   func(*ownedDirectory)
}

var _ cloud.Catalog = (*Client)(nil)

// New 创建远端目录 Client；非回环 HTTP、含路径歧义的 URL 和无界 HTTP Client 均被拒绝。
func New(baseURL string, httpClient *http.Client) (*Client, error) {
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
		if !validRedirectRequest(&normalized, req) {
			return fmt.Errorf("remote redirect crosses configured origin: %w", cloud.ErrInvalidResponse)
		}
		if callerRedirect != nil {
			if err := callerRedirect(req, via); err != nil {
				return err
			}
			if !validRedirectRequest(&normalized, req) {
				return fmt.Errorf("remote redirect policy changed configured origin: %w", cloud.ErrInvalidResponse)
			}
			return nil
		}
		if len(via) >= 10 {
			return fmt.Errorf("remote request stopped after 10 redirects: %w", cloud.ErrInvalidResponse)
		}
		return nil
	}

	return &Client{baseURL: &normalized, http: &clientCopy}, nil
}

func validRedirectRequest(baseURL *url.URL, request *http.Request) bool {
	return request != nil && request.URL != nil && request.URL.User == nil && sameOrigin(baseURL, request.URL)
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

func (c *Client) endpoint(segments ...string) *url.URL {
	endpoint := *c.baseURL
	decoded := make([]string, len(segments))
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		decoded[index] = segment
		escaped[index] = url.PathEscape(segment)
	}
	endpoint.Path = "/" + strings.Join(decoded, "/")
	endpoint.RawPath = "/" + strings.Join(escaped, "/")
	return &endpoint
}

func (c *Client) getJSON(ctx context.Context, endpoint *url.URL, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create remote catalog request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("request remote catalog: %w", ctxErr)
		}
		return fmt.Errorf("request remote catalog: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("remote catalog returned HTTP 404: %w", cloud.ErrNotFound)
	case http.StatusGone:
		return fmt.Errorf("remote catalog returned HTTP 410: %w", cloud.ErrYanked)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		problem, readErr := io.ReadAll(io.LimitReader(response.Body, maxProblemBody))
		if readErr != nil {
			return fmt.Errorf("read remote HTTP %d problem: %w", response.StatusCode, classifyResponseReadError(ctx, readErr))
		}
		problemContext := safeProblemContext(response, problem)
		if problemContext == "" {
			return fmt.Errorf("remote catalog returned HTTP %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
		}
		return fmt.Errorf("remote catalog returned HTTP %d %s: %w", response.StatusCode, problemContext, cloud.ErrInvalidResponse)
	}
	if response.ContentLength > maxJSONResponse {
		return fmt.Errorf("remote JSON response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil {
		return fmt.Errorf("read remote JSON response: %w", classifyResponseReadError(ctx, err))
	}
	if len(payload) > maxJSONResponse {
		return fmt.Errorf("remote JSON response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}
	if err := jsonstrict.Decode(payload, destination, false); err != nil {
		return fmt.Errorf("decode remote JSON response: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	return nil
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

func safeProblemContext(response *http.Response, body []byte) string {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/problem+json" {
		return ""
	}

	var problem problemDTO
	if err := jsonstrict.Decode(body, &problem, false); err != nil {
		return ""
	}
	if problem.Status == nil || *problem.Status != response.StatusCode || !isStableProblemCode(problem.Code) || !isStableProblemTraceID(problem.TraceID) {
		return ""
	}
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

// Package remote 提供基于 APC 只读 HTTP API 的 Cloud 目录实现。
package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"apm/internal/cloud"
)

const (
	maxJSONResponse = 20 << 20
	maxProblemBody  = 8 << 10
)

// Client 通过已验证的 APC base URL 查询远端目录；HTTP Client 必须设置整体超时。
type Client struct {
	baseURL *url.URL
	http    *http.Client
}

var _ cloud.Catalog = (*Client)(nil)

// New 创建远端目录 Client；非回环 HTTP、含路径歧义的 URL 和无界 HTTP Client 均被拒绝。
func New(baseURL string, httpClient *http.Client) (*Client, error) {
	if httpClient == nil || httpClient.Timeout <= 0 {
		return nil, fmt.Errorf("create remote Client with bounded HTTP client: %w", cloud.ErrProviderConfig)
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse remote base URL: %w", errors.Join(cloud.ErrProviderConfig, err))
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
		if req.URL.User != nil || !sameOrigin(&normalized, req.URL) {
			return fmt.Errorf("remote redirect crosses configured origin: %w", cloud.ErrInvalidResponse)
		}
		if callerRedirect != nil {
			return callerRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("remote request stopped after 10 redirects: %w", cloud.ErrInvalidResponse)
		}
		return nil
	}

	return &Client{baseURL: &normalized, http: &clientCopy}, nil
}

func validateBaseURL(baseURL *url.URL) error {
	if baseURL == nil || !baseURL.IsAbs() || baseURL.Opaque != "" || baseURL.Host == "" || baseURL.Hostname() == "" {
		return fmt.Errorf("remote base URL must be absolute and hierarchical: %w", cloud.ErrProviderConfig)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return fmt.Errorf("remote base URL scheme %q is unsupported: %w", baseURL.Scheme, cloud.ErrProviderConfig)
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.ForceQuery || baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return fmt.Errorf("remote base URL must not contain credentials, query, or fragment: %w", cloud.ErrProviderConfig)
	}
	if baseURL.Path != "" && baseURL.Path != "/" {
		return fmt.Errorf("remote base URL path %q is not root: %w", baseURL.Path, cloud.ErrProviderConfig)
	}
	if baseURL.Scheme == "http" && !isLoopbackHost(baseURL.Hostname()) {
		return fmt.Errorf("remote HTTP base URL host %q is not loopback: %w", baseURL.Hostname(), cloud.ErrProviderConfig)
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
			return fmt.Errorf("read remote HTTP %d problem: %w", response.StatusCode, errors.Join(cloud.ErrInvalidResponse, readErr))
		}
		detail := strings.TrimSpace(string(problem))
		if detail == "" {
			return fmt.Errorf("remote catalog returned HTTP %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
		}
		return fmt.Errorf("remote catalog returned HTTP %d: %s: %w", response.StatusCode, detail, cloud.ErrInvalidResponse)
	}
	if response.ContentLength > maxJSONResponse {
		return fmt.Errorf("remote JSON response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}

	payload, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil {
		return fmt.Errorf("read remote JSON response: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if len(payload) > maxJSONResponse {
		return fmt.Errorf("remote JSON response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode remote JSON response: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode remote JSON response with trailing value: %w", cloud.ErrInvalidResponse)
		}
		return fmt.Errorf("decode remote JSON response trailing data: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	return nil
}

package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"apm/internal/cloud"
	"apm/internal/cloud/local"
	"apm/internal/cloud/remote"
)

const (
	defaultCloudProvider = cloud.ProviderRemote
	// defaultAPCBaseURL 是 APM 默认使用的 APC 根地址（生产环境，规范 www 主机）。
	// 只接受 host-only 根地址，网关前缀 /api-server/apc 由 remote.Client.endpoint 统一拼接。
	defaultAPCBaseURL = "https://www.spacemit.com"
	cloudProviderEnv  = "APM_CLOUD_PROVIDER"
	apcBaseURLEnv     = "APC_BASE_URL"
	apcAPIKeyEnv      = "APC_API_KEY"
	localRootEnv      = "APM_CLOUD_LOCAL_ROOT"
)

// newCloudClient 为单次命令创建独立 Provider Registry，并且只打开显式选中的提供方。
// Provider factory 必须保持惰性，禁止因配置错误或远端失败自动回退到另一提供方。
func newCloudClient() (cloud.Client, error) {
	registry := cloud.NewProviderRegistry()
	if err := registry.Register(cloud.ProviderLocal, func() (cloud.Client, error) {
		root := os.Getenv(localRootEnv)
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return nil, fmt.Errorf("configure Local from %s=%q: clean absolute resource root is required: %w", localRootEnv, root, cloud.ErrProviderConfig)
		}
		if err := validatePhysicalLocalRoot(root); err != nil {
			return nil, fmt.Errorf("configure Local Cloud provider from %s: %w", localRootEnv, errors.Join(cloud.ErrProviderConfig, err))
		}
		client, err := local.New(root)
		if err != nil {
			return nil, fmt.Errorf("create Local Cloud client from %s=%q: %w", localRootEnv, root, errors.Join(cloud.ErrProviderConfig, err))
		}
		return client, nil
	}); err != nil {
		return nil, fmt.Errorf("register Local Cloud provider: %w", err)
	}
	if err := registry.Register(cloud.ProviderRemote, func() (cloud.Client, error) {
		baseURL := os.Getenv(apcBaseURLEnv)
		if baseURL == "" {
			baseURL = defaultAPCBaseURL
		}
		client, err := remote.NewWithAPIKey(baseURL, newRemoteHTTPClient(), os.Getenv(apcAPIKeyEnv))
		if err != nil {
			return nil, fmt.Errorf("configure Remote Cloud provider from %s: invalid value: %w", apcBaseURLEnv, cloud.ErrProviderConfig)
		}
		return client, nil
	}); err != nil {
		return nil, fmt.Errorf("register Remote Cloud provider: %w", err)
	}

	provider := cloud.ProviderID(os.Getenv(cloudProviderEnv))
	if provider == "" {
		provider = defaultCloudProvider
	}
	client, err := registry.Open(provider)
	if err != nil {
		return nil, fmt.Errorf("select Cloud provider from %s=%q: %w", cloudProviderEnv, provider, err)
	}
	return client, nil
}

// requireAPIKey 校验 APC_API_KEY 已配置；错误信息不打印完整 Key。
func requireAPIKey() error {
	if strings.TrimSpace(os.Getenv(apcAPIKeyEnv)) == "" {
		return fmt.Errorf("submission requires %s: %w", apcAPIKeyEnv, cloud.ErrProviderConfig)
	}
	return nil
}

func newRemoteHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       2 * time.Minute,
		CheckRedirect: redirectPolicy,
	}
}

// redirectPolicy 是 composition root 的重定向纵深防线：允许同源重定向，或匿名资源请求跨源跳转到
// HTTPS CDN（测试环境 catalog/content 302 到 cdn-resource.spacemit.com），拒绝降级到 HTTP 或携带
// userinfo。认证请求的跨源重定向由 Remote Client 内部的凭据保护先行拒绝。
func redirectPolicy(request *http.Request, via []*http.Request) error {
	if request == nil || request.URL == nil || request.URL.User != nil ||
		len(via) == 0 || via[0] == nil || via[0].URL == nil || via[0].URL.User != nil {
		return errors.New("reject Remote redirect without an original origin")
	}
	if sameHTTPOrigin(via[0].URL, request.URL) {
		return nil
	}
	if !strings.EqualFold(request.URL.Scheme, "https") {
		return fmt.Errorf("reject cross-origin Remote redirect to non-HTTPS %q", request.URL.Scheme)
	}
	return nil
}

func sameHTTPOrigin(left, right *url.URL) bool {
	return left != nil && right != nil &&
		strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		redirectOriginPort(left) == redirectOriginPort(right)
}

func redirectOriginPort(value *url.URL) string {
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

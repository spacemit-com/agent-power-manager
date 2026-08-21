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
	defaultAPCBaseURL    = "http://127.0.0.1:8080"
	cloudProviderEnv     = "APM_CLOUD_PROVIDER"
	apcBaseURLEnv        = "APC_BASE_URL"
	localRootEnv         = "APM_CLOUD_LOCAL_ROOT"
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
		client, err := remote.New(baseURL, newRemoteHTTPClient())
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

// validatePhysicalLocalRoot 从文件系统根向下检查每个路径组件，禁止跟随祖先或终端符号链接。
func validatePhysicalLocalRoot(root string) error {
	components := make([]string, 0, 8)
	for current := root; ; current = filepath.Dir(current) {
		components = append(components, current)
		if parent := filepath.Dir(current); parent == current {
			break
		}
	}
	for index := len(components) - 1; index >= 0; index-- {
		component := components[index]
		info, err := os.Lstat(component)
		if err != nil {
			return fmt.Errorf("inspect Local resource root component %q: %w", component, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Local resource root component %q is a symbolic link", component)
		}
		if !info.IsDir() {
			return fmt.Errorf("Local resource root component %q is not a directory", component)
		}
	}
	return nil
}

func newRemoteHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       2 * time.Minute,
		CheckRedirect: sameOriginOnly,
	}
}

// sameOriginOnly 是 composition root 的跨源重定向纵深防线；Remote Client 仍负责最终的 base origin 校验和错误分类。
func sameOriginOnly(request *http.Request, via []*http.Request) error {
	if request == nil || request.URL == nil || request.URL.User != nil ||
		len(via) == 0 || via[0] == nil || via[0].URL == nil || via[0].URL.User != nil {
		return errors.New("reject Remote redirect without an original origin")
	}
	if !sameHTTPOrigin(via[0].URL, request.URL) {
		return fmt.Errorf("reject cross-origin Remote redirect from %q to %q", via[0].URL.Host, request.URL.Host)
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

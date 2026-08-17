// Package pluginresource 定义本地 Plugin artifact 的资源契约。
package pluginresource

import "errors"

var (
	// ErrMiss 表示未找到缓存的 Plugin artifact。
	ErrMiss = errors.New("plugin artifact cache miss")
	// ErrInvalidArtifact 表示 Plugin artifact 不满足资源约束。
	ErrInvalidArtifact = errors.New("invalid plugin artifact")
	// ErrUnsupportedAgent 表示该 Agent 不支持 Plugin artifact。
	ErrUnsupportedAgent = errors.New("unsupported plugin artifact agent")
)

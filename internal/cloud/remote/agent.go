package remote

import (
	"context"
	"fmt"
	"os"

	"apm/internal/agentartifact"
	"apm/internal/cloud"
	"apm/internal/cloud/contentpolicy"
	"apm/internal/domain"
)

var _ cloud.AgentSource = (*Client)(nil)

// agentArchivePolicy 构造 Remote Agent 的归档策略：agent.yaml 必须为安全文本，
// 其余路径（assets/ 资产）允许二进制；解包后在仍为私有的输出目录句柄上执行
// agentartifact 语义校验（fd 相对，不经过可被重定向的路径），失败时清理私有
// 输出并保持目标目录缺席。
func agentArchivePolicy(ctx context.Context, ref domain.PowerRef) *archivePolicy {
	return &archivePolicy{
		validateFile: func(path string, body []byte) error {
			if path != "agent.yaml" {
				return nil
			}
			if contentpolicy.IsUnsafeText(body) {
				return unsafeRemoteFilesystem("remote Agent agent.yaml contains unsafe non-text content")
			}
			return nil
		},
		validateTree: func(directory *os.File) error {
			if _, err := agentartifact.ValidateDirectoryFd(ctx, directory, &ref); err != nil {
				return err
			}
			return nil
		},
	}
}

// DownloadAgent 验证精确版本 Agent 归档（application/gzip）与根 agent.yaml marker，
// 解包后在私有输出上执行 agentartifact 语义校验，再以不覆盖语义发布到调用方的
// 物理空目录；任何失败都不留下可见目标。
func (c *Client) DownloadAgent(ctx context.Context, entry domain.CatalogEntry, destination string) error {
	if entry.Ref.Type != domain.PowerTypeExpert || !validResolveQuery(entry.Ref) || entry.Ref.Version == "" {
		return fmt.Errorf("download invalid remote Agent identity: %w", cloud.ErrNotFound)
	}
	return c.downloadArchivePower(ctx, entry, destination, agentArchivePolicy(ctx, entry.Ref), "agent.yaml")
}

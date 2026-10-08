package local

import (
	"context"

	"apm/internal/domain"
)

// listAgents 枚举 agents 目录下的版本化 agent 资源（以 agent.yaml 为 marker）。
func (c *Client) listAgents(ctx context.Context) ([]domain.PowerRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.listResources(ctx, "agents", domain.PowerTypeExpert, "agent.yaml")
}

// agentLocalTreeContentPolicy 是 Local Agent 的物理快照内容策略：
// agent.yaml 必须为安全文本，其余路径（assets/ 资产）允许二进制；
// 可执行文件、闭集与 avatar 规则由 agentartifact.Validate 统一把关。
func agentLocalTreeContentPolicy(relative string, body []byte) error {
	if relative != "agent.yaml" {
		return nil
	}
	if isUnsafePayload(body) {
		return unsafeContent("Local Agent agent.yaml contains binary or archive content")
	}
	return nil
}

// DownloadAgent 将精确版本 Agent 版本目录原子复制至调用方提供的私有目录，不访问全局缓存；
// destination 必须不存在或为空的物理目录，失败时不留任何可见输出。
func (c *Client) DownloadAgent(ctx context.Context, entry domain.CatalogEntry, destination string) (resultErr error) {
	content, err := c.loadMatchingCatalogContent(ctx, entry, domain.PowerTypeExpert)
	if err != nil {
		return err
	}
	source, err := c.catalogSourcePath(entry.Ref)
	if err != nil {
		return err
	}
	destination, err = absoluteDestination(destination)
	if err != nil {
		return err
	}
	if err := validateTreeDestination(destination); err != nil {
		return err
	}
	if err := validateCopyBoundary(source, destination); err != nil {
		return err
	}
	if content.tree == nil {
		return unsafeContent("Local Agent catalog content has no captured tree")
	}
	return copyTreeAtomically(ctx, content.tree, destination, "Agent", c.beforeAgentPublish)
}

package remote

import (
	"context"
	"fmt"

	"apm/internal/cloud"
	"apm/internal/domain"
)

type latestMetadataDTO struct {
	Type    domain.PowerType `json:"type"`
	Name    string           `json:"name"`
	Version string           `json:"version"`
}

type exactMetadataDTO struct {
	resourceMetadataDTO
	Status     string `json:"status"`
	YankedAt   string `json:"yanked_at"`
	YankReason string `json:"yank_reason"`
}

// Resolve 将同类型同名称的 latest 或精确版本查询解析为精确 PowerRef；响应身份必须与请求一致。
func (c *Client) Resolve(ctx context.Context, query domain.PowerRef) (domain.PowerRef, error) {
	if !validResolveQuery(query) {
		return domain.PowerRef{}, fmt.Errorf("resolve invalid remote resource identity: %w", cloud.ErrNotFound)
	}
	if query.Version == "" {
		return c.resolveLatest(ctx, query)
	}
	return c.resolveExact(ctx, query)
}

func (c *Client) resolveLatest(ctx context.Context, query domain.PowerRef) (domain.PowerRef, error) {
	endpoint := c.endpoint("api", "v1", "resources", string(query.Type), query.Name, "latest")
	var metadata latestMetadataDTO
	if err := c.getJSON(ctx, endpoint, &metadata); err != nil {
		return domain.PowerRef{}, fmt.Errorf("resolve latest remote resource %s/%s: %w", query.Type, query.Name, err)
	}
	resolved, _, err := validatePowerIdentity(metadata.Type, metadata.Name, metadata.Version)
	if err != nil {
		return domain.PowerRef{}, fmt.Errorf("resolve latest remote resource metadata: %w", err)
	}
	if resolved.Type != query.Type || resolved.Name != query.Name {
		return domain.PowerRef{}, fmt.Errorf("latest remote resource identity does not match request: %w", cloud.ErrInvalidResponse)
	}
	return resolved, nil
}

func (c *Client) resolveExact(ctx context.Context, query domain.PowerRef) (domain.PowerRef, error) {
	endpoint := c.endpoint("api", "v1", "resources", string(query.Type), query.Name, "versions", query.Version)
	var metadata exactMetadataDTO
	if err := c.getJSON(ctx, endpoint, &metadata); err != nil {
		return domain.PowerRef{}, fmt.Errorf("resolve exact remote resource %s/%s/%s: %w", query.Type, query.Name, query.Version, err)
	}
	resolved, _, err := validateResourceMetadata(metadata.resourceMetadataDTO)
	if err != nil {
		return domain.PowerRef{}, fmt.Errorf("resolve exact remote resource metadata: %w", err)
	}
	if resolved != query {
		return domain.PowerRef{}, fmt.Errorf("exact remote resource identity does not match request: %w", cloud.ErrInvalidResponse)
	}
	switch metadata.Status {
	case "published":
		return resolved, nil
	case "yanked":
		return domain.PowerRef{}, fmt.Errorf("exact remote resource is yanked: %w", cloud.ErrYanked)
	default:
		return domain.PowerRef{}, fmt.Errorf("exact remote resource has invalid status %q: %w", metadata.Status, cloud.ErrInvalidResponse)
	}
}

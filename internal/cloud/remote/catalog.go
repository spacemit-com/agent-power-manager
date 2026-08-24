package remote

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"apm/internal/cloud"
	"apm/internal/domain"
	"github.com/Masterminds/semver/v3"
)

type catalogPageDTO struct {
	SnapshotUpdatedAt string                `json:"snapshot_updated_at"`
	Items             []resourceMetadataDTO `json:"items"`
	NextCursor        string                `json:"next_cursor"`
}

type resourceMetadataDTO struct {
	Type        domain.PowerType `json:"type"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Description string           `json:"description"`
	Message     string           `json:"message"`
	ContentType string           `json:"content_type"`
	ContentSize *int64           `json:"content_size"`
	ContentMD5  string           `json:"content_md5"`
	PublishedAt string           `json:"published_at"`
}

// List 遍历同一 APC 快照的全部 cursor 页，并按类型、名称和 SemVer 升序返回精确版本。
func (c *Client) List(ctx context.Context) ([]domain.PowerRef, error) {
	refs := make([]domain.PowerRef, 0)
	seenRefs := make(map[domain.PowerRef]struct{})
	seenCursors := make(map[string]struct{})
	firstSnapshot := ""
	cursor := ""

	for {
		endpoint := c.endpoint("api", "v1", "resources")
		query := endpoint.Query()
		query.Set("limit", "100")
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		endpoint.RawQuery = query.Encode()

		var page catalogPageDTO
		if err := c.getJSON(ctx, endpoint, &page); err != nil {
			return nil, fmt.Errorf("list remote resources: %w", err)
		}
		if page.Items == nil {
			return nil, fmt.Errorf("list remote resources with missing items: %w", cloud.ErrInvalidResponse)
		}
		if err := validateAPCTimestamp(page.SnapshotUpdatedAt); err != nil {
			return nil, fmt.Errorf("list remote resources with invalid snapshot timestamp: %w", errors.Join(cloud.ErrInvalidResponse, err))
		}
		if firstSnapshot == "" {
			firstSnapshot = page.SnapshotUpdatedAt
		} else if page.SnapshotUpdatedAt != firstSnapshot {
			return nil, fmt.Errorf("list remote resources changed snapshot: %w", cloud.ErrInvalidResponse)
		}

		for _, item := range page.Items {
			ref, _, err := validateResourceMetadata(item)
			if err != nil {
				return nil, fmt.Errorf("list remote resource metadata: %w", err)
			}
			if _, duplicate := seenRefs[ref]; duplicate {
				return nil, fmt.Errorf("list remote resources contains duplicate %s/%s/%s: %w", ref.Type, ref.Name, ref.Version, cloud.ErrInvalidResponse)
			}
			seenRefs[ref] = struct{}{}
			refs = append(refs, ref)
		}

		if page.NextCursor == "" {
			break
		}
		if _, duplicate := seenCursors[page.NextCursor]; duplicate {
			return nil, fmt.Errorf("list remote resources repeats cursor: %w", cloud.ErrInvalidResponse)
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}

	sort.SliceStable(refs, func(left, right int) bool {
		if refs[left].Type != refs[right].Type {
			return refs[left].Type < refs[right].Type
		}
		if refs[left].Name != refs[right].Name {
			return refs[left].Name < refs[right].Name
		}
		leftVersion, _ := canonicalVersion(refs[left].Version)
		rightVersion, _ := canonicalVersion(refs[right].Version)
		return leftVersion.LessThan(rightVersion)
	})
	return refs, nil
}

func validateResourceMetadata(metadata resourceMetadataDTO) (domain.PowerRef, *semver.Version, error) {
	ref, version, err := validatePowerIdentity(metadata.Type, metadata.Name, metadata.Version)
	if err != nil {
		return domain.PowerRef{}, nil, err
	}
	if strings.TrimSpace(metadata.Message) == "" {
		return domain.PowerRef{}, nil, fmt.Errorf("resource %s/%s/%s has empty message: %w", ref.Type, ref.Name, ref.Version, cloud.ErrInvalidResponse)
	}
	if metadata.ContentSize == nil || *metadata.ContentSize < 0 {
		return domain.PowerRef{}, nil, fmt.Errorf("resource %s/%s/%s has invalid content size: %w", ref.Type, ref.Name, ref.Version, cloud.ErrInvalidResponse)
	}
	if !isCanonicalMD5(metadata.ContentMD5) {
		return domain.PowerRef{}, nil, fmt.Errorf("resource %s/%s/%s has invalid MD5: %w", ref.Type, ref.Name, ref.Version, cloud.ErrInvalidResponse)
	}
	if metadata.ContentType != expectedContentType(ref.Type) {
		return domain.PowerRef{}, nil, fmt.Errorf("resource %s/%s/%s has content type %q: %w", ref.Type, ref.Name, ref.Version, metadata.ContentType, cloud.ErrInvalidResponse)
	}
	if err := validateAPCTimestamp(metadata.PublishedAt); err != nil {
		return domain.PowerRef{}, nil, fmt.Errorf("resource %s/%s/%s has invalid publish timestamp: %w", ref.Type, ref.Name, ref.Version, errors.Join(cloud.ErrInvalidResponse, err))
	}
	return ref, version, nil
}

func validateAPCTimestamp(value string) error {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return err
	}
	_, offset := parsed.Zone()
	if offset != 0 || (!strings.HasSuffix(value, "Z") && !strings.HasSuffix(value, "+00:00")) {
		return errors.New("timestamp is not RFC3339 UTC")
	}
	return nil
}

func validatePowerIdentity(powerType domain.PowerType, name, version string) (domain.PowerRef, *semver.Version, error) {
	if !isKnownPowerType(powerType) || !domain.IsCanonicalPowerName(name) {
		return domain.PowerRef{}, nil, fmt.Errorf("invalid resource identity %q/%q: %w", powerType, name, cloud.ErrInvalidResponse)
	}
	parsed, valid := canonicalVersion(version)
	if !valid {
		return domain.PowerRef{}, nil, fmt.Errorf("invalid resource version %q: %w", version, cloud.ErrInvalidResponse)
	}
	return domain.PowerRef{Type: powerType, Name: name, Version: version}, parsed, nil
}

func validResolveQuery(ref domain.PowerRef) bool {
	if !isKnownPowerType(ref.Type) || !domain.IsCanonicalPowerName(ref.Name) {
		return false
	}
	if ref.Version == "" {
		return true
	}
	_, valid := canonicalVersion(ref.Version)
	return valid
}

func isKnownPowerType(powerType domain.PowerType) bool {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin, domain.PowerTypeMCP, domain.PowerTypeAgent:
		return true
	default:
		return false
	}
}

func canonicalVersion(version string) (*semver.Version, bool) {
	if !strings.HasPrefix(version, "v") {
		return nil, false
	}
	native := strings.TrimPrefix(version, "v")
	parsed, err := semver.StrictNewVersion(native)
	if err != nil || parsed.Prerelease() != "" || parsed.Metadata() != "" || parsed.String() != native {
		return nil, false
	}
	return parsed, true
}

func isCanonicalMD5(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range []byte(value) {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return false
	}
	return true
}

func expectedContentType(powerType domain.PowerType) string {
	switch powerType {
	case domain.PowerTypeSkill, domain.PowerTypePlugin:
		return "application/gzip"
	case domain.PowerTypeMCP:
		return "application/json"
	case domain.PowerTypeAgent:
		return "application/yaml"
	default:
		return ""
	}
}

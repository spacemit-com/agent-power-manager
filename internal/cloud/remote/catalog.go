package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
	"github.com/Masterminds/semver/v3"
)

var _ cloud.CatalogSource = (*Client)(nil)

const remoteCatalogSchemaVersion = 1

// CatalogVersion 匿名读取 APC 的 catalog v1 内容标记；响应字段必须精确且目录大小不得超过本地上限。
func (c *Client) CatalogVersion(ctx context.Context) (version domain.CatalogVersion, resultErr error) {
	if err := ctx.Err(); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("get remote catalog version: %w", err)
	}
	response, err := c.requestCatalogResponse(ctx, c.endpoint("api", "v1", "resources", "version"))
	if err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("get remote catalog version: %w", err)
	}
	defer func() {
		if closeErr := closeCatalogResponse(ctx, response); closeErr != nil {
			version = domain.CatalogVersion{}
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	if err := requireCatalogJSONContentType(response); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("get remote catalog version: %w", err)
	}
	if response.ContentLength > maxJSONResponse {
		return domain.CatalogVersion{}, fmt.Errorf("remote catalog version response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}
	payload, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, maxJSONResponse+1))
	if err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("read remote catalog version: %w", classifyResponseReadError(ctx, err))
	}
	if len(payload) > maxJSONResponse {
		return domain.CatalogVersion{}, fmt.Errorf("remote catalog version response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}
	version, err = decodeCatalogVersion(payload)
	if err != nil {
		return domain.CatalogVersion{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("get remote catalog version: %w", err)
	}
	return version, nil
}

// DownloadCatalog 匿名下载完整 catalog 原始字节到调用方提供的尚不存在绝对路径；
// 本方法只校验传输边界，不解析 catalog schema。
func (c *Client) DownloadCatalog(ctx context.Context, destination string) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	parent, destinationName, err := prepareCatalogDestination(destination)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, catalogDestinationFailure("close remote catalog destination parent", parent.close()))
	}()

	response, err := c.requestCatalogResponse(ctx, c.endpoint("api", "v1", "resources"))
	if err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	responseClosed := false
	defer func() {
		if !responseClosed {
			resultErr = errors.Join(resultErr, closeCatalogResponse(ctx, response))
		}
	}()
	if err := requireCatalogJSONContentType(response); err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	if response.ContentLength < 0 {
		return fmt.Errorf("remote catalog Content-Length %d must be explicit and non-negative: %w", response.ContentLength, cloud.ErrInvalidResponse)
	}
	if response.ContentLength > catalog.MaxCatalogSize {
		return fmt.Errorf("remote catalog Content-Length %d exceeds %d bytes: %w", response.ContentLength, catalog.MaxCatalogSize, cloud.ErrInvalidResponse)
	}

	staged, output, err := parent.createNamedStaged(destinationName)
	if err != nil {
		return catalogDestinationFailure("create remote catalog destination", err)
	}
	outputClosed := false
	defer func() {
		if !outputClosed {
			resultErr = errors.Join(resultErr, catalogDestinationFailure("close remote catalog destination", output.Close()))
		}
		if resultErr != nil {
			resultErr = errors.Join(resultErr, catalogDestinationFailure("cleanup remote catalog destination", staged.remove()))
		}
	}()

	written := &catalogDestinationWriter{writer: output}
	actualSize, copyErr := io.Copy(written, io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, response.ContentLength+1))
	if copyErr != nil {
		if written.err != nil {
			return catalogDestinationFailure("write remote catalog destination", written.err)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("stream remote catalog: %w", ctxErr)
		}
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("remote catalog ended before Content-Length %d: %w", response.ContentLength, errors.Join(cloud.ErrIntegrity, copyErr))
		}
		return fmt.Errorf("stream remote catalog: %w", classifyResponseReadError(ctx, copyErr))
	}
	if prepareErr := staged.prepareForClose(output, 0o600); prepareErr != nil {
		return catalogDestinationFailure("prepare remote catalog destination", prepareErr)
	}
	if closeErr := output.Close(); closeErr != nil {
		outputClosed = true
		return catalogDestinationFailure("close remote catalog destination", closeErr)
	}
	outputClosed = true
	if closeErr := closeCatalogResponse(ctx, response); closeErr != nil {
		responseClosed = true
		return closeErr
	}
	responseClosed = true
	if actualSize != response.ContentLength {
		return fmt.Errorf("remote catalog size %d does not match Content-Length %d: %w", actualSize, response.ContentLength, cloud.ErrIntegrity)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	return nil
}

func (c *Client) requestCatalogResponse(ctx context.Context, endpoint fmt.Stringer) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create remote catalog request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("request remote catalog: %w", ctxErr)
		}
		return nil, fmt.Errorf("request remote catalog: %w", err)
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return response, nil
	}
	problem, readErr := io.ReadAll(io.LimitReader(response.Body, maxProblemBody))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read remote catalog HTTP %d response: %w", response.StatusCode, errors.Join(classifyResponseReadError(ctx, readErr), classifyResponseReadError(ctx, closeErr)))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("request remote catalog: %w", ctxErr)
	}
	if problemContext := safeProblemContext(response, problem); problemContext != "" {
		return nil, fmt.Errorf("remote catalog returned HTTP %d %s: %w", response.StatusCode, problemContext, cloud.ErrInvalidResponse)
	}
	return nil, fmt.Errorf("remote catalog returned HTTP %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
}

func requireCatalogJSONContentType(response *http.Response) error {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("remote catalog has content type %q: %w", response.Header.Get("Content-Type"), cloud.ErrInvalidResponse)
	}
	return nil
}

func closeCatalogResponse(ctx context.Context, response *http.Response) error {
	if response == nil || response.Body == nil {
		return nil
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close remote catalog response: %w", classifyResponseReadError(ctx, err))
	}
	return nil
}

func decodeCatalogVersion(payload []byte) (domain.CatalogVersion, error) {
	var fields map[string]json.RawMessage
	if err := jsonstrict.Decode(payload, &fields, false); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("decode remote catalog version: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	required := []string{"schema_version", "catalog_sha256", "catalog_size"}
	if len(fields) != len(required) {
		return domain.CatalogVersion{}, fmt.Errorf("remote catalog version must contain exactly schema_version, catalog_sha256, and catalog_size: %w", cloud.ErrInvalidResponse)
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return domain.CatalogVersion{}, fmt.Errorf("remote catalog version has missing or null %s: %w", key, cloud.ErrInvalidResponse)
		}
	}

	var schemaVersion int
	if err := jsonstrict.Decode(fields["schema_version"], &schemaVersion, true); err != nil || schemaVersion != remoteCatalogSchemaVersion {
		return domain.CatalogVersion{}, fmt.Errorf("remote catalog schema version must be %d: %w", remoteCatalogSchemaVersion, errors.Join(cloud.ErrInvalidResponse, err))
	}
	var version domain.CatalogVersion
	if err := jsonstrict.Decode(fields["catalog_sha256"], &version.SHA256, true); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("decode remote catalog SHA-256: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if err := jsonstrict.Decode(fields["catalog_size"], &version.Size, true); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("decode remote catalog size: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if err := version.Validate(); err != nil {
		return domain.CatalogVersion{}, fmt.Errorf("validate remote catalog version: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if version.Size > catalog.MaxCatalogSize {
		return domain.CatalogVersion{}, fmt.Errorf("remote catalog size %d exceeds %d bytes: %w", version.Size, catalog.MaxCatalogSize, cloud.ErrInvalidResponse)
	}
	return version, nil
}

func prepareCatalogDestination(destination string) (*pinnedDirectory, string, error) {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return nil, "", catalogDestinationFailure("validate remote catalog destination", fmt.Errorf("destination %q must be an absolute clean path", destination))
	}
	name := filepath.Base(destination)
	if !validSecureEntryName(name) {
		return nil, "", catalogDestinationFailure("validate remote catalog destination", fmt.Errorf("destination %q has an invalid base name", destination))
	}
	parent, err := openPinnedDirectory(filepath.Dir(destination))
	if err != nil {
		return nil, "", catalogDestinationFailure("open remote catalog destination parent", err)
	}
	if err := parent.requireAbsent(name); err != nil {
		return nil, "", errors.Join(catalogDestinationFailure("inspect remote catalog destination", err), catalogDestinationFailure("close remote catalog destination parent", parent.close()))
	}
	return parent, name, nil
}

func catalogDestinationFailure(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(cloud.ErrCatalogDestination, err))
}

type catalogDestinationWriter struct {
	writer io.Writer
	err    error
}

func (writer *catalogDestinationWriter) Write(buffer []byte) (int, error) {
	written, err := writer.writer.Write(buffer)
	if err == nil && written != len(buffer) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = err
	}
	return written, err
}

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

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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"apm/internal/catalog"
	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
	"github.com/Masterminds/semver/v3"
)

var _ cloud.CatalogSource = (*Client)(nil)

const remoteCatalogSchemaVersion = 1

type catalogDownloadHooks struct {
	beforeFinalize func(*stagedContent)
	closeOutput    func(*os.File) error
	closeResponse  func(context.Context, *http.Response) error
	closeParent    func(*pinnedDirectory) error
}

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
	return c.downloadCatalogWithHooks(ctx, destination, catalogDownloadHooks{})
}

func (c *Client) downloadCatalogWithHooks(ctx context.Context, destination string, hooks catalogDownloadHooks) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	parent, destinationName, err := prepareCatalogDestination(destination)
	if err != nil {
		return err
	}
	var response *http.Response
	var staged *stagedContent
	var output *os.File
	defer func() {
		if output != nil {
			resultErr = errors.Join(resultErr, catalogDestinationFailure("close remote catalog destination", closeCatalogOutput(output, hooks)))
		}
		if response != nil {
			resultErr = errors.Join(resultErr, closeCatalogDownloadResponse(ctx, response, hooks))
		}
		if staged != nil && resultErr != nil {
			resultErr = errors.Join(resultErr, catalogDestinationFailure("cleanup remote catalog destination", staged.remove()))
		}
		resultErr = errors.Join(resultErr, catalogDestinationFailure("close remote catalog destination parent", closeCatalogParent(parent, hooks)))
	}()

	response, err = c.requestCatalogResponse(ctx, c.endpoint("api", "v1", "resources"))
	if err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	if err := requireCatalogJSONContentType(response); err != nil {
		return fmt.Errorf("download remote catalog: %w", err)
	}
	contentLength, err := requireCatalogContentLength(response)
	if err != nil {
		return err
	}

	staged, output, err = parent.createNamedStaged(destinationName)
	if err != nil {
		return catalogDestinationFailure("create remote catalog destination", err)
	}

	written := &catalogDestinationWriter{writer: output}
	actualSize, copyErr := io.Copy(written, io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, contentLength+1))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("stream remote catalog: %w", ctxErr)
	}
	if copyErr != nil {
		if written.err != nil {
			return catalogDestinationFailure("write remote catalog destination", written.err)
		}
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("remote catalog ended before Content-Length %d: %w", contentLength, errors.Join(cloud.ErrIntegrity, copyErr))
		}
		return fmt.Errorf("stream remote catalog: %w", classifyResponseReadError(ctx, copyErr))
	}
	if actualSize != contentLength {
		return fmt.Errorf("remote catalog size %d does not match Content-Length %d: %w", actualSize, contentLength, cloud.ErrIntegrity)
	}
	if hooks.beforeFinalize != nil {
		hooks.beforeFinalize(staged)
	}
	if prepareErr := staged.prepareForClose(output, 0o600); prepareErr != nil {
		return catalogDestinationFailure("prepare remote catalog destination", prepareErr)
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
	request = request.WithContext(markAnonymousCatalogRedirect(request.Context()))
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

func requireCatalogContentLength(response *http.Response) (int64, error) {
	values := response.Header.Values("Content-Length")
	if len(values) != 1 || values[0] == "" {
		return 0, fmt.Errorf("remote catalog must contain one explicit Content-Length header: %w", cloud.ErrInvalidResponse)
	}
	for _, character := range []byte(values[0]) {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("remote catalog Content-Length %q is not unsigned decimal: %w", values[0], cloud.ErrInvalidResponse)
		}
	}
	contentLength, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse remote catalog Content-Length %q: %w", values[0], errors.Join(cloud.ErrInvalidResponse, err))
	}
	if contentLength != response.ContentLength {
		return 0, fmt.Errorf("remote catalog Content-Length header %d does not match parsed response length %d: %w", contentLength, response.ContentLength, cloud.ErrInvalidResponse)
	}
	if contentLength > catalog.MaxCatalogSize {
		return 0, fmt.Errorf("remote catalog Content-Length %d exceeds %d bytes: %w", contentLength, catalog.MaxCatalogSize, cloud.ErrInvalidResponse)
	}
	return contentLength, nil
}

func closeCatalogResponse(ctx context.Context, response *http.Response) error {
	if response == nil || response.Body == nil {
		return nil
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close remote catalog response: %w", errors.Join(classifyResponseReadError(ctx, err), err))
	}
	return nil
}

func closeCatalogOutput(output *os.File, hooks catalogDownloadHooks) error {
	if hooks.closeOutput != nil {
		return hooks.closeOutput(output)
	}
	return output.Close()
}

func closeCatalogDownloadResponse(ctx context.Context, response *http.Response, hooks catalogDownloadHooks) error {
	if hooks.closeResponse != nil {
		return hooks.closeResponse(ctx, response)
	}
	return closeCatalogResponse(ctx, response)
}

func closeCatalogParent(parent *pinnedDirectory, hooks catalogDownloadHooks) error {
	if hooks.closeParent != nil {
		return hooks.closeParent(parent)
	}
	return parent.close()
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

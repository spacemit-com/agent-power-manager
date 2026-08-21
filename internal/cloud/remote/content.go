package remote

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"apm/internal/cloud"
	"apm/internal/domain"
)

const maxRemoteContentSize = int64(20 << 20)

func (c *Client) downloadVerifiedContent(
	ctx context.Context,
	ref domain.PowerRef,
	expectedType string,
	tempParent string,
) (temporaryPath string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("download remote content: %w", err)
	}
	if err := validateRemotePhysicalDirectory(tempParent); err != nil {
		return "", err
	}
	metadata, err := c.getExactMetadata(ctx, ref)
	if err != nil {
		return "", err
	}
	contentSize := *metadata.ContentSize
	if contentSize > maxRemoteContentSize {
		return "", fmt.Errorf("remote content size %d exceeds %d bytes: %w", contentSize, maxRemoteContentSize, cloud.ErrUnsafeContent)
	}
	if metadata.ContentType != expectedType {
		return "", fmt.Errorf("remote metadata content type %q does not match %q: %w", metadata.ContentType, expectedType, cloud.ErrInvalidResponse)
	}

	endpoint := c.endpoint("api", "v1", "resources", string(ref.Type), ref.Name, "versions", ref.Version, "content")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", fmt.Errorf("create remote content request: %w", err)
	}
	request.Header.Set("Accept", expectedType)
	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("request remote content: %w", ctxErr)
		}
		return "", fmt.Errorf("request remote content: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNotFound:
		return "", fmt.Errorf("remote content returned HTTP 404: %w", cloud.ErrNotFound)
	case http.StatusGone:
		return "", fmt.Errorf("remote content returned HTTP 410: %w", cloud.ErrYanked)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("remote content returned HTTP %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != expectedType {
		return "", fmt.Errorf("remote content has media type %q: %w", response.Header.Get("Content-Type"), cloud.ErrInvalidResponse)
	}
	if response.ContentLength >= 0 && response.ContentLength != contentSize {
		return "", fmt.Errorf("remote Content-Length %d does not match metadata size %d: %w", response.ContentLength, contentSize, cloud.ErrIntegrity)
	}

	temporary, err := os.CreateTemp(tempParent, ".apm-content-")
	if err != nil {
		return "", fmt.Errorf("create remote content temporary file in %q: %w", tempParent, err)
	}
	createdPath := temporary.Name()
	temporaryPath = createdPath
	defer func() {
		if resultErr != nil {
			if err := os.Remove(createdPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				resultErr = errors.Join(resultErr, fmt.Errorf("remove remote content temporary file %q: %w", createdPath, err))
			}
		}
	}()

	hash := md5.New()
	limited := io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, contentSize+1)
	actualSize, copyErr := io.Copy(io.MultiWriter(temporary, hash), limited)
	closeErr := temporary.Close()
	if copyErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("stream remote content: %w", ctxErr)
		}
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return "", fmt.Errorf("remote content ended before metadata size %d: %w", contentSize, cloud.ErrIntegrity)
		}
		return "", fmt.Errorf("stream remote content: %w", copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close remote content temporary file %q: %w", createdPath, closeErr)
	}
	if actualSize != contentSize {
		return "", fmt.Errorf("remote content size %d does not match metadata size %d: %w", actualSize, contentSize, cloud.ErrIntegrity)
	}
	actualMD5 := hex.EncodeToString(hash.Sum(nil))
	if actualMD5 != metadata.ContentMD5 {
		return "", fmt.Errorf("remote content MD5 %s does not match metadata MD5 %s: %w", actualMD5, metadata.ContentMD5, cloud.ErrIntegrity)
	}
	return temporaryPath, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func validateRemotePhysicalDirectory(directory string) error {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return fmt.Errorf("remote temporary parent %q must be an absolute clean path: %w", directory, cloud.ErrUnsafeContent)
	}
	volume := filepath.VolumeName(directory)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(directory, current)
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("stat remote temporary ancestor %q: %w", current, errors.Join(cloud.ErrUnsafeContent, err))
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("remote temporary ancestor %q is not a physical directory: %w", current, cloud.ErrUnsafeContent)
		}
	}
	return nil
}

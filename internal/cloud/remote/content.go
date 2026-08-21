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

	"apm/internal/cloud"
	"apm/internal/domain"
)

const maxRemoteContentSize = int64(20 << 20)

func (c *Client) downloadVerifiedContent(
	ctx context.Context,
	ref domain.PowerRef,
	expectedType string,
	tempParent *pinnedDirectory,
	finalMode fs.FileMode,
) (staged *stagedContent, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("download remote content: %w", err)
	}
	if tempParent == nil {
		return nil, unsafeRemoteFilesystem("remote content temporary parent is unavailable")
	}
	metadata, err := c.getExactMetadata(ctx, ref)
	if err != nil {
		return nil, err
	}
	contentSize := *metadata.ContentSize
	if contentSize > maxRemoteContentSize {
		return nil, fmt.Errorf("remote content size %d exceeds %d bytes: %w", contentSize, maxRemoteContentSize, cloud.ErrUnsafeContent)
	}
	if metadata.ContentType != expectedType {
		return nil, fmt.Errorf("remote metadata content type %q does not match %q: %w", metadata.ContentType, expectedType, cloud.ErrInvalidResponse)
	}

	endpoint := c.endpoint("api", "v1", "resources", string(ref.Type), ref.Name, "versions", ref.Version, "content")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create remote content request: %w", err)
	}
	request.Header.Set("Accept", expectedType)
	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("request remote content: %w", ctxErr)
		}
		return nil, fmt.Errorf("request remote content: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNotFound:
		return nil, fmt.Errorf("remote content returned HTTP 404: %w", cloud.ErrNotFound)
	case http.StatusGone:
		return nil, fmt.Errorf("remote content returned HTTP 410: %w", cloud.ErrYanked)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("remote content returned HTTP %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
	}
	mediaType, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != expectedType || len(parameters) != 0 {
		return nil, fmt.Errorf("remote content has media type %q: %w", response.Header.Get("Content-Type"), cloud.ErrInvalidResponse)
	}
	if response.ContentLength < -1 {
		return nil, fmt.Errorf("remote Content-Length %d is invalid: %w", response.ContentLength, cloud.ErrInvalidResponse)
	}
	if response.ContentLength >= 0 && response.ContentLength != contentSize {
		return nil, fmt.Errorf("remote Content-Length %d does not match metadata size %d: %w", response.ContentLength, contentSize, cloud.ErrIntegrity)
	}

	staged, temporary, err := tempParent.createStaged(".apm-content-")
	if err != nil {
		return nil, err
	}
	created := staged
	closed := false
	defer func() {
		if !closed {
			resultErr = errors.Join(resultErr, temporary.Close())
		}
		if resultErr != nil {
			resultErr = errors.Join(resultErr, created.remove())
		}
	}()

	hash := md5.New()
	limited := io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, contentSize+1)
	actualSize, copyErr := io.Copy(io.MultiWriter(temporary, hash), limited)
	if copyErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("stream remote content: %w", ctxErr)
		}
		if errors.Is(copyErr, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("remote content ended before metadata size %d: %w", contentSize, cloud.ErrIntegrity)
		}
		return nil, fmt.Errorf("stream remote content: %w", copyErr)
	}
	prepareErr := staged.prepareForClose(temporary, finalMode)
	closeErr := temporary.Close()
	closed = true
	if prepareErr != nil {
		return nil, prepareErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close staged remote content: %w", closeErr)
	}
	if actualSize != contentSize {
		return nil, fmt.Errorf("remote content size %d does not match metadata size %d: %w", actualSize, contentSize, cloud.ErrIntegrity)
	}
	actualMD5 := hex.EncodeToString(hash.Sum(nil))
	if actualMD5 != metadata.ContentMD5 {
		return nil, fmt.Errorf("remote content MD5 %s does not match metadata MD5 %s: %w", actualMD5, metadata.ContentMD5, cloud.ErrIntegrity)
	}
	return staged, nil
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

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
)

var _ cloud.Submission = (*Client)(nil)

// submissionMetadataDTO 是 multipart 中 metadata part 的传输类型。
type submissionMetadataDTO struct {
	Type          domain.PowerType `json:"type"`
	Name          string           `json:"name"`
	Version       string           `json:"version"`
	Description   string           `json:"description"`
	ChangeSummary string           `json:"change_summary"`
	ContentMD5    string           `json:"content_md5"`
}

type submissionReceiptDTO struct {
	SubmissionID string                  `json:"submission_id"`
	Status       domain.SubmissionStatus `json:"status"`
	StatusURL    string                  `json:"status_url"`
}

type submissionDTO struct {
	SubmissionID     string                  `json:"submission_id"`
	Type             domain.PowerType        `json:"type"`
	Name             string                  `json:"name"`
	Version          string                  `json:"version"`
	Status           domain.SubmissionStatus `json:"status"`
	ContentAvailable bool                    `json:"content_available"`
	ValidationErrors []string                `json:"validation_errors"`
	ReviewComment    string                  `json:"review_comment"`
	PublishedVersion string                  `json:"published_version"`
}

type submissionSummaryDTO struct {
	SubmissionID string           `json:"submission_id"`
	Type         domain.PowerType `json:"type"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Status       string           `json:"status"`
	CreatedAt    string           `json:"created_at"`
}

type submissionListDTO struct {
	Items      []submissionSummaryDTO `json:"items"`
	NextCursor string                 `json:"next_cursor"`
}

// Submit 上传 multipart 提交并区分 202 接收与 200 幂等重放；不自动重试，中断由服务端 Idempotency-Key 去重。
func (c *Client) Submit(ctx context.Context, request domain.SubmissionRequest) (domain.SubmissionResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("submit remote resource: %w", err)
	}
	if c.apiKey == "" {
		return domain.SubmissionResult{}, fmt.Errorf("submit remote resource requires API key: %w", cloud.ErrAuth)
	}
	expectedContentType := submissionContentType(request.Type)
	if expectedContentType == "" || request.ContentType != expectedContentType {
		return domain.SubmissionResult{}, fmt.Errorf("submit remote resource type %q requires content type %q, got %q", request.Type, expectedContentType, request.ContentType)
	}
	metadata, err := json.Marshal(submissionMetadataDTO{
		Type:          request.Type,
		Name:          request.Name,
		Version:       request.Version,
		Description:   request.Description,
		ChangeSummary: request.ChangeSummary,
		ContentMD5:    request.ContentMD5,
	})
	if err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("encode remote submission metadata: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writeMultipartPart(writer, "metadata", "application/json", metadata); err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("write remote submission metadata part: %w", err)
	}
	if err := writeMultipartPart(writer, "content", request.ContentType, request.Content); err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("write remote submission content part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("close remote submission multipart: %w", err)
	}

	endpoint := c.endpoint("api", "v1", "submissions")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return domain.SubmissionResult{}, fmt.Errorf("create remote submission request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", writer.FormDataContentType())
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)
	httpRequest.Header.Set("Accept", "application/json")

	response, err := c.http.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return domain.SubmissionResult{}, fmt.Errorf("submit remote resource: %w", ctxErr)
		}
		return domain.SubmissionResult{}, fmt.Errorf("submit remote resource: %w", err)
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusAccepted:
		receipt, err := decodeSubmissionReceipt(ctx, response)
		if err != nil {
			return domain.SubmissionResult{}, err
		}
		return domain.SubmissionResult{Outcome: domain.SubmissionAccepted, SubmissionID: receipt.SubmissionID, Status: receipt.Status, StatusURL: receipt.StatusURL}, nil
	case http.StatusOK:
		replay, err := decodeSubmission(ctx, response)
		if err != nil {
			return domain.SubmissionResult{}, err
		}
		return domain.SubmissionResult{Outcome: domain.SubmissionReplayed, SubmissionID: replay.SubmissionID, Status: replay.Status}, nil
	default:
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return domain.SubmissionResult{}, fmt.Errorf("unexpected remote submission status %d: %w", response.StatusCode, cloud.ErrInvalidResponse)
		}
		return domain.SubmissionResult{}, c.classifySubmissionError(ctx, response)
	}
}

// submissionContentType 返回 PowerType 对应的合法请求内容媒体类型；未知类型返回空串。
func submissionContentType(powerType domain.PowerType) string {
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

// readSubmissionJSON 校验响应 Content-Type、限制并读取 2xx JSON 响应体、严格解码（拒绝重复键与未知字段）。
func readSubmissionJSON(ctx context.Context, response *http.Response, target any) error {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("remote submission response has content type %q: %w", response.Header.Get("Content-Type"), cloud.ErrInvalidResponse)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
	if err != nil {
		return fmt.Errorf("read remote submission response: %w", classifyResponseReadError(ctx, err))
	}
	if len(payload) > maxJSONResponse {
		return fmt.Errorf("remote submission response exceeds %d bytes: %w", maxJSONResponse, cloud.ErrInvalidResponse)
	}
	if err := jsonstrict.Decode(payload, target, true); err != nil {
		return fmt.Errorf("decode remote submission response: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	return nil
}

// decodeSubmissionReceipt 解码 202 的 receipt；身份、状态与非空 status_url 必须合法。
func decodeSubmissionReceipt(ctx context.Context, response *http.Response) (submissionReceiptDTO, error) {
	var receipt submissionReceiptDTO
	if err := readSubmissionJSON(ctx, response, &receipt); err != nil {
		return submissionReceiptDTO{}, err
	}
	status, err := parseSubmissionStatus(string(receipt.Status))
	if err != nil {
		return submissionReceiptDTO{}, fmt.Errorf("remote submission receipt has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if receipt.SubmissionID == "" {
		return submissionReceiptDTO{}, fmt.Errorf("remote submission receipt has empty submission_id: %w", cloud.ErrInvalidResponse)
	}
	if receipt.StatusURL == "" {
		return submissionReceiptDTO{}, fmt.Errorf("remote submission receipt has empty status_url: %w", cloud.ErrInvalidResponse)
	}
	receipt.Status = status
	return receipt, nil
}

// decodeSubmission 解码 200 的完整 Submission 详情；身份与状态必须合法。
func decodeSubmission(ctx context.Context, response *http.Response) (submissionDTO, error) {
	var dto submissionDTO
	if err := readSubmissionJSON(ctx, response, &dto); err != nil {
		return submissionDTO{}, err
	}
	status, err := parseSubmissionStatus(string(dto.Status))
	if err != nil {
		return submissionDTO{}, fmt.Errorf("remote submission has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if dto.SubmissionID == "" {
		return submissionDTO{}, fmt.Errorf("remote submission has empty submission_id: %w", cloud.ErrInvalidResponse)
	}
	dto.Status = status
	return dto, nil
}

// GetSubmission 读取一次提交详情；响应身份必须与请求 id 一致。
func (c *Client) GetSubmission(ctx context.Context, submissionID string) (domain.Submission, error) {
	if err := ctx.Err(); err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission: %w", err)
	}
	if submissionID == "" {
		return domain.Submission{}, fmt.Errorf("get remote submission with empty id: %w", cloud.ErrNotFound)
	}
	var dto submissionDTO
	if err := c.getJSON(ctx, c.endpoint("api", "v1", "submissions", submissionID), &dto); err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission: %w", err)
	}
	status, err := parseSubmissionStatus(string(dto.Status))
	if err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if dto.SubmissionID != submissionID {
		return domain.Submission{}, fmt.Errorf("remote submission identity does not match request: %w", cloud.ErrInvalidResponse)
	}
	return domain.Submission{
		SubmissionID:     dto.SubmissionID,
		Type:             dto.Type,
		Name:             dto.Name,
		Version:          dto.Version,
		Status:           status,
		ContentAvailable: dto.ContentAvailable,
		ValidationErrors: dto.ValidationErrors,
		ReviewComment:    dto.ReviewComment,
		PublishedVersion: dto.PublishedVersion,
	}, nil
}

// ListSubmissions 按 status/type/name 筛选并以 cursor 分页读取提交列表。
func (c *Client) ListSubmissions(ctx context.Context, query domain.SubmissionListQuery) (domain.SubmissionList, error) {
	if err := ctx.Err(); err != nil {
		return domain.SubmissionList{}, fmt.Errorf("list remote submissions: %w", err)
	}
	endpoint := c.endpoint("api", "v1", "submissions")
	values := endpoint.Query()
	if query.Status != "" {
		values.Set("status", string(query.Status))
	}
	if query.Type != "" {
		values.Set("type", string(query.Type))
	}
	if query.Name != "" {
		values.Set("name", query.Name)
	}
	if query.Cursor != "" {
		values.Set("cursor", query.Cursor)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	endpoint.RawQuery = values.Encode()

	var dto submissionListDTO
	if err := c.getJSON(ctx, endpoint, &dto); err != nil {
		return domain.SubmissionList{}, fmt.Errorf("list remote submissions: %w", err)
	}
	if dto.Items == nil {
		return domain.SubmissionList{}, fmt.Errorf("list remote submissions with missing items: %w", cloud.ErrInvalidResponse)
	}
	items := make([]domain.SubmissionSummary, 0, len(dto.Items))
	for _, item := range dto.Items {
		status, err := parseSubmissionStatus(item.Status)
		if err != nil {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
		}
		items = append(items, domain.SubmissionSummary{
			SubmissionID: item.SubmissionID,
			Type:         item.Type,
			Name:         item.Name,
			Version:      item.Version,
			Status:       status,
			CreatedAt:    item.CreatedAt,
		})
	}
	return domain.SubmissionList{Items: items, NextCursor: dto.NextCursor}, nil
}

// writeMultipartPart 写一个具名 part 并显式声明 Content-Type。
func writeMultipartPart(writer *multipart.Writer, name, contentType string, body []byte) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf("form-data; name=%q", name))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(body); err != nil {
		return err
	}
	return nil
}

// classifySubmissionError 将非 2xx 提交响应映射为稳定错误，并提取 problem code/trace_id；
// 409 ALREADY_PUBLISHED 同时匹配 ErrAlreadyPublished 与 ErrSubmissionConflict。
func (c *Client) classifySubmissionError(ctx context.Context, response *http.Response) error {
	problem, readErr := io.ReadAll(io.LimitReader(response.Body, maxProblemBody))
	if readErr != nil {
		return fmt.Errorf("read remote HTTP %d problem: %w", response.StatusCode, classifyResponseReadError(ctx, readErr))
	}
	decoded, hasProblem := decodeProblem(response, problem)
	var sentinel error
	switch response.StatusCode {
	case http.StatusNotFound:
		sentinel = cloud.ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		sentinel = cloud.ErrAuth
	case http.StatusConflict:
		if hasProblem && decoded.Code == "ALREADY_PUBLISHED" {
			sentinel = errors.Join(cloud.ErrAlreadyPublished, cloud.ErrSubmissionConflict)
		} else {
			sentinel = cloud.ErrSubmissionConflict
		}
	case http.StatusServiceUnavailable:
		sentinel = cloud.ErrUnavailable
	default:
		sentinel = cloud.ErrInvalidResponse
	}
	if !hasProblem {
		return fmt.Errorf("remote submission returned HTTP %d: %w", response.StatusCode, sentinel)
	}
	return fmt.Errorf("remote submission returned HTTP %d %s: %w", response.StatusCode, formatProblemContext(decoded), sentinel)
}

func parseSubmissionStatus(value string) (domain.SubmissionStatus, error) {
	switch domain.SubmissionStatus(value) {
	case domain.SubmissionReceived, domain.SubmissionValidating, domain.SubmissionValidationFailed,
		domain.SubmissionReviewPending, domain.SubmissionRejected, domain.SubmissionApproved,
		domain.SubmissionPublishing, domain.SubmissionPublishFailed, domain.SubmissionPublished:
		return domain.SubmissionStatus(value), nil
	default:
		return "", fmt.Errorf("unknown submission status %q", value)
	}
}

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
	"net/url"
	"strings"
	"time"

	"apm/internal/cloud"
	"apm/internal/domain"
	"apm/internal/utils/jsonstrict"
)

var _ cloud.Submission = (*Client)(nil)

const maxActiveSubmissions = 20

// submissionMetadataDTO 是 multipart 中 metadata part 的传输类型。
type submissionMetadataDTO struct {
	Type        domain.PowerType `json:"type"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Description string           `json:"description"`
	Message     string           `json:"message"`
	ContentMD5  string           `json:"content_md5"`
}

type submissionReceiptDTO struct {
	SubmissionID string                  `json:"submission_id"`
	Status       domain.SubmissionStatus `json:"status"`
	StatusURL    string                  `json:"status_url"`
}

type submissionDetailDTO struct {
	SubmissionID     *string               `json:"submission_id"`
	Type             *domain.PowerType     `json:"type"`
	Name             *string               `json:"name"`
	Version          *string               `json:"version"`
	Description      *string               `json:"description"`
	Message          *string               `json:"message"`
	Status           *string               `json:"status"`
	CreatedAt        *string               `json:"created_at"`
	UpdatedAt        *string               `json:"updated_at"`
	ContentAvailable *bool                 `json:"content_available"`
	ValidationErrors *[]string             `json:"validation_errors"`
	ReviewComment    *string               `json:"review_comment"`
	PublishedVersion *string               `json:"published_version"`
	Timeline         *[]submissionEventDTO `json:"timeline"`
}

type submissionEventDTO struct {
	EventID    *string `json:"event_id"`
	EventType  *string `json:"event_type"`
	StatusFrom *string `json:"status_from"`
	StatusTo   *string `json:"status_to"`
	Timestamp  *string `json:"timestamp"`
	Message    *string `json:"message"`
}

type submissionSummaryDTO struct {
	SubmissionID *string           `json:"submission_id"`
	Type         *domain.PowerType `json:"type"`
	Name         *string           `json:"name"`
	Version      *string           `json:"version"`
	Status       *string           `json:"status"`
	CreatedAt    *string           `json:"created_at"`
}

type submissionListDTO struct {
	Items []submissionSummaryDTO `json:"items"`
}

// Submit 在网络请求前校验并规范化必填 Message，随后上传 multipart 并区分 202 接收与 200 幂等重放；
// 不自动重试，中断由服务端 Idempotency-Key 去重。
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
	message := strings.TrimSpace(request.Message)
	if message == "" {
		return domain.SubmissionResult{}, errors.New("submit remote resource message is required")
	}
	metadata, err := json.Marshal(submissionMetadataDTO{
		Type:        request.Type,
		Name:        request.Name,
		Version:     request.Version,
		Description: request.Description,
		Message:     message,
		ContentMD5:  request.ContentMD5,
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
		replay, err := decodeSubmissionDetail(ctx, response)
		if err != nil {
			return domain.SubmissionResult{}, err
		}
		if replay.Type != request.Type || replay.Name != request.Name || replay.Version != request.Version || replay.Description != request.Description {
			return domain.SubmissionResult{}, fmt.Errorf("remote replayed submission identity does not match request: %w", cloud.ErrInvalidResponse)
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

// readSubmissionJSON 校验响应 Content-Type、限制并读取 2xx JSON 响应体、严格解码（拒绝重复键与尾随值，忽略未知响应字段以保持前向兼容）。
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
	if err := jsonstrict.Decode(payload, target, false); err != nil {
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
	if status != domain.SubmissionReceived {
		return submissionReceiptDTO{}, fmt.Errorf("remote submission receipt status is %q, want received: %w", status, cloud.ErrInvalidResponse)
	}
	receipt.Status = status
	return receipt, nil
}

// decodeSubmissionDetail 与 get 共用完整详情 decoder，确保 200 replay 不绕过字段和 timeline 约束。
func decodeSubmissionDetail(ctx context.Context, response *http.Response) (domain.Submission, error) {
	var dto submissionDetailDTO
	if err := readSubmissionJSON(ctx, response, &dto); err != nil {
		return domain.Submission{}, err
	}
	return validateSubmissionDetail(dto)
}

// getSubmissionJSON 仅为 Submission 查询附加 Bearer；匿名资源读取继续使用 getJSON，避免凭据扩散。
func (c *Client) getSubmissionJSON(ctx context.Context, endpoint *url.URL, target any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("query remote submission: %w", err)
	}
	if c.apiKey == "" {
		return fmt.Errorf("query remote submission requires API key: %w", cloud.ErrAuth)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create remote submission query: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)

	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("query remote submission: %w", ctxErr)
		}
		return fmt.Errorf("query remote submission: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return c.classifySubmissionError(ctx, response)
	}
	return readSubmissionJSON(ctx, response, target)
}

// GetSubmission 读取一次提交详情；响应身份必须与请求 id 一致。
func (c *Client) GetSubmission(ctx context.Context, submissionID string) (domain.Submission, error) {
	if err := ctx.Err(); err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission: %w", err)
	}
	if submissionID == "" {
		return domain.Submission{}, fmt.Errorf("get remote submission with empty id: %w", cloud.ErrNotFound)
	}
	var dto submissionDetailDTO
	if err := c.getSubmissionJSON(ctx, c.endpoint("api", "v1", "submissions", submissionID), &dto); err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission: %w", err)
	}
	detail, err := validateSubmissionDetail(dto)
	if err != nil {
		return domain.Submission{}, fmt.Errorf("get remote submission: %w", err)
	}
	if detail.SubmissionID != submissionID {
		return domain.Submission{}, fmt.Errorf("remote submission identity does not match request: %w", cloud.ErrInvalidResponse)
	}
	return detail, nil
}

// validateSubmissionDetail 将 presence-sensitive DTO 转为领域详情，并统一验证 get/replay 的完整契约。
func validateSubmissionDetail(dto submissionDetailDTO) (domain.Submission, error) {
	identity, err := validateSubmissionIdentity(dto.SubmissionID, dto.Type, dto.Name, dto.Version)
	if err != nil {
		return domain.Submission{}, fmt.Errorf("remote submission has invalid identity: %w", err)
	}
	if dto.Status == nil {
		return domain.Submission{}, fmt.Errorf("remote submission has missing status: %w", cloud.ErrInvalidResponse)
	}
	status, err := parseSubmissionStatus(*dto.Status)
	if err != nil {
		return domain.Submission{}, fmt.Errorf("remote submission has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
	}
	if !presentNonEmpty(dto.Description) || !presentNonEmpty(dto.Message) {
		return domain.Submission{}, fmt.Errorf("remote submission has missing description or message: %w", cloud.ErrInvalidResponse)
	}
	if _, err := parseRequiredSubmissionTimestamp(dto.CreatedAt); err != nil {
		return domain.Submission{}, fmt.Errorf("remote submission has invalid created_at: %w", err)
	}
	if _, err := parseRequiredSubmissionTimestamp(dto.UpdatedAt); err != nil {
		return domain.Submission{}, fmt.Errorf("remote submission has invalid updated_at: %w", err)
	}
	if dto.ContentAvailable == nil || dto.ValidationErrors == nil || dto.ReviewComment == nil || dto.PublishedVersion == nil || dto.Timeline == nil {
		return domain.Submission{}, fmt.Errorf("remote submission has missing detail fields: %w", cloud.ErrInvalidResponse)
	}
	timeline, err := validateSubmissionTimeline(*dto.Timeline)
	if err != nil {
		return domain.Submission{}, err
	}
	return domain.Submission{
		SubmissionID:     identity.SubmissionID,
		Type:             identity.Ref.Type,
		Name:             identity.Ref.Name,
		Version:          identity.Ref.Version,
		Description:      *dto.Description,
		Message:          *dto.Message,
		Status:           status,
		CreatedAt:        *dto.CreatedAt,
		UpdatedAt:        *dto.UpdatedAt,
		ContentAvailable: *dto.ContentAvailable,
		ValidationErrors: append([]string{}, (*dto.ValidationErrors)...),
		ReviewComment:    *dto.ReviewComment,
		PublishedVersion: *dto.PublishedVersion,
		Timeline:         timeline,
	}, nil
}

// ListSubmissions 按 status/type/name 读取当前凭据名下至多二十条活动提交，不接受分页参数。
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
	endpoint.RawQuery = values.Encode()

	var dto submissionListDTO
	if err := c.getSubmissionJSON(ctx, endpoint, &dto); err != nil {
		return domain.SubmissionList{}, fmt.Errorf("list remote submissions: %w", err)
	}
	if dto.Items == nil || len(dto.Items) > maxActiveSubmissions {
		return domain.SubmissionList{}, fmt.Errorf("list remote submissions has missing or excessive items: %w", cloud.ErrInvalidResponse)
	}
	items := make([]domain.SubmissionSummary, 0, len(dto.Items))
	seenIDs := make(map[string]struct{}, len(dto.Items))
	var previousCreated time.Time
	for index, item := range dto.Items {
		identity, err := validateSubmissionIdentity(item.SubmissionID, item.Type, item.Name, item.Version)
		if err != nil {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions has invalid identity: %w", err)
		}
		if _, duplicate := seenIDs[identity.SubmissionID]; duplicate {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions repeats submission_id %q: %w", identity.SubmissionID, cloud.ErrInvalidResponse)
		}
		seenIDs[identity.SubmissionID] = struct{}{}
		if item.Status == nil {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions has missing status: %w", cloud.ErrInvalidResponse)
		}
		status, err := parseSubmissionStatus(*item.Status)
		if err != nil {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions has invalid status: %w", errors.Join(cloud.ErrInvalidResponse, err))
		}
		if !domain.IsActiveSubmissionStatus(status) {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions contains terminal status %q: %w", status, cloud.ErrInvalidResponse)
		}
		if query.Status != "" && status != query.Status || query.Type != "" && identity.Ref.Type != query.Type || query.Name != "" && identity.Ref.Name != query.Name {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions item does not match filters: %w", cloud.ErrInvalidResponse)
		}
		createdAt, err := parseRequiredSubmissionTimestamp(item.CreatedAt)
		if err != nil {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions has invalid created_at: %w", err)
		}
		if index > 0 && previousCreated.Before(createdAt) {
			return domain.SubmissionList{}, fmt.Errorf("list remote submissions is not in descending created_at order: %w", cloud.ErrInvalidResponse)
		}
		previousCreated = createdAt
		items = append(items, domain.SubmissionSummary{
			SubmissionID: identity.SubmissionID,
			Type:         identity.Ref.Type,
			Name:         identity.Ref.Name,
			Version:      identity.Ref.Version,
			Status:       status,
			CreatedAt:    *item.CreatedAt,
		})
	}
	return domain.SubmissionList{Items: items}, nil
}

type validatedSubmissionIdentity struct {
	SubmissionID string
	Ref          domain.PowerRef
}

func validateSubmissionIdentity(submissionID *string, powerType *domain.PowerType, name, version *string) (validatedSubmissionIdentity, error) {
	if !presentNonEmpty(submissionID) || powerType == nil || name == nil || version == nil {
		return validatedSubmissionIdentity{}, fmt.Errorf("submission identity is incomplete: %w", cloud.ErrInvalidResponse)
	}
	ref, _, err := validatePowerIdentity(*powerType, *name, *version)
	if err != nil {
		return validatedSubmissionIdentity{}, err
	}
	return validatedSubmissionIdentity{SubmissionID: *submissionID, Ref: ref}, nil
}

func presentNonEmpty(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func parseRequiredSubmissionTimestamp(value *string) (time.Time, error) {
	if value == nil {
		return time.Time{}, fmt.Errorf("timestamp is missing: %w", cloud.ErrInvalidResponse)
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp %q is not RFC3339: %w", *value, errors.Join(cloud.ErrInvalidResponse, err))
	}
	return parsed, nil
}

func validateSubmissionTimeline(dto []submissionEventDTO) ([]domain.SubmissionEvent, error) {
	timeline := make([]domain.SubmissionEvent, 0, len(dto))
	var previousTimestamp time.Time
	for index, item := range dto {
		if !presentNonEmpty(item.EventID) || !presentNonEmpty(item.EventType) || item.StatusFrom == nil || item.StatusTo == nil || !presentNonEmpty(item.Message) {
			return nil, fmt.Errorf("remote submission timeline event is incomplete: %w", cloud.ErrInvalidResponse)
		}
		var statusFrom domain.SubmissionStatus
		if *item.StatusFrom != "" {
			parsed, err := parseSubmissionStatus(*item.StatusFrom)
			if err != nil {
				return nil, fmt.Errorf("remote submission timeline has invalid status_from: %w", errors.Join(cloud.ErrInvalidResponse, err))
			}
			statusFrom = parsed
		}
		statusTo, err := parseSubmissionStatus(*item.StatusTo)
		if err != nil {
			return nil, fmt.Errorf("remote submission timeline has invalid status_to: %w", errors.Join(cloud.ErrInvalidResponse, err))
		}
		timestamp, err := parseRequiredSubmissionTimestamp(item.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("remote submission timeline has invalid timestamp: %w", err)
		}
		if index > 0 && previousTimestamp.After(timestamp) {
			return nil, fmt.Errorf("remote submission timeline is not in ascending timestamp order: %w", cloud.ErrInvalidResponse)
		}
		previousTimestamp = timestamp
		timeline = append(timeline, domain.SubmissionEvent{
			EventID:    *item.EventID,
			EventType:  *item.EventType,
			StatusFrom: statusFrom,
			StatusTo:   statusTo,
			Timestamp:  *item.Timestamp,
			Message:    *item.Message,
		})
	}
	return timeline, nil
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
		if hasProblem {
			switch decoded.Code {
			case "ALREADY_PUBLISHED":
				sentinel = errors.Join(cloud.ErrAlreadyPublished, cloud.ErrSubmissionConflict)
			case "TOO_MANY_ACTIVE_SUBMISSIONS":
				sentinel = errors.Join(cloud.ErrTooManyActiveSubmissions, cloud.ErrSubmissionConflict)
			}
		}
		if sentinel == nil {
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

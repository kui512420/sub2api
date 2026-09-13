package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// 椒图视频任务存储。
//
// 为什么不直接复用 ImageTaskService：它的 Complete() 会按「OpenAI 图片响应」校验
// 结果载荷（data[].url / b64_json），视频载荷 {"url":...,"status":"completed"} 会被
// 判成非法结果（真机实测踩过）。把视频塞成图片形状只是把两个协议的生命周期绑在一起，
// 一旦开启对象存储还会试图把 mp4 当图片转存。因此视频用自己的任务服务，
// 只复用同一个 Redis 存储接口（ImageTaskStore）与归属校验语义。

const (
	// JiaotuVideoTaskIDPrefix 让视频任务 id 与图片任务在同一条 key 空间下仍可区分。
	JiaotuVideoTaskIDPrefix = "vidtask_"

	jiaotuVideoTaskTTL            = 24 * time.Hour
	jiaotuVideoTaskExecTimeout    = 30 * time.Minute
	jiaotuVideoTaskMaxURLBytes    = 4096
	jiaotuVideoTaskMaxErrorBytes  = 2048
	jiaotuVideoStatusProcessing   = "in_progress"
	jiaotuVideoStatusCompleted    = "completed"
	jiaotuVideoStatusFailed       = "failed"
	jiaotuVideoStatusExpired      = "expired"
	jiaotuVideoResultMaxPayload   = 8 << 10
	jiaotuVideoDefaultBillingUnit = 1
)

var (
	ErrJiaotuVideoTaskNotFound  = infraerrors.New(http.StatusNotFound, "JIAOTU_VIDEO_TASK_NOT_FOUND", "video task not found")
	ErrJiaotuVideoTaskForbidden = infraerrors.New(http.StatusForbidden, "JIAOTU_VIDEO_TASK_FORBIDDEN", "video task does not belong to this API key")
	ErrJiaotuVideoTaskUnavail   = infraerrors.New(http.StatusServiceUnavailable, "JIAOTU_VIDEO_TASK_UNAVAILABLE", "video task storage is unavailable")
)

// JiaotuVideoTaskOwner 与图片任务一致的归属主体（用户 + API Key）。
type JiaotuVideoTaskOwner struct {
	UserID   int64
	APIKeyID int64
}

// JiaotuVideoTask 对外的视频任务视图。
type JiaotuVideoTask struct {
	ID         string          `json:"id"`
	Object     string          `json:"object"`
	Status     string          `json:"status"`
	Model      string          `json:"model,omitempty"`
	URL        string          `json:"url,omitempty"`
	Duration   int             `json:"duration,omitempty"`
	Size       string          `json:"size,omitempty"`
	Resolution string          `json:"resolution,omitempty"`
	HasAudio   bool            `json:"has_audio,omitempty"`
	Reference  int             `json:"reference_count,omitempty"`
	CreatedAt  int64           `json:"created_at"`
	ExpiresAt  int64           `json:"expires_at"`
	Error      json.RawMessage `json:"error,omitempty"`
}

// JiaotuVideoTaskService 基于 ImageTaskStore（Redis JSON）的视频任务服务。
type JiaotuVideoTaskService struct {
	store ImageTaskStore
	ttl   time.Duration
}

// NewJiaotuVideoTaskService 构造视频任务服务；store 为空时所有操作返回不可用。
func NewJiaotuVideoTaskService(store ImageTaskStore) *JiaotuVideoTaskService {
	if store == nil {
		return nil
	}
	return &JiaotuVideoTaskService{store: store, ttl: jiaotuVideoTaskTTL}
}

// Available 报告任务存储是否可用（用于端点开关判定）。
func (s *JiaotuVideoTaskService) Available() bool {
	return s != nil && s.store != nil
}

// Create 新建一个 processing 状态的视频任务。
func (s *JiaotuVideoTaskService) Create(ctx context.Context, owner JiaotuVideoTaskOwner, model string) (*JiaotuVideoTask, error) {
	if !s.Available() {
		return nil, ErrJiaotuVideoTaskUnavail
	}
	now := time.Now()
	record := &ImageTaskRecord{
		ID:        JiaotuVideoTaskIDPrefix + strings.ReplaceAll(uuid.NewString(), "-", ""),
		UserID:    owner.UserID,
		APIKeyID:  owner.APIKeyID,
		Status:    ImageTaskStatusProcessing,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(s.ttl).Unix(),
	}
	record.Result = mustJiaotuVideoEnvelope(model, nil, jiaotuVideoStatusProcessing)
	if err := s.store.Save(ctx, record, s.ttl); err != nil {
		return nil, ErrJiaotuVideoTaskUnavail.WithCause(err)
	}
	return jiaotuVideoTaskFromRecord(record), nil
}

// Get 读取任务并强制归属校验：跨用户/跨 Key 一律 404，不泄露任务是否存在。
func (s *JiaotuVideoTaskService) Get(ctx context.Context, owner JiaotuVideoTaskOwner, id string) (*JiaotuVideoTask, error) {
	if !s.Available() {
		return nil, ErrJiaotuVideoTaskUnavail
	}
	id = strings.TrimSpace(id)
	if !strings.HasPrefix(id, JiaotuVideoTaskIDPrefix) {
		// 视频端点不回答图片任务 id，避免两套协议互相误读。
		return nil, ErrJiaotuVideoTaskNotFound
	}
	record, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return nil, ErrJiaotuVideoTaskNotFound
		}
		return nil, ErrJiaotuVideoTaskUnavail.WithCause(err)
	}
	if record.UserID != owner.UserID || record.APIKeyID != owner.APIKeyID {
		return nil, ErrJiaotuVideoTaskNotFound
	}
	if record.ExpiresAt > 0 && time.Now().Unix() > record.ExpiresAt {
		return nil, ErrJiaotuVideoTaskNotFound
	}
	return jiaotuVideoTaskFromRecord(record), nil
}

// List 返回该主体名下的视频任务，按创建时间倒序（新任务在前）。
//
// 这是“创作记录”页的数据源：任务存在 Redis 里 24h，但之前只有单查 Get，
// 前端又是纯内存记录，刷新后历史就“消失”了。非本主体的任务一律滤掉（与 Get
// 同一归属语义，不泄露存在性）；已过 ExporesAt 的任务也滤掉。
func (s *JiaotuVideoTaskService) List(ctx context.Context, owner JiaotuVideoTaskOwner, limit int) ([]*JiaotuVideoTask, error) {
	if !s.Available() {
		return nil, ErrJiaotuVideoTaskUnavail
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	records, err := s.store.ListByPrefix(ctx, JiaotuVideoTaskIDPrefix)
	if err != nil {
		return nil, ErrJiaotuVideoTaskUnavail.WithCause(err)
	}
	tasks := make([]*JiaotuVideoTask, 0, len(records))
	now := time.Now().Unix()
	for _, record := range records {
		if record == nil || record.UserID != owner.UserID || record.APIKeyID != owner.APIKeyID {
			continue
		}
		if record.ExpiresAt > 0 && now > record.ExpiresAt {
			continue
		}
		tasks = append(tasks, jiaotuVideoTaskFromRecord(record))
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt > tasks[j].CreatedAt })
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

// Complete 写入成功结果（含可下载地址）。载荷非法时按失败收尾，避免存坏数据。
func (s *JiaotuVideoTaskService) Complete(ctx context.Context, id string, result *JiaotuVideoResult, model string) error {
	if !s.Available() {
		return ErrJiaotuVideoTaskUnavail
	}
	if result == nil || strings.TrimSpace(result.URL) == "" {
		return s.failRaw(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "video generation returned no url"))
	}
	if len(result.URL) > jiaotuVideoTaskMaxURLBytes {
		return s.failRaw(ctx, id, http.StatusBadGateway, imageTaskErrorJSON("api_error", "video result URL is too long"))
	}
	payload := mustJiaotuVideoEnvelope(model, result, jiaotuVideoStatusCompleted)
	return s.finish(ctx, id, ImageTaskStatusCompleted, http.StatusOK, payload, nil)
}

// Fail 写入失败态与错误体。
func (s *JiaotuVideoTaskService) Fail(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	if !s.Available() {
		return ErrJiaotuVideoTaskUnavail
	}
	if statusCode < http.StatusUnauthorized {
		statusCode = http.StatusBadGateway
	}
	return s.failRaw(ctx, id, statusCode, taskErr)
}

func (s *JiaotuVideoTaskService) failRaw(ctx context.Context, id string, statusCode int, taskErr json.RawMessage) error {
	if len(taskErr) == 0 || !json.Valid(taskErr) {
		taskErr = imageTaskErrorJSON("api_error", "video generation failed")
	}
	if len(taskErr) > jiaotuVideoTaskMaxErrorBytes {
		taskErr = taskErr[:jiaotuVideoTaskMaxErrorBytes]
	}
	return s.finish(ctx, id, ImageTaskStatusFailed, statusCode, mustJiaotuVideoEnvelope("", nil, jiaotuVideoStatusFailed), taskErr)
}

func (s *JiaotuVideoTaskService) finish(ctx context.Context, id string, status string, statusCode int, result, taskErr json.RawMessage) error {
	record, err := s.store.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		if errors.Is(err, ErrImageTaskNotFound) {
			return ErrJiaotuVideoTaskNotFound
		}
		return ErrJiaotuVideoTaskUnavail.WithCause(err)
	}
	record.Status = status
	record.HTTPStatus = statusCode
	record.Result = result
	record.Error = taskErr
	if statusCode >= 0 {
		record.CompletedAt = func() *int64 { value := time.Now().Unix(); return &value }()
	}
	remaining := time.Until(time.Unix(record.ExpiresAt, 0))
	if remaining <= 0 {
		remaining = s.ttl
	}
	if err := s.store.Save(ctx, record, remaining); err != nil {
		return ErrJiaotuVideoTaskUnavail.WithCause(err)
	}
	return nil
}

// mustJiaotuVideoEnvelope 生成任务载荷（永远合法 JSON，长度受限）。
func mustJiaotuVideoEnvelope(model string, result *JiaotuVideoResult, status string) json.RawMessage {
	payload := map[string]any{"status": status, "object": "video"}
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		payload["model"] = trimmed
	}
	if result != nil {
		payload["url"] = result.URL
		payload["duration"] = result.Duration
		payload["size"] = result.Ratio
		payload["resolution"] = result.Quality
		payload["has_audio"] = result.Audio
		payload["reference_count"] = result.ReferenceCount
		if result.TaskID != "" {
			payload["upstream_task_id"] = result.TaskID
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil || len(raw) > jiaotuVideoResultMaxPayload {
		return json.RawMessage(`{"status":"` + status + `","object":"video"}`)
	}
	return json.RawMessage(raw)
}

func jiaotuVideoTaskFromRecord(record *ImageTaskRecord) *JiaotuVideoTask {
	task := &JiaotuVideoTask{
		ID:        record.ID,
		Object:    "video",
		Status:    jiaotuVideoPublicStatus(record.Status),
		CreatedAt: record.CreatedAt,
		ExpiresAt: record.ExpiresAt,
		Error:     record.Error,
	}
	var payload struct {
		Model         string `json:"model"`
		URL           string `json:"url"`
		Duration      int    `json:"duration"`
		Size          string `json:"size"`
		Resolution    string `json:"resolution"`
		HasAudio      bool   `json:"has_audio"`
		ReferenceList int    `json:"reference_count"`
	}
	if len(record.Result) > 0 {
		_ = json.Unmarshal(record.Result, &payload)
	}
	task.Model = payload.Model
	task.URL = payload.URL
	task.Duration = payload.Duration
	task.Size = payload.Size
	task.Resolution = payload.Resolution
	task.HasAudio = payload.HasAudio
	task.Reference = payload.ReferenceList
	return task
}

func jiaotuVideoPublicStatus(status string) string {
	switch status {
	case ImageTaskStatusProcessing:
		return jiaotuVideoStatusProcessing
	case ImageTaskStatusCompleted:
		return jiaotuVideoStatusCompleted
	case ImageTaskStatusFailed:
		return jiaotuVideoStatusFailed
	default:
		return status
	}
}

// ExecutionTimeout 供 handler 设定后台执行上限。
func (s *JiaotuVideoTaskService) ExecutionTimeout() time.Duration { return jiaotuVideoTaskExecTimeout }

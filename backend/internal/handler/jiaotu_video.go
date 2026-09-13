package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// 椒图视频的 OpenAI 兼容异步端点：
//
//	POST /v1/videos                        提交 → 202 + 任务 id
//	GET  /v1/videos/:request_id            轮询状态（in_progress / completed / failed）
//	GET  /v1/videos/:request_id/content    由网关代拉上游产物并回流
//
// 上游是同步 SSE（imageChat 回 type=="video" 的 URL），所以这里把「同步生成」包成
// 异步契约。计费发生在后台跑完的那一刻，客户端不轮询也不会漏记（与异步图片同语义）。
// 任务存储用 service.JiaotuVideoTaskService：图片任务的 Complete() 会按 OpenAI 图片响应
// 校验载荷，视频载荷套进去会被判非法（真机踩过），因此不复用其校验语义。

const (
	jiaotuVideoPollIntervalSeconds = 5
	jiaotuVideoMaxAttempts         = 3
	jiaotuVideoTaskIDPrefix        = "vidtask_"
)

func (h *AsyncImageHandler) jiaotuVideoTasks() *service.JiaotuVideoTaskService {
	if h == nil || h.tasks == nil {
		return nil
	}
	return service.NewJiaotuVideoTaskService(h.tasks.Store())
}

// JiaotuVideoSubmit POST /v1/videos（椒图分组）。
func (h *AsyncImageHandler) JiaotuVideoSubmit(c *gin.Context) {
	tasks := h.jiaotuVideoTasks()
	if tasks == nil || !tasks.Available() {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task store is unavailable")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.UserID <= 0 || apiKey.ID <= 0 {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	if !service.GroupAllowsImageGeneration(apiKey.Group) {
		imageTaskJSONError(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	if h.openAI == nil || h.openAI.gatewayService == nil {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video gateway is unavailable")
		return
	}

	body, err := readJiaotuVideoBody(c)
	if err != nil {
		writeJiaotuVideoBodyError(c, err)
		return
	}
	var input map[string]any
	if err := json.Unmarshal(body, &input); err != nil {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "请求体必须是 JSON 对象")
		return
	}

	catalog, err := h.openAI.gatewayService.JiaotuVideoCatalog(c.Request.Context())
	if err != nil {
		writeJiaotuVideoError(c, err)
		return
	}
	req, err := h.openAI.gatewayService.NormalizeJiaotuVideo(c.Request.Context(), input, catalog)
	if err != nil {
		writeJiaotuVideoError(c, err)
		return
	}

	task, err := tasks.Create(c.Request.Context(),
		service.JiaotuVideoTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}, req.Model.StableID)
	if err != nil {
		imageTaskError(c, err)
		return
	}
	pollURL := jiaotuVideoPollURL(c.Request.URL.Path, task.ID)
	c.Header("Cache-Control", "no-store")
	c.Header("Location", pollURL)
	c.Header("Retry-After", strconv.Itoa(jiaotuVideoPollIntervalSeconds))
	c.JSON(http.StatusAccepted, gin.H{
		"id":         task.ID,
		"object":     "video",
		"model":      req.Model.StableID,
		"status":     task.Status,
		"progress":   0,
		"created_at": task.CreatedAt,
		"expires_at": task.ExpiresAt,
		"poll_url":   pollURL,
	})

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	go h.runJiaotuVideo(tasks, task.ID, apiKey, req, subscription)
}

// runJiaotuVideo 后台执行：按 jiaotu.max_attempts 换号，成功即写结果并计费。
func (h *AsyncImageHandler) runJiaotuVideo(tasks *service.JiaotuVideoTaskService, taskID string, apiKey *service.APIKey, req *service.JiaotuVideoRequest, subscription *service.UserSubscription) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().Error("jiaotu.video_task.panicked", zap.String("task_id", taskID), zap.Any("panic", recovered))
			_ = tasks.Fail(context.Background(), taskID, http.StatusInternalServerError,
				imageTaskErrorPayload("api_error", "video generation task panicked"))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), tasks.ExecutionTimeout())
	defer cancel()

	maxAttempts := h.openAI.gatewayService.JiaotuMaxAccountSwitches()
	if maxAttempts <= 0 {
		maxAttempts = jiaotuVideoMaxAttempts
	}
	excluded := make(map[int64]struct{})
	var lastErr error
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			lastErr = errors.New("video task timed out")
			break
		}
		selection, _, err := h.openAI.gatewayService.SelectAccountWithSchedulerForPlatformImages(
			ctx, apiKey.GroupID, "", req.Model.StableID, excluded, "", service.PlatformJiaotu)
		if err != nil {
			lastErr = err
			break
		}
		if selection == nil || selection.Account == nil {
			lastErr = errors.New("no available jiaotu accounts")
			break
		}
		account := selection.Account
		excluded[account.ID] = struct{}{}
		started := time.Now()

		result, err := h.openAI.gatewayService.GenerateJiaotuVideoForAccount(ctx, account, req)
		if err != nil {
			lastErr = err
			logger.L().Warn("jiaotu.video_task.attempt_failed",
				zap.String("task_id", taskID), zap.Int64("account_id", account.ID),
				zap.Duration("elapsed", time.Since(started)), zap.Error(err))
			// 模型维护中/入参不合法与账号无关：再换号也是同一句话，直接定案。
			if jerr, ok := service.IsJiaotuError(err); ok && jerr.NonRetryableWithNextAccount() {
				break
			}
			continue
		}
		logger.L().Info("jiaotu.video_task.completed",
			zap.String("task_id", taskID), zap.Int64("account_id", account.ID),
			zap.Duration("elapsed", time.Since(started)), zap.String("model", req.Model.StableID))
		h.completeJiaotuVideoTask(taskID, apiKey, account, req, result, subscription, tasks)
		return
	}
	logger.L().Warn("jiaotu.video_task.failed", zap.String("task_id", taskID), zap.Error(lastErr))
	_ = tasks.Fail(context.Background(), taskID, jiaotuVideoFailureStatus(lastErr), jiaotuVideoErrorPayload(lastErr))
}

// completeJiaotuVideoTask 写入结果并计费；计费失败不影响结果可读。
func (h *AsyncImageHandler) completeJiaotuVideoTask(taskID string, apiKey *service.APIKey, account *service.Account,
	req *service.JiaotuVideoRequest, result *service.JiaotuVideoResult,
	subscription *service.UserSubscription, tasks *service.JiaotuVideoTaskService) {
	// 标记 completed 前先尝试把视频转存到对象存储（R2/S3）：成功后 result.URL 被替换为
	// 持久地址、archived=true；未配置对象存储或转存失败则保留上游临时链接兜底，不阻断交付。
	archived := h.persistJiaotuVideoToStorage(taskID, result)
	if err := tasks.Complete(context.Background(), taskID, result, req.Model.StableID, archived); err != nil {
		logger.L().Error("jiaotu.video_task.complete_failed", zap.String("task_id", taskID), zap.Error(err))
		return
	}
	forwardResult := service.JiaotuVideoUsageResult(req, result)
	if err := h.openAI.gatewayService.RecordUsage(context.Background(), &service.OpenAIRecordUsageInput{
		Result:           forwardResult,
		APIKey:           apiKey,
		User:             apiKey.User,
		Account:          account,
		Subscription:     subscription,
		InboundEndpoint:  "/v1/videos",
		UpstreamEndpoint: forwardResult.UpstreamEndpoint,
	}); err != nil {
		logger.L().Warn("jiaotu.video_task.billing_failed",
			zap.String("task_id", taskID), zap.Int64("account_id", account.ID), zap.Error(err))
	}
}

// persistJiaotuVideoToStorage 把上游生成好的视频下载并转存到已配置的对象存储。
// 成功时就地把 result.URL 替换为对象存储地址并返回 true；未启用对象存储、缺少结果
// 或转存失败时返回 false（调用方据此以上游临时链接收尾，保证视频始终可交付）。
func (h *AsyncImageHandler) persistJiaotuVideoToStorage(taskID string, result *service.JiaotuVideoResult) bool {
	if result == nil || strings.TrimSpace(result.URL) == "" || h.tasks == nil {
		return false
	}
	storage, ok := h.tasks.Storage()
	if !ok {
		return false
	}
	archiver := service.NewJiaotuVideoArchiver(storage)
	if !archiver.Available() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), service.JiaotuVideoArchiveTimeout)
	defer cancel()
	storedURL, err := archiver.Archive(ctx, taskID, result.URL)
	if err != nil {
		logger.L().Warn("jiaotu.video_task.archive_failed", zap.String("task_id", taskID), zap.Error(err))
		return false
	}
	logger.L().Info("jiaotu.video_task.archived", zap.String("task_id", taskID))
	result.URL = storedURL
	return true
}

// JiaotuVideoList GET /v1/videos。列出当前 API Key 名下的视频任务（创作记录页数据源）。
//
// 之前只有单查 Get，任务又只存在前端的内存数组里，刷新后历史就“消失”了；
// 实际上任务在后端保留 24h。列表按创建时间倒序，limit 控制返回条数。
func (h *AsyncImageHandler) JiaotuVideoList(c *gin.Context) {
	tasks := h.jiaotuVideoTasks()
	if tasks == nil || !tasks.Available() {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task store is unavailable")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := tasks.List(c.Request.Context(), service.JiaotuVideoTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}, limit)
	if err != nil {
		writeJiaotuVideoTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"object":   "list",
		"data":     items,
		"has_more": false,
	})
}

// JiaotuVideoStatus GET /v1/videos/:request_id。
func (h *AsyncImageHandler) JiaotuVideoStatus(c *gin.Context) {
	tasks := h.jiaotuVideoTasks()
	if tasks == nil || !tasks.Available() {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task store is unavailable")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	task, err := tasks.Get(c.Request.Context(), service.JiaotuVideoTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}, c.Param("request_id"))
	if err != nil {
		writeJiaotuVideoTaskError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if task.Status == "in_progress" {
		c.Header("Retry-After", strconv.Itoa(jiaotuVideoPollIntervalSeconds))
	}
	response := gin.H{
		"id":         task.ID,
		"task_id":    task.ID,
		"object":     "video",
		"status":     task.Status,
		"created_at": task.CreatedAt,
		"expires_at": task.ExpiresAt,
		"progress":   jiaotuVideoProgress(task.Status),
	}
	if task.Model != "" {
		response["model"] = task.Model
	}
	if task.URL != "" {
		response["url"] = task.URL
		response["duration"] = task.Duration
		response["size"] = task.Size
		response["resolution"] = task.Resolution
		response["has_audio"] = task.HasAudio
		response["reference_count"] = task.Reference
		if task.Archived {
			response["archived"] = true
		}
	}
	if len(task.Error) > 0 {
		response["error"] = json.RawMessage(task.Error)
	} else if task.Status == "failed" {
		// 失败态必须带可读错误体，否则客户端只能看到一个光秃秃的 failed。
		response["error"] = gin.H{"type": "api_error", "message": "video generation failed"}
	}
	c.JSON(http.StatusOK, response)
}

// JiaotuVideoContent GET /v1/videos/:request_id/content。
func (h *AsyncImageHandler) JiaotuVideoContent(c *gin.Context) {
	tasks := h.jiaotuVideoTasks()
	if tasks == nil || !tasks.Available() {
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task store is unavailable")
		return
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		imageTaskError(c, service.ErrImageTaskForbidden)
		return
	}
	task, err := tasks.Get(c.Request.Context(), service.JiaotuVideoTaskOwner{UserID: apiKey.UserID, APIKeyID: apiKey.ID}, c.Param("request_id"))
	if err != nil {
		writeJiaotuVideoTaskError(c, err)
		return
	}
	if task.Status != "completed" || strings.TrimSpace(task.URL) == "" {
		imageTaskJSONError(c, http.StatusConflict, "invalid_request_error", "video is not ready for download")
		return
	}
	// 已转存对象存储的视频：直接 307 跳转到签名直链，让客户端直连 R2/S3，
	// 避免大视频字节流经网关内存（也绕开代拉上游的结果体积上限）。
	if task.Archived {
		c.Header("Cache-Control", "no-store")
		c.Redirect(http.StatusTemporaryRedirect, task.URL)
		return
	}
	data, contentType, err := h.openAI.gatewayService.FetchJiaotuVideoBytes(c.Request.Context(), task.URL)
	if err != nil {
		writeJiaotuVideoError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if contentType == "" {
		contentType = "video/mp4"
	}
	c.Data(http.StatusOK, contentType, data)
}

func readJiaotuVideoBody(c *gin.Context) ([]byte, error) {
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("Request body is empty")
	}
	return body, nil
}

func writeJiaotuVideoBodyError(c *gin.Context, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		imageTaskJSONError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
		return
	}
	message := err.Error()
	if message == "Request body is empty" {
		imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", message)
		return
	}
	imageTaskJSONError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
}

// writeJiaotuVideoTaskError 把任务存储错误映射成客户端可读响应。
func writeJiaotuVideoTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrJiaotuVideoTaskNotFound):
		imageTaskJSONError(c, http.StatusNotFound, "not_found_error", "video task not found")
	case errors.Is(err, service.ErrJiaotuVideoTaskForbidden):
		imageTaskJSONError(c, http.StatusForbidden, "permission_error", "video task does not belong to this API key")
	case errors.Is(err, service.ErrJiaotuVideoTaskUnavail):
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task storage is unavailable")
	default:
		imageTaskJSONError(c, http.StatusServiceUnavailable, "api_error", "video task storage is unavailable")
	}
}

func jiaotuVideoProgress(status string) int {
	switch status {
	case "completed":
		return 100
	case "failed":
		return 0
	default:
		return 0
	}
}

func jiaotuVideoPollURL(path, taskID string) string {
	const fallback = "/v1/videos/"
	trimmed := strings.TrimRight(path, "/")
	switch {
	case trimmed == "":
		return fallback + taskID
	case strings.HasSuffix(trimmed, "/videos"):
		// 提交端点本身是 /v1/videos，不能把 "videos" 当成旧任务 id 削掉。
		return trimmed + "/" + taskID
	default:
		if index := strings.LastIndex(trimmed, "/"); index > 0 {
			return trimmed[:index] + "/" + taskID
		}
		return fallback + taskID
	}
}

func jiaotuVideoErrorPayload(err error) json.RawMessage {
	message := "video generation failed"
	if err != nil {
		message = jiaotuTrimErrorText(err.Error())
	}
	payload, marshalErr := json.Marshal(map[string]any{"type": "api_error", "message": message})
	if marshalErr != nil {
		return json.RawMessage(`{"type":"api_error","message":"video generation failed"}`)
	}
	return json.RawMessage(payload)
}

func jiaotuVideoFailureStatus(err error) int {
	if jerr, ok := service.IsJiaotuError(err); ok {
		return jerr.ClientStatus()
	}
	return http.StatusBadGateway
}

func writeJiaotuVideoError(c *gin.Context, err error) {
	if jerr, ok := service.IsJiaotuError(err); ok {
		c.JSON(jerr.ClientStatus(), gin.H{"error": gin.H{
			"type":    "api_error",
			"code":    string(jerr.Kind),
			"message": jerr.Message,
		}})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": err.Error()}})
}

// jiaotuTrimErrorText 截断上游错误文本，避免把整段响应体塞进任务记录。
// 按 rune 截，否则中文/emoji 会被切成乱码半字符。
func jiaotuTrimErrorText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "video generation failed"
	}
	const limit = 300
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

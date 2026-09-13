package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// 椒图原生图片转发：把 OpenAI Images 协议请求翻译成 imageChat（SSE），
// 并把失败语义交回 sub2api 既有的「换号 failover」管线。
//
// 与桥接版（kuikui sidecar）的关键差异：
//   - 模型必须显式命中稳定表，不再静默回落到全能图片 V2（契约 R4）；
//   - 选号前先看积分，避免把注定失败的请求打到上游（契约 §7）；
//   - 积分不足/401 会真正回写到账号状态，而不是只记在文件号池里（契约 §8）。

const (
	jiaotuMaxImageCount        = 4
	jiaotuDefaultRefLimit      = 6
	jiaotuPointsCooldown       = 10 * time.Minute
	jiaotuCatalogRefreshMargin = 30 * time.Second
)

var (
	jiaotuClientMu      sync.Mutex
	jiaotuClientCache   = map[string]*JiaotuClient{}
	jiaotuCatalogMu     sync.RWMutex
	jiaotuCatalogByBase = map[string]jiaotuCatalogEntry{}
)

type jiaotuCatalogEntry struct {
	catalog   *JiaotuCatalog
	fetchedAt time.Time
}

// jiaotuClient 按配置派生（并复用）客户端实例。
func (s *OpenAIGatewayService) jiaotuClient() *JiaotuClient {
	settings := JiaotuSettingsFromConfig(s.cfg)
	key := settings.APIBase + "|" + settings.WebOrigin
	jiaotuClientMu.Lock()
	defer jiaotuClientMu.Unlock()
	if client, ok := jiaotuClientCache[key]; ok {
		return client
	}
	client := NewJiaotuClient(settings)
	jiaotuClientCache[key] = client
	return client
}

// jiaotuCatalogFor 返回模型清单（带 TTL 缓存）；上游不可用时返回已缓存副本 + 错误。
func (s *OpenAIGatewayService) jiaotuCatalogFor(ctx context.Context, client *JiaotuClient, proxyURL string, force bool) (*JiaotuCatalog, error) {
	settings := client.Settings()
	key := settings.APIBase
	ttl := settings.ModelsCacheTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if !force {
		jiaotuCatalogMu.RLock()
		entry := jiaotuCatalogByBase[key]
		jiaotuCatalogMu.RUnlock()
		if entry.catalog != nil && time.Since(entry.fetchedAt) < ttl {
			return entry.catalog, nil
		}
	}

	catalog, err := client.QueryModels(ctx, proxyURL)
	if err != nil {
		jiaotuCatalogMu.RLock()
		stale := jiaotuCatalogByBase[key].catalog
		jiaotuCatalogMu.RUnlock()
		// 缓存过期且上游抖动时，宁可继续用旧清单，也不要让所有请求一起失败。
		if stale != nil && time.Since(jiaotuCatalogByBase[key].fetchedAt) < 24*time.Hour {
			logger.LegacyPrintf("service.jiaotu", "[Jiaotu] queryModels 失败，沿用缓存清单：%v", err)
			return stale, nil
		}
		return nil, err
	}
	jiaotuCatalogMu.Lock()
	jiaotuCatalogByBase[key] = jiaotuCatalogEntry{catalog: catalog, fetchedAt: time.Now()}
	jiaotuCatalogMu.Unlock()
	return catalog, nil
}

// jiaotuAccountStore 返回椒图状态回写用的持久面（默认即 accountRepo）。
func (s *OpenAIGatewayService) jiaotuAccountStore() JiaotuAccountStore {
	if s == nil {
		return nil
	}
	if s.jiaotuStore != nil {
		return s.jiaotuStore
	}
	return s.accountRepo
}

// InvalidateJiaotuCatalogCache 清掉模型清单缓存（配置变更/上游上新时调用）。
func InvalidateJiaotuCatalogCache() {
	jiaotuCatalogMu.Lock()
	jiaotuCatalogByBase = map[string]jiaotuCatalogEntry{}
	jiaotuCatalogMu.Unlock()
}

// JiaotuModelsForListing 供 /v1/models 使用：优先上游实时目录（含新模型），
// 上游不可达时退化到本地稳定表，保证列表不为空。
func (s *OpenAIGatewayService) JiaotuModelsForListing(ctx context.Context) []JiaotuCatalogEntry {
	if s == nil {
		return NewJiaotuCatalog(nil).OpenAIModelEntries()
	}
	client := s.jiaotuClient()
	catalog, err := s.jiaotuCatalogFor(ctx, client, "", false)
	if err != nil || catalog == nil {
		logger.LegacyPrintf("service.jiaotu", "[Jiaotu] /v1/models 回落本地稳定表: %v", err)
		catalog = NewJiaotuCatalog(nil)
	}
	return catalog.OpenAIModelEntries()
}

// JiaotuImageRequest 已归一化的椒图图片请求（供 /v1/images 与创作中心复用）。
type JiaotuImageRequest struct {
	Model      JiaotuModelSpec
	Prompt     string
	Count      int
	Ratio      string
	References []JiaotuReference
	// ResponseFormat: "" / url / b64_json
	ResponseFormat string
	// Upstream 为 nil 表示上游清单里没有该模型（缓存缺失或已下架）。
	Upstream *JiaotuUpstreamModel
}

// NormalizeJiaotuImagesRequest 把 OpenAI Images 入参翻译成椒图请求。
// 所有校验都在调用上游之前完成：不合法的请求不得消耗积分。
func (s *OpenAIGatewayService) NormalizeJiaotuImagesRequest(parsed *OpenAIImagesRequest, catalog *JiaotuCatalog) (*JiaotuImageRequest, error) {
	if parsed == nil {
		return nil, newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "缺少解析后的图片请求")
	}
	requestModel := strings.TrimSpace(parsed.Model)
	spec, known := catalog.SpecFor(requestModel)
	if !known {
		return nil, newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "未知椒图模型：%s", requestModel)
	}
	if spec.Capability != JiaotuCapabilityImage {
		return nil, newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "模型 %s 是椒图视频模型，请使用 /v1/videos", spec.StableID)
	}

	prompt := strings.TrimSpace(parsed.Prompt)
	if prompt == "" {
		return nil, newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "缺少必填参数：prompt")
	}
	count := parsed.N
	if count < 1 {
		count = 1
	}
	if count > jiaotuMaxImageCount {
		count = jiaotuMaxImageCount
	}
	ratio, err := JiaotuRatioFromOpenAISize(parsed.Size)
	if err != nil {
		return nil, newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "%v", err)
	}

	refs, err := s.jiaotuReferences(parsed)
	if err != nil {
		return nil, err
	}

	out := &JiaotuImageRequest{
		Model:          spec,
		Prompt:         prompt,
		Count:          count,
		Ratio:          ratio,
		References:     refs,
		ResponseFormat: strings.ToLower(strings.TrimSpace(parsed.ResponseFormat)),
	}
	if catalog != nil {
		if upstream, _, found := catalog.Resolve(spec.StableID); found {
			copyUpstream := upstream
			out.Upstream = &copyUpstream
		}
	}
	maxRefs := jiaotuDefaultRefLimit
	if out.Upstream != nil {
		maxRefs = out.Upstream.MaxReferenceImages()
	}
	if err := s.jiaotuClient().ValidateJiaotuReferences(refs, maxRefs); err != nil {
		return nil, err
	}
	return out, nil
}

// jiaotuReferences 收集参考图：multipart 上传件优先，其次入站 data URL。
// 明确拒绝 http(s) 参考图 URL —— 需要由调用方下载后以 multipart/dataURL 传入，避免网关被用作任意 URL 抓取器。
func (s *OpenAIGatewayService) jiaotuReferences(parsed *OpenAIImagesRequest) ([]JiaotuReference, error) {
	maxBytes := s.jiaotuClient().Settings().MaxRefBytes
	refs := make([]JiaotuReference, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, upload := range parsed.Uploads {
		// 空上传件不得静默丢弃：那会把「图生图」悄悄变成「文生图」。
		contentType := strings.ToLower(strings.TrimSpace(upload.ContentType))
		if contentType == "image/jpg" {
			contentType = "image/jpeg"
		}
		if contentType == "" {
			contentType = detectImageContentType(upload.Data)
		}
		refs = append(refs, JiaotuReference{Data: append([]byte(nil), upload.Data...), ContentType: contentType})
	}
	for _, value := range parsed.InputImageURLs {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
			return nil, newJiaotuError("references", JiaotuErrInvalidRequest, 0, "椒图参考图只接受 dataURL 或 multipart 上传，不支持外链 URL")
		}
		ref, err := DecodeJiaotuDataURL(trimmed, maxBytes)
		if err != nil {
			return nil, newJiaotuError("references", JiaotuErrInvalidRequest, 0, "%v", err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// JiaotuRatioFromOpenAISize 把 OpenAI 的 size 归一为椒图比例（kuikui extended.go:819-834）。
func JiaotuRatioFromOpenAISize(size string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "":
		return "1:1", nil
	case "auto":
		return "", nil
	case "1024x1024":
		return "1:1", nil
	case "1024x1536":
		return "9:16", nil
	case "1536x1024":
		return "16:9", nil
	case "1:1", "9:16", "16:9":
		return strings.ToLower(strings.TrimSpace(size)), nil
	default:
		return "", fmt.Errorf("不支持的 size：%s", size)
	}
}

// jiaotuMinPointsForImage 计算本次调用的最低积分门槛。
func jiaotuMinPointsForImage(req *JiaotuImageRequest, buffer int) int {
	cost := 1
	if req.Upstream != nil {
		cost = req.Upstream.PointsCost()
	}
	minPoints := cost * req.Count
	if minPoints < req.Count {
		minPoints = req.Count
	}
	return minPoints + buffer
}

// forwardJiaotuImages 是 ForwardImages 的椒图分支：单账号一次尝试，失败由 handler 的换号循环接管。
func (s *OpenAIGatewayService) forwardJiaotuImages(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	_ []byte,
	parsed *OpenAIImagesRequest,
	channelMappedModel string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if parsed == nil {
		return nil, fmt.Errorf("jiaotu images: parsed request is required")
	}
	requestModel := strings.TrimSpace(parsed.Model)
	if mapped := strings.TrimSpace(channelMappedModel); mapped != "" {
		requestModel = mapped
	}
	scoped := *parsed
	scoped.Model = requestModel

	client := s.jiaotuClient()
	proxyURL := ""
	if account != nil && account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	catalog, catalogErr := s.jiaotuCatalogFor(ctx, client, proxyURL, false)
	if catalogErr != nil {
		logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 模型清单不可用，按本地稳定表继续: %v", catalogErr)
	}
	req, err := s.NormalizeJiaotuImagesRequest(&scoped, catalog)
	if err != nil {
		return nil, jiaotuClientFacingError(err)
	}
	token := account.JiaotuToken()
	if token == "" {
		s.markJiaotuAccountExpired(ctx, account, "缺少 token 凭据")
		return nil, &UpstreamFailoverError{StatusCode: http.StatusUnauthorized, ResponseBody: []byte(`{"error":"jiaotu token missing"}`)}
	}

	minPoints := jiaotuMinPointsForImage(req, client.Settings().MinPointsBuffer)
	logger.LegacyPrintf(
		"service.jiaotu",
		"[Jiaotu] Images 原生调用 upstream_base=%s endpoint=%s request_model=%s upstream_model_id=%d account=%s refs=%d n=%d size=%s min_points=%d account_points=%d",
		client.BaseURL(), jiaotuPathImageChat, requestModel, req.Model.ProviderID, account.JiaotuIdentityForLog(),
		len(req.References), req.Count, req.Ratio, minPoints, account.JiaotuPoints(),
	)
	if !account.JiaotuHasEnoughPoints(minPoints) {
		// 本地预检：积分不够就不该打上游，直接按「该号不可用」换下一个。
		s.markJiaotuAccountPointsExhausted(ctx, account, minPoints)
		return nil, &UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ResponseBody: []byte(fmt.Sprintf(`{"error":"jiaotu insufficient points","account_points":%d,"required":%d}`, account.JiaotuPoints(), minPoints))}
	}

	sessionID := jiaotuRandomHex(16)
	generateReq := &JiaotuGenerateRequest{
		SessionID:  sessionID,
		Prompt:     req.Prompt,
		ModelID:    req.Model.ProviderID,
		Count:      req.Count,
		Size:       req.Ratio,
		References: req.References,
	}
	if req.Upstream != nil {
		generateReq.ModelID = req.Upstream.ID
	}
	if generateReq.Size == "" {
		generateReq.Size = "1:1"
	}

	upstreamIDs, uploadErr := client.UploadReferences(ctx, token, generateReq, proxyURL)
	if uploadErr != nil {
		logJiaotuFailure("upload", account, uploadErr)
		s.handleJiaotuAccountError(ctx, account, uploadErr)
		return nil, jiaotuFailoverOrClientError(uploadErr)
	}
	generateReq.UploadedOSSIDs = upstreamIDs

	stream, streamErr := client.ImageChat(ctx, token, generateReq.ChatBody(false), proxyURL)
	if streamErr != nil {
		logJiaotuFailure("imageChat", account, streamErr)
		s.handleJiaotuAccountError(ctx, account, streamErr)
		// 已经拿到部分图片时不 failover：否则换号重跑会重复消耗积分并产生孤儿图。
		if stream != nil && len(stream.ImageURLs) > 0 {
			return s.writeJiaotuImagesResponse(c, account, req, stream.ImageURLs, startTime)
		}
		return nil, jiaotuFailoverOrClientError(streamErr)
	}
	// 上游把 imageChat 当对话用：提示词被判不明确时只回 questions。自动作答一轮并
	// **保持同一 sessionId** 续发，才能接着拿到图（契约 R8）。
	if stream.NeedsClarification() && client.Settings().AutoAnswerQuestions {
		answer, ok := jiaotuClarificationAnswer(req.Prompt, stream.ClarificationQuestions())
		if !ok {
			logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 上游反问但无法自动作答（问法无选项），account=%s",
				account.JiaotuIdentityForLog())
		} else {
			logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 上游反问→自动应答续发同一会话 account=%s session=%s 应答=%s",
				account.JiaotuIdentityForLog(), generateReq.SessionID, truncateJiaotuMessage(answer))
			generateReq.Prompt = answer
			stream, streamErr = client.ImageChat(ctx, token, generateReq.ChatBody(false), proxyURL)
			if streamErr != nil {
				logJiaotuFailure("imageChat/answer", account, streamErr)
				s.handleJiaotuAccountError(ctx, account, streamErr)
				if stream != nil && len(stream.ImageURLs) > 0 {
					return s.writeJiaotuImagesResponse(c, account, req, stream.ImageURLs, startTime)
				}
				return nil, jiaotuFailoverOrClientError(streamErr)
			}
		}
	}
	if len(stream.ImageURLs) == 0 {
		emptyErr := jiaotuEmptyStreamError(stream, "椒图图片流结束但未返回图片")
		logJiaotuFailure("imageChat", account, emptyErr)
		return nil, jiaotuFailoverOrClientError(emptyErr)
	}
	logger.LegacyPrintf(
		"service.jiaotu",
		"[Jiaotu] imageChat 成功 account=%s model=%s images=%d 耗时=%s",
		account.JiaotuIdentityForLog(), req.Model.StableID, len(stream.ImageURLs), time.Since(startTime).Round(time.Millisecond),
	)
	return s.writeJiaotuImagesResponse(c, account, req, stream.ImageURLs, startTime)
}

// writeJiaotuImagesResponse 组装 OpenAI Images 响应并写回客户端。
func (s *OpenAIGatewayService) writeJiaotuImagesResponse(
	c *gin.Context,
	account *Account,
	req *JiaotuImageRequest,
	imageURLs []string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	data := make([]map[string]any, 0, len(imageURLs))
	for _, imageURL := range imageURLs {
		entry := map[string]any{}
		if req.ResponseFormat == "b64_json" {
			raw, _, err := s.jiaotuClient().FetchResultBytes(c.Request.Context(), imageURL, "")
			if err != nil {
				return nil, jiaotuClientFacingError(err)
			}
			entry["b64_json"] = base64.StdEncoding.EncodeToString(raw)
		} else {
			entry["url"] = imageURL
		}
		data = append(data, entry)
	}
	if len(data) == 0 {
		return nil, jiaotuClientFacingError(newJiaotuError("imageChat", JiaotuErrEmptyResult, 0, "图片生成结果为空"))
	}
	response := map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	}
	if size := strings.TrimSpace(req.Ratio); size != "" {
		response["size"] = size
	}
	body, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("marshal jiaotu images response: %w", err)
	}
	// 成功后清掉该号的历史失败说明，便于管理页判断健康度。
	s.clearJiaotuAccountError(c.Request.Context(), account)

	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)

	count := len(data)
	return &OpenAIForwardResult{
		Model:            req.Model.StableID,
		UpstreamModel:    req.Model.ProviderName,
		UpstreamEndpoint: jiaotuPathImageChat,
		Duration:         time.Since(startTime),
		ImageCount:       count,
		ImageSize:        req.Ratio,
		ImageInputSize:   req.Ratio,
		ImageOutputSizes: repeatJiaotuString(req.Ratio, count),
	}, nil
}

func repeatJiaotuString(value string, count int) []string {
	if value == "" || count <= 0 {
		return nil
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, value)
	}
	return out
}

// jiaotuEmptyStreamError 把「流结束但没产物」分成两类：
//   - 上游在 text 事件里回了业务错误（如「当前模型维护中，请选择其它模型使用」）→
//     model_unavailable：该模型当下不可用，与账号无关，换号必然重复失败，直接回 400；
//   - 其它真空结果 → empty_result：保留换号重试语义。
//
// 上游不发 error 事件而是把拒绝当对话文本回，不这样辨认的话客户端只能看到通用 502。
// model_unavailable 时 message 只用上游原话：这句会直接展示给用户，前面拼
// 「椒图图片流结束但未返回图片：」只会把人引到错误方向（像是上游挂了，其实是模型在下线维护）。
func jiaotuEmptyStreamError(stream *JiaotuStreamResult, prefix string) error {
	if message := stream.UpstreamErrorMessage(); message != "" {
		return newJiaotuError("imageChat", JiaotuErrModelUnavailable, 0, "%s", message)
	}
	return newJiaotuError("imageChat", JiaotuErrEmptyResult, 0,
		"%s：%s", prefix, jiaotuDescribeEvents(stream))
}

// jiaotuFailoverOrClientError 把上游错误分成「可换号」与「直接回客户端」两类。
func jiaotuFailoverOrClientError(err error) error {
	jerr, ok := IsJiaotuError(err)
	if !ok {
		return jiaotuFailoverFromMessage(http.StatusBadGateway, fmt.Sprint(err))
	}
	// invalid_request 与 model_unavailable 都属于「换号也白搭」：前者是本地参数错，
	// 后者是上游按模型拒绝。两者都必须把上游/本地原话透给客户端。
	if jerr.NonRetryableWithNextAccount() {
		return jiaotuClientFacingError(err)
	}
	if !jerr.RetryableWithNextAccount() {
		// 不可重试的上游错误也交给 failover 循环收尾：它会把本次账号排除后按上游原样报错。
		return jiaotuFailoverFromMessage(orDefaultStatus(jerr.StatusCode, http.StatusBadGateway), jerr.Error())
	}
	return &UpstreamFailoverError{
		StatusCode:             orDefaultStatus(jerr.StatusCode, http.StatusBadGateway),
		ResponseBody:           []byte(sanitizeUpstreamErrorMessage(jerr.Error())),
		RetryableOnSameAccount: false,
	}
}

func jiaotuFailoverFromMessage(status int, message string) error {
	return &UpstreamFailoverError{
		StatusCode:             status,
		ResponseBody:           []byte(sanitizeUpstreamErrorMessage(message)),
		RetryableOnSameAccount: false,
	}
}

// jiaotuClientFacingError 终端类错误：不换号，直接把原因回给客户端。
//
// Message 与 Code 分开传：客户端 JSON 里 error.message 必须是上游原话
// （「当前模型维护中，请选择其它模型使用」），error.code 才是机器可读分类。
// jerr.Message 在 Error() 里会被拼成 "椒图 <op> 失败（<kind>）：<原话>"，直接当
// message 用会把分类前缀也塞给用户。
func jiaotuClientFacingError(err error) error {
	if jerr, ok := IsJiaotuError(err); ok {
		return &OpenAIImagesUpstreamError{
			StatusCode: jerr.ClientStatus(),
			ErrorType:  "invalid_request_error",
			Code:       string(jerr.Kind),
			Message:    sanitizeUpstreamErrorMessage(jerr.Message),
		}
	}
	return &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadRequest,
		ErrorType:  "invalid_request_error",
		Message:    sanitizeUpstreamErrorMessage(fmt.Sprint(err)),
	}
}

func orDefaultStatus(status int, fallback int) int {
	if status > 0 {
		return status
	}
	return fallback
}

// handleJiaotuAccountError 按错误分类回写账号状态（积分/下架/冷却）。
func (s *OpenAIGatewayService) handleJiaotuAccountError(ctx context.Context, account *Account, err error) {
	HandleJiaotuAccountError(ctx, s.jiaotuAccountStore(), account, err)
}

// HandleJiaotuAccountError 供账号探测/后台维护等复用：按错误分类回写账号状态。
func HandleJiaotuAccountError(ctx context.Context, store JiaotuAccountStore, account *Account, err error) {
	jerr, ok := IsJiaotuError(err)
	if !ok || account == nil {
		return
	}
	switch jerr.Kind {
	case JiaotuErrAuth:
		markJiaotuAccountExpired(ctx, store, account, jerr.Message)
	case JiaotuErrInsufficientPoints:
		markJiaotuAccountPointsExhausted(ctx, store, account, account.JiaotuPoints())
	default:
		if jerr.ExhaustsAccount() {
			markJiaotuAccountPointsExhausted(ctx, store, account, account.JiaotuPoints())
		}
	}
}

func (s *OpenAIGatewayService) markJiaotuAccountExpired(ctx context.Context, account *Account, reason string) {
	markJiaotuAccountExpired(ctx, s.jiaotuAccountStore(), account, reason)
}

func (s *OpenAIGatewayService) markJiaotuAccountPointsExhausted(ctx context.Context, account *Account, minPoints int) {
	markJiaotuAccountPointsExhausted(ctx, s.jiaotuAccountStore(), account, minPoints)
}

func (s *OpenAIGatewayService) clearJiaotuAccountError(ctx context.Context, account *Account) {
	clearJiaotuAccountError(ctx, s.jiaotuAccountStore(), account)
}

// JiaotuAccountStore 状态回写所需的最小持久面（方便单测注入 fake）。
type JiaotuAccountStore interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
	Update(ctx context.Context, account *Account) error
}

// markJiaotuAccountExpired 上游登录失效：标 expired 并硬停，等管理员处理（椒图 token 无 refresh）。
func markJiaotuAccountExpired(ctx context.Context, store JiaotuAccountStore, account *Account, reason string) {
	patchJiaotuAccountState(ctx, store, account, func(creds map[string]any) {
		creds[JiaotuCredentialUpstreamStatus] = JiaotuUpstreamStatusExpired
		creds[JiaotuCredentialLastError] = truncateJiaotuMessage(reason)
	}, reason, true)
}

// markJiaotuAccountPointsExhausted 积分不足：清零 + 临时冷却（签到/充值后可恢复，不永久下架）。
func markJiaotuAccountPointsExhausted(ctx context.Context, store JiaotuAccountStore, account *Account, minPoints int) {
	patchJiaotuAccountState(ctx, store, account, func(creds map[string]any) {
		creds[JiaotuCredentialPoints] = 0
		creds[JiaotuCredentialLastError] = truncateJiaotuMessage(fmt.Sprintf("积分不足（需要 %d）", minPoints))
		creds[JiaotuCredentialPointsAt] = time.Now().UTC().Format(time.RFC3339)
	}, fmt.Sprintf("椒图积分不足，需要 %d", minPoints), false)
}

// clearJiaotuAccountError 成功后清掉历史失败说明，便于管理页判断健康度。
func clearJiaotuAccountError(ctx context.Context, store JiaotuAccountStore, account *Account) {
	if account == nil || account.Credentials == nil {
		return
	}
	if strings.TrimSpace(account.GetCredential(JiaotuCredentialLastError)) == "" {
		return
	}
	patchJiaotuAccountState(ctx, store, account, func(creds map[string]any) {
		creds[JiaotuCredentialLastError] = nil // 显式删除；空字符串会被当成有效值保留
	}, "", false)
}

// patchJiaotuAccountState 写回凭据/状态。失败只记日志：状态回写不得影响本次请求的响应。
func patchJiaotuAccountState(ctx context.Context, store JiaotuAccountStore, account *Account, patch func(map[string]any), errorMessage string, pause bool) {
	if account == nil || store == nil || patch == nil {
		return
	}
	credentials := account.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	merged := make(map[string]any, len(credentials)+2)
	for key, value := range credentials {
		merged[key] = value
	}
	patch(merged)

	fresh, err := store.GetByID(ctx, account.ID)
	if err != nil || fresh == nil {
		logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 读取账号失败，跳过状态回写 account=%d err=%v", account.ID, err)
		return
	}
	fresh.Credentials = MergeJiaotuCredentials(toJSONMap(fresh.Credentials), merged)
	if errorMessage != "" {
		fresh.ErrorMessage = truncateJiaotuMessage(errorMessage)
	}
	if pause {
		fresh.Schedulable = false
		fresh.Status = "inactive"
	} else {
		fresh.TempUnschedulableUntil = jiaotuPtrTime(time.Now().Add(jiaotuPointsCooldown))
		fresh.TempUnschedulableReason = "jiaotu_insufficient_points"
	}
	if err := store.Update(ctx, fresh); err != nil {
		logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 状态回写失败 account=%d err=%v", account.ID, err)
		return
	}
	account.Credentials = merged
	account.ErrorMessage = fresh.ErrorMessage
	account.Schedulable = fresh.Schedulable
	account.Status = fresh.Status
	account.TempUnschedulableUntil = fresh.TempUnschedulableUntil
	account.TempUnschedulableReason = fresh.TempUnschedulableReason
}

func truncateJiaotuMessage(message string) string {
	message = strings.TrimSpace(sanitizeUpstreamErrorMessage(message))
	const limit = 480
	if len(message) > limit {
		return message[:limit] + "…"
	}
	return message
}

// jiaotuClarificationAnswer 用客户端原始需求回答上游的结构化反问。
// 规则：选项里能在原始需求中找到依据的优先，否则取第一个；
// 只要有一个问题没给出选项，就不猜——返回 ok=false 交给上层保留真实错因。
func jiaotuClarificationAnswer(originalPrompt string, questions []JiaotuQuestion) (string, bool) {
	if len(questions) == 0 {
		return "", false
	}
	picks := make([]string, 0, len(questions))
	for _, question := range questions {
		if len(question.Options) == 0 {
			return "", false
		}
		choice := question.Options[0]
		for _, option := range question.Options {
			if option = strings.TrimSpace(option); option != "" && strings.Contains(originalPrompt, option) {
				choice = option
				break
			}
		}
		picks = append(picks, fmt.Sprintf("%s：%s", strings.TrimSpace(question.Question), choice))
	}
	return fmt.Sprintf("%s。请严格按以下需求直接生成图片，不要反问：%s", strings.Join(picks, "；"), originalPrompt), true
}

// JiaotuMaxAccountSwitches 返回椒图媒体请求允许的**额外**换号次数（与全局上限取 min）。
// 配置值 N 含义为「最多尝试 N+1 个号」；<=0 表示不额外收紧。
// 椒图号池按积分逐号消耗，默认 3 与 kuikui 的三轮重试保持一致。
func (s *OpenAIGatewayService) JiaotuMaxAccountSwitches() int {
	if s == nil || s.cfg == nil {
		return 0
	}
	attempts := s.cfg.Jiaotu.MaxAttempts
	if attempts <= 0 {
		return 0
	}
	return attempts
}

// logJiaotuFailure 记录上游失败的具体分类与原文；failover 本身只记 status，不带原因，
// 没有这行日志就无法区分「账号坏了」与「请求发对了但上游报错」。
func logJiaotuFailure(stage string, account *Account, err error) {
	identity := ""
	if account != nil {
		identity = account.JiaotuIdentityForLog()
	}
	kind := "unknown"
	status := 0
	if jerr, ok := IsJiaotuError(err); ok {
		kind = string(jerr.Kind)
		status = jerr.StatusCode
	}
	logger.LegacyPrintf(
		"service.jiaotu",
		"[Jiaotu] %s 失败 account=%s kind=%s upstream_status=%d err=%v",
		stage, identity, kind, status, err,
	)
}

// jiaotuDescribeEvents 把 SSE 事件概况（类型计数 + 首个原始事件）拼成一行诊断文本。
// 没有它就无法区分「上游真的没出图」与「事件字段变了导致我们读不到」。
func jiaotuDescribeEvents(stream *JiaotuStreamResult) string {
	if stream == nil || len(stream.Events) == 0 {
		return "事件数 0"
	}
	counts := map[string]int{}
	for _, event := range stream.Events {
		counts[event.string("type")]++
	}
	types := make([]string, 0, len(counts))
	for typeName, count := range counts {
		if typeName == "" {
			typeName = "(无type)"
		}
		types = append(types, fmt.Sprintf("%s×%d", typeName, count))
	}
	sample, _ := json.Marshal(stream.Events[0])
	if len(sample) > 300 {
		sample = sample[:300]
	}
	diagnostic := fmt.Sprintf("事件数 %d，类型[%s]，首个事件=%s", len(stream.Events), strings.Join(types, " "), sample)

	// 上游会把 imageChat 当对话用：把 questions 事件与正文片段带出来，才能判断该怎么应答。
	var questionsRaw []byte
	var textParts []string
	for _, event := range stream.Events {
		switch strings.ToLower(event.string("type")) {
		case "questions":
			if raw, err := json.Marshal(event); err == nil && len(questionsRaw) < 700 {
				questionsRaw = raw
			}
		case "text":
			if content := event.string("content"); content != "" && len(strings.Join(textParts, "")) < 400 {
				textParts = append(textParts, content)
			}
		}
	}
	if len(questionsRaw) > 0 {
		diagnostic += fmt.Sprintf("；questions=%s", questionsRaw)
	}
	if len(textParts) > 0 {
		diagnostic += fmt.Sprintf("；正文=%s", strings.Join(textParts, ""))
	}
	return diagnostic
}

func toJSONMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

func jiaotuPtrTime(t time.Time) *time.Time { return &t }

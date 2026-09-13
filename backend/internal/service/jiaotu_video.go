package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 椒图视频通道：与图片共用 imageChat（SSE），差别只在请求体带 duration/qualityLevel/audio，
// 结果事件的 type 是 "video"。协议字段逐条对齐 kuikui generateVideo（main.go:1097-1196）。

// JiaotuVideoRequest 归一化后的视频生成请求（已过全部本地校验）。
type JiaotuVideoRequest struct {
	Model      JiaotuModelSpec
	Upstream   *JiaotuUpstreamModel
	Prompt     string
	Duration   int
	Ratio      string
	Quality    string
	Audio      bool
	References []JiaotuReference

	ReferenceVideoURL string
	ShotType          string
	PromptExtend      *bool
}

// JiaotuVideoResult 一次成功的视频生成。
type JiaotuVideoResult struct {
	URL            string
	TaskID         string
	ModelID        int
	ModelName      string
	Duration       int
	Ratio          string
	Quality        string
	Audio          bool
	ReferenceCount int
}

// MinPointsCost 本次视频调用的最低积分：按画质单价 × 时长（kuikui main.go:1124）。
func (r *JiaotuVideoRequest) MinPointsCost() int {
	cost := 1
	if r.Upstream != nil {
		cost = r.Upstream.PointsCostForQuality(r.Quality)
	}
	if cost < 1 {
		cost = 1
	}
	duration := r.Duration
	if duration < 1 {
		duration = 1
	}
	return cost * duration
}

// ChatBody 组装 imageChat 视频请求体。
func (r *JiaotuVideoRequest) ChatBody(sessionID string, ossIDs []string) map[string]any {
	body := map[string]any{
		"sessionId":    sessionID,
		"requestMode":  "standard",
		"prompt":       r.Prompt,
		"imagesIds":    append([]string{}, ossIDs...),
		"imageCount":   1, // 视频固定 1，上游按此字段判产物数量
		"modelId":      r.upstreamModelID(),
		"size":         r.Ratio,
		"duration":     strconv.Itoa(r.Duration), // 上游要求字符串
		"qualityLevel": r.Quality,
		"audio":        r.Audio,
	}
	if value := strings.TrimSpace(r.ReferenceVideoURL); value != "" {
		body["referenceVideoUrl"] = value
	}
	if value := strings.TrimSpace(r.ShotType); value != "" {
		body["shotType"] = value
	}
	if r.PromptExtend != nil {
		body["promptExtend"] = *r.PromptExtend
	}
	return body
}

func (r *JiaotuVideoRequest) upstreamModelID() int {
	if r.Upstream != nil && r.Upstream.ID > 0 {
		return r.Upstream.ID
	}
	return r.Model.ProviderID
}

// MaxReferenceImages 该模型允许的参考图上限（H3 为 9）。
func (r *JiaotuVideoRequest) MaxReferenceImages() int {
	if r.Upstream != nil {
		return r.Upstream.MaxReferenceImages()
	}
	return jiaotuDefaultRefLimit
}

// NormalizeJiaotuVideoRequest 校验并归一化 /v1/videos 入参。
// 所有不合法组合都在这里本地报错，绝不把注定失败的请求打到上游烧积分。
func NormalizeJiaotuVideoRequest(input map[string]any, catalog *JiaotuCatalog, settings JiaotuSettings) (*JiaotuVideoRequest, error) {
	if input == nil {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "请求体必须是 JSON 对象")
	}
	rawModel := jiaotuStringValue(input["model"])
	spec, known := catalog.SpecFor(rawModel)
	if !known {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "未知椒图模型：%s", strings.TrimSpace(rawModel))
	}
	if spec.Capability != JiaotuCapabilityVideo {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "模型 %s 是椒图图片模型，请使用 /v1/images", spec.StableID)
	}

	prompt := strings.TrimSpace(jiaotuStringValue(input["prompt"]))
	if prompt == "" {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "缺少必填参数：prompt")
	}
	if len(prompt) > jiaotuMaxVideoPromptChars {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "提示词最多 %d 个字符，当前 %d 个", jiaotuMaxVideoPromptChars, len(prompt))
	}

	// 上游会把不支持的参数当成隐式行为差异；明确拒绝比悄悄丢弃诚实。
	if _, ok := input["seed"]; ok {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "椒图视频通道不支持 seed")
	}
	if hasJiaotuValues(input["referenceAudios"]) || hasJiaotuValues(input["reference_audios"]) || hasJiaotuValues(input["audio_files"]) {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0, "椒图当前视频通道不支持参考音频")
	}

	req := &JiaotuVideoRequest{
		Model:             spec,
		Prompt:            prompt,
		Audio:             jiaotuBoolValue(input["audio"]),
		ReferenceVideoURL: strings.TrimSpace(jiaotuStringValue(input["referenceVideoUrl"])),
		ShotType:          strings.TrimSpace(jiaotuStringValue(input["shotType"])),
	}
	if value, ok := input["promptExtend"]; ok {
		normalized := jiaotuBoolValue(value)
		req.PromptExtend = &normalized
	}
	if catalog != nil {
		if upstream, _, found := catalog.Resolve(spec.StableID); found {
			copyUpstream := upstream
			req.Upstream = &copyUpstream
		}
	}

	durationList, qualityList, ratioList := []string{"5"}, []string{"768P"}, []string{"16:9"}
	if req.Upstream != nil {
		durationList = req.Upstream.Options(req.Upstream.DurationList)
		qualityList = req.Upstream.Options(req.Upstream.QualityLevelList)
		ratioList = req.Upstream.Options(req.Upstream.ResolutionList)
	}

	requestedDuration := jiaotuIntValue(firstJiaotuValue(input["duration"], input["seconds"]))
	if requestedDuration < 1 {
		requestedDuration = jiaotuDefaultVideoDuration
	}
	if requestedDuration > jiaotuMaxVideoDurationSeconds {
		return nil, newJiaotuError("video/normalize", JiaotuErrInvalidRequest, 0,
			"时长最多 %d 秒，当前 %d 秒", jiaotuMaxVideoDurationSeconds, requestedDuration)
	}
	req.Duration = chooseJiaotuOptionNumber(durationList, requestedDuration, jiaotuDefaultVideoDuration)

	// resolution_name / resolution 都接受，画质优先按上游 qualityLevelList 归一。
	req.Quality = chooseJiaotuOption(qualityList, firstJiaotuString(input["qualityLevel"], input["resolution_name"], input["quality"]), "768P")
	req.Ratio = chooseJiaotuOption(ratioList, firstJiaotuString(input["size"], input["aspect_ratio"], input["ratio"]), "16:9")

	refs, err := jiaotuReferencesFromInput(input, settings.MaxRefBytes)
	if err != nil {
		return nil, err
	}
	client := &JiaotuClient{settings: settings}
	if err := client.ValidateJiaotuReferences(refs, req.MaxReferenceImages()); err != nil {
		return nil, err
	}
	req.References = refs
	return req, nil
}

// GenerateJiaotuVideo 上传参考图并跑一次 imageChat 视频流。
// 单账号一次尝试：失败由调用方决定换号还是报错。
func GenerateJiaotuVideo(ctx context.Context, client *JiaotuClient, token string, req *JiaotuVideoRequest, proxyURL string) (*JiaotuVideoResult, error) {
	if client == nil || req == nil {
		return nil, newJiaotuError("video", JiaotuErrInvalidRequest, 0, "缺少客户端或请求")
	}
	if strings.TrimSpace(token) == "" {
		return nil, newJiaotuError("video", JiaotuErrAuth, 0, "缺少 token 凭据")
	}

	generateReq := &JiaotuGenerateRequest{
		SessionID:         jiaotuRandomHex(16),
		Prompt:            req.Prompt,
		ModelID:           req.upstreamModelID(),
		Count:             1,
		Size:              req.Ratio,
		References:        req.References,
		Duration:          req.Duration,
		QualityLevel:      req.Quality,
		Audio:             req.Audio,
		ReferenceVideoURL: req.ReferenceVideoURL,
		ShotType:          req.ShotType,
		PromptExtend:      req.PromptExtend,
	}
	ossIDs, err := client.UploadReferences(ctx, token, generateReq, proxyURL)
	if err != nil {
		return nil, err
	}
	stream, err := client.ImageChat(ctx, token, req.ChatBody(generateReq.SessionID, ossIDs), proxyURL)
	if err != nil {
		return nil, err
	}
	if len(stream.VideoURLs) == 0 {
		// 上游把「模型维护中」这类拒绝当对话文本回：与图片同口径先认业务错误，
		// 否则视频侧同样只会得到一个查不出原因的通用 502。
		if message := stream.UpstreamErrorMessage(); message != "" {
			return nil, newJiaotuError("imageChat", JiaotuErrModelUnavailable, 0,
				"椒图视频生成未执行：%s", message)
		}
		if stream.NeedsClarification() {
			return nil, newJiaotuError("imageChat", JiaotuErrEmptyResult, 0, "椒图视频流要求补充信息：%s", jiaotuDescribeStream(stream))
		}
		return nil, newJiaotuError("imageChat", JiaotuErrEmptyResult, 0, "椒图视频流结束但未返回视频：%s", jiaotuDescribeStream(stream))
	}
	name := req.Model.DisplayName
	if req.Upstream != nil {
		name = req.Upstream.DisplayName()
	}
	return &JiaotuVideoResult{
		URL:            stream.VideoURLs[0],
		TaskID:         stream.TaskID,
		ModelID:        req.upstreamModelID(),
		ModelName:      name,
		Duration:       req.Duration,
		Ratio:          req.Ratio,
		Quality:        req.Quality,
		Audio:          req.Audio,
		ReferenceCount: len(ossIDs),
	}, nil
}

func jiaotuDescribeStream(stream *JiaotuStreamResult) string {
	if stream == nil {
		return "无事件"
	}
	summary := fmt.Sprintf("事件数 %d", len(stream.Events))
	if text := stream.TextContent(200); text != "" {
		summary += "，正文=" + text
	}
	return summary
}

const (
	jiaotuMaxVideoPromptChars     = 10000
	jiaotuDefaultVideoDuration    = 5
	jiaotuMaxVideoDurationSeconds = 15
)

// chooseJiaotuOptionNumber 在允许值里挑最接近请求值的选项（kuikui chooseDuration）。
func chooseJiaotuOptionNumber(options []string, requested, fallback int) int {
	best, distance := fallback, int(^uint(0)>>1)
	seen := false
	for _, option := range options {
		value, err := strconv.Atoi(strings.TrimSpace(option))
		if err != nil {
			continue
		}
		gap := value - requested
		if gap < 0 {
			gap = -gap
		}
		if !seen || gap < distance {
			best, distance, seen = value, gap, true
		}
	}
	if !seen {
		return requested
	}
	return best
}

// chooseJiaotuOption 在允许值里挑大小写无关的匹配，退到 fallback，再退到首项。
func chooseJiaotuOption(options []string, requested, fallback string) string {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), strings.TrimSpace(requested)) {
			return strings.TrimSpace(option)
		}
	}
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), fallback) {
			return strings.TrimSpace(option)
		}
	}
	if len(options) > 0 {
		return strings.TrimSpace(options[0])
	}
	return strings.TrimSpace(requested)
}

func jiaotuBoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case json.Number:
		n, _ := typed.Int64()
		return n != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "有声":
			return true
		}
	}
	return false
}

func hasJiaotuValues(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return true
	}
}

func firstJiaotuValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstJiaotuString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(jiaotuStringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

// jiaotuReferencesFromInput 从入站载荷取参考图：支持 dataURL 字符串数组与
// [{dataUrl|url}] 对象数组（对齐 kuikui parseReferences 的两种形态）。
func jiaotuReferencesFromInput(input map[string]any, maxBytes int64) ([]JiaotuReference, error) {
	raw, ok := input["image"]
	if !ok || raw == nil {
		raw = input["referenceImages"]
	}
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		single := strings.TrimSpace(jiaotuStringValue(raw))
		if single == "" {
			return nil, nil
		}
		items = []any{single}
	}
	refs := make([]JiaotuReference, 0, len(items))
	for index, item := range items {
		dataURL := ""
		switch typed := item.(type) {
		case string:
			dataURL = typed
		case map[string]any:
			dataURL = firstJiaotuString(typed["dataUrl"], typed["data_url"], typed["url"])
		}
		if strings.TrimSpace(dataURL) == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(dataURL)), "data:") {
			return nil, newJiaotuError("references", JiaotuErrInvalidRequest, 0,
				"第 %d 张参考图只接受 dataURL，不支持外链 URL", index+1)
		}
		ref, err := DecodeJiaotuDataURL(dataURL, maxBytes)
		if err != nil {
			return nil, newJiaotuError("references", JiaotuErrInvalidRequest, 0, "第 %d 张参考图无效：%v", index+1, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// --- OpenAIGatewayService 侧入口（供 handler 使用）---

// JiaotuVideoCatalog 返回视频参数校验所需的模型清单（带 TTL 缓存）。
func (s *OpenAIGatewayService) JiaotuVideoCatalog(ctx context.Context) (*JiaotuCatalog, error) {
	if s == nil {
		return nil, newJiaotuError("video/catalog", JiaotuErrNetwork, 0, "gateway service unavailable")
	}
	client := s.jiaotuClient()
	return s.jiaotuCatalogFor(ctx, client, "", false)
}

// NormalizeJiaotuVideo 归一化 /v1/videos 入参。
func (s *OpenAIGatewayService) NormalizeJiaotuVideo(ctx context.Context, input map[string]any, catalog *JiaotuCatalog) (*JiaotuVideoRequest, error) {
	return NormalizeJiaotuVideoRequest(input, catalog, s.jiaotuClient().Settings())
}

// GenerateJiaotuVideoForAccount 在指定账号上跑一次视频生成：先本地积分预检，
// 失败按错误分类回写账号状态（积分清零/下架），供调度器换号。
func (s *OpenAIGatewayService) GenerateJiaotuVideoForAccount(ctx context.Context, account *Account, req *JiaotuVideoRequest) (*JiaotuVideoResult, error) {
	if account == nil || req == nil {
		return nil, newJiaotuError("video", JiaotuErrInvalidRequest, 0, "缺少账号或请求")
	}
	client := s.jiaotuClient()
	token := account.JiaotuToken()
	if token == "" {
		markJiaotuAccountExpired(ctx, s.jiaotuAccountStore(), account, "缺少 token 凭据")
		return nil, newJiaotuError("video", JiaotuErrAuth, 0, "账号缺少 token")
	}
	minPoints := req.MinPointsCost() + client.Settings().MinPointsBuffer
	if !account.JiaotuHasEnoughPoints(minPoints) {
		markJiaotuAccountPointsExhausted(ctx, s.jiaotuAccountStore(), account, minPoints)
		return nil, newJiaotuError("video", JiaotuErrInsufficientPoints, 0, "账号积分不足（需要 %d，当前 %d）", minPoints, account.JiaotuPoints())
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	result, err := GenerateJiaotuVideo(ctx, client, token, req, proxyURL)
	if err != nil {
		HandleJiaotuAccountError(ctx, s.jiaotuAccountStore(), account, err)
		return nil, err
	}
	clearJiaotuAccountError(ctx, s.jiaotuAccountStore(), account)
	return result, nil
}

// FetchJiaotuVideoBytes 代拉上游视频产物（受 max_result_mb 限制）。
func (s *OpenAIGatewayService) FetchJiaotuVideoBytes(ctx context.Context, rawURL string) ([]byte, string, error) {
	if s == nil {
		return nil, "", newJiaotuError("video/content", JiaotuErrNetwork, 0, "gateway service unavailable")
	}
	return s.jiaotuClient().FetchResultBytes(ctx, rawURL, "")
}

// JiaotuVideoUsageResult 把视频结果包装成网关通用的计费结构。
func JiaotuVideoUsageResult(req *JiaotuVideoRequest, result *JiaotuVideoResult) *OpenAIForwardResult {
	if result == nil {
		return &OpenAIForwardResult{VideoCount: 0}
	}
	model := req.Model.StableID
	upstream := req.Model.ProviderName
	if result.ModelName != "" {
		upstream = result.ModelName
	}
	return &OpenAIForwardResult{
		Model:                model,
		BillingModel:         model,
		UpstreamModel:        upstream,
		UpstreamEndpoint:     jiaotuPathImageChat,
		VideoCount:           1,
		VideoResolution:      result.Quality,
		VideoDurationSeconds: result.Duration,
	}
}

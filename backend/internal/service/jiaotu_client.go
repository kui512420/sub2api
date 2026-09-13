package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
)

// 椒图上游客户端：直连 https://api.jiaotuai.cn。
// 协议事实来源：docs/JIAOTU_NATIVE_INTEGRATION.md（逐字段对齐 kuikui cmd/kuikui/main.go）。

const (
	jiaotuPathModels       = "/api/v1/ai/queryModels"
	jiaotuPathImageChat    = "/api/v1/ai/imageChat"
	jiaotuPathUploadToken  = "/api/v1/upload/token"
	jiaotuPathUploadNotify = "/api/v1/upload/callback"
	jiaotuPathBilling      = "/api/v1/userBilling/page?pageNum=1&pageSize=1"
	jiaotuPathSignIn       = "/api/v1/sign/getSing"
	jiaotuAcceptJSON       = "application/json, text/plain, */*"
	jiaotuUserAgent        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	// jiaotuStreamUserAgent 是 imageChat 实际使用的短 UA，与 kuikui stream() 完全一致；
	// 上游对两类接口的指纹要求不同，不得混用。
	jiaotuStreamUserAgent = "Mozilla/5.0 Chrome/131 Safari/537.36"
	jiaotuSECHUA          = `"Chromium";v="131", "Google Chrome";v="131", "Not_A Brand";v="24"`
	jiaotuSSELineMaxBytes = 4 * 1024 * 1024
)

// JiaotuErrorKind 上游失败的分类，决定 sub2api 是「换号重试」还是「直接报错」。
type JiaotuErrorKind string

const (
	JiaotuErrNetwork            JiaotuErrorKind = "network"
	JiaotuErrTimeout            JiaotuErrorKind = "timeout"
	JiaotuErrAuth               JiaotuErrorKind = "auth"                // 401/403：token 失效，号应下架
	JiaotuErrInsufficientPoints JiaotuErrorKind = "insufficient_points" // 上游明确积分不足：换号
	JiaotuErrUpstream5xx        JiaotuErrorKind = "upstream_5xx"        // 上游瞬时故障：换号
	JiaotuErrUpstream4xx        JiaotuErrorKind = "upstream_4xx"        // 参数/内容类：不换号
	JiaotuErrEmptyResult        JiaotuErrorKind = "empty_result"        // 流结束但无产物：换号
	JiaotuErrBadResponse        JiaotuErrorKind = "bad_response"        // 响应结构不合预期：换号
	JiaotuErrInvalidRequest     JiaotuErrorKind = "invalid_request"     // 本地入参校验失败：400
	// JiaotuErrModelUnavailable 上游按模型拒绝本次生成（如「当前模型维护中」）。
	// 它与账号无关：实测 13 个不同号对同一模型返回同一句话，换号只会重复失败并白等。
	// 直接回客户端，让用户换模型。
	JiaotuErrModelUnavailable JiaotuErrorKind = "model_unavailable"
)

// JiaotuError 携带分类的上游错误。
type JiaotuError struct {
	Kind       JiaotuErrorKind
	StatusCode int
	Message    string
	Op         string
}

func (e *JiaotuError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("椒图 %s 失败（HTTP %d / %s）：%s", e.Op, e.StatusCode, e.Kind, e.Message)
	}
	return fmt.Sprintf("椒图 %s 失败（%s）：%s", e.Op, e.Kind, e.Message)
}

// RetryableWithNextAccount 表示「换下一个椒图号还有意义」。
func (e *JiaotuError) RetryableWithNextAccount() bool {
	switch e.Kind {
	case JiaotuErrInsufficientPoints, JiaotuErrUpstream5xx, JiaotuErrEmptyResult,
		JiaotuErrBadResponse, JiaotuErrNetwork, JiaotuErrTimeout, JiaotuErrAuth:
		return true
	default:
		return false
	}
}

// ExhaustsAccount 表示该号已不可用，应下架/冷却而不是继续派单。
func (e *JiaotuError) ExhaustsAccount() bool {
	return e.Kind == JiaotuErrAuth || e.Kind == JiaotuErrInsufficientPoints
}

// NonRetryableWithNextAccount 表示这类错误换号重试毫无意义，必须直接把原因回给客户端：
//   - invalid_request：本地入参不合法，每个号都会同样失败；
//   - model_unavailable：上游按「模型」而非「账号」拒绝（实测 13 个号同一回复），
//     换号只会把 7 秒耗时和同样的失败重复三遍，最后还退化成一个查不出原因的通用 502。
func (e *JiaotuError) NonRetryableWithNextAccount() bool {
	return e.Kind == JiaotuErrInvalidRequest || e.Kind == JiaotuErrModelUnavailable
}

// ClientStatus 映射到对客户端的 HTTP 状态。
func (e *JiaotuError) ClientStatus() int {
	switch e.Kind {
	case JiaotuErrInvalidRequest, JiaotuErrModelUnavailable:
		return http.StatusBadRequest
	case JiaotuErrAuth, JiaotuErrInsufficientPoints:
		return http.StatusServiceUnavailable
	case JiaotuErrUpstream4xx:
		if e.StatusCode > 0 {
			return e.StatusCode
		}
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

// IsJiaotuError 供 handler 层判定错误类型。
func IsJiaotuError(err error) (*JiaotuError, bool) {
	var target *JiaotuError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func newJiaotuError(op string, kind JiaotuErrorKind, statusCode int, format string, args ...any) *JiaotuError {
	return &JiaotuError{Kind: kind, StatusCode: statusCode, Message: fmt.Sprintf(format, args...), Op: op}
}

// JiaotuSettings 客户端运行参数（从 config 派生，便于单测注入 httptest 地址）。
type JiaotuSettings struct {
	APIBase             string
	WebOrigin           string
	Timeout             time.Duration
	HeaderTimeout       time.Duration
	ConnectTimeout      time.Duration
	MaxRefBytes         int64
	MaxResultBytes      int64
	ModelsCacheTTL      time.Duration
	MinPointsBuffer     int
	AutoAnswerQuestions bool
}

// withDefaults 补齐安全默认值（单测只填 APIBase 也能跑）。
func (s JiaotuSettings) withDefaults() JiaotuSettings {
	if strings.TrimSpace(s.APIBase) == "" {
		s.APIBase = "https://api.jiaotuai.cn"
	}
	if strings.TrimSpace(s.WebOrigin) == "" {
		s.WebOrigin = "https://jiaotu.top"
	}
	if s.Timeout <= 0 {
		s.Timeout = 600 * time.Second
	}
	if s.HeaderTimeout <= 0 {
		s.HeaderTimeout = 90 * time.Second
	}
	if s.ConnectTimeout <= 0 {
		s.ConnectTimeout = 15 * time.Second
	}
	if s.MaxRefBytes <= 0 {
		s.MaxRefBytes = int64(10) << 20
	}
	if s.MaxResultBytes <= 0 {
		s.MaxResultBytes = int64(32) << 20
	}
	if s.ModelsCacheTTL <= 0 {
		s.ModelsCacheTTL = 10 * time.Minute
	}
	return s
}

// JiaotuSettingsFromConfig 读取 jiaotu.* 配置并补齐安全默认值。
func JiaotuSettingsFromConfig(cfg *config.Config) JiaotuSettings {
	settings := JiaotuSettings{AutoAnswerQuestions: true}.withDefaults()
	if cfg == nil {
		return settings
	}
	section := cfg.Jiaotu
	if base := strings.TrimRight(strings.TrimSpace(section.APIBase), "/"); base != "" {
		settings.APIBase = base
	}
	if origin := strings.TrimRight(strings.TrimSpace(section.WebOrigin), "/"); origin != "" {
		settings.WebOrigin = origin
	}
	settings.Timeout = durationOrDefault(section.TimeoutSeconds, settings.Timeout)
	settings.HeaderTimeout = durationOrDefault(section.HeaderTimeoutSeconds, settings.HeaderTimeout)
	settings.ConnectTimeout = durationOrDefault(section.ConnectTimeoutSeconds, settings.ConnectTimeout)
	settings.ModelsCacheTTL = durationOrDefault(section.ModelsCacheTTLSeconds, settings.ModelsCacheTTL)
	if section.MaxReferenceMB > 0 {
		settings.MaxRefBytes = int64(section.MaxReferenceMB) << 20
	}
	if section.MaxResultMB > 0 {
		settings.MaxResultBytes = int64(section.MaxResultMB) << 20
	}
	if section.MinPointsBuffer > 0 {
		settings.MinPointsBuffer = section.MinPointsBuffer
	}
	settings.AutoAnswerQuestions = section.AutoAnswerQuestions
	return settings
}

func durationOrDefault(seconds int, fallback time.Duration) time.Duration {
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

// JiaotuClient 是无状态的椒图 HTTP 客户端（代理按需派生 transport）。
type JiaotuClient struct {
	settings JiaotuSettings
	http     *http.Client

	transportMu sync.Mutex
	transports  map[string]*http.Transport
}

// NewJiaotuClient 构造客户端。
func NewJiaotuClient(settings JiaotuSettings) *JiaotuClient {
	settings = settings.withDefaults()
	// 刻意不设 http.Client.Timeout：图片/视频为分钟级长任务，整体超时由 context 控制，
	// 而「卡死连接」由 ResponseHeaderTimeout 兜底（移植契约 R3）。
	return &JiaotuClient{
		settings:   settings,
		http:       &http.Client{Transport: newJiaotuTransport("", settings), Timeout: 0},
		transports: map[string]*http.Transport{},
	}
}

func newJiaotuTransport(proxyURL string, settings JiaotuSettings) *http.Transport {
	transport := &http.Transport{
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   settings.ConnectTimeout,
		ResponseHeaderTimeout: settings.HeaderTimeout,
		ForceAttemptHTTP2:     true,
		DialContext: (&net.Dialer{
			Timeout: settings.ConnectTimeout,
		}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if proxyURL = strings.TrimSpace(proxyURL); proxyURL != "" {
		if _, parsed, err := proxyurl.Parse(proxyURL); err == nil && parsed != nil {
			// 统一走 proxyutil：http/https 设 Transport.Proxy，socks5(h) 换 DialContext。
			_ = proxyutil.ConfigureTransportProxy(transport, parsed)
		}
	}
	return transport
}

// httpClientFor 返回复用直连或指定代理的 client。
func (c *JiaotuClient) httpClientFor(proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return c.http
	}
	c.transportMu.Lock()
	defer c.transportMu.Unlock()
	if transport, ok := c.transports[proxyURL]; ok {
		return &http.Client{Transport: transport}
	}
	transport := newJiaotuTransport(proxyURL, c.settings)
	c.transports[proxyURL] = transport
	return &http.Client{Transport: transport}
}

// Settings 暴露只读配置（供上层拼装错误信息/日志）。
func (c *JiaotuClient) Settings() JiaotuSettings { return c.settings }

// BaseURL 暴露上游域名（测试里指向 httptest.Server）。
func (c *JiaotuClient) BaseURL() string { return c.settings.APIBase }

func (c *JiaotuClient) newRequest(ctx context.Context, method, url, token, accept string, body []byte) (*http.Request, error) {
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	// 椒图会按浏览器指纹校验（部分部署把短信挑战绑定到该头组），必须完整带上。
	req.Header.Set("User-Agent", jiaotuUserAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Origin", c.settings.WebOrigin)
	req.Header.Set("Referer", c.settings.WebOrigin+"/")
	req.Header.Set("Sec-CH-UA", jiaotuSECHUA)
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// doJSON 发一个普通 JSON 请求，返回状态码与原始响应体。
func (c *JiaotuClient) doJSON(ctx context.Context, op, method, path, token string, body []byte, proxyURL string) (int, []byte, error) {
	req, err := c.newRequest(ctx, method, c.settings.APIBase+path, token, jiaotuAcceptJSON, body)
	if err != nil {
		return 0, nil, newJiaotuError(op, JiaotuErrNetwork, 0, "构造请求失败：%v", err)
	}
	resp, err := c.httpClientFor(proxyURL).Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 0, nil, newJiaotuError(op, JiaotuErrTimeout, 0, "请求超时：%v", err)
		}
		if errors.Is(err, context.Canceled) {
			return 0, nil, newJiaotuError(op, JiaotuErrNetwork, 0, "请求被取消：%v", err)
		}
		return 0, nil, newJiaotuError(op, JiaotuErrNetwork, 0, "网络错误：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, c.settings.MaxResultBytes+1))
	if readErr != nil {
		return resp.StatusCode, raw, newJiaotuError(op, JiaotuErrBadResponse, resp.StatusCode, "读取响应失败：%v", readErr)
	}
	if len(raw) > int(c.settings.MaxResultBytes) {
		return resp.StatusCode, raw, newJiaotuError(op, JiaotuErrBadResponse, resp.StatusCode, "响应超过 %d 字节上限", c.settings.MaxResultBytes)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return resp.StatusCode, raw, newJiaotuError(op, JiaotuErrAuth, resp.StatusCode, "账号登录已失效")
	}
	if resp.StatusCode >= 500 {
		return resp.StatusCode, raw, newJiaotuError(op, JiaotuErrUpstream5xx, resp.StatusCode, "上游异常：%s", jiaotuSnippet(raw))
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, raw, newJiaotuError(op, JiaotuErrUpstream4xx, resp.StatusCode, "上游拒绝：%s", jiaotuSnippet(raw))
	}
	return resp.StatusCode, raw, nil
}

// QueryModels 拉取上游模型清单（免费，无需 token）。
func (c *JiaotuClient) QueryModels(ctx context.Context, proxyURL string) (*JiaotuCatalog, error) {
	op := "queryModels"
	ctx, cancel := context.WithTimeout(ctx, c.settings.Timeout)
	defer cancel()
	_, raw, err := c.doJSON(ctx, op, http.MethodGet, jiaotuPathModels, "", nil, proxyURL)
	if err != nil {
		return nil, err
	}
	var root struct {
		Code int                   `json:"code"`
		Data []JiaotuUpstreamModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, newJiaotuError(op, JiaotuErrBadResponse, 0, "模型列表解析失败：%v", err)
	}
	if len(root.Data) == 0 {
		return nil, newJiaotuError(op, JiaotuErrBadResponse, 0, "模型列表为空")
	}
	return NewJiaotuCatalog(root.Data), nil
}

// RefreshPoints 查询账号剩余积分（E6）。返回 0 不代表一定失败，调用方按 ok 判断。
func (c *JiaotuClient) RefreshPoints(ctx context.Context, token, proxyURL string) (int, error) {
	op := "userBilling"
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, raw, err := c.doJSON(ctx, op, http.MethodGet, jiaotuPathBilling, token, nil, proxyURL)
	if err != nil {
		return 0, err
	}
	return jiaotuParseAvailablePoints(raw), nil
}

// SignIn 执行每日免费积分签到（E7）。返回 (是否成功, 上游文案)。
func (c *JiaotuClient) SignIn(ctx context.Context, token, proxyURL string) (bool, string, error) {
	op := "sign"
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, raw, err := c.doJSON(ctx, op, http.MethodGet, jiaotuPathSignIn, token, nil, proxyURL)
	if err != nil {
		return false, "", err
	}
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	message := jiaotuResponseMessage(root)
	code := jiaotuIntValue(root["code"])
	if strings.Contains(message, "已经签到") || strings.Contains(message, "已签到") || strings.Contains(message, "今日签到过") {
		return true, "已签到", nil
	}
	if code == 200 || code == 0 {
		return true, message, nil
	}
	if message == "" {
		message = "签到失败"
	}
	return false, message, nil
}

// JiaotuReference 一张待上传的参考图。
type JiaotuReference struct {
	Data        []byte
	ContentType string
}

// ValidateJiaotuReferences 入站参考图校验（对齐 kuikui parseReferences main.go:822-860）。
// 数量超限/格式不支持/体积越界一律本地 400，绝不消耗上游积分。
func (c *JiaotuClient) ValidateJiaotuReferences(refs []JiaotuReference, maxCount int) error {
	if maxCount < 0 {
		maxCount = 0
	}
	if len(refs) > maxCount {
		return newJiaotuError("references", JiaotuErrInvalidRequest, 0, "当前模型最多支持 %d 张参考图，当前收到 %d 张", maxCount, len(refs))
	}
	for i, ref := range refs {
		switch strings.ToLower(strings.TrimSpace(ref.ContentType)) {
		case "image/png", "image/jpeg", "image/jpg", "image/webp":
		default:
			return newJiaotuError("references", JiaotuErrInvalidRequest, 0, "第 %d 张参考图格式不支持，仅支持 PNG、JPG/JPEG、WebP", i+1)
		}
		if len(ref.Data) == 0 {
			return newJiaotuError("references", JiaotuErrInvalidRequest, 0, "第 %d 张参考图为空", i+1)
		}
		if int64(len(ref.Data)) >= c.settings.MaxRefBytes {
			return newJiaotuError("references", JiaotuErrInvalidRequest, 0, "第 %d 张参考图必须小于 %d MiB", i+1, c.settings.MaxRefBytes>>20)
		}
	}
	return nil
}

// UploadReference 三步上传：E3 取凭证 → E4 直传 → E5 确认，返回 ossId。
func (c *JiaotuClient) UploadReference(ctx context.Context, token, sessionID string, ref JiaotuReference, name string, proxyURL string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	extension := ".png"
	switch strings.ToLower(strings.TrimSpace(ref.ContentType)) {
	case "image/jpeg", "image/jpg":
		extension = ".jpg"
	case "image/webp":
		extension = ".webp"
	}
	fileName := fmt.Sprintf("%s-%s%s", name, jiaotuRandomHex(4), extension)

	payload, _ := json.Marshal(map[string]any{"sessionId": sessionID, "fileName": fileName})
	_, raw, err := c.doJSON(reqCtx, "upload/token", http.MethodPost, jiaotuPathUploadToken, token, payload, proxyURL)
	if err != nil {
		return "", err
	}
	var tokenPayload struct {
		Code int `json:"code"`
		Data struct {
			UploadURL     string `json:"uploadUrl"`
			CallbackToken string `json:"callbackToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &tokenPayload); err != nil {
		return "", newJiaotuError("upload/token", JiaotuErrBadResponse, 0, "上传凭证解析失败：%v", err)
	}
	uploadURL := strings.TrimSpace(tokenPayload.Data.UploadURL)
	callbackToken := strings.TrimSpace(tokenPayload.Data.CallbackToken)
	if uploadURL == "" || callbackToken == "" {
		return "", newJiaotuError("upload/token", JiaotuErrBadResponse, 0, "上游未返回参考图上传凭证")
	}

	putReq, err := http.NewRequestWithContext(reqCtx, http.MethodPut, uploadURL, bytes.NewReader(ref.Data))
	if err != nil {
		return "", newJiaotuError("upload/put", JiaotuErrNetwork, 0, "构造上传请求失败：%v", err)
	}
	contentType := strings.TrimSpace(ref.ContentType)
	if contentType == "" {
		contentType = "image/png"
	}
	putReq.Header.Set("Content-Type", contentType)
	putReq.ContentLength = int64(len(ref.Data))
	putResp, err := c.httpClientFor("").Do(putReq) // 直传走 OSS 预签名地址，不经上游代理
	if err != nil {
		return "", newJiaotuError("upload/put", JiaotuErrNetwork, 0, "参考图上传失败：%v", err)
	}
	_, _ = io.Copy(io.Discard, putResp.Body)
	_ = putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated && putResp.StatusCode != http.StatusNoContent {
		return "", newJiaotuError("upload/put", JiaotuErrUpstream5xx, putResp.StatusCode, "参考图文件上传失败")
	}

	callbackBody, _ := json.Marshal(map[string]any{"callbackToken": callbackToken, "fileName": fileName})
	_, notifyRaw, err := c.doJSON(reqCtx, "upload/callback", http.MethodPost, jiaotuPathUploadNotify, token, callbackBody, proxyURL)
	if err != nil {
		return "", err
	}
	var callback struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(notifyRaw, &callback); err != nil {
		return "", newJiaotuError("upload/callback", JiaotuErrBadResponse, 0, "上传确认解析失败：%v", err)
	}
	if callback.Code != 0 && callback.Code != 200 {
		message := jiaotuFirstNonEmpty(callback.Message, callback.Msg)
		if message == "" {
			return "", newJiaotuError("upload/callback", JiaotuErrBadResponse, 0, "上游 code %d", callback.Code)
		}
		// 上传确认失败通常是账号侧状态问题（例如积分/权限），交给换号逻辑处理。
		return "", newJiaotuError("upload/callback", JiaotuErrUpstream5xx, 0, "上游 code %d：%s", callback.Code, message)
	}
	var dataFields map[string]any
	_ = json.Unmarshal(callback.Data, &dataFields)
	ossID := jiaotuFirstNonEmpty(
		jiaotuStringValue(dataFields["ossId"]),
		jiaotuStringValue(dataFields["ossID"]),
		jiaotuStringValue(dataFields["oss_id"]),
	)
	if ossID == "" || ossID == "<nil>" {
		return "", newJiaotuError("upload/callback", JiaotuErrBadResponse, 0, "上游上传确认未返回 ossId")
	}
	return ossID, nil
}

// JiaotuStreamEvent imageChat 的一条 SSE 事件。
type JiaotuStreamEvent map[string]any

// JiaotuStreamResult SSE 收集结果。
type JiaotuStreamResult struct {
	Events    []JiaotuStreamEvent
	ImageURLs []string
	VideoURLs []string
	OSSIDs    []string
	TaskID    string
	TaskMode  string
}

// jiaotuContentTagPattern 匹配 <jiaotu-xxx> / </jiaotu-xxx> 正文包裹标签。
var jiaotuContentTagPattern = regexp.MustCompile(`(?s)</?jiaotu-[a-z0-9_-]*>`)

// unwrapJiaotuContent 剥掉上游正文包裹并压平空白。
// 实测 text 事件形如 <jiaotu-content>哎呀…</jiaotu-content>；不剥就会混进 URL（契约 R9）。
func unwrapJiaotuContent(value string) string {
	if value == "" {
		return ""
	}
	if !strings.Contains(value, "<jiaotu-") {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(jiaotuContentTagPattern.ReplaceAllString(value, ""))
}

// jiaotuUpstreamErrorTagPattern 匹配上游业务错误包裹 <jiaotu-error>…</jiaotu-error>。
// 与正常的 <jiaotu-content> 正文包裹语义不同：出现它代表本次生成未执行。
var jiaotuUpstreamErrorTagPattern = regexp.MustCompile(`(?is)<jiaotu-error>(.*?)</jiaotu-error>`)

// UpstreamErrorMessage 提取上游在 text 事件里回的业务错误正文。
// 上游会把「模型维护中」这类拒绝当普通对话文本发出来（type=text，无 error 事件），
// 不主动识别就只能退化成「流结束但无产物」，客户端看到的是通用 502。
//
// 标签会被上游拆到多个 text 事件里（实测首个事件 content 只有 `<jiaotu-error>`，
// 后续事件才带正文），所以必须先按事件顺序拼接再匹配，逐事件匹配会漏。
func (r *JiaotuStreamResult) UpstreamErrorMessage() string {
	if r == nil {
		return ""
	}
	var builder strings.Builder
	for _, event := range r.Events {
		if !strings.EqualFold(event.string("type"), "text") {
			continue
		}
		builder.WriteString(event.string("content"))
	}
	if builder.Len() == 0 {
		return ""
	}
	if match := jiaotuUpstreamErrorTagPattern.FindStringSubmatch(builder.String()); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

// JiaotuQuestion imageChat 反问事件里的单个问题（实测 content 为该结构的 JSON 字符串）。
type JiaotuQuestion struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

// ClarificationQuestions 解析 type=="questions" 事件。
// 上游在提示词被判定不明确时不回图、改为反问；无论后续走「自动应答」还是
// 「把问题透传给客户端」，都得先能稳定读出这些结构化问题。
func (r *JiaotuStreamResult) ClarificationQuestions() []JiaotuQuestion {
	if r == nil {
		return nil
	}
	var out []JiaotuQuestion
	for _, event := range r.Events {
		if !strings.EqualFold(event.string("type"), "questions") {
			continue
		}
		raw := unwrapJiaotuContent(event.string("content"))
		if raw == "" {
			continue
		}
		var questions []JiaotuQuestion
		if err := json.Unmarshal([]byte(raw), &questions); err != nil {
			continue
		}
		for _, question := range questions {
			if strings.TrimSpace(question.Question) != "" {
				out = append(out, question)
			}
		}
	}
	return out
}

// NeedsClarification 判断上游是否「没回图而是在反问」。
// 实测（契约 R8）：提示词被判不明确时，imageChat 回 text×N + questions×1 + end 就结束。
func (r *JiaotuStreamResult) NeedsClarification() bool {
	if r == nil || len(r.ImageURLs) > 0 || len(r.VideoURLs) > 0 {
		return false
	}
	return len(r.ClarificationQuestions()) > 0
}

// TextContent 拼接所有 text 事件正文（已剥包），用于把上游说明透传给调用方/日志。
func (r *JiaotuStreamResult) TextContent(limit int) string {
	if r == nil {
		return ""
	}
	parts := make([]string, 0, len(r.Events))
	total := 0
	for _, event := range r.Events {
		if !strings.EqualFold(event.string("type"), "text") {
			continue
		}
		chunk := unwrapJiaotuContent(event.string("content"))
		if chunk == "" {
			continue
		}
		parts = append(parts, chunk)
		total += len(chunk)
		if limit > 0 && total >= limit {
			break
		}
	}
	joined := strings.Join(parts, "")
	if limit > 0 && len(joined) > limit {
		joined = joined[:limit]
	}
	return joined
}

// ImageChat 调用 E2 并收集 SSE 事件。
func (c *JiaotuClient) ImageChat(ctx context.Context, token string, body map[string]any, proxyURL string) (*JiaotuStreamResult, error) {
	op := "imageChat"
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, newJiaotuError(op, JiaotuErrInvalidRequest, 0, "请求体序列化失败：%v", err)
	}
	reqCtx, cancel := context.WithTimeout(ctx, c.settings.Timeout)
	defer cancel()

	// imageChat 的头集合必须与浏览器实际发出的一致（对齐 kuikui stream() main.go:972-983）：
	// 只带 Accept/Content-Type/Origin/Referer/UA/Authorization。多发 Sec-CH-UA*/Sec-Fetch-*
	// 与 JSON 接口指纹不一致，会被上游当作异常客户端（实测全部 502）。
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.settings.APIBase+jiaotuPathImageChat, strings.NewReader(string(payload)))
	if err != nil {
		return nil, newJiaotuError(op, JiaotuErrNetwork, 0, "构造请求失败：%v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.settings.WebOrigin)
	req.Header.Set("Referer", c.settings.WebOrigin+"/")
	req.Header.Set("User-Agent", jiaotuStreamUserAgent)
	req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	resp, err := c.httpClientFor(proxyURL).Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, newJiaotuError(op, JiaotuErrTimeout, 0, "生成超时")
		}
		return nil, newJiaotuError(op, JiaotuErrNetwork, 0, "网络错误：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, newJiaotuError(op, JiaotuErrAuth, resp.StatusCode, "账号登录已失效")
	}
	if resp.StatusCode >= 500 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, newJiaotuError(op, JiaotuErrUpstream5xx, resp.StatusCode, "%s", jiaotuSnippet(raw))
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, newJiaotuError(op, JiaotuErrUpstream4xx, resp.StatusCode, "%s", jiaotuSnippet(raw))
	}

	result := &JiaotuStreamResult{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), jiaotuSSELineMaxBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var event JiaotuStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue // 上游会混发心跳/非 JSON 片段，忽略即可
		}
		result.Events = append(result.Events, event)
		if value := event.string("taskId"); value != "" {
			result.TaskID = value
		}
		if value := event.string("taskMode"); value != "" {
			result.TaskMode = value
		}
		switch strings.ToLower(event.string("type")) {
		case "image":
			// kuikui 取 '#' 之前部分（main.go:1268-1275），但先剥掉上游正文包裹（R9）。
			url := strings.TrimSpace(strings.Split(unwrapJiaotuContent(event.string("content")), "#")[0])
			if url != "" {
				result.ImageURLs = append(result.ImageURLs, url)
			}
		case "video":
			if url := strings.TrimSpace(unwrapJiaotuContent(event.string("content"))); url != "" {
				result.VideoURLs = append(result.VideoURLs, url)
			}
		}
		// 上游也用 ossId 直接引用产物；只有 ossId 时拼不出可访问 URL，
		// 但保留下来供日志排查（见 jiaotuDescribeEvents）。
		if value := event.string("ossId"); value != "" {
			result.OSSIDs = append(result.OSSIDs, value)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return result, newJiaotuError(op, JiaotuErrTimeout, 0, "生成超时：%v", err)
		}
		return result, newJiaotuError(op, JiaotuErrNetwork, 0, "流读取中断：%v", err)
	}
	if failure := result.Failure(); failure != nil {
		return result, failure
	}
	return result, nil
}

// Failure 从事件流里提取业务失败（insufficient_points / error / failed / ...）。
func (r *JiaotuStreamResult) Failure() error {
	if r == nil {
		return nil
	}
	for _, event := range r.Events {
		switch strings.ToLower(event.string("type")) {
		case "insufficient_points":
			return newJiaotuError("imageChat", JiaotuErrInsufficientPoints, 0, "椒图账号积分不足")
		case "error", "failed", "failure", "task_error":
			message := jiaotuFirstNonEmpty(unwrapJiaotuContent(event.string("message")), unwrapJiaotuContent(event.string("msg")), unwrapJiaotuContent(event.string("content")), "椒图生成失败")
			return newJiaotuError("imageChat", JiaotuErrUpstream5xx, 0, "%s", message)
		}
	}
	return nil
}

func (e JiaotuStreamEvent) string(key string) string {
	if value, ok := e[key].(string); ok {
		return value
	}
	return ""
}

// JiaotuGenerateRequest 一次图片/视频生成的内部请求。
type JiaotuGenerateRequest struct {
	SessionID  string
	Prompt     string
	ModelID    int
	Count      int
	Size       string // 比例，如 1:1 / 16:9 / 9:16
	References []JiaotuReference
	// 视频参数
	Duration          int
	QualityLevel      string
	Audio             bool
	ReferenceVideoURL string
	ShotType          string
	PromptExtend      *bool
	// UploadedOSSIDs 由 UploadReferences 回填，作为 imageChat 的 imagesIds。
	UploadedOSSIDs []string
}

// ChatBody 组装 imageChat 请求体（视频字段仅在需要时出现，避免上游按多余字段判错）。
func (r JiaotuGenerateRequest) ChatBody(video bool) map[string]any {
	count := r.Count
	if count < 1 {
		count = 1
	}
	body := map[string]any{
		"sessionId":   r.SessionID,
		"requestMode": "standard",
		"prompt":      r.Prompt,
		"imagesIds":   append([]string{}, r.UploadedOSSIDs...),
		"modelId":     r.ModelID,
		"imageCount":  count,
		"size":        r.Size,
	}
	if video {
		body["imageCount"] = 1
		body["duration"] = strconv.Itoa(r.Duration)
		body["qualityLevel"] = r.QualityLevel
		body["audio"] = r.Audio
		if value := strings.TrimSpace(r.ReferenceVideoURL); value != "" {
			body["referenceVideoUrl"] = value
		}
		if value := strings.TrimSpace(r.ShotType); value != "" {
			body["shotType"] = value
		}
		if r.PromptExtend != nil {
			body["promptExtend"] = *r.PromptExtend
		}
	}
	return body
}

// UploadReferences 顺序上传参考图并返回 ossId 列表。任何一张失败即中止（换号重试由上层决定）。
func (c *JiaotuClient) UploadReferences(ctx context.Context, token string, req *JiaotuGenerateRequest, proxyURL string) ([]string, error) {
	ids := make([]string, 0, len(req.References))
	for i, ref := range req.References {
		ossID, err := c.UploadReference(ctx, token, req.SessionID, ref, fmt.Sprintf("reference-%d", i+1), proxyURL)
		if err != nil {
			return ids, err
		}
		ids = append(ids, ossID)
	}
	return ids, nil
}

// DecodeJiaotuDataURL 解析 dataURL → 参考图（容忍大小写与缺省 MIME）。
func DecodeJiaotuDataURL(value string, maxBytes int64) (JiaotuReference, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return JiaotuReference{}, fmt.Errorf("图片必须是 base64 dataURL")
	}
	parts := strings.SplitN(trimmed, ",", 2)
	if len(parts) != 2 {
		return JiaotuReference{}, fmt.Errorf("dataURL 缺少逗号分隔")
	}
	meta := strings.TrimPrefix(strings.ToLower(parts[0]), "data:")
	contentType := meta
	if semi := strings.Index(meta, ";"); semi >= 0 {
		contentType = meta[:semi]
	}
	raw, err := jiaotuDecodeBase64(parts[1])
	if err != nil {
		return JiaotuReference{}, fmt.Errorf("dataURL base64 解码失败：%v", err)
	}
	if len(raw) == 0 {
		return JiaotuReference{}, fmt.Errorf("dataURL 内容为空")
	}
	if maxBytes > 0 && int64(len(raw)) >= maxBytes {
		return JiaotuReference{}, fmt.Errorf("dataURL 超过 %d 字节上限", maxBytes)
	}
	// 声明的 MIME 不可信：image/jpg 归一为 image/jpeg，缺失或非法时按实际字节嗅探。
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	case "image/jpg":
		contentType = "image/jpeg"
	default:
		contentType = detectImageContentType(raw)
	}
	return JiaotuReference{Data: raw, ContentType: contentType}, nil
}

// jiaotuDecodeBase64 容忍 padding 缺失与 URL-safe 字母表（上游导出偶见）。
func jiaotuDecodeBase64(value string) ([]byte, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	candidates := []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding}
	if strings.ContainsAny(cleaned, "-_") {
		candidates = append(candidates, base64.URLEncoding, base64.RawURLEncoding)
	}
	var lastErr error
	for _, encoding := range candidates {
		raw, err := encoding.DecodeString(cleaned)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// FetchResultBytes 下载上游产物（图片/视频 URL 或 dataURL），受 MaxResultBytes 限制。
// 仅用于把椒图产出的临时 CDN 链接转成 b64_json 或自有存储。
func (c *JiaotuClient) FetchResultBytes(ctx context.Context, rawURL, proxyURL string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, "", newJiaotuError("download", JiaotuErrBadResponse, 0, "结果为空 URL")
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		ref, err := DecodeJiaotuDataURL(trimmed, c.settings.MaxResultBytes)
		if err != nil {
			return nil, "", newJiaotuError("download", JiaotuErrBadResponse, 0, "%v", err)
		}
		return ref.Data, ref.ContentType, nil
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return nil, "", newJiaotuError("download", JiaotuErrBadResponse, 0, "不支持的结果地址协议")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, trimmed, nil)
	if err != nil {
		return nil, "", newJiaotuError("download", JiaotuErrNetwork, 0, "构造下载请求失败：%v", err)
	}
	resp, err := c.httpClientFor(proxyURL).Do(req)
	if err != nil {
		return nil, "", newJiaotuError("download", JiaotuErrNetwork, 0, "下载失败：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, "", newJiaotuError("download", JiaotuErrUpstream5xx, resp.StatusCode, "下载结果 HTTP %d", resp.StatusCode)
	}
	limit := c.settings.MaxResultBytes
	if limit <= 0 {
		limit = int64(32) << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", newJiaotuError("download", JiaotuErrNetwork, 0, "读取结果失败：%v", err)
	}
	if int64(len(data)) > limit {
		return nil, "", newJiaotuError("download", JiaotuErrBadResponse, 0, "结果超过 %d 字节上限", limit)
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = detectImageContentType(data)
	}
	return data, contentType, nil
}

func jiaotuParseAvailablePoints(raw []byte) int {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return 0
	}
	pick := func(container any) int {
		m, ok := container.(map[string]any)
		if !ok {
			return 0
		}
		return jiaotuIntValue(m["availableCount"])
	}
	if rows, ok := root["rows"].([]any); ok && len(rows) > 0 {
		if n := pick(rows[0]); n > 0 {
			return n
		}
	}
	if data, ok := root["data"].(map[string]any); ok {
		if rows, ok := data["records"].([]any); ok && len(rows) > 0 {
			if n := pick(rows[0]); n > 0 {
				return n
			}
		}
		if rows, ok := data["rows"].([]any); ok && len(rows) > 0 {
			if n := pick(rows[0]); n > 0 {
				return n
			}
		}
		if n := pick(data); n > 0 {
			return n
		}
	}
	return 0
}

func jiaotuResponseMessage(root map[string]any) string {
	if root == nil {
		return ""
	}
	return jiaotuFirstNonEmpty(jiaotuStringValue(root["message"]), jiaotuStringValue(root["msg"]))
}

func jiaotuFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jiaotuSnippet(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "(空响应)"
	}
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}

func jiaotuRandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return fmt.Sprintf("%x", buf)
}

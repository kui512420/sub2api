//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// jiaotuTestClient 指向 httptest 服务端的椒图客户端。
func jiaotuTestClient(t *testing.T, base string, timeout time.Duration) *JiaotuClient {
	t.Helper()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return NewJiaotuClient(JiaotuSettings{
		APIBase:        base,
		WebOrigin:      "https://jiaotu.test",
		Timeout:        timeout,
		HeaderTimeout:  timeout,
		ConnectTimeout: 2 * time.Second,
		MaxRefBytes:    1 << 20,
		MaxResultBytes: 4 << 20,
		ModelsCacheTTL: time.Minute,
	})
}

func writeJiaotuSSE(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, frame := range frames {
		_, _ = fmt.Fprintf(w, "data:%s\n\n", frame)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	_, _ = io.WriteString(w, "data:[DONE]\n\n")
}

func TestJiaotuClientSendsBrowserFingerprintHeaders(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeJiaotuSSE(w, `{"type":"image","content":"https://cdn.test/a.png#frag"}`)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.ImageChat(context.Background(), "tok-1", map[string]any{"prompt": "hi"}, "")
	require.NoError(t, err)

	// imageChat 只发浏览器实际发的那几个头（对齐 kuikui stream() main.go:972-983），
	// 多发 Sec-CH-UA*/Sec-Fetch-* 会被上游当成异常客户端。
	require.Equal(t, jiaotuStreamUserAgent, captured.Get("User-Agent"))
	require.Equal(t, "https://jiaotu.test", captured.Get("Origin"))
	require.Equal(t, "https://jiaotu.test/", captured.Get("Referer"))
	require.Equal(t, "text/event-stream", captured.Get("Accept"))
	require.Equal(t, "application/json", captured.Get("Content-Type"))
	require.Equal(t, "Bearer tok-1", captured.Get("Authorization"), "已带 Bearer 前缀的 token 不得重复")
	require.Empty(t, captured.Get("Sec-CH-UA"))
	require.Empty(t, captured.Get("Sec-Fetch-Mode"))
	require.Empty(t, captured.Get("Sec-Fetch-Site"))
	require.Empty(t, captured.Get("Accept-Language"))
}

func TestJiaotuClientJSONEndpointsCarryFullFingerprint(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []JiaotuUpstreamModel{
			{ID: 18, ShowModelName: "全能图片 V2", ModelType: 0},
		}})
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.QueryModels(context.Background(), "")
	require.NoError(t, err)

	require.Equal(t, jiaotuUserAgent, captured.Get("User-Agent"))
	require.Equal(t, jiaotuSECHUA, captured.Get("Sec-CH-UA"))
	require.Equal(t, "?0", captured.Get("Sec-CH-UA-Mobile"))
	require.Equal(t, `"Windows"`, captured.Get("Sec-CH-UA-Platform"))
	require.Equal(t, "cors", captured.Get("Sec-Fetch-Mode"))
	require.Equal(t, "cross-site", captured.Get("Sec-Fetch-Site"))
	require.Equal(t, "empty", captured.Get("Sec-Fetch-Dest"))
	require.Equal(t, "zh-CN,zh;q=0.9,en;q=0.8", captured.Get("Accept-Language"))
	require.Equal(t, "https://jiaotu.test/", captured.Get("Referer"))
	require.Empty(t, captured.Get("Authorization"), "免费接口不带 token")
}

func TestJiaotuClientImageChatSuccessStripsURLFragment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, jiaotuPathImageChat, r.URL.Path)
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &body))
		require.Equal(t, "standard", body["requestMode"])
		require.Equal(t, float64(18), body["modelId"])
		require.Equal(t, "1:1", body["size"])
		require.Equal(t, "画一只猫", body["prompt"])
		writeJiaotuSSE(w,
			`: heartbeat`, // 注释行必须被忽略
			`{"type":"progress","content":"生成中"}`,
			`{"type":"image","content":"https://cdn.test/a.png?sig=1#fragment"}`,
			`{"type":"image","content":"https://cdn.test/b.png"}`,
			`{"taskId":"task-9","taskMode":"video"}`,
			`not-json-at-all`,
		)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	request := JiaotuGenerateRequest{SessionID: "sess", Prompt: "画一只猫", ModelID: 18, Count: 1, Size: "1:1"}
	result, err := client.ImageChat(context.Background(), "tok", request.ChatBody(false), "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://cdn.test/a.png?sig=1", "https://cdn.test/b.png"}, result.ImageURLs)
	require.Equal(t, "task-9", result.TaskID)
	require.Equal(t, "video", result.TaskMode)
	require.Nil(t, result.Failure())
}

func TestJiaotuClientImageChatInsufficientPoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJiaotuSSE(w, `{"type":"insufficient_points","message":"椒图账号积分不足"}`)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	require.Error(t, err)

	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrInsufficientPoints, jerr.Kind)
	require.Contains(t, jerr.Message, "积分不足")
	require.True(t, jerr.RetryableWithNextAccount(), "积分不足必须换号")
	require.True(t, jerr.ExhaustsAccount(), "积分不足要把该号踢出候选")
	require.Equal(t, http.StatusServiceUnavailable, jerr.ClientStatus())
}

func TestJiaotuClientImageChatHTTP500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "boom")
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrUpstream5xx, jerr.Kind)
	require.Equal(t, 500, jerr.StatusCode)
	require.True(t, jerr.RetryableWithNextAccount())
	require.False(t, jerr.ExhaustsAccount(), "上游 500 不该直接判账号死刑")

	// 换号语义必须翻译成 handler 能识别的 failover 错误
	failover := jiaotuFailoverOrClientError(err)
	var upstreamErr *UpstreamFailoverError
	require.ErrorAs(t, failover, &upstreamErr)
	require.Equal(t, 500, upstreamErr.StatusCode)
	require.False(t, upstreamErr.RetryableOnSameAccount)
}

func TestJiaotuClientImageChatSSEErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJiaotuSSE(w, `{"type":"error","message":"模型繁忙"}`)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrUpstream5xx, jerr.Kind)
	require.Contains(t, jerr.Error(), "模型繁忙")
}

func TestJiaotuClientImageChatTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(1200 * time.Millisecond):
			writeJiaotuSSE(w, `{"type":"image","content":"https://cdn.test/late.png"}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 120*time.Millisecond)
	_, err := client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	require.Error(t, err)
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Contains(t, []JiaotuErrorKind{JiaotuErrTimeout, JiaotuErrNetwork}, jerr.Kind)
	require.True(t, jerr.RetryableWithNextAccount(), "超时应换号重试")
}

func TestJiaotuClientAuthExpiryMarksAccount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)

	_, err := client.RefreshPoints(context.Background(), "tok", "")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrAuth, jerr.Kind)
	require.True(t, jerr.ExhaustsAccount())

	_, err = client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	jerr, ok = IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrAuth, jerr.Kind)
}

func TestJiaotuClientUploadReferenceFlow(t *testing.T) {
	var putBody string
	var putContentType string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/upload/token":
			var req map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
			require.Equal(t, "sess-1", req["sessionId"])
			require.True(t, strings.HasPrefix(req["fileName"].(string), "reference-1-"))
			require.True(t, strings.HasSuffix(req["fileName"].(string), ".jpg"))
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"uploadUrl":     server.URL + "/oss",
				"callbackToken": "cb-1",
			}})
		case "/oss":
			require.Equal(t, http.MethodPut, r.Method)
			raw, _ := io.ReadAll(r.Body)
			putBody = string(raw)
			putContentType = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case "/api/v1/upload/callback":
			var req map[string]any
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
			require.Equal(t, "cb-1", req["callbackToken"])
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"ossId": "oss-777"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	ossID, err := client.UploadReference(context.Background(), "tok", "sess-1",
		JiaotuReference{Data: []byte("fake-jpeg-bytes"), ContentType: "image/jpeg"}, "reference-1", "")
	require.NoError(t, err)
	require.Equal(t, "oss-777", ossID)
	require.Equal(t, "fake-jpeg-bytes", putBody)
	require.Equal(t, "image/jpeg", putContentType)
}

func TestJiaotuClientUploadReferenceRejectsMissingOSSID(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/upload/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"uploadUrl": server.URL + "/oss", "callbackToken": "cb",
			}})
		case "/oss":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/upload/callback":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"ossId": "<nil>"}})
		}
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	_, err := client.UploadReference(context.Background(), "tok", "sess", JiaotuReference{Data: []byte("x"), ContentType: "image/png"}, "r", "")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrBadResponse, jerr.Kind)
	require.True(t, jerr.RetryableWithNextAccount())
}

func TestJiaotuClientQueryModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, jiaotuPathModels, r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"), "模型清单是免费接口，不带 token")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []JiaotuUpstreamModel{
			{ID: 18, ShowModelName: "全能图片 V2", ModelType: 0, ImageSize: 6, CostRadish: 4,
				ResolutionList:        []JiaotuModelOption{{Value: "1:1"}},
				QualityLevelList:      []JiaotuModelOption{{Value: "768P"}},
				DurationList:          []JiaotuModelOption{{Value: "5"}, {Value: "10"}},
				SupportFirstLastFrame: "1",
				ResolutionFeeConfig:   []JiaotuModelFee{{Value: "768P", Fee: 8}},
			},
			{ID: 25, ShowModelName: "MiniMax H3", ModelType: 1, ImageSize: 9, CostRadish: 30},
			{ID: 99, ShowModelName: "上游新图片模型", ModelType: 0, ImageSize: 4},
		}})
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	catalog, err := client.QueryModels(context.Background(), "")
	require.NoError(t, err)

	upstream, spec, found := catalog.Resolve("jiaotu-image-v2")
	require.True(t, found)
	require.Equal(t, 18, spec.ProviderID)
	require.Equal(t, 6, upstream.MaxReferenceImages())
	require.Equal(t, 4, upstream.PointsCost())
	require.Equal(t, 8, upstream.PointsCostForQuality("768P"), "视频/带画质模型应按 resolutionFeeConfig 计费")
	require.True(t, upstream.SupportsFirstLastFrame())
	require.Equal(t, []string{"5", "10"}, upstream.Options(upstream.DurationList))

	// 上游新增模型退化为 img-<id>，不阻塞新模型可用
	unknown, _, ok := catalog.Resolve("img-99")
	require.True(t, ok)
	require.Equal(t, 99, unknown.ID)

	entries := catalog.OpenAIModelEntries()
	byID := map[string]JiaotuCatalogEntry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	require.Equal(t, "jiaotu", byID["jiaotu-image-v2"].Provider)
	require.Equal(t, "image", byID["jiaotu-image-v2"].Capability)
	require.Equal(t, "openai-images", byID["jiaotu-image-v2"].CompatibilityProtocol)
	require.Equal(t, 18, byID["jiaotu-image-v2"].ProviderModelID)
	require.Contains(t, byID["jiaotu-minimax-h3"].Aliases, "vid-25", "视频模型保留 vid-<id> 别名")
	require.Equal(t, "video", byID["jiaotu-minimax-h3"].Capability)
	require.NotContains(t, byID, "gpt-image-1", "gpt-image-1 属 OpenAI 命名空间，椒图目录不得劫持")
}

// TestJiaotuCatalogCacheReusesUpstream 验证模型清单按 TTL 缓存，且 force 可以绕过。
func TestJiaotuCatalogCacheReusesUpstream(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []JiaotuUpstreamModel{
			{ID: 18, ShowModelName: "全能图片 V2", ModelType: 0, ImageSize: 6, CostRadish: 4},
		}})
	}))
	defer server.Close()

	svc := &OpenAIGatewayService{cfg: &config.Config{Jiaotu: config.JiaotuConfig{
		APIBase:               server.URL,
		ModelsCacheTTLSeconds: 600,
	}}}
	ctx := context.Background()

	first, err := svc.jiaotuCatalogFor(ctx, svc.jiaotuClient(), "", false)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 1, hits)

	_, err = svc.jiaotuCatalogFor(ctx, svc.jiaotuClient(), "", false)
	require.NoError(t, err)
	require.Equal(t, 1, hits, "TTL 内不得重复打下游")

	_, err = svc.jiaotuCatalogFor(ctx, svc.jiaotuClient(), "", true)
	require.NoError(t, err)
	require.Equal(t, 2, hits, "force 必须刷新")
}

func TestJiaotuGenerateRequestChatBody(t *testing.T) {
	extend := true
	req := JiaotuGenerateRequest{
		SessionID: "sess", Prompt: "p", ModelID: 18, Count: 2, Size: "16:9",
		UploadedOSSIDs: []string{"oss-1", "oss-2"},
	}
	imageBody := req.ChatBody(false)
	require.Equal(t, "sess", imageBody["sessionId"])
	require.Equal(t, "standard", imageBody["requestMode"])
	require.Equal(t, 18, imageBody["modelId"])
	require.Equal(t, 2, imageBody["imageCount"])
	require.Equal(t, "16:9", imageBody["size"])
	require.Equal(t, []string{"oss-1", "oss-2"}, imageBody["imagesIds"])
	require.NotContains(t, imageBody, "duration", "图片请求不得带视频字段")

	video := req
	video.Duration = 5
	video.QualityLevel = "768P"
	video.Audio = true
	video.ReferenceVideoURL = "https://cdn.test/ref.mp4"
	video.ShotType = "single"
	video.PromptExtend = &extend
	videoBody := video.ChatBody(true)
	require.Equal(t, "5", videoBody["duration"], "上游要求 duration 为字符串")
	require.Equal(t, "768P", videoBody["qualityLevel"])
	require.Equal(t, true, videoBody["audio"])
	require.Equal(t, 1, videoBody["imageCount"], "视频固定 1 帧")
	require.Equal(t, "https://cdn.test/ref.mp4", videoBody["referenceVideoUrl"])
	require.Equal(t, "single", videoBody["shotType"])
	require.Equal(t, true, videoBody["promptExtend"])

	minimal := JiaotuGenerateRequest{SessionID: "s", Prompt: "p", ModelID: 25, Size: "1:1"}
	body := minimal.ChatBody(true)
	require.Equal(t, 1, body["imageCount"], "n<1 自动抬到 1")
	require.NotContains(t, body, "shotType", "可选字段为空时不得出现")
	require.NotContains(t, body, "promptExtend")
	require.Equal(t, []string{}, body["imagesIds"])
}

func jiaotuTestCatalog() *JiaotuCatalog {
	return NewJiaotuCatalog([]JiaotuUpstreamModel{
		{ID: 18, ShowModelName: "全能图片 V2", ModelType: 0, ImageSize: 2, CostRadish: 4},
		{ID: 22, ShowModelName: "Seedream 5.0 Pro", ModelType: 0, ImageSize: 6, CostRadish: 7},
		{ID: 25, ShowModelName: "MiniMax H3", ModelType: 1, ImageSize: 9, CostRadish: 30},
	})
}

func jiaotuBaseRequest() *OpenAIImagesRequest {
	return &OpenAIImagesRequest{Model: "jiaotu-image-v2", Prompt: "画一只猫", N: 1, Size: "1024x1024"}
}

func TestNormalizeJiaotuImagesRequestValid(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	req, err := svc.NormalizeJiaotuImagesRequest(jiaotuBaseRequest(), jiaotuTestCatalog())
	require.NoError(t, err)
	require.Equal(t, "1:1", req.Ratio)
	require.Equal(t, 1, req.Count)
	require.Equal(t, 18, req.Model.ProviderID)
	require.NotNil(t, req.Upstream)
	require.Equal(t, 4, req.Upstream.PointsCost())
	require.Equal(t, 4, jiaotuMinPointsForImage(req, 0), "minPoints = cost × n")

	for _, pair := range []struct{ openai, ratio string }{
		{"1024x1024", "1:1"}, {"1024x1536", "9:16"}, {"1536x1024", "16:9"},
		{"9:16", "9:16"}, {"auto", ""}, {"", "1:1"},
	} {
		parsed := jiaotuBaseRequest()
		parsed.Size = pair.openai
		normalized, err := svc.NormalizeJiaotuImagesRequest(parsed, jiaotuTestCatalog())
		require.NoError(t, err, pair.openai)
		require.Equal(t, pair.ratio, normalized.Ratio)
	}

	// n 钳制到 1..4（kuikui main.go:1216-1221）
	parsed := jiaotuBaseRequest()
	parsed.N = 9
	normalized, err := svc.NormalizeJiaotuImagesRequest(parsed, jiaotuTestCatalog())
	require.NoError(t, err)
	require.Equal(t, 4, normalized.Count)
	require.Equal(t, 16, jiaotuMinPointsForImage(normalized, 0))

	// 多语言/别名入站都要能解析
	for _, alias := range []string{"jiaotu-seedream-5-pro", "img-22", "22", "Seedream 5.0 Pro"} {
		parsed := jiaotuBaseRequest()
		parsed.Model = alias
		got, err := svc.NormalizeJiaotuImagesRequest(parsed, jiaotuTestCatalog())
		require.NoError(t, err, alias)
		require.Equal(t, 22, got.Model.ProviderID, alias)
	}
}

func TestNormalizeJiaotuImagesRequestRejectsBeforeUpstream(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	assertInvalid := func(parsed *OpenAIImagesRequest, wantContains string) {
		t.Helper()
		_, err := svc.NormalizeJiaotuImagesRequest(parsed, jiaotuTestCatalog())
		require.Error(t, err)
		jerr, ok := IsJiaotuError(err)
		require.True(t, ok, "必须是可分类的椒图错误")
		require.Equal(t, JiaotuErrInvalidRequest, jerr.Kind)
		require.Contains(t, jerr.Message, wantContains)
		// 参数类错误不得转成 failover：否则会给好号白白打上失败标记
		_, isFailover := jiaotuFailoverOrClientError(err).(*UpstreamFailoverError)
		require.False(t, isFailover)
	}

	missingPrompt := jiaotuBaseRequest()
	missingPrompt.Prompt = "   "
	assertInvalid(missingPrompt, "prompt")

	unknown := jiaotuBaseRequest()
	unknown.Model = "jiaotu-does-not-exist"
	assertInvalid(unknown, "未知椒图模型")

	// 上游清单里存在但未登记稳定表的视频模型，不得当作图片模型
	video := jiaotuBaseRequest()
	video.Model = "vid-25"
	assertInvalid(video, "/v1/videos")

	badSize := jiaotuBaseRequest()
	badSize.Size = "123x456"
	assertInvalid(badSize, "不支持的 size")

	// 参考图数量按上游模型上限（本目录 ImageSize=2）
	overCount := jiaotuBaseRequest()
	overCount.Uploads = []OpenAIImagesUpload{
		{Data: []byte("a"), ContentType: "image/png"},
		{Data: []byte("b"), ContentType: "image/png"},
		{Data: []byte("c"), ContentType: "image/png"},
	}
	assertInvalid(overCount, "最多支持 2 张")

	badFormat := jiaotuBaseRequest()
	badFormat.Uploads = []OpenAIImagesUpload{{Data: []byte("gif"), ContentType: "image/gif"}}
	assertInvalid(badFormat, "格式不支持")

	emptyRef := jiaotuBaseRequest()
	emptyRef.Uploads = []OpenAIImagesUpload{{Data: []byte(""), ContentType: "image/png"}}
	assertInvalid(emptyRef, "为空")

	external := jiaotuBaseRequest()
	external.InputImageURLs = []string{"https://cdn.test/ref.png"}
	assertInvalid(external, "不支持外链")
}

func TestNormalizeJiaotuImagesRequestDataURLReferences(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	payload := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte("png-bytes-here")), "+", "")
	parsed := jiaotuBaseRequest()
	parsed.Endpoint = openAIImagesEditsEndpoint
	parsed.InputImageURLs = []string{"DATA:image/png;base64," + payload}

	req, err := svc.NormalizeJiaotuImagesRequest(parsed, jiaotuTestCatalog())
	require.NoError(t, err)
	require.Len(t, req.References, 1)
	require.Equal(t, "image/png", req.References[0].ContentType)
	require.Equal(t, []byte("png-bytes-here"), req.References[0].Data)

	bad := jiaotuBaseRequest()
	bad.InputImageURLs = []string{"data:image/png"}
	_, err = svc.NormalizeJiaotuImagesRequest(bad, jiaotuTestCatalog())
	require.Error(t, err)
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Contains(t, jerr.Message, "逗号")
}

func TestNormalizeJiaotuImagesRequestUnknownUpstreamOnlyModel(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	catalog := NewJiaotuCatalog([]JiaotuUpstreamModel{
		{ID: 77, ShowModelName: "上游新图片模型", ModelType: 0, ImageSize: 3, CostRadish: 6},
	})
	parsed := jiaotuBaseRequest()
	parsed.Model = "img-77"

	req, err := svc.NormalizeJiaotuImagesRequest(parsed, catalog)
	require.NoError(t, err, "上游新模型应可直接使用，不必等本地登记")
	require.Equal(t, 77, req.Model.ProviderID)
	require.Equal(t, "img-77", req.Model.StableID)
	require.NotNil(t, req.Upstream)
	require.Equal(t, 6, req.Upstream.PointsCost())
	require.Equal(t, 3, req.Upstream.MaxReferenceImages())

	// 目录里没有的数字 ID 依然要拒绝，不得凭空造模型
	parsed.Model = "424242"
	_, err = svc.NormalizeJiaotuImagesRequest(parsed, catalog)
	require.Error(t, err)
}

func TestJiaotuRatioFromOpenAISizeRejectsUnknown(t *testing.T) {
	_, err := JiaotuRatioFromOpenAISize("2048x2048")
	require.Error(t, err)
	value, err := JiaotuRatioFromOpenAISize(" 16:9 ")
	require.NoError(t, err)
	require.Equal(t, "16:9", value)
}

func TestJiaotuFailoverClassification(t *testing.T) {
	cases := []struct {
		kind         JiaotuErrorKind
		wantFailover bool
	}{
		{JiaotuErrInsufficientPoints, true},
		{JiaotuErrUpstream5xx, true},
		{JiaotuErrEmptyResult, true},
		{JiaotuErrAuth, true},
		{JiaotuErrTimeout, true},
		{JiaotuErrInvalidRequest, false},
		{JiaotuErrUpstream4xx, true},
		// 上游按模型拒绝（「当前模型维护中」）：换号只会把同样的失败重复三遍。
		{JiaotuErrModelUnavailable, false},
	}
	for _, tc := range cases {
		err := newJiaotuError("imageChat", tc.kind, 500, "x")
		_, isFailover := jiaotuFailoverOrClientError(err).(*UpstreamFailoverError)
		require.Equal(t, tc.wantFailover, isFailover, string(tc.kind))
	}

	var clientErr *OpenAIImagesUpstreamError
	require.ErrorAs(t, jiaotuClientFacingError(newJiaotuError("normalize", JiaotuErrInvalidRequest, 0, "坏参数")), &clientErr)
	require.Equal(t, http.StatusBadRequest, clientErr.StatusCode)
	require.Equal(t, "invalid_request_error", clientErr.ErrorType)
	require.Equal(t, string(JiaotuErrInvalidRequest), clientErr.Code)
	// message 必须是上游/本地原话，不能带上 "椒图 imageChat 失败（kind）：" 前缀。
	require.Equal(t, "坏参数", clientErr.Message)

	// model_unavailable 同样直接回客户端，且同样不换号。
	modelErr := newJiaotuError("imageChat", JiaotuErrModelUnavailable, 0, "当前模型维护中，请选择其它模型使用")
	require.ErrorAs(t, jiaotuClientFacingError(modelErr), &clientErr)
	require.Equal(t, http.StatusBadRequest, clientErr.StatusCode)
	require.Equal(t, "invalid_request_error", clientErr.ErrorType)
	require.Equal(t, string(JiaotuErrModelUnavailable), clientErr.Code)
	require.Equal(t, "当前模型维护中，请选择其它模型使用", clientErr.Message)
}

func TestJiaotuEmptyStreamErrorPrefersUpstreamBusinessError(t *testing.T) {
	// 上游把拒绝当普通 text 事件回（实测 13 个号同一句话），必须认出来，
	// 否则退化成 empty_result → 换号 3 次 → 通用 502，用户看不到原因。
	stream := &JiaotuStreamResult{Events: []JiaotuStreamEvent{
		{"type": "text", "content": "<jiaotu-error>当前模型维护中，请选择其它模型使用</jiaotu-error>"},
		{"type": "text", "content": "<jiaotu-content>请稍后再试</jiaotu-content>"},
		{"type": "end"},
	}}
	require.Equal(t, "当前模型维护中，请选择其它模型使用", stream.UpstreamErrorMessage())

	// 真机形态：上游把标签拆到多个 text 事件（首事件只有开标签）。
	split := &JiaotuStreamResult{Events: []JiaotuStreamEvent{
		{"type": "text", "content": "<jiaotu-error>"},
		{"type": "text", "content": "当前模型维护中，请选择其它模型使用"},
		{"type": "text", "content": "</jiaotu-error>"},
		{"type": "end"},
	}}
	require.Equal(t, "当前模型维护中，请选择其它模型使用", split.UpstreamErrorMessage())

	err := jiaotuEmptyStreamError(stream, "椒图图片流结束但未返回图片")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrModelUnavailable, jerr.Kind)
	require.Contains(t, jerr.Message, "当前模型维护中，请选择其它模型使用")
	require.True(t, jerr.NonRetryableWithNextAccount(), "model_unavailable 必须免换号")

	// 没有 <jiaotu-error> 时保持原语义：仍是可换号的 empty_result。
	plain := &JiaotuStreamResult{Events: []JiaotuStreamEvent{
		{"type": "text", "content": "<jiaotu-content>好的</jiaotu-content>"},
		{"type": "end"},
	}}
	require.Empty(t, plain.UpstreamErrorMessage())
	plainErr, ok := IsJiaotuError(jiaotuEmptyStreamError(plain, "椒图图片流结束但未返回图片"))
	require.True(t, ok)
	require.Equal(t, JiaotuErrEmptyResult, plainErr.Kind)
	require.True(t, plainErr.RetryableWithNextAccount(), "真·空结果保留换号语义")
}

func TestJiaotuStreamParsesRealWorldEventShape(t *testing.T) {
	// 事件形态取自 2026-09-13 真机抓包（契约 R8/R9）：正文被 <jiaotu-content> 包裹，
	// 上游可能不回图而是发结构化反问。
	questionsPayload := `[{"question":"画面主体是什么？","options":["人物","动物","风景"]},{"question":"画面风格是？","options":["写实","卡通","国风"]}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJiaotuSSE(w,
			`{"type":"text","content":"<jiaotu-content>哎呀，我刚刚没太识别明白你发送的内容😜</jiaotu-content>","taskMode":"standard"}`,
			`{"type":"questions","content":`+jiaotuJSONString(questionsPayload)+`,"taskId":"standard:abc","taskStatus":"PROCESSING"}`,
			`{"type":"image","content":"<jiaotu-content>https://cdn.test/out.png?sig=1#frag</jiaotu-content>","ossId":"oss-999"}`,
			`{"type":"video","content":"<jiaotu-content>https://cdn.test/clip.mp4</jiaotu-content>"}`,
			`{"type":"end"}`,
		)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	stream, err := client.ImageChat(context.Background(), "tok", map[string]any{}, "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://cdn.test/out.png?sig=1"}, stream.ImageURLs, "必须剥掉正文包裹再取 '#' 前部分")
	require.Equal(t, []string{"https://cdn.test/clip.mp4"}, stream.VideoURLs)
	require.Equal(t, []string{"oss-999"}, stream.OSSIDs)
	require.Equal(t, "standard:abc", stream.TaskID)

	require.Equal(t, []JiaotuQuestion{
		{Question: "画面主体是什么？", Options: []string{"人物", "动物", "风景"}},
		{Question: "画面风格是？", Options: []string{"写实", "卡通", "国风"}},
	}, stream.ClarificationQuestions())
	require.Equal(t, "哎呀，我刚刚没太识别明白你发送的内容😜", stream.TextContent(400))
}

func TestJiaotuUnwrapContentTags(t *testing.T) {
	require.Equal(t, "", unwrapJiaotuContent(""))
	require.Equal(t, "plain", unwrapJiaotuContent("  plain  "))
	require.Equal(t, "https://a/b.png", unwrapJiaotuContent("<jiaotu-content>https://a/b.png</jiaotu-content>"))
	require.Equal(t, "ab", unwrapJiaotuContent("<jiaotu-content>a</jiaotu-content><jiaotu-content>b</jiaotu-content>"), "事体间本无分隔符，事体拼接不应插空格")
	require.Equal(t, "a。b", unwrapJiaotuContent("<jiaotu-content>a。</jiaotu-content>b"))
	require.Equal(t, "keep <not-jiaotu>", unwrapJiaotuContent("keep <not-jiaotu>"))
}

func TestJiaotuClarificationQuestionsTolerant(t *testing.T) {
	stream := &JiaotuStreamResult{Events: []JiaotuStreamEvent{
		{"type": "questions", "content": "不是 JSON"},
		{"type": "questions", "content": `[{"question":"  ","options":[]}]`},
		{"type": "text", "content": "无疑问"},
	}}
	require.Nil(t, stream.ClarificationQuestions())
	require.Equal(t, "无疑问", stream.TextContent(100))
	require.Nil(t, (*JiaotuStreamResult)(nil).ClarificationQuestions())
}

func jiaotuJSONString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func TestJiaotuNeedsClarification(t *testing.T) {
	stream := &JiaotuStreamResult{Events: []JiaotuStreamEvent{
		{"type": "questions", "content": `[{"question":"风格？","options":["写实","卡通"]}]`},
	}}
	require.True(t, stream.NeedsClarification())

	// 已经出图了就不该再追问（即便流里混着 questions）
	both := &JiaotuStreamResult{
		Events:    stream.Events,
		ImageURLs: []string{"https://cdn.test/a.png"},
	}
	require.False(t, both.NeedsClarification())

	require.False(t, (&JiaotuStreamResult{Events: []JiaotuStreamEvent{{"type": "end"}}}).NeedsClarification())
	require.False(t, (*JiaotuStreamResult)(nil).NeedsClarification())
}

func TestJiaotuClarificationAnswer(t *testing.T) {
	questions := []JiaotuQuestion{
		{Question: "画面主体是什么？", Options: []string{"人物", "动物", "风景"}},
		{Question: "画面风格是？", Options: []string{"写实", "卡通", "国风"}},
	}

	// 原始需求里能找到依据的选项优先（"橘猫"→无法匹配，"皮克斯"→无法匹配，全部退到首项）
	answer, ok := jiaotuClarificationAnswer("一只戴宇航头盔的橘猫", questions)
	require.True(t, ok)
	require.Contains(t, answer, "画面主体是什么？：人物", "无匹配时应取首个选项")
	require.Contains(t, answer, "请严格按以下需求直接生成图片，不要反问：一只戴宇航头盔的橘猫")

	// 命中的选项必须优先于顺序
	matched, ok := jiaotuClarificationAnswer("画一只动物，卡通风格", questions)
	require.True(t, ok)
	require.Contains(t, matched, "画面主体是什么？：动物")
	require.Contains(t, matched, "画面风格是？：卡通")

	// 开放性问题（无选项）不能瞎猜，交回上层保留真实错因
	_, ok = jiaotuClarificationAnswer("p", []JiaotuQuestion{{Question: "想要什么尺寸？"}})
	require.False(t, ok)

	_, ok = jiaotuClarificationAnswer("p", nil)
	require.False(t, ok)
}

// TestJiaotuSessionContinuationGetsImage 验证机制前提：反问后用同一 sessionId 续发即可拿到图。
// 上游按会话上下文推进任务，这条是真机验证前的 mock 版本。
func TestJiaotuSessionContinuationGetsImage(t *testing.T) {
	type seen struct {
		sessionID string
		prompt    string
	}
	var calls []seen

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		sessionID, _ := body["sessionId"].(string)
		prompt, _ := body["prompt"].(string)
		calls = append(calls, seen{sessionID: sessionID, prompt: prompt})

		if len(calls) == 1 {
			writeJiaotuSSE(w,
				`{"type":"text","content":"<jiaotu-content>哎呀，没太识别明白</jiaotu-content>"}`,
				`{"type":"questions","content":"[{\"question\":\"画面主体是什么？\",\"options\":[\"人物\",\"动物\"]}]"}`,
				`{"type":"end"}`,
			)
			return
		}
		writeJiaotuSSE(w, `{"type":"image","content":"https://cdn.test/final.png"}`)
	}))
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	req := JiaotuGenerateRequest{SessionID: "sess-fixed", Prompt: "一只戴宇航头盔的橘猫", ModelID: 18, Count: 1, Size: "1:1"}

	first, err := client.ImageChat(context.Background(), "tok", req.ChatBody(false), "")
	require.NoError(t, err)
	require.Empty(t, first.ImageURLs)
	require.True(t, first.NeedsClarification())

	answer, ok := jiaotuClarificationAnswer(req.Prompt, first.ClarificationQuestions())
	require.True(t, ok)
	req.Prompt = answer

	second, err := client.ImageChat(context.Background(), "tok", req.ChatBody(false), "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://cdn.test/final.png"}, second.ImageURLs)

	require.Len(t, calls, 2)
	require.Equal(t, calls[0].sessionID, calls[1].sessionID, "续发必须复用同一 sessionId")
	require.Equal(t, "sess-fixed", calls[1].sessionID)
	require.Contains(t, calls[1].prompt, "画面主体是什么？：人物")
	require.Contains(t, calls[1].prompt, "一只戴宇航头盔的橘猫")
}

func TestJiaotuSettingsAutoAnswerDefault(t *testing.T) {
	settings := JiaotuSettingsFromConfig(nil)
	require.True(t, settings.AutoAnswerQuestions, "默认开启自动应答，否则椒图分组几乎无法出图")
	require.Equal(t, "https://api.jiaotuai.cn", settings.APIBase)
	require.Equal(t, int64(10)<<20, settings.MaxRefBytes)

	custom := JiaotuSettingsFromConfig(&config.Config{Jiaotu: config.JiaotuConfig{
		APIBase:             "https://jiaotu.test",
		AutoAnswerQuestions: false,
	}})
	require.False(t, custom.AutoAnswerQuestions)
	require.Equal(t, "https://jiaotu.test", custom.APIBase)
	require.Equal(t, 600*time.Second, custom.Timeout, "未配置项仍要落到安全默认")
}

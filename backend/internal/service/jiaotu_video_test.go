//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func jiaotuVideoCatalog() *JiaotuCatalog {
	return NewJiaotuCatalog([]JiaotuUpstreamModel{
		{ID: 18, ShowModelName: "全能图片 V2", ModelType: 0, ImageSize: 6, CostRadish: 4},
		{ID: 25, ShowModelName: "MiniMax H3", ModelType: 1, ImageSize: 9, CostRadish: 30,
			DurationList:     []JiaotuModelOption{{Value: "5"}, {Value: "10"}, {Value: "15"}},
			QualityLevelList: []JiaotuModelOption{{Value: "480P"}, {Value: "768P"}},
			ResolutionList:   []JiaotuModelOption{{Value: "16:9"}, {Value: "9:16"}, {Value: "1:1"}},
		},
	})
}

func jiaotuVideoSettings() JiaotuSettings {
	return JiaotuSettings{APIBase: "https://jiaotu.test", WebOrigin: "https://jiaotu.test", MaxRefBytes: 1 << 20}.withDefaults()
}

func jiaotuVideoInput() map[string]any {
	return map[string]any{"model": "jiaotu-minimax-h3", "prompt": "镜头缓慢推进，晨雾中的孤舟", "duration": 8, "size": "9:16", "qualityLevel": "480P"}
}

func TestNormalizeJiaotuVideoRequestValid(t *testing.T) {
	req, err := NormalizeJiaotuVideoRequest(jiaotuVideoInput(), jiaotuVideoCatalog(), jiaotuVideoSettings())
	require.NoError(t, err)
	require.Equal(t, 25, req.Model.ProviderID)
	require.Equal(t, JiaotuCapabilityVideo, req.Model.Capability)
	require.NotNil(t, req.Upstream)
	require.Equal(t, 10, req.Duration, "8 秒不在允许值里时取最近的 10")
	require.Equal(t, "9:16", req.Ratio)
	require.Equal(t, "480P", req.Quality)
	require.Empty(t, req.References, "无参考图时不构造引用")

	body := req.ChatBody("sess-1", []string{"oss-1"})
	require.Equal(t, "10", body["duration"], "duration 必须是字符串且取归一后的值")
	require.Equal(t, 1, body["imageCount"], "视频固定 1")
	require.Equal(t, 25, body["modelId"])
	require.Equal(t, false, body["audio"])
	require.NotContains(t, body, "shotType", "空可选字段不得出现")
	require.NotContains(t, body, "promptExtend")

	// 单价 30/秒（costRadish）× 时长
	require.Equal(t, 300, req.MinPointsCost())
	require.Equal(t, 9, req.MaxReferenceImages(), "H3 支持 9 张参考图")
}

func TestNormalizeJiaotuVideoRequestRejectsLocally(t *testing.T) {
	catalog := jiaotuVideoCatalog()
	settings := jiaotuVideoSettings()

	assertInvalid := func(mutate func(map[string]any), want string) {
		t.Helper()
		input := jiaotuVideoInput()
		mutate(input)
		_, err := NormalizeJiaotuVideoRequest(input, catalog, settings)
		require.Error(t, err)
		jerr, ok := IsJiaotuError(err)
		require.True(t, ok)
		require.Equal(t, JiaotuErrInvalidRequest, jerr.Kind)
		require.Contains(t, jerr.Message, want)
	}

	assertInvalid(func(in map[string]any) { in["model"] = "jiaotu-image-v2" }, "/v1/images")
	assertInvalid(func(in map[string]any) { in["model"] = "不存在的模型" }, "未知椒图模型")
	assertInvalid(func(in map[string]any) { in["prompt"] = "   " }, "prompt")
	assertInvalid(func(in map[string]any) { in["prompt"] = strings.Repeat("啊", 6000) }, "提示词最多")
	assertInvalid(func(in map[string]any) { in["seed"] = 42 }, "seed")
	assertInvalid(func(in map[string]any) { in["referenceAudios"] = []any{"data:audio/wav;base64,AAA"} }, "不支持参考音频")
	assertInvalid(func(in map[string]any) { in["duration"] = 999 }, "时长最多")
	assertInvalid(func(in map[string]any) { in["image"] = "https://cdn.test/ref.png" }, "不支持外链")

	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))
	assertInvalid(func(in map[string]any) {
		refs := make([]any, 0, 10)
		for i := 0; i < 10; i++ {
			refs = append(refs, png)
		}
		in["image"] = refs
	}, "最多支持 9 张")

	// 未知画质/比例不得报错，应归一到上游允许值（宽容而非拒绝）
	input := jiaotuVideoInput()
	input["qualityLevel"] = "1080P"
	input["size"] = "21:9"
	req, err := NormalizeJiaotuVideoRequest(input, catalog, settings)
	require.NoError(t, err)
	require.Equal(t, "768P", req.Quality, "无匹配时退到默认画质，而不是悄悄换个画质")
	require.Equal(t, "16:9", req.Ratio)
}

func TestNormalizeJiaotuVideoRequestTolerantWithoutCatalog(t *testing.T) {
	// 模型清单不可用时仍要能按本地稳定表工作（fallback 上限 6 张、时长默认值）
	req, err := NormalizeJiaotuVideoRequest(jiaotuVideoInput(), nil, jiaotuVideoSettings())
	require.NoError(t, err)
	require.Nil(t, req.Upstream)
	require.Equal(t, 25, req.Model.ProviderID)
	require.Equal(t, jiaotuDefaultVideoDuration, req.Duration)
	require.Equal(t, jiaotuDefaultRefLimit, req.MaxReferenceImages())
}

func TestGenerateJiaotuVideoSuccess(t *testing.T) {
	var chatBodies []map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/upload/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"uploadUrl": server.URL + "/oss", "callbackToken": "cb"}})
		case "/oss":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/upload/callback":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"ossId": "oss-1"}})
		case "/api/v1/ai/imageChat":
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			chatBodies = append(chatBodies, body)
			require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
			writeJiaotuSSE(w,
				`{"type":"text","content":"<jiaotu-content>正在生成</jiaotu-content>"}`,
				`{"type":"video","content":"https://cdn.test/clip.mp4?sig=1","taskId":"standard:vid-1"}`,
			)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	settings := jiaotuVideoSettings()
	settings.APIBase = server.URL
	client := NewJiaotuClient(settings)

	req, err := NormalizeJiaotuVideoRequest(jiaotuVideoInput(), jiaotuVideoCatalog(), settings)
	require.NoError(t, err)
	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fake-png-bytes"))
	refs, err := jiaotuReferencesFromInput(map[string]any{"image": []any{png}}, settings.MaxRefBytes)
	require.NoError(t, err)
	req.References = refs

	result, err := GenerateJiaotuVideo(context.Background(), client, "tok", req, "")
	require.NoError(t, err)
	require.Equal(t, "https://cdn.test/clip.mp4?sig=1", result.URL)
	require.Equal(t, "standard:vid-1", result.TaskID)
	require.Equal(t, 25, result.ModelID)
	require.Equal(t, 1, result.ReferenceCount)
	require.Equal(t, "MiniMax H3", result.ModelName)

	require.Len(t, chatBodies, 1)
	require.Equal(t, []any{"oss-1"}, chatBodies[0]["imagesIds"], "参考图必须先换成 ossId")
	require.Equal(t, "10", chatBodies[0]["duration"])
}

func TestGenerateJiaotuVideoFailureKinds(t *testing.T) {
	cases := []struct {
		name     string
		frames   []string
		wantKind JiaotuErrorKind
	}{
		{"反问未出片", []string{`{"type":"questions","content":"[{\"question\":\"多长？\",\"options\":[\"5秒\"]}]"}`, `{"type":"end"}`}, JiaotuErrEmptyResult},
		{"流结束无产物", []string{`{"type":"text","content":"<jiaotu-content>好的</jiaotu-content>"}`, `{"type":"end"}`}, JiaotuErrEmptyResult},
		// 上游按模型拒绝：与图片同口径认成 model_unavailable，不再退化成通用 502。
		{"模型维护中", []string{`{"type":"text","content":"<jiaotu-error>当前模型维护中，请选择其它模型使用</jiaotu-error>"}`, `{"type":"end"}`}, JiaotuErrModelUnavailable},
		{"积分不足", []string{`{"type":"insufficient_points"}`}, JiaotuErrInsufficientPoints},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/ai/imageChat" {
					writeJiaotuSSE(w, tc.frames...)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			settings := jiaotuVideoSettings()
			settings.APIBase = server.URL
			req, err := NormalizeJiaotuVideoRequest(jiaotuVideoInput(), jiaotuVideoCatalog(), settings)
			require.NoError(t, err)

			_, err = GenerateJiaotuVideo(context.Background(), NewJiaotuClient(settings), "tok", req, "")
			jerr, ok := IsJiaotuError(err)
			require.True(t, ok)
			require.Equal(t, tc.wantKind, jerr.Kind)
			if tc.wantKind == JiaotuErrInsufficientPoints {
				require.True(t, jerr.RetryableWithNextAccount())
				require.True(t, jerr.ExhaustsAccount())
			}
		})
	}
}

func TestGenerateJiaotuVideoAuthAndEmptyToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	settings := jiaotuVideoSettings()
	settings.APIBase = server.URL
	req, err := NormalizeJiaotuVideoRequest(jiaotuVideoInput(), jiaotuVideoCatalog(), settings)
	require.NoError(t, err)

	_, err = GenerateJiaotuVideo(context.Background(), NewJiaotuClient(settings), "tok", req, "")
	jerr, ok := IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrAuth, jerr.Kind)

	_, err = GenerateJiaotuVideo(context.Background(), NewJiaotuClient(settings), "  ", req, "")
	jerr, ok = IsJiaotuError(err)
	require.True(t, ok)
	require.Equal(t, JiaotuErrAuth, jerr.Kind, "空 token 直接判鉴权问题，不打上游")
}

func TestJiaotuVideoOptionHelpers(t *testing.T) {
	require.Equal(t, 10, chooseJiaotuOptionNumber([]string{"5", "10", "15"}, 8, 5))
	require.Equal(t, 5, chooseJiaotuOptionNumber([]string{"5", "10"}, 1, 5))
	require.Equal(t, 7, chooseJiaotuOptionNumber(nil, 7, 5), "无候选时保留请求值")
	require.Equal(t, "16:9", chooseJiaotuOption([]string{"16:9", "9:16"}, "unknown", "16:9"))
	require.Equal(t, "custom", chooseJiaotuOption(nil, "custom", "16:9"))

	require.True(t, jiaotuBoolValue("有声"))
	require.True(t, jiaotuBoolValue(json.Number("1")))
	require.False(t, jiaotuBoolValue("false"))
	require.False(t, jiaotuBoolValue(nil))
	require.True(t, hasJiaotuValues([]any{1}))
	require.False(t, hasJiaotuValues([]any{}))
	require.False(t, hasJiaotuValues("  "))
}

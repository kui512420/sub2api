//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeJiaotuVideoStore 内存版任务存储，够覆盖轮询三态。
type fakeJiaotuVideoStore struct {
	records map[string]*service.ImageTaskRecord
}

func (f *fakeJiaotuVideoStore) Save(_ context.Context, task *service.ImageTaskRecord, _ time.Duration) error {
	clone := *task
	f.records[task.ID] = &clone
	return nil
}

func (f *fakeJiaotuVideoStore) Get(_ context.Context, id string) (*service.ImageTaskRecord, error) {
	record, ok := f.records[id]
	if !ok {
		return nil, service.ErrImageTaskNotFound
	}
	clone := *record
	return &clone, nil
}

// ListByPrefix 遍历内存记录，支持按前缀过滤（ vidtask_ 区分视频与图片任务）。
func (f *fakeJiaotuVideoStore) ListByPrefix(_ context.Context, prefix string) ([]*service.ImageTaskRecord, error) {
	out := make([]*service.ImageTaskRecord, 0, len(f.records))
	for id, record := range f.records {
		if strings.HasPrefix(id, prefix) {
			clone := *record
			out = append(out, &clone)
		}
	}
	return out, nil
}

func jiaotuVideoTestHandler(status, result, taskErr string) (*AsyncImageHandler, *service.ImageTaskService) {
	store := &fakeJiaotuVideoStore{records: map[string]*service.ImageTaskRecord{}}
	tasks := service.NewImageTaskServiceWithOptions(store, time.Hour, 10*time.Minute)
	now := time.Now().Unix()
	record := &service.ImageTaskRecord{
		ID:        "vidtask_1",
		UserID:    7,
		APIKeyID:  11,
		Status:    status,
		CreatedAt: now,
		ExpiresAt: now + 3600,
	}
	if result != "" {
		record.Result = json.RawMessage(result)
	}
	if taskErr != "" {
		record.Error = json.RawMessage(taskErr)
	}
	_, _ = tasks.Create(context.Background(), service.ImageTaskOwner{UserID: 7, APIKeyID: 11})
	store.records["vidtask_1"] = record
	return &AsyncImageHandler{tasks: tasks}, tasks
}

func jiaotuVideoRequestWithKey(path, id string, userID, apiKeyID int64) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{{Key: "request_id", Value: id}}
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: apiKeyID, UserID: userID})
	return c, recorder
}

func jiaotuVideoRequestWithoutKey(path, id string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	c.Params = gin.Params{{Key: "request_id", Value: id}}
	return c, recorder
}

func TestJiaotuVideoStatusProcessing(t *testing.T) {
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusProcessing, "", "")
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1", "vidtask_1", 7, 11)
	h.JiaotuVideoStatus(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "5", recorder.Header().Get("Retry-After"), "处理中必须告诉客户端多久再来问")
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "in_progress", body["status"])
	require.Equal(t, "video", body["object"])
	require.Equal(t, float64(0), body["progress"])
	require.NotContains(t, body, "url")
}

func TestJiaotuVideoStatusCompletedMergesResult(t *testing.T) {
	result := `{"id":"vidtask_1","object":"video","status":"completed","progress":100,"url":"https://cdn.test/a.mp4","duration":10,"size":"16:9","resolution":"480P","has_audio":false,"reference_count":2}`
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusCompleted, result, "")
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1", "vidtask_1", 7, 11)
	h.JiaotuVideoStatus(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "completed", body["status"])
	require.Equal(t, "https://cdn.test/a.mp4", body["url"])
	require.Equal(t, float64(10), body["duration"])
	require.Equal(t, float64(100), body["progress"])
	require.Equal(t, false, body["has_audio"])
	require.Equal(t, float64(2), body["reference_count"])
}

func TestJiaotuVideoStatusFailedKeepsError(t *testing.T) {
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusFailed, "", `{"type":"api_error","message":"椒图视频流结束但未返回视频"}`)
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1", "vidtask_1", 7, 11)
	h.JiaotuVideoStatus(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "failed", body.Status)
	require.Contains(t, string(body.Error), "未返回视频")
}

func TestJiaotuVideoStatusFailedWithoutErrorPayload(t *testing.T) {
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusFailed, "", "")
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1", "vidtask_1", 7, 11)
	h.JiaotuVideoStatus(c)

	var body struct {
		Error map[string]string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "api_error", body.Error["type"], "缺错误体时也要给出可解析的错误对象")
}

func TestJiaotuVideoStatusOwnership(t *testing.T) {
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusCompleted, `{"url":"https://cdn.test/a.mp4"}`, "")

	// 别的 API Key 不能读到别人的任务；越权一律 404（ImageTaskService 有意不泄露任务是否存在）
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1", "vidtask_1", 8, 12)
	h.JiaotuVideoStatus(c)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// 不存在的任务 → 404
	c2, recorder2 := jiaotuVideoRequestWithKey("/v1/videos/nope", "nope", 7, 11)
	h.JiaotuVideoStatus(c2)
	require.Equal(t, http.StatusNotFound, recorder2.Code)

	// 未认证（context 里根本没有 APIKey）→ 403；不能退化成"匿名可读"
	c3, recorder3 := jiaotuVideoRequestWithoutKey("/v1/videos/vidtask_1", "vidtask_1")
	h.JiaotuVideoStatus(c3)
	require.Equal(t, http.StatusForbidden, recorder3.Code)

	c4, recorder4 := jiaotuVideoRequestWithoutKey("/v1/videos/vidtask_1/content", "vidtask_1")
	h.JiaotuVideoContent(c4)
	require.Equal(t, http.StatusForbidden, recorder4.Code)
}

func TestJiaotuVideoContentGuardsState(t *testing.T) {
	h, _ := jiaotuVideoTestHandler(service.ImageTaskStatusProcessing, "", "")
	c, recorder := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1/content", "vidtask_1", 7, 11)
	h.JiaotuVideoContent(c)
	require.Equal(t, http.StatusConflict, recorder.Code, "未完成任务不能取内容")

	// 状态是 completed 但载荷里没有可下载地址：仍属于「未就绪」，不能假装 404 资源不存在
	h2, _ := jiaotuVideoTestHandler(service.ImageTaskStatusCompleted, `{"status":"completed","object":"video"}`, "")
	c2, recorder2 := jiaotuVideoRequestWithKey("/v1/videos/vidtask_1/content", "vidtask_1", 7, 11)
	h2.JiaotuVideoContent(c2)
	require.Equal(t, http.StatusConflict, recorder2.Code)
}

func TestJiaotuVideoPollURL(t *testing.T) {
	require.Equal(t, "/v1/videos/vidtask_9", jiaotuVideoPollURL("/v1/videos", "vidtask_9"))
	require.Equal(t, "/v1/videos/vidtask_9", jiaotuVideoPollURL("/v1/videos/vidtask_old", "vidtask_9"))
	require.Equal(t, "/v1/videos/vidtask_9", jiaotuVideoPollURL("", "vidtask_9"))
	require.Equal(t, "/v1/videos/vidtask_9", jiaotuVideoPollURL("/", "vidtask_9"))
}

func TestJiaotuVideoErrorPayloadHelpers(t *testing.T) {
	payload := jiaotuVideoErrorPayload(nil)
	require.Contains(t, string(payload), "video generation failed")

	var decoded struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(jiaotuVideoErrorPayload(errForTest{msg: strings.Repeat("错误", 500)}), &decoded))
	require.LessOrEqual(t, len([]rune(decoded.Message)), 301, "错误文本必须按 rune 截断，避免污染任务记录")
	require.NotContains(t, decoded.Message, "�", "截断不得切出半个字符")

	// 椒图分类错误的状态映射在 service 包内测试（newJiaotuError 不导出）；
	// 这里只兜底未知错误。
	require.Equal(t, http.StatusBadGateway, jiaotuVideoFailureStatus(nil))
	require.Equal(t, http.StatusBadGateway, jiaotuVideoFailureStatus(errForTest{msg: "boom"}))

}

type errForTest struct{ msg string }

func (e errForTest) Error() string { return e.msg }

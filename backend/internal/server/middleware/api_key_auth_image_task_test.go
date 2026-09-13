package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAsyncMediaTaskRead(t *testing.T) {
	// 图片任务只读（既有语义，不得回归）
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/images/tasks/imgtask_123"))
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/images/tasks/imgtask_123"))
	require.False(t, isAsyncMediaTaskRead(http.MethodPost, "/v1/images/tasks/imgtask_123"))
	require.False(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/images/generations"))

	// 视频任务只读：列表、单查、内容拉取都要免计费。
	// 生成扣掉余额后仍能取回结果，否则创作记录页「有片子却看不到」（实测踩过）。
	// 注：URL.Path 不含 query，limit=50 这类参数不影响判定。
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos"))
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/vidtask_abc"))
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/generations/vidtask_abc"))
	require.True(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/generations/vidtask_abc/content"))

	// 创建端点是 POST，不享受免计费；GET 形式的创建路径也不能被误放行。
	require.False(t, isAsyncMediaTaskRead(http.MethodPost, "/v1/videos"))
	require.False(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/generations"))
	require.False(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/edits"))
	require.False(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/videos/extensions"))
	require.False(t, isAsyncMediaTaskRead(http.MethodGet, "/v1/chat/completions"))
}

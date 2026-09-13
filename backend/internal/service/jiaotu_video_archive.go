package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 椒图视频转存对象存储。
//
// 图片走 ImageResultUploader.Rewrite（按 OpenAI 图片响应 data[].url 解析），视频载荷
// 形状不同、体积也大得多，因此单独实现一个轻量转存器，但复用同一个 ImageStorage
// （同一套 R2/S3 凭证与桶），仅用 videos/ 前缀与图片区分。
const (
	jiaotuVideoObjectPrefix = "videos/"
	// 视频比图片大：同步生图上限 32MiB，视频放宽到 512MiB。
	jiaotuVideoMaxArchiveBytes int64 = 512 << 20
	jiaotuVideoObjectContentType     = "video/mp4"
)

// JiaotuVideoArchiveTimeout 是单个视频「下载上游 + 上传对象存储」的总时限。
const JiaotuVideoArchiveTimeout = 10 * time.Minute

// JiaotuVideoArchiver 把上游生成好的视频字节转存到对象存储，返回可下载 URL。
type JiaotuVideoArchiver struct {
	storage    ImageStorage
	httpClient *http.Client
}

// NewJiaotuVideoArchiver 构造转存器；storage 为 nil 时返回 nil（表示不做持久化）。
func NewJiaotuVideoArchiver(storage ImageStorage) *JiaotuVideoArchiver {
	if storage == nil {
		return nil
	}
	return &JiaotuVideoArchiver{
		storage:    storage,
		httpClient: &http.Client{Timeout: JiaotuVideoArchiveTimeout},
	}
}

// Available 报告转存器是否可用。
func (a *JiaotuVideoArchiver) Available() bool {
	return a != nil && a.storage != nil
}

// Archive 下载 upstreamURL 指向的视频并写入对象存储，返回对象的可访问 URL
// （私有桶为 presigned 临时签名链接，与图片转存一致）。taskID 用于生成稳定的对象 key。
func (a *JiaotuVideoArchiver) Archive(ctx context.Context, taskID, upstreamURL string) (string, error) {
	if !a.Available() {
		return "", fmt.Errorf("video archiver is unavailable")
	}
	upstreamURL = strings.TrimSpace(upstreamURL)
	if upstreamURL == "" {
		return "", fmt.Errorf("upstream video url is empty")
	}
	data, contentType, err := a.download(ctx, upstreamURL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(contentType, "video/") {
		// 部分上游不回标准 Content-Type，回退按 mp4 处理，不因此判失败。
		contentType = jiaotuVideoObjectContentType
	}
	key := jiaotuVideoObjectPrefix + strings.TrimPrefix(taskID, JiaotuVideoTaskIDPrefix) + ".mp4"
	url, err := a.storage.Save(ctx, key, contentType, data)
	if err != nil {
		return "", fmt.Errorf("upload video to object storage: %w", err)
	}
	return url, nil
}

func (a *JiaotuVideoArchiver) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, JiaotuVideoArchiveTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build video download request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download video: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download video: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, jiaotuVideoMaxArchiveBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read video body: %w", err)
	}
	if int64(len(data)) > jiaotuVideoMaxArchiveBytes {
		return nil, "", fmt.Errorf("video exceeds %d bytes limit", jiaotuVideoMaxArchiveBytes)
	}
	contentType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	if contentType == "" {
		contentType = jiaotuVideoObjectContentType
	}
	return data, contentType, nil
}

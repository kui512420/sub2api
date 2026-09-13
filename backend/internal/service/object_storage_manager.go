package service

import (
	"context"
	"strings"
	"time"
)

// 对象存储的「后台素材库」管理能力：列举 / 删除生成产物（图片、视频）。
//
// ImageStorage 只要求实现 Save（写入）即可支撑生成链路；后台预览/管理是可选的
// 增强能力，由 S3 兼容实现额外满足 ObjectManager。这样最小实现（如测试桩）不必
// 被迫实现列举/删除，同时后台可以在运行时用类型断言判断是否开放管理页。

// StoredObjectKind 标记对象类别，便于前端分组展示与筛选。
const (
	StoredObjectKindImage = "image"
	StoredObjectKindVideo = "video"
	StoredObjectKindOther = "other"
)

// StoredObject 是对象存储中的一个生成产物。
type StoredObject struct {
	Key          string    `json:"key"`
	Kind         string    `json:"kind"`
	ContentType  string    `json:"content_type"`
	SizeBytes    int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
	// URL 是即时生成的预签名预览/下载地址（私有桶场景），不入库、每次列举现签。
	URL string `json:"url"`
}

// StoredObjectPage 是一页列举结果。
type StoredObjectPage struct {
	Objects     []StoredObject `json:"objects"`
	NextToken   string         `json:"next_token,omitempty"`
	IsTruncated bool           `json:"is_truncated"`
}

// ObjectManager 是对象存储的可选管理能力（列举 / 删除）。
type ObjectManager interface {
	// ListObjects 按前缀列举对象，continuationToken 用于翻页。
	ListObjects(ctx context.Context, prefix, continuationToken string, maxKeys int32) (*StoredObjectPage, error)
	// DeleteObject 删除指定 key 的对象。
	DeleteObject(ctx context.Context, key string) error
}

// InferStoredObjectKind 按 key 前缀/扩展名推断对象类别。
func InferStoredObjectKind(key string) string {
	lower := strings.ToLower(key)
	switch {
	case strings.HasPrefix(lower, "videos/") || strings.HasSuffix(lower, ".mp4") ||
		strings.HasSuffix(lower, ".webm") || strings.HasSuffix(lower, ".mov"):
		return StoredObjectKindVideo
	case strings.HasPrefix(lower, "images/") || strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".jpg") || strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".webp") || strings.HasSuffix(lower, ".gif"):
		return StoredObjectKindImage
	default:
		return StoredObjectKindOther
	}
}

// IsManagedObjectKey 判断 key 是否落在允许后台管理的前缀白名单内。
// 管理接口只能操作生成产物前缀（图片前缀、videos/），不得触碰备份 backups/ 等
// 其他对象，避免越权删除。allowedPrefixes 为空时一律拒绝。
func IsManagedObjectKey(key string, allowedPrefixes []string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.Contains(key, "..") {
		return false
	}
	for _, prefix := range allowedPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

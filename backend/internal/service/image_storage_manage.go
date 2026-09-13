package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// 后台素材库（对象存储里的生成产物）的列举 / 删除编排。
//
// 安全边界：素材库只允许浏览和删除「生成产物」前缀——图片前缀（来自配置，默认
// images/）与视频前缀（videos/）。备份对象走 backups/，绝不在此暴露或可删。
// "全部"视图通过分别列举这两个前缀后按时间合并实现，而不是列整个桶根。

// ManagedObjectLibrary 是素材库一页结果（附带桶信息供前端展示）。
type ManagedObjectLibrary struct {
	Enabled     bool           `json:"enabled"`
	Bucket      string         `json:"bucket"`
	Kind        string         `json:"kind"`
	Objects     []StoredObject `json:"objects"`
	NextToken   string         `json:"next_token,omitempty"`
	IsTruncated bool           `json:"is_truncated"`
}

// 素材库支持的类别筛选值。
const (
	ManagedKindAll   = "all"
	ManagedKindImage = StoredObjectKindImage
	ManagedKindVideo = StoredObjectKindVideo
)

var (
	// ErrManagedStorageUnavailable 对象存储未启用 / 凭证不全 / 实现不支持管理。
	ErrManagedStorageUnavailable = infraerrors.New(http.StatusServiceUnavailable,
		"MANAGED_STORAGE_UNAVAILABLE", "object storage is not available for management")
	// ErrManagedObjectForbidden key 不在允许管理的生成产物前缀内。
	ErrManagedObjectForbidden = infraerrors.New(http.StatusForbidden,
		"MANAGED_OBJECT_FORBIDDEN", "object key is outside the managed media prefixes")
)

// manager 用当前有效配置构造一个对象管理器，并返回对应配置。
func (s *ImageStorageSettingService) manager(ctx context.Context) (ObjectManager, *config.ImageStorageConfig, error) {
	cfg, err := s.effectiveConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.Enabled || !cfg.IsConfigured() {
		return nil, nil, ErrManagedStorageUnavailable
	}
	storage, err := s.factory(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	mgr, ok := storage.(ObjectManager)
	if !ok {
		return nil, nil, ErrManagedStorageUnavailable
	}
	return mgr, cfg, nil
}

// imagePrefix 返回图片对象前缀（与上传时拼接 key 的前缀保持一致）。
func managedImagePrefix(cfg *config.ImageStorageConfig) string {
	prefix := strings.TrimSpace(cfg.Prefix)
	if prefix == "" {
		return "images/"
	}
	return prefix
}

// ManagedPrefixes 返回允许后台管理的前缀白名单（图片 + 视频）。
func (s *ImageStorageSettingService) ManagedPrefixes(cfg *config.ImageStorageConfig) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 2)
	for _, prefix := range []string{managedImagePrefix(cfg), jiaotuVideoObjectPrefix} {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}
	return out
}

// ListManagedObjects 按类别列举生成产物。kind ∈ all/image/video，token 为上一页
// 返回的 next_token（不透明），limit 为单页数量。
func (s *ImageStorageSettingService) ListManagedObjects(ctx context.Context, kind, token string, limit int32) (*ManagedObjectLibrary, error) {
	mgr, cfg, err := s.manager(ctx)
	if err != nil {
		return nil, err
	}
	imagePrefix := managedImagePrefix(cfg)
	videoPrefix := jiaotuVideoObjectPrefix

	switch kind {
	case "", ManagedKindAll:
		kind = ManagedKindAll
	case ManagedKindImage, ManagedKindVideo:
	default:
		kind = ManagedKindAll
	}

	if kind == ManagedKindImage {
		page, err := mgr.ListObjects(ctx, imagePrefix, token, limit)
		if err != nil {
			return nil, err
		}
		return &ManagedObjectLibrary{Enabled: true, Bucket: cfg.Bucket, Kind: kind,
			Objects: page.Objects, NextToken: page.NextToken, IsTruncated: page.IsTruncated}, nil
	}
	if kind == ManagedKindVideo {
		page, err := mgr.ListObjects(ctx, videoPrefix, token, limit)
		if err != nil {
			return nil, err
		}
		return &ManagedObjectLibrary{Enabled: true, Bucket: cfg.Bucket, Kind: kind,
			Objects: page.Objects, NextToken: page.NextToken, IsTruncated: page.IsTruncated}, nil
	}

	// all：分别列举图片 / 视频前缀，再按修改时间倒序合并。
	imgToken, vidToken := decodeManagedContinuation(token)
	imgPage, err := mgr.ListObjects(ctx, imagePrefix, imgToken, limit)
	if err != nil {
		return nil, err
	}
	vidPage, err := mgr.ListObjects(ctx, videoPrefix, vidToken, limit)
	if err != nil {
		return nil, err
	}
	merged := make([]StoredObject, 0, len(imgPage.Objects)+len(vidPage.Objects))
	merged = append(merged, imgPage.Objects...)
	merged = append(merged, vidPage.Objects...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].LastModified.After(merged[j].LastModified) })

	hasMore := imgPage.IsTruncated || vidPage.IsTruncated
	var nextToken string
	if hasMore {
		nextToken = encodeManagedContinuation(imgPage.NextToken, vidPage.NextToken)
	}
	if limit > 0 && int32(len(merged)) > limit {
		merged = merged[:limit]
	}
	return &ManagedObjectLibrary{Enabled: true, Bucket: cfg.Bucket, Kind: kind,
		Objects: merged, NextToken: nextToken, IsTruncated: hasMore}, nil
}

// DeleteManagedObject 删除一个生成产物；key 必须落在图片/视频前缀白名单内。
func (s *ImageStorageSettingService) DeleteManagedObject(ctx context.Context, key string) error {
	mgr, cfg, err := s.manager(ctx)
	if err != nil {
		return err
	}
	if !IsManagedObjectKey(key, s.ManagedPrefixes(cfg)) {
		return ErrManagedObjectForbidden
	}
	return mgr.DeleteObject(ctx, key)
}

// all 视图的组合翻页 token：base64url(imageToken + "\x1f" + videoToken)。
func encodeManagedContinuation(imageToken, videoToken string) string {
	raw := imageToken + "\x1f" + videoToken
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeManagedContinuation(token string) (imageToken, videoToken string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	// 只有「本服务编码的组合 token」解码后才一定含分隔符 0x1f。单类别视图传入的
	// 是 S3 原始 continuation token，即便它恰好是合法 base64 字符，也因不含分隔符
	// 而必须原样作为图片 token 返回，不能把解码后的乱码当 token。
	if err != nil || !strings.ContainsRune(string(raw), 0x1f) {
		return token, ""
	}
	if idx := strings.IndexByte(string(raw), 0x1f); idx >= 0 {
		return string(raw[:idx]), string(raw[idx+1:])
	}
	return token, ""
}

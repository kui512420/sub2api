package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 确保 S3ImageStorage 满足后台素材库需要的管理能力。
var _ service.ObjectManager = (*S3ImageStorage)(nil)

const (
	defaultManagedListMaxKeys int32 = 48
	maxManagedListMaxKeys     int32 = 200
)

// ListObjects 按前缀列举桶内对象并为每个对象现签一个预览 URL。
// ListObjectsV2 不返回 ContentType，这里按扩展名推断 MIME 与类别，避免逐个 HeadObject。
func (s *S3ImageStorage) ListObjects(ctx context.Context, prefix, continuationToken string, maxKeys int32) (*service.StoredObjectPage, error) {
	if maxKeys <= 0 {
		maxKeys = defaultManagedListMaxKeys
	}
	if maxKeys > maxManagedListMaxKeys {
		maxKeys = maxManagedListMaxKeys
	}
	input := &s3.ListObjectsV2Input{
		Bucket:  &s.bucket,
		MaxKeys: &maxKeys,
	}
	if strings.TrimSpace(prefix) != "" {
		input.Prefix = aws.String(prefix)
	}
	if strings.TrimSpace(continuationToken) != "" {
		input.ContinuationToken = aws.String(continuationToken)
	}

	out, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("S3 ListObjectsV2: %w", err)
	}

	presignClient := s3.NewPresignClient(s.client)
	page := &service.StoredObjectPage{
		Objects:     make([]service.StoredObject, 0, len(out.Contents)),
		IsTruncated: aws.ToBool(out.IsTruncated),
	}
	if out.NextContinuationToken != nil {
		page.NextToken = *out.NextContinuationToken
	}

	for _, obj := range out.Contents {
		if obj.Key == nil {
			continue
		}
		key := *obj.Key
		// 跳过「目录占位对象」（key 以 / 结尾且大小为 0）。
		if strings.HasSuffix(key, "/") {
			continue
		}
		contentType := contentTypeForManagedKey(key)
		signed, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
			Bucket: &s.bucket,
			Key:    &key,
		}, s3.WithPresignExpires(s.presignExpiry))
		if err != nil {
			return nil, fmt.Errorf("presign object %q: %w", key, err)
		}
		item := service.StoredObject{
			Key:         key,
			Kind:        service.InferStoredObjectKind(key),
			ContentType: contentType,
			URL:         signed.URL,
		}
		if obj.Size != nil {
			item.SizeBytes = *obj.Size
		}
		if obj.LastModified != nil {
			item.LastModified = *obj.LastModified
		}
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}

// DeleteObject 删除单个对象。
func (s *S3ImageStorage) DeleteObject(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("S3 DeleteObject: %w", err)
	}
	return nil
}

func contentTypeForManagedKey(key string) string {
	switch strings.ToLower(filepath.Ext(key)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	default:
		return "application/octet-stream"
	}
}

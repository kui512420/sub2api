package repository

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const imageTaskKeyPrefix = "image_task:"

type imageTaskStore struct {
	rdb *redis.Client
}

func NewImageTaskStore(rdb *redis.Client) service.ImageTaskStore {
	return &imageTaskStore{rdb: rdb}
}

func (s *imageTaskStore) Save(ctx context.Context, task *service.ImageTaskRecord, ttl time.Duration) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, imageTaskKey(task.ID), data, ttl).Err()
}

func (s *imageTaskStore) Get(ctx context.Context, id string) (*service.ImageTaskRecord, error) {
	data, err := s.rdb.Get(ctx, imageTaskKey(id)).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, service.ErrImageTaskNotFound
		}
		return nil, err
	}
	var task service.ImageTaskRecord
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func imageTaskKey(id string) string {
	return imageTaskKeyPrefix + strings.TrimSpace(id)
}

// ListByPrefix 用 SCAN 遍历同前缀任务并逐条读出。
//
// 用 SCAN 而不是 KEYS：任务量可能到几百上千条（24h TTL），KEYS 会阻塞 Redis。
// 单条 MGET 不可行是因为 key 里存的 id 才是真实任务 ID，拿到 key 后直接 GET 即可。
func (s *imageTaskStore) ListByPrefix(ctx context.Context, prefix string) ([]*service.ImageTaskRecord, error) {
	pattern := imageTaskKey(prefix) + "*"
	records := make([]*service.ImageTaskRecord, 0, 32)
	var cursor uint64
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			data, err := s.rdb.Get(ctx, key).Bytes()
			if err != nil {
				if err == redis.Nil {
					continue // TTL 刚好在 SCAN 与 GET 之间过期，跳过即可
				}
				return nil, err
			}
			var task service.ImageTaskRecord
			if err := json.Unmarshal(data, &task); err != nil {
				continue // 脏数据不阻断整页列表
			}
			records = append(records, &task)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return records, nil
}

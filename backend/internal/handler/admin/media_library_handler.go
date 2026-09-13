package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 后台素材库：预览 / 管理对象存储里的生成产物（图片、视频）。
// 复用 BackupHandler 持有的 ImageStorageSettingService（同一套 R2/S3 配置）。
// 列举为只读操作；删除属于破坏性操作，在路由层叠加 step-up 2FA。

// ListStoredObjects GET /admin/backups/image-storage/objects
// 查询参数：type=all|image|video，continuation_token=上一页 next_token，limit=单页数量。
func (h *BackupHandler) ListStoredObjects(c *gin.Context) {
	kind := strings.TrimSpace(c.DefaultQuery("type", service.ManagedKindAll))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "48"))
	if limit <= 0 {
		limit = 48
	}
	library, err := h.imageStorage.ListManagedObjects(
		c.Request.Context(), kind, c.Query("continuation_token"), int32(limit))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, library)
}

// DeleteStoredObject DELETE /admin/backups/image-storage/objects?key=...
// 兼容 JSON body {"key":"..."}。只允许删除图片/视频前缀下的对象（service 层强制白名单）。
func (h *BackupHandler) DeleteStoredObject(c *gin.Context) {
	key := strings.TrimSpace(c.Query("key"))
	if key == "" {
		var body struct {
			Key string `json:"key"`
		}
		_ = c.ShouldBindJSON(&body)
		key = strings.TrimSpace(body.Key)
	}
	if key == "" {
		response.BadRequest(c, "key is required")
		return
	}
	if err := h.imageStorage.DeleteManagedObject(c.Request.Context(), key); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true, "key": key})
}

package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminComplianceGuard 已移除"部署与运营合规确认"拦截：管理员访问控制台不再需要
// 确认合规承诺，直接放行后续中间件与路由。保留函数签名仅为兼容现有路由注册调用。
func AdminComplianceGuard(_ *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
	}
}

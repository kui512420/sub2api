//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShouldAccumulateUserPlatformQuotaWhitelist 锁定 user × platform 配额累加的守卫口径：
// 只有 AllowedQuotaPlatforms 白名单内的平台才可能有配额行（admin 设置、注册快照、
// DB 的 user_platform_quotas_platform_check 用的是同一份枚举），其余平台一律跳过。
func TestShouldAccumulateUserPlatformQuotaWhitelist(t *testing.T) {
	t.Parallel()

	for _, platform := range AllowedQuotaPlatforms {
		require.True(t, ShouldAccumulateUserPlatformQuota(platform),
			"白名单平台 %q 必须参与平台配额累加", platform)
	}

	// 椒图是原生图片/视频上游，没有 user × platform 配额行；composite 是分组概念，
	// 计量平台在算 quotaPlatform 时已被替换成具体上游平台。两者都必须跳过，
	// 否则写入必然撞 CHECK 约束（ALERT 日志 + flusher 整批 UPSERT 失败）。
	require.False(t, ShouldAccumulateUserPlatformQuota(PlatformJiaotu))
	require.False(t, ShouldAccumulateUserPlatformQuota(PlatformComposite))
	require.False(t, ShouldAccumulateUserPlatformQuota(""))
	require.False(t, ShouldAccumulateUserPlatformQuota("  jiaotu  "))
}

// TestAllowedQuotaPlatformsExcludesNativePoolPlatforms 保证后续再加工分池型上游
// （积分/套餐制，无 user 级配额行）时不会误入白名单——白名单一旦扩大，
// 就要同步扩 DB CHECK 约束，这是两个独立的变更。
func TestAllowedQuotaPlatformsExcludesNativePoolPlatforms(t *testing.T) {
	t.Parallel()

	require.Contains(t, AllowedQuotaPlatforms, PlatformMiniMax) // 已入库平台仍在白名单内（回归哨兵）
	require.NotContains(t, AllowedQuotaPlatforms, PlatformJiaotu)
	require.NotContains(t, AllowedQuotaPlatforms, PlatformComposite)
}

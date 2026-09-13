//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 这组用例不测注册流程本身（本轮只搬代码、不做真实注册），
// 只锁住一条安全边界：**默认必须拒绝，且拒绝时不发任何网络请求**。
func TestJiaotuAutoRegisterDisabledByDefault(t *testing.T) {
	require.False(t, (&OpenAIGatewayService{cfg: &config.Config{}}).JiaotuAutoRegisterEnabled(),
		"零配置下补号必须是关闭的")

	_, err := (&OpenAIGatewayService{cfg: &config.Config{}}).RegisterJiaotuAccount(context.Background())
	require.ErrorIs(t, err, ErrJiaotuAutoRegisterDisabled)

	var nilService *OpenAIGatewayService
	require.False(t, nilService.JiaotuAutoRegisterEnabled())
	_, err = nilService.RegisterJiaotuAccount(context.Background())
	require.ErrorIs(t, err, ErrJiaotuAutoRegisterDisabled)
}

func TestJiaotuAutoRegisterNeedsCredentials(t *testing.T) {
	// 只开开关、不配接码凭据：Enabled 仍为 false，避免"开了开关才发现没号可接码"。
	enabled := &config.Config{}
	enabled.Jiaotu.AutoRegister.Enabled = true
	require.False(t, (&OpenAIGatewayService{cfg: enabled}).JiaotuAutoRegisterEnabled())

	// 配了豪猪但没项目 ID：开关状态只反映"有凭据"，SID 缺失在真正取号时才报错。
	withHaoZhu := &config.Config{}
	withHaoZhu.Jiaotu.AutoRegister.Enabled = true
	withHaoZhu.Jiaotu.AutoRegister.SMS.Haozhu.User = "u"
	withHaoZhu.Jiaotu.AutoRegister.SMS.Haozhu.Pass = "p"
	require.True(t, (&OpenAIGatewayService{cfg: withHaoZhu}).JiaotuAutoRegisterEnabled())

	_, err := (&OpenAIGatewayService{cfg: withHaoZhu}).jiaotuRegistrarForTest().smsProvider()
	require.NoError(t, err)

	_, err = (&OpenAIGatewayService{}).jiaotuRegistrarForTest().smsProvider()
	require.ErrorIs(t, err, ErrJiaotuNoSMSProvider)
}

func TestJiaotuRegistrationConfigDefaultsAreEmptySecrets(t *testing.T) {
	settings := jiaotuRegistrationConfigFrom(&config.Config{})
	require.False(t, settings.Enabled)
	require.Empty(t, settings.ProxyAPI)
	require.Empty(t, settings.HaoZhu.Token)
	require.Empty(t, settings.HaoZhu.User)
	require.Empty(t, settings.HaoZhu.Pass)
	require.Empty(t, settings.My531.Token)
	require.Empty(t, settings.My531.User)
	require.Empty(t, settings.My531.Pass)
	require.Equal(t, jiaotuDefaultHaoZhuAPI, settings.HaoZhu.API)
	require.Equal(t, jiaotuDefaultMy531API, settings.My531.API)
	require.Equal(t, 2*time.Minute, settings.Cooldown)
	require.Equal(t, 3, settings.ProxyAttempts)
	require.Equal(t, 4*time.Second, settings.MinInterval)
}

func TestJiaotuProxyEndpointParsing(t *testing.T) {
	require.Equal(t, "1.2.3.4:8080", jiaotuProxyEndpointFrom("1.2.3.4:8080"))
	require.Equal(t, "1.2.3.4:8080", jiaotuProxyEndpointFrom("http://1.2.3.4:8080\n"))
	require.Equal(t, "1.2.3.4:8080", jiaotuProxyEndpointFrom(`{"code":0,"data":"1.2.3.4:8080"}`))
	require.Equal(t, "1.2.3.4:8080", jiaotuProxyEndpointFrom("garbage\n1.2.3.4:8080"))
	require.Equal(t, "", jiaotuProxyEndpointFrom(""))
	require.Equal(t, "", jiaotuProxyEndpointFrom("1.2.3.4"), "缺端口不算可用代理")
	require.Equal(t, "", jiaotuProxyEndpointFrom("not a proxy"))
}

func TestJiaotuSMSCodeExtraction(t *testing.T) {
	require.Equal(t, "123456", jiaotuSMSCode("验证码：123456 五分钟内有效"))
	require.Equal(t, "123456", jiaotuSMSCode("1|prefix|123456"), "6 位优先于其它数字串")
	require.Equal(t, "1234", jiaotuSMSCode("code 1234 only"))
	require.Equal(t, "", jiaotuSMSCode("no digits here"))
	require.Equal(t, "0571888", jiaotuSMSCode("来电 0571888"), "7 位也接受")

	// my531 顶层 code 是状态码（1 = 还在等），不能被当成验证码
	require.Equal(t, "", jiaotuMy531Value(`{"code":1,"msg":"please wait"}`, "code"))
	require.Equal(t, "13800000000", jiaotuMy531Value(`{"code":0,"data":"13800000000"}`, "phone"))
	require.Equal(t, "13800000000", jiaotuMy531Value("1|13800000000", "phone"))

	// my531 成败看 stat，不看 code（这里锁住搬迁语义，防止再次发挥）
	require.True(t, jiaotuMy531Success(`{"stat":true,"data":"13800000000"}`))
	require.True(t, jiaotuMy531Success(`{"stat":"1","data":"tok"}`))
	require.False(t, jiaotuMy531Success(`{"stat":false,"msg":"bad token"}`))
	require.False(t, jiaotuMy531Success(`{"code":1,"msg":"waiting"}`), "缺 stat 一律视为未成功")
	require.True(t, jiaotuMy531Success("1|13800000000"), "非 JSON 回落管道分隔格式")
}

func TestJiaotuRegistrationSMSWiringIsInertWithoutNetwork(t *testing.T) {
	// 没有真实凭据时，构造 provider 不应发出任何请求（httptest 未启动即证明这点）。
	settings := jiaotuRegistrationConfigFrom(&config.Config{})
	provider, err := (&jiaotuRegistrar{cfg: settings}).smsProvider()
	require.ErrorIs(t, err, ErrJiaotuNoSMSProvider)
	require.Nil(t, provider)
}

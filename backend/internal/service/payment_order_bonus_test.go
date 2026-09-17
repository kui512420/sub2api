//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// bonusOrderFixture describes one CreateOrder scenario. The single enabled
// provider instance is EasyPay (its constructor only needs plain strings, so no
// real credentials are required) and its upstream is a local stub.
type bonusOrderFixture struct {
	instanceLimits   string
	globalMultiplier string
	amount           float64
}

// createBonusOrder runs CreateOrder end to end for a single enabled EasyPay
// instance serving "alipay" with the given limits, then returns the persisted order.
func createBonusOrder(t *testing.T, f bonusOrderFixture) *ent.PaymentOrder {
	t.Helper()
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"trade_no":"T-1","payurl":"https://pay.example.com/x","qrcode":""}`))
	}))
	t.Cleanup(server.Close)

	client := newPaymentConfigServiceTestClient(t)

	instanceConfig := `{"pid":"pid-1","pkey":"pkey-1","apiBase":"` + server.URL + `",` +
		`"notifyUrl":"https://example.com/notify","returnUrl":"https://example.com/return"}`

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay").
		SetConfig(instanceConfig).
		SetSupportedTypes(string(payment.TypeAlipay)).
		SetEnabled(true).
		SetSortOrder(1).
		SetLimits(f.instanceLimits).
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("bonus-create@example.com").
		SetUsername("bonus-user").
		SetPasswordHash("hash").
		SetStatus(payment.EntityStatusActive).
		Save(ctx)
	require.NoError(t, err)

	settingRepo := newMockSettingRepo()
	settingRepo.data[SettingPaymentEnabled] = "true"
	if f.globalMultiplier != "" {
		settingRepo.data[SettingBalanceRechargeMult] = f.globalMultiplier
	}

	svc := &PaymentService{
		entClient:     client,
		registry:      payment.NewRegistry(),
		configService: NewPaymentConfigService(client, settingRepo, nil),
		userRepo: &mockUserRepo{getByIDUser: &User{
			ID: user.ID, Email: user.Email, Username: user.Username, Status: payment.EntityStatusActive,
		}},
		loadBalancer:    payment.NewDefaultLoadBalancer(client, nil),
		providersLoaded: true,
	}

	_, err = svc.CreateOrder(ctx, CreateOrderRequest{
		UserID:      user.ID,
		Amount:      f.amount,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeBalance,
	})
	require.NoError(t, err)

	order, err := client.PaymentOrder.Query().Only(ctx)
	require.NoError(t, err)
	return order
}

// 端到端：到账金额由最终选中渠道的倍率决定，且订单 amount 与 pay_amount 分离。
func TestCreateOrderCreditsChannelBonusMultiplier(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"bonusMultiplier":1.2}}`,
		globalMultiplier: "1",
		amount:           100,
	})

	// 实付 100，渠道倍率 1.2 → 到账 120；网关实收仍为 100
	require.Equal(t, 120.0, order.Amount)
	require.Equal(t, 100.0, order.PayAmount)
}

// 渠道配置覆盖全局倍率，两者不相乘。
func TestCreateOrderChannelMultiplierBeatsGlobal(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"bonusMultiplier":1.2}}`,
		globalMultiplier: "2",
		amount:           100,
	})

	require.Equal(t, 120.0, order.Amount, "channel 1.2 must win over global 2.0, never 2.4")
	require.Equal(t, 100.0, order.PayAmount)
}

// 渠道未配置倍率时回落到全局倍率（改动前行为的兼容基线）。
func TestCreateOrderFallsBackToGlobalMultiplier(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"singleMax":1000}}`,
		globalMultiplier: "1.5",
		amount:           100,
	})

	require.Equal(t, 150.0, order.Amount)
	require.Equal(t, 100.0, order.PayAmount)
}

// 渠道与全局都不配置时，到账金额等于实付金额。
func TestCreateOrderWithoutAnyMultiplierCreditsExactly(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   "",
		globalMultiplier: "",
		amount:           100,
	})

	require.Equal(t, 100.0, order.Amount)
	require.Equal(t, 100.0, order.PayAmount)
}

// 渠道单笔上限基于用户实付金额，不受到账金额影响。
func TestCreateOrderValidatesChannelLimitAgainstPaidAmount(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"singleMax":100,"bonusMultiplier":1.2}}`,
		globalMultiplier: "1",
		amount:           100,
	})

	// 实付 100 未超上限，即使到账 120 也不应被拒
	require.Equal(t, 120.0, order.Amount)
}

// 非法渠道倍率回落全局，不阻断下单。
func TestCreateOrderInvalidChannelMultiplierFallsBackToGlobal(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"bonusMultiplier":0}}`,
		globalMultiplier: "1.5",
		amount:           100,
	})

	require.Equal(t, 150.0, order.Amount)
}

// 显式渠道倍率 1.0 覆盖全局倍率，到账等于实付。
func TestCreateOrderExplicitOneOverridesGlobal(t *testing.T) {
	order := createBonusOrder(t, bonusOrderFixture{
		instanceLimits:   `{"alipay":{"bonusMultiplier":1}}`,
		globalMultiplier: "1.5",
		amount:           100,
	})

	require.Equal(t, 100.0, order.Amount)
}

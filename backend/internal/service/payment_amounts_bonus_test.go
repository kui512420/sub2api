//go:build unit

package service

import (
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func bonusFloatPtr(v float64) *float64 { return &v }

// 渠道倍率优先于全局倍率，两者不相乘。
func TestResolveChannelBonusMultiplierPrefersChannelOverGlobal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		channel *float64
		global  float64
		want    float64
	}{
		{name: "channel configured overrides global", channel: bonusFloatPtr(1.2), global: 2.0, want: 1.2},
		{name: "channel configured below global", channel: bonusFloatPtr(0.5), global: 2.0, want: 0.5},
		{name: "channel explicit 1.0 overrides global", channel: bonusFloatPtr(1.0), global: 1.5, want: 1.0},
		{name: "channel unset falls back to global", channel: nil, global: 1.5, want: 1.5},
		{name: "both unset yields 1.0", channel: nil, global: 0, want: 1.0},
		{name: "global invalid yields 1.0", channel: nil, global: -3, want: 1.0},
		{name: "channel zero falls back to global", channel: bonusFloatPtr(0), global: 1.5, want: 1.5},
		{name: "channel negative falls back to global", channel: bonusFloatPtr(-1), global: 1.5, want: 1.5},
		{name: "channel NaN falls back to global", channel: bonusFloatPtr(math.NaN()), global: 1.5, want: 1.5},
		{name: "channel Inf falls back to global", channel: bonusFloatPtr(math.Inf(1)), global: 1.5, want: 1.5},
		{name: "channel invalid and global invalid yields 1.0", channel: bonusFloatPtr(0), global: 0, want: 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveChannelBonusMultiplier(tt.channel, tt.global); got != tt.want {
				t.Fatalf("resolveChannelBonusMultiplier(%v, %v) = %v, want %v", tt.channel, tt.global, got, tt.want)
			}
		})
	}
}

// 渠道倍率与全局倍率不相乘（1.2 × 2.0 = 2.4 是错误行为）。
func TestResolveChannelBonusMultiplierNeverMultiplies(t *testing.T) {
	t.Parallel()

	got := resolveChannelBonusMultiplier(bonusFloatPtr(1.2), 2.0)
	if got == 2.4 {
		t.Fatal("channel and global multipliers must not multiply")
	}
	if got != 1.2 {
		t.Fatalf("got %v, want 1.2", got)
	}
}

func TestIsValidBonusMultiplier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value float64
		want  bool
	}{
		{name: "one", value: 1, want: true},
		{name: "above one", value: 1.25, want: true},
		{name: "below one", value: 0.14, want: true},
		{name: "tiny positive", value: 0.0001, want: true},
		{name: "zero", value: 0, want: false},
		{name: "negative", value: -1, want: false},
		{name: "NaN", value: math.NaN(), want: false},
		{name: "positive Inf", value: math.Inf(1), want: false},
		{name: "negative Inf", value: math.Inf(-1), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidBonusMultiplier(tt.value); got != tt.want {
				t.Fatalf("isValidBonusMultiplier(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// 选定实例的倍率解析：nil 选择回落全局。
func TestResolveSelectionBonusMultiplier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sel    *payment.InstanceSelection
		global float64
		want   float64
	}{
		{name: "nil selection falls back to global", sel: nil, global: 1.5, want: 1.5},
		{name: "nil selection and invalid global yields 1.0", sel: nil, global: 0, want: 1.0},
		{
			name:   "selection without channel multiplier falls back to global",
			sel:    &payment.InstanceSelection{InstanceID: "1"},
			global: 1.5,
			want:   1.5,
		},
		{
			name:   "selection with channel multiplier overrides global",
			sel:    &payment.InstanceSelection{InstanceID: "1", BonusMultiplier: bonusFloatPtr(1.2)},
			global: 2.0,
			want:   1.2,
		},
		{
			name:   "selection with invalid channel multiplier falls back to global",
			sel:    &payment.InstanceSelection{InstanceID: "1", BonusMultiplier: bonusFloatPtr(0)},
			global: 1.5,
			want:   1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveSelectionBonusMultiplier(tt.sel, tt.global); got != tt.want {
				t.Fatalf("resolveSelectionBonusMultiplier() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 到账金额：实付 100 配 1.2 倍应得 120，且以选中实例的倍率为准。
func TestCreditedBalanceUsesResolvedChannelMultiplier(t *testing.T) {
	t.Parallel()

	sel := &payment.InstanceSelection{InstanceID: "7", BonusMultiplier: bonusFloatPtr(1.2)}
	multiplier := resolveSelectionBonusMultiplier(sel, 2.0)

	got := calculateCreditedBalance(100, multiplier)
	if got != 120 {
		t.Fatalf("credited balance = %v, want 120", got)
	}
}

// 同类型多渠道：各自按自己实例的倍率计算。
func TestCreditedBalanceDiffersPerChannelUnderSamePaymentType(t *testing.T) {
	t.Parallel()

	channelWithBonus := &payment.InstanceSelection{InstanceID: "1", ProviderKey: "easypay", BonusMultiplier: bonusFloatPtr(1.1)}
	channelWithoutBonus := &payment.InstanceSelection{InstanceID: "2", ProviderKey: "alipay"}

	global := 1.0
	withBonus := calculateCreditedBalance(100, resolveSelectionBonusMultiplier(channelWithBonus, global))
	withoutBonus := calculateCreditedBalance(100, resolveSelectionBonusMultiplier(channelWithoutBonus, global))

	if withBonus != 110 {
		t.Fatalf("channel with bonus credited = %v, want 110", withBonus)
	}
	if withoutBonus != 100 {
		t.Fatalf("channel without bonus credited = %v, want 100", withoutBonus)
	}
}

// 未配置任何倍率时到账金额等于实付金额（改动前行为的兼容基线）。
func TestCreditedBalanceWithoutAnyMultiplierEqualsPayment(t *testing.T) {
	t.Parallel()

	sel := &payment.InstanceSelection{InstanceID: "1"}
	got := calculateCreditedBalance(100, resolveSelectionBonusMultiplier(sel, 0))
	if got != 100 {
		t.Fatalf("credited balance = %v, want 100", got)
	}
}

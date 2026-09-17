//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func TestValidateChannelBonusMultipliers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		limits  string
		wantErr bool
		reason  string
	}{
		{name: "empty limits", limits: "", wantErr: false},
		{name: "blank limits", limits: "   ", wantErr: false},
		{name: "no bonus key", limits: `{"alipay":{"singleMax":100}}`, wantErr: false},
		{name: "null bonus is unset", limits: `{"alipay":{"bonusMultiplier":null}}`, wantErr: false},
		{name: "valid bonus", limits: `{"alipay":{"bonusMultiplier":1.2}}`, wantErr: false},
		{name: "valid bonus below one", limits: `{"alipay":{"bonusMultiplier":0.5}}`, wantErr: false},
		{name: "multiple types valid", limits: `{"alipay":{"bonusMultiplier":1.2},"wxpay":{"bonusMultiplier":1.0}}`, wantErr: false},
		{name: "zero rejected", limits: `{"alipay":{"bonusMultiplier":0}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "negative rejected", limits: `{"alipay":{"bonusMultiplier":-1}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "string rejected", limits: `{"alipay":{"bonusMultiplier":"1.2"}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "bool rejected", limits: `{"alipay":{"bonusMultiplier":true}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "object rejected", limits: `{"alipay":{"bonusMultiplier":{}}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "one invalid among valid rejected", limits: `{"alipay":{"bonusMultiplier":1.2},"wxpay":{"bonusMultiplier":0}}`, wantErr: true, reason: "INVALID_BONUS_MULTIPLIER"},
		{name: "malformed json", limits: `{"alipay":`, wantErr: true, reason: "VALIDATION_ERROR"},
		{name: "array rejected", limits: `[1,2]`, wantErr: true, reason: "VALIDATION_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateChannelBonusMultipliers(tt.limits)
			if tt.wantErr && err == nil {
				t.Fatalf("validateChannelBonusMultipliers(%q) = nil, want error", tt.limits)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateChannelBonusMultipliers(%q) = %v, want nil", tt.limits, err)
			}
			if tt.wantErr {
				appErr := infraerrors.FromError(err)
				if appErr.Reason != tt.reason {
					t.Fatalf("reason = %q, want %q", appErr.Reason, tt.reason)
				}
			}
		})
	}
}

// 渠道 JSON 中的 bonusMultiplier 必须能被 ChannelLimits 正确反序列化，
// 且未配置时保持 nil（与显式 1.0 区分）。
func TestChannelLimitsParsesBonusMultiplier(t *testing.T) {
	t.Parallel()

	var limits payment.InstanceLimits

	if err := json.Unmarshal([]byte(`{"alipay":{"singleMax":100,"bonusMultiplier":1.25}}`), &limits); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	alipay := limits["alipay"]
	if alipay.BonusMultiplier == nil {
		t.Fatal("bonusMultiplier should be parsed, got nil")
	}
	if *alipay.BonusMultiplier != 1.25 {
		t.Fatalf("bonusMultiplier = %v, want 1.25", *alipay.BonusMultiplier)
	}
	if alipay.SingleMax != 100 {
		t.Fatalf("singleMax = %v, want 100", alipay.SingleMax)
	}

	var legacy payment.InstanceLimits
	if err := json.Unmarshal([]byte(`{"alipay":{"singleMax":100}}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy["alipay"].BonusMultiplier != nil {
		t.Fatal("absent bonusMultiplier must stay nil, not default to 1.0")
	}
}

// 显式 1.0 与未配置必须在序列化层面可区分。
func TestChannelLimitsExplicitOneIsNotNil(t *testing.T) {
	t.Parallel()

	var limits payment.InstanceLimits
	if err := json.Unmarshal([]byte(`{"alipay":{"bonusMultiplier":1.0}}`), &limits); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := limits["alipay"].BonusMultiplier
	if got == nil {
		t.Fatal("explicit 1.0 must deserialize to a non-nil pointer")
	}
	if *got != 1.0 {
		t.Fatalf("bonusMultiplier = %v, want 1.0", *got)
	}
}

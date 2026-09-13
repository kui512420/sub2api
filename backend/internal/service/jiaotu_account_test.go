//go:build unit

package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func jiaotuAccount(creds map[string]any) *Account {
	return &Account{
		ID:          7,
		Name:        "椒图 jp-9",
		Platform:    PlatformJiaotu,
		Type:        AccountTypeAPIKey,
		Credentials: creds,
	}
}

func TestAccountIsJiaotu(t *testing.T) {
	require.False(t, (&Account{Platform: PlatformOpenAI}).IsJiaotu())
	require.False(t, (*Account)(nil).IsJiaotu())
	require.True(t, (&Account{Platform: PlatformJiaotu}).IsJiaotu())
	require.Equal(t, "jiaotu", PlatformJiaotu)
}

func TestJiaotuCredentialAccessors(t *testing.T) {
	account := jiaotuAccount(map[string]any{
		JiaotuCredentialToken:          "Bearer abc.def.ghi",
		JiaotuCredentialPoolID:         "jp-9",
		JiaotuCredentialPhone:          "13800001234",
		JiaotuCredentialPoints:         float64(49),
		JiaotuCredentialUpstreamStatus: "ok",
		JiaotuCredentialPointsAt:       "2026-09-12T03:34:26.126Z",
	})

	require.Equal(t, "abc.def.ghi", account.JiaotuToken(), "必须容忍历史数据里的 Bearer 前缀")
	require.Equal(t, "jp-9", account.JiaotuPoolID())
	require.Equal(t, 49, account.JiaotuPoints())
	require.Equal(t, "138****1234", MaskJiaotuPhone(account.GetCredential(JiaotuCredentialPhone)))
	require.Equal(t, "ok", account.JiaotuUpstreamStatus())
	require.True(t, account.JiaotuUsableInPool())
	require.True(t, account.JiaotuHasEnoughPoints(49))
	require.False(t, account.JiaotuHasEnoughPoints(50))
	require.True(t, account.JiaotuHasEnoughPoints(0), "免费请求不设积分门槛")

	pointsAt := account.JiaotuPointsAt()
	require.NotNil(t, pointsAt)
	require.Equal(t, 2026, pointsAt.Year())

	// 日志里不得出现 token / 完整手机号
	require.Equal(t, "jp-9(138****1234)", account.JiaotuIdentityForLog())
	require.NotContains(t, account.JiaotuIdentityForLog(), "abc.def.ghi")
}

func TestJiaotuCredentiaDefaultsAndTolerance(t *testing.T) {
	bare := jiaotuAccount(map[string]any{JiaotuCredentialToken: "tok"})
	require.Equal(t, JiaotuUpstreamStatusOK, bare.JiaotuUpstreamStatus(), "缺省状态按 ok")
	require.Equal(t, 0, bare.JiaotuPoints())
	require.Nil(t, bare.JiaotuPointsAt())
	require.True(t, bare.JiaotuUsableInPool())

	expired := jiaotuAccount(map[string]any{
		JiaotuCredentialToken: "tok", JiaotuCredentialUpstreamStatus: "expired",
	})
	require.False(t, expired.JiaotuUsableInPool())

	noToken := jiaotuAccount(map[string]any{JiaotuCredentialPoints: 99})
	require.False(t, noToken.JiaotuUsableInPool())
	require.Equal(t, "", noToken.JiaotuToken())

	garbage := jiaotuAccount(map[string]any{JiaotuCredentialToken: "tok", JiaotuCredentialPoints: "很多"})
	require.Equal(t, 0, garbage.JiaotuPoints())

	// 非椒图账号一律读不到凭据，避免误用其他平台的 token 字段
	other := &Account{Platform: PlatformOpenAI, Credentials: map[string]any{"token": "x"}}
	require.Equal(t, "", other.JiaotuToken())
	require.Equal(t, "", other.JiaotuUpstreamStatus())
	require.Equal(t, 0, other.JiaotuPoints())
	require.Equal(t, "", other.JiaotuIdentityForLog())
}

func TestMaskJiaotuPhone(t *testing.T) {
	require.Equal(t, "", MaskJiaotuPhone(""))
	require.Equal(t, "138****1234", MaskJiaotuPhone(" 13800001234 "))
	require.Equal(t, "1****9", MaskJiaotuPhone("123456789"))
	require.Equal(t, "**", MaskJiaotuPhone("a"))
}

func TestParseJiaotuPoolInputFormats(t *testing.T) {
	poolJSON := `{"accounts":[{"id":"jp-9","phone":"13800001234","token":"tokA","userId":730754,
		"nickName":"椒图081641","inviteOwn":"7N9ZL","points":49,"status":"ok","lastError":"",
		"pointsAt":"2026-09-12T03:34:26.126Z"},{"id":"jp-10","token":"tokB","points":3,"status":"ok"},
		{"id":"jp-11","token":"tokC","status":"expired","lastError":"账号认证失败"}],"cursor":1,"targetSize":5}`

	entries, err := ParseJiaotuPoolInput(poolJSON)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, "jp-9", entries[0].PoolID)
	require.Equal(t, "tokA", entries[0].Token)
	require.Equal(t, 49, entries[0].Points)
	require.Equal(t, "7N9ZL", entries[0].InviteOwn)
	require.Equal(t, JiaotuUpstreamStatusExpired, entries[2].NormalizeStatus())

	target, ok := JiaotuPoolTargetSize(poolJSON)
	require.True(t, ok)
	require.Equal(t, 5, target)

	wrapped := `{"code":0,"data":{"accounts":[{"token":"tokD","points":10}],"targetSize":2}}`
	entries, err = ParseJiaotuPoolInput(wrapped)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "tokD", entries[0].Token)
	target, ok = JiaotuPoolTargetSize(wrapped)
	require.True(t, ok)
	require.Equal(t, 2, target)

	dataArray := `{"code":0,"data":[{"token":"tokE"}]}`
	entries, err = ParseJiaotuPoolInput(dataArray)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "tokE", entries[0].Token)

	bareArray := `[{"id":"jp-1","token":"tokF"},{"id":"jp-2","token":"tokG"}]`
	entries, err = ParseJiaotuPoolInput(bareArray)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	text := "jp-1,13800001234,tokH\ntokI\n13800005678 tokJ\n# 注释行\n\n"
	entries, err = ParseJiaotuPoolInput(text)
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, "jp-1", entries[0].PoolID)
	require.Equal(t, "tokH", entries[0].Token)
	require.Equal(t, "tokI", entries[1].Token)
	require.Equal(t, "13800005678", entries[2].Phone)
	require.Equal(t, "tokJ", entries[2].Token)
}

func TestParseJiaotuPoolInputDedupeAndErrors(t *testing.T) {
	// 同一 poolID 只保留一条
	dup := `{"accounts":[{"id":"jp-9","token":"tokA"},{"id":"jp-9","token":"tokZ"}]}`
	entries, err := ParseJiaotuPoolInput(dup)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "tokA", entries[0].Token)

	// 无 poolID 时按 token 去重
	dupToken := `[{"token":"tokA"},{"token":"tokA","id":"jp-x"},{"token":"tokB","id":"jp-y"}]`
	entries, err = ParseJiaotuPoolInput(dupToken)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	_, err = ParseJiaotuPoolInput("   ")
	require.Error(t, err)

	_, err = ParseJiaotuPoolInput(`{"accounts":[{"phone":"13800001234"}]}`)
	require.Error(t, err, "全部缺 token 必须报错而不是静默导入 0 条")

	_, err = ParseJiaotuPoolInput("not json at all")
	require.Error(t, err, "既不是 JSON 也没有 token 的行要报错")
}

func TestBuildJiaotuCredentialsRoundTrip(t *testing.T) {
	entries, err := ParseJiaotuPoolInput(`{"accounts":[
		{"id":"jp-9","phone":"13800001234","token":"tokA","userId":730754,"nickName":"椒图081641",
		 "inviteCode":"7PQ8K","inviteOwn":"7N9ZL","points":49,"status":"ok","pointsAt":"2026-09-12T03:34:26.126Z"},
		{"token":"tokB","points":-5,"status":"weird"}]}`)
	require.NoError(t, err)

	creds := BuildJiaotuCredentials(entries[0])
	account := jiaotuAccount(creds)
	require.Equal(t, "tokA", account.JiaotuToken())
	require.Equal(t, 49, account.JiaotuPoints())
	require.Equal(t, "730754", account.GetCredential(JiaotuCredentialUserID))
	require.Equal(t, "椒图081641", account.GetCredential(JiaotuCredentialNickName))
	require.Equal(t, "7N9ZL", account.GetCredential(JiaotuCredentialInviteOwn))
	require.Equal(t, "7PQ8K", account.GetCredential(JiaotuCredentialInviteCode))
	require.NotNil(t, account.JiaotuPointsAt())

	require.Equal(t, "椒图 jp-9", JiaotuAccountName(entries[0], 0))
	require.Equal(t, "weird", entries[1].NormalizeStatus(), "未知状态保留原值")
	unknown := jiaotuAccount(BuildJiaotuCredentials(entries[1]))
	require.False(t, unknown.JiaotuUsableInPool(), "只有 ok 能被选号挑中，未知状态不默认放行")

	negative := jiaotuAccount(BuildJiaotuCredentials(entries[1]))
	require.Equal(t, 0, negative.JiaotuPoints(), "负积分按 0 处理，避免选号门槛被绕过")

	// credentials 里不能出现会被 SanitizeStoredCredentials 剥掉的键名
	sanitized := SanitizeStoredCredentials(PlatformJiaotu, BuildJiaotuCredentials(entries[0]))
	require.Equal(t, "tokA", jiaotuAccount(sanitized).JiaotuToken(), "导入链路必须能安全穿过凭据清洗")

	// credentials 必须是可 JSON 序列化的（落 DB 的 datatypes.JSONMap）
	raw, err := json.Marshal(BuildJiaotuCredentials(entries[0]))
	require.NoError(t, err)
	require.Contains(t, string(raw), `"token":"tokA"`)
}

func TestMergeJiaotuCredentials(t *testing.T) {
	existing := map[string]any{
		JiaotuCredentialToken:  "tokA",
		JiaotuCredentialPoints: 49,
		"proxy_custom":         "keepme",
	}
	incoming := map[string]any{
		JiaotuCredentialToken:  "",
		JiaotuCredentialPoints: 12,
		JiaotuCredentialPoolID: "jp-9",
	}
	merged := MergeJiaotuCredentials(existing, incoming)
	require.Equal(t, "tokA", merged[JiaotuCredentialToken], "空 token 不得覆盖已有 token")
	require.Equal(t, 12, merged[JiaotuCredentialPoints])
	require.Equal(t, "jp-9", merged[JiaotuCredentialPoolID])
	require.Equal(t, "keepme", merged["proxy_custom"], "无关键必须保留")

	replaced := MergeJiaotuCredentials(existing, map[string]any{JiaotuCredentialToken: "tokNew"})
	require.Equal(t, "tokNew", replaced[JiaotuCredentialToken])
}

func TestParseJiaotuTime(t *testing.T) {
	require.Nil(t, parseJiaotuTime(""))
	require.Nil(t, parseJiaotuTime("昨天"))
	require.NotNil(t, parseJiaotuTime(time.Now().UTC().Format(time.RFC3339Nano)))
	require.NotNil(t, parseJiaotuTime("1757000000"))
}

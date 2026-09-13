//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// fakeJiaotuStore 记录状态回写，满足 JiaotuAccountStore 最小面。
type fakeJiaotuStore struct {
	account    *Account
	getErr     error
	updateErr  error
	updates    []*Account
	getByIDArg int64
}

func (f *fakeJiaotuStore) GetByID(_ context.Context, id int64) (*Account, error) {
	f.getByIDArg = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.account == nil {
		return nil, errors.New("account not found")
	}
	clone := *f.account
	clone.Credentials = map[string]any{}
	for key, value := range f.account.Credentials {
		clone.Credentials[key] = value
	}
	return &clone, nil
}

func (f *fakeJiaotuStore) Update(_ context.Context, account *Account) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updates = append(f.updates, account)
	f.account = account
	return nil
}

func jiaotuPoolAccount(points int) *Account {
	return &Account{
		ID:       42,
		Name:     "椒图 jp-9",
		Platform: PlatformJiaotu,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			JiaotuCredentialToken:          "tok",
			JiaotuCredentialPoolID:         "jp-9",
			JiaotuCredentialPoints:         points,
			JiaotuCredentialUpstreamStatus: JiaotuUpstreamStatusOK,
		},
		Schedulable: true,
	}
}

func TestMarkJiaotuCredintsSurviveStoreRoundTrip(t *testing.T) {
	account := jiaotuPoolAccount(49)
	store := &fakeJiaotuStore{account: account}

	markJiaotuAccountPointsExhausted(context.Background(), store, account, 50)
	require.Len(t, store.updates, 1)
	stored := store.updates[0]
	require.Equal(t, 0, stored.JiaotuPoints())
	require.Contains(t, stored.GetCredential(JiaotuCredentialLastError), "需要 50")
	require.Equal(t, "tok", stored.JiaotuToken(), "回写不得丢掉 token")
	require.Equal(t, "jp-9", stored.JiaotuPoolID())
}

func TestMarkJiaotuPointsExhaustedAppliesTempCooldown(t *testing.T) {
	account := jiaotuPoolAccount(49)
	store := &fakeJiaotuStore{account: account}
	before := time.Now()

	markJiaotuAccountPointsExhausted(context.Background(), store, account, 50)

	require.Equal(t, int64(42), store.getByIDArg)
	require.Len(t, store.updates, 1)
	updated := store.updates[0]

	require.Equal(t, 0, updated.JiaotuPoints())
	require.Equal(t, JiaotuUpstreamStatusOK, updated.JiaotuUpstreamStatus(), "积分不足不是登录失效，不得标 expired")
	require.True(t, updated.Schedulable, "积分不足只应临时冷却，不能永久下架")
	require.Equal(t, StatusActive, updated.Status)
	require.Equal(t, "jiaotu_insufficient_points", updated.TempUnschedulableReason)

	require.NotNil(t, updated.TempUnschedulableUntil)
	until := *updated.TempUnschedulableUntil
	require.WithinDuration(t, before.Add(jiaotuPointsCooldown), until, 5*time.Second,
		"冷却时长必须等于 jiaotuPointsCooldown")
	require.False(t, updated.IsSchedulable(), "冷却期内调度器必须选不到该号")

	// 内存对象同步，避免同一请求内重复回写
	require.Equal(t, 0, account.JiaotuPoints())
	require.NotNil(t, account.TempUnschedulableUntil)
}

func TestMarkJiaotuExpiredHardPauses(t *testing.T) {
	account := jiaotuPoolAccount(49)
	store := &fakeJiaotuStore{account: account}

	markJiaotuAccountExpired(context.Background(), store, account, "账号登录已失效")

	require.Len(t, store.updates, 1)
	updated := store.updates[0]
	require.Equal(t, JiaotuUpstreamStatusExpired, updated.JiaotuUpstreamStatus())
	require.False(t, updated.Schedulable, "token 无法刷新，必须硬停等管理员处理")
	require.NotEqual(t, StatusActive, updated.Status)
	require.Contains(t, updated.ErrorMessage, "登录已失效")
	require.Nil(t, updated.TempUnschedulableUntil, "硬停不需要再叠一层冷却")
	require.False(t, updated.IsSchedulable())
}

func TestClearJiaotuAccountError(t *testing.T) {
	account := jiaotuPoolAccount(49)
	account.Credentials[JiaotuCredentialLastError] = "上次失败"
	store := &fakeJiaotuStore{account: account}

	clearJiaotuAccountError(context.Background(), store, account)
	require.Len(t, store.updates, 1)
	_, exists := store.updates[0].Credentials[JiaotuCredentialLastError]
	require.False(t, exists, "成功后必须清掉历史失败说明")

	// 无历史失败时不该产生写放大
	clean := jiaotuPoolAccount(10)
	cleanStore := &fakeJiaotuStore{account: clean}
	clearJiaotuAccountError(context.Background(), cleanStore, clean)
	require.Empty(t, cleanStore.updates)
}

func TestJiaotuStateWriteFailuresAreSwallowed(t *testing.T) {
	// 读不到账号：不得 panic，也不得写入
	account := jiaotuPoolAccount(49)
	missing := &fakeJiaotuStore{}
	require.NotPanics(t, func() {
		markJiaotuAccountPointsExhausted(context.Background(), missing, account, 60)
	})
	require.Empty(t, missing.updates)

	// 写入失败：错误只记日志，内存状态不应被误标为已回写
	broken := &fakeJiaotuStore{account: jiaotuPoolAccount(49), updateErr: errors.New("db down")}
	require.NotPanics(t, func() {
		markJiaotuAccountExpired(context.Background(), broken, account, "boom")
	})
	require.Empty(t, broken.updates)

	// nil store / nil account / nil patch 都必须安全
	require.NotPanics(t, func() {
		markJiaotuAccountExpired(context.Background(), nil, account, "x")
		markJiaotuAccountPointsExhausted(context.Background(), &fakeJiaotuStore{}, nil, 1)
		patchJiaotuAccountState(context.Background(), &fakeJiaotuStore{}, account, nil, "x", false)
	})
}

func TestHandleJiaotuAccountErrorRoutesByKind(t *testing.T) {
	cases := []struct {
		name         string
		kind         JiaotuErrorKind
		wantExpired  bool
		wantPoints   bool
		wantCooldown bool
	}{
		{"auth 硬停", JiaotuErrAuth, true, false, false},
		{"积分不足进冷却", JiaotuErrInsufficientPoints, false, true, true},
		{"上游500 不动账号状态", JiaotuErrUpstream5xx, false, false, false},
		{"超时不动账号状态", JiaotuErrTimeout, false, false, false},
		{"空结果不动账号状态", JiaotuErrEmptyResult, false, false, false},
		{"参数错误不动账号状态", JiaotuErrInvalidRequest, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := jiaotuPoolAccount(49)
			store := &fakeJiaotuStore{account: account}
			HandleJiaotuAccountError(context.Background(), store, account,
				newJiaotuError("imageChat", tc.kind, 500, "x"))

			if !tc.wantExpired && !tc.wantPoints {
				require.Empty(t, store.updates, "瞬时/参数类故障不该惩罚账号")
				return
			}
			require.Len(t, store.updates, 1)
			updated := store.updates[0]
			if tc.wantExpired {
				require.Equal(t, JiaotuUpstreamStatusExpired, updated.JiaotuUpstreamStatus())
				require.False(t, updated.Schedulable)
			}
			if tc.wantPoints {
				require.Equal(t, 0, updated.JiaotuPoints())
				require.True(t, updated.Schedulable)
			}
			if tc.wantCooldown {
				require.NotNil(t, updated.TempUnschedulableUntil)
			}
		})
	}
}

func TestJiaotuPointsThresholdBlocksSelection(t *testing.T) {
	low := jiaotuPoolAccount(4)
	require.False(t, low.JiaotuHasEnoughPoints(5), "积分不够必须在选择阶段出局")
	require.True(t, low.JiaotuHasEnoughPoints(4))
	require.True(t, low.JiaotuUsableInPool())

	zeroed := jiaotuPoolAccount(0)
	require.False(t, zeroed.JiaotuHasEnoughPoints(1))

	expired := jiaotuPoolAccount(100)
	expired.Credentials[JiaotuCredentialUpstreamStatus] = JiaotuUpstreamStatusExpired
	require.False(t, expired.JiaotuUsableInPool(), "积分再多，token 失效也不可用")

	require.Equal(t, 5, jiaotuMinPointsForImage(&JiaotuImageRequest{
		Count:    2,
		Upstream: &JiaotuUpstreamModel{ID: 18, CostRadish: 2},
	}, 1), "门槛 = 单价 × 张数 + 余量")
}

func TestJiaotuMaxAccountSwitchesFromConfig(t *testing.T) {
	require.Equal(t, 0, (&OpenAIGatewayService{}).JiaotuMaxAccountSwitches(), "无配置不额外收紧")
	require.Equal(t, 0, (&OpenAIGatewayService{cfg: &config.Config{}}).JiaotuMaxAccountSwitches())
	require.Equal(t, 3, (&OpenAIGatewayService{cfg: &config.Config{
		Jiaotu: config.JiaotuConfig{MaxAttempts: 3},
	}}).JiaotuMaxAccountSwitches())
	require.Equal(t, 0, (&OpenAIGatewayService{cfg: &config.Config{
		Jiaotu: config.JiaotuConfig{MaxAttempts: -1},
	}}).JiaotuMaxAccountSwitches())
}

func TestJiaotuFailoverAlwaysExcludesCurrentAccount(t *testing.T) {
	// 椒图失败一律 RetryableOnSameAccount=false：同一号积分/风控状态不会瞬时变好，
	// 同号重试只会重复消耗时间并可能重复计费。
	for _, kind := range []JiaotuErrorKind{
		JiaotuErrUpstream5xx, JiaotuErrTimeout, JiaotuErrNetwork,
		JiaotuErrInsufficientPoints, JiaotuErrAuth, JiaotuErrEmptyResult, JiaotuErrBadResponse,
	} {
		err := jiaotuFailoverOrClientError(newJiaotuError("imageChat", kind, 500, "x"))
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover, string(kind))
		require.False(t, failover.RetryableOnSameAccount, string(kind))
	}
}

// --- 号池维护（积分刷新 / 签到）---

func jiaotuBillingServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/userBilling/page":
			require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			handler(w, r)
		case "/api/v1/sign/getSing":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "签到成功"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRefreshJiaotuAccountPointsWritesChanges(t *testing.T) {
	server := jiaotuBillingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "rows": []any{map[string]any{"availableCount": 77}}})
	})
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	account := jiaotuPoolAccount(49)
	account.Credentials[JiaotuCredentialLastError] = "上次失败"
	store := &fakeJiaotuStore{account: account}

	result, err := RefreshJiaotuAccountPoints(context.Background(), client, store, account)
	require.NoError(t, err)
	require.Equal(t, 77, result.Points)
	require.True(t, result.Updated)
	require.Len(t, store.updates, 1)
	updated := store.updates[0]
	require.Equal(t, 77, updated.JiaotuPoints())
	require.NotEmpty(t, updated.GetCredential(JiaotuCredentialPointsAt))
	_, hasError := updated.Credentials[JiaotuCredentialLastError]
	require.False(t, hasError, "积分恢复正常应清掉历史失败说明")
	require.NotNil(t, updated.JiaotuPointsAt())
}

func TestRefreshJiaotuAccountPointsSkipsNoopWrites(t *testing.T) {
	server := jiaotuBillingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"records": []any{map[string]any{"availableCount": "49"}}}})
	})
	defer server.Close()

	client := jiaotuTestClient(t, server.URL, 0)
	account := jiaotuPoolAccount(49)
	store := &fakeJiaotuStore{account: account}

	result, err := RefreshJiaotuAccountPoints(context.Background(), client, store, account)
	require.NoError(t, err)
	require.Equal(t, 49, result.Points)
	require.False(t, result.Updated, "积分未变不得写库（全池定时扫描会放大成无意义 UPDATE）")
	require.Empty(t, store.updates)
}

func TestRefreshJiaotuAccountPointsHandlesAuthAndNetwork(t *testing.T) {
	account := jiaotuPoolAccount(49)
	expiredStore := &fakeJiaotuStore{account: account}
	server401 := jiaotuBillingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	defer server401.Close()

	result, err := RefreshJiaotuAccountPoints(context.Background(), jiaotuTestClient(t, server401.URL, 0), expiredStore, account)
	require.NoError(t, err, "401 是可解释的账号状态，不作为维护错误上抛")
	require.Equal(t, JiaotuUpstreamStatusExpired, result.Status)
	require.Len(t, expiredStore.updates, 1)
	require.False(t, expiredStore.updates[0].Schedulable)

	networkStore := &fakeJiaotuStore{account: jiaotuPoolAccount(49)}
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	_, err = RefreshJiaotuAccountPoints(context.Background(), jiaotuTestClient(t, deadURL, 500*time.Millisecond), networkStore, jiaotuPoolAccount(49))
	require.Error(t, err, "网络故障必须上报")
	require.Empty(t, networkStore.updates, "网络故障不该顺手改账号状态")
}

func TestRefreshJiaotuAccountPointsGuardsInputs(t *testing.T) {
	_, err := RefreshJiaotuAccountPoints(context.Background(), nil, &fakeJiaotuStore{}, jiaotuPoolAccount(1))
	require.ErrorIs(t, err, ErrJiaotuNotMaintainable)

	_, err = RefreshJiaotuAccountPoints(context.Background(), nil, &fakeJiaotuStore{}, &Account{Platform: PlatformOpenAI})
	require.ErrorIs(t, err, ErrJiaotuNotMaintainable)

	// 缺 token 的号：不打上游，直接标失效
	store := &fakeJiaotuStore{account: &Account{ID: 3, Platform: PlatformJiaotu, Credentials: map[string]any{}}}
	result, err := RefreshJiaotuAccountPoints(context.Background(), NewJiaotuClient(JiaotuSettings{}), store, store.account)
	require.NoError(t, err)
	require.Equal(t, JiaotuUpstreamStatusExpired, result.Status)
	require.False(t, store.updates[0].Schedulable)
}

func TestJiaotuPoolMaintenanceAggregates(t *testing.T) {
	var hits int
	var mu sync.Mutex
	server := jiaotuBillingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		current := hits
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "rows": []any{map[string]any{"availableCount": 100 + current}}})
	})
	defer server.Close()

	accounts := []*Account{jiaotuPoolAccount(1), jiaotuPoolAccount(2), jiaotuPoolAccount(3)}
	store := &fakeJiaotuStore{account: accounts[0]}
	require.NotNil(t, store)
	svc := &OpenAIGatewayService{
		cfg:         &config.Config{Jiaotu: config.JiaotuConfig{APIBase: server.URL, MaxAttempts: 3}},
		jiaotuStore: &multiJiaotuStore{accounts: accounts},
	}

	summary := svc.JiaotuPoolMaintenance(context.Background(), accounts, JiaotuPoolMaintenanceOptions{RefreshPoints: true})
	require.Equal(t, 3, summary.Total)
	require.Equal(t, 3, summary.Refreshed, "三个号积分都变了")
	require.Equal(t, 0, summary.Failed)
	require.Len(t, summary.Items, 3)

	// 空列表与取消上下文都要安全
	require.Equal(t, 0, svc.JiaotuPoolMaintenance(context.Background(), nil, JiaotuPoolMaintenanceOptions{RefreshPoints: true}).Total)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := svc.JiaotuPoolMaintenance(cancelled, accounts, JiaotuPoolMaintenanceOptions{RefreshPoints: true})
	require.Equal(t, 0, stopped.Refreshed+stopped.Failed, "已取消的上下文不得继续派单")
}

// multiJiaotuStore 支持多账号的 fake，便于批量维护测试。
type multiJiaotuStore struct {
	accounts []*Account
	updates  int
}

func (m *multiJiaotuStore) GetByID(_ context.Context, id int64) (*Account, error) {
	for _, account := range m.accounts {
		if account.ID == id {
			clone := *account
			return &clone, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *multiJiaotuStore) Update(_ context.Context, account *Account) error {
	m.updates++
	for i, existing := range m.accounts {
		if existing.ID == account.ID {
			m.accounts[i] = account
		}
	}
	return nil
}

func TestJiaotuPoolMaintenanceSignInAggregates(t *testing.T) {
	server := jiaotuBillingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "rows": []any{map[string]any{"availableCount": 50}}})
	})
	defer server.Close()

	accounts := []*Account{jiaotuPoolAccount(1)}
	svc := &OpenAIGatewayService{
		cfg:         &config.Config{Jiaotu: config.JiaotuConfig{APIBase: server.URL}},
		jiaotuStore: &multiJiaotuStore{accounts: accounts},
	}
	summary := svc.JiaotuPoolMaintenance(context.Background(), accounts, JiaotuPoolMaintenanceOptions{
		RefreshPoints: true, SignIn: true, Concurrency: 1,
	})
	require.NotNil(t, summary.SignIn)
	require.Equal(t, 1, summary.SignIn.Attempted)
	require.Equal(t, 1, summary.SignIn.Success, "上游 code 200 + 签到成功 应计入成功")
}

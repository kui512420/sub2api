package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 椒图号池维护：积分刷新（E6）与每日签到（E7，可选）。
//
// 为什么要主动刷：sub2api 的选号门槛读的是 credentials["points"]，而积分会在上游
// 被消耗/签到/充值后变化。不刷新会出现两种坏情况：号池显示有钱但每次都 401/积分不足，
// 或号其实已充值却一直被封在冷却里。

// JiaotuPointsRefreshResult 单号刷新结果。
type JiaotuPointsRefreshResult struct {
	AccountID int64  `json:"account_id"`
	Name      string `json:"name,omitempty"`
	Points    int    `json:"points"`
	Status    string `json:"status"`
	Updated   bool   `json:"updated"`
	Message   string `json:"message,omitempty"`
}

// JiaotuPoolMaintenanceResult 批量刷新/签到的汇总。
type JiaotuPoolMaintenanceResult struct {
	Total     int                         `json:"total"`
	Refreshed int                         `json:"refreshed"`
	Unchanged int                         `json:"unchanged"`
	Failed    int                         `json:"failed"`
	Expired   int                         `json:"expired"`
	SignIn    *JiaotuSignInSummary        `json:"sign_in,omitempty"`
	Items     []JiaotuPointsRefreshResult `json:"items,omitempty"`
}

// JiaotuSignInSummary 每日签到汇总（免费积分来源，默认关闭）。
type JiaotuSignInSummary struct {
	Attempted int `json:"attempted"`
	Success   int `json:"success"`
	Already   int `json:"already"`
	Failed    int `json:"failed"`
}

// JiaotuSignInEnabledByConfig 报告 jiaotu.sign_in_enabled（维护请求未指定 sign_in 时的默认值）。
func JiaotuSignInEnabledByConfig(cfg *config.Config) bool {
	return cfg != nil && cfg.Jiaotu.SignInEnabled
}

// JiaotuPoolMaintenanceOptions 控制一次批量维护的行为。
type JiaotuPoolMaintenanceOptions struct {
	RefreshPoints bool
	SignIn        bool
	Concurrency   int
	Limit         int
}

const (
	jiaotuMaintenanceDefaultConcurrency = 5
	jiaotuMaintenanceCallTimeout        = 30 * time.Second
)

// ErrJiaotuNotMaintainable 目标账号不是可维护的椒图账号（非 jiaotu 平台/缺依赖）。
var ErrJiaotuNotMaintainable = errors.New("not a maintainable jiaotu account")

// RefreshJiaotuAccountPoints 查询单号积分并落库。
// 401/403 归类为鉴权失效：标 expired 并硬停（椒图 token 无 refresh 能力）。
func RefreshJiaotuAccountPoints(ctx context.Context, client *JiaotuClient, store JiaotuAccountStore, account *Account) (JiaotuPointsRefreshResult, error) {
	result := JiaotuPointsRefreshResult{AccountID: account.ID, Name: account.Name}
	if client == nil || store == nil || account == nil || !account.IsJiaotu() {
		result.Message = "不是可用的椒图账号"
		return result, ErrJiaotuNotMaintainable
	}
	token := account.JiaotuToken()
	if token == "" {
		markJiaotuAccountExpired(ctx, store, account, "缺少 token 凭据")
		result.Status = JiaotuUpstreamStatusExpired
		result.Message = "缺少 token 凭据"
		return result, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, jiaotuMaintenanceCallTimeout)
	defer cancel()

	points, err := client.RefreshPoints(callCtx, token, "")
	if err != nil {
		jerr, ok := IsJiaotuError(err)
		if ok && jerr.Kind == JiaotuErrAuth {
			markJiaotuAccountExpired(ctx, store, account, jerr.Message)
			result.Status = JiaotuUpstreamStatusExpired
			result.Message = jerr.Message
			return result, nil
		}
		result.Message = err.Error()
		return result, err
	}

	previous := account.JiaotuPoints()
	healthy := account.JiaotuUpstreamStatus() == JiaotuUpstreamStatusOK &&
		strings.TrimSpace(account.GetCredential(JiaotuCredentialLastError)) == ""
	if previous == points && healthy {
		// 积分未变且账号本就健康：不写库。全池定时扇描若每次都 UPDATE，
		// 会把一个免费查询接口变成 DB 写放大源。
		result.Points = points
		result.Status = JiaotuUpstreamStatusOK
		result.Message = "积分未变化，跳过写入"
		return result, nil
	}

	patchJiaotuAccountState(ctx, store, account, func(creds map[string]any) {
		creds[JiaotuCredentialPoints] = points
		creds[JiaotuCredentialPointsAt] = time.Now().UTC().Format(time.RFC3339)
		creds[JiaotuCredentialUpstreamStatus] = JiaotuUpstreamStatusOK
		creds[JiaotuCredentialLastError] = nil // 积分查询成功即视为健康，清掉历史失败说明
	}, "", false)
	result.Points = points
	result.Status = JiaotuUpstreamStatusOK
	result.Updated = true
	return result, nil
}

// JiaotuPoolMaintenance 对一批椒图账号执行刷新/签到（受控并发）。
func (s *OpenAIGatewayService) JiaotuPoolMaintenance(ctx context.Context, accounts []*Account, opts JiaotuPoolMaintenanceOptions) JiaotuPoolMaintenanceResult {
	if s == nil {
		return runJiaotuPoolMaintenance(ctx, nil, nil, accounts, opts)
	}
	return runJiaotuPoolMaintenance(ctx, s.jiaotuClient(), s.jiaotuAccountStore(), accounts, opts)
}

// runJiaotuPoolMaintenance 不依赖具体服务实例：方便账号探测服务/后台任务直接复用。
func runJiaotuPoolMaintenance(ctx context.Context, client *JiaotuClient, store JiaotuAccountStore, accounts []*Account, opts JiaotuPoolMaintenanceOptions) JiaotuPoolMaintenanceResult {
	summary := JiaotuPoolMaintenanceResult{Total: len(accounts)}
	if len(accounts) == 0 || client == nil || store == nil {
		return summary
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = jiaotuMaintenanceDefaultConcurrency
	}
	if concurrency > 10 {
		concurrency = 10 // 上游对并发敏感，且这些都是免费接口，无需更高
	}

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		sem   = make(chan struct{}, concurrency)
		items = make([]JiaotuPointsRefreshResult, 0, len(accounts))
		sign  *JiaotuSignInSummary
	)
	if opts.SignIn {
		sign = &JiaotuSignInSummary{}
	}
	summary.Items = items

	processed := 0
	for _, account := range accounts {
		if ctx.Err() != nil {
			break // 调用方已取消（如 admin 断电）：不再继续派单
		}
		if account == nil || !account.IsJiaotu() {
			continue
		}
		if opts.Limit > 0 && processed >= opts.Limit {
			break
		}
		processed++
		wg.Add(1)
		sem <- struct{}{}
		go func(account *Account) {
			defer wg.Done()
			defer func() { <-sem }()

			if opts.SignIn {
				jiaotuSignInOnce(ctx, client, store, account, sign, &mu)
			}
			if !opts.RefreshPoints {
				return
			}
			result, err := RefreshJiaotuAccountPoints(ctx, client, store, account)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				summary.Failed++
			case result.Status == JiaotuUpstreamStatusExpired:
				summary.Expired++
				summary.Failed++
			case result.Updated:
				summary.Refreshed++
			default:
				summary.Unchanged++
			}
			summary.Items = append(summary.Items, result)
		}(account)
	}
	wg.Wait()

	if sign != nil {
		summary.SignIn = sign
	}
	return summary
}

// jiaotuSignInOnce 每日签到（免费积分来源）。已签到视为成功且不重复计失败。
func jiaotuSignInOnce(ctx context.Context, client *JiaotuClient, store JiaotuAccountStore, account *Account, sign *JiaotuSignInSummary, mu *sync.Mutex) {
	token := account.JiaotuToken()
	if token == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, jiaotuMaintenanceCallTimeout)
	defer cancel()

	ok, message, err := client.SignIn(callCtx, token, "")
	mu.Lock()
	defer mu.Unlock()
	sign.Attempted++
	switch {
	case err != nil:
		sign.Failed++
		// 登录失效是签到最有价值的发现：标expired 并硬停，避免后续白派单。
		if jerr, ok := IsJiaotuError(err); ok && store != nil {
			if jerr.Kind == JiaotuErrAuth {
				markJiaotuAccountExpired(ctx, store, account, jerr.Message)
			}
		}
		logger.LegacyPrintf("service.jiaotu", "[Jiaotu] 签到失败 account=%s err=%v", account.JiaotuIdentityForLog(), err)
	case message == "已签到":
		sign.Already++
	case ok:
		sign.Success++
	default:
		sign.Failed++
	}
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// 椒图自动补号（自动注册新号）——从 kuikui cmd/kuikui/register.go 忠实搬迁。
//
// ⚠️ 本文件刻意**不接入任何请求热路径**：图片/视频选号失败只做换号与冷却，绝不自动注册。
// 原因：注册会真实消耗接码费用、并可能触发上游风控，必须由人显式批准后再启用。
// 开关：jiaotu.auto_register.enabled（默认 false）。关闭时所有入口立即返回错误，不发任何网络请求。
//
// 本轮（目标已确认）只搬代码：不做真实注册、不写专项测试用例，验收口径是
// `go build ./...` 与 `go vet ./...` 干净 + 开关默认关闭 + 无凭据进入被跟踪文件。

// jiaotuRegistrationConfig 是补号所需的配置快照（来自 jiaotu.auto_register.*）。
type jiaotuRegistrationConfig struct {
	Enabled          bool
	PoolTarget       int
	ProxyAPI         string
	FallbackInvite   string
	MinInterval      time.Duration
	Cooldown         time.Duration
	ProxyAttempts    int
	ConnectTimeout   time.Duration
	RequestTimeout   time.Duration
	CodeWaitTimeout  time.Duration
	CodePollInterval time.Duration
	HaoZhu           jiaotuHaoZhuConfig
	My531            jiaotuMy531Config
}

type jiaotuHaoZhuConfig struct {
	API    string
	User   string
	Pass   string
	Token  string
	SID    string
	Author string
	UID    string
}

type jiaotuMy531Config struct {
	API   string
	Token string
	User  string
	Pass  string
	PID   string
	Dev   string
}

const (
	jiaotuDefaultHaoZhuAPI    = "https://api.haozhuma.com"
	jiaotuDefaultMy531API     = "http://api.my531.com"
	jiaotuDefaultMy531PID     = "68420"
	jiaotuDefaultMy531Dev     = "taxin888"
	jiaotuDefaultHaoZhuAuthor = "adminzf"
	jiaotuDefaultInvite       = "7PQ8K"
	jiaotuMy531GetPhoneTries  = 10
	jiaotuMy531PhoneRetryGap  = 3 * time.Second
	jiaotuProxyProbeTimeout   = 8 * time.Second
)

// callJiaotu 只关心响应体的场景（注册链路里业务码自己判）。
func (c *JiaotuClient) callJiaotu(ctx context.Context, method, path, token string, body []byte, proxyURL string) ([]byte, error) {
	_, raw, err := c.doJSON(ctx, path, method, path, token, body, proxyURL)
	return raw, err
}

// jiaotuRegistrationConfigFrom 读取配置并补齐默认值。
// 注意：凭据只从配置/env 读取，绝不写入仓库任何被跟踪文件。
func jiaotuRegistrationConfigFrom(cfg *config.Config) jiaotuRegistrationConfig {
	settings := jiaotuRegistrationConfig{
		MinInterval:      4 * time.Second,
		Cooldown:         2 * time.Minute,
		ProxyAttempts:    3,
		ConnectTimeout:   15 * time.Second,
		RequestTimeout:   30 * time.Second,
		CodeWaitTimeout:  120 * time.Second,
		CodePollInterval: 5 * time.Second,
		FallbackInvite:   jiaotuDefaultInvite,
		HaoZhu: jiaotuHaoZhuConfig{
			API:    jiaotuDefaultHaoZhuAPI,
			Author: jiaotuDefaultHaoZhuAuthor,
		},
		My531: jiaotuMy531Config{
			API: jiaotuDefaultMy531API,
			PID: jiaotuDefaultMy531PID,
			Dev: jiaotuDefaultMy531Dev,
		},
	}
	if cfg == nil {
		return settings
	}
	section := cfg.Jiaotu.AutoRegister
	settings.Enabled = section.Enabled
	settings.PoolTarget = section.PoolTarget
	settings.ProxyAPI = strings.TrimSpace(section.ProxyAPI)
	if invite := strings.TrimSpace(section.FallbackInvite); invite != "" {
		settings.FallbackInvite = invite
	}
	settings.MinInterval = durationOrDefault(section.MinIntervalSeconds, settings.MinInterval)
	settings.Cooldown = durationOrDefault(section.CooldownSeconds, settings.Cooldown)
	if section.ProxyAttempts > 0 {
		settings.ProxyAttempts = section.ProxyAttempts
	}

	sms := section.SMS
	if api := strings.TrimSpace(sms.Haozhu.API); api != "" {
		settings.HaoZhu.API = normalizeJiaotuSMSEndpointBase(api, jiaotuDefaultHaoZhuAPI)
	}
	settings.HaoZhu.User = strings.TrimSpace(sms.Haozhu.User)
	settings.HaoZhu.Pass = strings.TrimSpace(sms.Haozhu.Pass)
	settings.HaoZhu.Token = strings.TrimSpace(sms.Haozhu.Token)
	settings.HaoZhu.SID = strings.TrimSpace(sms.Haozhu.SID)
	if author := strings.TrimSpace(sms.Haozhu.Author); author != "" {
		settings.HaoZhu.Author = author
	}
	settings.HaoZhu.UID = strings.TrimSpace(sms.Haozhu.UID)
	settings.CodePollInterval = durationOrDefault(sms.Haozhu.PollIntervalSeconds, settings.CodePollInterval)
	settings.CodeWaitTimeout = durationOrDefault(sms.Haozhu.WaitTimeoutSeconds, settings.CodeWaitTimeout)

	if api := strings.TrimSpace(sms.My531.API); api != "" {
		settings.My531.API = normalizeJiaotuSMSEndpointBase(api, jiaotuDefaultMy531API)
	}
	settings.My531.Token = strings.TrimSpace(sms.My531.Token)
	settings.My531.User = strings.TrimSpace(sms.My531.User)
	settings.My531.Pass = strings.TrimSpace(sms.My531.Pass)
	if pid := strings.TrimSpace(sms.My531.PID); pid != "" {
		settings.My531.PID = pid
	}
	if dev := strings.TrimSpace(sms.My531.Dev); dev != "" {
		settings.My531.Dev = dev
	}
	return settings
}

func normalizeJiaotuSMSEndpointBase(raw, fallback string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return fallback
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	// 允许运维直接填到 /sms/ 这一层，这里统一回到站点根。
	if strings.HasSuffix(base, "/sms") {
		base = strings.TrimSuffix(base, "/sms")
	}
	return strings.TrimRight(base, "/")
}

// jiaotuSMSProvider 抽象接码平台：豪猪与旧版 my531 两条实现。
type jiaotuSMSProvider interface {
	GetPhone(ctx context.Context) (string, error)
	MessageCode(ctx context.Context, phone string) (string, error)
	Release(ctx context.Context, phone string)
	Blacklist(ctx context.Context, phone string)
	Name() string
}

// ErrJiaotuAutoRegisterDisabled 补号未启用（默认状态）。
var ErrJiaotuAutoRegisterDisabled = errors.New("椒图自动补号未启用（jiaotu.auto_register.enabled=false）")

// ErrJiaotuNoSMSProvider 没配置任何接码平台。
var ErrJiaotuNoSMSProvider = errors.New("未配置接码平台：请填写 jiaotu.auto_register.sms.haozhu 或 my531")

// jiaotuRegistrar 承载注册流程的可变状态（单飞 + 风控闸门）。
type jiaotuRegistrar struct {
	cfg      jiaotuRegistrationConfig
	base     *JiaotuClient
	settings JiaotuSettings

	mu        sync.Mutex
	flight    *jiaotuRegisterFlight
	lastStart time.Time
	streakErr int
	coolUntil time.Time
	// inviteToken 是生成邀请码时可借用的已有号 token（可选）。
	inviteToken string
}

type jiaotuRegisterFlight struct {
	done    chan struct{}
	account JiaotuPoolAccount
	err     error
}

func newJiaotuRegistrar(cfg *config.Config, client *JiaotuClient) *jiaotuRegistrar {
	settings := JiaotuSettingsFromConfig(cfg)
	return &jiaotuRegistrar{
		cfg:      jiaotuRegistrationConfigFrom(cfg),
		base:     client,
		settings: settings,
	}
}

// RegisterJiaotuAccount 显式注册一个新椒图号并返回号池条目（不落库，由调用方决定持久化）。
//
// **默认即拒绝**：jiaotu.auto_register.enabled=false 时不发任何网络请求直接返回
// ErrJiaotuAutoRegisterDisabled，因此它不会被任何热路径意外触发；
// 本目标也未把它挂到任何 HTTP 端点上（用户已确认本轮只搬代码、不测试）。
func (s *OpenAIGatewayService) RegisterJiaotuAccount(ctx context.Context) (JiaotuPoolAccount, error) {
	if s == nil {
		return JiaotuPoolAccount{}, ErrJiaotuAutoRegisterDisabled
	}
	registrar := newJiaotuRegistrar(s.cfg, s.jiaotuClient())
	if !registrar.Enabled() {
		return JiaotuPoolAccount{}, ErrJiaotuAutoRegisterDisabled
	}
	return registrar.Register(ctx)
}

// jiaotuRegistrarForTest 暴露 registrar 供包内用例检查凭据判定（不新增对外 API）。
func (s *OpenAIGatewayService) jiaotuRegistrarForTest() *jiaotuRegistrar {
	if s == nil {
		return newJiaotuRegistrar(nil, nil)
	}
	return newJiaotuRegistrar(s.cfg, s.jiaotuClient())
}

// JiaotuAutoRegisterEnabled 报告补号开关状态（供管理页/日志展示）。
func (s *OpenAIGatewayService) JiaotuAutoRegisterEnabled() bool {
	if s == nil {
		return false
	}
	return newJiaotuRegistrar(s.cfg, s.jiaotuClient()).Enabled()
}

// Enabled 报告补号是否可用（纯读，无副作用）。
func (r *jiaotuRegistrar) Enabled() bool {
	if r == nil {
		return false
	}
	if !r.cfg.Enabled {
		return false
	}
	return r.smsConfigured() || r.my531Configured()
}

func (r *jiaotuRegistrar) smsConfigured() bool {
	hz := r.cfg.HaoZhu
	return hz.Token != "" || (hz.User != "" && hz.Pass != "")
}

func (r *jiaotuRegistrar) my531Configured() bool {
	my := r.cfg.My531
	return my.Token != "" || (my.User != "" && my.Pass != "")
}

// Register 注册一个新椒图号并返回号池条目（**不自动落库**：由调用方决定如何持久化，
// 避免未经批准就把第三方账号写进生产数据）。
// 并发多次调用会被合并成一次真实注册（对齐 kuikui registerJiaotu 的 single-flight）。
func (r *jiaotuRegistrar) Register(ctx context.Context) (JiaotuPoolAccount, error) {
	if r == nil || !r.cfg.Enabled {
		return JiaotuPoolAccount{}, ErrJiaotuAutoRegisterDisabled
	}

	r.mu.Lock()
	if current := r.flight; current != nil {
		r.mu.Unlock()
		select {
		case <-current.done:
			return current.account, current.err
		case <-ctx.Done():
			return JiaotuPoolAccount{}, ctx.Err()
		}
	}
	flight := &jiaotuRegisterFlight{done: make(chan struct{})}
	r.flight = flight
	r.mu.Unlock()

	flight.account, flight.err = r.registerOnce(ctx)

	r.mu.Lock()
	close(flight.done)
	r.flight = nil
	r.mu.Unlock()
	return flight.account, flight.err
}

// registerOnce 带代理轮换的一次完整注册：探测代理 → 注册 → 失败换代理，最多 ProxyAttempts 次。
func (r *jiaotuRegistrar) registerOnce(ctx context.Context) (JiaotuPoolAccount, error) {
	proxyEnabled := r.cfg.ProxyAPI != ""
	if err := r.riskGateBefore(ctx, proxyEnabled); err != nil {
		return JiaotuPoolAccount{}, err
	}
	if !proxyEnabled {
		account, err := r.registerAttempt(ctx, r.base, "")
		r.riskGateAfter(proxyEnabled, err)
		return account, err
	}

	var lastErr error
	for attempt := 0; attempt < r.cfg.ProxyAttempts; attempt++ {
		proxyURL, err := r.acquireProxy(ctx)
		if err == nil {
			if err = r.probeProxy(ctx, proxyURL); err == nil {
				account, registerErr := r.registerAttempt(ctx, r.base, proxyURL)
				if registerErr == nil {
					r.riskGateAfter(true, nil)
					return account, nil
				}
				err = registerErr
			}
			lastErr = err
		} else {
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("椒图自动注册失败")
		}
		if attempt+1 < r.cfg.ProxyAttempts {
			if waitErr := jiaotuWait(ctx, time.Duration(attempt+1)*2*time.Second); waitErr != nil {
				return JiaotuPoolAccount{}, waitErr
			}
		}
	}
	r.riskGateAfter(true, lastErr)
	return JiaotuPoolAccount{}, lastErr
}

// registerAttempt 单次注册：取邀请码 → 取号 → 发验证码 → 等码 → 登录 → 取资料 → 生成自己的邀请码。
func (r *jiaotuRegistrar) registerAttempt(ctx context.Context, client *JiaotuClient, proxyURL string) (JiaotuPoolAccount, error) {
	invite, err := r.registrationInvite(ctx, client, proxyURL)
	if err != nil {
		return JiaotuPoolAccount{}, err
	}

	provider, err := r.smsProvider()
	if err != nil {
		return JiaotuPoolAccount{}, err
	}
	phone, err := provider.GetPhone(ctx)
	if err != nil {
		return JiaotuPoolAccount{}, err
	}
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer releaseCancel()
	provider.Release(releaseCtx, phone)

	if _, raw, err := client.doJSON(ctx, "user/sendPhoneCode", http.MethodPost,
		"/api/v1/user/sendPhoneCode", "", mustJiaotuJSON(map[string]any{"phone": phone, "countryCode": "86"}), proxyURL); err != nil {
		return JiaotuPoolAccount{}, err
	} else if code, message := jiaotuBusinessError(raw); code != 0 {
		return JiaotuPoolAccount{}, fmt.Errorf("椒图发送验证码失败（上游 code %d）：%s", code, message)
	}

	code, err := provider.MessageCode(ctx, phone)
	if err != nil {
		if codeErr := ctx.Err(); codeErr == nil {
			blacklistCtx, blacklistCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer blacklistCancel()
			provider.Blacklist(blacklistCtx, phone)
		}
		return JiaotuPoolAccount{}, err
	}

	loginRaw, err := client.callJiaotu(ctx, http.MethodPost, "/api/v1/user/login", "",
		mustJiaotuJSON(map[string]any{"phone": phone, "code": strings.TrimSpace(code), "countryCode": "86", "inviteCode": invite}), proxyURL)
	if err != nil {
		return JiaotuPoolAccount{}, err
	}
	var login map[string]any
	_ = json.Unmarshal(loginRaw, &login)
	token := jiaotuTokenFromValue(login)
	if token == "" {
		if message := jiaotuResponseMessageFrom(login); message != "" {
			return JiaotuPoolAccount{}, fmt.Errorf("椒图登录未返回 token：%s", message)
		}
		return JiaotuPoolAccount{}, errors.New("椒图登录未返回 token")
	}

	account := JiaotuPoolAccount{
		PoolID: "jp-" + jiaotuRandomHex(8), Phone: phone, Token: token, InviteCode: invite,
		Status: JiaotuUpstreamStatusOK, Points: 0, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if infoRaw, infoErr := client.callJiaotu(ctx, http.MethodGet, "/api/v1/user/getInfo", token, nil, proxyURL); infoErr == nil {
		var info struct {
			Data map[string]any `json:"data"`
		}
		if json.Unmarshal(infoRaw, &info) == nil && info.Data != nil {
			account.NickName = strings.TrimSpace(jiaotuStringValue(info.Data["nickName"]))
			if userID := jiaotuStringValue(info.Data["userId"]); userID != "" {
				account.UserID = json.RawMessage(strconv.Quote(userID))
			}
		}
	}
	if inviteRaw, inviteErr := client.callJiaotu(ctx, http.MethodPost, "/api/v1/inviteUser/generate", token, nil, proxyURL); inviteErr == nil {
		var payload any
		if json.Unmarshal(inviteRaw, &payload) == nil {
			account.InviteOwn = jiaotuInviteCodeFromValue(payload)
		}
	}
	return account, nil
}

// smsProvider 优先豪猪 OpenAPI，未配置时回落旧版 my531。
func (r *jiaotuRegistrar) smsProvider() (jiaotuSMSProvider, error) {
	if r.smsConfigured() {
		return &jiaotuHaoZhuSMS{cfg: r.cfg.HaoZhu, timeouts: r.cfg}, nil
	}
	if r.my531Configured() {
		return &jiaotuMy531SMS{cfg: r.cfg.My531, timeouts: r.cfg}, nil
	}
	return nil, ErrJiaotuNoSMSProvider
}

// --- 风控闸门（对齐 kuikui registrationRiskBefore/After）---

func (r *jiaotuRegistrar) riskGateBefore(ctx context.Context, proxyEnabled bool) error {
	if !proxyEnabled {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now := time.Now(); now.Before(r.coolUntil) {
		return fmt.Errorf("椒图注册已进入冷却（剩余 %s），避免反复触发风控", r.coolUntil.Sub(now).Round(time.Second))
	}
	if !r.lastStart.IsZero() {
		if elapsed := time.Since(r.lastStart); elapsed < r.cfg.MinInterval {
			if err := jiaotuWait(ctx, r.cfg.MinInterval-elapsed); err != nil {
				return err
			}
		}
	}
	r.lastStart = time.Now()
	return nil
}

func (r *jiaotuRegistrar) riskGateAfter(proxyEnabled bool, err error) {
	if !proxyEnabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.streakErr, r.coolUntil = 0, time.Time{}
		return
	}
	r.streakErr++
	if r.streakErr >= r.cfg.ProxyAttempts {
		r.coolUntil = time.Now().Add(r.cfg.Cooldown)
		r.streakErr = 0
		logger.LegacyPrintf("service.jiaotu.register", "[Jiaotu] 连续注册失败 %d 次，冷却 %s：%v", r.cfg.ProxyAttempts, r.cfg.Cooldown, err)
	}
}

// --- 短效代理 ---

func (r *jiaotuRegistrar) acquireProxy(ctx context.Context) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, r.cfg.ProxyAPI, nil)
	if err != nil {
		return "", err
	}
	response, err := (&http.Client{Timeout: r.cfg.RequestTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("连不上代理接口：%w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if err != nil {
		return "", err
	}
	endpoint := jiaotuProxyEndpointFrom(string(raw))
	if endpoint == "" {
		return "", errors.New("代理接口没返回可用的 ip:port")
	}
	return "http://" + endpoint, nil
}

func jiaotuProxyEndpointFrom(raw string) string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return ""
	}
	if strings.ContainsAny(text, "{[") {
		var payload any
		if json.Unmarshal([]byte(text), &payload) == nil {
			text = jiaotuNestedString(payload, "ip", "proxy", "data", "result")
		}
	}
	for _, candidate := range strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';' || r == ' '
	}) {
		endpoint := strings.TrimPrefix(strings.TrimSpace(candidate), "http://")
		if host, port, err := net.SplitHostPort(endpoint); err == nil && host != "" && port != "" {
			return net.JoinHostPort(host, port)
		}
	}
	return ""
}

func (r *jiaotuRegistrar) probeProxy(ctx context.Context, proxyURL string) error {
	probeCtx, cancel := context.WithTimeout(ctx, jiaotuProxyProbeTimeout)
	defer cancel()
	_, _, err := r.base.doJSON(probeCtx, "proxy/probe", http.MethodGet, jiaotuPathModels, "", nil, proxyURL)
	if err != nil {
		// 能拿到上游的业务响应就说明代理通了；只有连不上才算探测失败。
		if proxyProbeAcceptable(err) {
			return nil
		}
		return err
	}
	return nil
}

// proxyProbeAcceptable 代理可用性以连通为准；上游业务错误说明已经打通。
func proxyProbeAcceptable(err error) bool {
	if jerr, ok := IsJiaotuError(err); ok {
		return jerr.Kind == JiaotuErrUpstream5xx || jerr.Kind == JiaotuErrUpstream4xx || jerr.Kind == JiaotuErrAuth
	}
	return false
}

// --- 邀请码 ---

// registrationInvite 复刻 kuikui 的优先级：号池里已有号的 inviteOwn > 兜底邀请码。
// 号池 inviteOwn 需要一个可用 token 作为来源，由 SetInviteSourceToken 显式注入；
// 未注入时用兜底邀请码（kuikui 空池同样回落到 ZHIYU_JIAOTU_INVITE 默认值）。
func (r *jiaotuRegistrar) registrationInvite(ctx context.Context, client *JiaotuClient, proxyURL string) (string, error) {
	r.mu.Lock()
	source := r.inviteToken
	r.mu.Unlock()
	if source != "" {
		if raw, err := client.callJiaotu(ctx, http.MethodPost, "/api/v1/inviteUser/generate", source, nil, proxyURL); err == nil {
			var payload any
			if json.Unmarshal(raw, &payload) == nil {
				if code := jiaotuInviteCodeFromValue(payload); code != "" {
					return code, nil
				}
			}
		}
	}
	if r.cfg.FallbackInvite != "" {
		return r.cfg.FallbackInvite, nil
	}
	return "", errors.New("没有可用邀请码")
}

// SetInviteSourceToken 注入「用哪个已有号的 token 去生成邀请码」。
// 未注入时注册会退到兜底邀请码，功能仍然可用。
func (r *jiaotuRegistrar) SetInviteSourceToken(token string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inviteToken = strings.TrimSpace(token)
}

// --- 豪猪 OpenAPI ---

type jiaotuHaoZhuSMS struct {
	cfg      jiaotuHaoZhuConfig
	timeouts jiaotuRegistrationConfig

	mu    sync.Mutex
	token string
	http  *http.Client
}

func (h *jiaotuHaoZhuSMS) Name() string { return "haozhu" }

func (h *jiaotuHaoZhuSMS) httpClient() *http.Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.http == nil {
		h.http = &http.Client{Timeout: h.timeouts.RequestTimeout}
	}
	return h.http
}

func (h *jiaotuHaoZhuSMS) request(ctx context.Context, api string, allowWait bool, params url.Values) (map[string]any, error) {
	query := url.Values{"api": {api}}
	for key, values := range params {
		for _, value := range values {
			if value != "" {
				query.Add(key, value)
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.cfg.API+"/sms/?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("豪猪接口地址无效：%w", err)
	}
	req.Header.Set("User-Agent", jiaotuUserAgent)
	response, err := h.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("豪猪接口请求失败：%w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("读取豪猪接口失败：%w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("豪猪接口返回 HTTP %d", response.StatusCode)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("豪猪接口返回非 JSON：%w", err)
	}
	code := strings.TrimSpace(jiaotuStringValue(payload["code"]))
	message := strings.TrimSpace(jiaotuStringValue(payload["msg"]))
	if code == "0" || code == "200" || (allowWait && code == "-1" && strings.Contains(message, "等待")) {
		return payload, nil
	}
	if message == "" {
		message = "未知错误"
	}
	return nil, fmt.Errorf("豪猪 %s 失败（code %s）：%s", api, code, message)
}

func (h *jiaotuHaoZhuSMS) ensureToken(ctx context.Context) error {
	if h.cfg.Token != "" {
		return nil // 直接给了 token，不必登录
	}
	if h.cfg.User == "" || h.cfg.Pass == "" {
		return ErrJiaotuNoSMSProvider
	}
	h.mu.Lock()
	cached := h.token
	h.mu.Unlock()
	if cached != "" {
		return nil
	}

	payload, err := h.request(ctx, "login", false, url.Values{"user": {h.cfg.User}, "pass": {h.cfg.Pass}})
	if err != nil {
		return err
	}
	token := jiaotuNestedString(payload, "token", "accessToken", "access_token")
	if token == "" {
		return errors.New("豪猪登录未返回 token")
	}
	h.mu.Lock()
	h.token = token
	h.mu.Unlock()
	return nil
}

func (h *jiaotuHaoZhuSMS) currentToken() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.token != "" {
		return h.token
	}
	return h.cfg.Token
}

func (h *jiaotuHaoZhuSMS) GetPhone(ctx context.Context) (string, error) {
	if err := h.ensureToken(ctx); err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 0; attempt < jiaotuMy531GetPhoneTries; attempt++ {
		payload, err := h.request(ctx, "getPhone", false, url.Values{
			"token": {h.currentToken()}, "sid": {h.cfg.SID}, "uid": {h.cfg.UID}, "author": {h.cfg.Author},
		})
		if err == nil {
			phone := jiaotuNestedString(payload, "phone", "mobile", "number")
			if phone == "" {
				phone = jiaotuNestedScalarString(payload)
			}
			if phone == "" {
				return "", errors.New("豪猪获取手机号失败：响应中没有手机号")
			}
			return phone, nil
		}
		lastErr = err
		// 豪猪用 code=-1 表示当前项目暂无可用号码：短暂重试；确定性错误立即返回。
		if !strings.Contains(err.Error(), "code -1") || attempt == jiaotuMy531GetPhoneTries-1 {
			return "", err
		}
		if waitErr := jiaotuWait(ctx, h.timeouts.CodePollInterval); waitErr != nil {
			return "", waitErr
		}
	}
	return "", lastErr
}

func (h *jiaotuHaoZhuSMS) MessageCode(ctx context.Context, phone string) (string, error) {
	if err := h.ensureToken(ctx); err != nil {
		return "", err
	}
	deadline := time.Now().Add(h.timeouts.CodeWaitTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		payload, err := h.request(ctx, "getMessage", true, url.Values{
			"token": {h.currentToken()}, "sid": {h.cfg.SID}, "phone": {phone},
		})
		if err == nil {
			if code := jiaotuSMSCodeFromPayload(payload); code != "" {
				return code, nil
			}
		} else {
			lastErr = err
		}
		if waitErr := jiaotuWait(ctx, h.timeouts.CodePollInterval); waitErr != nil {
			return "", waitErr
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("豪猪验证码获取超时：%w", lastErr)
	}
	return "", errors.New("豪猪验证码获取超时，请重试")
}

func (h *jiaotuHaoZhuSMS) Release(ctx context.Context, phone string) {
	_ = h.cancel(ctx, "cancelRecv", phone)
}

func (h *jiaotuHaoZhuSMS) Blacklist(ctx context.Context, phone string) {
	_ = h.cancel(ctx, "addBlacklist", phone)
}

func (h *jiaotuHaoZhuSMS) cancel(ctx context.Context, api, phone string) error {
	if phone == "" {
		return nil
	}
	if err := h.ensureToken(ctx); err != nil {
		return err
	}
	_, err := h.request(ctx, api, false, url.Values{"token": {h.currentToken()}, "sid": {h.cfg.SID}, "phone": {phone}})
	return err
}

// --- 旧版 my531（保留兼容，豪猪优先）---

type jiaotuMy531SMS struct {
	cfg      jiaotuMy531Config
	timeouts jiaotuRegistrationConfig

	mu    sync.Mutex
	token string
}

func (m *jiaotuMy531SMS) Name() string { return "my531" }

func (m *jiaotuMy531SMS) request(ctx context.Context, path string, values url.Values) (string, error) {
	values.Set("type", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.API+path+"?"+values.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", jiaotuUserAgent)
	response, err := (&http.Client{Timeout: m.timeouts.RequestTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("连不上 my531：%w", err)
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (m *jiaotuMy531SMS) ensureToken(ctx context.Context) error {
	if m.cfg.Token != "" {
		return nil
	}
	m.mu.Lock()
	cached := m.token
	m.mu.Unlock()
	if cached != "" {
		return nil
	}
	if m.cfg.User == "" || m.cfg.Pass == "" {
		return ErrJiaotuNoSMSProvider
	}

	raw, err := m.request(ctx, "/Login/", url.Values{"username": {m.cfg.User}, "password": {m.cfg.Pass}})
	if err != nil {
		return err
	}
	token := jiaotuMy531Value(raw, "token")
	if !jiaotuMy531Success(raw) || token == "" {
		return errors.New("my531 登录失败")
	}
	m.mu.Lock()
	m.token = token
	m.mu.Unlock()
	return nil
}

func (m *jiaotuMy531SMS) currentToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" {
		return m.token
	}
	return m.cfg.Token
}

func (m *jiaotuMy531SMS) GetPhone(ctx context.Context) (string, error) {
	if err := m.ensureToken(ctx); err != nil {
		return "", err
	}
	for attempt := 0; attempt < jiaotuMy531GetPhoneTries; attempt++ {
		raw, err := m.request(ctx, "/GetPhone/", url.Values{
			"token": {m.currentToken()}, "id": {m.cfg.PID}, "loop": {"1"},
		})
		if err != nil {
			return "", err
		}
		if phone := jiaotuMy531Value(raw, "phone", "number"); phone != "" {
			return phone, nil
		}
		if attempt < jiaotuMy531GetPhoneTries-1 {
			if waitErr := jiaotuWait(ctx, jiaotuMy531PhoneRetryGap); waitErr != nil {
				return "", waitErr
			}
		}
	}
	return "", errors.New("my531 获取手机号失败")
}

func (m *jiaotuMy531SMS) MessageCode(ctx context.Context, phone string) (string, error) {
	if err := m.ensureToken(ctx); err != nil {
		return "", err
	}
	params := url.Values{"token": {m.currentToken()}, "id": {m.cfg.PID}, "phone": {phone}, "dev": {m.cfg.Dev}}
	// 回收号上可能残留旧短信：先排空，否则第一条码会被椒图判为无效。
	_, _ = m.request(ctx, "/GetMsg/", params)

	deadline := time.Now().Add(m.timeouts.CodeWaitTimeout)
	for time.Now().Before(deadline) {
		raw, err := m.request(ctx, "/GetMsg/", params)
		if err == nil {
			if code := jiaotuSMSCode(jiaotuMy531Value(raw, "code", "msg", "sms", "text")); code != "" {
				return code, nil
			}
		}
		if waitErr := jiaotuWait(ctx, jiaotuMy531PhoneRetryGap); waitErr != nil {
			return "", waitErr
		}
	}
	return "", errors.New("验证码获取超时，请重试")
}

func (m *jiaotuMy531SMS) Release(ctx context.Context, phone string) {
	if phone == "" {
		return
	}
	if err := m.ensureToken(ctx); err != nil {
		return
	}
	_, _ = m.request(ctx, "/Cancel/", url.Values{"token": {m.currentToken()}, "id": {m.cfg.PID}, "phone": {phone}})
}

func (m *jiaotuMy531SMS) Blacklist(context.Context, string) {
	// my531 没有拉黑接口，退化为 Release。
}

// --- 解析辅助（对齐 kuikui register.go 的宽容解析）---

func jiaotuMy531Value(raw string, keys ...string) string {
	var object map[string]any
	if json.Unmarshal([]byte(raw), &object) == nil {
		switch value := object["data"].(type) {
		case string, float64, bool:
			if text := jiaotuStringValue(value); text != "" {
				return text
			}
		case map[string]any:
			for _, key := range keys {
				if text := jiaotuStringValue(value[key]); text != "" {
					return text
				}
			}
		}
		for _, key := range keys {
			// my531 顶层 code 是状态码（1=还在等），不能当成短信验证码。
			if key == "code" {
				continue
			}
			if text := jiaotuStringValue(object[key]); text != "" {
				return text
			}
		}
	}
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) > 1 && parts[0] == "1" {
		return parts[1]
	}
	return ""
}

func jiaotuMy531Success(raw string) bool {
	var object map[string]any
	if json.Unmarshal([]byte(raw), &object) == nil {
		// my531 用 stat 表达成败（true 或 "1"），code 是另一个语义，不能混用。
		value, ok := object["stat"]
		return ok && (value == true || jiaotuStringValue(value) == "1")
	}
	return strings.HasPrefix(strings.TrimSpace(raw), "1|")
}

// jiaotuSMSCode 从整段短信正文里抽验证码：优先 6 位数字，其次 4-8 位。
func jiaotuSMSCode(value string) string {
	var runs []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			runs = append(runs, current.String())
			current.Reset()
		}
	}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	for _, run := range runs {
		if len(run) == 6 {
			return run
		}
	}
	for _, run := range runs {
		if len(run) >= 4 && len(run) <= 8 {
			return run
		}
	}
	if len(runs) == 1 {
		return runs[0]
	}
	return ""
}

func jiaotuSMSCodeFromPayload(payload map[string]any) string {
	for _, key := range []string{"yzm", "sms", "msg", "message", "text"} {
		if code := jiaotuSMSCode(jiaotuNestedString(payload, key)); len(code) >= 4 {
			return code
		}
	}
	if text := jiaotuNestedScalarString(payload); text != "" {
		if code := jiaotuSMSCode(text); len(code) >= 4 {
			return code
		}
	}
	return ""
}

func jiaotuNestedString(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if text := strings.TrimSpace(jiaotuStringValue(typed[key])); text != "" {
				return text
			}
		}
		for _, key := range []string{"data", "result", "payload"} {
			if text := jiaotuNestedString(typed[key], keys...); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range typed {
			if text := jiaotuNestedString(item, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}

func jiaotuNestedScalarString(value any) string {
	switch typed := value.(type) {
	case string, float64, bool:
		return strings.TrimSpace(jiaotuStringValue(typed))
	case map[string]any:
		for _, key := range []string{"data", "result", "payload"} {
			if text := jiaotuNestedScalarString(typed[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range typed {
			if text := jiaotuNestedScalarString(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func jiaotuTokenFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"token", "accessToken", "access_token", "jwt"} {
			if token := jiaotuTokenFromValue(typed[key]); token != "" {
				return token
			}
		}
		for _, key := range []string{"data", "result", "payload", "user", "userinfo", "userInfo"} {
			if token := jiaotuTokenFromValue(typed[key]); token != "" {
				return token
			}
		}
	case []any:
		for _, item := range typed {
			if token := jiaotuTokenFromValue(item); token != "" {
				return token
			}
		}
	}
	return ""
}

func jiaotuInviteCodeFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		for _, key := range []string{"inviteCode", "invite_code", "code"} {
			if code := jiaotuInviteCodeFromValue(typed[key]); code != "" {
				return code
			}
		}
		for _, key := range []string{"data", "result", "payload"} {
			if code := jiaotuInviteCodeFromValue(typed[key]); code != "" {
				return code
			}
		}
	case []any:
		for _, item := range typed {
			if code := jiaotuInviteCodeFromValue(item); code != "" {
				return code
			}
		}
	}
	return ""
}

func jiaotuBusinessError(raw []byte) (int, string) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return 0, ""
	}
	code := jiaotuIntValue(root["code"])
	if code == 200 {
		code = 0
	}
	return code, jiaotuResponseMessageFrom(root)
}

func jiaotuResponseMessageFrom(root map[string]any) string {
	if root == nil {
		return ""
	}
	return jiaotuFirstNonEmpty(jiaotuStringValue(root["message"]), jiaotuStringValue(root["msg"]))
}

func mustJiaotuJSON(payload map[string]any) []byte {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

func jiaotuWait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

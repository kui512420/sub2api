package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 椒图号池导入：把 kuikui 侧 server/data/.jiaotu-pool.json（或同构 JSON / 纯文本 token 列表）
// 一次性灌成 sub2api 账号。协议与字段映射见 docs/JIAOTU_NATIVE_INTEGRATION.md §9。
//
// 安全约束：任何返回体与日志都不得包含 token 原文或完整手机号；去重按「号池 ID + token」双键。

const (
	// 椒图是「按号消耗积分」的共享上游，单号并发默认 1（移植契约 R5）。
	defaultJiaotuImportConcurrency = 1
	defaultJiaotuImportPriority    = 50
	maxJiaotuImportEntries         = 1000
	// 账号 status 的「停用」值：后端无专用常量，与 admin 契约（active|inactive|error）对齐。
	jiaotuImportStatusInactive = "inactive"
)

type JiaotuPoolImportRequest struct {
	Content                 string         `json:"content"`
	NamePrefix              *string        `json:"name_prefix"`
	Notes                   *string        `json:"notes"`
	GroupIDs                []int64        `json:"group_ids"`
	ProxyID                 *int64         `json:"proxy_id"`
	Concurrency             *int           `json:"concurrency"`
	Priority                *int           `json:"priority"`
	RateMultiplier          *float64       `json:"rate_multiplier"`
	LoadFactor              *int           `json:"load_factor"`
	CredentialExtras        map[string]any `json:"credential_extras"`
	UpdateExisting          *bool          `json:"update_existing"`
	SkipDefaultGroupBind    *bool          `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool          `json:"confirm_mixed_channel_risk"`
}

type JiaotuPoolImportResult struct {
	Total      int                       `json:"total"`
	Created    int                       `json:"created"`
	Updated    int                       `json:"updated"`
	Skipped    int                       `json:"skipped"`
	Failed     int                       `json:"failed"`
	Ready      int                       `json:"ready"`
	Unusable   int                       `json:"unusable"`
	TargetSize int                       `json:"target_size,omitempty"`
	Items      []JiaotuPoolImportItem    `json:"items,omitempty"`
	Errors     []JiaotuPoolImportMessage `json:"errors,omitempty"`
	Warnings   []JiaotuPoolImportMessage `json:"warnings,omitempty"`
}

type JiaotuPoolImportItem struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action"`
	AccountID int64  `json:"account_id,omitempty"`
	Points    int    `json:"points,omitempty"`
	Status    string `json:"status,omitempty"`
	Message   string `json:"message,omitempty"`
}

type JiaotuPoolImportMessage struct {
	Index   int    `json:"index"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// ImportJiaotuPool POST /api/v1/admin/accounts/import/jiaotu-pool
func (h *AccountHandler) ImportJiaotuPool(c *gin.Context) {
	var req JiaotuPoolImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency != nil && *req.Concurrency < 0 {
		response.BadRequest(c, "concurrency must be >= 0")
		return
	}
	if req.Priority != nil && *req.Priority < 0 {
		response.BadRequest(c, "priority must be >= 0")
		return
	}
	if req.RateMultiplier != nil && *req.RateMultiplier < 0 {
		response.BadRequest(c, "rate_multiplier must be >= 0")
		return
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		response.BadRequest(c, "load_factor must be <= 10000")
		return
	}

	entries, err := service.ParseJiaotuPoolInput(req.Content)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(entries) > maxJiaotuImportEntries {
		response.BadRequest(c, fmt.Sprintf("单次最多导入 %d 个椒图账号，当前 %d 个", maxJiaotuImportEntries, len(entries)))
		return
	}

	executeAdminIdempotentJSON(c, "admin.accounts.import_jiaotu_pool", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.importJiaotuPool(ctx, req, entries)
	})
}

func (h *AccountHandler) importJiaotuPool(ctx context.Context, req JiaotuPoolImportRequest, entries []service.JiaotuPoolAccount) (JiaotuPoolImportResult, error) {
	result := JiaotuPoolImportResult{
		Total:      len(entries),
		Items:      make([]JiaotuPoolImportItem, 0, len(entries)),
		TargetSize: jiaotuImportTargetSize(req.Content),
	}

	existing, err := h.listAccountsFiltered(ctx, service.PlatformJiaotu, "", "", "", 0, "", "created_at", "desc")
	if err != nil {
		return result, err
	}
	index := buildJiaotuAccountIndex(existing)

	updateExisting := true
	if req.UpdateExisting != nil {
		updateExisting = *req.UpdateExisting
	}
	concurrency := defaultJiaotuImportConcurrency
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := defaultJiaotuImportPriority
	if req.Priority != nil {
		priority = *req.Priority
	}
	skipDefaultGroupBind := req.SkipDefaultGroupBind != nil && *req.SkipDefaultGroupBind
	skipMixedChannelCheck := req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk
	namePrefix := strings.TrimSpace(derefString(req.NamePrefix))

	for i, entry := range entries {
		creds := service.BuildJiaotuCredentials(entry)
		for key, value := range req.CredentialExtras {
			if key == service.JiaotuCredentialToken || key == service.JiaotuCredentialPoolID {
				continue // 不允许用 extras 覆盖身份主键
			}
			if _, exists := creds[key]; !exists {
				creds[key] = value
			}
		}
		name := service.JiaotuAccountName(entry, i)
		if namePrefix != "" {
			name = namePrefix + " " + name
		}
		upstreamStatus := entry.NormalizeStatus()
		usable := upstreamStatus == service.JiaotuUpstreamStatusOK && entry.Token != ""
		if usable {
			result.Ready++
		} else {
			result.Unusable++
		}

		matched := index.Find(entry)
		if matched != nil && !updateExisting {
			result.Skipped++
			result.Items = append(result.Items, JiaotuPoolImportItem{
				Index: i, Name: name, Action: "skipped", AccountID: matched.ID,
				Points: matched.JiaotuPoints(), Status: matched.JiaotuUpstreamStatus(),
				Message: "已存在同 identity 的椒图账号（update_existing=false，跳过）",
			})
			continue
		}

		if matched != nil {
			merged := service.MergeJiaotuCredentials(matched.Credentials, creds)
			input := &service.UpdateAccountInput{Credentials: merged}
			if !usable && matched.JiaotuUpstreamStatus() == service.JiaotuUpstreamStatusOK {
				// 只在下架动作上写 Status；管理员手工停用的账号不被重新启用。
				input.Status = jiaotuImportStatusInactive
			}
			if req.Concurrency != nil || req.Priority != nil || req.RateMultiplier != nil || req.LoadFactor != nil {
				input.Concurrency = req.Concurrency
				input.Priority = req.Priority
				input.RateMultiplier = req.RateMultiplier
				input.LoadFactor = req.LoadFactor
			}
			if req.ProxyID != nil {
				input.ProxyID = req.ProxyID
			}
			if len(req.GroupIDs) > 0 {
				groupIDs := append([]int64(nil), req.GroupIDs...)
				input.GroupIDs = &groupIDs
				input.SkipMixedChannelCheck = skipMixedChannelCheck
			}
			updated, updateErr := h.adminService.UpdateAccount(ctx, matched.ID, input)
			if updateErr != nil {
				result.Failed++
				result.Errors = append(result.Errors, JiaotuPoolImportMessage{Index: i, Name: name, Message: updateErr.Error()})
				result.Items = append(result.Items, JiaotuPoolImportItem{Index: i, Name: name, Action: "failed", Message: updateErr.Error()})
				continue
			}
			result.Updated++
			accountID := matched.ID
			if updated != nil {
				accountID = updated.ID
				index.Add(*updated)
			}
			result.Items = append(result.Items, JiaotuPoolImportItem{
				Index: i, Name: name, Action: "updated", AccountID: accountID,
				Points: entry.Points, Status: upstreamStatus,
			})
			continue
		}
		account, createErr := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
			Name:                  name,
			Notes:                 req.Notes,
			Platform:              service.PlatformJiaotu,
			Type:                  service.AccountTypeAPIKey,
			Credentials:           creds,
			ProxyID:               req.ProxyID,
			Concurrency:           concurrency,
			Priority:              priority,
			RateMultiplier:        req.RateMultiplier,
			LoadFactor:            req.LoadFactor,
			GroupIDs:              req.GroupIDs,
			SkipDefaultGroupBind:  skipDefaultGroupBind,
			SkipMixedChannelCheck: skipMixedChannelCheck,
		})
		if createErr != nil {
			result.Failed++
			result.Errors = append(result.Errors, JiaotuPoolImportMessage{Index: i, Name: name, Message: createErr.Error()})
			result.Items = append(result.Items, JiaotuPoolImportItem{Index: i, Name: name, Action: "failed", Message: createErr.Error()})
			continue
		}
		if account != nil {
			index.Add(*account)
			if !usable {
				// 上游状态非 ok：建完立即下架，避免调度器挑到坏号
				if _, statusErr := h.adminService.UpdateAccount(ctx, account.ID, &service.UpdateAccountInput{Status: jiaotuImportStatusInactive}); statusErr != nil {
					result.Warnings = append(result.Warnings, JiaotuPoolImportMessage{Index: i, Name: name, Message: "账号已导入但下架失败：" + statusErr.Error()})
				}
			}
		}
		result.Created++
		var accountID int64
		if account != nil {
			accountID = account.ID
		}
		result.Items = append(result.Items, JiaotuPoolImportItem{
			Index: i, Name: name, Action: "created", AccountID: accountID,
			Points: entry.Points, Status: upstreamStatus,
		})
	}

	return result, nil
}

// jiaotuAccountIndex 按「号池 ID」和「token」双键索引已有账号，保证重复导入不产生第二份账号。
type jiaotuAccountIndex struct {
	byPoolID map[string]service.Account
	byToken  map[string]service.Account
}

func buildJiaotuAccountIndex(accounts []service.Account) *jiaotuAccountIndex {
	index := &jiaotuAccountIndex{
		byPoolID: make(map[string]service.Account, len(accounts)),
		byToken:  make(map[string]service.Account, len(accounts)),
	}
	for _, account := range accounts {
		index.Add(account)
	}
	return index
}

func (i *jiaotuAccountIndex) Add(account service.Account) {
	if !account.IsJiaotu() {
		return
	}
	if poolID := account.JiaotuPoolID(); poolID != "" {
		if _, exists := i.byPoolID[poolID]; !exists {
			i.byPoolID[poolID] = account
		}
	}
	if token := account.JiaotuToken(); token != "" {
		if _, exists := i.byToken[token]; !exists {
			i.byToken[token] = account
		}
	}
}

// Find 按「号池 ID → token」优先级返回匹配到的已有账号，无匹配返回 nil。
func (i *jiaotuAccountIndex) Find(entry service.JiaotuPoolAccount) *service.Account {
	if poolID := strings.TrimSpace(entry.PoolID); poolID != "" {
		if account, ok := i.byPoolID[poolID]; ok {
			found := account
			return &found
		}
	}
	if token := strings.TrimSpace(entry.Token); token != "" {
		if account, ok := i.byToken[token]; ok {
			found := account
			return &found
		}
	}
	return nil
}

// JiaotuPoolMaintenance POST /api/v1/admin/accounts/jiaotu/maintenance
// 批量维护椒图号池：刷新积分（默认）与可选的每日签到。均为免费接口。
func (h *AccountHandler) JiaotuPoolMaintenance(c *gin.Context) {
	var req JiaotuPoolMaintenanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if req.Concurrency < 0 || req.Concurrency > 10 {
		response.BadRequest(c, "concurrency must be between 0 and 10")
		return
	}
	if req.Limit < 0 {
		response.BadRequest(c, "limit must be >= 0")
		return
	}
	if h.accountTestService == nil {
		response.InternalError(c, "account test service unavailable")
		return
	}
	opts := service.JiaotuPoolMaintenanceOptions{
		RefreshPoints: req.RefreshPoints == nil || *req.RefreshPoints,
		SignIn:        req.SignIn != nil && *req.SignIn,
		Concurrency:   req.Concurrency,
		Limit:         req.Limit,
	}
	// 未显式指定时回落到 jiaotu.sign_in_enabled（运维可以只在配置里打开每日签到）。
	if req.SignIn == nil {
		opts.SignIn = h.accountTestService.JiaotuSignInEnabled()
	}

	summary, err := h.accountTestService.JiaotuPoolMaintenance(c.Request.Context(), opts)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, summary)
}

type JiaotuPoolMaintenanceRequest struct {
	RefreshPoints *bool `json:"refresh_points"`
	// SignIn 未提供时回落到 jiaotu.sign_in_enabled；显式 true/false 优先。
	SignIn      *bool `json:"sign_in"`
	Concurrency int   `json:"concurrency"`
	Limit       int   `json:"limit"`
}

func jiaotuImportTargetSize(content string) int {
	if size, ok := service.JiaotuPoolTargetSize(content); ok {
		return size
	}
	return 0
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

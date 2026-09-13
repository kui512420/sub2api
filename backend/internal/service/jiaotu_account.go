package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 椒图（Jiaotu）账号建模。
//
// 一个 sub2api 账号 = 一个椒图号（上游 token + 积分）。协议细节见
// docs/JIAOTU_NATIVE_INTEGRATION.md；本文件只负责「平台识别 + credentials 读写 + 号池导入解析」，
// 上游调用（imageChat / 参考图上传 / 视频）在 jiaotu_service.go。

// credentials 键名。注意：不得使用 password/sso/cookie 等会被
// SanitizeStoredCredentials（credentials_sanitize.go）剥掉的键。
const (
	JiaotuCredentialToken          = "token"
	JiaotuCredentialPhone          = "phone"
	JiaotuCredentialUserID         = "user_id"
	JiaotuCredentialPoolID         = "jiaotu_id" // 号池原始 id，如 jp-9；导入去重键
	JiaotuCredentialNickName       = "nick_name"
	JiaotuCredentialInviteOwn      = "invite_own" // 本号邀请码，补号时优先来源
	JiaotuCredentialInviteCode     = "invite_code"
	JiaotuCredentialPoints         = "points"
	JiaotuCredentialPointsAt       = "points_at"
	JiaotuCredentialUpstreamStatus = "jiaotu_status" // 上游语义：ok / expired / error
	JiaotuCredentialLastError      = "last_error"
	JiaotuCredentialImportedAt     = "imported_at"
)

// 上游账号状态（号池 status 字段）。
const (
	JiaotuUpstreamStatusOK      = "ok"
	JiaotuUpstreamStatusExpired = "expired"
	JiaotuUpstreamStatusError   = "error"
)

// IsJiaotu 判断账号是否走椒图上游。
func (a *Account) IsJiaotu() bool {
	return a != nil && a.Platform == PlatformJiaotu
}

// JiaotuToken 返回上游 Bearer token（容忍误带 "Bearer " 前缀的历史数据）。
func (a *Account) JiaotuToken() string {
	if !a.IsJiaotu() {
		return ""
	}
	return trimJiaotuBearer(a.GetCredential(JiaotuCredentialToken))
}

// JiaotuPoolID 返回号池原始 ID（如 jp-9）；手工录入的账号可能为空。
func (a *Account) JiaotuPoolID() string {
	if !a.IsJiaotu() {
		return ""
	}
	return strings.TrimSpace(a.GetCredential(JiaotuCredentialPoolID))
}

// JiaotuUpstreamStatus 返回上游语义状态，缺省按 ok 处理（手工录入通常只填 token）。
func (a *Account) JiaotuUpstreamStatus() string {
	if !a.IsJiaotu() {
		return ""
	}
	status := strings.ToLower(strings.TrimSpace(a.GetCredential(JiaotuCredentialUpstreamStatus)))
	if status == "" {
		return JiaotuUpstreamStatusOK
	}
	return status
}

// JiaotuPoints 返回最近一次刷新到的积分；解析失败按 0。
func (a *Account) JiaotuPoints() int {
	if !a.IsJiaotu() {
		return 0
	}
	raw := strings.TrimSpace(a.GetCredential(JiaotuCredentialPoints))
	if raw == "" {
		return 0
	}
	// GetCredential 可能给出 "49" 或 "49.000000"（float64 转写）
	if n, err := strconv.ParseFloat(raw, 64); err == nil && n > 0 {
		return int(n)
	}
	return 0
}

// JiaotuPointsAt 返回积分最后更新时间；无则 nil。
func (a *Account) JiaotuPointsAt() *time.Time {
	if !a.IsJiaotu() {
		return nil
	}
	return parseJiaotuTime(a.GetCredential(JiaotuCredentialPointsAt))
}

// JiaotuUsableInPool 判断该号是否具备「可被椒图选号逻辑挑中」的基本条件：
// 有 token 且上游状态为 ok。调度器自身的 schedulable/冷却判定在此之上。
func (a *Account) JiaotuUsableInPool() bool {
	return a.JiaotuToken() != "" && a.JiaotuUpstreamStatus() == JiaotuUpstreamStatusOK
}

// JiaotuHasEnoughPoints 判断积分是否够本次调用的最低消耗。
// minPoints<=0 时视为不限制（例如查模型列表这类免费请求）。
func (a *Account) JiaotuHasEnoughPoints(minPoints int) bool {
	if minPoints <= 0 {
		return true
	}
	return a.JiaotuPoints() >= minPoints
}

// JiaotuIdentityForLog 返回可安全写日志的身份标识（手机号脱敏，不含 token）。
func (a *Account) JiaotuIdentityForLog() string {
	if !a.IsJiaotu() {
		return ""
	}
	poolID := a.JiaotuPoolID()
	if poolID == "" {
		poolID = fmt.Sprintf("#%d", a.ID)
	}
	if phone := MaskJiaotuPhone(a.GetCredential(JiaotuCredentialPhone)); phone != "" {
		return poolID + "(" + phone + ")"
	}
	return poolID
}

// MaskJiaotuPhone 脱敏手机号：11 位保留前 3 后 4，其余整体打星。
func MaskJiaotuPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}
	if len(phone) == 11 {
		return phone[:3] + "****" + phone[7:]
	}
	if len(phone) <= 2 {
		return "**"
	}
	return phone[:1] + "****" + phone[len(phone)-1:]
}

// JiaotuPoolAccount 椒图号池条目，兼容 kuikui server/data/.jiaotu-pool.json 与
// GET /api/jiaotu/status 的字段命名。
type JiaotuPoolAccount struct {
	PoolID     string          `json:"id"`
	Phone      string          `json:"phone"`
	Token      string          `json:"token"`
	UserID     json.RawMessage `json:"userId,omitempty"`
	NickName   string          `json:"nickName"`
	InviteCode string          `json:"inviteCode,omitempty"`
	InviteOwn  string          `json:"inviteOwn,omitempty"`
	Points     int             `json:"points"`
	Status     string          `json:"status"`
	LastError  string          `json:"lastError,omitempty"`
	PointsAt   string          `json:"pointsAt,omitempty"`
	CreatedAt  string          `json:"createdAt,omitempty"`
	LastUsedAt string          `json:"lastUsedAt,omitempty"`
	AuthErrors int             `json:"authErrors,omitempty"`
}

// NormalizeStatus 归一化上游状态：空或未知值按 ok（与 kuikui 号池默认一致）。
func (e JiaotuPoolAccount) NormalizeStatus() string {
	switch strings.ToLower(strings.TrimSpace(e.Status)) {
	case "":
		return JiaotuUpstreamStatusOK
	case JiaotuUpstreamStatusExpired, "auth_failed":
		return JiaotuUpstreamStatusExpired
	case JiaotuUpstreamStatusError:
		return JiaotuUpstreamStatusError
	default:
		return e.Status
	}
}

// IdentityKey 导入去重键：优先号池 ID，其次 token。
func (e JiaotuPoolAccount) IdentityKey() string {
	if id := strings.TrimSpace(e.PoolID); id != "" {
		return "pool:" + id
	}
	if token := trimJiaotuBearer(e.Token); token != "" {
		return "token:" + sha256Hex(token)
	}
	return ""
}

// ParseJiaotuPoolInput 解析管理页粘贴的号池内容，容忍四种形态：
//  1. 完整号池对象 {"accounts":[...], "cursor":1, "targetSize":1}
//  2. 带包装的状态响应 {"code":0,"data":{"accounts":[...]}} / {"data":[...]}
//  3. 裸数组 [...]
//  4. 纯文本行：token / phone,token / jiaotuId,phone,token（每行一条，也接受单条 JSON 对象）
//
// 返回去重后的条目；全部无效时返回错误。
func ParseJiaotuPoolInput(content string) ([]JiaotuPoolAccount, error) {
	raw := strings.TrimSpace(content)
	if raw == "" {
		return nil, fmt.Errorf("椒图号池内容为空")
	}

	if entries, ok := parseJiaotuPoolJSON(raw); ok {
		return dedupeJiaotuPoolEntries(entries)
	}

	// 逐行文本：允许整体不是合法 JSON（例如从表格里粘贴的 token 列表）
	var entries []JiaotuPoolAccount
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "{") {
			var entry JiaotuPoolAccount
			if err := json.Unmarshal([]byte(line), &entry); err == nil && trimJiaotuBearer(entry.Token) != "" {
				entries = append(entries, entry)
				continue
			}
		}
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r == ',' || r == '\t' || r == ' ' || r == ';'
		})
		entry := JiaotuPoolAccount{Status: JiaotuUpstreamStatusOK}
		switch len(fields) {
		case 1:
			entry.Token = fields[0]
		case 2:
			entry.Phone, entry.Token = fields[0], fields[1]
		case 3:
			entry.PoolID, entry.Phone, entry.Token = fields[0], fields[1], fields[2]
		default:
			// 字段数不对（多为粘贴了无关文本）→ 不当作 token，避免静默导入脏数据
			continue
		}
		if trimJiaotuBearer(entry.Token) == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return dedupeJiaotuPoolEntries(entries)
}

func parseJiaotuPoolJSON(raw string) ([]JiaotuPoolAccount, bool) {
	// 1) 号池对象 / 状态包装对象
	var wrapper struct {
		Code     any                 `json:"code"`
		Accounts []JiaotuPoolAccount `json:"accounts"`
		Data     json.RawMessage     `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		if len(wrapper.Accounts) > 0 {
			return wrapper.Accounts, true
		}
		if len(wrapper.Data) > 0 {
			// data 可能是数组，也可能是 {accounts:[...]}
			var dataAccounts struct {
				Accounts []JiaotuPoolAccount `json:"accounts"`
			}
			if err := json.Unmarshal(wrapper.Data, &dataAccounts); err == nil && len(dataAccounts.Accounts) > 0 {
				return dataAccounts.Accounts, true
			}
			var dataArray []JiaotuPoolAccount
			if err := json.Unmarshal(wrapper.Data, &dataArray); err == nil && len(dataArray) > 0 {
				return dataArray, true
			}
		}
	}
	// 2) 裸数组
	var array []JiaotuPoolAccount
	if err := json.Unmarshal([]byte(raw), &array); err == nil && len(array) > 0 {
		return array, true
	}
	return nil, false
}

func dedupeJiaotuPoolEntries(entries []JiaotuPoolAccount) ([]JiaotuPoolAccount, error) {
	out := make([]JiaotuPoolAccount, 0, len(entries))
	seenPoolID := make(map[string]struct{}, len(entries))
	seenToken := make(map[string]struct{}, len(entries))
	invalid := 0
	for _, entry := range entries {
		entry.Token = trimJiaotuBearer(entry.Token)
		entry.PoolID = strings.TrimSpace(entry.PoolID)
		entry.Phone = strings.TrimSpace(entry.Phone)
		if entry.Token == "" {
			invalid++
			continue
		}
		// 去重同时看 poolID 与 token：同一 token 带/不带 id 两次出现时不能建两个账号。
		if entry.PoolID != "" {
			if _, dup := seenPoolID[entry.PoolID]; dup {
				continue
			}
		}
		if _, dup := seenToken[entry.Token]; dup {
			continue
		}
		if entry.PoolID != "" {
			seenPoolID[entry.PoolID] = struct{}{}
		}
		seenToken[entry.Token] = struct{}{}
		out = append(out, entry)
	}
	if len(out) == 0 {
		if invalid > 0 {
			return nil, fmt.Errorf("椒图号池内容无效：%d 条均缺少 token", invalid)
		}
		return nil, fmt.Errorf("椒图号池内容里没有可导入的账号")
	}
	return out, nil
}

// JiaotuPoolTargetSize 从号池内容里读取 targetSize（若存在），用于同步 jiaotu.pool_target。
func JiaotuPoolTargetSize(content string) (int, bool) {
	var wrapper struct {
		TargetSize int `json:"targetSize"`
		Data       struct {
			TargetSize int `json:"targetSize"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &wrapper); err != nil {
		return 0, false
	}
	if wrapper.TargetSize > 0 {
		return wrapper.TargetSize, true
	}
	if wrapper.Data.TargetSize > 0 {
		return wrapper.Data.TargetSize, true
	}
	return 0, false
}

// BuildJiaotuCredentials 由号池条目生成账号 credentials。
func BuildJiaotuCredentials(entry JiaotuPoolAccount) map[string]any {
	creds := map[string]any{
		JiaotuCredentialToken:          entry.Token,
		JiaotuCredentialUpstreamStatus: entry.NormalizeStatus(),
		JiaotuCredentialPoints:         entry.Points,
		JiaotuCredentialImportedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if creds[JiaotuCredentialPoints].(int) < 0 {
		creds[JiaotuCredentialPoints] = 0
	}
	if poolID := strings.TrimSpace(entry.PoolID); poolID != "" {
		creds[JiaotuCredentialPoolID] = poolID
	}
	if phone := strings.TrimSpace(entry.Phone); phone != "" {
		creds[JiaotuCredentialPhone] = phone
	}
	if nick := strings.TrimSpace(entry.NickName); nick != "" {
		creds[JiaotuCredentialNickName] = nick
	}
	if invite := strings.TrimSpace(entry.InviteOwn); invite != "" {
		creds[JiaotuCredentialInviteOwn] = invite
	}
	if invite := strings.TrimSpace(entry.InviteCode); invite != "" {
		creds[JiaotuCredentialInviteCode] = invite
	}
	if lastErr := strings.TrimSpace(entry.LastError); lastErr != "" {
		creds[JiaotuCredentialLastError] = lastErr
	}
	if at := strings.TrimSpace(entry.PointsAt); at != "" {
		creds[JiaotuCredentialPointsAt] = at
	}
	if userID := strings.TrimSpace(string(entry.UserID)); userID != "" && userID != "null" {
		var value any
		if err := json.Unmarshal(entry.UserID, &value); err == nil && value != nil {
			creds[JiaotuCredentialUserID] = value
		}
	}
	return creds
}

// JiaotuAccountName 生成展示名：优先「椒图 <号池ID>」，其次昵称，最后手机号脱敏。
func JiaotuAccountName(entry JiaotuPoolAccount, index int) string {
	if id := strings.TrimSpace(entry.PoolID); id != "" {
		return "椒图 " + id
	}
	if nick := strings.TrimSpace(entry.NickName); nick != "" {
		return "椒图 " + nick
	}
	if phone := MaskJiaotuPhone(entry.Phone); phone != "" {
		return "椒图 " + phone
	}
	return fmt.Sprintf("椒图账号 %d", index+1)
}

// MergeJiaotuCredentials 把新解析出的凭据合并进已有 credentials：
//   - token 为空时保留原 token（允许「只更新积分/状态」的部分导入）；
//   - 显式传 nil 表示删除该键（否则 delete() 会被「以 DB 现状为基底」的合并反带回来）。
func MergeJiaotuCredentials(existing, incoming map[string]any) map[string]any {
	merged := make(map[string]any, len(existing)+len(incoming))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range incoming {
		if v == nil {
			delete(merged, k)
			continue
		}
		if k == JiaotuCredentialToken {
			if token, _ := v.(string); strings.TrimSpace(token) == "" {
				continue
			}
		}
		merged[k] = v
	}
	return merged
}

func trimJiaotuBearer(token string) string {
	token = strings.TrimSpace(token)
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	return token
}

func parseJiaotuTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return &t
	}
	if ts, err := strconv.ParseInt(value, 10, 64); err == nil && ts > 0 {
		t := time.Unix(ts, 0)
		return &t
	}
	return nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

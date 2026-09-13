package service

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// 椒图模型目录（稳定 ID ↔ 上游数字 modelId）。
//
// 上游 GET /api/v1/ai/queryModels 才是权威清单；本表提供的是「客户端可见的稳定 ID」与
// 展示名覆盖层：上游新增模型时会自动以 img-<id>/vid-<id> 形式出现（见 JiaotuCatalog.MergeUpstream），
// 而已命名的稳定 ID 一旦发布就不得改变含义——它们已被 sub2api 用户写进配置里。
// 来源：kuikui cmd/kuikui/main.go:531-608（显示名 / 别名 / 稳定 ID 三张表）。

const (
	JiaotuCapabilityImage = "image"
	JiaotuCapabilityVideo = "video"
)

// JiaotuModelSpec 一个稳定的椒图模型条目。
type JiaotuModelSpec struct {
	StableID     string   // 客户端可见 ID，如 jiaotu-image-v2
	ProviderID   int      // 上游 modelId
	Capability   string   // image / video
	DisplayName  string   // 对外展示名（含椒图线路说明）
	ProviderName string   // 上游 showModelName，仅用于匹配与展示
	Aliases      []string // 额外可接受的入站别名
}

// jiaotuModelSpecs 是权威表；顺序即 /v1/models 的默认输出顺序。
var jiaotuModelSpecs = []JiaotuModelSpec{
	{StableID: "jiaotu-image-v2", ProviderID: 18, Capability: JiaotuCapabilityImage, DisplayName: "Nano Banana（椒图全能图片 V2）", ProviderName: "全能图片 V2", Aliases: []string{"image-v2", "img-18"}},
	{StableID: "jiaotu-image-v2-lite", ProviderID: 5, Capability: JiaotuCapabilityImage, DisplayName: "Nano Banana（椒图全能图片 V2 轻量线路）", ProviderName: "全能图片 V2 轻量版", Aliases: []string{"img-5"}},
	{StableID: "jiaotu-image-pro", ProviderID: 11, Capability: JiaotuCapabilityImage, DisplayName: "Nano Banana Pro（椒图全能图片 Pro）", ProviderName: "全能图片 Pro", Aliases: []string{"img-11"}},
	{StableID: "jiaotu-image-pro-fast", ProviderID: 19, Capability: JiaotuCapabilityImage, DisplayName: "Nano Banana Pro（椒图企业 Fast 线路）", ProviderName: "全能图片Pro Fast", Aliases: []string{"img-19"}},
	{StableID: "jiaotu-gpt-image-2", ProviderID: 16, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新图片 V2（兼容 GPT Image 2）", ProviderName: "最新图片 V2", Aliases: []string{"img-16"}},
	{StableID: "jiaotu-gpt-image-2-pro", ProviderID: 20, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新图片 V2 Pro（兼容 GPT Image 2）", ProviderName: "最新图片V2 Pro", Aliases: []string{"img-20"}},
	{StableID: "jiaotu-gpt-image-2-5-sunburst-pro", ProviderID: 36, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新2.5 Sunburst Pro（兼容 GPT Image 2.5）", ProviderName: "最新2.5 Sunburst Pro", Aliases: []string{"img-36"}},
	{StableID: "jiaotu-gpt-image-2-5-flare-pro", ProviderID: 37, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新2.5 Flare Pro（兼容 GPT Image 2.5）", ProviderName: "最新2.5 Flare Pro", Aliases: []string{"img-37"}},
	{StableID: "jiaotu-gpt-image-2-5-sunburst", ProviderID: 38, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新2.5 Sunburst（兼容 GPT Image 2.5）", ProviderName: "最新2.5 Sunburst", Aliases: []string{"img-38"}},
	{StableID: "jiaotu-gpt-image-2-5-flare", ProviderID: 39, Capability: JiaotuCapabilityImage, DisplayName: "椒图最新2.5 Flare（兼容 GPT Image 2.5）", ProviderName: "最新2.5 Flare", Aliases: []string{"img-39"}},
	{StableID: "jiaotu-seedream-4-5", ProviderID: 12, Capability: JiaotuCapabilityImage, DisplayName: "Seedream 4.5", ProviderName: "Seedream 4.5", Aliases: []string{"img-12"}},
	{StableID: "jiaotu-seedream-5", ProviderID: 17, Capability: JiaotuCapabilityImage, DisplayName: "Seedream 5.0", ProviderName: "Seedream 5.0 lite", Aliases: []string{"img-17"}},
	{StableID: "jiaotu-seedream-5-pro", ProviderID: 22, Capability: JiaotuCapabilityImage, DisplayName: "Seedream 5.0 Pro", ProviderName: "Seedream 5.0 Pro", Aliases: []string{"img-22"}},
	{StableID: "jiaotu-wan-2-7", ProviderID: 21, Capability: JiaotuCapabilityImage, DisplayName: "Wan 2.7 Image（阿里 Wan 系列）", ProviderName: "Wan 2.7", Aliases: []string{"img-21"}},
	{StableID: "jiaotu-wanxiang-2-6", ProviderID: 14, Capability: JiaotuCapabilityVideo, DisplayName: "Tongyi Wanxiang 2.6（通义万相）", ProviderName: "万相 2.6", Aliases: []string{"vid-14"}},
	{StableID: "jiaotu-seedance-2", ProviderID: 15, Capability: JiaotuCapabilityVideo, DisplayName: "Seedance 2.0", ProviderName: "Seedance 2.0", Aliases: []string{"vid-15"}},
	{StableID: "jiaotu-minimax-h3", ProviderID: 25, Capability: JiaotuCapabilityVideo, DisplayName: "MiniMax H3", ProviderName: "MiniMax H3", Aliases: []string{"vid-25", "h3", "minimax-h3", "minimax_h3"}},
}

// 注意：kuikui 桥接版把 `gpt-image-1` 作为「实际走椒图全能图片 V2」的协议别名，
// 但 sub2api 里 `gpt-image-*` 属于 OpenAI 自己的模型命名空间（IsGPTImageGenerationModel），
// 再把它注册成椒图别名会劫持 OpenAI 图片流量。因此本表**有意不包含** gpt-image-1：
// 要调椒图请直接用 `jiaotu-image-v2`（戒 `img-18`）。

var (
	jiaotuByStableID = func() map[string]JiaotuModelSpec {
		m := make(map[string]JiaotuModelSpec, len(jiaotuModelSpecs))
		for _, spec := range jiaotuModelSpecs {
			m[strings.ToLower(spec.StableID)] = spec
		}
		return m
	}()
	jiaotuByAlias = func() map[string]JiaotuModelSpec {
		m := make(map[string]JiaotuModelSpec, len(jiaotuModelSpecs)*3)
		for _, spec := range jiaotuModelSpecs {
			m[strings.ToLower(spec.StableID)] = spec
			for _, alias := range spec.Aliases {
				m[strings.ToLower(alias)] = spec
			}
			if name := strings.ToLower(strings.TrimSpace(spec.ProviderName)); name != "" {
				m[name] = spec
			}
			if name := strings.ToLower(strings.TrimSpace(spec.DisplayName)); name != "" {
				m[name] = spec
			}
		}
		// kuikui 的中文叫法别名表（main.go:551-592），保留兼容。
		m["全能图片 v2"] = jiaotuByStableID["jiaotu-image-v2"]
		m["全能图片 v2 轻量版"] = jiaotuByStableID["jiaotu-image-v2-lite"]
		m["全能图片 pro"] = jiaotuByStableID["jiaotu-image-pro"]
		m["全能图片pro fast"] = jiaotuByStableID["jiaotu-image-pro-fast"]
		m["最新图片 v2"] = jiaotuByStableID["jiaotu-gpt-image-2"]
		m["最新图片v2 pro"] = jiaotuByStableID["jiaotu-gpt-image-2-pro"]
		m["最新2.5 sunburst pro"] = jiaotuByStableID["jiaotu-gpt-image-2-5-sunburst-pro"]
		m["最新2.5 flare pro"] = jiaotuByStableID["jiaotu-gpt-image-2-5-flare-pro"]
		m["最新2.5 sunburst"] = jiaotuByStableID["jiaotu-gpt-image-2-5-sunburst"]
		m["最新2.5 flare"] = jiaotuByStableID["jiaotu-gpt-image-2-5-flare"]
		m["gpt image 2.5 sunburst pro"] = jiaotuByStableID["jiaotu-gpt-image-2-5-sunburst-pro"]
		m["gpt image 2.5 flare pro"] = jiaotuByStableID["jiaotu-gpt-image-2-5-flare-pro"]
		m["gpt image 2.5 sunburst"] = jiaotuByStableID["jiaotu-gpt-image-2-5-sunburst"]
		m["gpt image 2.5 flare"] = jiaotuByStableID["jiaotu-gpt-image-2-5-flare"]
		return m
	}()
	jiaotuByProviderID = func() map[int]JiaotuModelSpec {
		m := make(map[int]JiaotuModelSpec, len(jiaotuModelSpecs))
		for _, spec := range jiaotuModelSpecs {
			if _, exists := m[spec.ProviderID]; !exists {
				m[spec.ProviderID] = spec
			}
		}
		return m
	}()
)

// JiaotuModelSpecFor 解析入站模型名 → 稳定条目。
// 接受：稳定 ID、img-<n>/vid-<n>、上游数字 ID 的字符串形式、椒图中文名。
// known 为 false 时调用方必须显式报错，不得静默回落默认模型（移植契约 R4）。
func JiaotuModelSpecFor(model string) (spec JiaotuModelSpec, known bool) {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return JiaotuModelSpec{}, false
	}
	if found, ok := jiaotuByAlias[key]; ok {
		return found, true
	}
	// 纯数字（上游 modelId）
	if n, err := strconv.Atoi(key); err == nil {
		if found, ok := jiaotuByProviderID[n]; ok {
			return found, true
		}
	}
	return JiaotuModelSpec{}, false
}

// JiaotuImageModelNameFor 返回椒图模型的展示名；非椒图模型返回 ""。
// 保留给 gateway_handler 的 /v1/models 输出与 openai_images 的校验使用。
func JiaotuImageModelNameFor(model string) string {
	spec, ok := JiaotuModelSpecFor(model)
	if !ok {
		return ""
	}
	return spec.DisplayName
}

// IsJiaotuVideoModel 判断入站模型是否为椒图视频模型（含 vid-<n> 与 h3 别名）。
func IsJiaotuVideoModel(model string) bool {
	spec, ok := JiaotuModelSpecFor(model)
	return ok && spec.Capability == JiaotuCapabilityVideo
}

// IsJiaotuImageModel 判断入站模型是否为椒图图片模型。
func IsJiaotuImageModel(model string) bool {
	spec, ok := JiaotuModelSpecFor(model)
	return ok && spec.Capability == JiaotuCapabilityImage
}

// JiaotuModelIDs 返回按能力过滤的稳定 ID 列表（用于 /v1/models 与分组默认模型）。
func JiaotuModelIDs(capability string) []string {
	out := make([]string, 0, len(jiaotuModelSpecs))
	for _, spec := range jiaotuModelSpecs {
		if capability != "" && spec.Capability != capability {
			continue
		}
		out = append(out, spec.StableID)
	}
	return out
}

// JiaotuDefaultModelIDs 返回全部椒图稳定 ID（分组未配置模型清单时的兜底）。
func JiaotuDefaultModelIDs() []string {
	return JiaotuModelIDs("")
}

// JiaotuModelOption 上游 {label,value} 选项（分辨率/画质/时长）。
type JiaotuModelOption struct {
	Label any `json:"label"`
	Value any `json:"value"`
}

// JiaotuModelFee 上游按画质计费条目。
type JiaotuModelFee struct {
	Value    any   `json:"value"`
	Label    any   `json:"label"`
	HasAudio *bool `json:"hasAudio"`
	Fee      any   `json:"fee"`
}

// JiaotuUpstreamModel 是上游 /api/v1/ai/queryModels 的单条模型描述。
type JiaotuUpstreamModel struct {
	ID                    int                 `json:"id"`
	ShowModelName         string              `json:"showModelName"`
	Description           string              `json:"description"`
	ModelType             int                 `json:"modelType"` // 0=图片，1=视频
	ImageSize             int                 `json:"imageSize"` // 参考图上限
	CostRadish            any                 `json:"costRadish"`
	ResolutionList        []JiaotuModelOption `json:"resolutionList"`
	QualityLevelList      []JiaotuModelOption `json:"qualityLevelList"`
	DurationList          []JiaotuModelOption `json:"durationList"`
	AudioList             []JiaotuModelOption `json:"audioList"`
	SupportFirstLastFrame string              `json:"supportFirstLastFrame"`
	ResolutionFeeConfig   []JiaotuModelFee    `json:"resolutionFeeConfig"`
}

// Capability 上游 modelType → 能力字符串。
func (m JiaotuUpstreamModel) Capability() string {
	if m.ModelType == 1 {
		return JiaotuCapabilityVideo
	}
	return JiaotuCapabilityImage
}

// ProviderModelID 归一化 "img-18" / "vid-25" 形式的上游 ID。
func (m JiaotuUpstreamModel) LegacyID() string {
	prefix := "img-"
	if m.ModelType == 1 {
		prefix = "vid-"
	}
	return prefix + strconv.Itoa(m.ID)
}

// StableID 优先用本仓库登记的稳定 ID；上游新增模型退化为 img-<n>/vid-<n>（与 kuikui 一致）。
func (m JiaotuUpstreamModel) StableID() string {
	if spec, ok := jiaotuByProviderID[m.ID]; ok && spec.Capability == m.Capability() {
		return spec.StableID
	}
	return m.LegacyID()
}

// DisplayName 展示名：登记表优先，其次上游 showModelName，最后兜底文案。
func (m JiaotuUpstreamModel) DisplayName() string {
	if spec, ok := jiaotuByProviderID[m.ID]; ok && spec.Capability == m.Capability() && spec.DisplayName != "" {
		return spec.DisplayName
	}
	if name := strings.TrimSpace(m.ShowModelName); name != "" {
		return name
	}
	if m.ModelType == 1 {
		return "视频模型 " + strconv.Itoa(m.ID)
	}
	return "图片模型 " + strconv.Itoa(m.ID)
}

// MaxReferenceImages 参考图上限；上游给 0/负数时按 kuikui 语义兜底（图片 <1→6，视频 <0→6）。
func (m JiaotuUpstreamModel) MaxReferenceImages() int {
	limit := m.ImageSize
	if limit < 1 {
		limit = 6
	}
	if limit > 15 {
		limit = 15
	}
	return limit
}

// PointsCost 单次调用（1 张/1 秒基准）的积分消耗；解析失败按 1。
func (m JiaotuUpstreamModel) PointsCost() int {
	n := jiaotuIntValue(m.CostRadish)
	if n < 1 {
		return 1
	}
	return n
}

// PointsCostForQuality 视频按 qualityLevel 命中 resolutionFeeConfig.fee，否则回落 PointsCost。
func (m JiaotuUpstreamModel) PointsCostForQuality(quality string) int {
	quality = strings.ToLower(strings.TrimSpace(quality))
	if quality != "" {
		for _, fee := range m.ResolutionFeeConfig {
			if strings.EqualFold(jiaotuStringValue(fee.Value), quality) || strings.EqualFold(jiaotuStringValue(fee.Label), quality) {
				if n := jiaotuIntValue(fee.Fee); n > 0 {
					return n
				}
			}
		}
	}
	return m.PointsCost()
}

// Options 抽取 {label,value} 列表里的非空 value 字符串。
func (m JiaotuUpstreamModel) Options(list []JiaotuModelOption) []string {
	out := make([]string, 0, len(list))
	for _, option := range list {
		if value := strings.TrimSpace(jiaotuStringValue(option.Value)); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// SupportsFirstLastFrame 上游用字符串 "1" 表达能力开关。
func (m JiaotuUpstreamModel) SupportsFirstLastFrame() bool {
	return strings.TrimSpace(m.SupportFirstLastFrame) == "1"
}

// JiaotuCatalog 上游模型清单 + 本地稳定表的合并视图。
type JiaotuCatalog struct {
	models []JiaotuUpstreamModel
}

// NewJiaotuCatalog 用上游清单构建目录（nil/空清单安全）。
func NewJiaotuCatalog(models []JiaotuUpstreamModel) *JiaotuCatalog {
	copied := make([]JiaotuUpstreamModel, 0, len(models))
	for _, model := range models {
		if model.ID <= 0 {
			continue
		}
		copied = append(copied, model)
	}
	return &JiaotuCatalog{models: copied}
}

// Resolve 按入站模型名找上游模型；本地稳定表命中但上游未返回时返回 false（调用方决定报错文案）。
func (c *JiaotuCatalog) Resolve(model string) (JiaotuUpstreamModel, JiaotuModelSpec, bool) {
	spec, known := c.specFor(model)
	if !known {
		return JiaotuUpstreamModel{}, JiaotuModelSpec{}, false
	}
	if c != nil {
		for _, candidate := range c.models {
			if candidate.ID == spec.ProviderID {
				return candidate, spec, true
			}
		}
	}
	return JiaotuUpstreamModel{}, spec, false
}

// SpecFor 解析入站模型名，优先本地稳定表，后退到上游清单（img-<n>/vid-<n>/纯数字）。
// 上游上新但本地未登记的模型仍可用（ID 为 img-<n>），但不允许凭空捏造不存在的上游 ID。
func (c *JiaotuCatalog) SpecFor(model string) (JiaotuModelSpec, bool) {
	return c.specFor(model)
}

func (c *JiaotuCatalog) specFor(model string) (JiaotuModelSpec, bool) {
	if spec, ok := JiaotuModelSpecFor(model); ok {
		return spec, true
	}
	if c == nil {
		return JiaotuModelSpec{}, false
	}
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return JiaotuModelSpec{}, false
	}
	numeric := key
	switch {
	case strings.HasPrefix(numeric, "img-"):
		numeric = strings.TrimPrefix(numeric, "img-")
	case strings.HasPrefix(numeric, "vid-"):
		numeric = strings.TrimPrefix(numeric, "vid-")
	}
	id, err := strconv.Atoi(numeric)
	if err != nil || id <= 0 {
		return JiaotuModelSpec{}, false
	}
	for _, candidate := range c.models {
		if candidate.ID != id {
			continue
		}
		// 上游清单命中才合成，能力/展示名以上游为准。
		if !strings.HasPrefix(key, "img-") && !strings.HasPrefix(key, "vid-") && candidate.Capability() == JiaotuCapabilityVideo {
			return JiaotuModelSpec{}, false
		}
		return JiaotuModelSpec{
			StableID:     candidate.StableID(),
			ProviderID:   candidate.ID,
			Capability:   candidate.Capability(),
			DisplayName:  candidate.DisplayName(),
			ProviderName: strings.TrimSpace(candidate.ShowModelName),
			Aliases:      []string{candidate.LegacyID()},
		}, true
	}
	return JiaotuModelSpec{}, false
}

// Models 返回上游清单快照（只读拷贝）。
func (c *JiaotuCatalog) Models() []JiaotuUpstreamModel {
	if c == nil {
		return nil
	}
	out := make([]JiaotuUpstreamModel, len(c.models))
	copy(out, c.models)
	return out
}

// OpenAIModelEntries 生成 /v1/models 输出，字段语义与 kuikui openAIModelList 对齐：
// 携带 capability / provider / provider_model_id / provider_model_name / aliases，
// 图片模型额外标 compatibility_protocol=openai-images。
func (c *JiaotuCatalog) OpenAIModelEntries() []JiaotuCatalogEntry {
	seen := map[string]struct{}{}
	out := make([]JiaotuCatalogEntry, 0, len(c.models)+len(jiaotuModelSpecs))
	appendEntry := func(entry JiaotuCatalogEntry) {
		if _, dup := seen[entry.ID]; dup {
			return
		}
		seen[entry.ID] = struct{}{}
		out = append(out, entry)
	}
	if c != nil {
		for _, model := range c.models {
			aliases := []string{model.LegacyID()}
			if name := strings.TrimSpace(model.ShowModelName); name != "" {
				aliases = append(aliases, name)
			}
			entry := JiaotuCatalogEntry{
				ID: model.StableID(), Object: "model", OwnedBy: "jiaotu", Name: model.DisplayName(),
				Capability: model.Capability(), Provider: "jiaotu", Type: model.Capability(),
				DisplayName:     model.DisplayName(),
				ProviderModelID: model.ID, ProviderModelName: strings.TrimSpace(model.ShowModelName),
				Aliases: aliases,
			}
			if model.ModelType == 0 {
				entry.CompatibilityProtocol = "openai-images"
			}
			appendEntry(entry)
		}
	}
	for _, spec := range jiaotuModelSpecs {
		prefix := "img-"
		if spec.Capability == JiaotuCapabilityVideo {
			prefix = "vid-"
		}
		aliases := append([]string{prefix + strconv.Itoa(spec.ProviderID), spec.ProviderName}, spec.Aliases...)
		entry := JiaotuCatalogEntry{
			ID: spec.StableID, Object: "model", OwnedBy: "jiaotu", Name: spec.DisplayName,
			Capability: spec.Capability, Provider: "jiaotu", Type: spec.Capability,
			DisplayName:     spec.DisplayName,
			ProviderModelID: spec.ProviderID, ProviderModelName: spec.ProviderName,
			Aliases: aliases,
		}
		if spec.Capability == JiaotuCapabilityImage {
			entry.CompatibilityProtocol = "openai-images"
		}
		appendEntry(entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// JiaotuCatalogEntry /v1/models 的一条输出。
type JiaotuCatalogEntry struct {
	ID                    string   `json:"id"`
	Object                string   `json:"object"`
	OwnedBy               string   `json:"owned_by"`
	Name                  string   `json:"name"`
	Capability            string   `json:"capability"`
	Provider              string   `json:"provider"`
	ProviderModelID       int      `json:"provider_model_id"`
	ProviderModelName     string   `json:"provider_model_name,omitempty"`
	CompatibilityProtocol string   `json:"compatibility_protocol,omitempty"`
	Aliases               []string `json:"aliases,omitempty"`
	Type                  string   `json:"type"`
	DisplayName           string   `json:"display_name,omitempty"`
}

func jiaotuStringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func jiaotuIntValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(typed))
		return n
	default:
		return 0
	}
}

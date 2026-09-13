package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// Async image results are kept in Redis for up to 24 hours. Keep a generous
// bound for legitimate high-resolution/multi-image responses, while ensuring
// an upstream cannot turn the task store into an unbounded blob cache.
const defaultImageTaskMaxResultBytes int64 = 128 << 20 // 128 MiB

const maxImageTaskResultItems = 16

// validateImageTaskResult checks the small OpenAI Images response contract used
// by the asynchronous endpoint. A successful task must contain at least one
// data item with a URL or b64_json; arbitrary JSON is not a successful image
// result. When object storage is unavailable, inline image payloads are
// rejected so they can never be written to Redis by mistake.
func validateImageTaskResult(result json.RawMessage, allowBase64, allowDataURL, allowEmptyData bool, maxImageBytes int64) error {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 {
		return fmt.Errorf("image response is empty")
	}
	if int64(len(trimmed)) > defaultImageTaskMaxResultBytes {
		return fmt.Errorf("image response exceeds %d bytes", defaultImageTaskMaxResultBytes)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return fmt.Errorf("parse image response: %w", err)
	}
	if top == nil {
		return fmt.Errorf("image response must be a JSON object")
	}

	// b64_json is only valid directly on an image item. Reject copies hidden in
	// metadata/error fields as well, otherwise they could bypass the uploader.
	for key, raw := range top {
		if key == "data" {
			continue
		}
		if containsJSONKey(raw, "b64_json") || containsInlineImageData(raw) {
			return fmt.Errorf("image response contains inline image data outside data items")
		}
	}

	rawData, ok := top["data"]
	if !ok {
		return fmt.Errorf("image response is missing data")
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &items); err != nil {
		return fmt.Errorf("image response data must be an array: %w", err)
	}
	if len(items) == 0 {
		if allowEmptyData {
			return nil
		}
		return fmt.Errorf("image response data is empty")
	}
	if len(items) > maxImageTaskResultItems {
		return fmt.Errorf("image response contains too many images")
	}

	if maxImageBytes <= 0 {
		maxImageBytes = defaultImageMaxDownloadBytes
	}
	for index, item := range items {
		if item == nil {
			return fmt.Errorf("image response data[%d] must be an object", index)
		}
		url, _, err := imageTaskStringField(item, "url")
		if err != nil {
			return fmt.Errorf("image response data[%d].url: %w", index, err)
		}
		b64, hasB64, err := imageTaskStringField(item, "b64_json")
		if err != nil {
			return fmt.Errorf("image response data[%d].b64_json: %w", index, err)
		}
		if hasB64 && b64 != "" {
			if !allowBase64 {
				return fmt.Errorf("image response data[%d] contains inline b64_json", index)
			}
			if err := validateImageTaskBase64(b64, maxImageBytes); err != nil {
				return fmt.Errorf("image response data[%d].b64_json: %w", index, err)
			}
		}
		if url != "" && strings.HasPrefix(strings.ToLower(url), "data:") && !allowDataURL {
			return fmt.Errorf("image response data[%d] contains an inline data URL", index)
		}
		if url == "" && b64 == "" {
			return fmt.Errorf("image response data[%d] has neither url nor b64_json", index)
		}

		// Only a direct item-level b64_json is supported. Nested occurrences are
		// ambiguous and would not be removed by ImageResultUploader.Rewrite.
		//
		// url 必须一并排除：数据类 URL（data:image/...;base64,）正是本函数要「转存」的
		// 合法入站形态（见上面的 allowDataURL 判定与 decodeImageDataURL），把 url 当
		// 「嵌套载荷」拦下会让整个 data URL 转存链路失效，只剩一条含义错的
		// 「contains nested b64_json」，也抱不到下游那些精确的解码错误。
		for key, raw := range item {
			if key == "b64_json" || key == "url" {
				continue
			}
			if containsJSONKey(raw, "b64_json") || containsInlineImageData(raw) {
				return fmt.Errorf("image response data[%d] contains nested b64_json", index)
			}
		}
	}
	return nil
}

func imageTaskStringField(object map[string]json.RawMessage, field string) (string, bool, error) {
	raw, ok := object[field]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", ok, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("must be a string")
	}
	return strings.TrimSpace(value), true, nil
}

func validateImageTaskBase64(value string, maxImageBytes int64) error {
	// Reject obviously oversized values before DecodeString allocates its output.
	if int64(base64.StdEncoding.DecodedLen(len(value))) > maxImageBytes+3 {
		return fmt.Errorf("decoded image exceeds %d bytes", maxImageBytes)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("invalid base64 payload")
	}
	if len(decoded) == 0 {
		return fmt.Errorf("base64 payload is empty")
	}
	if int64(len(decoded)) > maxImageBytes {
		return fmt.Errorf("decoded image exceeds %d bytes", maxImageBytes)
	}
	return nil
}

func containsJSONKey(raw json.RawMessage, wanted string) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return containsJSONKeyValue(value, wanted)
}

func containsJSONKeyValue(value any, wanted string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if strings.EqualFold(key, wanted) || containsJSONKeyValue(nested, wanted) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsJSONKeyValue(nested, wanted) {
				return true
			}
		}
	}
	return false
}

func containsInlineImageData(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	return containsInlineImageDataValue(value)
}

func containsInlineImageDataValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(typed)), "data:image/")
	case map[string]any:
		for _, nested := range typed {
			if containsInlineImageDataValue(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsInlineImageDataValue(nested) {
				return true
			}
		}
	}
	return false
}

// sanitizeImageTaskResult is the final API boundary. It removes inline image
// blobs even for tasks created by an older version or a custom store, and also
// drops data: URLs which embed the same blob under a different field name.
func sanitizeImageTaskResult(result json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(result)) == 0 || !json.Valid(result) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(result))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	value = sanitizeImageTaskJSONValue(value)
	out, err := json.Marshal(value)
	if err != nil || int64(len(out)) > defaultImageTaskMaxResultBytes {
		return nil
	}
	return out
}

func sanitizeImageTaskJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if strings.EqualFold(key, "b64_json") {
				continue
			}
			if strings.EqualFold(key, "url") {
				if text, ok := nested.(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "data:") {
					continue
				}
			}
			if text, ok := nested.(string); ok && strings.HasPrefix(strings.ToLower(strings.TrimSpace(text)), "data:image/") {
				continue
			}
			out[key] = sanitizeImageTaskJSONValue(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, nested := range typed {
			out[index] = sanitizeImageTaskJSONValue(nested)
		}
		return out
	default:
		return value
	}
}

// Package llmusage extracts non-standard usage fields from OpenRouter
// responses returned via the openai-go SDK.
//
// OpenRouter's OpenAI-compatible response carries Anthropic-style cache
// tokens and the upstream-billed cost in places that are not part of
// the typed CompletionUsage struct. The same field can appear under
// several names depending on the provider/version, so we probe each
// known location in order.
package llmusage

import (
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
)

// IncludeUsageOption asks OpenRouter to populate usage.cost on the response.
// Required for ProviderCostUSD to return non-zero.
var IncludeUsageOption = option.WithJSONSet("usage", map[string]any{"include": true})

// CacheWriteTokens returns the cache-creation token count reported by
// the provider. OpenRouter for Anthropic places this in one of:
//   - usage.cache_creation_input_tokens (root extra)
//   - usage.prompt_tokens_details.cache_creation_tokens (nested extra)
//   - usage.prompt_tokens_details.cache_write_tokens (nested extra, older naming)
func CacheWriteTokens(u openai.CompletionUsage) int64 {
	if v, ok := extraInt64(u.JSON.ExtraFields, "cache_creation_input_tokens"); ok {
		return v
	}
	if v, ok := extraInt64(u.PromptTokensDetails.JSON.ExtraFields, "cache_creation_tokens"); ok {
		return v
	}
	if v, ok := extraInt64(u.PromptTokensDetails.JSON.ExtraFields, "cache_write_tokens"); ok {
		return v
	}
	return 0
}

// CachedTokens returns the cache-read token count, falling back to
// Anthropic's cache_read_input_tokens at the root when the typed
// prompt_tokens_details.cached_tokens is missing.
func CachedTokens(u openai.CompletionUsage) int64 {
	if u.PromptTokensDetails.CachedTokens > 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	if v, ok := extraInt64(u.JSON.ExtraFields, "cache_read_input_tokens"); ok {
		return v
	}
	return 0
}

// ProviderCostUSD returns the upstream-billed cost reported by OpenRouter
// when the request includes "usage": {"include": true}. Zero when not
// present (e.g. direct OpenAI calls).
func ProviderCostUSD(u openai.CompletionUsage) float64 {
	if v, ok := extraFloat64(u.JSON.ExtraFields, "cost"); ok {
		return v
	}
	return 0
}

// ActualProvider returns the concrete upstream selected by an OpenAI-compatible
// router when it exposes a top-level provider field. Direct provider APIs may
// omit it; callers must preserve that as unknown rather than inferring it from
// the requested model name.
func ActualProvider(chunk openai.ChatCompletionChunk) string {
	for _, key := range []string{"provider", "provider_name"} {
		if v, ok := extraString(chunk.JSON.ExtraFields, key); ok {
			return v
		}
	}
	return ""
}

// Note: respjson.Field.Valid() returns false for extra (non-schema)
// fields even when a raw value is present, so we don't gate on it —
// only on whether the raw payload parses as the target numeric type.
func extraInt64(extras map[string]respjson.Field, key string) (int64, bool) {
	f, ok := extras[key]
	if !ok {
		return 0, false
	}
	raw := f.Raw()
	if raw == "" || raw == "null" {
		return 0, false
	}
	var v int64
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0, false
	}
	return v, true
}

func extraFloat64(extras map[string]respjson.Field, key string) (float64, bool) {
	f, ok := extras[key]
	if !ok {
		return 0, false
	}
	raw := f.Raw()
	if raw == "" || raw == "null" {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return 0, false
	}
	return v, true
}

func extraString(extras map[string]respjson.Field, key string) (string, bool) {
	f, ok := extras[key]
	if !ok {
		return "", false
	}
	raw := f.Raw()
	if raw == "" || raw == "null" {
		return "", false
	}
	var v string
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v == "" {
		return "", false
	}
	return v, true
}

package llm

import (
	"context"

	"github.com/EternisAI/takes-replies-basket-test/internal/llmusage"
	"github.com/openai/openai-go/v3"
)

// Usage is what one completion consumed. PromptTokens is the total input,
// with CachedTokens and CacheWriteTokens nested inside it rather than added to
// it. CostUSD is the upstream-billed figure OpenRouter reports; it stays zero
// for endpoints that do not report one, which a caller must read as unknown
// rather than free.
type Usage struct {
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	CacheWriteTokens int64
	CostUSD          float64
}

type usageObserverKey struct{}

// WithUsageObserver returns a context under which every OpenAICompleter
// completion reports what it consumed to fn. Observation is scoped to the
// context rather than the completer because subsystems share one completer
// instance and only some of them account for their own spend.
//
// Only a completed, decoded response is reported: a request that fails in the
// SDK — a bad key, a timeout, an exhausted retry — reports nothing, so an
// observer counts responses, not attempts.
//
// fn runs on the calling goroutine of each completion, so a context shared by
// concurrent calls invokes it concurrently and it must be safe for that.
func WithUsageObserver(ctx context.Context, fn func(Usage)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, usageObserverKey{}, fn)
}

func usageObserverFrom(ctx context.Context) func(Usage) {
	fn, _ := ctx.Value(usageObserverKey{}).(func(Usage))
	return fn
}

func usageOf(model string, u openai.CompletionUsage) Usage {
	return Usage{
		Model:            model,
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		CachedTokens:     llmusage.CachedTokens(u),
		CacheWriteTokens: llmusage.CacheWriteTokens(u),
		CostUSD:          llmusage.ProviderCostUSD(u),
	}
}

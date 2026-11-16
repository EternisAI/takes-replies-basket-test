// Package llm narrows the openai SDK to the methods the backend actually
// calls, so the coordinator's response-handling code can be exercised
// against a fake client in unit tests without standing up OpenRouter or
// httptest scaffolding.
//
// Today only the coordinator consumes this interface. Other modules
// (forecast, twitter, curator, …) still take *openai.Client directly —
// migrating them is out of scope until they need mocking too.
package llm

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	ssestream "github.com/openai/openai-go/v3/packages/ssestream"
)

// Client exposes the only two SDK methods the coordinator calls. Adding
// methods here means widening the test surface that fakes must implement,
// so keep this small.
type Client interface {
	NewChatCompletion(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error)
	NewChatCompletionStream(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk]
}

// Wrap adapts a concrete *openai.Client into the Client interface. The
// adapter is a thin shim — every method forwards directly to the SDK.
func Wrap(c *openai.Client) Client {
	return &openaiAdapter{c: c}
}

type openaiAdapter struct {
	c *openai.Client
}

func (a *openaiAdapter) NewChatCompletion(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error) {
	return a.c.Chat.Completions.New(ctx, params, opts...)
}

func (a *openaiAdapter) NewChatCompletionStream(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk] {
	return a.c.Chat.Completions.NewStreaming(ctx, params, opts...)
}

package llm

import (
	"context"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	ssestream "github.com/openai/openai-go/v3/packages/ssestream"
)

// RouteByModelPrefix routes requests whose model starts with prefix to routed.
// The prefix is stripped before the routed client sees the request.
func RouteByModelPrefix(primary Client, prefix string, routed Client) Client {
	prefix = strings.TrimSpace(prefix)
	if primary == nil || prefix == "" || routed == nil {
		return primary
	}
	return &modelPrefixRouter{primary: primary, prefix: prefix, routed: routed}
}

type modelPrefixRouter struct {
	primary Client
	prefix  string
	routed  Client
}

func (r *modelPrefixRouter) NewChatCompletion(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error) {
	client, routedParams := r.route(params)
	return client.NewChatCompletion(ctx, routedParams, opts...)
}

func (r *modelPrefixRouter) NewChatCompletionStream(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk] {
	client, routedParams := r.route(params)
	return client.NewChatCompletionStream(ctx, routedParams, opts...)
}

func (r *modelPrefixRouter) route(params openai.ChatCompletionNewParams) (Client, openai.ChatCompletionNewParams) {
	if strings.HasPrefix(params.Model, r.prefix) {
		params.Model = strings.TrimPrefix(params.Model, r.prefix)
		return r.routed, params
	}
	return r.primary, params
}

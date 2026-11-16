package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/llmusage"
	"github.com/invopop/jsonschema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

// Completer is the LLM seam used by the structured-output helper. The
// production implementation calls OpenRouter; tests supply a stub that returns
// canned JSON, so structured-output decoding is exercised without network.
type Completer interface {
	// Complete sends a single structured-output request and returns the raw
	// JSON content of the model's first choice.
	Complete(ctx context.Context, req CompletionRequest) (string, error)
}

// CompletionRequest is the model-agnostic input to a structured-output call.
// Schema is a JSON Schema (as produced by GenerateSchema) the provider must
// constrain the output to.
type CompletionRequest struct {
	Model        string
	SystemPrompt string
	UserPrompt   string
	// SchemaName labels the schema in the provider request.
	SchemaName string
	// Schema is the JSON Schema the response must satisfy.
	Schema any
	// ReasoningEffort is left out of the request when empty.
	ReasoningEffort string
	// CanonicalOpenRouterReasoning sends reasoning.effort instead of the
	// OpenAI-compatible reasoning_effort field.
	CanonicalOpenRouterReasoning bool
	// ExcludeReasoning excludes reasoning content from the returned message.
	ExcludeReasoning bool
	// ServiceTier selects an OpenRouter service tier. Empty leaves it unchanged.
	ServiceTier string
	// MaxTokens caps the output, reasoning included. Zero leaves it to the provider.
	MaxTokens int
	// Strict makes the provider enforce the schema, bounds included. Every
	// object must then list all its properties as required and have no maps.
	Strict bool
	// CacheSystemPrompt puts Anthropic's cache breakpoint on the system
	// prompt, on OpenRouter only, so requests that share it read it from the
	// cache. It pays only for a fixed system prompt over the model's minimum.
	CacheSystemPrompt bool
	// NoRetry sends the request once, with the client's retries off, for a
	// caller that pays for each attempt and counts them itself.
	NoRetry bool
	// OpenRouterWebSearch opts this request into the bounded Exa fast server tool.
	// A non-OpenRouter route fails closed when this is true.
	OpenRouterWebSearch bool
	// OpenRouterWebSearchAllowedDomains restricts search results for this request.
	OpenRouterWebSearchAllowedDomains []string
	// Provider is OpenRouter's provider routing for the request, sent on
	// OpenRouter only. Nil leaves the choice of endpoint to OpenRouter.
	Provider *ProviderRouting
}

// ProviderRouting is the part of OpenRouter's provider object a caller
// sets: the endpoints to try, in order; whether any other may serve the
// request when they fail; and whether only endpoints that take every
// parameter sent may serve it, which keeps a JSON schema from going to an
// endpoint that ignores it.
// See https://openrouter.ai/docs/guides/routing/provider-selection
type ProviderRouting struct {
	Order             []string `json:"order,omitempty"`
	AllowFallbacks    *bool    `json:"allow_fallbacks,omitempty"`
	RequireParameters bool     `json:"require_parameters,omitempty"`
}

// GenerateSchema reflects a JSON Schema from a Go type. No additional
// properties, no $ref indirection (OpenRouter non-strict mode wants a
// self-contained schema).
func GenerateSchema[T any]() any {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T
	return reflector.Reflect(v)
}

// OpenAICompleter is the production Completer backed by an openai-go client
// pointed at OpenRouter. OpenRouter says whether the endpoint actually is
// OpenRouter, which decides whether the usage-accounting extension may be sent;
// a strict OpenAI-compatible endpoint rejects unknown body fields.
type OpenAICompleter struct {
	Client     *openai.Client
	OpenRouter bool
}

func NewOpenAICompleter(client *openai.Client, openRouter bool) *OpenAICompleter {
	return &OpenAICompleter{Client: client, OpenRouter: openRouter}
}

func (c *OpenAICompleter) SupportsOpenRouterWebSearch() bool {
	return c != nil && c.OpenRouter
}

func (c *OpenAICompleter) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	if req.OpenRouterWebSearch && !c.OpenRouter {
		return "", fmt.Errorf("OpenRouter web search is unavailable on this completion route")
	}
	messages := make([]openai.ChatCompletionMessageParamUnion, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, systemMessage(req.SystemPrompt, req.CacheSystemPrompt && c.OpenRouter))
	}
	messages = append(messages, openai.UserMessage(req.UserPrompt))

	// Opting into usage accounting is an OpenRouter extension, so it goes out
	// only against OpenRouter, and only when someone is listening. Standard
	// usage fields come back either way; cost does not.
	var opts []option.RequestOption
	observe := usageObserverFrom(ctx)
	if observe != nil && c.OpenRouter {
		opts = append(opts, llmusage.IncludeUsageOption)
	}
	if req.NoRetry {
		opts = append(opts, option.WithMaxRetries(0))
	}
	if req.Provider != nil && c.OpenRouter {
		opts = append(opts, option.WithJSONSet("provider", req.Provider))
	}
	if req.OpenRouterWebSearch {
		searchParameters := map[string]any{
			"engine": "exa", "mode": "fast", "max_uses": 1,
			"max_results": 5, "max_total_results": 5, "max_characters": 2000,
		}
		if len(req.OpenRouterWebSearchAllowedDomains) > 0 {
			searchParameters["allowed_domains"] = append([]string(nil), req.OpenRouterWebSearchAllowedDomains...)
		}
		opts = append(opts,
			option.WithJSONSet("tools", []map[string]any{{
				"type":       "openrouter:web_search",
				"parameters": searchParameters,
			}}),
			option.WithJSONSet("max_tool_calls", 1),
			option.WithJSONSet("tool_choice", "required"),
		)
	}
	reasoningEffort := req.ReasoningEffort
	if c.OpenRouter && req.CanonicalOpenRouterReasoning && reasoningEffort != "" {
		opts = append(opts, option.WithJSONSet("reasoning.effort", reasoningEffort))
		reasoningEffort = ""
	}
	if c.OpenRouter && req.ExcludeReasoning {
		opts = append(opts, option.WithJSONSet("reasoning.exclude", true))
	}
	if c.OpenRouter && strings.TrimSpace(req.ServiceTier) != "" {
		opts = append(opts, option.WithJSONSet("service_tier", strings.TrimSpace(req.ServiceTier)))
	}
	params := openai.ChatCompletionNewParams{
		Model:           req.Model,
		Messages:        messages,
		ReasoningEffort: shared.ReasoningEffort(reasoningEffort),
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   req.SchemaName,
					Schema: req.Schema,
					// Schema-guided unless the caller asks for strict. Strict
					// structured outputs require every property in `required` and
					// forbid open `additionalProperties`, which schemas with
					// dynamic-key maps cannot satisfy. Go-side validation enforces
					// correctness either way.
					Strict: openai.Bool(req.Strict),
				},
			},
		},
	}
	// Reasoning models reject a temperature alongside an effort.
	if req.ReasoningEffort == "" {
		params.Temperature = openai.Float(0)
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}
	chat, err := c.Client.Chat.Completions.New(ctx, params, opts...)
	if err != nil {
		return "", fmt.Errorf("chat completion: %w", err)
	}
	if req.OpenRouterWebSearch {
		CaptureWebCitations(ctx, WebSearchReceipt{
			Citations: webCitationsFrom(chat), SearchRequests: webSearchRequestsFrom(chat),
		})
	}
	if observe != nil {
		observe(usageOf(req.Model, chat.Usage))
	}
	if len(chat.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}
	choice := chat.Choices[0]
	// A cut-off answer can still parse when the schema is not strict.
	switch {
	case choice.Message.Refusal != "":
		return "", fmt.Errorf("model refused: %s", choice.Message.Refusal)
	case choice.FinishReason == "length":
		return "", fmt.Errorf("response truncated at the output limit")
	}
	return choice.Message.Content, nil
}

// systemMessage is the system prompt, as one text part carrying the cache
// breakpoint when cached: OpenRouter takes a per-message breakpoint only on
// a content part.
func systemMessage(prompt string, cached bool) openai.ChatCompletionMessageParamUnion {
	if !cached {
		return openai.SystemMessage(prompt)
	}
	part := openai.ChatCompletionContentPartTextParam{Text: prompt}
	part.SetExtraFields(map[string]any{"cache_control": map[string]any{"type": "ephemeral"}})
	return openai.SystemMessage([]openai.ChatCompletionContentPartTextParam{part})
}

// Structured runs one structured-output round-trip: reflect a JSON Schema from
// T, ask the Completer for T-shaped JSON, and unmarshal into T. The Completer
// seam means this is fully testable without network access.
func Structured[T any](ctx context.Context, c Completer, model, systemPrompt, userPrompt string) (T, error) {
	var out T
	schema := GenerateSchema[T]()
	content, err := c.Complete(ctx, CompletionRequest{
		Model:        model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		SchemaName:   "structured_output",
		Schema:       schema,
	})
	if err != nil {
		return out, err
	}
	// Decode the single JSON value, accepting unknown fields (LLMs legitimately
	// emit extra keys), then reject any trailing non-whitespace: a follow-up
	// Token() must report io.EOF, otherwise the payload had trailing garbage.
	dec := json.NewDecoder(strings.NewReader(content))
	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("decode LLM JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return out, fmt.Errorf("decode LLM JSON: unexpected trailing data after JSON value")
	}
	return out, nil
}

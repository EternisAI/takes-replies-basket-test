package host

import (
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const openRouterBaseURL = "https://openrouter.ai/api/v1"

// NewOpenRouterCompleter returns a real OpenRouter-backed completer when a
// key is present. Without a key it returns an explicit unavailable adapter;
// it never substitutes canned model output.
func NewOpenRouterCompleter(apiKey string) llm.Completer {
	if strings.TrimSpace(apiKey) == "" {
		return llm.NewUnavailableCompleter()
	}
	client := openai.NewClient(option.WithAPIKey(strings.TrimSpace(apiKey)), option.WithBaseURL(openRouterBaseURL))
	return llm.NewOpenAICompleter(&client, true)
}

package llm

import (
	"context"
	"errors"
)

// ErrNotConfigured is returned when a model operation is requested without
// an OpenRouter key. The API and worker remain available for health checks
// and conversation reads while model-backed operations fail closed.
var ErrNotConfigured = errors.New("OpenRouter API key is not configured")

type unavailableCompleter struct{}

func NewUnavailableCompleter() Completer { return unavailableCompleter{} }

func (unavailableCompleter) Complete(context.Context, CompletionRequest) (string, error) {
	return "", ErrNotConfigured
}

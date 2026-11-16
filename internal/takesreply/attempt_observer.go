package takesreply

import (
	"context"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

type completionAttemptObserverKey struct{}

type attemptObservingCompleter struct{ llm.Completer }

func (c attemptObservingCompleter) Complete(ctx context.Context, request llm.CompletionRequest) (string, error) {
	if observe, ok := ctx.Value(completionAttemptObserverKey{}).(func()); ok {
		observe()
	}
	return c.Completer.Complete(ctx, request)
}

func (c attemptObservingCompleter) SupportsOpenRouterWebSearch() bool {
	return llm.SupportsOpenRouterWebSearch(c.Completer)
}

func withCompletionAttemptObserver(ctx context.Context, observe func()) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, completionAttemptObserverKey{}, observe)
}

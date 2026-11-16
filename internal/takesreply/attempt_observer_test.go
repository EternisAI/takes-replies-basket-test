package takesreply

import (
	"context"
	"errors"
	"testing"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

type failedAttemptCompleter struct{}

func (failedAttemptCompleter) Complete(context.Context, llm.CompletionRequest) (string, error) {
	return "", errors.New("completion failed before usage was returned")
}

func TestAttemptObserverCountsCompletionWithoutUsage(t *testing.T) {
	attempts := 0
	completer := attemptObservingCompleter{Completer: failedAttemptCompleter{}}
	ctx := withCompletionAttemptObserver(context.Background(), func() { attempts++ })

	if _, err := completer.Complete(ctx, llm.CompletionRequest{}); err == nil {
		t.Fatal("completion should fail")
	}
	if attempts != 1 {
		t.Fatalf("observed attempts = %d, want 1", attempts)
	}
}

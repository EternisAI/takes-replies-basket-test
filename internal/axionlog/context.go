package axionlog

import "context"

type threadIDKey struct{}

func WithThreadID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, id)
}

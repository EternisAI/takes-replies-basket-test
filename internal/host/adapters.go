// Package host contains small adapters for the standalone deployment. Its
// admission adapter deliberately does not bill or charge the configured test
// owner; model usage is retained for inspection only.
package host

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesreply"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Admission struct{ Usage *UsageLedger }

func (a Admission) AdmitReplyTurn(ctx context.Context, userID string) (func(bool, string, string, []llm.Usage, bool), error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("authenticated owner is required")
	}
	return func(committed bool, threadID, completionID string, items []llm.Usage, unknownAttempt bool) {
		if a.Usage == nil {
			return
		}
		a.Usage.Record(context.WithoutCancel(ctx), userID, threadID, completionID, "takes_reply_turn", items, &committed, unknownAttempt)
	}, nil
}

// Assert the host adapter matches the preserved domain seam.
var _ takesreply.Admission = Admission{}

type UsageLedger struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewUsageLedger(pool *pgxpool.Pool, logger *slog.Logger) *UsageLedger {
	if logger == nil {
		logger = slog.Default()
	}
	return &UsageLedger{pool: pool, logger: logger}
}

func (u *UsageLedger) CaptureCompletionCost(userID, threadID, completionID, purpose string, items []llm.Usage) {
	u.Record(context.Background(), userID, threadID, completionID, purpose, items, nil, false)
}

func (u *UsageLedger) Record(ctx context.Context, userID, threadID, completionID, purpose string, items []llm.Usage, committed *bool, unknownAttempt bool) {
	if u == nil || u.pool == nil {
		return
	}
	var completion uuid.UUID
	if parsed, err := uuid.Parse(completionID); err == nil {
		completion = parsed
	}
	var committedValue any
	if committed != nil {
		committedValue = *committed
	}
	for _, item := range items {
		var completionValue any
		if completion != uuid.Nil {
			completionValue = completion
		}
		var cost any
		if item.CostUSD > 0 {
			cost = item.CostUSD
		}
		writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := u.pool.Exec(writeCtx, `INSERT INTO reply_model_usage
			(id,user_id,thread_id,completion_id,purpose,event_kind,model,prompt_tokens,completion_tokens,cached_tokens,cache_write_tokens,provider_cost_usd,cost_reported,request_committed)
			VALUES ($1,$2,$3,$4,$5,'completion',$6,$7,$8,$9,$10,$11,$12,$13)`, uuid.New(), userID, threadID,
			completionValue, purpose, item.Model, item.PromptTokens, item.CompletionTokens, item.CachedTokens, item.CacheWriteTokens, cost, cost != nil, committedValue)
		cancel()
		if err != nil {
			u.logger.Warn("could not persist reply model usage", "error", err)
		}
	}
	if unknownAttempt {
		writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := u.pool.Exec(writeCtx, `INSERT INTO reply_model_usage
			(id,user_id,thread_id,completion_id,purpose,event_kind,cost_reported,request_committed)
			VALUES ($1,$2,$3,NULL,$4,'unreported_attempt',false,$5)`, uuid.New(), userID, threadID, purpose, committedValue)
		cancel()
		if err != nil {
			u.logger.Warn("could not persist unreported reply model attempt", "error", err)
		}
	}
}

var _ takesreply.BuildUsage = (*UsageLedger)(nil)

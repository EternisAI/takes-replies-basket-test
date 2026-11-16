package takesreply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/axionlog"
	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const buildTimeout = 10 * time.Minute

type ResearchBuilder interface {
	Build(context.Context, takesresearch.Input) (takesresearch.Output, error)
}

type BuildUsage interface {
	CaptureCompletionCost(string, string, string, string, []llm.Usage)
}

type BuildGate interface{ Refuse() error }

type BuildWorker struct {
	queries replydb.Querier
	catalog takesresearch.CatalogReader
	builder ResearchBuilder
	usage   BuildUsage
	gate    BuildGate
	logger  *slog.Logger
}

func NewBuildWorker(pool *pgxpool.Pool, catalog takesresearch.CatalogReader, builder ResearchBuilder, usage BuildUsage, gate BuildGate, logger *slog.Logger) *BuildWorker {
	return &BuildWorker{queries: replydb.New(pool), catalog: catalog, builder: builder, usage: usage, gate: gate, logger: logger}
}

func (w *BuildWorker) Run(ctx context.Context, pollInterval time.Duration) {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	timer := time.NewTicker(pollInterval)
	defer timer.Stop()
	for {
		worked, err := w.RunNext(ctx)
		if err != nil && ctx.Err() == nil {
			w.logger.ErrorContext(ctx, "Takes reply build failed", "error", err)
		}
		if worked && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (w *BuildWorker) RunNext(ctx context.Context) (bool, error) {
	expiredAt := clock.Now().UTC()
	if err := w.queries.ExpireBuilds(ctx, &expiredAt); err != nil {
		return false, fmt.Errorf("expire interrupted Takes reply builds: %w", err)
	}
	if w.gate != nil && w.gate.Refuse() != nil {
		return false, nil
	}
	now := clock.Now().UTC()
	leaseUntil := now.Add(buildTimeout + 2*time.Minute)
	job, err := w.queries.ClaimBuild(ctx, replydb.ClaimBuildParams{StartedAt: &now, LeaseUntil: &leaseUntil})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim Takes reply build: %w", err)
	}
	ctx = axionlog.WithThreadID(ctx, job.ThreadID)
	buildCtx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	var usageMu sync.Mutex
	var usage []llm.Usage
	buildCtx = llm.WithUsageObserver(buildCtx, func(item llm.Usage) { usageMu.Lock(); usage = append(usage, item); usageMu.Unlock() })
	defer func() {
		if w.usage != nil {
			usageMu.Lock()
			calls := append([]llm.Usage(nil), usage...)
			usageMu.Unlock()
			w.usage.CaptureCompletionCost(job.UserID, job.ThreadID, job.ID.String(), "takes_reply_build", calls)
		}
	}()
	output, buildErr := w.research(buildCtx, job)
	status := "FAILED"
	var failure *string
	if buildErr != nil {
		message := "The basket build could not be completed. Provider work was not repeated."
		failure = &message
	} else {
		switch output.Status {
		case takesresearch.StatusReady:
			status = "COMPLETED"
		case takesresearch.StatusPartial:
			status = "PARTIAL"
		case takesresearch.StatusFailed:
			message := "The build could not produce a complete proposed basket. Review the missing requirements."
			failure = &message
		default:
			buildErr = errors.New("research returned an unsupported status")
			message := "The build returned an unsupported status."
			failure = &message
		}
	}
	if status == "COMPLETED" && !completeProposal(output.ProposalJSON, job.ID.String(), output.CompletionSnapshot) {
		status = "FAILED"
		buildErr = errors.New("build omitted required proposal data")
		message := "The build did not return the required proposal data."
		failure = &message
	}
	completion, err := json.Marshal(output.CompletionSnapshot)
	if err != nil {
		return true, err
	}
	missing := output.MissingRequirements
	if missing == nil {
		missing = []string{}
	}
	missingJSON, err := json.Marshal(missing)
	if err != nil {
		return true, err
	}
	completedAt := clock.Now().UTC()
	saveCtx, saveCancel := context.WithTimeout(context.WithoutCancel(ctx), requestCleanupTimeout)
	defer saveCancel()
	changed, err := w.queries.CompleteBuild(saveCtx, replydb.CompleteBuildParams{ID: job.ID, Status: status, Proposal: output.ProposalJSON,
		Assessment: output.AssessmentJSON, CompletionSnapshot: completion, MissingRequirements: missingJSON, FailureMessage: failure, CompletedAt: &completedAt})
	if err != nil {
		return true, fmt.Errorf("save Takes reply build result: %w", err)
	}
	if changed != 1 {
		return true, errors.New("takes reply build claim expired before completion")
	}
	return true, buildErr
}

func (w *BuildWorker) research(ctx context.Context, job replydb.TakesReplyBuild) (takesresearch.Output, error) {
	var snapshot buildSnapshot
	if err := json.Unmarshal(job.Snapshot, &snapshot); err != nil {
		return takesresearch.Output{}, fmt.Errorf("decode immutable Takes reply snapshot: %w", err)
	}
	hash, err := requestHash(snapshot)
	if err != nil || hash != job.SnapshotHash || snapshot.ThreadID != job.ThreadID || snapshot.UserID != job.UserID || snapshot.Revision != int(job.ReplyRevision) {
		return takesresearch.Output{}, errors.New("immutable Takes reply snapshot does not match its build")
	}
	start, err := w.catalog.CaptureCatalog(ctx)
	if err != nil {
		return takesresearch.Output{}, fmt.Errorf("capture Takes reply start catalog: %w", err)
	}
	startJSON, err := json.Marshal(start)
	if err != nil {
		return takesresearch.Output{}, err
	}
	changed, err := w.queries.SaveBuildStartSnapshot(ctx, replydb.SaveBuildStartSnapshotParams{ID: job.ID, StartSnapshot: startJSON})
	if err != nil {
		return takesresearch.Output{}, fmt.Errorf("persist Takes reply start catalog: %w", err)
	}
	if changed != 1 {
		return takesresearch.Output{}, errors.New("takes reply build no longer owns its start catalog")
	}
	messages := make([]takesresearch.PersistedUserMessage, 0, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		if message.Role != "user" {
			continue
		}
		messages = append(messages, takesresearch.PersistedUserMessage{ID: message.ID.String(), ThreadID: snapshot.ThreadID,
			UserID: snapshot.UserID, Role: message.Role, Content: message.Content, CreatedAt: message.At})
	}
	output, buildErr := w.builder.Build(ctx, takesresearch.Input{BuildID: job.ID.String(), ThreadID: job.ThreadID, UserID: job.UserID,
		ConstructionMode: snapshot.ConstructionMode, BuildCreatedAt: job.CreatedAt, DraftJSON: snapshot.Draft, StartSnapshot: start, UserMessages: messages, UserMessagesLoaded: true, UserMessagesComplete: true})
	if buildErr == nil && snapshot.ConstructionMode == takesresearch.ExpressionMode {
		var publicStatus struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(output.ProposalJSON, &publicStatus) != nil || publicStatus.Status != string(output.Status) {
			return takesresearch.Output{}, errors.New("exploratory worker/public status mismatch")
		}
		if err := takesresearch.ValidateExpressionResult(output.ProposalJSON, job.ID.String(), output.CompletionSnapshot); err != nil {
			return takesresearch.Output{}, err
		}
	}
	return output, buildErr
}

func completeProposal(raw json.RawMessage, buildID string, catalog ...takeintelligence.CatalogSnapshot) bool {
	var mode struct {
		ConstructionMode string `json:"constructionMode"`
		Status           string `json:"status"`
	}
	if json.Unmarshal(raw, &mode) != nil {
		return false
	}
	if mode.ConstructionMode != takesresearch.ExpressionMode {
		return false
	}
	if len(catalog) != 1 || mode.Status != string(takesresearch.StatusReady) {
		return false
	}
	return takesresearch.ValidateExpressionResult(raw, buildID, catalog[0]) == nil
}

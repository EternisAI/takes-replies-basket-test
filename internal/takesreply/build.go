package takesreply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Build struct {
	ID                  uuid.UUID       `json:"id"`
	ReplyRevision       int             `json:"replyRevision"`
	Status              string          `json:"status"`
	CreatedAt           time.Time       `json:"createdAt"`
	CompletedAt         *time.Time      `json:"completedAt,omitempty"`
	Proposal            json.RawMessage `json:"proposal,omitempty"`
	Assessment          json.RawMessage `json:"assessment,omitempty"`
	MissingRequirements json.RawMessage `json:"missingRequirements"`
	FailureMessage      *string         `json:"failureMessage,omitempty"`
}

type buildSnapshot struct {
	ConstructionMode string          `json:"constructionMode,omitempty"`
	ThreadID         string          `json:"threadId"`
	UserID           string          `json:"userId"`
	Revision         int             `json:"revision"`
	AsOf             time.Time       `json:"asOf"`
	Draft            json.RawMessage `json:"draft"`
	Messages         []Message       `json:"messages"`
}

func buildView(row replydb.TakesReplyBuild) *Build {
	return &Build{ID: row.ID, ReplyRevision: int(row.ReplyRevision), Status: row.Status,
		CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt, Proposal: row.Proposal,
		Assessment: row.Assessment, MissingRequirements: row.MissingRequirements, FailureMessage: row.FailureMessage}
}

func (b *Build) FailedFor(revision int) bool {
	return b != nil && b.Status == "FAILED" && b.ReplyRevision == revision
}

func withoutFailedBuildChoice(draft Draft, build *Build) Draft {
	if !build.FailedFor(draft.Revision) {
		return draft
	}
	choices := make([]Choice, 0, len(draft.Choices))
	for _, choice := range draft.Choices {
		if !selectedChoiceRequestsBuild(&choice) {
			choices = append(choices, choice)
		}
	}
	draft.Choices = choices
	return draft
}

func latestBuild(ctx context.Context, q replydb.Querier, threadID, userID string) (*Build, error) {
	row, err := q.GetLatestBuild(ctx, replydb.GetLatestBuildParams{ThreadID: threadID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Takes reply build: %w", err)
	}
	return buildView(row), nil
}

// Enqueuing leaves the reply revision unchanged. Its leased TURN reservation
// is committed before admission, which uses a separate database connection.
func (s *Service) continueBuild(ctx context.Context, userID string, threadID uuid.UUID, input TurnInput, payloadHash string) (TurnResult, bool, error) {
	expected, err := databaseRevision(input.ExpectedRevision)
	if err != nil {
		return TurnResult{}, true, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("begin Takes reply build: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRequestKey(ctx, tx, userID, requestOperationTurn, input.RequestID); err != nil {
		return TurnResult{}, true, err
	}
	q := replydb.New(tx)
	row, err := q.LockConversation(ctx, replydb.LockConversationParams{ThreadID: threadID.String(), UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !IsReplyThread(row.Metadata)) {
		return TurnResult{}, true, ErrConversationNotFound
	}
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("lock Takes reply build source: %w", err)
	}
	prior, priorErr := q.GetRequestForUpdate(ctx, replydb.GetRequestForUpdateParams{UserID: userID, Operation: requestOperationTurn, RequestID: input.RequestID})
	if priorErr == nil {
		if prior.ThreadID != threadID.String() || prior.PayloadHash != payloadHash {
			return TurnResult{}, true, ErrIdempotencyConflict
		}
		if prior.Status == "COMPLETED" && len(prior.Response) > 0 {
			var result TurnResult
			if err := json.Unmarshal(prior.Response, &result); err != nil {
				return TurnResult{}, true, fmt.Errorf("decode Takes reply build replay: %w", err)
			}
			if result.Decision != "BUILD_QUEUED" {
				return result, true, nil
			}
			build, err := q.GetBuildForRevision(ctx, replydb.GetBuildForRevisionParams{ThreadID: threadID.String(), UserID: userID, ReplyRevision: prior.ExpectedRevision})
			if err != nil {
				return TurnResult{}, true, fmt.Errorf("read replayed Takes reply build: %w", err)
			}
			draft, err := decodeBuildDraft(row.DraftState, threadID.String(), row.CurrentRevision)
			if err != nil {
				return TurnResult{}, true, err
			}
			result, err = buildTurnResult(ctx, q, draft, build)
			return result, true, err
		}
		if prior.Status == "RUNNING" && prior.LeaseUntil != nil && prior.LeaseUntil.After(clock.Now()) {
			return TurnResult{}, true, ErrTurnInFlight
		}
	} else if !errors.Is(priorErr, pgx.ErrNoRows) {
		return TurnResult{}, true, fmt.Errorf("read Takes reply build request: %w", priorErr)
	}
	if row.CurrentRevision != expected {
		return TurnResult{}, true, ErrRevisionConflict
	}
	draft, err := decodeBuildDraft(row.DraftState, threadID.String(), row.CurrentRevision)
	if err != nil {
		return TurnResult{}, true, err
	}
	var selected *Choice
	for i := range draft.Choices {
		if draft.Choices[i].ID == *input.SelectedChoiceID {
			selected = &draft.Choices[i]
			break
		}
	}
	if selected == nil {
		return TurnResult{}, true, ErrInvalidChoice
	}
	if !selectedChoiceRequestsBuild(selected) {
		return TurnResult{}, false, nil
	}
	if strings.TrimSpace(valueOrEmpty(input.Message)) != "" || (draft.Stage != "CHOICES" && draft.Stage != "READY_TO_BUILD") || !hasUsableBelief(BeliefOutput{}, draft.Belief) {
		return TurnResult{}, true, ErrInvalidInput
	}
	if active, err := q.HasActiveTurn(ctx, threadID.String()); err != nil {
		return TurnResult{}, true, err
	} else if active {
		return TurnResult{}, true, ErrTurnInFlight
	}
	build, buildErr := q.GetBuildForRevision(ctx, replydb.GetBuildForRevisionParams{ThreadID: threadID.String(), UserID: userID, ReplyRevision: expected})
	if buildErr != nil && !errors.Is(buildErr, pgx.ErrNoRows) {
		return TurnResult{}, true, fmt.Errorf("read Takes reply revision build: %w", buildErr)
	}
	if errors.Is(buildErr, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('takes-reply-build-user:' || $1)::bigint)`, userID); err != nil {
			return TurnResult{}, true, err
		}
		if active, err := q.HasActiveBuild(ctx, userID); err != nil {
			return TurnResult{}, true, err
		} else if active {
			return TurnResult{}, true, ErrBuildActiveElsewhere
		}
	}
	now := clock.Now().UTC()
	leaseUntil := now.Add(turnLease)
	reserved := reservedTurn{UserID: userID, ThreadID: threadID, RequestID: input.RequestID, AttemptID: uuid.New()}
	if priorErr == nil {
		changed, err := q.ReopenRequest(ctx, replydb.ReopenRequestParams{UserID: userID, RequestID: input.RequestID, AttemptID: reserved.AttemptID, LeaseUntil: &leaseUntil})
		if err != nil {
			return TurnResult{}, true, fmt.Errorf("reopen Takes reply build action: %w", err)
		}
		if changed != 1 {
			return TurnResult{}, true, ErrTurnInFlight
		}
	} else if err := q.InsertRequest(ctx, replydb.InsertRequestParams{UserID: userID, Operation: requestOperationTurn, RequestID: input.RequestID,
		ThreadID: threadID.String(), PayloadHash: payloadHash, Status: "RUNNING", ExpectedRevision: expected,
		AttemptID: reserved.AttemptID, LeaseUntil: &leaseUntil, CreatedAt: now}); err != nil {
		return TurnResult{}, true, fmt.Errorf("reserve Takes reply build action: %w", err)
	}
	if buildErr == nil {
		result, err := buildTurnResult(ctx, q, draft, build)
		if err != nil {
			return TurnResult{}, true, err
		}
		if err := completeBuildAction(ctx, q, reserved, result); err != nil {
			return TurnResult{}, true, err
		}
		if err := tx.Commit(ctx); err != nil {
			return TurnResult{}, true, err
		}
		return result, true, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return TurnResult{}, true, fmt.Errorf("commit Takes reply build reservation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			s.failRequest(reserved)
		}
	}()
	finish, err := s.admission.AdmitReplyTurn(ctx, userID)
	if err != nil {
		return TurnResult{}, true, err
	}
	defer func() {
		if !committed {
			finish(false, threadID.String(), "", nil, false)
		}
	}()
	enqueueTx, err := s.pool.Begin(ctx)
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("begin Takes reply build enqueue: %w", err)
	}
	defer func() { _ = enqueueTx.Rollback(ctx) }()
	q = replydb.New(enqueueTx)
	row, err = q.LockConversation(ctx, replydb.LockConversationParams{ThreadID: threadID.String(), UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !IsReplyThread(row.Metadata)) {
		return TurnResult{}, true, ErrConversationNotFound
	}
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("recheck Takes reply build source: %w", err)
	}
	if row.CurrentRevision != expected {
		return TurnResult{}, true, ErrRevisionConflict
	}
	draft, err = decodeBuildDraft(row.DraftState, threadID.String(), row.CurrentRevision)
	if err != nil {
		return TurnResult{}, true, err
	}
	if _, err := enqueueTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('takes-reply-build-user:' || $1)::bigint)`, userID); err != nil {
		return TurnResult{}, true, err
	}
	if active, err := q.HasActiveBuild(ctx, userID); err != nil {
		return TurnResult{}, true, err
	} else if active {
		return TurnResult{}, true, ErrBuildActiveElsewhere
	}
	rows, err := q.GetBuildMessages(ctx, replydb.GetBuildMessagesParams{ThreadID: threadID.String(), ReplyRevision: expected})
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("snapshot Takes reply messages: %w", err)
	}
	messages := make([]Message, 0, len(rows))
	for _, item := range rows {
		messages = append(messages, Message{ID: item.ID, Role: item.Role, Content: item.Content, At: item.CreatedAt.Time})
	}
	now = clock.Now().UTC()
	snapshot := buildSnapshot{ConstructionMode: s.config.ConstructionMode, ThreadID: threadID.String(), UserID: userID, Revision: int(expected), AsOf: now, Draft: row.DraftState, Messages: messages}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return TurnResult{}, true, err
	}
	hash, err := requestHash(snapshot)
	if err != nil {
		return TurnResult{}, true, err
	}
	build, err = q.InsertBuild(ctx, replydb.InsertBuildParams{ID: uuid.New(), ThreadID: threadID.String(), UserID: userID,
		ReplyRevision: expected, RequestID: input.RequestID, SelectedChoiceID: selected.ID, Snapshot: snapshotJSON, SnapshotHash: hash, CreatedAt: now})
	if err != nil {
		return TurnResult{}, true, fmt.Errorf("queue Takes reply build: %w", err)
	}
	result, err := buildTurnResult(ctx, q, draft, build)
	if err != nil {
		return TurnResult{}, true, err
	}
	if err := completeBuildAction(ctx, q, reserved, result); err != nil {
		return TurnResult{}, true, err
	}
	if err := enqueueTx.Commit(ctx); err != nil {
		return TurnResult{}, true, fmt.Errorf("commit Takes reply build: %w", err)
	}
	committed = true
	finish(true, threadID.String(), build.ID.String(), nil, false)
	return result, true, nil
}

func decodeBuildDraft(raw []byte, threadID string, revision int32) (Draft, error) {
	var draft Draft
	if err := json.Unmarshal(raw, &draft); err != nil {
		return Draft{}, fmt.Errorf("decode Takes reply build source: %w", err)
	}
	if draft.Revision != int(revision) || draft.ThreadID != threadID {
		return Draft{}, errors.New("takes reply build source revision is inconsistent")
	}
	return withReadyBuildChoice(draft), nil
}

func buildTurnResult(ctx context.Context, q replydb.Querier, draft Draft, build replydb.TakesReplyBuild) (TurnResult, error) {
	messages, err := q.GetMessages(ctx, draft.ThreadID)
	if err != nil {
		return TurnResult{}, err
	}
	converted, truncated := conversationMessages(messages)
	view := buildView(build)
	conversation := Conversation{ThreadID: draft.ThreadID, ThreadKind: "TAKES", Draft: withoutFailedBuildChoice(draft, view), Messages: converted, MessagesTruncated: truncated, Build: view}
	raw, err := json.Marshal(conversation)
	if err != nil {
		return TurnResult{}, err
	}
	message := buildMessage(build.Status)
	return TurnResult{Outcome: "ACCEPTED", Conversation: raw, Decision: "BUILD_QUEUED", AssistantMessage: &message,
		DecisionPoint: json.RawMessage(`{"kind":"none","conclusion":"","factIndices":[]}`)}, nil
}

func completeBuildAction(ctx context.Context, q replydb.Querier, reserved reservedTurn, result TurnResult) error {
	response, err := json.Marshal(result)
	if err != nil {
		return err
	}
	now := clock.Now().UTC()
	changed, err := q.CompleteRequest(ctx, replydb.CompleteRequestParams{UserID: reserved.UserID, RequestID: reserved.RequestID, AttemptID: reserved.AttemptID, Response: response, CompletedAt: &now})
	if err != nil {
		return fmt.Errorf("complete Takes reply build action: %w", err)
	}
	if changed != 1 {
		return errors.New("takes reply build reservation changed before enqueue")
	}
	return nil
}

func buildMessage(status string) string {
	switch status {
	case "COMPLETED":
		return "Your proposed basket is ready."
	case "PARTIAL":
		return "The build finished with gaps. Review the missing requirements."
	case "FAILED":
		return "The basket build failed. Review its status before starting a new build."
	case "RUNNING":
		return "Your proposed basket is being built."
	default:
		return "Your proposed basket is queued."
	}
}

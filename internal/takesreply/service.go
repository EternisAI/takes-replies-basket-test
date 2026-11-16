package takesreply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/secrets"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	threadMarkerKey         = "takes_reply"
	threadMarkerValue       = "v1"
	messageMarkerKey        = "takes_reply"
	requestOperationCreate  = "CREATE"
	requestOperationTurn    = "TURN"
	defaultReplyModel       = "anthropic/claude-sonnet-5.5"
	defaultEvidenceModel    = "google/gemini-3.8-flash"
	defaultReplyTokens      = 6000
	defaultEvidenceTokens   = 1800
	defaultReplyTimeout     = 25 * time.Second
	defaultEvidenceTimeout  = 18 * time.Second
	turnLease               = 90 * time.Second
	maxTurnMessageRunes     = 4000
	maxConversationMessages = 100
	requestCleanupTimeout   = 3 * time.Second
	maxDatabaseRevision     = 1<<31 - 1
)

var (
	ErrConversationNotFound = errors.New("takes reply conversation not found")
	ErrTurnInFlight         = errors.New("a Takes reply turn is already in progress")
	ErrBuildActiveElsewhere = errors.New("another Takes reply build is already active")
	ErrRevisionConflict     = errors.New("takes reply conversation revision changed")
	ErrInvalidChoice        = errors.New("selected Takes reply choice is unavailable")
	ErrIdempotencyConflict  = errors.New("request ID was already used for different Takes reply input")
	ErrInvalidInput         = errors.New("invalid Takes reply input")
)

type Config struct {
	ConstructionMode string `mapstructure:"constructionMode"`
	ReplyModel       string `mapstructure:"replyModel"`
	EvidenceModel    string `mapstructure:"evidenceModel"`
}

func databaseRevision(value int) (int32, error) {
	if value < 0 || value > maxDatabaseRevision {
		return 0, errors.New("takes reply revision is outside the supported range")
	}
	return int32(value), nil
}

func nextDatabaseRevision(current int32) (int32, error) {
	if current < 0 || current == int32(maxDatabaseRevision) {
		return 0, errors.New("takes reply conversation revision cannot advance")
	}
	return current + 1, nil
}

func (c Config) normalized() Config {
	if c.ConstructionMode == "" {
		c.ConstructionMode = takesresearch.ExpressionMode
	}
	if strings.TrimSpace(c.ReplyModel) == "" {
		c.ReplyModel = defaultReplyModel
	}
	if strings.TrimSpace(c.EvidenceModel) == "" {
		c.EvidenceModel = defaultEvidenceModel
	}
	c.ConstructionMode = strings.ToUpper(strings.TrimSpace(c.ConstructionMode))
	c.ReplyModel = strings.TrimSpace(c.ReplyModel)
	c.EvidenceModel = strings.TrimSpace(c.EvidenceModel)
	return c
}

type Admission interface {
	AdmitReplyTurn(context.Context, string) (func(bool, string, string, []llm.Usage, bool), error)
}

type Service struct {
	pool      *pgxpool.Pool
	queries   replydb.Querier
	completer llm.Completer
	llm       llm.Completer
	admission Admission
	config    Config
	logger    *slog.Logger

	// These aliases are consumed by the accepted prompt/evidence primitives.
	model                       string
	conversationModel           string
	conversationMaxTokens       int
	conversationReasoningEffort string
	evidenceReasoningEffort     string
	decisionEvidenceTransport   http.RoundTripper
}

func NewService(pool *pgxpool.Pool, queries replydb.Querier, completer llm.Completer, admission Admission, cfg Config, logger *slog.Logger) (*Service, error) {
	cfg = cfg.normalized()
	if cfg.ConstructionMode != takesresearch.ExpressionMode {
		return nil, errors.New("unsupported server basket construction mode")
	}
	if pool == nil || queries == nil || completer == nil || admission == nil || logger == nil {
		return nil, errors.New("takes reply service requires storage, model, admission, and logger")
	}
	observedCompleter := attemptObservingCompleter{Completer: completer}
	return &Service{
		pool: pool, queries: queries, completer: observedCompleter, llm: observedCompleter, admission: admission, config: cfg, logger: logger,
		model: cfg.EvidenceModel, conversationModel: cfg.ReplyModel,
		conversationMaxTokens: defaultReplyTokens, conversationReasoningEffort: "high", evidenceReasoningEffort: "low",
	}, nil
}

type turnContext struct {
	AsOf               time.Time                `json:"asOf"`
	Draft              Draft                    `json:"draft"`
	RecentMessages     []Message                `json:"recentMessages"`
	CurrentMessage     string                   `json:"currentMessage"`
	SelectedChoice     *Choice                  `json:"selectedChoice"`
	RequestedBuild     bool                     `json:"requestedBuild"`
	EvidenceCheck      EvidenceCheckOutput      `json:"evidenceCheck"`
	HistoricalEvidence HistoricalEvidenceOutput `json:"historicalEvidence"`
}

type reservedTurn struct {
	UserID             string
	ThreadID           uuid.UUID
	RequestID          uuid.UUID
	AttemptID          uuid.UUID
	MessageID          uuid.UUID
	AssistantMessageID uuid.UUID
	Draft              Draft
	Message            string
	Selected           *Choice
	PayloadHash        string
}

func (s *Service) CreateConversation(ctx context.Context, userID string, requestID uuid.UUID) (Conversation, error) {
	if s == nil {
		return Conversation{}, errors.New("takes reply service is unavailable")
	}
	if strings.TrimSpace(userID) == "" {
		return Conversation{}, errors.New("authenticated user is required")
	}
	if requestID == uuid.Nil {
		return Conversation{}, fmt.Errorf("%w: request ID is required", ErrInvalidInput)
	}
	input := struct {
		Operation string    `json:"operation"`
		RequestID uuid.UUID `json:"requestId"`
	}{Operation: requestOperationCreate, RequestID: requestID}
	hash, err := requestHash(input)
	if err != nil {
		return Conversation{}, err
	}
	threadID, attemptID := uuid.New(), uuid.New()
	now := clock.Now().UTC()
	draft := newDraft(threadID.String(), now)
	draftJSON, err := json.Marshal(draft)
	if err != nil {
		return Conversation{}, fmt.Errorf("encode initial Takes reply state: %w", err)
	}
	conversation := Conversation{ThreadID: threadID.String(), ThreadKind: "TAKES", Draft: draft, Messages: []Message{}}
	response, err := json.Marshal(conversation)
	if err != nil {
		return Conversation{}, fmt.Errorf("encode initial Takes reply response: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Conversation{}, fmt.Errorf("begin Takes reply conversation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRequestKey(ctx, tx, userID, requestOperationCreate, requestID); err != nil {
		return Conversation{}, err
	}
	q := replydb.New(tx)
	prior, err := q.GetRequestForUpdate(ctx, replydb.GetRequestForUpdateParams{UserID: userID, Operation: requestOperationCreate, RequestID: requestID})
	if err == nil {
		if prior.PayloadHash != hash {
			return Conversation{}, ErrIdempotencyConflict
		}
		if prior.Status == "COMPLETED" && len(prior.Response) > 0 {
			row, lookupErr := q.LockConversation(ctx, replydb.LockConversationParams{ThreadID: prior.ThreadID, UserID: userID})
			if errors.Is(lookupErr, pgx.ErrNoRows) || (lookupErr == nil && !IsReplyThread(row.Metadata)) {
				return Conversation{}, ErrConversationNotFound
			}
			if lookupErr != nil {
				return Conversation{}, fmt.Errorf("check prior Takes reply conversation: %w", lookupErr)
			}
			var existing Conversation
			if err := json.Unmarshal(prior.Response, &existing); err != nil {
				return Conversation{}, fmt.Errorf("decode prior Takes reply creation: %w", err)
			}
			return existing, nil
		}
		return Conversation{}, ErrTurnInFlight
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, fmt.Errorf("read Takes reply creation request: %w", err)
	}
	metadata, _ := json.Marshal(map[string]string{threadMarkerKey: threadMarkerValue})
	if err := q.CreateThread(ctx, replydb.CreateThreadParams{ThreadID: threadID.String(), UserID: userID, Metadata: metadata}); err != nil {
		return Conversation{}, fmt.Errorf("create Takes reply thread: %w", err)
	}
	if err := q.CreateConversation(ctx, replydb.CreateConversationParams{ThreadID: threadID.String(), UserID: userID, DraftState: draftJSON, CreatedAt: now}); err != nil {
		return Conversation{}, fmt.Errorf("create Takes reply state: %w", err)
	}
	if err := q.InsertRequest(ctx, replydb.InsertRequestParams{
		UserID: userID, Operation: requestOperationCreate, RequestID: requestID, ThreadID: threadID.String(),
		PayloadHash: hash, Status: "COMPLETED", ExpectedRevision: 0, AttemptID: attemptID,
		Response: response, CreatedAt: now,
	}); err != nil {
		return Conversation{}, fmt.Errorf("save Takes reply creation request: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Conversation{}, fmt.Errorf("commit Takes reply conversation: %w", err)
	}
	return conversation, nil
}

func (s *Service) Conversation(ctx context.Context, userID, threadID string) (Conversation, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(threadID))
	if err != nil || strings.TrimSpace(userID) == "" {
		return Conversation{}, ErrConversationNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Conversation{}, fmt.Errorf("begin Takes reply read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := replydb.New(tx)
	row, err := q.GetConversation(ctx, replydb.GetConversationParams{ThreadID: parsed.String(), UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrConversationNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("read Takes reply conversation: %w", err)
	}
	if !IsReplyThread(row.Metadata) {
		return Conversation{}, ErrConversationNotFound
	}
	var draft Draft
	if err := json.Unmarshal(row.DraftState, &draft); err != nil {
		return Conversation{}, fmt.Errorf("decode Takes reply state: %w", err)
	}
	if draft.ThreadID != row.ThreadID || draft.Revision != int(row.CurrentRevision) {
		return Conversation{}, errors.New("takes reply state revision is inconsistent")
	}
	rows, err := q.GetMessages(ctx, row.ThreadID)
	if err != nil {
		return Conversation{}, fmt.Errorf("read Takes reply messages: %w", err)
	}
	messages, truncated := conversationMessages(rows)
	build, err := latestBuild(ctx, q, row.ThreadID, userID)
	if err != nil {
		return Conversation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Conversation{}, fmt.Errorf("finish Takes reply read: %w", err)
	}
	return Conversation{ThreadID: row.ThreadID, ThreadKind: "TAKES", Draft: withoutFailedBuildChoice(withReadyBuildChoice(draft), build), Messages: messages, MessagesTruncated: truncated, Build: build}, nil
}

func IsReplyThread(metadata []byte) bool {
	var values map[string]json.RawMessage
	if json.Unmarshal(metadata, &values) != nil {
		return false
	}
	var marker string
	return json.Unmarshal(values[threadMarkerKey], &marker) == nil && marker == threadMarkerValue
}

func (s *Service) Continue(ctx context.Context, userID string, input TurnInput) (result TurnResult, err error) {
	if s == nil {
		return TurnResult{}, errors.New("takes reply service is unavailable")
	}
	if strings.TrimSpace(userID) == "" {
		return TurnResult{}, errors.New("authenticated user is required")
	}
	if input.Message != nil && secrets.HasWalletSecret(*input.Message) {
		return TurnResult{}, fmt.Errorf("%w: message contains a private wallet credential", ErrInvalidInput)
	}
	threadID, err := uuid.Parse(strings.TrimSpace(input.ThreadID))
	if err != nil || input.RequestID == uuid.Nil || input.ExpectedRevision < 0 || input.ExpectedRevision > maxDatabaseRevision {
		return TurnResult{}, fmt.Errorf("%w: thread, supported revision, and request ID are required", ErrInvalidInput)
	}
	message := strings.TrimSpace(valueOrEmpty(input.Message))
	if (message == "" && input.SelectedChoiceID == nil) || utf8.RuneCountInString(message) > maxTurnMessageRunes {
		return TurnResult{}, fmt.Errorf("%w: a message or selected choice is required and the message must be within the supported length", ErrInvalidInput)
	}
	payloadHash, err := requestHash(input)
	if err != nil {
		return TurnResult{}, err
	}
	if input.SelectedChoiceID != nil {
		built, handled, buildErr := s.continueBuild(ctx, userID, threadID, input, payloadHash)
		if errors.Is(buildErr, ErrRevisionConflict) || errors.Is(buildErr, ErrTurnInFlight) {
			current, readErr := s.Conversation(ctx, userID, threadID.String())
			if readErr != nil {
				return TurnResult{}, readErr
			}
			outcome := "REVISION_CONFLICT"
			if errors.Is(buildErr, ErrTurnInFlight) {
				outcome = "PROCESSING"
			}
			return nonAcceptedTurnResult(outcome, current)
		}
		if buildErr != nil || handled {
			return built, buildErr
		}
	}
	reserved, retryResponse, processing, err := s.reserveTurn(ctx, userID, threadID, input, payloadHash)
	if errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrTurnInFlight) {
		current, readErr := s.Conversation(ctx, userID, threadID.String())
		if readErr != nil {
			return TurnResult{}, readErr
		}
		outcome := "REVISION_CONFLICT"
		if errors.Is(err, ErrTurnInFlight) {
			outcome = "PROCESSING"
		}
		return nonAcceptedTurnResult(outcome, current)
	}
	if err != nil {
		return TurnResult{}, err
	}
	if len(retryResponse) > 0 {
		if err := json.Unmarshal(retryResponse, &result); err != nil {
			return TurnResult{}, fmt.Errorf("decode prior Takes reply result: %w", err)
		}
		return result, nil
	}
	if processing {
		current, readErr := s.Conversation(ctx, userID, threadID.String())
		if readErr != nil {
			return TurnResult{}, readErr
		}
		return nonAcceptedTurnResult("PROCESSING", current)
	}

	committed := false
	finished := false
	var usageMu sync.Mutex
	var usage []llm.Usage
	var attempts int
	defer func() {
		if !committed {
			s.failRequest(reserved)
		}
	}()
	finish, err := s.admission.AdmitReplyTurn(ctx, userID)
	if err != nil {
		return TurnResult{}, err
	}
	// Install this immediately after admission. It releases the user's slot
	// and settles observed usage on errors, cancellation, or panic.
	defer func() {
		if !finished {
			usageMu.Lock()
			calls := append([]llm.Usage(nil), usage...)
			unknownUsageAttempt := attempts > len(calls)
			usageMu.Unlock()
			finish(false, reserved.ThreadID.String(), "", calls, unknownUsageAttempt)
		}
	}()
	callCtx := withCompletionAttemptObserver(ctx, func() {
		usageMu.Lock()
		attempts++
		usageMu.Unlock()
	})
	callCtx = llm.WithUsageObserver(callCtx, func(item llm.Usage) {
		usageMu.Lock()
		usage = append(usage, item)
		usageMu.Unlock()
	})

	recent, err := s.recentMessages(ctx, reserved.ThreadID)
	if err != nil {
		return TurnResult{}, err
	}
	requestedBuild := explicitBuildRequest(reserved.Message) || selectedChoiceRequestsBuild(reserved.Selected)
	asOf := clock.Now().UTC()
	replyContext := turnContext{
		AsOf:  asOf,
		Draft: reserved.Draft, RecentMessages: recent, CurrentMessage: reserved.Message,
		SelectedChoice: reserved.Selected, RequestedBuild: requestedBuild,
	}
	replyContext.EvidenceCheck, replyContext.HistoricalEvidence = s.prebuildEvidenceContextAt(callCtx, reserved.Message, requestedBuild, asOf)
	request, err := acceptedReplyCompletionRequestForTurnContext(
		s.conversationCompletionModel(), s.conversationCompletionEffort(), s.conversationCompletionMaxTokens(),
		isInitialBrandPreferenceReply(reserved.Draft, recent, reserved.Selected, requestedBuild, reserved.Message), replyContext,
	)
	if err != nil {
		return TurnResult{}, err
	}
	replyCtx, cancel := context.WithTimeout(callCtx, defaultReplyTimeout)
	content, err := s.completer.Complete(replyCtx, request)
	cancel()
	if err != nil {
		return TurnResult{}, fmt.Errorf("takes reply model: %w", err)
	}
	output, decisionPoint, err := decodeDecisionPointTurnOutputForContext(content, replyContext)
	if err != nil {
		return TurnResult{}, fmt.Errorf("decode Takes reply model output: %w", err)
	}
	output.AssistantMessage = cleanText(output.AssistantMessage, 1800)
	if err := validateTurnOutput(output); err != nil {
		return TurnResult{}, err
	}
	output = normalizeUnsubmittedBuildHandoff(output)
	output = normalizeConversationChoices(output, reserved.Draft.Belief)
	if err := validateConversationChoices(output); err != nil {
		return TurnResult{}, err
	}
	newDraft := applyTurnOutput(reserved.Draft, output, reserved.MessageID, input.SelectedChoiceID, reserved.Message, clock.Now())
	if output.Decision == "READY_TO_BUILD" {
		newDraft.Stage = "READY_TO_BUILD"
	}
	result, err = s.commitTurn(ctx, userID, reserved, input, newDraft, output, decisionPoint, replyContext.EvidenceCheck)
	if err != nil {
		return TurnResult{}, err
	}
	committed = true
	usageMu.Lock()
	calls := append([]llm.Usage(nil), usage...)
	usageMu.Unlock()
	finish(true, reserved.ThreadID.String(), reserved.AssistantMessageID.String(), calls, false)
	finished = true
	return result, nil
}

func normalizeUnsubmittedBuildHandoff(output TurnModelOutput) TurnModelOutput {
	switch output.Decision {
	case "BUILD_QUEUED":
		output.Decision = "READY_TO_BUILD"
		output.Stage = "READY_TO_BUILD"
		output.AssistantMessage = "The idea is ready for basket research."
	case "READY_TO_BUILD":
		output.Stage = "READY_TO_BUILD"
		if claimsBuildStarted(output.AssistantMessage) {
			output.AssistantMessage = "The idea is ready for basket research."
		}
	default:
		if claimsBuildStarted(output.AssistantMessage) {
			output.AssistantMessage = "I can keep helping refine the idea."
		}
	}
	// Model output does not authorize queued work. Only a saved Build action
	// submitted by the user can create the durable job.
	output.BuildRequested = false
	return output
}

func claimsBuildStarted(message string) bool {
	lower := strings.ToLower(message)
	for _, phrase := range []string{
		"research has started", "research started", "research is underway", "research is in progress",
		"i've started researching", "i started researching", "we've started researching", "we started researching",
		"build has started", "build is underway", "build is in progress", "queued the build", "build is queued",
		"build queued", "basket is being built", "queued your basket", "choose build", "click build",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func requiredProvider(name string) *llm.ProviderRouting {
	allowFallbacks := false
	return &llm.ProviderRouting{Order: []string{name}, AllowFallbacks: &allowFallbacks, RequireParameters: true}
}

func (s *Service) prebuildEvidenceContextAt(ctx context.Context, message string, requestedBuild bool, asOf time.Time) (EvidenceCheckOutput, HistoricalEvidenceOutput) {
	asOf = asOf.UTC()
	check := EvidenceCheckOutput{AsOfUTC: asOf, Status: "SKIPPED", Sources: []EvidenceCheckSource{}, Facts: []EvidenceCheckFact{},
		Limitation: stringPtr("No bounded current-evidence check was needed for this turn.")}
	historical := HistoricalEvidenceOutput{Sources: []EvidenceCheckSource{}, Facts: []EvidenceCheckFact{}}
	if requestedBuild {
		check.Limitation = stringPtr("A later build step requires its own research; no research or basket was started in this conversation.")
		return check, historical
	}
	if !needsCurrentEvidenceCheck(message) {
		return check, historical
	}
	check, historical, _ = s.prebuildDecisionEvidenceCheckAt(ctx, message, asOf)
	return check, historical
}

func (s *Service) reserveTurn(ctx context.Context, userID string, threadID uuid.UUID, input TurnInput, payloadHash string) (reservedTurn, []byte, bool, error) {
	expectedRevision, err := databaseRevision(input.ExpectedRevision)
	if err != nil {
		return reservedTurn{}, nil, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("begin Takes reply turn: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockRequestKey(ctx, tx, userID, requestOperationTurn, input.RequestID); err != nil {
		return reservedTurn{}, nil, false, err
	}
	q := replydb.New(tx)
	conversationRow, err := q.LockConversation(ctx, replydb.LockConversationParams{ThreadID: threadID.String(), UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return reservedTurn{}, nil, false, ErrConversationNotFound
	}
	if err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("lock Takes reply conversation: %w", err)
	}
	if !IsReplyThread(conversationRow.Metadata) {
		return reservedTurn{}, nil, false, ErrConversationNotFound
	}
	currentRevision := conversationRow.CurrentRevision
	prior, priorErr := q.GetRequestForUpdate(ctx, replydb.GetRequestForUpdateParams{UserID: userID, Operation: requestOperationTurn, RequestID: input.RequestID})
	if priorErr == nil {
		if prior.ThreadID != threadID.String() || prior.PayloadHash != payloadHash {
			return reservedTurn{}, nil, false, ErrIdempotencyConflict
		}
		if prior.Status == "COMPLETED" && len(prior.Response) > 0 {
			return reservedTurn{}, append([]byte(nil), prior.Response...), false, nil
		}
		if prior.Status == "RUNNING" && prior.LeaseUntil != nil && prior.LeaseUntil.After(clock.Now()) {
			return reservedTurn{}, nil, true, nil
		}
	} else if !errors.Is(priorErr, pgx.ErrNoRows) {
		return reservedTurn{}, nil, false, fmt.Errorf("read Takes reply request: %w", priorErr)
	}
	if currentRevision != expectedRevision {
		return reservedTurn{}, nil, false, ErrRevisionConflict
	}
	if _, err := nextDatabaseRevision(currentRevision); err != nil {
		return reservedTurn{}, nil, false, err
	}
	if active, err := q.HasActiveTurn(ctx, threadID.String()); err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("check active Takes reply turn: %w", err)
	} else if active {
		return reservedTurn{}, nil, false, ErrTurnInFlight
	}
	var draft Draft
	if err := json.Unmarshal(conversationRow.DraftState, &draft); err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("decode Takes reply state: %w", err)
	}
	if draft.Revision != int(currentRevision) || draft.ThreadID != threadID.String() {
		return reservedTurn{}, nil, false, errors.New("takes reply state revision is inconsistent")
	}
	var selected *Choice
	if input.SelectedChoiceID != nil {
		for i := range draft.Choices {
			if draft.Choices[i].ID == *input.SelectedChoiceID {
				choice := draft.Choices[i]
				choice.InstrumentRefs = []string{}
				selected = &choice
				break
			}
		}
		if selected == nil {
			return reservedTurn{}, nil, false, ErrInvalidChoice
		}
	}
	message := strings.TrimSpace(valueOrEmpty(input.Message))
	if selected != nil {
		if message != "" {
			message += "\n\n"
		}
		message += "Selected option: " + selected.Label
	}
	if utf8.RuneCountInString(message) > maxTurnMessageRunes {
		return reservedTurn{}, nil, false, fmt.Errorf("%w: the message with its selected option exceeds the supported length", ErrInvalidInput)
	}
	attemptID, messageID, assistantID := uuid.New(), uuid.New(), uuid.New()
	now := clock.Now().UTC()
	leaseUntil := now.Add(turnLease)
	if priorErr == nil {
		updated, err := q.ReopenRequest(ctx, replydb.ReopenRequestParams{AttemptID: attemptID, LeaseUntil: &leaseUntil, UserID: userID, RequestID: input.RequestID})
		if err != nil {
			return reservedTurn{}, nil, false, fmt.Errorf("reopen Takes reply request: %w", err)
		}
		if updated != 1 {
			return reservedTurn{}, nil, false, ErrTurnInFlight
		}
	} else if err := q.InsertRequest(ctx, replydb.InsertRequestParams{
		UserID: userID, Operation: requestOperationTurn, RequestID: input.RequestID, ThreadID: threadID.String(),
		PayloadHash: payloadHash, Status: "RUNNING", ExpectedRevision: expectedRevision,
		AttemptID: attemptID, LeaseUntil: &leaseUntil, CreatedAt: now,
	}); err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("reserve Takes reply turn: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return reservedTurn{}, nil, false, fmt.Errorf("commit Takes reply turn reservation: %w", err)
	}
	return reservedTurn{
		UserID: userID, ThreadID: threadID, RequestID: input.RequestID, AttemptID: attemptID,
		MessageID: messageID, AssistantMessageID: assistantID, Draft: draft, Message: message,
		Selected: selected, PayloadHash: payloadHash,
	}, nil, false, nil
}

func (s *Service) commitTurn(ctx context.Context, userID string, reserved reservedTurn, input TurnInput, draft Draft, output TurnModelOutput, point DecisionPointOutput, evidence EvidenceCheckOutput) (TurnResult, error) {
	newRevision, err := databaseRevision(draft.Revision)
	if err != nil {
		return TurnResult{}, err
	}
	expectedRevision, err := databaseRevision(input.ExpectedRevision)
	if err != nil {
		return TurnResult{}, err
	}
	now := clock.Now().UTC()
	draftJSON, err := json.Marshal(draft)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode Takes reply draft: %w", err)
	}
	userMessage := Message{ID: reserved.MessageID, Role: "user", Content: reserved.Message, At: now}
	assistantMessage := Message{ID: reserved.AssistantMessageID, Role: "assistant", Content: output.AssistantMessage, At: now}
	conversation, err := s.conversationWithNewMessages(ctx, userID, reserved.ThreadID, draft, userMessage, assistantMessage)
	if err != nil {
		return TurnResult{}, err
	}
	conversationJSON, err := json.Marshal(conversation)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode Takes reply response conversation: %w", err)
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode Takes reply evidence check: %w", err)
	}
	pointJSON, err := json.Marshal(point)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode Takes reply decision point: %w", err)
	}
	result := TurnResult{Outcome: "ACCEPTED", Conversation: conversationJSON, AssistantMessage: &output.AssistantMessage,
		Decision: output.Decision, DecisionPoint: pointJSON, EvidenceCheck: evidenceJSON}
	response, err := json.Marshal(result)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode Takes reply request result: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TurnResult{}, fmt.Errorf("begin Takes reply commit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := replydb.New(tx)
	rows, err := q.UpdateConversation(ctx, replydb.UpdateConversationParams{
		NewRevision: newRevision, DraftState: draftJSON, UpdatedAt: now,
		ThreadID: reserved.ThreadID.String(), UserID: userID, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("update Takes reply revision: %w", err)
	}
	if rows != 1 {
		return TurnResult{}, ErrRevisionConflict
	}
	userMetadata, _ := json.Marshal(map[string]any{
		messageMarkerKey: threadMarkerValue, "requestId": input.RequestID.String(),
		"selectedChoiceId": input.SelectedChoiceID, "takes_reply_revision": draft.Revision, "takes_reply_order": 0,
	})
	assistantMetadata, _ := json.Marshal(map[string]any{
		messageMarkerKey: threadMarkerValue, "decision": output.Decision, "decisionPoint": point, "evidenceCheck": evidence,
		"takes_reply_revision": draft.Revision, "takes_reply_order": 1,
	})
	if err := q.InsertMessage(ctx, replydb.InsertMessageParams{ID: userMessage.ID, ThreadID: reserved.ThreadID.String(), Role: "user", Content: userMessage.Content, Metadata: userMetadata, CreatedAt: pgtype.Timestamp{Time: now, Valid: true}}); err != nil {
		return TurnResult{}, fmt.Errorf("save Takes reply user message: %w", err)
	}
	if err := q.InsertMessage(ctx, replydb.InsertMessageParams{ID: assistantMessage.ID, ThreadID: reserved.ThreadID.String(), Role: "assistant", Content: assistantMessage.Content, Metadata: assistantMetadata, CreatedAt: pgtype.Timestamp{Time: now, Valid: true}}); err != nil {
		return TurnResult{}, fmt.Errorf("save Takes reply assistant message: %w", err)
	}
	completed, err := q.CompleteRequest(ctx, replydb.CompleteRequestParams{Response: response, CompletedAt: &now, UserID: userID, RequestID: input.RequestID, AttemptID: reserved.AttemptID})
	if err != nil {
		return TurnResult{}, fmt.Errorf("complete Takes reply request: %w", err)
	}
	if completed != 1 {
		return TurnResult{}, errors.New("takes reply request lease changed before commit")
	}
	if err := tx.Commit(ctx); err != nil {
		return TurnResult{}, fmt.Errorf("commit Takes reply response: %w", err)
	}
	return result, nil
}

func (s *Service) conversationWithNewMessages(ctx context.Context, userID string, threadID uuid.UUID, draft Draft, added ...Message) (Conversation, error) {
	row, err := s.queries.GetConversation(ctx, replydb.GetConversationParams{ThreadID: threadID.String(), UserID: userID})
	if err != nil || !IsReplyThread(row.Metadata) {
		return Conversation{}, ErrConversationNotFound
	}
	messages, err := s.queries.GetMessages(ctx, threadID.String())
	if err != nil {
		return Conversation{}, fmt.Errorf("read current Takes reply messages: %w", err)
	}
	converted, truncated := conversationMessages(messages)
	converted = append(converted, added...)
	if len(converted) > maxConversationMessages {
		converted = converted[len(converted)-maxConversationMessages:]
		truncated = true
	}
	build, err := latestBuild(ctx, s.queries, threadID.String(), userID)
	if err != nil {
		return Conversation{}, err
	}
	return Conversation{ThreadID: threadID.String(), ThreadKind: "TAKES", Draft: draft, Messages: converted, MessagesTruncated: truncated, Build: build}, nil
}

func conversationMessages(rows []replydb.GetMessagesRow) ([]Message, bool) {
	truncated := len(rows) > maxConversationMessages
	if truncated {
		rows = rows[len(rows)-maxConversationMessages:]
	}
	messages := make([]Message, 0, len(rows))
	for _, item := range rows {
		at := time.Time{}
		if item.CreatedAt.Valid {
			at = item.CreatedAt.Time
		}
		messages = append(messages, Message{ID: item.ID, Role: item.Role, Content: item.Content, At: at})
	}
	return messages, truncated
}

func (s *Service) recentMessages(ctx context.Context, threadID uuid.UUID) ([]Message, error) {
	rows, err := s.queries.GetRecentMessages(ctx, threadID.String())
	if err != nil {
		return nil, fmt.Errorf("read Takes reply history: %w", err)
	}
	messages := make([]Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		at := time.Time{}
		if rows[i].CreatedAt.Valid {
			at = rows[i].CreatedAt.Time
		}
		messages = append(messages, Message{ID: rows[i].ID, Role: rows[i].Role, Content: rows[i].Content, At: at})
	}
	return messages, nil
}

func (s *Service) failRequest(reserved reservedTurn) {
	ctx, cancel := context.WithTimeout(context.Background(), requestCleanupTimeout)
	defer cancel()
	_, err := s.queries.FailRequest(ctx, replydb.FailRequestParams{CompletedAt: ptrTime(clock.Now().UTC()), UserID: reserved.UserID, RequestID: reserved.RequestID, AttemptID: reserved.AttemptID})
	if err != nil {
		s.logger.ErrorContext(ctx, "mark Takes reply request failed", "request_id", reserved.RequestID, "error", err)
	}
}

func lockRequestKey(ctx context.Context, tx pgx.Tx, userID, operation string, requestID uuid.UUID) error {
	key := userID + ":" + operation + ":" + requestID.String()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, key); err != nil {
		return fmt.Errorf("lock Takes reply request: %w", err)
	}
	return nil
}

func ptrTime(value time.Time) *time.Time { return &value }

func emptyDecisionPoint() json.RawMessage {
	return json.RawMessage(`{"kind":"none","conclusion":"","factIndices":[]}`)
}

func nonAcceptedTurnResult(outcome string, conversation Conversation) (TurnResult, error) {
	conversationJSON, err := json.Marshal(conversation)
	if err != nil {
		return TurnResult{}, fmt.Errorf("encode nonaccepted Takes reply conversation: %w", err)
	}
	return TurnResult{Outcome: outcome, Conversation: conversationJSON, Decision: "REPLY", DecisionPoint: emptyDecisionPoint()}, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

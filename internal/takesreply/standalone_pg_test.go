package takesreply

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type isolatedReplyDB struct {
	pool *pgxpool.Pool
}

func newIsolatedReplyDB(t *testing.T) *isolatedReplyDB {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TAKES_REPLY_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set TAKES_REPLY_TEST_DATABASE_URL to an isolated PostgreSQL instance")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "takes_reply_standalone_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaSQL := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaSQL); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schemaSQL+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	for _, name := range []string{"0001_host_parent_tables.sql", "0002_reply_model_usage.sql", "0247_takes_reply.sql", "0248_takes_reply_builds.sql"} {
		data, err := os.ReadFile(filepath.Join(root, "storage", "pg", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		up := strings.SplitN(string(data), "-- +goose Down", 2)[0]
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s in disposable schema: %v", name, err)
		}
	}
	return &isolatedReplyDB{pool: pool}
}

type queuedReplyCompleter struct {
	response string
	calls    atomic.Int32
	err      error
}

func (c *queuedReplyCompleter) Complete(context.Context, llm.CompletionRequest) (string, error) {
	c.calls.Add(1)
	return c.response, c.err
}

func (c *queuedReplyCompleter) SupportsOpenRouterWebSearch() bool { return false }

type noChargeAdmission struct{ calls atomic.Int32 }

func (a *noChargeAdmission) AdmitReplyTurn(context.Context, string) (func(bool, string, string, []llm.Usage, bool), error) {
	a.calls.Add(1)
	return func(bool, string, string, []llm.Usage, bool) {}, nil
}

func replyFixture(t *testing.T, output TurnModelOutput) string {
	t.Helper()
	value := map[string]any{
		"decisionPoint": DecisionPointOutput{Kind: "none", Conclusion: "", FactIndices: []int{}},
		"turn":          output,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func readyChoiceReplyFixture(t *testing.T, statement string) string {
	t.Helper()
	return replyFixture(t, TurnModelOutput{
		AssistantMessage: "Choose a route for this idea.", Decision: "REPLY", Stage: "CHOICES",
		Belief:            BeliefOutput{UserStatement: &statement, IntentStatus: "INVESTMENT_THESIS", ThesisStatus: "NOT_ESTABLISHED"},
		Horizon:           HorizonOutput{Days: nil, UserSpecified: false},
		Choices:           []ChoiceOutput{{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, Label: "Robotics suppliers", ExposureRole: "category", Description: "Explore companies that may supply robotics systems."}},
		SelectedChoiceIDs: []string{}, Criteria: []string{}, Exclusions: []string{}, Preferences: []PreferenceOutput{},
	})
}

func newPGReplyService(t *testing.T, pool *pgxpool.Pool, completer llm.Completer) (*Service, *noChargeAdmission) {
	t.Helper()
	admission := &noChargeAdmission{}
	service, err := NewService(pool, replydb.New(pool), completer, admission, Config{ConstructionMode: takesresearch.ExpressionMode}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return service, admission
}

func TestReplyConversationCreateTurnReplayRevisionAndOwnerIsolationPG(t *testing.T) {
	db := newIsolatedReplyDB(t)
	ctx := context.Background()
	statement := "I think robotics demand will increase."
	model := &queuedReplyCompleter{response: readyChoiceReplyFixture(t, statement)}
	service, admission := newPGReplyService(t, db.pool, model)
	owner := "reply-owner-a"
	createID := uuid.New()
	created, err := service.CreateConversation(ctx, owner, createID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.CreateConversation(ctx, owner, createID)
	if err != nil || replay.ThreadID != created.ThreadID {
		t.Fatalf("create replay returned %s (%v), want thread %s", replay.ThreadID, err, created.ThreadID)
	}
	if _, err := service.Conversation(ctx, "reply-owner-b", created.ThreadID); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("different owner read error = %v, want not found", err)
	}

	message := "I have an idea about robotics."
	requestID := uuid.New()
	input := TurnInput{ThreadID: created.ThreadID, ExpectedRevision: 0, RequestID: requestID, Message: &message}
	first, err := service.Continue(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	var conversation Conversation
	if err := json.Unmarshal(first.Conversation, &conversation); err != nil {
		t.Fatal(err)
	}
	if conversation.Draft.Revision != 1 || len(conversation.Draft.Choices) == 0 {
		t.Fatalf("first reply did not advance revision and save choices: %+v", conversation.Draft)
	}
	replayed, err := service.Continue(ctx, owner, input)
	var firstConversation, replayedConversation Conversation
	if decodeErr := json.Unmarshal(first.Conversation, &firstConversation); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if decodeErr := json.Unmarshal(replayed.Conversation, &replayedConversation); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if err != nil || replayed.Outcome != first.Outcome || replayed.Decision != first.Decision || !reflect.DeepEqual(replayedConversation, firstConversation) {
		t.Fatalf("turn replay result mismatch: %v", err)
	}
	if model.calls.Load() != 1 || admission.calls.Load() != 1 {
		t.Fatalf("replay repeated model/admission: model=%d admission=%d", model.calls.Load(), admission.calls.Load())
	}

	collision := input
	otherMessage := "A different payload under the same request ID."
	collision.Message = &otherMessage
	if _, err := service.Continue(ctx, owner, collision); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same request ID with different payload error = %v", err)
	}
	stale := TurnInput{ThreadID: created.ThreadID, ExpectedRevision: 0, RequestID: uuid.New(), Message: &otherMessage}
	staleResult, err := service.Continue(ctx, owner, stale)
	if err != nil || staleResult.Outcome != "REVISION_CONFLICT" {
		t.Fatalf("stale revision outcome=%q err=%v", staleResult.Outcome, err)
	}
	if model.calls.Load() != 1 {
		t.Fatalf("conflict/replay path made another model call: %d", model.calls.Load())
	}
}

type failedExpressionBuilder struct{ calls atomic.Int32 }

func (b *failedExpressionBuilder) Build(context.Context, takesresearch.Input) (takesresearch.Output, error) {
	b.calls.Add(1)
	return takesresearch.Output{Status: takesresearch.StatusFailed}, errors.New("synthetic provider failure")
}

type fixedCatalog struct {
	snapshot takeintelligence.CatalogSnapshot
}

func (c fixedCatalog) CaptureCatalog(context.Context) (takeintelligence.CatalogSnapshot, error) {
	return c.snapshot, nil
}

func testCatalog(t *testing.T) takeintelligence.CatalogSnapshot {
	t.Helper()
	now := time.Now().UTC()
	snapshot := takeintelligence.CatalogSnapshot{
		ID: uuid.NewString(), AdapterVersion: "synthetic-test-fixture-v1", Provider: "hyperliquid",
		ConfiguredVenues: []string{""}, StartedAt: now, CompletedAt: now, Result: takeintelligence.SnapshotComplete,
		Instruments: []takeintelligence.Instrument{},
	}
	digest, err := takeintelligence.CatalogDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Digest = digest
	return snapshot
}

func seedReadyBuild(t *testing.T, pool *pgxpool.Pool, threadID string, revision int) (Draft, string) {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT draft_state FROM takes_reply_conversations WHERE thread_id=$1`, threadID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var draft Draft
	if err := json.Unmarshal(raw, &draft); err != nil {
		t.Fatal(err)
	}
	draft.Revision = revision
	draft.ID = threadID + ":" + strconv.Itoa(revision)
	draft.Stage = "READY_TO_BUILD"
	statement := "I think robotics suppliers could benefit."
	draft.Belief.UserStatement = &statement
	draft.Choices = []Choice{{ID: draft.ID + ":build", Label: "Build it now", ExposureRole: "build_now", Description: "Build a proposed basket from this idea."}}
	encoded, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE takes_reply_conversations SET current_revision=$2,draft_state=$3 WHERE thread_id=$1`, threadID, revision, encoded); err != nil {
		t.Fatal(err)
	}
	return draft, draft.Choices[0].ID
}

func TestFailedBuildIsDeduplicatedAndNewReplyRevisionCanRetryPG(t *testing.T) {
	db := newIsolatedReplyDB(t)
	ctx := context.Background()
	service, _ := newPGReplyService(t, db.pool, llm.NewUnavailableCompleter())
	conversation, err := service.CreateConversation(ctx, "build-owner", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	_, choiceID := seedReadyBuild(t, db.pool, conversation.ThreadID, 1)
	click := TurnInput{ThreadID: conversation.ThreadID, ExpectedRevision: 1, RequestID: uuid.New(), SelectedChoiceID: &choiceID}
	queued, err := service.Continue(ctx, "build-owner", click)
	if err != nil || queued.Decision != "BUILD_QUEUED" {
		t.Fatalf("queue result decision=%q err=%v", queued.Decision, err)
	}
	clickedAgain, err := service.Continue(ctx, "build-owner", click)
	if err != nil || clickedAgain.Decision != "BUILD_QUEUED" {
		t.Fatalf("queued request replay decision=%q err=%v", clickedAgain.Decision, err)
	}

	catalog := testCatalog(t)
	builder := &failedExpressionBuilder{}
	worker := NewBuildWorker(db.pool, fixedCatalog{snapshot: catalog}, builder, nil, nil, slog.New(slog.DiscardHandler))
	worked, err := worker.RunNext(ctx)
	if err == nil || !worked {
		t.Fatalf("worker processed failed fixture? worked=%t err=%v", worked, err)
	}
	failed, err := service.Conversation(ctx, "build-owner", conversation.ThreadID)
	if err != nil || failed.Build == nil || failed.Build.Status != "FAILED" {
		t.Fatalf("failed build not visible: build=%+v err=%v", failed.Build, err)
	}
	for _, choice := range failed.Draft.Choices {
		if selectedChoiceRequestsBuild(&choice) {
			t.Fatal("terminally failed revision still exposes its Build choice")
		}
	}
	if _, err := service.Continue(ctx, "build-owner", click); err != nil {
		t.Fatalf("replay failed build action: %v", err)
	}
	if worked, err := worker.RunNext(ctx); err != nil || worked || builder.calls.Load() != 1 {
		t.Fatalf("failed build was replayed: worked=%t err=%v provider calls=%d", worked, err, builder.calls.Load())
	}

	_, nextChoice := seedReadyBuild(t, db.pool, conversation.ThreadID, 2)
	next := TurnInput{ThreadID: conversation.ThreadID, ExpectedRevision: 2, RequestID: uuid.New(), SelectedChoiceID: &nextChoice}
	retried, err := service.Continue(ctx, "build-owner", next)
	if err != nil || retried.Decision != "BUILD_QUEUED" {
		t.Fatalf("new revision did not queue a retry: decision=%q err=%v", retried.Decision, err)
	}
	if worked, err := worker.RunNext(ctx); err == nil || !worked || builder.calls.Load() != 2 {
		t.Fatalf("new revision worker call worked=%t err=%v provider calls=%d", worked, err, builder.calls.Load())
	}
}

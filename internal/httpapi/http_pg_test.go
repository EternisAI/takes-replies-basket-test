package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesreply"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	replydb "github.com/EternisAI/takes-replies-basket-test/storage/pg/generated/takesreply"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRESTConversationLifecycleOwnerIsolationAndMissingKeyPG(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TAKES_REPLY_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("set TAKES_REPLY_TEST_DATABASE_URL to an isolated PostgreSQL instance")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := "takes_reply_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	schemaSQL := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaSQL); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schemaSQL+" CASCADE")
		admin.Close()
	})
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
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
			t.Fatalf("apply %s in disposable test schema: %v", name, err)
		}
	}

	service, err := takesreply.NewService(pool, replydb.New(pool), llm.NewUnavailableCompleter(), hostAdmissionForTest{}, takesreply.Config{
		ConstructionMode: takesresearch.ExpressionMode,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(service, Config{BearerToken: "test-only-private-token", OwnerUserID: "fixed-owner"})
	if err != nil {
		t.Fatal(err)
	}

	requestID := uuid.New()
	createBody, _ := json.Marshal(map[string]string{"requestId": requestID.String()})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/reply-conversations", bytes.NewReader(createBody)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", unauthorized.Code)
	}

	create := func() (takesreply.Conversation, int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/reply-conversations", bytes.NewReader(createBody))
		request.Header.Set("Authorization", "Bearer test-only-private-token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		var conversation takesreply.Conversation
		if err := json.Unmarshal(recorder.Body.Bytes(), &conversation); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		return conversation, recorder.Code
	}
	created, status := create()
	if status != http.StatusOK || created.ThreadID == "" {
		t.Fatalf("create returned %d and thread %q", status, created.ThreadID)
	}
	replayed, status := create()
	if status != http.StatusOK || replayed.ThreadID != created.ThreadID {
		t.Fatalf("create replay returned %d/thread %s, want %s", status, replayed.ThreadID, created.ThreadID)
	}

	read := func(threadID string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/reply-conversations/"+threadID, nil)
		request.Header.Set("Authorization", "Bearer test-only-private-token")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	got := read(created.ThreadID)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), created.ThreadID) {
		t.Fatalf("read returned %d: %s", got.Code, got.Body.String())
	}
	other, err := service.CreateConversation(ctx, "different-owner", uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got := read(other.ThreadID); got.Code != http.StatusNotFound {
		t.Fatalf("configured owner read another owner's thread with %d", got.Code)
	}

	staleBody, _ := json.Marshal(map[string]any{"requestId": uuid.NewString(), "expectedRevision": 1, "message": "stale reply"})
	staleRequest := httptest.NewRequest(http.MethodPost, "/v1/reply-conversations/"+created.ThreadID+"/turns", bytes.NewReader(staleBody))
	staleRequest.Header.Set("Authorization", "Bearer test-only-private-token")
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, staleRequest)
	var staleResult takesreply.TurnResult
	if err := json.Unmarshal(staleRecorder.Body.Bytes(), &staleResult); err != nil || staleRecorder.Code != http.StatusOK || staleResult.Outcome != "REVISION_CONFLICT" {
		t.Fatalf("stale turn returned %d, outcome %q, decode error %v", staleRecorder.Code, staleResult.Outcome, err)
	}

	turnID := uuid.New()
	turnBody, _ := json.Marshal(map[string]any{"requestId": turnID.String(), "expectedRevision": 0, "message": "tell me more"})
	turnRequest := httptest.NewRequest(http.MethodPost, "/v1/reply-conversations/"+created.ThreadID+"/turns", bytes.NewReader(turnBody))
	turnRequest.Header.Set("Authorization", "Bearer test-only-private-token")
	turnRecorder := httptest.NewRecorder()
	handler.ServeHTTP(turnRecorder, turnRequest)
	if turnRecorder.Code != http.StatusServiceUnavailable || !strings.Contains(turnRecorder.Body.String(), `"code":"MODEL_NOT_CONFIGURED"`) {
		t.Fatalf("missing key turn returned %d: %s", turnRecorder.Code, turnRecorder.Body.String())
	}
}

type hostAdmissionForTest struct{}

func (hostAdmissionForTest) AdmitReplyTurn(context.Context, string) (func(bool, string, string, []llm.Usage, bool), error) {
	return func(bool, string, string, []llm.Usage, bool) {}, nil
}

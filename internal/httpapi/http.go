// Package httpapi exposes the standalone test service's authenticated REST
// adapter. It binds every request to one configured owner; it is not a
// multi-user identity provider.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesreply"
	"github.com/google/uuid"
)

const maxJSONBody = 64 << 10

type Config struct {
	BearerToken     string
	OwnerUserID     string
	ModelConfigured bool
	DatabasePing    func(context.Context) error
}

type Handler struct {
	service *takesreply.Service
	cfg     Config
}

func New(service *takesreply.Service, cfg Config) (*Handler, error) {
	if service == nil {
		return nil, errors.New("reply service is required")
	}
	if strings.TrimSpace(cfg.BearerToken) == "" {
		return nil, errors.New("AUTH_BEARER_TOKEN is required")
	}
	if strings.TrimSpace(cfg.OwnerUserID) == "" {
		cfg.OwnerUserID = "test-owner"
	}
	return &Handler{service: service, cfg: cfg}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		h.health(w, r)
		return
	}
	if r.URL.Path == "/" {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "takes-replies-basket-test",
			"routes":  []string{"GET /healthz", "POST /v1/reply-conversations", "GET /v1/reply-conversations/{threadID}", "POST /v1/reply-conversations/{threadID}/turns"},
		})
		return
	}
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid bearer token is required.")
		return
	}

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "v1" && parts[1] == "reply-conversations" && r.Method == http.MethodPost {
		h.create(w, r)
		return
	}
	if len(parts) == 3 && parts[0] == "v1" && parts[1] == "reply-conversations" && r.Method == http.MethodGet {
		h.get(w, r, parts[2])
		return
	}
	if len(parts) == 4 && parts[0] == "v1" && parts[1] == "reply-conversations" && parts[3] == "turns" && r.Method == http.MethodPost {
		h.turn(w, r, parts[2])
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/reply-conversations") {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "The request method or route is not supported.")
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET for this endpoint.")
		return
	}
	database := "ok"
	status := http.StatusOK
	if h.cfg.DatabasePing != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.cfg.DatabasePing(ctx); err != nil {
			database, status = "unavailable", http.StatusServiceUnavailable
		}
	}
	model := "not_configured"
	if h.cfg.ModelConfigured {
		model = "configured"
	}
	writeJSON(w, status, map[string]string{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK], "database": database, "model": model})
}

func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	value := r.Header.Get("Authorization")
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return false
	}
	candidate := strings.TrimSpace(value[len(prefix):])
	if candidate == "" || len(candidate) != len(h.cfg.BearerToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(h.cfg.BearerToken)) == 1
}

type createRequest struct {
	RequestID string `json:"requestId"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var body createRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", err.Error())
		return
	}
	requestID, err := uuid.Parse(strings.TrimSpace(body.RequestID))
	if err != nil || requestID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "requestId must be a nonzero UUID.")
		return
	}
	conversation, err := h.service.CreateConversation(r.Context(), h.cfg.OwnerUserID, requestID)
	if err != nil {
		h.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, threadID string) {
	conversation, err := h.service.Conversation(r.Context(), h.cfg.OwnerUserID, threadID)
	if err != nil {
		h.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

type turnRequest struct {
	RequestID        string  `json:"requestId"`
	ExpectedRevision *int    `json:"expectedRevision"`
	Message          *string `json:"message"`
	SelectedChoiceID *string `json:"selectedChoiceId"`
}

func (h *Handler) turn(w http.ResponseWriter, r *http.Request, threadID string) {
	var body turnRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", err.Error())
		return
	}
	requestID, err := uuid.Parse(strings.TrimSpace(body.RequestID))
	if err != nil || requestID == uuid.Nil || body.ExpectedRevision == nil {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "requestId and expectedRevision are required; requestId must be a nonzero UUID.")
		return
	}
	result, err := h.service.Continue(r.Context(), h.cfg.OwnerUserID, takesreply.TurnInput{
		ThreadID: threadID, ExpectedRevision: *body.ExpectedRevision, RequestID: requestID,
		Message: body.Message, SelectedChoiceID: body.SelectedChoiceID,
	})
	if err != nil {
		h.writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.Body == nil {
		return errors.New("a JSON body is required")
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func (h *Handler) writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, takesreply.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", err.Error())
	case errors.Is(err, takesreply.ErrConversationNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "The reply conversation was not found.")
	case errors.Is(err, takesreply.ErrRevisionConflict):
		writeError(w, http.StatusConflict, "REVISION_CONFLICT", "The conversation changed. Read the current revision and retry.")
	case errors.Is(err, takesreply.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "This request ID was already used with different input.")
	case errors.Is(err, takesreply.ErrTurnInFlight):
		writeError(w, http.StatusConflict, "PROCESSING", "A turn is already in progress for this conversation.")
	case errors.Is(err, takesreply.ErrBuildActiveElsewhere):
		writeError(w, http.StatusConflict, "BUILD_ACTIVE_ELSEWHERE", "Another proposed basket build is already active for this owner.")
	case errors.Is(err, takesreply.ErrInvalidChoice):
		writeError(w, http.StatusBadRequest, "INVALID_CHOICE", "The selected choice is not available for this revision.")
	case errors.Is(err, llm.ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "MODEL_NOT_CONFIGURED", "OpenRouter is not configured. Conversation storage is available, but model-backed turns and basket builds are disabled.")
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", "The request could not be completed.")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

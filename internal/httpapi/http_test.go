package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

func TestHealthIsPublicAndReportsMissingModelConfiguration(t *testing.T) {
	h := &Handler{cfg: Config{OwnerUserID: "fixed-owner", ModelConfigured: false}}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"model":"not_configured"`) {
		t.Fatalf("health response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestHealthReportsDatabaseFailure(t *testing.T) {
	h := &Handler{cfg: Config{DatabasePing: func(context.Context) error { return errors.New("offline") }}}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"database":"unavailable"`) {
		t.Fatalf("health response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestReplyRoutesRequireBearerToken(t *testing.T) {
	h := &Handler{cfg: Config{BearerToken: "a-long-private-test-token", OwnerUserID: "fixed-owner"}}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/v1/reply-conversations", strings.NewReader(`{"requestId":"7cc7a42a-edab-429b-bfca-53b260a07926"}`)),
		httptest.NewRequest(http.MethodGet, "/v1/reply-conversations/thread-id", nil),
	} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s returned %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestUnknownOwnerFieldCannotOverrideConfiguredIdentity(t *testing.T) {
	h := &Handler{cfg: Config{BearerToken: "a-long-private-test-token", OwnerUserID: "fixed-owner"}}
	request := httptest.NewRequest(http.MethodPost, "/v1/reply-conversations", strings.NewReader(`{"requestId":"7cc7a42a-edab-429b-bfca-53b260a07926","userId":"another-owner"}`))
	request.Header.Set("Authorization", "Bearer a-long-private-test-token")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown identity field returned %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestMissingOpenRouterKeyMapsToServiceUnavailable(t *testing.T) {
	h := &Handler{}
	recorder := httptest.NewRecorder()
	h.writeDomainError(recorder, llm.ErrNotConfigured)
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"MODEL_NOT_CONFIGURED"`) {
		t.Fatalf("model configuration error = %d %s", recorder.Code, recorder.Body.String())
	}
}

package takesreply

import "testing"

func TestAcceptedReplyRequestPinsProductionRoute(t *testing.T) {
	request, err := acceptedReplyCompletionRequest("anthropic/claude-sonnet-5.5", "high", 6000, EvidenceCheckOutput{}, false, "turn")
	if err != nil {
		t.Fatalf("build accepted reply request: %v", err)
	}
	if request.Provider == nil || len(request.Provider.Order) != 1 || request.Provider.Order[0] != "anthropic" ||
		request.Provider.AllowFallbacks == nil || *request.Provider.AllowFallbacks || !request.Provider.RequireParameters {
		t.Fatalf("reply provider route = %#v, want Anthropic only with required parameters", request.Provider)
	}
	if request.Model != "anthropic/claude-sonnet-5.5" || request.ReasoningEffort != "high" || request.MaxTokens != 6000 ||
		!request.CanonicalOpenRouterReasoning || !request.ExcludeReasoning || request.ServiceTier != "default" || !request.NoRetry {
		t.Fatalf("reply request settings changed: %#v", request)
	}
}

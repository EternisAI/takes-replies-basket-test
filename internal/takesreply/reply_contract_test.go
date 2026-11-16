package takesreply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
	"github.com/google/uuid"
)

const (
	acceptedConversationPromptSHA256 = "f6c30871284f3911cf85ccbaeb851f12acf8b17a8fab1eba309d0be53e97f0b1"
	acceptedResponseSchemaSHA256     = "f9e722a83960ba917f12a93f2871dcec10f3cbbc02471f0af91b40a6b0e3f11f"
	acceptedResponseSchemaID         = "https://github.com/axionhypothesis/backend-go/internal/takesconversation/decision-point-turn-output"
	acceptedTurnSchemaID             = "https://github.com/axionhypothesis/backend-go/internal/takesconversation/turn-model-output"
)

func TestAcceptedConversationPromptHash(t *testing.T) {
	sum := sha256.Sum256([]byte(conversationSystem))
	if got := hex.EncodeToString(sum[:]); got != acceptedConversationPromptSHA256 {
		t.Fatalf("accepted prompt hash changed: got %s, want %s", got, acceptedConversationPromptSHA256)
	}
	if got := conversationSystemForEvidence(EvidenceCheckOutput{}, false); got != conversationSystem {
		t.Fatal("evidence-aware prompt changed the accepted system prompt")
	}
}

func TestAcceptedDecisionPointTurnSchemaIDAndHash(t *testing.T) {
	schema, err := decisionPointTurnModelOutputSchema()
	if err != nil {
		t.Fatalf("build response schema: %v", err)
	}
	root, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("schema root has type %T, want object", schema)
	}
	if got, _ := root["$id"].(string); got != acceptedResponseSchemaID {
		t.Fatalf("schema $id changed: got %q, want %q", got, acceptedResponseSchemaID)
	}
	properties, ok := root["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema root is missing properties")
	}
	turnSchema, ok := properties["turn"].(map[string]any)
	if !ok {
		t.Fatal("schema is missing the turn object")
	}
	if got, _ := turnSchema["$id"].(string); got != acceptedTurnSchemaID {
		t.Fatalf("turn schema $id changed: got %q, want %q", got, acceptedTurnSchemaID)
	}
	responseFormat := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   decisionPointTurnSchemaName,
			"strict": true,
			"schema": schema,
		},
	}
	encoded, err := json.Marshal(responseFormat)
	if err != nil {
		t.Fatalf("marshal response format: %v", err)
	}
	sum := sha256.Sum256(encoded)
	if got := hex.EncodeToString(sum[:]); got != acceptedResponseSchemaSHA256 {
		t.Fatalf("accepted response format hash changed: got %s, want %s", got, acceptedResponseSchemaSHA256)
	}
}

func TestSafePublicURLRejectsNonStandardPort(t *testing.T) {
	for _, value := range []string{
		"http://reuters.com/story",
		"https://user@reuters.com/story",
		"https://reuters.com:8443/story",
	} {
		if got := safePublicURL(value); got != nil {
			t.Errorf("safePublicURL(%q) = %q, want rejection", value, *got)
		}
	}
	if got := safePublicURL("https://www.reuters.com/story"); got == nil || *got != "https://www.reuters.com/story" {
		t.Fatalf("safe HTTPS URL was rejected or changed: %v", got)
	}
}

func TestDecisionEvidenceSelectionMustBeBoundedAndClosed(t *testing.T) {
	valid := `{"evidence":[{"publicUrl":"https://www.reuters.com/story","subject":"Acme","sourceExcerpt":"Acme reported higher sales.","category":"company_context"}]}`
	if _, err := decodeDecisionEvidenceOutput(valid); err != nil {
		t.Fatalf("decode valid evidence selection: %v", err)
	}
	extraField := `{"evidence":[{"publicUrl":"https://www.reuters.com/story","subject":"Acme","sourceExcerpt":"Acme reported higher sales.","category":"company_context","conclusion":"buy"}]}`
	if _, err := decodeDecisionEvidenceOutput(extraField); err == nil {
		t.Fatal("evidence selection accepted an unsupported conclusion field")
	}
	tooMany := `{"evidence":[{"publicUrl":"https://www.reuters.com/1","subject":"Acme","sourceExcerpt":"1","category":"company_context"},{"publicUrl":"https://www.reuters.com/2","subject":"Acme","sourceExcerpt":"2","category":"company_context"},{"publicUrl":"https://www.reuters.com/3","subject":"Acme","sourceExcerpt":"3","category":"company_context"},{"publicUrl":"https://www.reuters.com/4","subject":"Acme","sourceExcerpt":"4","category":"company_context"}]}`
	if _, err := decodeDecisionEvidenceOutput(tooMany); err == nil {
		t.Fatal("evidence selection accepted more than three source excerpts")
	}
}

func TestConversationCompletionRequestUsesAcceptedDefaults(t *testing.T) {
	config := (Config{}).normalized()
	service := &Service{
		model: config.EvidenceModel, conversationModel: config.ReplyModel,
		conversationMaxTokens: defaultReplyTokens, conversationReasoningEffort: "high",
	}
	request, err := conversationCompletionRequest(
		service.conversationCompletionModel(), service.conversationCompletionEffort(), service.conversationCompletionMaxTokens(),
		EvidenceCheckOutput{}, false, "user text",
	)
	if err != nil {
		t.Fatalf("build conversation request: %v", err)
	}
	if config.ReplyModel != "anthropic/claude-sonnet-5.5" || request.Model != config.ReplyModel {
		t.Errorf("reply model default = %q and request model = %q", config.ReplyModel, request.Model)
	}
	if config.EvidenceModel != "google/gemini-3.8-flash" {
		t.Errorf("evidence model default = %q", config.EvidenceModel)
	}
	if request.ReasoningEffort != "high" || request.MaxTokens != 6000 {
		t.Errorf("reply request defaults = effort %q / %d tokens, want high / 6000", request.ReasoningEffort, request.MaxTokens)
	}
	if !request.Strict || !request.NoRetry {
		t.Errorf("request bounds changed: strict=%t noRetry=%t", request.Strict, request.NoRetry)
	}
	if request.SchemaName != decisionPointTurnSchemaName {
		t.Errorf("schema name = %q, want %q", request.SchemaName, decisionPointTurnSchemaName)
	}
	provider := requiredProvider("anthropic")
	if !reflect.DeepEqual(provider.Order, []string{"anthropic"}) || provider.AllowFallbacks == nil || *provider.AllowFallbacks || !provider.RequireParameters {
		t.Errorf("accepted reply provider route is not pinned: %+v", provider)
	}
}

func TestModelOutputCannotReturnQueuedBuildClaim(t *testing.T) {
	got := normalizeUnsubmittedBuildHandoff(TurnModelOutput{
		AssistantMessage: "The build is queued and research has started.",
		Decision:         "BUILD_QUEUED",
		BuildRequested:   true,
		Stage:            "DEVELOPING",
	})
	if got.Decision == "BUILD_QUEUED" || got.BuildRequested {
		t.Fatalf("model output retained build authorization: decision=%q buildRequested=%t", got.Decision, got.BuildRequested)
	}
	if got.Decision != "READY_TO_BUILD" || got.Stage != "READY_TO_BUILD" {
		t.Fatalf("queued output was not reduced to ready-to-build: decision=%q stage=%q", got.Decision, got.Stage)
	}
	if got.AssistantMessage != "The idea is ready for basket research." {
		t.Fatalf("queued output retained a claim that work started: %q", got.AssistantMessage)
	}
}

func TestModelOutputRemovesQueuedClaimsFromInconsistentFields(t *testing.T) {
	for _, input := range []TurnModelOutput{
		{
			AssistantMessage: "The build is queued and research has started.",
			Decision:         "REPLY",
			BuildRequested:   true,
			Stage:            "DEVELOPING",
		},
		{
			AssistantMessage: "I started researching the basket.",
			Decision:         "REPLY",
			BuildRequested:   false,
			Stage:            "DEVELOPING",
		},
		{
			AssistantMessage: "The build is underway.",
			Decision:         "READY_TO_BUILD",
			BuildRequested:   true,
			Stage:            "READY_TO_BUILD",
		},
	} {
		got := normalizeUnsubmittedBuildHandoff(input)
		if got.BuildRequested {
			t.Errorf("normalized output retained BuildRequested for inconsistent model fields: %+v", got)
		}
		lower := strings.ToLower(got.AssistantMessage)
		for _, phrase := range []string{
			"build is queued", "queued the build", "build queued", "research has started", "research started",
			"research is underway", "research is in progress", "i've started researching", "i started researching",
			"we've started researching", "we started researching", "build has started", "build is underway",
			"build is in progress", "basket is being built", "queued your basket", "choose build", "click build",
		} {
			if strings.Contains(lower, phrase) {
				t.Errorf("normalized output retained false work-started claim %q: %q", phrase, got.AssistantMessage)
			}
		}
	}
}

func TestUnrelatedTurnCannotPreserveUserStatedStatusForFabricatedReplacement(t *testing.T) {
	now := time.Date(2026, time.October, 26, 12, 0, 0, 0, time.UTC)
	sourceMessageID := uuid.New()
	old := newDraft("thread", now)
	old.Belief.InvestmentThesis = InvestmentThesis{
		Statement:       stringPtr("Costco membership renewals support recurring fee income"),
		CausalMechanism: stringPtr("Membership fees provide recurring income"),
		SourceMessageID: &sourceMessageID,
		Status:          "USER_STATED",
	}
	out := TurnModelOutput{
		AssistantMessage: "Here is a current valuation check.",
		Decision:         "REPLY",
		Stage:            "DEVELOPING",
		Belief: BeliefOutput{
			IntentStatus:    "INVESTMENT_THESIS",
			ThesisStatement: stringPtr("Costco doubled its warehouse count and margins are set to expand"),
			ThesisMechanism: stringPtr("New warehouses will immediately raise customer spending"),
			ThesisStatus:    "USER_STATED",
		},
	}
	updated := applyTurnOutput(old, out, uuid.New(), nil, "How are Costco shares valued today?", now.Add(time.Minute))
	if updated.Belief.InvestmentThesis.Status != "INFERRED_UNCONFIRMED" {
		t.Fatalf("replacement thesis status = %q, want INFERRED_UNCONFIRMED", updated.Belief.InvestmentThesis.Status)
	}
	if updated.Belief.InvestmentThesis.SourceMessageID != nil {
		t.Fatalf("ungrounded replacement retained source message ID %v", *updated.Belief.InvestmentThesis.SourceMessageID)
	}
}

type replyContractCompleter struct {
	request       llm.CompletionRequest
	response      string
	receipt       llm.WebSearchReceipt
	called        bool
	searchOK      bool
	hasDeadline   bool
	requestExpiry time.Time
}

func (c *replyContractCompleter) Complete(ctx context.Context, request llm.CompletionRequest) (string, error) {
	c.request = request
	c.called = true
	c.requestExpiry, c.hasDeadline = ctx.Deadline()
	llm.CaptureWebCitations(ctx, c.receipt)
	return c.response, nil
}

func (c *replyContractCompleter) SupportsOpenRouterWebSearch() bool { return c.searchOK }

func TestEvidenceRequestUsesBoundedDefaultsAndVerifiedSource(t *testing.T) {
	const (
		publicURL = "https://www.reuters.com/markets/companies/acme-outlook"
		excerpt   = "Acme Inc. manufactures industrial valves for water utilities."
	)
	searchRequests := 1
	publishedAt := time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)
	completer := &replyContractCompleter{
		response: `{"evidence":[{"publicUrl":"` + publicURL + `","subject":"Acme Inc.","sourceExcerpt":"` + excerpt + `","category":"company_context"}]}`,
		receipt: llm.WebSearchReceipt{
			Citations:      []llm.WebCitation{{URL: publicURL, Title: "Acme Inc. outlook", Content: excerpt, PublishedAt: &publishedAt}},
			SearchRequests: &searchRequests,
		},
		searchOK: true,
	}
	config := (Config{}).normalized()
	service := &Service{llm: completer, model: config.EvidenceModel, evidenceReasoningEffort: "low"}
	check, diagnostics := service.prebuildDecisionEvidenceCheckWithDiagnostics(context.Background(), "Acme Inc. may expand margins. My portfolio holds 100 shares; contact me at hidden@example.com.")
	if !completer.called {
		t.Fatal("evidence request was not sent to the in-memory completer")
	}
	request := completer.request
	if request.Model != "google/gemini-3.8-flash" || request.Model != config.EvidenceModel {
		t.Errorf("evidence model = %q, want default %q", request.Model, config.EvidenceModel)
	}
	if request.ReasoningEffort != "low" || request.MaxTokens != 1800 || !request.Strict || !request.NoRetry {
		t.Errorf("evidence request bounds = effort %q tokens %d strict %t noRetry %t", request.ReasoningEffort, request.MaxTokens, request.Strict, request.NoRetry)
	}
	if !completer.hasDeadline || time.Until(completer.requestExpiry) <= 0 || time.Until(completer.requestExpiry) > defaultEvidenceTimeout {
		t.Errorf("evidence request deadline is not bounded by %s: deadline=%v", defaultEvidenceTimeout, completer.requestExpiry)
	}
	if !request.OpenRouterWebSearch {
		t.Error("evidence request did not enable the bounded search tool")
	}
	briefStart := strings.LastIndex(request.UserPrompt, "\n\n")
	if briefStart < 0 {
		t.Fatalf("evidence request is missing its bounded JSON input: %q", request.UserPrompt)
	}
	var evidenceInput decisionEvidenceInput
	if err := json.Unmarshal([]byte(request.UserPrompt[briefStart+2:]), &evidenceInput); err != nil {
		t.Fatalf("decode evidence request input: %v", err)
	}
	if len(evidenceInput.PublicResearchBrief) > 700 || !strings.Contains(evidenceInput.PublicResearchBrief, "Acme Inc.") {
		t.Errorf("public research brief is missing topic or exceeds its cap: %q", evidenceInput.PublicResearchBrief)
	}
	if strings.Contains(evidenceInput.PublicResearchBrief, "100 shares") || strings.Contains(evidenceInput.PublicResearchBrief, "hidden@example.com") {
		t.Errorf("private position/contact text reached the evidence request: %q", evidenceInput.PublicResearchBrief)
	}
	if _, err := time.Parse("2006-01-02", evidenceInput.AsOfUTC); err != nil {
		t.Errorf("evidence request has invalid as-of cutoff %q: %v", evidenceInput.AsOfUTC, err)
	}
	wantDomains := []string{"sec.gov", "nasdaq.com", "morningstar.com", "stockanalysis.com", "macrotrends.net", "finance.yahoo.com", "reuters.com"}
	if !reflect.DeepEqual(request.OpenRouterWebSearchAllowedDomains, wantDomains) {
		t.Errorf("allowed search domains = %v, want %v", request.OpenRouterWebSearchAllowedDomains, wantDomains)
	}
	if request.Provider == nil || !reflect.DeepEqual(request.Provider.Order, []string{"google-ai-studio"}) || request.Provider.AllowFallbacks == nil || *request.Provider.AllowFallbacks || !request.Provider.RequireParameters {
		t.Errorf("evidence provider route is not pinned: %+v", request.Provider)
	}
	if !request.CanonicalOpenRouterReasoning || !request.ExcludeReasoning || request.ServiceTier != "default" {
		t.Errorf("evidence route options changed: canonical=%t exclude=%t tier=%q", request.CanonicalOpenRouterReasoning, request.ExcludeReasoning, request.ServiceTier)
	}
	if check.Status != "UNAVAILABLE" || len(check.Facts) != 0 || len(check.Sources) != 1 {
		t.Fatalf("untyped citation entered current evidence or lost source provenance: status=%q facts=%d sources=%d", check.Status, len(check.Facts), len(check.Sources))
	}
	if check.Sources[0].SourceRef != publicURL || check.Sources[0].PublicURL == nil || *check.Sources[0].PublicURL != publicURL {
		t.Errorf("source reference does not point to the cited public URL: %+v", check.Sources[0])
	}
	if check.Sources[0].PublishedAt == nil || !check.Sources[0].PublishedAt.Equal(publishedAt) || check.Sources[0].RetrievedAt.IsZero() {
		t.Errorf("source publication/retrieval provenance was not preserved: %+v", check.Sources[0])
	}
	if check.Sources[0].Title != "" || check.Sources[0].Excerpt != "" || check.Sources[0].PageSummary != "" {
		t.Errorf("untyped citation text was not scrubbed from the source row: %+v", check.Sources[0])
	}
	if check.SearchRequests == nil || *check.SearchRequests != 1 || diagnostics.AcceptedSelectionCount != 1 {
		t.Errorf("search receipt/accepted excerpt not preserved: requests=%v diagnostics=%+v", check.SearchRequests, diagnostics)
	}
}

func TestEvidenceRequestRejectsExcerptOrURLNotInProviderCitation(t *testing.T) {
	const (
		citedURL     = "https://www.reuters.com/markets/companies/acme-outlook"
		citedExcerpt = "Acme Inc. manufactures industrial valves for water utilities."
	)
	for _, test := range []struct {
		name string
		url  string
		text string
	}{
		{name: "excerpt mismatch", url: citedURL, text: "Acme Inc. manufactures industrial valves for all utility companies."},
		{name: "URL not annotated", url: "https://www.reuters.com/markets/companies/other", text: citedExcerpt},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(decisionEvidenceOutput{Evidence: []decisionEvidenceSelection{{
				PublicURL: test.url, Subject: "Acme Inc.", Excerpt: test.text, Category: "company_context",
			}}})
			if err != nil {
				t.Fatalf("encode fixture response: %v", err)
			}
			searchRequests := 1
			completer := &replyContractCompleter{
				response: string(body),
				receipt: llm.WebSearchReceipt{
					Citations:      []llm.WebCitation{{URL: citedURL, Title: "Acme Inc. outlook", Content: citedExcerpt}},
					SearchRequests: &searchRequests,
				},
				searchOK: true,
			}
			service := &Service{llm: completer, model: defaultEvidenceModel}
			check := service.prebuildDecisionEvidenceCheck(context.Background(), "Acme Inc. may expand margins.")
			if check.Status != "UNAVAILABLE" || len(check.Facts) != 0 {
				t.Fatalf("unsupported selection was accepted: status=%q facts=%+v", check.Status, check.Facts)
			}
		})
	}
}

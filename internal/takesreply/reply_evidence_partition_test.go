package takesreply

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestReplyEvidenceSeparatesCurrentHistoryAndFutureAtOneCutoff(t *testing.T) {
	asOf := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	currentAt := time.Date(2026, time.October, 27, 9, 0, 0, 0, time.UTC)
	historicalAt := time.Date(2023, time.December, 31, 0, 0, 0, 0, time.UTC)
	futureAt := time.Date(2026, time.October, 28, 0, 0, 0, 0, time.UTC)
	publicURL := "https://example.invalid/company/metrics"
	check, historical := filterUnsupportedCurrentEvidenceSnippetsAt(EvidenceCheckOutput{
		Status: "CHECKED",
		Sources: []EvidenceCheckSource{{
			SourceRef: publicURL, PublicURL: &publicURL, Title: "SYNTHETIC ONLY current and prior revenue",
			Excerpt: "SYNTHETIC ONLY 2023 and 2026 revenue values", PageSummary: "SYNTHETIC ONLY",
			PublishedAt: timePtr(historicalAt), RetrievedAt: asOf,
		}},
		Facts: []EvidenceCheckFact{
			{Claim: "reported revenue", Value: "100", ObservedAt: &currentAt, SourceRef: publicURL},
			{Claim: "reported revenue", Value: "75", ObservedAt: &historicalAt, SourceRef: publicURL},
			{Claim: "reported revenue", Value: "110", ObservedAt: &futureAt, SourceRef: publicURL},
		},
	}, asOf)

	if !check.AsOfUTC.Equal(asOf) || len(check.Facts) != 1 || check.Facts[0].Value != "100" || check.Facts[0].TemporalStatus != EvidenceTemporalCurrent {
		t.Fatalf("current evidence did not retain the cutoff-date observation: asOf=%s facts=%+v", check.AsOfUTC, check.Facts)
	}
	if len(historical.Facts) != 1 || historical.Facts[0].Value != "75" || historical.Facts[0].TemporalStatus != EvidenceTemporalHistoricalOnly {
		t.Fatalf("historical evidence did not retain only the dated prior observation: %+v", historical.Facts)
	}
	if len(check.Sources) != 1 || len(historical.Sources) != 1 {
		t.Fatalf("mixed source should remain attributed in both partitions: current=%d historical=%d", len(check.Sources), len(historical.Sources))
	}
	for _, source := range append(check.Sources, historical.Sources...) {
		if source.Title != "" || source.Excerpt != "" || source.PageSummary != "" {
			t.Fatalf("mixed source prose should be scrubbed after fact partitioning: %+v", source)
		}
	}

	turn := turnContext{AsOf: asOf, EvidenceCheck: check, HistoricalEvidence: historical}
	request, err := acceptedReplyCompletionRequestForTurnContext("anthropic/claude-sonnet-5.5", "high", 6000, false, turn)
	if err != nil {
		t.Fatalf("build evidence-aware request: %v", err)
	}
	const contextIntro = "Use the saved draft, recent message history, selected choice, and current user turn below. Return a complete updated working interpretation in the required JSON schema. Do not invent evidence.\n\n"
	if !strings.HasPrefix(request.UserPrompt, contextIntro) {
		t.Fatalf("unexpected model-context prefix: %q", request.UserPrompt)
	}
	var modelContext turnContext
	if err := json.Unmarshal([]byte(strings.TrimPrefix(request.UserPrompt, contextIntro)), &modelContext); err != nil {
		t.Fatalf("decode serialized model context: %v", err)
	}
	if !modelContext.AsOf.Equal(asOf) || len(modelContext.EvidenceCheck.Facts) != 1 || len(modelContext.HistoricalEvidence.Facts) != 1 {
		t.Fatalf("serialized current and historical evidence lost the shared cutoff or partition: %+v", modelContext)
	}

	response := decisionPointTurnOutput{
		DecisionPoint: &DecisionPointOutput{Kind: "expectations", Conclusion: "The current estimate supports this judgment.", FactIndices: []int{0}},
		Turn:          &TurnModelOutput{AssistantMessage: "The current estimate is relevant.", Decision: "REPLY", Stage: "DEVELOPING"},
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode response using current evidence: %v", err)
	}
	if _, _, err := decodeDecisionPointTurnOutputForContext(string(encoded), turn); err != nil {
		t.Fatalf("current evidence index should be valid: %v", err)
	}

	turn.EvidenceCheck.Facts = []EvidenceCheckFact{}
	if _, _, err := decodeDecisionPointTurnOutputForContext(string(encoded), turn); err == nil {
		t.Fatal("historical-only evidence must not validate a current decisionPoint index")
	}
}

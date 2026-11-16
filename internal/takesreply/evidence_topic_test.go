package takesreply

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/llm"
)

func TestOptionalFinancialEvidenceRequiresExplicitTopic(t *testing.T) {
	for _, test := range []struct {
		brief string
		want  bool
	}{
		{"I like to shop at Costco because it is cheap", false},
		{"Costco shopping is cheap and its membership business looks useful", false},
		{"I don't need analyst forecasts. I like Costco's business activity", false},
		{"I do not want earnings estimates", false},
		{"Costco's business model without analyst forecasts", false},
		{"No valuation review please", false},
		{"Is COST stock cheap at 43x earnings?", true},
		{"What is Costco's valuation?", true},
		{"What do analysts forecast for Costco EPS?", true},
		{"I don't need forecasts; what is the stock price valuation?", true},
	} {
		if got := wantsOptionalFinancialContext(test.brief); got != test.want {
			t.Errorf("%q got %t want %t", test.brief, got, test.want)
		}
	}
}

type topicGateTransport struct{ calls int }

func (t *topicGateTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return nil, errors.New("unexpected enrichment: synthetic test transport")
}

func TestBusinessBriefCannotReceiveUnrequestedForecastEnrichment(t *testing.T) {
	const forecastURL = "https://stockanalysis.com/stocks/cost/forecast/"
	const quote = "Costco analysts forecast EPS growth of 11% for fiscal 2027."
	asOf := time.Date(2026, 10, 28, 22, 0, 0, 0, time.UTC)
	published := asOf.Add(-24 * time.Hour)
	body, _ := json.Marshal(decisionEvidenceOutput{Evidence: []decisionEvidenceSelection{{PublicURL: forecastURL, Subject: "Costco", Excerpt: quote, Category: "reported_estimate_or_guidance"}}})
	for _, brief := range []string{"I like to shop at Costco because it is cheap", "I don't need analyst forecasts. Costco operates membership warehouses"} {
		model := &replyContractCompleter{response: string(body), receipt: llm.WebSearchReceipt{Citations: []llm.WebCitation{{URL: forecastURL, Title: "Costco forecast", Content: quote, PublishedAt: &published}}}, searchOK: true}
		transport := &topicGateTransport{}
		service := &Service{llm: model, model: defaultEvidenceModel, decisionEvidenceTransport: transport}
		current, historical, diagnostics := service.prebuildDecisionEvidenceCheckAt(context.Background(), brief, asOf)
		if model.request.SystemPrompt != businessDecisionEvidenceSystem || transport.calls != 0 || len(current.Facts) != 0 || len(historical.Facts) != 0 || diagnostics.Rejections.UnrequestedFinancialContext != 1 {
			t.Fatalf("unrequested forecast entered the reply context: current=%+v historical=%+v diagnostics=%+v reads=%d", current, historical, diagnostics, transport.calls)
		}
	}
}
func TestExplicitFinancialQuestionKeepsAcceptedEvidenceRoute(t *testing.T) {
	model := &replyContractCompleter{response: `{"evidence":[]}`, searchOK: true}
	service := &Service{llm: model, model: defaultEvidenceModel}
	service.prebuildDecisionEvidenceCheck(context.Background(), "Is COST stock cheap at 43x earnings?")
	if model.request.SystemPrompt != decisionEvidenceSystem || !model.request.OpenRouterWebSearch || !strings.Contains(model.request.SystemPrompt, "forward-forecast pages") {
		t.Fatal("explicit financial question lost the accepted bounded financial route")
	}
}

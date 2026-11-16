package takesreply

import (
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"github.com/google/uuid"
)

func TestCanonicalBuildChoiceIsAddedOnceOnlyForEligibleBeliefMenus(t *testing.T) {
	belief := "More small businesses will pay for bookkeeping software."
	input := TurnModelOutput{
		Decision: "REPLY", Stage: "CHOICES",
		Belief: BeliefOutput{UserStatement: &belief, IntentStatus: "INVESTMENT_THESIS"},
		Choices: []ChoiceOutput{
			{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Intuit"}}, Label: "Intuit", ExposureRole: "named_company", Description: "Focus on Intuit."},
			{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Sage"}}, Label: "Sage", ExposureRole: "peer_company", Description: "Compare with Sage."},
			{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, Label: "Broader accounting software", ExposureRole: "category", Description: "Compare the wider category."},
			{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, Label: "Build now", ExposureRole: "BUILD_NOW", Description: "Model action."},
		},
	}
	if err := validateConversationChoices(input); err != nil {
		t.Fatalf("three routes plus one model action should be valid: %v", err)
	}
	got := normalizeConversationChoices(input, Belief{})
	if len(got.Choices) != 4 || got.Choices[0].Label != "Intuit" || got.Choices[1].Label != "Sage" || got.Choices[2].Label != "Broader accounting software" {
		t.Fatalf("route menu was not preserved within the four-item cap: %+v", got.Choices)
	}
	if !selectedChoiceRequestsBuild(&Choice{Label: got.Choices[3].Label, ExposureRole: got.Choices[3].ExposureRole}) || got.BuildRequested {
		t.Fatalf("expected one canonical action without implicit authorization, got %+v", got)
	}

	duplicates := input
	duplicates.Choices = []ChoiceOutput{
		input.Choices[0], input.Choices[1], input.Choices[3],
		{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, Label: "Build it now", ExposureRole: "other_role", Description: "Second action alias."},
	}
	got = normalizeConversationChoices(duplicates, Belief{})
	buildActions := 0
	for _, choice := range got.Choices {
		if selectedChoiceRequestsBuild(&Choice{Label: choice.Label, ExposureRole: choice.ExposureRole}) {
			buildActions++
		}
	}
	if len(got.Choices) != 3 || buildActions != 1 {
		t.Fatalf("model action aliases should collapse to one canonical item: %+v", got.Choices)
	}
}

func TestBuildChoiceIsRemovedFromIneligibleMenuWithoutErasingSelectedRoute(t *testing.T) {
	belief := "Costco may benefit if more shoppers trade down."
	route := Choice{ID: uuid.NewString(), Label: "Costco alone", ExposureRole: "named_company"}
	selectedRoute := Choice{ID: uuid.NewString(), Label: "Costco and peers", ExposureRole: "peer_companies"}
	build := Choice{ID: uuid.NewString(), Label: "Build it now", ExposureRole: "build_now"}
	old := Draft{
		ThreadID: uuid.NewString(), Belief: Belief{UserStatement: &belief},
		Choices: []Choice{route, selectedRoute, build}, SelectedChoiceIDs: []string{route.ID},
	}
	answer := normalizeConversationChoices(TurnModelOutput{Decision: "REPLY", Stage: "DEVELOPING"}, old.Belief)
	got := applyTurnOutput(old, answer, uuid.New(), nil, "How do analysts calculate forward P/E?", time.Unix(2, 0))
	if got.Stage != "DEVELOPING" || len(got.Choices) != 2 || hasChoiceID(got.Choices, build.ID) {
		t.Fatalf("answer-only update retained Build now or erased route state: %+v", got)
	}
	if len(got.SelectedChoiceIDs) != 1 || got.SelectedChoiceIDs[0] != route.ID || got.Belief.UserStatement == nil || *got.Belief.UserStatement != belief {
		t.Fatalf("answer-only update lost selected route or belief: %+v", got)
	}

	selectedID := selectedRoute.ID
	ready := normalizeConversationChoices(TurnModelOutput{Decision: "READY_TO_BUILD", Stage: "READY_TO_BUILD"}, got.Belief)
	got = applyTurnOutput(got, ready, uuid.New(), &selectedID, "Use the broader route.", time.Unix(3, 0))
	if !hasChoiceID(got.Choices, selectedID) || buildChoiceIndex(got.Choices) < 0 {
		t.Fatalf("selected research route and Build now action should remain available: %+v", got)
	}
}

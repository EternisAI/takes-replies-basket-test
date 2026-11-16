package takesreply

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"github.com/google/uuid"
)

func TestSelectedCompanyScopeSurvivesModelMenuAndOrdinaryFollowups(t *testing.T) {
	statement, why, thesis := "I like Costco because prices are low.", "If shoppers trade down, warehouse clubs may attract spending.", "The clubs may benefit from trading down."
	named := takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco Wholesale", "BJ's Wholesale Club"}}
	route := Choice{ID: "clubs", Label: "Costco and BJ's", ExposureRole: "named_companies", CompanyScope: &named}
	old := Draft{ThreadID: "scope-thread", Choices: []Choice{route}, SelectedChoiceIDs: []string{}, Belief: Belief{
		UserStatement: &statement, CausalMechanism: &why, IntentStatus: "PERSONAL_PREFERENCE",
		InvestmentThesis:    InvestmentThesis{Statement: &thesis, Status: "INFERRED_UNCONFIRMED"},
		PersonalPreferences: []PersonalPreference{{Subject: "Costco", Preference: "low prices", SourceMessageID: uuid.New()}},
	}}
	// The model tries to change the selected route's scope under the same label.
	out := TurnModelOutput{Decision: "READY_TO_BUILD", Stage: "READY_TO_BUILD", Choices: []ChoiceOutput{{
		Label: route.Label, ExposureRole: route.ExposureRole, Description: "Research the clubs.",
		CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}},
	}}}
	selected := route.ID
	got := applyTurnOutput(old, out, uuid.New(), &selected, "", time.Unix(2, 0))
	if got.SelectedCompanyScope == nil || got.SelectedCompanyScope.Scope.Kind != takesresearch.CompanyScopeNamed || got.SelectedCompanyScope.SourceChoiceID != route.ID || got.SelectedCompanyScope.Origin != "MODEL_PROPOSED_USER_SELECTED" || got.SelectedCompanyScope.NeedsConfirmation {
		t.Fatalf("model changed accepted scope or provenance: %+v", got.SelectedCompanyScope)
	}
	if got.Choices[0].CompanyScope == nil || !slices.Equal(got.Choices[0].CompanyScope.IssuerNames, named.IssuerNames) {
		t.Fatal("model output overwrote the selected route's typed company names")
	}
	snapshot, err := json.Marshal(got.SelectedCompanyScope)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"Make Costco the largest holding.", "How does membership work?", "Maybe look at another sector."} {
		updated := applyTurnOutput(got, out, uuid.New(), nil, message, time.Unix(3, 0))
		if updated.SelectedCompanyScope == nil || !updated.SelectedCompanyScope.NeedsConfirmation || updated.SelectedCompanyScope.Scope.Kind != takesresearch.CompanyScopeNamed || !slices.Equal(updated.SelectedCompanyScope.Scope.IssuerNames, named.IssuerNames) {
			t.Fatalf("raw text silently broadened the accepted boundary: %+v", updated.SelectedCompanyScope)
		}
		if *updated.Belief.UserStatement != statement || *updated.Belief.CausalMechanism != why || updated.Belief.InvestmentThesis.Status != "INFERRED_UNCONFIRMED" || len(updated.Belief.PersonalPreferences) != 1 {
			t.Fatal("scope handling changed the saved belief, uncertainty or personal preference")
		}
		updated.SelectedCompanyScope.Scope.IssuerNames[0] = "Changed only in new revision"
		unchanged, err := json.Marshal(got.SelectedCompanyScope)
		if err != nil || string(unchanged) != string(snapshot) {
			t.Fatal("a later revision mutated the prior scope snapshot")
		}
	}
}

func TestOnlyAnExplicitNewRouteSelectionReplacesCompanyScope(t *testing.T) {
	named := takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco Wholesale"}}
	open := takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}
	old := Draft{ThreadID: "scope-thread", Choices: []Choice{
		{ID: "broad", Label: "Wider consumer theme", ExposureRole: "category", CompanyScope: &open},
		{ID: "build", Label: "Build it now", ExposureRole: "build_now", CompanyScope: &open},
	}, SelectedChoiceIDs: []string{"clubs"}, SelectedCompanyScope: &takesresearch.SelectedCompanyScope{
		Scope: named, SourceChoiceID: "clubs", Origin: "MODEL_PROPOSED_USER_SELECTED", NeedsConfirmation: true,
	}}
	buildID := "build"
	built := applyTurnOutput(old, TurnModelOutput{Stage: "READY_TO_BUILD"}, uuid.New(), &buildID, "", time.Unix(2, 0))
	if built.SelectedCompanyScope == nil || built.SelectedCompanyScope.Scope.Kind != takesresearch.CompanyScopeNamed || built.SelectedCompanyScope.SourceChoiceID != "clubs" {
		t.Fatal("Build action must not overwrite a named route with its action-only OPEN_THEME scope")
	}
	selected := "broad"
	got := applyTurnOutput(old, TurnModelOutput{Stage: "READY_TO_BUILD"}, uuid.New(), &selected, "", time.Unix(3, 0))
	if got.SelectedCompanyScope == nil || got.SelectedCompanyScope.Scope.Kind != takesresearch.CompanyScopeOpen || got.SelectedCompanyScope.SourceChoiceID != selected || got.SelectedCompanyScope.NeedsConfirmation {
		t.Fatalf("explicit accepted broader route did not replace pending scope: %+v", got.SelectedCompanyScope)
	}
	legacyID := "legacy"
	legacy := Draft{ThreadID: "scope-thread", Choices: []Choice{{ID: legacyID, Label: "Costco and BJ's", ExposureRole: "named_companies"}}}
	got = applyTurnOutput(legacy, TurnModelOutput{Stage: "READY_TO_BUILD"}, uuid.New(), &legacyID, "", time.Unix(4, 0))
	if got.SelectedCompanyScope != nil {
		t.Fatal("legacy prose must not be backfilled into an asserted structured scope")
	}
}

func TestForwardChoiceScopeIsValidatedBeforeSaving(t *testing.T) {
	out := TurnModelOutput{Stage: "CHOICES", Choices: []ChoiceOutput{{Label: "Club route", ExposureRole: "named_companies", Description: "Research these companies."}}}
	if err := validateConversationChoices(out); err == nil {
		t.Fatal("new model routes require a structured scope")
	}
	out.Choices[0].CompanyScope = takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco Wholesale", " costco wholesale "}}
	if err := validateConversationChoices(out); err == nil {
		t.Fatal("duplicate scope names must not enter the saved choice")
	}
	out.Choices[0].CompanyScope.IssuerNames = []string{"Costco Wholesale"}
	if err := validateConversationChoices(out); err != nil {
		t.Fatal(err)
	}
}

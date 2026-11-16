package takesreply

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"github.com/google/uuid"
)

func TestOptionalAnchorsPreserveOldScopeBytesAndSelectedProvenance(t *testing.T) {
	for _, test := range []struct {
		scope takesresearch.CompanyScope
		raw   string
	}{
		{takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco"}}, `{"kind":"NAMED_COMPANIES","issuerNames":["Costco"]}`},
		{takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, `{"kind":"OPEN_THEME","issuerNames":[]}`},
	} {
		raw, _ := json.Marshal(test.scope)
		if string(raw) != test.raw {
			t.Fatal("absent anchors changed canonical scope bytes", string(raw))
		}
	}
	named := takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco", "BJ's Wholesale"}}
	anchored := takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}, AnchorIssuerNames: []string{"Costco", "BJ's Wholesale"}}
	old := Draft{ThreadID: "scope", Choices: []Choice{{ID: "named", Label: "Only these companies", CompanyScope: &named}, {ID: "anchored", Label: "Broader with these anchors", CompanyScope: &anchored}}, SelectedCompanyScope: &takesresearch.SelectedCompanyScope{Scope: named, SourceChoiceID: "named", Origin: "MODEL_PROPOSED_USER_SELECTED"}}
	out := TurnModelOutput{Decision: "REPLY", Stage: "CHOICES", Choices: []ChoiceOutput{{Label: "Broader with these anchors", CompanyScope: anchored}}}
	pending := applyTurnOutput(old, out, uuid.New(), nil, "Show a broader idea with these companies as anchors.", time.Now())
	if pending.SelectedCompanyScope.Scope.Kind != takesresearch.CompanyScopeNamed || len(pending.SelectedCompanyScope.Scope.AnchorIssuerNames) != 0 {
		t.Fatal("ordinary request silently accepted anchors")
	}
	selected := "anchored"
	got := applyTurnOutput(old, out, uuid.New(), &selected, "", time.Now())
	if got.SelectedCompanyScope.SourceChoiceID != selected || got.SelectedCompanyScope.Origin != "MODEL_PROPOSED_USER_SELECTED" || len(got.SelectedCompanyScope.Scope.AnchorIssuerNames) != 2 {
		t.Fatal("real route selection lost anchors/provenance")
	}
	snapshot, _ := json.Marshal(got.SelectedCompanyScope)
	newer := applyTurnOutput(got, TurnModelOutput{}, uuid.New(), nil, "Make Costco the largest.", time.Now())
	newer.SelectedCompanyScope.Scope.AnchorIssuerNames[0] = "Changed in later revision"
	after, _ := json.Marshal(got.SelectedCompanyScope)
	if string(snapshot) != string(after) || anchored.AnchorIssuerNames[0] != "Costco" {
		t.Fatal("later revision mutated selected anchor provenance")
	}
}

func TestAnchorScopeBoundsAndSchemaDescriptions(t *testing.T) {
	for _, s := range []takesresearch.CompanyScope{
		{Kind: takesresearch.CompanyScopeNamed, IssuerNames: []string{"Costco"}, AnchorIssuerNames: []string{"Costco"}},
		{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}, AnchorIssuerNames: []string{"Costco", " costco "}},
		{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}, AnchorIssuerNames: make([]string, 9)},
	} {
		if s.Validate() == nil {
			t.Fatal("invalid anchor contract accepted")
		}
	}
	schema, err := decisionPointTurnModelOutputSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(schema)
	for _, want := range []string{"anchorIssuerNames", "newly offered OPEN_THEME route", "selecting this real route accepts"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("schema lacks anchor instruction %q", want)
		}
	}
	// Schema guidance complements the isolated compatibility appendix; the
	// exact original CandidateB base remains independently hash-checked.
}

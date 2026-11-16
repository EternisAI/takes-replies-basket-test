package takesresearch

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	CompanyScopeNamed = "NAMED_COMPANIES"
	CompanyScopeOpen  = "OPEN_THEME"
)

// CompanyScope is a model-proposed route, accepted by selecting its choice.
// Issuer names establish requested scope, never identity or instrument access.
type CompanyScope struct {
	Kind              string   `json:"kind" jsonschema:"enum=NAMED_COMPANIES,enum=OPEN_THEME" jsonschema_description:"NAMED_COMPANIES only for an explicitly restricted company-only route. OPEN_THEME for thematic, behavioral, category or broader economic interpretations; example companies in the wording do not close that universe."`
	IssuerNames       []string `json:"issuerNames" jsonschema:"maxItems=8" jsonschema_description:"For NAMED_COMPANIES, the complete explicitly restricted issuer list. For OPEN_THEME always empty, even when the question, label or description names illustrative companies; examples are not automatic holdings or anchors."`
	AnchorIssuerNames []string `json:"anchorIssuerNames,omitempty" jsonschema:"maxItems=8" jsonschema_description:"Optional only on a newly offered OPEN_THEME route: retain public issuers only if the person explicitly requests retention or this route clearly offers named core anchors for their acceptance. Observation companies and illustrative examples are not automatic anchors. NAMED_COMPANIES stays exclusive through issuerNames and has no anchors. Never infer accepted anchors from mere mentions; selecting this real route accepts the proposed anchor list."`
}

func (s CompanyScope) Validate() error {
	if s.Kind != CompanyScopeNamed && s.Kind != CompanyScopeOpen {
		return errors.New("company scope kind is missing or unsupported")
	}
	if len(s.IssuerNames) > maxBasketResearchCandidates || (s.Kind == CompanyScopeNamed && len(s.IssuerNames) == 0) || (s.Kind == CompanyScopeOpen && len(s.IssuerNames) != 0) {
		return errors.New("company scope names do not match the route kind")
	}
	if len(s.AnchorIssuerNames) > maxBasketResearchCandidates || (s.Kind == CompanyScopeNamed && len(s.AnchorIssuerNames) > 0) {
		return errors.New("company scope anchors require a bounded open route")
	}
	seen := map[string]bool{}
	for _, name := range append(append([]string{}, s.IssuerNames...), s.AnchorIssuerNames...) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || len(name) > 120 || seen[key] {
			return errors.New("company scope requires distinct bounded issuer names")
		}
		seen[key] = true
	}
	return nil
}

type SelectedCompanyScope struct {
	Scope             CompanyScope `json:"scope"`
	SourceChoiceID    string       `json:"sourceChoiceId"`
	Origin            string       `json:"origin"`
	NeedsConfirmation bool         `json:"needsConfirmation,omitempty"`
}

type ResearchIdea struct {
	UserStatement            string                `json:"userStatement"`
	SavedMechanism           string                `json:"savedMechanism"`
	IntentStatus             string                `json:"intentStatus"`
	WorkingThesis            string                `json:"workingThesis"`
	ThesisStatus             string                `json:"thesisStatus"`
	SelectedRouteID          string                `json:"selectedRouteId"`
	SelectedRouteLabel       string                `json:"selectedRouteLabel"`
	SelectedRouteDescription string                `json:"selectedRouteDescription"`
	CompanyScope             *SelectedCompanyScope `json:"companyScope"`
	ScopeVerification        string                `json:"scopeVerification"`
}

func savedResearchIdea(raw json.RawMessage) (ResearchIdea, error) {
	var draft struct {
		Belief            json.RawMessage `json:"belief"`
		SelectedChoiceIDs []string        `json:"selectedChoiceIds"`
		Choices           []struct {
			ID           string `json:"id"`
			Label        string `json:"label"`
			Description  string `json:"description"`
			ExposureRole string `json:"exposureRole"`
		} `json:"choices"`
		SelectedScope *SelectedCompanyScope `json:"selectedCompanyScope"`
	}
	if err := json.Unmarshal(raw, &draft); err != nil {
		return ResearchIdea{}, err
	}
	var belief struct {
		UserStatement    string `json:"userStatement"`
		CausalMechanism  string `json:"causalMechanism"`
		IntentStatus     string `json:"intentStatus"`
		InvestmentThesis struct {
			Statement string `json:"statement"`
			Status    string `json:"status"`
		} `json:"investmentThesis"`
	}
	if len(draft.Belief) > 0 && string(draft.Belief) != "null" {
		if draft.Belief[0] == '"' {
			// Older snapshots stored only the statement. Preserve it without
			// inventing a confirmed mechanism or typed company scope.
			if err := json.Unmarshal(draft.Belief, &belief.UserStatement); err != nil {
				return ResearchIdea{}, err
			}
		} else if err := json.Unmarshal(draft.Belief, &belief); err != nil {
			return ResearchIdea{}, err
		}
	}
	idea := ResearchIdea{UserStatement: belief.UserStatement, SavedMechanism: belief.CausalMechanism, IntentStatus: belief.IntentStatus, WorkingThesis: belief.InvestmentThesis.Statement, ThesisStatus: belief.InvestmentThesis.Status, ScopeVerification: "LEGACY_UNVERIFIED"}
	for _, id := range draft.SelectedChoiceIDs {
		for _, choice := range draft.Choices {
			if choice.ID == id && choice.ExposureRole != "build_now" {
				idea.SelectedRouteID = id
				idea.SelectedRouteLabel = choice.Label
				idea.SelectedRouteDescription = choice.Description
			}
		}
	}
	if draft.SelectedScope != nil {
		scope := draft.SelectedScope
		if scope.Origin != "MODEL_PROPOSED_USER_SELECTED" || scope.SourceChoiceID == "" {
			return ResearchIdea{}, errors.New("selected company scope has no accepted-choice provenance")
		}
		if err := scope.Scope.Validate(); err != nil {
			return ResearchIdea{}, err
		}
		idea.CompanyScope = scope
		idea.ScopeVerification = "SELECTED_CHOICE_PENDING_ISSUER_CHECK"
		if scope.NeedsConfirmation {
			idea.ScopeVerification = "SELECTED_SCOPE_RECONFIRMATION_PENDING"
		}
	}
	return idea, nil
}

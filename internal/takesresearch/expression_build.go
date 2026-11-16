package takesresearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

func (b *Builder) buildExpression(ctx context.Context, input Input) (Output, error) {
	out := Output{Status: StatusFailed}
	if b.expressionModel == nil || b.expressionIdentity == nil {
		return out, errors.New("exploratory dependencies are not configured")
	}
	if hasError(takeintelligence.ValidateCatalog(input.StartSnapshot, input.StartSnapshot.CompletedAt)) {
		return out, errors.New("exploratory start catalog failed validation")
	}
	saved, err := parseSavedDraftConstraints(input.DraftJSON)
	if err != nil {
		return out, err
	}
	idea, err := savedResearchIdea(input.DraftJSON)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(idea.UserStatement) == "" && strings.TrimSpace(idea.SavedMechanism) == "" && strings.TrimSpace(idea.WorkingThesis) == "" {
		return out, errors.New("an exploratory expression needs a saved user idea")
	}
	limits := []string{"Company connections and mix emphasis are model proposals, not verified business research or measured risk/return. No earnings, filing, price or news research was run for this blueprint."}
	constraintBlocked := false
	if reason := validateSavedCashConstraint(input, &saved); reason != nil {
		limits = append(limits, *reason)
		constraintBlocked = true
	}
	for _, f := range validateSavedUserExclusions(input, &saved) {
		limits = append(limits, f.Message)
		if f.Severity == takeintelligence.SeverityError {
			constraintBlocked = true
		}
	}
	preferences := ProposalDraft{}
	for _, f := range applyVerifiedSizingPreferences(&preferences, input, saved) {
		if f.Severity == takeintelligence.SeverityError {
			constraintBlocked = true
		}
		limits = append(limits, f.Message)
	}
	var horizon *int
	if preferences.HorizonOrigin == "user" {
		days := preferences.HorizonDays
		horizon = &days
	}
	plan, err := b.expressionModel.PlanExpression(ctx, ExpressionPlanningInput{Idea: idea, Exclusions: saved.Exclusions, HorizonDays: horizon, RiskStyle: string(preferences.RiskStyle)})
	if err != nil {
		return out, fmt.Errorf("propose exploratory mix: %w", err)
	}
	if err := validateExpressionPlan(plan); err != nil {
		return out, err
	}
	identityCtx, cancel := context.WithTimeout(ctx, expressionIdentityTimeout)
	entries, retrieved, identityErr := b.expressionIdentity.Tickers(identityCtx)
	cancel()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if identityErr == nil && (retrieved.IsZero() || retrieved.After(clock.Now().UTC())) {
		entries = nil
		identityErr = errors.New("invalid identity retrieval time")
	}
	if identityErr != nil {
		limits = append(limits, "Company identity metadata was unavailable. Suggestions remain exploratory; no unverified company was mapped to a contract.")
	}
	issuers := make(map[string]basketResearchIssuer, len(plan.Companies))
	issuerCounts := map[string]int{}
	for _, company := range plan.Companies {
		issuer, e := basketResearchIssuerFromIndex(basketResearchSourceCandidate{ID: company.ID, IssuerName: company.IssuerName}, entries, retrieved, false)
		if e == nil {
			issuers[company.ID] = issuer
			issuerCounts[canonicalCIK(issuer.CIK)]++
		}
	}
	if err := validateExpressionScope(idea, plan, entries); err != nil {
		return out, err
	}
	view := ExpressionView{Story: &ExpressionStory{Version: expressionStoryVersion, Basis: "MODEL_INFERENCE", ExpressionStoryPlan: plan.Story}, Kind: "EXPLORATORY_BLUEPRINT", Readiness: "READY_FOR_REVIEW", Basis: "MODEL_INFERENCE", Belief: idea, Scope: idea.CompanyScope, Roles: []ExpressionRole{}, Companies: []ExpressionCompany{}, AvailableContracts: []ExpressionContract{}, Mix: ExpressionMix{Status: "ILLUSTRATIVE_PROPOSAL", Denominator: expressionDenominator}, OverlapRisks: plan.OverlapRisks, HorizonDays: horizon, Limitations: limits}
	if idea.CompanyScope == nil {
		view.Readiness = "NEEDS_REVIEW"
		view.Limitations = append(view.Limitations, "This older route has no typed company scope; the proposed universe needs confirmation before it represents an accepted scope.")
	}
	if idea.CompanyScope != nil && idea.CompanyScope.NeedsConfirmation {
		view.Readiness = "NEEDS_REVIEW"
		view.Limitations = append(view.Limitations, "A later message may change the last selected company scope. The boundary was retained; confirm a new route before treating it as settled.")
	}
	view.MissingCompanies = expressionMissingNames(idea, plan, entries)
	if len(view.MissingCompanies) > 0 {
		constraintBlocked = true
		view.Readiness = "NEEDS_REVIEW"
		view.Limitations = append(view.Limitations, "The blueprint omitted selected companies or anchors; their intended shares are unknown, so no mix was assigned.")
	}
	for _, role := range plan.Roles {
		view.Roles = append(view.Roles, ExpressionRole{ID: role.ID, Label: role.Label, ProposedMechanism: role.ProposedMechanism, ExposureKind: role.ExposureKind, WhyThisRole: role.WhyThisRole, PlannedMixPercent: expressionPercent(float64(role.PlannedMixPercent))})
	}
	catalog, _ := ResearchCatalog(input.StartSnapshot)
	product, productVerified := standingProductRequest(input)
	productLimit := product.Shortfall(catalog)
	allowContracts := productVerified && productLimit == ""
	if !allowContracts {
		view.Limitations = append(view.Limitations, "The current market does not satisfy the confirmed product/access instructions; proposed companies were not converted to other products.")
	}
	researchPlan := ResearchPlan{}
	for _, r := range plan.Roles {
		researchPlan.BusinessRoles = append(researchPlan.BusinessRoles, PlannedBusinessRole{ID: r.ID, Label: r.Label})
	}
	for _, company := range plan.Companies {
		displayName := expressionScopeName(idea, company.IssuerName, entries)
		candidate := ResearchPoolCandidate{ID: company.ID, IssuerName: displayName, Aliases: []string{company.IssuerName}, BusinessRoleIDs: []string{company.RoleID}}
		researchPlan.Candidates = append(researchPlan.Candidates, candidate)
		row := ExpressionCompany{PortfolioRole: company.PortfolioRole, DistinctiveContribution: company.DistinctiveContribution, ExtraAssumption: company.ExtraAssumption, ID: company.ID, IssuerName: displayName, RoleID: company.RoleID, ProposedConnection: company.ProposedConnection, WhyThisShare: company.WhyThisShare, WeightBasis: "MODEL_INFERENCE", Basis: "MODEL_INFERENCE", SourceSupport: "NOT_RESEARCHED", PlannedMixPercent: expressionPercent(float64(company.PlannedMixPercent)), Identity: ExpressionIdentity{Status: "UNRESOLVED"}, Availability: "IDENTITY_UNRESOLVED"}
		if candidateMatchesExclusion(candidate, saved.Exclusions) {
			constraintBlocked = true
			row.Availability = "USER_EXCLUDED"
			view.Limitations = append(view.Limitations, company.IssuerName+" was excluded by a confirmed user instruction; its proposed fraction is unassigned.")
		}
		if issuer, ok := issuers[company.ID]; ok {
			stamp := issuer.RetrievedAt
			row.Identity = ExpressionIdentity{Status: "RESOLVED", OfficialName: issuer.OfficialName, CIK: canonicalCIK(issuer.CIK), RetrievedAt: &stamp, PublicURL: secCompanyTickerURL}
			switch {
			case row.Availability == "USER_EXCLUDED":
			case issuerCounts[canonicalCIK(issuer.CIK)] > 1:
				constraintBlocked = true
				view.Limitations = append(view.Limitations, "Several proposed names resolve to the same issuer; confirm the intended company universe before sizing.")
				row.Availability = "DUPLICATE_ISSUER"
			case !allowContracts:
				row.Availability = "PRODUCT_UNAVAILABLE"
			default:
				mapping := basketResearchCandidateMapping(company.ID, issuer, entries, input.StartSnapshot)
				row.Availability = "NO_EXACT_INSTRUMENT"
				// One exact contract per company; never duplicate one issuer's planned share.
				if mapping.Status == CandidateMappingMapped && len(mapping.ReviewedMappings) > 0 {
					reviewed := mapping.ReviewedMappings[0]
					if !hasError(takeintelligence.ValidateMappings(input.StartSnapshot, []takeintelligence.UnderlyingMapping{reviewed}, clock.Now())) {
						row.Availability = "AVAILABLE"
						view.AvailableContracts = append(view.AvailableContracts, ExpressionContract{CompanyID: company.ID, Key: reviewed.Key, Mapping: reviewed, PlannedMixPercent: row.PlannedMixPercent, AccountAccess: "UNKNOWN"})
					} else {
						row.Availability = "MAPPING_UNVERIFIED"
					}
				}
			}
		}
		view.Companies = append(view.Companies, row)
	}
	if saved.RequestedHoldings != nil && *saved.RequestedHoldings != len(view.Companies) {
		constraintBlocked = true
		view.Limitations = append(view.Limitations, "The proposed company count does not satisfy the saved requested count; no extra name was invented.")
	}
	constraints := resolveUserAllocation(input, researchPlan, saved)
	applyExpressionMix(&view, plan, constraints, constraintBlocked)
	refreshExpressionStoryEmphasis(&view)
	// Completion remains read-only metadata. No price or evidence resolver is called.
	completion, err := b.catalog.CaptureCatalog(ctx)
	if err != nil {
		return out, fmt.Errorf("refresh exploratory catalog: %w", err)
	}
	if hasError(takeintelligence.ValidateCatalog(completion, completion.CompletedAt)) {
		return out, errors.New("exploratory completion catalog failed validation")
	}
	retainExpressionMappings(&view, completion)
	projection := ProposalView{ConstructionMode: ExpressionMode, Expression: &view, Status: string(StatusReady), BuildID: input.BuildID, StartSnapshotID: input.StartSnapshot.ID, StartSnapshotAt: input.StartSnapshot.CompletedAt, CompletionSnapshotID: completion.ID, Interpretation: "A proposed company mix expressing your idea; company connections have not been researched.", InterpretationStatus: "EXPLORATORY_NOT_RESEARCHED", AsOf: clock.Now().UTC(), Holdings: []json.RawMessage{}, Evidence: []json.RawMessage{}, Playbook: []json.RawMessage{}, Assessment: ProposalAssessmentView{Complete: false, Ready: false, LiveEligible: false}}
	out.Status = StatusReady
	if view.Readiness == "NEEDS_REVIEW" {
		out.Status = StatusPartial
		projection.Status = string(out.Status)
		out.MissingRequirements = view.Limitations[1:]
	}
	out.CompletionSnapshot = completion
	out.ProposalJSON, err = json.Marshal(projection)
	if err != nil {
		return Output{}, err
	}
	out.AssessmentJSON, err = json.Marshal(projection.Assessment)
	if err != nil {
		return Output{}, err
	}
	if err := ValidateExpressionResult(out.ProposalJSON, input.BuildID, completion); err != nil {
		return Output{}, err
	}
	return out, nil
}

func validateExpressionScope(idea ResearchIdea, p ExpressionPlan, entries []secExchangeTicker) error {
	if idea.CompanyScope == nil {
		return nil
	}
	if idea.CompanyScope.Scope.Kind == CompanyScopeOpen {
		for _, c := range p.Companies {
			for _, anchor := range idea.CompanyScope.Scope.AnchorIssuerNames {
				if expressionScopeName(idea, c.IssuerName, entries) == anchor && c.PortfolioRole != "CORE" {
					return errors.New("selected expression anchor must be core")
				}
			}
		}
		return nil
	}
	if p.Story.AlternativeOrGap != nil && p.Story.AlternativeOrGap.Kind != "MISSING_ANGLE" {
		return errors.New("exclusive expression scope permits only an in-scope missing angle")
	}
	if len(p.Companies) > len(idea.CompanyScope.Scope.IssuerNames) {
		return errors.New("exploratory plan expanded the selected named scope")
	}
	allowed := map[string]bool{}
	literal := map[string]bool{}
	for _, name := range idea.CompanyScope.Scope.IssuerNames {
		literal[strings.ToLower(strings.TrimSpace(name))] = true
		if entry, ok := matchNamedIssuer(entries, name); ok {
			allowed[canonicalCIK(entry.CIK)] = true
		}
	}
	for _, c := range p.Companies {
		if literal[strings.ToLower(strings.TrimSpace(c.IssuerName))] {
			continue
		}
		entry, ok := matchNamedIssuer(entries, c.IssuerName)
		if !ok || !allowed[canonicalCIK(entry.CIK)] {
			return errors.New("exploratory company is outside the selected named scope")
		}
	}
	return nil
}

func applyExpressionMix(v *ExpressionView, plan ExpressionPlan, c resolvedUserAllocation, blocked bool) {
	if blocked || c.Unresolved {
		v.Mix.Status = "UNRESOLVED"
		v.Readiness = "NEEDS_REVIEW"
		v.Limitations = append(v.Limitations, firstNonEmpty(c.Context.CoverageNote, "Saved constraints could not be verified; no weights were assigned."))
		for i := range v.Companies {
			v.Companies[i].PlannedMixPercent = nil
			v.Companies[i].WeightBasis = "UNASSIGNED"
			v.Companies[i].WhyThisShare = "No share is assigned while the scope or allocation constraints need review."
		}
		for i := range v.Roles {
			v.Roles[i].PlannedMixPercent = nil
		}
		for i := range v.AvailableContracts {
			v.AvailableContracts[i].PlannedMixPercent = nil
		}
		return
	}
	if len(c.Context.SourceIDs) > 0 {
		v.Mix.ConstraintSourceIDs = uniqueSorted(c.Context.SourceIDs)
	}
	if c.CashPercent != nil {
		v.Mix.CashPercent = expressionPercent(*c.CashPercent)
		v.Mix.TotalBudgetKnown = true
		v.Mix.Status = "USER_CONSTRAINT"
	}
	if c.CashPercent != nil && *c.CashPercent == 100 {
		v.Mix.Status = "USER_CASH_ONLY"
		for i := range v.Companies {
			v.Companies[i].PlannedMixPercent = nil
			v.Companies[i].WeightBasis = "UNASSIGNED"
			v.Companies[i].WhyThisShare = "Your confirmed 100% cash instruction leaves no current exposure mix."
		}
		for i := range v.Roles {
			v.Roles[i].PlannedMixPercent = nil
		}
		for i := range v.AvailableContracts {
			v.AvailableContracts[i].PlannedMixPercent = nil
			v.AvailableContracts[i].TotalBudgetPercent = expressionPercent(0)
		}
		v.Mix.AvailablePercent = expressionPercent(0)
		v.Mix.UnassignedPercent = expressionPercent(0)
		return
	}
	sleeve := 1.0
	if c.CashPercent != nil {
		sleeve = 1 - *c.CashPercent/100
	}
	rolePlan := map[string]float64{}
	roleCount := map[string]int{}
	for _, r := range plan.Roles {
		rolePlan[r.ID] = float64(r.PlannedMixPercent)
	}
	for _, x := range v.Companies {
		roleCount[x.RoleID]++
	}
	if len(c.Candidate) > 0 || len(c.Role) > 0 || c.Context.Policy == "EQUAL_ROLE" {
		v.Mix.Status = "USER_CONSTRAINT"
		for i := range v.Companies {
			company := &v.Companies[i]
			percent := 0.0
			switch {
			case len(c.Candidate) > 0:
				percent = c.Candidate[company.ID] / sleeve * 100
			case c.Context.Policy == "EQUAL_ROLE":
				percent = 100 / float64(len(v.Roles)) / float64(roleCount[company.RoleID])
			default:
				percent = c.Role[company.RoleID] / sleeve * 100 * float64(plan.Companies[i].PlannedMixPercent) / rolePlan[company.RoleID]
			}
			company.WeightBasis = "USER_CONSTRAINT"
			company.WhyThisShare = "Your confirmed allocation instruction sets this share; the earlier illustrative weight reason no longer applies."
			if len(c.Role) > 0 {
				company.WeightBasis = "USER_ROLE_WITH_MODEL_SPLIT"
				company.WhyThisShare = "Your confirmed role budget sets this sleeve; the within-role company split remains illustrative."
			}
			company.PlannedMixPercent = expressionPercent(percent)
		}
		for i := range v.Roles {
			sum := 0.0
			for _, x := range v.Companies {
				if x.RoleID == v.Roles[i].ID {
					sum += *x.PlannedMixPercent
				}
			}
			v.Roles[i].PlannedMixPercent = expressionPercent(sum)
		}
	}
	updateExpressionProjection(v)
}
func updateExpressionProjection(v *ExpressionView) {
	if v.Mix.Status == "UNRESOLVED" || v.Mix.Status == "USER_CASH_ONLY" {
		return
	}
	byID := map[string]*float64{}
	for _, c := range v.Companies {
		byID[c.ID] = c.PlannedMixPercent
	}
	available := 0.0
	for i := range v.AvailableContracts {
		leg := &v.AvailableContracts[i]
		leg.PlannedMixPercent = byID[leg.CompanyID]
		if leg.PlannedMixPercent != nil {
			available += *leg.PlannedMixPercent
			if v.Mix.CashPercent != nil {
				leg.TotalBudgetPercent = expressionPercent(*leg.PlannedMixPercent * (1 - *v.Mix.CashPercent/100))
			}
		}
	}
	if math.Abs(available-100) < 1e-8 {
		available = 100
	}
	v.Mix.AvailablePercent = expressionPercent(available)
	v.Mix.UnassignedPercent = expressionPercent(100 - available)
}
func retainExpressionMappings(v *ExpressionView, snapshot takeintelligence.CatalogSnapshot) {
	kept := []ExpressionContract{}
	for _, leg := range v.AvailableContracts {
		if !hasError(takeintelligence.ValidateMappings(snapshot, []takeintelligence.UnderlyingMapping{leg.Mapping}, clock.Now())) {
			kept = append(kept, leg)
		} else {
			for i := range v.Companies {
				if v.Companies[i].ID == leg.CompanyID {
					v.Companies[i].Availability = "MAPPING_UNVERIFIED"
				}
			}
		}
	}
	v.AvailableContracts = kept
	updateExpressionProjection(v)
}

func expressionScopeName(idea ResearchIdea, name string, entries []secExchangeTicker) string {
	if idea.CompanyScope == nil {
		return name
	}
	entry, ok := matchNamedIssuer(entries, name)
	for _, allowed := range expressionRequestedNames(idea.CompanyScope.Scope) {
		if strings.EqualFold(strings.TrimSpace(allowed), strings.TrimSpace(name)) {
			return allowed
		}
		if target, found := matchNamedIssuer(entries, allowed); ok && found && canonicalCIK(entry.CIK) == canonicalCIK(target.CIK) {
			return allowed
		}
	}
	return name
}

func expressionMissingNames(idea ResearchIdea, p ExpressionPlan, entries []secExchangeTicker) []string {
	missing := []string{}
	if idea.CompanyScope == nil {
		return missing
	}
	seen := map[string]bool{}
	for _, c := range p.Companies {
		seen[strings.ToLower(strings.TrimSpace(expressionScopeName(idea, c.IssuerName, entries)))] = true
	}
	for _, name := range expressionRequestedNames(idea.CompanyScope.Scope) {
		if !seen[strings.ToLower(strings.TrimSpace(name))] {
			missing = append(missing, name)
		}
	}
	return missing
}

// Named universes and explicitly selected open-route anchors are both required
// coverage. Mentions elsewhere in the saved idea never enter this list.
func expressionRequestedNames(s CompanyScope) []string {
	if s.Kind == CompanyScopeNamed {
		return s.IssuerNames
	}
	return s.AnchorIssuerNames
}
func refreshExpressionStoryEmphasis(v *ExpressionView) {
	for _, c := range v.Companies {
		if c.WeightBasis == "USER_CONSTRAINT" || c.WeightBasis == "USER_ROLE_WITH_MODEL_SPLIT" {
			v.Story.WhyThisMix = "Your confirmed allocation sets the relative emphasis. The company connections remain suggestions to explore."
			for i := range v.Roles {
				v.Roles[i].WhyThisRole = "Your allocation sets this role's emphasis; its business connection remains a proposal."
			}
			return
		}
	}
	if v.Mix.Status == "UNRESOLVED" || v.Mix.Status == "USER_CASH_ONLY" {
		v.Story.WhyThisMix = "The company connections remain a proposed idea map. No current exposure mix is assigned while your constraints are unresolved or require cash only."
	}
}

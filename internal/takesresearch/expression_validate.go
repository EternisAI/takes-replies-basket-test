package takesresearch

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

// ValidateExpressionResult checks the actual public data at the durable worker
// boundary. A READY flag alone cannot bypass scope, mapping or mix validation.
func ValidateExpressionResult(raw json.RawMessage, buildID string, snapshot takeintelligence.CatalogSnapshot) error {
	var p ProposalView
	if json.Unmarshal(raw, &p) != nil || p.ConstructionMode != ExpressionMode || p.BuildID != buildID || p.Expression == nil || p.AsOf.IsZero() || p.CompletionSnapshotID != snapshot.ID {
		return errors.New("exploratory result identity is invalid")
	}
	if p.Assessment.Ready || p.Assessment.LiveEligible || p.Assessment.Complete || len(p.Holdings) > 0 || len(p.Evidence) > 0 || len(p.Playbook) > 0 {
		return errors.New("exploratory output claimed sourced portfolio readiness")
	}
	if hasError(takeintelligence.ValidateCatalog(snapshot, snapshot.CompletedAt)) {
		return errors.New("exploratory completion catalog failed integrity checks")
	}
	e := p.Expression
	if e.Kind != "EXPLORATORY_BLUEPRINT" || e.Basis != "MODEL_INFERENCE" || len(e.Roles) < 1 || len(e.Roles) > maxExpressionRoles || len(e.Companies) < 1 || len(e.Companies) > maxExpressionCompanies || len(e.AvailableContracts) > len(e.Companies) || len(e.OverlapRisks) > maxExpressionRoles {
		return errors.New("exploratory result counts or provenance are invalid")
	}
	if (e.Readiness == "READY_FOR_REVIEW" && p.Status != string(StatusReady)) || (e.Readiness == "NEEDS_REVIEW" && p.Status != string(StatusPartial)) || (e.Readiness != "READY_FOR_REVIEW" && e.Readiness != "NEEDS_REVIEW") {
		return errors.New("exploratory readiness/status mismatch")
	}
	if e.Scope != nil {
		if e.Scope.Origin != "MODEL_PROPOSED_USER_SELECTED" || e.Scope.SourceChoiceID == "" || e.Scope.Scope.Validate() != nil {
			return errors.New("exploratory scope lacks selected provenance")
		}
	}
	if (e.Scope == nil || e.Scope.NeedsConfirmation) && e.Readiness != "NEEDS_REVIEW" {
		return errors.New("unconfirmed scope claimed exploratory readiness")
	}
	if e.Story != nil {
		if e.Story.Version != expressionStoryVersion || e.Story.Basis != "MODEL_INFERENCE" {
			return errors.New("invalid expression story provenance")
		}
		if err := validateExpressionStory(e.Story.ExpressionStoryPlan); err != nil {
			return err
		}
		if e.Scope != nil && e.Scope.Scope.Kind == CompanyScopeNamed && e.Story.AlternativeOrGap != nil && e.Story.AlternativeOrGap.Kind != "MISSING_ANGLE" {
			return errors.New("exclusive expression scope acquired an outside alternative")
		}
	}
	roles := map[string]bool{}
	sums := map[string]float64{}
	companies := map[string]ExpressionCompany{}
	names := map[string]bool{}
	sized := e.Mix.Status != "UNRESOLVED" && e.Mix.Status != "USER_CASH_ONLY"
	if e.Mix.Status != "ILLUSTRATIVE_PROPOSAL" && e.Mix.Status != "USER_CONSTRAINT" && e.Mix.Status != "UNRESOLVED" && e.Mix.Status != "USER_CASH_ONLY" {
		return errors.New("unknown exploratory mix basis")
	}
	total, core := 0.0, 0.0
	discoveries, userWeights := 0, false
	for _, r := range e.Roles {
		if !validResearchQuestionID(r.ID) || roles[r.ID] || !expressionText(r.Label, 100) || !expressionText(r.ProposedMechanism, 320) || !expressionText(r.WhyThisRole, 240) || (r.ExposureKind != "DIRECT" && r.ExposureKind != "ADJACENT") || !expressionWeight(r.PlannedMixPercent, sized) {
			return errors.New("invalid exploratory role data")
		}
		if e.Story != nil && (!expressionText(r.Label, 60) || !expressionText(r.ProposedMechanism, 160) || !expressionText(r.WhyThisRole, 120)) {
			return errors.New("new expression role exceeds its editorial bounds")
		}
		roles[r.ID] = true
	}
	for _, c := range e.Companies {
		name := strings.ToLower(strings.TrimSpace(c.IssuerName))
		if !validResearchQuestionID(c.ID) || companies[c.ID].ID != "" || names[name] || !roles[c.RoleID] || !expressionText(c.IssuerName, 120) || !expressionText(c.ProposedConnection, 240) || !expressionText(c.WhyThisShare, 240) || c.Basis != "MODEL_INFERENCE" || c.SourceSupport != "NOT_RESEARCHED" || !expressionWeight(c.PlannedMixPercent, sized) {
			return errors.New("invalid exploratory company data")
		}
		if e.Story == nil && (c.PortfolioRole != "" || c.DistinctiveContribution != "" || c.ExtraAssumption != "") {
			return errors.New("expression company editorial data lacks its story contract")
		}
		if e.Story != nil {
			if !expressionText(c.ProposedConnection, 160) || !expressionText(c.WhyThisShare, 140) {
				return errors.New("new expression company exceeds its editorial bounds")
			}
			if err := validateExpressionContribution(c.PortfolioRole, c.DistinctiveContribution, c.ExtraAssumption); err != nil {
				return err
			}
			if c.PortfolioRole == "DISCOVERY" {
				discoveries++
			} else if sized {
				core += *c.PlannedMixPercent
			}
			userWeights = userWeights || c.WeightBasis == "USER_CONSTRAINT" || c.WeightBasis == "USER_ROLE_WITH_MODEL_SPLIT"
		}
		if e.Scope != nil && e.Scope.Scope.Kind == CompanyScopeNamed {
			found := false
			for _, n := range e.Scope.Scope.IssuerNames {
				found = found || strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(c.IssuerName))
			}
			if !found {
				return errors.New("company outside accepted named scope")
			}
		}
		switch c.WeightBasis {
		case "MODEL_INFERENCE", "USER_CONSTRAINT", "USER_ROLE_WITH_MODEL_SPLIT", "UNASSIGNED":
		default:
			return errors.New("invalid proposed weight provenance")
		}
		if (c.WeightBasis == "UNASSIGNED") != (!sized) {
			return errors.New("unassigned company acquired a weight rationale")
		}
		if c.Identity.Status != "UNRESOLVED" && c.Identity.Status != "RESOLVED" {
			return errors.New("invalid company identity state")
		}
		if c.Identity.Status == "RESOLVED" && (canonicalCIK(c.Identity.CIK) == "" || c.Identity.CIK != canonicalCIK(c.Identity.CIK) || c.Identity.PublicURL != secCompanyTickerURL || c.Identity.OfficialName == "" || c.Identity.RetrievedAt == nil || c.Identity.RetrievedAt.IsZero() || c.Identity.RetrievedAt.After(p.AsOf)) {
			return errors.New("company identity metadata is incomplete or future dated")
		}
		switch c.Availability {
		case "AVAILABLE", "NO_EXACT_INSTRUMENT", "IDENTITY_UNRESOLVED", "DUPLICATE_ISSUER", "USER_EXCLUDED", "PRODUCT_UNAVAILABLE", "MAPPING_UNVERIFIED":
		default:
			return errors.New("invalid exploratory availability")
		}
		if c.Identity.Status != "RESOLVED" && c.Availability != "IDENTITY_UNRESOLVED" && c.Availability != "USER_EXCLUDED" {
			return errors.New("unresolved identity acquired a contract state")
		}
		if sized {
			total += *c.PlannedMixPercent
			sums[c.RoleID] += *c.PlannedMixPercent
		}
		companies[c.ID] = c
		names[name] = true
	}
	if e.Scope != nil {
		requested := map[string]bool{}
		for _, name := range expressionRequestedNames(e.Scope.Scope) {
			requested[strings.ToLower(strings.TrimSpace(name))] = true
		}
		missing := map[string]bool{}
		for _, name := range e.MissingCompanies {
			key := strings.ToLower(strings.TrimSpace(name))
			if missing[key] || names[key] || !requested[key] {
				return errors.New("invalid selected-scope omission")
			}
			missing[key] = true
		}
		for _, name := range expressionRequestedNames(e.Scope.Scope) {
			key := strings.ToLower(strings.TrimSpace(name))
			if !names[key] && !missing[key] {
				return errors.New("selected company or anchor silently omitted")
			}
			if e.Story != nil && e.Scope.Scope.Kind == CompanyScopeOpen && names[key] {
				for _, company := range e.Companies {
					if strings.EqualFold(strings.TrimSpace(company.IssuerName), key) && company.PortfolioRole != "CORE" {
						return errors.New("selected anchor lost its core role")
					}
				}
			}
		}
		if len(e.MissingCompanies) > 0 && (e.Mix.Status != "UNRESOLVED" || e.Readiness != "NEEDS_REVIEW") {
			return errors.New("missing selected scope acquired a concentrated mix")
		}
	}
	for _, c := range e.Companies {
		if (c.Availability == "USER_EXCLUDED" || c.Availability == "DUPLICATE_ISSUER") && (e.Mix.Status != "UNRESOLVED" || e.Readiness != "NEEDS_REVIEW") {
			return errors.New("excluded suggestion acquired an accepted mix")
		}
	}
	if e.Story != nil && discoveries > maxExpressionDiscoveries {
		return errors.New("public expression discovery count exceeds three")
	}
	if e.Story != nil && sized && discoveries > 0 && !userWeights && core <= 50 {
		return errors.New("illustrative public expression lost its core majority")
	}
	if userWeights && (e.Mix.Status != "USER_CONSTRAINT" || len(e.Mix.ConstraintSourceIDs) == 0) {
		return errors.New("user emphasis lacks allocation provenance")
	}
	if sized && math.Abs(total-100) > 1e-7 {
		return errors.New("exploratory planned weights do not sum to 100")
	}
	for _, r := range e.Roles {
		if sized && math.Abs(sums[r.ID]-*r.PlannedMixPercent) > 1e-7 {
			return errors.New("exploratory role/company mix mismatch")
		}
	}
	if e.Mix.Denominator != expressionDenominator || e.Mix.TotalBudgetKnown != (e.Mix.CashPercent != nil) {
		return errors.New("exploratory denominator or cash provenance is invalid")
	}
	if e.Mix.CashPercent != nil && !finiteCashPercent(*e.Mix.CashPercent) {
		return errors.New("invalid explicit cash percentage")
	}
	if e.Mix.Status != "ILLUSTRATIVE_PROPOSAL" && e.Mix.Status != "UNRESOLVED" && len(e.Mix.ConstraintSourceIDs) == 0 {
		return errors.New("user mix lacks instruction provenance")
	}
	if e.Mix.Status == "ILLUSTRATIVE_PROPOSAL" && e.Mix.CashPercent != nil {
		return errors.New("inferred cash is not permitted")
	}
	if e.HorizonDays != nil && (*e.HorizonDays < 1 || *e.HorizonDays > 3650) {
		return errors.New("invalid user horizon")
	}
	keys := map[string]bool{}
	seen := map[string]bool{}
	exposures := map[string]bool{}
	available := 0.0
	for _, leg := range e.AvailableContracts {
		c := companies[leg.CompanyID]
		m := leg.Mapping
		if c.ID == "" || seen[c.ID] || c.Availability != "AVAILABLE" || c.Identity.Status != "RESOLVED" || leg.Key != m.Key || keys[leg.Key.String()] || exposures[m.ExposureKey] || canonicalCIK(m.IssuerCIK) != c.Identity.CIK || m.EntityName != c.Identity.OfficialName || leg.AccountAccess != "UNKNOWN" || !sameExpressionWeight(leg.PlannedMixPercent, c.PlannedMixPercent) {
			return errors.New("exploratory contract lost its exact company binding")
		}
		if hasError(takeintelligence.ValidateMappings(snapshot, []takeintelligence.UnderlyingMapping{m}, p.AsOf)) {
			return errors.New("exploratory contract mapping failed catalog validation")
		}
		if sized {
			available += *leg.PlannedMixPercent
		}
		switch {
		case e.Mix.TotalBudgetKnown && sized:
			if leg.TotalBudgetPercent == nil || math.Abs(*leg.TotalBudgetPercent-*leg.PlannedMixPercent*(1-*e.Mix.CashPercent/100)) > 1e-7 {
				return errors.New("total-budget projection differs from confirmed cash")
			}
		case e.Mix.Status == "USER_CASH_ONLY":
			if leg.TotalBudgetPercent == nil || *leg.TotalBudgetPercent != 0 {
				return errors.New("cash-only instruction acquired a positive contract weight")
			}
		case leg.TotalBudgetPercent != nil:
			return errors.New("unknown capital budget acquired a total-budget weight")
		}
		seen[c.ID] = true
		keys[leg.Key.String()] = true
		exposures[m.ExposureKey] = true
	}
	for _, c := range e.Companies {
		if c.Availability == "AVAILABLE" && !seen[c.ID] {
			return errors.New("available company has no exact contract")
		}
	}
	switch e.Mix.Status {
	case "UNRESOLVED":
		if e.Mix.AvailablePercent != nil || e.Mix.UnassignedPercent != nil || e.Mix.CashPercent != nil || e.Readiness != "NEEDS_REVIEW" {
			return errors.New("unresolved sizing acquired invented weights")
		}
	case "USER_CASH_ONLY":
		if e.Mix.CashPercent == nil || *e.Mix.CashPercent != 100 || e.Mix.AvailablePercent == nil || *e.Mix.AvailablePercent != 0 || e.Mix.UnassignedPercent == nil || *e.Mix.UnassignedPercent != 0 {
			return errors.New("cash-only state lacks user 100-percent constraint")
		}
	default:
		if !expressionWeight(e.Mix.AvailablePercent, true) || !expressionWeight(e.Mix.UnassignedPercent, true) || math.Abs(*e.Mix.AvailablePercent-available) > 1e-7 || math.Abs(available+*e.Mix.UnassignedPercent-100) > 1e-7 {
			return errors.New("available projection silently changed the planned mix")
		}
	}
	for _, risk := range e.OverlapRisks {
		if len(risk.RoleIDs) < 1 || len(risk.RoleIDs) > maxExpressionRoles || !expressionText(risk.ProposedDependency, 320) {
			return errors.New("invalid proposed overlap")
		}
		seenRoles := map[string]bool{}
		for _, id := range risk.RoleIDs {
			if !roles[id] || seenRoles[id] {
				return errors.New("proposed overlap has invalid role references")
			}
			seenRoles[id] = true
		}
	}
	return nil
}
func expressionWeight(v *float64, required bool) bool {
	if !required {
		return v == nil
	}
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0 && *v <= 100
}
func sameExpressionWeight(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return math.Abs(*a-*b) < 1e-7
}

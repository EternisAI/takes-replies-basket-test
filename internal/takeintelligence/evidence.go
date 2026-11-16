package takeintelligence

import (
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

type EvidenceRole string

const (
	RoleCurrentPrice         EvidenceRole = "current_price"
	RoleValuationDenominator EvidenceRole = "valuation_denominator"
	RoleThesisSupport        EvidenceRole = "thesis_support"
	// RoleReportedObservation is a dated subject fact that can inform synthesis
	// but does not, by itself, establish a forward investment thesis.
	RoleReportedObservation EvidenceRole = "reported_observation"
)

// EvidenceSubject is a stable scope key for one factual claim. The kind is
// explicit so macro/group evidence cannot silently stand in for an issuer.
type EvidenceSubjectKind string

const (
	SubjectCompany       EvidenceSubjectKind = "company"
	SubjectSecurity      EvidenceSubjectKind = "security"
	SubjectFund          EvidenceSubjectKind = "fund"
	SubjectCommodity     EvidenceSubjectKind = "commodity"
	SubjectProtocol      EvidenceSubjectKind = "protocol"
	SubjectDAO           EvidenceSubjectKind = "dao"
	SubjectNetwork       EvidenceSubjectKind = "network"
	SubjectModelVendor   EvidenceSubjectKind = "model_vendor"
	SubjectIndex         EvidenceSubjectKind = "index"
	SubjectFX            EvidenceSubjectKind = "fx"
	SubjectCrypto        EvidenceSubjectKind = "crypto"
	SubjectOther         EvidenceSubjectKind = "other"
	SubjectIndustryGroup EvidenceSubjectKind = "industry_group"
	SubjectMacro         EvidenceSubjectKind = "macro"
	SubjectInstrument    EvidenceSubjectKind = "instrument"
)

type EvidenceSubject struct {
	Kind EvidenceSubjectKind
	Key  string
}

func (subject EvidenceSubject) String() string {
	return string(subject.Kind) + ":" + strings.TrimSpace(subject.Key)
}

func (subject EvidenceSubject) valid() bool {
	return knownEvidenceSubjectKind(subject.Kind) && strings.TrimSpace(subject.Key) != ""
}

func knownEvidenceSubjectKind(kind EvidenceSubjectKind) bool {
	switch kind {
	case SubjectCompany, SubjectSecurity, SubjectFund, SubjectCommodity, SubjectProtocol, SubjectDAO, SubjectNetwork, SubjectModelVendor, SubjectIndex, SubjectFX, SubjectCrypto, SubjectOther, SubjectIndustryGroup, SubjectMacro, SubjectInstrument:
		return true
	default:
		return false
	}
}

// SubjectForMapping derives the exact evidence scope from a reviewed mapping.
// It never guesses identity from an instrument symbol.
func SubjectForMapping(mapping UnderlyingMapping) EvidenceSubject {
	if mapping.Status != MappingReviewed || strings.TrimSpace(mapping.EntityKey) == "" {
		return EvidenceSubject{}
	}
	return EvidenceSubject{Kind: EvidenceSubjectKind(mapping.EntityKind), Key: mapping.EntityKey}
}

type EvidenceRecord struct {
	ID                string
	Roles             []EvidenceRole
	Subjects          []EvidenceSubject
	Class             SourceClass
	Visibility        SourceVisibility
	DisplayPermission DisplayPermission
	Provider          string
	SourceID          string
	URL               string
	Form              string
	PeriodStart       time.Time
	PeriodEnd         time.Time
	PublishedAt       time.Time
	ObservedAt        time.Time
	RetrievedAt       time.Time
	Section           string
	FactTag           string
	Unit              string
	// Value is the exact source value for structured observations. It is kept
	// separate from claim prose so playbook checks can bind an observed value
	// to the precise issuer, fact tag, unit, and period.
	Value              string
	ContentFingerprint string
	FixtureOnly        bool
}

type RoleRequirement struct {
	Role           EvidenceRole
	Target         *EvidenceSubject
	Required       bool
	MaximumAge     time.Duration
	RequirePublic  bool
	RequirePrimary bool
}

type ClaimKind string

const (
	ClaimFact           ClaimKind = "fact"
	ClaimUndervaluation ClaimKind = "undervaluation"
)

type ValuationBasis struct {
	Method                         string
	Formula                        string
	CurrentPriceEvidenceID         string
	FinancialDenominatorEvidenceID string
}

type EvidenceClaim struct {
	ID             string
	Role           EvidenceRole
	Target         EvidenceSubject
	Kind           ClaimKind
	Statement      string
	Quote          string
	Required       bool
	EvidenceIDs    []string
	RequiresUnit   bool
	ValuationBasis *ValuationBasis
}

type CoverageInput struct {
	AsOf           time.Time
	RequestedRoles []RoleRequirement
	Claims         []EvidenceClaim
	Evidence       []EvidenceRecord
}

type ClaimCoverage struct {
	ClaimID     string
	Role        EvidenceRole
	Target      EvidenceSubject
	Supported   bool
	EvidenceIDs []string
}

type RoleCoverage struct {
	Role            EvidenceRole
	Target          *EvidenceSubject
	Required        bool
	Supported       bool
	SupportedClaims int
	EvidenceIDs     []string
}

type CoverageResult struct {
	Complete        bool
	LiveEligible    bool
	FixtureOnlyUsed bool
	Roles           []RoleCoverage
	Claims          []ClaimCoverage
	Findings        []Finding
}

// EvaluateCoverage checks dated evidence against explicit requested roles and
// claim links. Fixture-only references can exercise the rules but never make
// the result live-eligible.
func EvaluateCoverage(input CoverageInput) CoverageResult {
	result := CoverageResult{Complete: true, LiveEligible: true}
	var findings []Finding
	if input.AsOf.IsZero() {
		findings = append(findings, Finding{Code: "coverage_as_of_missing", Severity: SeverityError, Message: "an as-of time is required to evaluate evidence"})
		result.Complete, result.LiveEligible = false, false
	}
	roleReqs := make(map[string]RoleRequirement, len(input.RequestedRoles))
	for _, req := range input.RequestedRoles {
		if req.Role == "" {
			findings = append(findings, Finding{Code: "evidence_role_missing", Severity: SeverityError, Message: "requested evidence roles need stable names"})
			continue
		}
		if req.MaximumAge < 0 {
			findings = append(findings, Finding{Code: "evidence_age_limit_invalid", Subject: string(req.Role), Severity: SeverityError, Message: "maximum evidence age cannot be negative"})
		}
		if req.Target != nil && !req.Target.valid() {
			findings = append(findings, Finding{Code: "evidence_role_target_invalid", Subject: string(req.Role), Severity: SeverityError, Message: "a scoped evidence role needs a valid subject kind and key"})
		}
		key := evidenceRoleKey(req.Role, req.Target)
		if _, ok := roleReqs[key]; ok {
			findings = append(findings, Finding{Code: "duplicate_evidence_role", Subject: key, Severity: SeverityError, Message: "requested evidence role and subject appear more than once"})
			continue
		}
		roleReqs[key] = req
	}
	evidence := make(map[string]EvidenceRecord, len(input.Evidence))
	for _, record := range input.Evidence {
		if strings.TrimSpace(record.ID) == "" {
			findings = append(findings, Finding{Code: "evidence_id_missing", Severity: SeverityError, Message: "evidence needs an internal immutable ID"})
			continue
		}
		if _, ok := evidence[record.ID]; ok {
			findings = append(findings, Finding{Code: "duplicate_evidence_id", Subject: record.ID, Severity: SeverityError, Message: "evidence ID appears more than once"})
			continue
		}
		evidence[record.ID] = record
	}

	claimIDs := make(map[string]bool, len(input.Claims))
	claimCoverage := make(map[string]ClaimCoverage, len(input.Claims))
	for _, claim := range input.Claims {
		subject := claim.ID
		if strings.TrimSpace(claim.ID) == "" || claim.Role == "" {
			findings = append(findings, Finding{Code: "claim_identity_missing", Subject: subject, Severity: SeverityError, Message: "evidence claim needs an ID and requested role"})
		}
		if claimIDs[claim.ID] {
			findings = append(findings, Finding{Code: "duplicate_claim_id", Subject: subject, Severity: SeverityError, Message: "claim ID appears more than once"})
		}
		claimIDs[claim.ID] = true
		if !claim.Target.valid() {
			findings = append(findings, Finding{Code: "claim_target_missing", Subject: subject, Severity: SeverityError, Message: "every factual claim needs an explicit subject kind and stable subject key"})
		}
		if claim.Required && strings.TrimSpace(claim.Statement) == "" {
			findings = append(findings, Finding{Code: "claim_statement_missing", Subject: subject, Severity: SeverityError, Message: "a required source claim needs a concise factual statement"})
		}
		if claim.Required && strings.TrimSpace(claim.Quote) == "" {
			findings = append(findings, Finding{Code: "claim_quote_missing", Subject: subject, Severity: SeverityError, Message: "a required source claim needs a source excerpt or direct provider value"})
		}
		req, requested := evidenceRoleRequirement(roleReqs, claim.Role, claim.Target)
		if !requested {
			findings = append(findings, Finding{Code: "claim_role_not_requested", Subject: subject, Severity: SeverityWarning, Message: "claim role and subject are not part of the requested evidence coverage"})
		}
		coverage := ClaimCoverage{ClaimID: claim.ID, Role: claim.Role, Target: claim.Target}
		if claim.Kind == ClaimUndervaluation {
			valid, used, issues := evaluateValuationBasis(claim, evidence, roleReqs, input.AsOf)
			findings = append(findings, issues...)
			coverage.Supported = valid && requested && claim.Target.valid()
			if coverage.Supported {
				coverage.EvidenceIDs = append(coverage.EvidenceIDs, used...)
				for _, id := range used {
					if record, ok := evidence[id]; ok {
						if record.FixtureOnly {
							result.FixtureOnlyUsed = true
							result.LiveEligible = false
						}
					}
				}
			}
		} else {
			if claim.Kind != ClaimFact {
				findings = append(findings, Finding{Code: "claim_kind_invalid", Subject: subject, Severity: SeverityError, Message: "claim kind is not supported"})
			}
			// An unrequested or unscoped claim is not evidence, even if a source
			// happens to carry the same role label.
			if requested && claim.Target.valid() {
				for _, id := range claim.EvidenceIDs {
					record, ok := evidence[id]
					if !ok {
						findings = append(findings, Finding{Code: "claim_evidence_missing", Subject: subject, Severity: SeverityWarning, Message: "a linked evidence ID was not found"})
						continue
					}
					valid, issues := evidenceSupports(record, claim.Role, claim.Target, req, claim.RequiresUnit, input.AsOf)
					findings = append(findings, issues...)
					if valid {
						coverage.Supported = true
						coverage.EvidenceIDs = append(coverage.EvidenceIDs, id)
						if record.FixtureOnly {
							result.FixtureOnlyUsed = true
							result.LiveEligible = false
						}
					}
				}
			}
		}
		coverage.EvidenceIDs = sortedUnique(coverage.EvidenceIDs)
		if claim.Required && !coverage.Supported {
			findings = append(findings, Finding{Code: "claim_unsupported", Subject: subject, Severity: SeverityError, Message: "required claim has no eligible, dated evidence"})
			result.Complete, result.LiveEligible = false, false
		} else if !coverage.Supported && claim.ID != "" {
			findings = append(findings, Finding{Code: "claim_unsupported_optional", Subject: subject, Severity: SeverityWarning, Message: "optional claim has no eligible, dated evidence and must not be presented as supported"})
		}
		claimCoverage[claim.ID] = coverage
	}

	for _, req := range input.RequestedRoles {
		coveredClaims := 0
		ids := make([]string, 0)
		for _, claim := range input.Claims {
			if claim.Role != req.Role || (req.Target != nil && claim.Target != *req.Target) {
				continue
			}
			coverage, ok := claimCoverage[claim.ID]
			if ok && coverage.Supported {
				coveredClaims++
				ids = append(ids, coverage.EvidenceIDs...)
			}
		}
		supported := coveredClaims > 0
		result.Roles = append(result.Roles, RoleCoverage{Role: req.Role, Target: req.Target, Required: req.Required, Supported: supported, SupportedClaims: coveredClaims, EvidenceIDs: sortedUnique(ids)})
		if req.Required && !supported {
			findings = append(findings, Finding{Code: "requested_role_uncovered", Subject: evidenceRoleKey(req.Role, req.Target), Severity: SeverityError, Message: "no supported claim covers this requested role and subject"})
			result.Complete, result.LiveEligible = false, false
		}
	}
	for _, claim := range input.Claims {
		coverage := claimCoverage[claim.ID]
		result.Claims = append(result.Claims, coverage)
	}
	sort.Slice(result.Roles, func(i, j int) bool {
		return evidenceRoleKey(result.Roles[i].Role, result.Roles[i].Target) < evidenceRoleKey(result.Roles[j].Role, result.Roles[j].Target)
	})
	sort.Slice(result.Claims, func(i, j int) bool { return result.Claims[i].ClaimID < result.Claims[j].ClaimID })
	result.Findings = findingsSorted(findings)
	if hasErrors(result.Findings) {
		result.Complete = false
	}
	if !result.Complete || result.FixtureOnlyUsed {
		result.LiveEligible = false
	}
	return result
}

func evidenceRoleKey(role EvidenceRole, target *EvidenceSubject) string {
	if target == nil {
		return string(role)
	}
	return string(role) + "@" + target.String()
}

func evidenceRoleRequirement(requirements map[string]RoleRequirement, role EvidenceRole, target EvidenceSubject) (RoleRequirement, bool) {
	if scoped, ok := requirements[evidenceRoleKey(role, &target)]; ok {
		return scoped, true
	}
	global, ok := requirements[evidenceRoleKey(role, nil)]
	return global, ok
}

func evidenceSupports(record EvidenceRecord, role EvidenceRole, target EvidenceSubject, req RoleRequirement, requireUnit bool, asOf time.Time) (bool, []Finding) {
	subject := record.ID
	var findings []Finding
	valid := true
	if !target.valid() {
		findings = append(findings, Finding{Code: "claim_target_missing", Subject: subject, Severity: SeverityWarning, Message: "evidence claim has no valid subject target"})
		valid = false
	} else if !slices.Contains(record.Subjects, target) {
		findings = append(findings, Finding{Code: "evidence_subject_mismatch", Subject: subject, Severity: SeverityWarning, Message: "source does not explicitly cover the claim subject"})
		valid = false
	}
	if !slices.Contains(record.Roles, role) {
		findings = append(findings, Finding{Code: "evidence_role_mismatch", Subject: subject, Severity: SeverityWarning, Message: "source is not tagged for the claim's evidence role"})
		valid = false
	}
	if role == RoleCurrentPrice && record.ObservedAt.IsZero() {
		findings = append(findings, Finding{Code: "current_price_observation_missing", Subject: subject, Severity: SeverityWarning, Message: "current price evidence needs the market observation time"})
		valid = false
	}
	if record.Class == "" || strings.TrimSpace(record.Provider) == "" || strings.TrimSpace(record.SourceID) == "" {
		findings = append(findings, Finding{Code: "evidence_provenance_missing", Subject: subject, Severity: SeverityWarning, Message: "source class, provider, and immutable source ID are required"})
		valid = false
	}
	if !knownSourceClass(record.Class) {
		findings = append(findings, Finding{Code: "evidence_source_class_invalid", Subject: subject, Severity: SeverityWarning, Message: "evidence source class is not supported"})
		valid = false
	}
	if record.Visibility != VisibilityPublic && record.Visibility != VisibilityLicensedPrivate && record.Visibility != VisibilityInternal {
		findings = append(findings, Finding{Code: "evidence_visibility_invalid", Subject: subject, Severity: SeverityWarning, Message: "evidence visibility must be explicit"})
		valid = false
	}
	if record.DisplayPermission != DisplayPermissionAllowed && record.DisplayPermission != DisplayPermissionProhibited && record.DisplayPermission != DisplayPermissionUnknown {
		findings = append(findings, Finding{Code: "evidence_display_permission_invalid", Subject: subject, Severity: SeverityWarning, Message: "evidence display permission must be explicit"})
		valid = false
	}
	if record.RetrievedAt.IsZero() || (!asOf.IsZero() && record.RetrievedAt.After(asOf)) {
		findings = append(findings, Finding{Code: "evidence_retrieval_time_invalid", Subject: subject, Severity: SeverityWarning, Message: "evidence retrieval time is missing or later than the proposal"})
		valid = false
	}
	anchor := evidenceFreshnessAnchor(record)
	switch {
	case record.PublishedAt.IsZero() && record.ObservedAt.IsZero():
		findings = append(findings, Finding{Code: "evidence_source_date_missing", Subject: subject, Severity: SeverityWarning, Message: "evidence needs a source publication or observation date"})
		valid = false
	case !asOf.IsZero() && (record.PublishedAt.After(asOf) || record.ObservedAt.After(asOf) || anchor.After(asOf)):
		findings = append(findings, Finding{Code: "evidence_source_date_in_future", Subject: subject, Severity: SeverityWarning, Message: "evidence source date is later than the proposal"})
		valid = false
	case req.MaximumAge > 0 && !asOf.IsZero() && asOf.Sub(anchor) > req.MaximumAge:
		findings = append(findings, Finding{Code: "evidence_stale", Subject: subject, Severity: SeverityWarning, Message: "source is older than the requested role's freshness limit"})
		valid = false
	}
	if !record.PeriodEnd.IsZero() && !record.PublishedAt.IsZero() && record.PeriodEnd.After(record.PublishedAt) {
		findings = append(findings, Finding{Code: "evidence_period_invalid", Subject: subject, Severity: SeverityWarning, Message: "report period ends after its publication date"})
		valid = false
	}
	if requireUnit && strings.TrimSpace(record.Unit) == "" {
		findings = append(findings, Finding{Code: "evidence_unit_missing", Subject: subject, Severity: SeverityWarning, Message: "this claim needs a stated unit"})
		valid = false
	}
	if !record.RequireFingerprint() {
		findings = append(findings, Finding{Code: "evidence_fingerprint_missing", Subject: subject, Severity: SeverityWarning, Message: "a content fingerprint is required for dated source evidence"})
		valid = false
	}
	if req.RequirePublic {
		if record.Visibility != VisibilityPublic {
			findings = append(findings, Finding{Code: "evidence_not_public", Subject: subject, Severity: SeverityWarning, Message: "private or licensed source cannot satisfy this public evidence role"})
			valid = false
		}
		if record.DisplayPermission != DisplayPermissionAllowed {
			findings = append(findings, Finding{Code: "evidence_display_permission_unknown", Subject: subject, Severity: SeverityWarning, Message: "public citation display rights are not confirmed"})
			valid = false
		}
		if !validHTTPURL(record.URL) {
			findings = append(findings, Finding{Code: "evidence_public_url_missing", Subject: subject, Severity: SeverityWarning, Message: "public role needs an official or otherwise approved source URL"})
			valid = false
		}
	}
	if req.RequirePrimary && record.Class != SourceOfficialPrimary && record.Class != SourceVenuePrimary {
		findings = append(findings, Finding{Code: "evidence_not_primary", Subject: subject, Severity: SeverityWarning, Message: "this role requires a primary source"})
		valid = false
	}
	return valid, findings
}

func (record EvidenceRecord) RequireFingerprint() bool {
	return strings.TrimSpace(record.ContentFingerprint) != ""
}

func knownSourceClass(class SourceClass) bool {
	switch class {
	case SourceOfficialPrimary, SourceVenuePrimary, SourcePublicSecondary, SourceMarketData, SourceLicensedVendor, SourcePrivateWiki:
		return true
	default:
		return false
	}
}

func evidenceAnchor(record EvidenceRecord) time.Time {
	if record.Class == SourceVenuePrimary || record.Class == SourceMarketData {
		if !record.ObservedAt.IsZero() {
			return record.ObservedAt
		}
	}
	if !record.PublishedAt.IsZero() {
		return record.PublishedAt
	}
	return record.ObservedAt
}

func evidenceFreshnessAnchor(record EvidenceRecord) time.Time {
	if record.Class == SourceVenuePrimary || record.Class == SourceMarketData {
		if !record.ObservedAt.IsZero() {
			return record.ObservedAt
		}
	}
	if !record.PeriodEnd.IsZero() {
		return record.PeriodEnd
	}
	return evidenceAnchor(record)
}

func evaluateValuationBasis(claim EvidenceClaim, records map[string]EvidenceRecord, roles map[string]RoleRequirement, asOf time.Time) (bool, []string, []Finding) {
	var findings []Finding
	basis := claim.ValuationBasis
	if basis == nil || strings.TrimSpace(basis.Method) == "" || strings.TrimSpace(basis.Formula) == "" || basis.CurrentPriceEvidenceID == "" || basis.FinancialDenominatorEvidenceID == "" {
		return false, nil, []Finding{{Code: "valuation_basis_missing", Subject: claim.ID, Severity: SeverityWarning, Message: "an undervaluation claim needs a named method, formula, current price, and dated financial denominator"}}
	}
	price, priceOK := records[basis.CurrentPriceEvidenceID]
	denominator, denominatorOK := records[basis.FinancialDenominatorEvidenceID]
	if !slices.Contains(claim.EvidenceIDs, basis.CurrentPriceEvidenceID) || !slices.Contains(claim.EvidenceIDs, basis.FinancialDenominatorEvidenceID) {
		findings = append(findings, Finding{Code: "valuation_evidence_unlinked", Subject: claim.ID, Severity: SeverityWarning, Message: "price and denominator evidence must both be linked to the claim"})
		return false, nil, findings
	}
	priceReq, priceRoleOK := evidenceRoleRequirement(roles, RoleCurrentPrice, claim.Target)
	denomReq, denomRoleOK := evidenceRoleRequirement(roles, RoleValuationDenominator, claim.Target)
	if !priceOK || !priceRoleOK {
		findings = append(findings, Finding{Code: "valuation_current_price_missing", Subject: claim.ID, Severity: SeverityWarning, Message: "a current-price source and requested role are required"})
		return false, nil, findings
	}
	if !denominatorOK || !denomRoleOK {
		findings = append(findings, Finding{Code: "valuation_denominator_missing", Subject: claim.ID, Severity: SeverityWarning, Message: "a dated financial-denominator source and requested role are required"})
		return false, nil, findings
	}
	priceEligible, priceFindings := evidenceSupports(price, RoleCurrentPrice, claim.Target, priceReq, true, asOf)
	denominatorEligible, denominatorFindings := evidenceSupports(denominator, RoleValuationDenominator, claim.Target, denomReq, true, asOf)
	findings = append(findings, priceFindings...)
	findings = append(findings, denominatorFindings...)
	if price.ObservedAt.IsZero() {
		findings = append(findings, Finding{Code: "valuation_price_observation_missing", Subject: claim.ID, Severity: SeverityWarning, Message: "current price must have an observation timestamp"})
		priceEligible = false
	}
	if denominator.PeriodEnd.IsZero() || denominator.PublishedAt.IsZero() {
		findings = append(findings, Finding{Code: "valuation_denominator_date_missing", Subject: claim.ID, Severity: SeverityWarning, Message: "financial denominator needs both a report period end and publication date"})
		denominatorEligible = false
	}
	return priceEligible && denominatorEligible, []string{basis.CurrentPriceEvidenceID, basis.FinancialDenominatorEvidenceID}, findings
}

// Citation is the public projection of an evidence reference. It intentionally
// omits internal IDs and any private source metadata or source text.
type Citation struct {
	URL         string
	Date        time.Time
	Form        string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Section     string
	FactTag     string
	Unit        string
}

// PublicCitation returns a display-safe citation only when visibility and
// source terms permit it. Fixture references are never returned for users.
func PublicCitation(record EvidenceRecord) (Citation, bool) {
	if record.FixtureOnly || !knownSourceClass(record.Class) || record.Class == SourcePrivateWiki || record.Visibility != VisibilityPublic || record.DisplayPermission != DisplayPermissionAllowed || !validHTTPURL(record.URL) {
		return Citation{}, false
	}
	date := evidenceAnchor(record)
	if date.IsZero() {
		return Citation{}, false
	}
	return Citation{URL: record.URL, Date: date, Form: record.Form, PeriodStart: record.PeriodStart, PeriodEnd: record.PeriodEnd, Section: record.Section, FactTag: record.FactTag, Unit: record.Unit}, true
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func sortedUnique(values []string) []string {
	slices.Sort(values)
	return slices.Compact(values)
}

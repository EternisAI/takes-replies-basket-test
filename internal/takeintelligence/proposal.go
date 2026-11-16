package takeintelligence

import (
	"slices"
	"time"
)

type PortfolioShape string

const (
	ShapeUnresolved      PortfolioShape = "unresolved"
	ShapeSingleCandidate PortfolioShape = "single_candidate"
	ShapeBasket          PortfolioShape = "basket"
)

type SelectedCandidate struct {
	Key                   ProviderInstrumentKey
	Mapping               *UnderlyingMapping
	Role                  string
	Direction             PositionDirection
	UserWeight            *float64
	ResearchWeightPercent *int
	RequiredClaimIDs      []string
}

type ProposalInput struct {
	CompletedAt     time.Time
	Snapshot        CatalogSnapshot
	Selected        []SelectedCandidate
	RequestedRoles  []RoleRequirement
	Claims          []EvidenceClaim
	Evidence        []EvidenceRecord
	RequestedBasket bool
	Allocation      *AllocationInput
	Playbook        *Playbook
}

type ProposalAssessment struct {
	Complete             bool
	LiveEligible         bool
	Ready                bool
	Shape                PortfolioShape
	CandidateAssessments []CandidateAssessment
	Evidence             CoverageResult
	Allocation           *AllocationResult
	Playbook             *PlaybookAssessment
	Findings             []Finding
}

// EvaluateProposal combines the pure catalog, identity, evidence, allocation,
// and playbook gates at one completion time. Completeness follows the requested
// roles and portfolio shape; fixtures may pass these rules but never become live-eligible.
func EvaluateProposal(input ProposalInput) ProposalAssessment {
	result := ProposalAssessment{Shape: ShapeUnresolved}
	var findings []Finding
	if input.CompletedAt.IsZero() {
		findings = append(findings, Finding{Code: "proposal_completion_time_missing", Severity: SeverityError, Message: "proposal completion time is required for all freshness checks"})
	}
	findings = append(findings, ValidateCatalog(input.Snapshot, input.CompletedAt)...)
	if len(input.Selected) == 0 {
		findings = append(findings, Finding{Code: "proposal_candidates_missing", Severity: SeverityError, Message: "proposal needs at least one selected, supported instrument"})
	}
	if len(input.RequestedRoles) == 0 || !slices.ContainsFunc(input.RequestedRoles, func(role RoleRequirement) bool { return role.Required }) {
		findings = append(findings, Finding{Code: "proposal_evidence_scope_missing", Severity: SeverityError, Message: "proposal needs at least one required evidence role tied to the user's request"})
	}

	selectedByKey := make(map[string]SelectedCandidate, len(input.Selected))
	exposureKeys := make(map[string]bool, len(input.Selected))
	for _, selected := range input.Selected {
		key := selected.Key.String()
		if _, exists := selectedByKey[key]; exists {
			findings = append(findings, Finding{Code: "proposal_candidate_duplicate", Subject: key, Severity: SeverityError, Message: "a selected instrument appears more than once"})
		}
		selectedByKey[key] = selected
		assessment := AssessCandidate(input.Snapshot, selected.Key, selected.Mapping, input.CompletedAt)
		result.CandidateAssessments = append(result.CandidateAssessments, assessment)
		findings = append(findings, assessment.Findings...)
		if assessment.Eligible && selected.Mapping != nil && selected.Mapping.Status == MappingReviewed {
			exposureKeys[selected.Mapping.ExposureKey] = true
		}
		if selected.Mapping != nil && selected.Mapping.Status == MappingReviewed && selected.Mapping.ExposureKey != "" {
			for _, previous := range input.Selected {
				if previous.Key == selected.Key {
					continue
				}
				if previous.Mapping != nil && previous.Mapping.Status == MappingReviewed && previous.Mapping.ExposureKey == selected.Mapping.ExposureKey {
					code, message := "proposal_duplicate_exposure", "selected instruments resolve to the same economic exposure and cannot count as diversification"
					if previous.Direction != selected.Direction {
						code, message = "proposal_conflicting_exposure", "opposite-side instruments resolve to the same economic exposure and must be resolved"
					}
					findings = append(findings, Finding{Code: code, Subject: selected.Mapping.ExposureKey, Severity: SeverityError, Message: message})
					break
				}
			}
		}
	}
	if len(exposureKeys) == 1 {
		result.Shape = ShapeSingleCandidate
	} else if len(exposureKeys) > 1 {
		result.Shape = ShapeBasket
	}
	if input.RequestedBasket && len(exposureKeys) < 2 {
		findings = append(findings, Finding{Code: "requested_basket_unavailable", Severity: SeverityError, Message: "the requested basket has fewer than two distinct, supported exposures; return a clearly named single-candidate view or report the gap"})
	}

	coverageInput := CoverageInput{AsOf: input.CompletedAt, RequestedRoles: input.RequestedRoles, Claims: input.Claims, Evidence: input.Evidence}
	result.Evidence = EvaluateCoverage(coverageInput)
	findings = append(findings, result.Evidence.Findings...)
	findings = append(findings, validateCandidateEvidenceLinks(input.Selected, input.Claims, result.Evidence.Claims)...)

	if input.Allocation == nil {
		findings = append(findings, Finding{Code: "proposal_allocation_missing", Severity: SeverityError, Message: "a complete portfolio proposal needs an explicit allocation method and denominator"})
	} else {
		allocationInput := *input.Allocation
		allocationInput.AsOf = input.CompletedAt
		allocation := Allocate(allocationInput)
		result.Allocation = &allocation
		findings = append(findings, allocation.Findings...)
		findings = append(findings, validateAllocationMatchesSelection(allocationInput, selectedByKey)...)
	}

	if input.Playbook == nil {
		findings = append(findings, Finding{Code: "proposal_playbook_missing", Severity: SeverityError, Message: "a complete proposal needs a belief-relevant review playbook"})
	} else {
		playbook := ValidatePlaybook(*input.Playbook, input.Evidence, input.CompletedAt)
		result.Playbook = &playbook
		findings = append(findings, playbook.Findings...)
	}

	result.Findings = findingsSorted(uniqueFindings(findings))
	result.Complete = !hasErrors(result.Findings)
	result.LiveEligible = result.Complete && !input.Snapshot.FixtureOnly && result.Evidence.LiveEligible
	if result.Playbook != nil && !result.Playbook.LiveEligible {
		result.LiveEligible = false
	}
	for _, candidate := range result.CandidateAssessments {
		if !candidate.Eligible || candidate.AccountAccess != AccountAccessUnknown && candidate.AccountAccess != AccountAccessAvailable && candidate.AccountAccess != AccountAccessUnavailable {
			result.LiveEligible = false
		}
		if candidate.Instrument.Key.valid() && (input.Snapshot.FixtureOnly || mappingIsFixtureOnly(input.Selected, candidate.Instrument.Key)) {
			result.LiveEligible = false
		}
	}
	if result.Allocation == nil || !result.Allocation.Complete {
		result.LiveEligible = false
	}
	result.Ready = result.Complete && result.LiveEligible
	return result
}

func validateCandidateEvidenceLinks(selected []SelectedCandidate, claims []EvidenceClaim, coverage []ClaimCoverage) []Finding {
	claimByID := make(map[string]EvidenceClaim, len(claims))
	for _, claim := range claims {
		claimByID[claim.ID] = claim
	}
	coverageByID := make(map[string]ClaimCoverage, len(coverage))
	for _, claim := range coverage {
		coverageByID[claim.ClaimID] = claim
	}
	var findings []Finding
	for _, candidate := range selected {
		subject := candidate.Key.String()
		if candidate.Mapping == nil || candidate.Mapping.Status != MappingReviewed {
			findings = append(findings, Finding{Code: "candidate_claim_subject_unresolved", Subject: subject, Severity: SeverityError, Message: "candidate-specific claims require a reviewed economic-underlying mapping"})
			continue
		}
		target := SubjectForMapping(*candidate.Mapping)
		if !target.valid() {
			findings = append(findings, Finding{Code: "candidate_claim_subject_invalid", Subject: subject, Severity: SeverityError, Message: "reviewed mapping does not provide a stable evidence subject"})
			continue
		}
		if len(candidate.RequiredClaimIDs) == 0 {
			findings = append(findings, Finding{Code: "candidate_claims_missing", Subject: subject, Severity: SeverityError, Message: "each selected holding needs at least one explicit, subject-bound supporting claim"})
			continue
		}
		seen := make(map[string]bool, len(candidate.RequiredClaimIDs))
		for _, claimID := range candidate.RequiredClaimIDs {
			if seen[claimID] {
				findings = append(findings, Finding{Code: "candidate_claim_duplicate", Subject: subject, Severity: SeverityError, Message: "candidate references the same supporting claim more than once"})
				continue
			}
			seen[claimID] = true
			claim, exists := claimByID[claimID]
			if !exists {
				findings = append(findings, Finding{Code: "candidate_claim_missing", Subject: subject, Severity: SeverityError, Message: "candidate references a claim that is not in the proposal"})
				continue
			}
			if claim.Target != target {
				findings = append(findings, Finding{Code: "candidate_claim_subject_mismatch", Subject: subject, Severity: SeverityError, Message: "candidate claim target does not match its reviewed economic underlying"})
				continue
			}
			claimCoverage, exists := coverageByID[claimID]
			if !exists || !claimCoverage.Supported {
				findings = append(findings, Finding{Code: "candidate_claim_unsupported", Subject: subject, Severity: SeverityError, Message: "candidate supporting claim has no eligible, dated, subject-matched evidence"})
			}
		}
	}
	return findingsSorted(findings)
}

func validateAllocationMatchesSelection(allocation AllocationInput, selected map[string]SelectedCandidate) []Finding {
	var findings []Finding
	if len(allocation.Legs) != len(selected) {
		return []Finding{{Code: "allocation_selection_mismatch", Severity: SeverityError, Message: "allocation legs must match the selected instrument set exactly"}}
	}
	seen := make(map[string]bool, len(allocation.Legs))
	for _, leg := range allocation.Legs {
		key := leg.Key.String()
		candidate, ok := selected[key]
		if !ok || seen[key] {
			findings = append(findings, Finding{Code: "allocation_selection_mismatch", Subject: key, Severity: SeverityError, Message: "allocation leg is not a unique selected candidate"})
			continue
		}
		seen[key] = true
		if candidate.Mapping == nil || leg.ExposureKey != candidate.Mapping.ExposureKey || leg.Role != candidate.Role || leg.Direction != candidate.Direction {
			findings = append(findings, Finding{Code: "allocation_candidate_identity_mismatch", Subject: key, Severity: SeverityError, Message: "allocation exposure, role, and direction must match the reviewed selected candidate"})
		}
	}
	return findingsSorted(findings)
}

func mappingIsFixtureOnly(selected []SelectedCandidate, key ProviderInstrumentKey) bool {
	for _, candidate := range selected {
		if candidate.Key == key && candidate.Mapping != nil && mappingFixtureOnly(*candidate.Mapping) {
			return true
		}
	}
	return false
}

func uniqueFindings(findings []Finding) []Finding {
	seen := make(map[string]bool, len(findings))
	result := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := string(finding.Severity) + "\x00" + finding.Code + "\x00" + finding.Subject + "\x00" + finding.Message
		if !seen[key] {
			seen[key] = true
			result = append(result, finding)
		}
	}
	return result
}

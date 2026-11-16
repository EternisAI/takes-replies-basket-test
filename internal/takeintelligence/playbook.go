package takeintelligence

import (
	"math"
	"slices"
	"strings"
	"time"
)

type ThresholdBasis string

const (
	ThresholdNone              ThresholdBasis = "none"
	ThresholdUserInstruction   ThresholdBasis = "user_instruction"
	ThresholdProductRiskPolicy ThresholdBasis = "product_risk_policy"
	ThresholdOfficialGuidance  ThresholdBasis = "official_source_guidance"
	ThresholdEvidenceFormula   ThresholdBasis = "evidence_formula"
)

type RuleNature string

const (
	RuleObservedFact RuleNature = "observed_fact"
	RulePlannedWatch RuleNature = "planned_watch"
)

// RuleBasis values align with proposal provenance categories.
type RuleBasis string

const (
	RuleBasisThesisEvidence    RuleBasis = "THESIS_EVIDENCE"
	RuleBasisProductRiskPolicy RuleBasis = "PRODUCT_RISK_POLICY"
	RuleBasisUserInstruction   RuleBasis = "USER_INSTRUCTION"
	RuleBasisQualitative       RuleBasis = "QUALITATIVE"
)

type ReviewAction string

const (
	ReviewCompareBaseline ReviewAction = "compare_baseline"
	ReviewInspectSource   ReviewAction = "inspect_source"
	ReviewReassessBelief  ReviewAction = "reassess_belief"
	ReviewReconsiderScope ReviewAction = "reconsider_scope"
	ReviewRevisitWeights  ReviewAction = "revisit_weights"
)

// ReviewIntent names the user's manual decision to revisit. Intents never
// authorize monitoring, orders, or automatic portfolio changes.
type ReviewIntent string

const (
	ReviewIntentAdd   ReviewIntent = "ADD_REVIEW"
	ReviewIntentTrim  ReviewIntent = "TRIM_REVIEW"
	ReviewIntentPause ReviewIntent = "PAUSE_ADDS"
	ReviewIntentExit  ReviewIntent = "EXIT_REVIEW"
)

type ThresholdComparator string

const (
	ThresholdAbove   ThresholdComparator = "above"
	ThresholdAtLeast ThresholdComparator = "at_least"
	ThresholdBelow   ThresholdComparator = "below"
	ThresholdAtMost  ThresholdComparator = "at_most"
)

// Playbook is inert, dated review guidance. It intentionally has no scheduler,
// notification, monitoring, order, or trade-authorization fields.
type Playbook struct {
	HorizonDays int
	AsOf        time.Time
	Rules       []PlaybookRule
}

type PlaybookRule struct {
	ID                      string
	Nature                  RuleNature
	Basis                   RuleBasis
	Target                  *EvidenceSubject
	BasisRef                string
	PolicyVersion           string
	PolicyRef               string
	UserInstructionRef      string
	Signal                  string
	FactTag                 string
	Unit                    string
	ComparisonPeriod        string
	LatestValue             string
	LatestValueUnknown      bool
	ObservedAt              time.Time
	BaselineEvidenceIDs     []string
	EvidenceIDs             []string
	WhyItMatters            string
	NextObservableEvent     string
	ReviewResponse          string
	Action                  ReviewAction
	ReviewIntents           []ReviewIntent
	ThesisBreaksIf          string
	StrengthensIf           string
	WeakensIf               string
	ComparisonDescription   string
	NumericThreshold        *float64
	ThresholdComparator     ThresholdComparator
	ThresholdUnit           string
	ThresholdBasis          ThresholdBasis
	ThresholdInstructionRef string
	ThresholdEvidenceIDs    []string
	AsOf                    time.Time
}

type PlaybookRuleAssessment struct {
	RuleID      string
	Nature      RuleNature
	Basis       RuleBasis
	Supported   bool
	FixtureOnly bool
	Citations   []Citation
	Findings    []Finding
}

type PlaybookAssessment struct {
	Complete        bool
	LiveEligible    bool
	FixtureOnlyUsed bool
	Rules           []PlaybookRuleAssessment
	Findings        []Finding
}

func ValidatePlaybook(playbook Playbook, evidence []EvidenceRecord, proposalAsOf time.Time) PlaybookAssessment {
	result := PlaybookAssessment{Complete: true, LiveEligible: true}
	var findings []Finding
	if proposalAsOf.IsZero() || playbook.AsOf.IsZero() || playbook.AsOf.After(proposalAsOf) {
		findings = append(findings, Finding{Code: "playbook_as_of_invalid", Severity: SeverityError, Message: "playbook needs a valid as-of time no later than proposal completion"})
	}
	if playbook.HorizonDays <= 0 {
		findings = append(findings, Finding{Code: "playbook_horizon_missing", Severity: SeverityError, Message: "playbook needs the proposal's explicit horizon in days"})
	}
	if len(playbook.Rules) == 0 {
		findings = append(findings, Finding{Code: "playbook_rules_missing", Severity: SeverityError, Message: "a complete proposal needs at least one belief-relevant review rule"})
	}
	byID := make(map[string]EvidenceRecord, len(evidence))
	for _, record := range evidence {
		if record.ID != "" {
			if _, exists := byID[record.ID]; exists {
				findings = append(findings, Finding{Code: "duplicate_evidence_id", Subject: record.ID, Severity: SeverityError, Message: "evidence ID appears more than once in the playbook context"})
			}
			byID[record.ID] = record
		}
	}
	seen := make(map[string]bool, len(playbook.Rules))
	for _, rule := range playbook.Rules {
		assessment := validatePlaybookRule(rule, byID, proposalAsOf)
		if rule.ID != "" && seen[rule.ID] {
			assessment.Findings = append(assessment.Findings, Finding{Code: "playbook_rule_duplicate", Subject: rule.ID, Severity: SeverityError, Message: "playbook rule ID appears more than once"})
		}
		seen[rule.ID] = true
		assessment.Findings = findingsSorted(assessment.Findings)
		assessment.Supported = !hasErrors(assessment.Findings)
		if hasErrors(assessment.Findings) {
			result.Complete = false
		}
		if assessment.FixtureOnly {
			result.FixtureOnlyUsed = true
			result.LiveEligible = false
		}
		findings = append(findings, assessment.Findings...)
		result.Rules = append(result.Rules, assessment)
	}
	result.Findings = findingsSorted(findings)
	if hasErrors(result.Findings) {
		result.Complete = false
		result.LiveEligible = false
	}
	if !result.Complete || result.FixtureOnlyUsed {
		result.LiveEligible = false
	}
	return result
}

func validatePlaybookRule(rule PlaybookRule, evidence map[string]EvidenceRecord, proposalAsOf time.Time) PlaybookRuleAssessment {
	assessment := PlaybookRuleAssessment{RuleID: rule.ID, Nature: rule.Nature, Basis: rule.Basis}
	var findings []Finding
	if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Signal) == "" || strings.TrimSpace(rule.ComparisonPeriod) == "" || strings.TrimSpace(rule.WhyItMatters) == "" || strings.TrimSpace(rule.NextObservableEvent) == "" || strings.TrimSpace(rule.ReviewResponse) == "" {
		findings = append(findings, Finding{Code: "playbook_rule_incomplete", Subject: rule.ID, Severity: SeverityError, Message: "rule needs a stable ID, observable signal, period, belief link, next event, and review response"})
	}
	if !validReviewAction(rule.Action) {
		findings = append(findings, Finding{Code: "playbook_action_invalid", Subject: rule.ID, Severity: SeverityError, Message: "rule action must be an inert review step, not monitoring or execution"})
	}
	if len(rule.ReviewIntents) > 4 || len(strings.TrimSpace(rule.ThesisBreaksIf)) > 180 || len(strings.TrimSpace(rule.StrengthensIf)) > 180 || len(strings.TrimSpace(rule.WeakensIf)) > 180 {
		findings = append(findings, Finding{Code: "playbook_review_intent_invalid", Subject: rule.ID, Severity: SeverityError, Message: "review intents and thesis-break conditions exceed their supported bounds"})
	}
	if len(rule.ReviewIntents) > 0 && (strings.TrimSpace(rule.StrengthensIf) == "" || strings.TrimSpace(rule.WeakensIf) == "") {
		findings = append(findings, Finding{Code: "playbook_directional_condition_missing", Subject: rule.ID, Severity: SeverityError, Message: "directional review intents need source-backed strengthening and weakening conditions"})
	}
	seenIntents := make(map[ReviewIntent]bool, len(rule.ReviewIntents))
	for _, intent := range rule.ReviewIntents {
		if !validReviewIntent(intent) || seenIntents[intent] {
			findings = append(findings, Finding{Code: "playbook_review_intent_invalid", Subject: rule.ID, Severity: SeverityError, Message: "review intents must be known and unique"})
			continue
		}
		seenIntents[intent] = true
		if intent == ReviewIntentExit && strings.TrimSpace(rule.ThesisBreaksIf) == "" {
			findings = append(findings, Finding{Code: "playbook_exit_condition_missing", Subject: rule.ID, Severity: SeverityError, Message: "an exit review needs a source-backed thesis-break condition"})
		}
	}
	if rule.Target != nil && !rule.Target.valid() {
		findings = append(findings, Finding{Code: "playbook_target_invalid", Subject: rule.ID, Severity: SeverityError, Message: "a scoped review rule needs a valid subject kind and stable key"})
	}
	if rule.Nature == RuleObservedFact && rule.Target == nil {
		findings = append(findings, Finding{Code: "playbook_observed_target_missing", Subject: rule.ID, Severity: SeverityError, Message: "an observed value must name the exact issuer or instrument it describes"})
	}
	if containsExecutionLanguage(rule.ReviewResponse) {
		findings = append(findings, Finding{Code: "playbook_response_not_inert", Subject: rule.ID, Severity: SeverityError, Message: "playbook response must describe a review step, not an order, trade, or active monitor"})
	}
	if rule.AsOf.IsZero() || rule.AsOf.After(proposalAsOf) {
		findings = append(findings, Finding{Code: "playbook_rule_as_of_invalid", Subject: rule.ID, Severity: SeverityError, Message: "rule as-of time must be present and no later than proposal completion"})
	}
	if rule.Nature != RuleObservedFact && rule.Nature != RulePlannedWatch {
		findings = append(findings, Finding{Code: "playbook_rule_nature_invalid", Subject: rule.ID, Severity: SeverityError, Message: "rule must be an observed fact or a planned watch"})
	}
	if rule.LatestValueUnknown {
		if strings.TrimSpace(rule.LatestValue) != "" || !rule.ObservedAt.IsZero() {
			findings = append(findings, Finding{Code: "playbook_unknown_value_conflict", Subject: rule.ID, Severity: SeverityError, Message: "unknown latest value cannot also include a value or observation date"})
		}
		if rule.Nature == RuleObservedFact {
			findings = append(findings, Finding{Code: "playbook_observed_fact_unknown", Subject: rule.ID, Severity: SeverityError, Message: "an observed fact needs a known value; use a planned watch when the current observation is unknown"})
		}
	} else {
		if rule.Nature == RulePlannedWatch {
			findings = append(findings, Finding{Code: "playbook_planned_watch_has_observation", Subject: rule.ID, Severity: SeverityError, Message: "a planned watch cannot present a current value as already observed"})
		}
		if strings.TrimSpace(rule.LatestValue) == "" || rule.ObservedAt.IsZero() || rule.ObservedAt.After(proposalAsOf) || (!rule.AsOf.IsZero() && rule.ObservedAt.After(rule.AsOf)) {
			findings = append(findings, Finding{Code: "playbook_observation_missing", Subject: rule.ID, Severity: SeverityError, Message: "known latest value needs its observation and as-of dates"})
		}
		if strings.TrimSpace(rule.Unit) == "" {
			findings = append(findings, Finding{Code: "playbook_unit_missing", Subject: rule.ID, Severity: SeverityError, Message: "a known observation needs a stated unit"})
		}
		if rule.Nature == RuleObservedFact && strings.TrimSpace(rule.FactTag) == "" {
			findings = append(findings, Finding{Code: "playbook_fact_tag_missing", Subject: rule.ID, Severity: SeverityError, Message: "an observed value must identify the source fact it copies"})
		}
	}

	switch rule.Basis {
	case RuleBasisThesisEvidence:
		if len(rule.EvidenceIDs)+len(rule.BaselineEvidenceIDs) == 0 {
			findings = append(findings, Finding{Code: "playbook_thesis_evidence_missing", Subject: rule.ID, Severity: SeverityError, Message: "thesis-evidence rules need at least one dated source or baseline reference"})
		}
	case RuleBasisProductRiskPolicy:
		if strings.TrimSpace(rule.PolicyVersion) == "" || strings.TrimSpace(rule.PolicyRef) == "" {
			findings = append(findings, Finding{Code: "playbook_policy_provenance_missing", Subject: rule.ID, Severity: SeverityError, Message: "product-risk rules need a policy version and stable policy reference"})
		}
	case RuleBasisUserInstruction:
		if strings.TrimSpace(rule.UserInstructionRef) == "" {
			findings = append(findings, Finding{Code: "playbook_instruction_provenance_missing", Subject: rule.ID, Severity: SeverityError, Message: "user-instruction rules need a stable reference to the instruction"})
		}
	case RuleBasisQualitative:
		if strings.TrimSpace(rule.BasisRef) == "" {
			findings = append(findings, Finding{Code: "playbook_qualitative_provenance_missing", Subject: rule.ID, Severity: SeverityError, Message: "qualitative planned rules need an explicit basis reference"})
		}
	default:
		findings = append(findings, Finding{Code: "playbook_basis_invalid", Subject: rule.ID, Severity: SeverityError, Message: "rule basis must preserve thesis evidence, product policy, user instruction, or qualitative provenance"})
	}
	if rule.Nature == RuleObservedFact && len(rule.EvidenceIDs) == 0 {
		findings = append(findings, Finding{Code: "playbook_observed_evidence_missing", Subject: rule.ID, Severity: SeverityError, Message: "an observed value must link dated evidence that supports that observation"})
	}
	if rule.NumericThreshold == nil {
		if rule.ThresholdComparator != "" {
			findings = append(findings, Finding{Code: "playbook_threshold_comparator_without_value", Subject: rule.ID, Severity: SeverityError, Message: "threshold comparator is present without a numeric threshold"})
		}
		if rule.ThresholdBasis != "" && rule.ThresholdBasis != ThresholdNone {
			findings = append(findings, Finding{Code: "playbook_threshold_basis_without_value", Subject: rule.ID, Severity: SeverityError, Message: "threshold basis is present without a numeric threshold"})
		}
		if len(rule.ThresholdEvidenceIDs) > 0 || strings.TrimSpace(rule.ThresholdInstructionRef) != "" {
			findings = append(findings, Finding{Code: "playbook_threshold_source_without_value", Subject: rule.ID, Severity: SeverityError, Message: "threshold sources are present without a numeric threshold"})
		}
		if strings.TrimSpace(rule.ComparisonDescription) == "" {
			findings = append(findings, Finding{Code: "playbook_comparison_missing", Subject: rule.ID, Severity: SeverityError, Message: "qualitative rules need an explicit comparison, such as the prior comparable period"})
		}
	} else {
		if math.IsNaN(*rule.NumericThreshold) || math.IsInf(*rule.NumericThreshold, 0) || strings.TrimSpace(rule.ThresholdUnit) == "" {
			findings = append(findings, Finding{Code: "playbook_threshold_invalid", Subject: rule.ID, Severity: SeverityError, Message: "numeric threshold must be finite and carry a unit"})
		}
		if !validThresholdComparator(rule.ThresholdComparator) {
			findings = append(findings, Finding{Code: "playbook_threshold_comparator_missing", Subject: rule.ID, Severity: SeverityError, Message: "numeric threshold needs an explicit above/below comparator"})
		}
		if !rule.LatestValueUnknown && strings.TrimSpace(rule.Unit) != strings.TrimSpace(rule.ThresholdUnit) {
			findings = append(findings, Finding{Code: "playbook_threshold_unit_mismatch", Subject: rule.ID, Severity: SeverityError, Message: "numeric threshold unit must match the observed signal unit"})
		}
		switch rule.ThresholdBasis {
		case ThresholdUserInstruction:
			if strings.TrimSpace(rule.ThresholdInstructionRef) == "" {
				findings = append(findings, Finding{Code: "playbook_user_threshold_unproven", Subject: rule.ID, Severity: SeverityError, Message: "user-set threshold needs a reference to the user's instruction"})
			}
		case ThresholdProductRiskPolicy:
			if strings.TrimSpace(rule.PolicyVersion) == "" || strings.TrimSpace(rule.PolicyRef) == "" {
				findings = append(findings, Finding{Code: "playbook_policy_provenance_missing", Subject: rule.ID, Severity: SeverityError, Message: "product-risk thresholds need a policy version and stable policy reference"})
			}
		case ThresholdOfficialGuidance, ThresholdEvidenceFormula:
			if len(rule.ThresholdEvidenceIDs) == 0 {
				findings = append(findings, Finding{Code: "playbook_threshold_evidence_missing", Subject: rule.ID, Severity: SeverityError, Message: "source-based threshold needs dated public evidence"})
			}
			if rule.ThresholdBasis == ThresholdEvidenceFormula && strings.TrimSpace(rule.ComparisonDescription) == "" {
				findings = append(findings, Finding{Code: "playbook_threshold_formula_missing", Subject: rule.ID, Severity: SeverityError, Message: "evidence-derived threshold needs a transparent formula or comparison description"})
			}
			if rule.ThresholdBasis == ThresholdOfficialGuidance {
				for _, id := range rule.ThresholdEvidenceIDs {
					record, ok := evidence[id]
					if ok && record.Class != SourceOfficialPrimary && record.Class != SourceVenuePrimary {
						findings = append(findings, Finding{Code: "playbook_guidance_not_primary", Subject: rule.ID, Severity: SeverityError, Message: "official guidance threshold needs a primary source"})
					}
				}
			}
		default:
			findings = append(findings, Finding{Code: "playbook_threshold_basis_missing", Subject: rule.ID, Severity: SeverityError, Message: "numeric thresholds need a user instruction, official guidance, or an evidence formula"})
		}
	}

	ids := slices.Clone(rule.EvidenceIDs)
	ids = append(ids, rule.BaselineEvidenceIDs...)
	ids = append(ids, rule.ThresholdEvidenceIDs...)
	ids = sortedUnique(ids)
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		record, ok := evidence[id]
		if !ok {
			findings = append(findings, Finding{Code: "playbook_evidence_unavailable", Subject: rule.ID, Severity: SeverityError, Message: "a linked playbook evidence record is unavailable"})
			continue
		}
		if rule.Target == nil || !slices.Contains(record.Subjects, *rule.Target) {
			findings = append(findings, Finding{Code: "playbook_evidence_subject_mismatch", Subject: rule.ID, Severity: SeverityError, Message: "linked playbook evidence does not identify the same subject as the rule"})
		}
		if record.RetrievedAt.IsZero() || record.RetrievedAt.After(proposalAsOf) || (!rule.AsOf.IsZero() && record.RetrievedAt.After(rule.AsOf)) || evidenceAnchor(record).IsZero() || evidenceAnchor(record).After(proposalAsOf) || (!rule.AsOf.IsZero() && (evidenceAnchor(record).After(rule.AsOf) || record.PublishedAt.After(rule.AsOf) || record.ObservedAt.After(rule.AsOf))) || !record.RequireFingerprint() {
			findings = append(findings, Finding{Code: "playbook_evidence_date_invalid", Subject: rule.ID, Severity: SeverityError, Message: "playbook evidence needs a dated, retrieved, fingerprinted source"})
			continue
		}
		citation, ok := PublicCitation(record)
		if !ok {
			if record.FixtureOnly && record.Visibility == VisibilityPublic && record.DisplayPermission == DisplayPermissionAllowed && validHTTPURL(record.URL) {
				assessment.FixtureOnly = true
				continue
			}
			findings = append(findings, Finding{Code: "playbook_evidence_not_displayable", Subject: rule.ID, Severity: SeverityError, Message: "playbook evidence must have approved public citation visibility"})
			continue
		}
		assessment.Citations = append(assessment.Citations, citation)
		if record.FixtureOnly {
			assessment.FixtureOnly = true
		}
	}
	if rule.Nature == RuleObservedFact {
		matchedObservation := false
		for _, id := range rule.EvidenceIDs {
			record, ok := evidence[id]
			if !ok || rule.Target == nil || !slices.Contains(record.Subjects, *rule.Target) {
				continue
			}
			if strings.TrimSpace(record.Value) != strings.TrimSpace(rule.LatestValue) ||
				strings.TrimSpace(record.Unit) != strings.TrimSpace(rule.Unit) ||
				!record.ObservedAt.Equal(rule.ObservedAt) || strings.TrimSpace(record.FactTag) != strings.TrimSpace(rule.FactTag) {
				continue
			}
			if record.PeriodEnd.IsZero() || record.PeriodEnd.After(proposalAsOf) || proposalAsOf.Sub(record.PeriodEnd) > 400*24*time.Hour {
				findings = append(findings, Finding{Code: "playbook_observation_period_stale", Subject: rule.ID, Severity: SeverityError, Message: "the source observation period is too old or later than the proposal date"})
				continue
			}
			matchedObservation = true
		}
		if !matchedObservation {
			findings = append(findings, Finding{Code: "playbook_observation_not_source_bound", Subject: rule.ID, Severity: SeverityError, Message: "the observed value, unit, date, fact tag, and subject must match a linked source record"})
		}
	}
	assessment.Citations = uniqueCitations(assessment.Citations)
	assessment.Findings = findingsSorted(findings)
	assessment.Supported = !hasErrors(assessment.Findings)
	return assessment
}

func validReviewAction(action ReviewAction) bool {
	switch action {
	case ReviewCompareBaseline, ReviewInspectSource, ReviewReassessBelief, ReviewReconsiderScope, ReviewRevisitWeights:
		return true
	default:
		return false
	}
}

func validReviewIntent(intent ReviewIntent) bool {
	switch intent {
	case ReviewIntentAdd, ReviewIntentTrim, ReviewIntentPause, ReviewIntentExit:
		return true
	default:
		return false
	}
}

func validThresholdComparator(comparator ThresholdComparator) bool {
	switch comparator {
	case ThresholdAbove, ThresholdAtLeast, ThresholdBelow, ThresholdAtMost:
		return true
	default:
		return false
	}
}

func containsExecutionLanguage(value string) bool {
	lower := strings.ToLower(value)
	for _, phrase := range []string{"place order", "execute trade", "trade now", "buy shares", "sell shares", "add position", "open position", "close position", "monitor automatically", "send notification"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func uniqueCitations(citations []Citation) []Citation {
	seen := make(map[string]bool, len(citations))
	result := make([]Citation, 0, len(citations))
	for _, citation := range citations {
		key := citation.URL + "\x00" + citation.Date.UTC().Format(time.RFC3339Nano) + "\x00" + citation.Form + "\x00" + citation.Section + "\x00" + citation.FactTag
		if !seen[key] {
			seen[key] = true
			result = append(result, citation)
		}
	}
	return result
}

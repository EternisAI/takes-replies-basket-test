package takesresearch

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	allocationPercentPattern     = regexp.MustCompile(`(?i)([+-]?[0-9][0-9.,]*)\s*%`)
	allocationIntentPattern      = regexp.MustCompile(`(?i)\b(allocate|allocation|weights?|assign|split|set|invest|put)\b`)
	allocationNegationPattern    = regexp.MustCompile(`(?i)\b(not an allocation|not allocate|do not allocate|don't allocate|not (?:my )?(?:portfolio )?allocation|isn't (?:my )?(?:portfolio )?allocation|hypothetical|for example|example only|quoted|quote)\b|\b(?:don't|dont|do not|never|not)\s+(?:(?:want|need|plan|intend)\s+to\s+)?(?:allocate|keep|hold|reserve|maintain|leave|set aside)\b`)
	allocationUncertaintyPattern = regexp.MustCompile(`(?i)\b(?:maybe|perhaps|possibly|might|consider|considering|suppose|imagine|hypothetical(?:ly)?|for example|example only)\b`)
	allocationCashPattern        = regexp.MustCompile(`(?i)\bcash\b`)
	cashOnlyAllocationPattern    = regexp.MustCompile(`(?i)^\s*(?:allocate\s+([0-9]{1,3}(?:\.[0-9]{1,4})?)\s*%\s+to\s+cash|keep\s+([0-9]{1,3}(?:\.[0-9]{1,4})?)\s*%\s+cash)\s*[.!]?\s*$`)
	cashInstructionPattern       = regexp.MustCompile(`(?i)\b(keep|hold|reserve|maintain|leave|set aside|allocate|allocation)\b`)
	equalRoleBudgetPattern       = regexp.MustCompile(`(?i)\bequal(?: total[- ]budget notional)? budgets?\b`)
	equalWithinRolePattern       = regexp.MustCompile(`(?i)\bsplit each role equally\b`)
	zeroCashPattern              = regexp.MustCompile(`(?i)\b(?:reserve|keep|hold|allocate|maintain|leave|set aside)\s+(?:no|zero)\s+cash\b|\b(?:want|need)\s+(?:no|zero)\s+cash(?:\s+reserve)?\b|\b(?:reserve|keep|hold|allocate|maintain|leave|set aside)\s+0\s*%\s*cash\b`)
	zeroCashRefusalPattern       = regexp.MustCompile(`(?i)\b(?:don't|dont|do not|never)\s+(?:(?:want|need)\s+to\s+)?(?:keep|hold|reserve|maintain|leave|set aside|allocate)\s+(?:any|no|zero)\s+cash\b`)
	// A whole-message match keeps quoted, conditional, attributed, and
	// questioning uses of "No cash, all in" from counting as an instruction.
	directZeroCashPattern = regexp.MustCompile(`(?i)^\s*(?:no\s+cash[\s,;:.-]+all\s+in|all\s+in[\s,;:.-]+no\s+cash)\s*[.!]?\s*$`)
	// A negation before "no cash" in the same clause, as in "I do not want no
	// cash", does not clearly choose zero cash.
	zeroCashDoubleNegativePattern = regexp.MustCompile(`(?i)\b(?:don't|dont|do not|does not|doesn't|never|not)\b[^.;!?]*\b(?:no|zero)\s+cash\b`)
	// The horizon parser's third-party wording: "They want 15% cash" reports
	// someone else's choice, not the user's own instruction.
	allocationThirdPartyPattern = regexp.MustCompile(`(?i)\b(?:my friend|my partner|my spouse|my coworker|my colleague|someone else|another person|they|he|she|friend says|partner says|spouse says)\b`)
	// These recognize unsupported limit instructions only. They never extract
	// a maximum or turn a limit into an exact allocation.
	// Match actual paired quotation marks, not apostrophes inside contractions
	// or issuer names such as "Don't ... BJ's".
	quotedCompanyLimitPattern      = regexp.MustCompile(`(?:^|\s)'[^'\n]+'(?:$|[\s.!?;,])`)
	companyLimitRemovalPattern     = regexp.MustCompile(`(?i)\b(?:remove|drop|lift|ignore|disable|without)\b[^.!?;]{0,80}\b(?:cap|limit|ceiling|rule|maximum)\b|\b(?:do not|don't|dont|never)\s+(?:want to\s+)?(?:cap|limit)\b|\bno (?:allocation )?(?:cap|limit|ceiling)\b`)
	unsupportedCompanyLimitPattern = regexp.MustCompile(`(?i)\bno\s+(?:(?:single|one|individual|any)\s+)?(?:company|holding|position|name)\b[^.!?;]{0,80}\b(?:above|over|more than|exceed)\b|\b(?:allocate|assign|invest|put)\s+(?:at most|no more than|not more than|less than)\b|\b(?:do not|don't|dont|never)\s+(?:allocate|invest|put)\s+more than\b|\b(?:cap|limit)\s+(?:each|every|any|the|my)\b|\b(?:company|holding|position|per[- ]name)\b[^.!?;]{0,40}\b(?:maximum|max|ceiling|must not exceed|at most|no more than)\b`)
	// Keep any suffix that could qualify, change, or retract sizing. Only
	// unrelated trailing commentary may be separated from a direct schedule.
	allocationSuffixQualifierPattern = regexp.MustCompile(`(?i)\b(?:cash|allocat\w*|weights?|budgets?|percent\w*|siz(?:e|es|ing)|split|equal\w*|invest\w*|put|keep|hold|reserve|maintain|leave|set|use|instead|change|prefer|actually|ignore|cancel|retract|forget|wait|no|not|never|without|don[’']?t|do not|if|unless|when|only|should|would|could|what about|how about)\b`)
	allocationDirectPrefixPattern    = regexp.MustCompile(`(?i)^\s*(?:(?:please|i (?:want|intend|plan) to)\s+)?(?:allocate|assign|split|invest|put|set|keep|hold|reserve|maintain|leave|use)\b`)
	allocationGenericFollowupPattern = regexp.MustCompile(`(?i)^\s*(?:does that work|is that (?:okay|ok))\s*\?\s*$`)
	allocationConditionalPattern     = regexp.MustCompile(`(?i)\b(?:if|unless|when|provided|as long as)\b`)
	allocationRetractionPattern      = regexp.MustCompile(`(?i)\b(?:ignore|cancel|retract|forget|wait|actually\s+no|not\s+that|don[’']?t\s+do\s+that)\b`)
)

const (
	allocationTargetCandidate = "candidate"
	allocationTargetRole      = "business_role"
)

type resolvedUserAllocation struct {
	Context        AllocationConstraintContext
	Candidate      map[string]float64
	Role           map[string]float64
	CashPercent    *float64
	SourceByTarget map[string][]string
	Unresolved     bool
	// Superseded reports that a later direct instruction replaced an earlier,
	// different one.
	Superseded bool
}

type parsedAllocationMessage struct {
	policy    string
	targets   []AllocationConstraint
	cash      *float64
	sourceIDs []string
}

func validateSavedCashConstraint(input Input, saved *savedDraftConstraints) *string {
	if saved == nil || saved.CashConstraint == nil {
		return nil
	}
	constraint := saved.CashConstraint
	if !input.UserMessagesLoaded || !input.UserMessagesComplete || input.BuildCreatedAt.IsZero() || strings.TrimSpace(constraint.SourceMessageID) == "" {
		saved.CashConstraint = nil
		message := "A saved cash reserve lacked complete, source-message-backed user instruction and was not applied."
		return &message
	}
	for _, message := range input.UserMessages {
		if message.ID != constraint.SourceMessageID {
			continue
		}
		if message.ThreadID != input.ThreadID || message.UserID != input.UserID || message.Role != "user" || message.CreatedAt.IsZero() || message.CreatedAt.After(input.BuildCreatedAt) || !cashInstructionMatches(message.Content, constraint.Percent) {
			saved.CashConstraint = nil
			invalid := "A saved cash reserve did not match its persisted user instruction and was not applied."
			return &invalid
		}
		return nil
	}
	saved.CashConstraint = nil
	missing := "A saved cash reserve referenced no verified user message in this build's history and was not applied."
	return &missing
}

func cashInstructionMatches(content string, expected float64) bool {
	content, _ = allocationInstructionPrefix(content)
	if allocationRetractionPattern.MatchString(content) || allocationConditionalPattern.MatchString(content) {
		return false
	}
	// Quoted examples and hypothetical text are not allocation instructions.
	// Preserve contractions such as "don't" for the explicit negation check,
	// but reject paired ASCII apostrophes and typographic quote delimiters.
	if strings.ContainsAny(content, "\"“”‘’") || quotedCompanyLimitPattern.MatchString(content) || allocationUncertaintyPattern.MatchString(content) || indirectAllocationText(content) {
		return false
	}
	if expected == 0 && zeroCashDoubleNegativePattern.MatchString(content) {
		return false
	}
	if allocationNegationPattern.MatchString(content) {
		return expected == 0 && !strings.Contains(content, "%") && zeroCashRefusalPattern.MatchString(content)
	}
	if !validAllocationPercentTokens(content) {
		return false
	}
	if expected == 0 && (zeroCashPattern.MatchString(content) || directZeroCashPattern.MatchString(content)) {
		return true
	}
	if !allocationCashPattern.MatchString(content) || !cashInstructionPattern.MatchString(content) {
		return false
	}
	for _, part := range regexp.MustCompile(`[;,\n]+`).Split(content, -1) {
		if !allocationCashPattern.MatchString(part) {
			continue
		}
		matches := allocationPercentPattern.FindAllStringSubmatch(part, -1)
		if len(matches) != 1 {
			continue
		}
		value, err := strconv.ParseFloat(matches[0][1], 64)
		if err == nil && math.Abs(value-expected) <= 1e-8 {
			return true
		}
	}
	return false
}

// indirectAllocationText reports a question or someone else's choice. Neither
// is the user's own allocation instruction, whatever the amount.
func indirectAllocationText(content string) bool {
	return strings.Contains(content, "?") || allocationThirdPartyPattern.MatchString(content)
}

// ExplicitCashInstruction recognizes a direct, unquoted cash allocation in a
// user message. Callers use it before persisting model-extracted constraints;
// the build boundary repeats the owner, history, and cutoff checks.
func ExplicitCashInstruction(message string, expected float64) bool {
	return cashInstructionMatches(message, expected)
}

// resolveUserAllocation reads only persisted user messages that are bound to
// this build's owner/thread and immutable creation cutoff. It never forwards
// raw text to the model. It accepts only complete, unambiguous percentage
// schedules using candidate issuer names or declared business-role labels.
func resolveUserAllocation(input Input, plan ResearchPlan, saved savedDraftConstraints) resolvedUserAllocation {
	result := resolvedUserAllocation{
		Context:   AllocationConstraintContext{Status: AllocationConstraintsUnavailable, Targets: []AllocationConstraint{}},
		Candidate: map[string]float64{}, Role: map[string]float64{}, SourceByTarget: map[string][]string{},
	}
	if !input.UserMessagesLoaded || input.BuildCreatedAt.IsZero() || strings.TrimSpace(input.ThreadID) == "" || strings.TrimSpace(input.UserID) == "" {
		result.Unresolved = true
		result.Context.Status = AllocationConstraintsUnavailable
		result.Context.CoverageNote = "Persisted allocation-message coverage was not available for this build."
		return result
	}
	if !input.UserMessagesComplete {
		result.Unresolved = true
		result.Context.Status = AllocationConstraintsUnresolved
		result.Context.CoverageNote = "Allocation history exceeds the bounded message scan; no default weights were applied."
		return result
	}
	unresolved := func(note string) resolvedUserAllocation {
		result.Unresolved = true
		result.Context.Status = AllocationConstraintsUnresolved
		result.Context.CoverageNote = note
		return result
	}
	type allocationEvent struct {
		parsed parsedAllocationMessage
		valid  bool
		at     time.Time
		id     string
	}
	seenIDs := make(map[string]bool, len(input.UserMessages))
	events := make([]allocationEvent, 0, 2)
	for _, message := range input.UserMessages {
		if strings.TrimSpace(message.ID) == "" || seenIDs[message.ID] || message.ThreadID != input.ThreadID || message.UserID != input.UserID || strings.TrimSpace(message.Role) != "user" || message.CreatedAt.IsZero() || message.CreatedAt.After(input.BuildCreatedAt) || len(message.Content) > 32_000 {
			return unresolved("Persisted allocation messages failed owner, role, identity, or build-cutoff checks; no default weights were applied.")
		}
		seenIDs[message.ID] = true
		parsed, found, valid := parseAllocationMessage(message.Content, message.ID, plan)
		if found {
			events = append(events, allocationEvent{parsed: parsed, valid: valid, at: message.CreatedAt, id: message.ID})
		}
	}
	// A verified saved cash level counts as an instruction at the time of its
	// source message, so a later direct instruction can replace it.
	if saved.CashConstraint != nil && !slices.ContainsFunc(events, func(event allocationEvent) bool { return event.id == saved.CashConstraint.SourceMessageID }) {
		cash := saved.CashConstraint.Percent
		event := allocationEvent{
			parsed: parsedAllocationMessage{policy: "CASH_ONLY", targets: []AllocationConstraint{}, cash: &cash, sourceIDs: []string{saved.CashConstraint.SourceMessageID}},
			valid:  true, id: saved.CashConstraint.SourceMessageID,
		}
		for _, message := range input.UserMessages {
			if message.ID == event.id {
				event.at = message.CreatedAt
			}
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		result.Context.Status = AllocationConstraintsNone
		return result
	}
	// The user's latest direct instruction takes precedence, and a stated cash
	// level stands until a later instruction states another. An ambiguous latest
	// instruction, or one that conflicts with a standing cash level it does not
	// mention, blocks sizing until the user resolves it.
	slices.SortStableFunc(events, func(left, right allocationEvent) int {
		if !left.at.Equal(right.at) {
			return left.at.Compare(right.at)
		}
		return strings.Compare(left.id, right.id)
	})
	latest, earlier := events[len(events)-1], events[:len(events)-1]
	if !latest.valid {
		return unresolved("Your latest allocation instruction was ambiguous or incomplete; no default weights were applied.")
	}
	base := latest.parsed
	switch {
	case base.policy == "CASH_ONLY":
		if base.cash == nil {
			return unresolved("The explicit cash-only instruction did not include a verified percentage; no default weights were applied.")
		}
		// A later cash level must agree with the most recent schedule, whose
		// unassigned remainder is its cash; otherwise the holdings need resizing.
		for index := len(earlier) - 1; index >= 0; index-- {
			schedule := earlier[index].parsed
			if !earlier[index].valid || schedule.policy == "CASH_ONLY" {
				continue
			}
			scheduleCash := 100.0
			if schedule.cash != nil {
				scheduleCash = *schedule.cash
			} else {
				for _, target := range schedule.targets {
					scheduleCash -= target.Percent
				}
			}
			if math.Abs(scheduleCash-*base.cash) > 1e-8 {
				return unresolved(fmt.Sprintf("Your later %s%% cash instruction changes an earlier allocation that left %s%% in cash; tell me how the holdings should change. No weights were applied.", formatPercent(*base.cash), formatPercent(scheduleCash)))
			}
			laterCash := *base.cash
			base = schedule
			base.cash = &laterCash
			base.sourceIDs = append(slices.Clone(schedule.sourceIDs), latest.parsed.sourceIDs...)
			break
		}
	case base.cash == nil:
		// A schedule that does not mention cash keeps the most recently stated
		// cash level, however many messages ago it was given.
		for index := len(earlier) - 1; index >= 0; index-- {
			if !earlier[index].valid || earlier[index].parsed.cash == nil {
				continue
			}
			standing := *earlier[index].parsed.cash
			holdings := 0.0
			for _, target := range base.targets {
				holdings += target.Percent
			}
			if math.Abs(holdings+standing-100) > 1e-8 {
				return unresolved(fmt.Sprintf("Your latest allocation puts %s%% in holdings, but your earlier %s%% cash instruction still stands, which makes %s%% of the budget; tell me whether to change the holdings or the cash. No weights were applied.", formatPercent(holdings), formatPercent(standing), formatPercent(holdings+standing)))
			}
			base.cash = &standing
			base.sourceIDs = append(slices.Clone(base.sourceIDs), earlier[index].parsed.sourceIDs...)
			break
		}
	}
	base.sourceIDs = uniqueSorted(base.sourceIDs)
	// A complete schedule that assigns 100% to holdings and states no cash
	// leaves a user-originated zero, not a product cash policy downstream.
	cash := 0.0
	if base.cash != nil {
		cash = *base.cash
	}
	if base.policy == "" {
		total := cash
		for _, target := range base.targets {
			total += target.Percent
		}
		if math.Abs(total-100) > 1e-8 {
			return unresolved("Explicit allocation percentages do not account for the full total budget; no default weights were applied.")
		}
	}
	for _, event := range earlier {
		cashChanged := event.parsed.cash != nil && math.Abs(*event.parsed.cash-cash) > 1e-8
		if event.valid && (cashChanged || (event.parsed.policy != "CASH_ONLY" && !sameTargets(event.parsed, base))) {
			result.Superseded = true
		}
	}
	result.Context.Status = AllocationConstraintsResolved
	result.Context.Policy = base.policy
	result.Context.Targets = slices.Clone(base.targets)
	result.Context.CashPercent = &cash
	result.Context.SourceIDs = slices.Clone(base.sourceIDs)
	result.CashPercent = &cash
	for _, target := range base.targets {
		key := target.TargetKind + ":" + target.TargetID
		result.SourceByTarget[key] = slices.Clone(base.sourceIDs)
		switch target.TargetKind {
		case allocationTargetCandidate:
			result.Candidate[target.TargetID] = target.Percent / 100
		case allocationTargetRole:
			result.Role[target.TargetID] = target.Percent / 100
		}
	}
	return result
}

// allocationInstructionPrefix keeps the complete instruction and any later
// qualification. Decimal points are not sentence boundaries. A question or
// third-person word in unrelated trailing commentary must not erase a schedule.
func allocationInstructionPrefix(content string) (string, bool) {
	boundaries := []int{}
	for index, char := range content {
		decimalPoint := index > 0 && index+1 < len(content) && content[index-1] >= '0' && content[index-1] <= '9' && content[index+1] >= '0' && content[index+1] <= '9'
		if char == ';' || char == '\n' || char == '!' || char == '?' || char == '.' && !decimalPoint {
			boundaries = append(boundaries, index+1)
		}
	}
	boundaries = append(boundaries, len(content))
	if len(boundaries) < 2 {
		return content, false
	}
	first := content[:boundaries[0]]
	direct := allocationDirectPrefixPattern.MatchString(first) && strings.Contains(first, "%") && (allocationIntentPattern.MatchString(first) || cashInstructionPattern.MatchString(first)) &&
		!allocationNegationPattern.MatchString(first) && !allocationUncertaintyPattern.MatchString(first) && !indirectAllocationText(first) && !strings.ContainsAny(first, "\"“”‘’") && !quotedCompanyLimitPattern.MatchString(first)
	if !direct {
		return content, false
	}
	end := boundaries[0]
	for index := 1; index < len(boundaries); index++ {
		part := content[boundaries[index-1]:boundaries[index]]
		if strings.TrimSpace(part) == "" {
			continue
		}
		genericQuestion := allocationGenericFollowupPattern.MatchString(part)
		if strings.ContainsAny(part, "%\"“”‘’") || allocationSuffixQualifierPattern.MatchString(part) || allocationUncertaintyPattern.MatchString(part) || allocationNegationPattern.MatchString(part) || quotedCompanyLimitPattern.MatchString(part) || strings.Contains(part, "?") && !genericQuestion {
			end = boundaries[index]
		}
	}
	return content[:end], true
}

// validAllocationPercentTokens rejects malformed percentages before splitting
// clauses, so an invalid latest cash instruction cannot become an implied zero.
func validAllocationPercentTokens(content string) bool {
	matches := allocationPercentPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) != strings.Count(content, "%") {
		return false
	}
	for _, match := range matches {
		start, end := match[2], match[3]
		if start > 0 {
			prior, _ := utf8.DecodeLastRuneInString(content[:start])
			if unicode.IsLetter(prior) || unicode.IsNumber(prior) || strings.ContainsRune("_.+-", prior) {
				return false
			}
		}
		raw := content[start:end]
		whole, fraction, hasFraction := strings.Cut(raw, ".")
		digits := strings.TrimLeft(whole, "+-")
		if len(digits) > 3 || strings.Contains(raw, ",") || hasFraction && (len(fraction) == 0 || len(fraction) > 4) {
			return false
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return false
		}
	}
	return true
}

// Unsupported company limits require clarification instead of being silently
// discarded now that there is no product concentration ceiling. Questions,
// examples, attributed text and requests to remove a cap are not new limits.
func unsupportedCompanyLimitInstruction(content string) bool {
	if !allocationPercentPattern.MatchString(content) || strings.ContainsAny(content, "\"“”‘’") || quotedCompanyLimitPattern.MatchString(content) || allocationUncertaintyPattern.MatchString(content) || indirectAllocationText(content) || companyLimitRemovalPattern.MatchString(content) {
		return false
	}
	return unsupportedCompanyLimitPattern.MatchString(content)
}

func parseAllocationMessage(content, messageID string, plan ResearchPlan) (parsedAllocationMessage, bool, bool) {
	content, directPrefix := allocationInstructionPrefix(content)
	if directPrefix && (allocationConditionalPattern.MatchString(content) || allocationRetractionPattern.MatchString(content) || allocationNegationPattern.MatchString(content) || allocationUncertaintyPattern.MatchString(content) || indirectAllocationText(content) || strings.ContainsAny(content, "\"“”‘’") || quotedCompanyLimitPattern.MatchString(content)) {
		return parsedAllocationMessage{}, true, false
	}
	if unsupportedCompanyLimitInstruction(content) {
		return parsedAllocationMessage{}, true, false
	}
	if allocationNegationPattern.MatchString(content) || allocationUncertaintyPattern.MatchString(content) || strings.ContainsAny(content, "\"“”‘’") || indirectAllocationText(content) {
		return parsedAllocationMessage{}, false, true
	}
	if strings.Contains(content, "%") && (allocationIntentPattern.MatchString(content) || allocationCashPattern.MatchString(content) && cashInstructionPattern.MatchString(content)) && !validAllocationPercentTokens(content) {
		return parsedAllocationMessage{}, true, false
	}
	if directZeroCashPattern.MatchString(content) {
		zeroCash := 0.0
		return parsedAllocationMessage{policy: "CASH_ONLY", targets: []AllocationConstraint{}, cash: &zeroCash, sourceIDs: []string{messageID}}, true, true
	}
	if match := cashOnlyAllocationPattern.FindStringSubmatch(content); len(match) == 3 {
		valueText := match[1]
		if valueText == "" {
			valueText = match[2]
		}
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
			return parsedAllocationMessage{}, true, false
		}
		return parsedAllocationMessage{policy: "CASH_ONLY", targets: []AllocationConstraint{}, cash: &value, sourceIDs: []string{messageID}}, true, true
	}
	if equalRoleBudgetPattern.MatchString(content) && equalWithinRolePattern.MatchString(content) && len(plan.BusinessRoles) > 0 && zeroCashPattern.MatchString(content) {
		zeroCash := 0.0
		return parsedAllocationMessage{policy: "EQUAL_ROLE", targets: []AllocationConstraint{}, cash: &zeroCash, sourceIDs: []string{messageID}}, true, true
	}
	if !strings.Contains(content, "%") || !allocationIntentPattern.MatchString(content) {
		return parsedAllocationMessage{}, false, true
	}
	percentMatches := allocationPercentPattern.FindAllStringSubmatchIndex(content, -1)
	if len(percentMatches) == 0 {
		return parsedAllocationMessage{}, false, false
	}
	parts := regexp.MustCompile(`[;,\n]+`).Split(content, -1)
	parsed := parsedAllocationMessage{targets: []AllocationConstraint{}, sourceIDs: []string{messageID}}
	seen := make(map[string]bool)
	for _, part := range parts {
		matches := allocationPercentPattern.FindAllStringSubmatch(part, -1)
		if len(matches) == 0 {
			continue
		}
		if len(matches) != 1 {
			return parsedAllocationMessage{}, true, false
		}
		percent, err := strconv.ParseFloat(matches[0][1], 64)
		if err != nil || math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 || percent > 100 {
			return parsedAllocationMessage{}, true, false
		}
		targets := allocationTargetsInText(part, plan)
		if allocationCashPattern.MatchString(part) {
			if len(targets) != 0 || parsed.cash != nil {
				return parsedAllocationMessage{}, true, false
			}
			value := percent
			parsed.cash = &value
			continue
		}
		if percent == 0 || len(targets) != 1 {
			return parsedAllocationMessage{}, true, false
		}
		target := targets[0]
		key := target.TargetKind + ":" + target.TargetID
		if seen[key] {
			return parsedAllocationMessage{}, true, false
		}
		seen[key] = true
		target.Percent = percent
		parsed.targets = append(parsed.targets, target)
	}
	if len(parsed.targets) == 0 || len(percentMatches) != len(parsed.targets)+boolInt(parsed.cash != nil) {
		return parsedAllocationMessage{}, true, false
	}
	sortAllocationTargets(parsed.targets)
	return parsed, true, true
}

func allocationTargetsInText(text string, plan ResearchPlan) []AllocationConstraint {
	var targets []AllocationConstraint
	for _, candidate := range plan.Candidates {
		if sameIdentityPhrase(candidate.IssuerName, text) {
			targets = append(targets, AllocationConstraint{TargetKind: allocationTargetCandidate, TargetID: candidate.ID})
		}
	}
	for _, role := range plan.BusinessRoles {
		if sameIdentityPhrase(role.Label, text) || sameIdentityPhrase(role.ID, text) {
			targets = append(targets, AllocationConstraint{TargetKind: allocationTargetRole, TargetID: role.ID})
		}
	}
	return targets
}

// sameTargets compares two instructions' policy and holdings, whatever cash
// each states.
func sameTargets(left, right parsedAllocationMessage) bool {
	if left.policy != right.policy || len(left.targets) != len(right.targets) {
		return false
	}
	for index := range left.targets {
		if left.targets[index].TargetKind != right.targets[index].TargetKind || left.targets[index].TargetID != right.targets[index].TargetID || math.Abs(left.targets[index].Percent-right.targets[index].Percent) > 1e-8 {
			return false
		}
	}
	return true
}

func sortAllocationTargets(targets []AllocationConstraint) {
	slices.SortFunc(targets, func(left, right AllocationConstraint) int {
		if left.TargetKind != right.TargetKind {
			return strings.Compare(left.TargetKind, right.TargetKind)
		}
		return strings.Compare(left.TargetID, right.TargetID)
	})
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func allocationConstraintKey(kind, id string) string { return fmt.Sprintf("%s:%s", kind, id) }

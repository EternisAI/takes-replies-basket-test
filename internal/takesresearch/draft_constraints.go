package takesresearch

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

var (
	exclusionActionPattern      = regexp.MustCompile(`(?i)\b(?:exclude|excluding|excluded|omit|omitting|leave out|keep out|do not include|don't include|dont include|do not hold|don't hold|dont hold|do not own|don't own|dont own|no exposure to)\b`)
	exclusionNegationPattern    = regexp.MustCompile(`(?i)\b(?:don't|dont|don’t|do not|never|not|shouldn't|shouldn’t|should not|no need to)\s+(?:(?:want|need|have|plan|intend)\s+to\s+)?(?:exclude|omit|avoid|leave out|keep out)\b`)
	exclusionNoIncludePattern   = regexp.MustCompile(`(?i)\b(?:do not|don't|dont|don’t|never)\s+(?:(?:want|need)\s+to\s+)?(?:include|hold|own|invest in)\b`)
	exclusionIncludePattern     = regexp.MustCompile(`(?i)\b(?:include|retain|add|hold|own|keep in|do not exclude|don't exclude|dont exclude|don’t exclude|not exclude|don't omit|don’t omit|do not omit)\b`)
	exclusionUncertaintyPattern = regexp.MustCompile(`(?i)\b(?:maybe|perhaps|might|could|consider|whether|hypothetical(?:ly)?|for example|example only|suppose|imagine|quoted|quote)\b`)
)

type savedDraftConstraints struct {
	Horizon struct {
		Days            int    `json:"days"`
		Origin          string `json:"origin"`
		SourceMessageID string `json:"sourceMessageId"`
	} `json:"horizon"`
	RiskPreference *struct {
		Style           string `json:"style"`
		Status          string `json:"status"`
		SourceMessageID string `json:"sourceMessageId"`
	} `json:"riskPreference"`
	RequestedHoldings *int `json:"requestedHoldings"`
	CashConstraint    *struct {
		Percent         float64 `json:"percent"`
		Denominator     string  `json:"denominator"`
		SourceMessageID string  `json:"sourceMessageId"`
	} `json:"cashConstraint"`
	Exclusions []string `json:"exclusions"`
}

// applyVerifiedSizingPreferences makes the user's latest direct risk-style and
// horizon statements authoritative. Neither is required: without a verified
// statement the standard defaults apply and are disclosed as defaults, never as
// user choices. A saved draft value that a later message replaced or that the
// message history cannot confirm is not used.
func applyVerifiedSizingPreferences(draft *ProposalDraft, input Input, saved savedDraftConstraints) []takeintelligence.Finding {
	savedStyle, savedStyleSource := "", ""
	if saved.RiskPreference != nil && saved.RiskPreference.Status == "USER_CONFIRMED" {
		savedStyle = strings.ToUpper(strings.TrimSpace(saved.RiskPreference.Style))
		savedStyleSource = strings.TrimSpace(saved.RiskPreference.SourceMessageID)
	}
	savedUserHorizon := strings.EqualFold(strings.TrimSpace(saved.Horizon.Origin), "USER")
	draft.RiskStyle = ""
	draft.HorizonDays, draft.HorizonOrigin = defaultHorizonDays, "product_default_unconfirmed"
	if !input.UserMessagesLoaded || !input.UserMessagesComplete || input.BuildCreatedAt.IsZero() || input.ThreadID == "" || input.UserID == "" || !validSizingMessageHistory(input) {
		if savedStyle == "" && !savedUserHorizon {
			return nil
		}
		draft.PreferenceNotes = append(draft.PreferenceNotes, "Your saved risk style or horizon could not be checked against the full conversation, so the standard defaults were used.")
		return []takeintelligence.Finding{{Code: "sizing_preferences_unverified", Severity: takeintelligence.SeverityWarning,
			Message: "Saved risk-style or horizon preferences could not be verified against complete user-message history; standard defaults were used instead."}}
	}
	orderedMessages := slices.Clone(input.UserMessages)
	slices.SortFunc(orderedMessages, func(left, right PersistedUserMessage) int {
		if left.CreatedAt.Before(right.CreatedAt) {
			return -1
		}
		if left.CreatedAt.After(right.CreatedAt) {
			return 1
		}
		return strings.Compare(left.ID, right.ID)
	})
	var style takeintelligence.RiskStyle
	styleStated, horizonStated := false, false
	horizonDays := 0
	riskContextSeen := false
	for _, message := range orderedMessages {
		if parsed, ok := takeintelligence.ParseRiskStyleAnswer(message.Content, riskContextSeen || message.ID == savedStyleSource); ok {
			style, styleStated, riskContextSeen = parsed, true, true
		}
		if days, ok := takeintelligence.ParseUserHorizonDays(message.Content); ok {
			horizonDays, horizonStated = days, true
		}
	}
	var findings []takeintelligence.Finding
	switch {
	case styleStated && style.Resolved():
		draft.RiskStyle = style
	case styleStated:
		draft.PreferenceNotes = append(draft.PreferenceNotes, "Your latest risk statement was not one of conservative, balanced, or aggressive, so the standard approach was used.")
	}
	if savedStyle != "" && savedStyle != string(draft.RiskStyle) {
		findings = append(findings, takeintelligence.Finding{Code: "saved_risk_style_superseded", Severity: takeintelligence.SeverityInfo,
			Message: "A later direct user message replaced or did not confirm the saved risk style; the latest statement was used."})
	}
	switch {
	case horizonStated && horizonDays > 0:
		draft.HorizonDays, draft.HorizonOrigin = horizonDays, "user"
	case horizonStated:
		draft.HorizonDays, draft.HorizonOrigin = defaultHorizonDays, "product_default_unconfirmed"
		draft.PreferenceNotes = append(draft.PreferenceNotes, "Your latest holding-horizon statement was not a single duration, so the one-year default review horizon was used.")
	}
	if savedUserHorizon && saved.Horizon.Days != 0 && (!horizonStated || horizonDays != saved.Horizon.Days) {
		findings = append(findings, takeintelligence.Finding{Code: "saved_horizon_superseded", Severity: takeintelligence.SeverityInfo,
			Message: "A later direct user message replaced or did not confirm the saved horizon; the latest statement was used."})
	}
	return findings
}

func validSizingMessageHistory(input Input) bool {
	seen := make(map[string]bool, len(input.UserMessages))
	for _, message := range input.UserMessages {
		if strings.TrimSpace(message.ID) == "" || seen[message.ID] || message.ThreadID != input.ThreadID || message.UserID != input.UserID || message.Role != "user" || message.CreatedAt.IsZero() || message.CreatedAt.After(input.BuildCreatedAt) || len(message.Content) > 32_000 {
			return false
		}
		seen[message.ID] = true
	}
	return true
}

func parseSavedDraftConstraints(raw json.RawMessage) (savedDraftConstraints, error) {
	var constraints savedDraftConstraints
	if err := json.Unmarshal(raw, &constraints); err != nil {
		return constraints, err
	}
	if constraints.Horizon.Days != 0 && (constraints.Horizon.Days < 1 || constraints.Horizon.Days > 3650) {
		return constraints, errors.New("saved horizon is outside the supported range")
	}
	if constraints.RequestedHoldings != nil && (*constraints.RequestedHoldings < 1 || *constraints.RequestedHoldings > 40) {
		return constraints, errors.New("saved requested-holdings count is outside the supported range")
	}
	if constraints.CashConstraint != nil && (math.IsNaN(constraints.CashConstraint.Percent) || math.IsInf(constraints.CashConstraint.Percent, 0) || constraints.CashConstraint.Percent < 0 || constraints.CashConstraint.Percent > 100) {
		return constraints, errors.New("saved cash constraint is outside the supported range")
	}
	if len(constraints.Exclusions) > 20 {
		return constraints, errors.New("saved exclusion list exceeds the supported bound")
	}
	for _, exclusion := range constraints.Exclusions {
		if len(strings.TrimSpace(exclusion)) > 120 {
			return constraints, errors.New("saved exclusion exceeds the supported bound")
		}
	}
	return constraints, nil
}

// ExplicitUserExclusionState reports an unambiguous, direct user instruction
// about one target. It is intentionally separate from model-produced draft
// fields: only persisted user messages can establish a hard exclusion.
func ExplicitUserExclusionState(message, target string) (mentioned, excluded bool) {
	target = strings.TrimSpace(target)
	if target == "" || containsQuoteDelimiter(message) {
		return false, false
	}
	// A question or hypothetical is not a saved constraint. Split corrective
	// clauses so a later instruction in the same turn can supersede an earlier
	// one (for example, “exclude A; actually, keep A”).
	parts := regexp.MustCompile(`(?i);|\n|\bbut\b|\bhowever\b|[.!]`).Split(message, -1)
	for _, part := range parts {
		if strings.Contains(part, "?") || exclusionUncertaintyPattern.MatchString(part) || !sameIdentityPhrase(target, part) {
			continue
		}
		if exclusionNegationPattern.MatchString(part) {
			mentioned, excluded = true, false
			continue
		}
		if exclusionNoIncludePattern.MatchString(part) {
			mentioned, excluded = true, true
			continue
		}
		if exclusionIncludePattern.MatchString(part) {
			mentioned, excluded = true, false
			continue
		}
		if exclusionActionPattern.MatchString(part) {
			mentioned, excluded = true, true
		}
	}
	return mentioned, excluded
}

func containsQuoteDelimiter(message string) bool {
	runes := []rune(message)
	for index, current := range runes {
		switch current {
		case '"', '“', '”', '‘':
			return true
		case '\'', '’':
			// Apostrophes between letters are contractions (for example,
			// "don't" or "don’t"), not quoted text.
			if index > 0 && index+1 < len(runes) && unicode.IsLetter(runes[index-1]) && unicode.IsLetter(runes[index+1]) {
				continue
			}
			return true
		}
	}
	return false
}

func validateSavedUserExclusions(input Input, saved *savedDraftConstraints) []takeintelligence.Finding {
	if saved == nil || len(saved.Exclusions) == 0 {
		return nil
	}
	if !input.UserMessagesLoaded || !input.UserMessagesComplete || input.BuildCreatedAt.IsZero() || input.ThreadID == "" || input.UserID == "" {
		saved.Exclusions = nil
		return []takeintelligence.Finding{{Code: "saved_exclusion_unverified", Severity: takeintelligence.SeverityError, Message: "Saved exclusions lacked complete source-message-backed user history and were not applied."}}
	}
	seenIDs := make(map[string]bool, len(input.UserMessages))
	for _, message := range input.UserMessages {
		if strings.TrimSpace(message.ID) == "" || seenIDs[message.ID] || message.ThreadID != input.ThreadID || message.UserID != input.UserID || message.Role != "user" || message.CreatedAt.IsZero() || message.CreatedAt.After(input.BuildCreatedAt) || len(message.Content) > 32_000 {
			saved.Exclusions = nil
			return []takeintelligence.Finding{{Code: "saved_exclusion_history_invalid", Severity: takeintelligence.SeverityError, Message: "Saved exclusions failed user, thread, role, identity, or build-cutoff checks and were not applied."}}
		}
		seenIDs[message.ID] = true
	}
	confirmed := make([]string, 0, len(saved.Exclusions))
	for _, exclusion := range saved.Exclusions {
		stateSeen, stateExcluded := false, false
		latest := time.Time{}
		for _, message := range input.UserMessages {
			mentioned, excluded := ExplicitUserExclusionState(message.Content, exclusion)
			if mentioned && !message.CreatedAt.Before(latest) {
				latest, stateSeen, stateExcluded = message.CreatedAt, true, excluded
			}
		}
		if stateSeen && stateExcluded {
			confirmed = append(confirmed, strings.TrimSpace(exclusion))
		}
	}
	if len(confirmed) == len(saved.Exclusions) {
		saved.Exclusions = uniqueSorted(confirmed)
		return nil
	}
	saved.Exclusions = uniqueSorted(confirmed)
	return []takeintelligence.Finding{{Code: "saved_exclusion_unverified", Severity: takeintelligence.SeverityError, Message: "One or more model-saved exclusions lacked a matching direct user instruction and were not applied."}}
}
func sameIdentityPhrase(left, right string) bool {
	a, b := identityTokens(left), identityTokens(right)
	if len(a) == 0 || len(b) == 0 || len(a) > len(b) {
		return false
	}
	for start := 0; start+len(a) <= len(b); start++ {
		if slices.Equal(a, b[start:start+len(a)]) {
			return true
		}
	}
	return false
}

func identityTokens(value string) []string {
	var tokens []string
	var token strings.Builder
	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, strings.ToLower(token.String()))
			token.Reset()
		}
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

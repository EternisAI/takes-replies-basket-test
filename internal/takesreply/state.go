package takesreply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"github.com/google/uuid"
)

func newDraft(threadID string, now time.Time) Draft {
	return Draft{
		ID: threadID + ":0", ThreadID: threadID, Revision: 0, Stage: "UNDERSTANDING",
		CreatedAt: now, UpdatedAt: now,
		Belief: Belief{IntentStatus: "UNCLEAR", InvestmentThesis: InvestmentThesis{Status: "NOT_ESTABLISHED"}, PersonalPreferences: []PersonalPreference{}},
		Facts:  []Fact{}, Assumptions: []Assumption{}, Horizon: Horizon{Days: 365, Origin: "PRODUCT_DEFAULT"}, Choices: []Choice{},
		SelectedChoiceIDs: []string{}, SelectionCriteria: []Criterion{}, Exclusions: []string{}, Preferences: []Preference{},
	}
}

func requestHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func applyTurnOutput(old Draft, out TurnModelOutput, sourceID uuid.UUID, selectedID *string, currentMessage string, now time.Time) Draft {
	updated := old
	thesisStatementGrounded := out.Belief.ThesisStatement != nil && messageSupports(cleanText(*out.Belief.ThesisStatement, 1000), currentMessage)
	thesisMechanismGrounded := out.Belief.ThesisMechanism != nil && messageSupports(cleanText(*out.Belief.ThesisMechanism, 1000), currentMessage)
	updated.Revision++
	updated.ID = updated.ThreadID + ":" + fmt.Sprint(updated.Revision)
	updated.UpdatedAt = now
	updated.Stage = out.Stage
	if out.Belief.UserStatement != nil {
		value := cleanText(*out.Belief.UserStatement, 1000)
		if value != "" && (old.Belief.UserStatement == nil || normalizeText(*old.Belief.UserStatement) != normalizeText(value)) {
			updated.Belief.UserStatement = &value
			if messageSupports(value, currentMessage) {
				updated.Belief.SourceMessageID = &sourceID
			} else {
				updated.Belief.SourceMessageID = nil
			}
		}
	}
	if out.Belief.CausalMechanism != nil {
		value := cleanText(*out.Belief.CausalMechanism, 1000)
		if value != "" && (old.Belief.CausalMechanism == nil || normalizeText(*old.Belief.CausalMechanism) != normalizeText(value)) {
			updated.Belief.CausalMechanism = &value
			// A generated explanation is an inference unless its material terms
			// appear in the current user turn. Do not overwrite an existing user
			// statement's source when the mechanism is only inferred.
			if updated.Belief.SourceMessageID == nil && messageSupports(value, currentMessage) {
				updated.Belief.SourceMessageID = &sourceID
			}
		}
	}
	if validIntent(out.Belief.IntentStatus) {
		updated.Belief.IntentStatus = out.Belief.IntentStatus
	}
	if out.Belief.ThesisStatement != nil {
		value := cleanText(*out.Belief.ThesisStatement, 1000)
		if value != "" && (old.Belief.InvestmentThesis.Statement == nil || normalizeText(*old.Belief.InvestmentThesis.Statement) != normalizeText(value)) {
			updated.Belief.InvestmentThesis.Statement = &value
			if out.Belief.ThesisStatus == "USER_STATED" && messageSupports(value, currentMessage) {
				updated.Belief.InvestmentThesis.SourceMessageID = &sourceID
			} else {
				updated.Belief.InvestmentThesis.SourceMessageID = nil
				if out.Belief.ThesisStatus == "USER_STATED" {
					updated.Belief.InvestmentThesis.Status = "INFERRED_UNCONFIRMED"
				}
			}
		}
	}
	if out.Belief.ThesisMechanism != nil {
		value := cleanText(*out.Belief.ThesisMechanism, 1000)
		if value != "" && (old.Belief.InvestmentThesis.CausalMechanism == nil || normalizeText(*old.Belief.InvestmentThesis.CausalMechanism) != normalizeText(value)) {
			updated.Belief.InvestmentThesis.CausalMechanism = &value
			if updated.Belief.InvestmentThesis.SourceMessageID == nil && messageSupports(value, currentMessage) {
				updated.Belief.InvestmentThesis.SourceMessageID = &sourceID
			}
		}
	}
	if validThesisStatus(out.Belief.ThesisStatus) {
		oldThesisUnchanged := equalTextPtr(old.Belief.InvestmentThesis.Statement, updated.Belief.InvestmentThesis.Statement) &&
			equalTextPtr(old.Belief.InvestmentThesis.CausalMechanism, updated.Belief.InvestmentThesis.CausalMechanism)
		if out.Belief.ThesisStatus != "USER_STATED" || thesisStatementGrounded || thesisMechanismGrounded ||
			(old.Belief.InvestmentThesis.Status == "USER_STATED" && oldThesisUnchanged) {
			updated.Belief.InvestmentThesis.Status = out.Belief.ThesisStatus
		} else {
			updated.Belief.InvestmentThesis.Status = "INFERRED_UNCONFIRMED"
		}
	}
	if len(out.Belief.PersonalPreferences) > 0 {
		updated.Belief.PersonalPreferences = make([]PersonalPreference, 0, min(len(out.Belief.PersonalPreferences), 8))
		for _, p := range out.Belief.PersonalPreferences[:min(len(out.Belief.PersonalPreferences), 8)] {
			subject, pref := cleanText(p.Subject, 120), cleanText(p.Preference, 240)
			if subject == "" || pref == "" {
				continue
			}
			if messageSupports(subject+" "+pref, currentMessage) || existingPreference(old.Belief.PersonalPreferences, subject, pref) != nil {
				provenance := sourceID
				if existing := existingPreference(old.Belief.PersonalPreferences, subject, pref); existing != nil {
					provenance = existing.SourceMessageID
				}
				updated.Belief.PersonalPreferences = append(updated.Belief.PersonalPreferences, PersonalPreference{Subject: subject, Preference: pref, SourceMessageID: provenance})
			}
		}
	}
	if out.Horizon.Days != nil {
		days := *out.Horizon.Days
		explicitUserHorizon := out.Horizon.UserSpecified && messageMentionsHorizon(currentMessage, days)
		if explicitUserHorizon {
			// Record explicit user provenance even when the requested horizon is
			// numerically equal to the product default or saved value.
			updated.Horizon.Days = days
			updated.Horizon.Origin, updated.Horizon.SourceMessageID = "USER", &sourceID
		} else if days != old.Horizon.Days {
			// A model's inferred default must not silently replace an explicit
			// user horizon on an unrelated turn. Only a current message that
			// actually states the new horizon can change that saved constraint.
			if old.Horizon.Origin == "USER" && !explicitUserHorizon {
				// Preserve the prior user value and its provenance unchanged.
			} else {
				updated.Horizon.Days = days
				updated.Horizon.Origin, updated.Horizon.SourceMessageID = "ASSISTANT_INFERENCE", nil
			}
		}
	}
	if selectedID == nil && strings.TrimSpace(currentMessage) != "" && old.SelectedCompanyScope != nil {
		pending := *old.SelectedCompanyScope
		pending.Scope.IssuerNames = append([]string{}, pending.Scope.IssuerNames...)
		pending.Scope.AnchorIssuerNames = append([]string{}, pending.Scope.AnchorIssuerNames...)
		pending.NeedsConfirmation = true
		updated.SelectedCompanyScope = &pending
	}
	var selectedChoice *Choice
	if selectedID != nil {
		for i := range old.Choices {
			if old.Choices[i].ID == *selectedID {
				choice := old.Choices[i]
				choice.InstrumentRefs = []string{}
				selectedChoice = &choice
				break
			}
		}
	}
	if len(out.Choices) > 0 {
		updated.Choices = make([]Choice, 0, min(len(out.Choices), 6))
		for _, choice := range out.Choices[:min(len(out.Choices), 6)] {
			label := cleanText(choice.Label, 56)
			if label == "" {
				continue
			}
			role := cleanText(choice.ExposureRole, 48)
			if role == "build_now" {
				label = "Build it now"
			}
			choiceID := uuid.NewString()
			if selectedChoice != nil && normalizeText(selectedChoice.Label) == normalizeText(label) {
				choiceID = selectedChoice.ID
			}
			// Instrument identifiers must come from the catalog resolver, not
			// free-form model output.
			scope := choice.CompanyScope
			scope.IssuerNames = append([]string{}, scope.IssuerNames...)
			scope.AnchorIssuerNames = append([]string{}, scope.AnchorIssuerNames...)
			if selectedChoice != nil && choiceID == selectedChoice.ID && selectedChoice.CompanyScope != nil {
				scope = *selectedChoice.CompanyScope
				scope.IssuerNames = append([]string{}, scope.IssuerNames...)
				scope.AnchorIssuerNames = append([]string{}, scope.AnchorIssuerNames...)
			}
			updated.Choices = append(updated.Choices, Choice{CompanyScope: &scope, ID: choiceID, Label: label, ExposureRole: role, Description: cleanText(choice.Description, 180), InstrumentRefs: []string{}, Tradeoffs: stableStringList([]string{choice.Tradeoff}, 1, 160)})
		}
		if selectedChoice != nil && !selectedChoiceRequestsBuild(selectedChoice) && !hasChoiceID(updated.Choices, selectedChoice.ID) {
			buildIndex := buildChoiceIndex(updated.Choices)
			maxRoutes := 4
			if buildIndex >= 0 {
				maxRoutes = 3
			}
			routeCount := len(updated.Choices)
			if buildIndex >= 0 {
				routeCount--
			}
			if routeCount >= maxRoutes {
				for i := len(updated.Choices) - 1; i >= 0; i-- {
					if !selectedChoiceRequestsBuild(&updated.Choices[i]) {
						updated.Choices = append(updated.Choices[:i], updated.Choices[i+1:]...)
						break
					}
				}
			}
			if buildIndex = buildChoiceIndex(updated.Choices); buildIndex >= 0 {
				updated.Choices = append(updated.Choices, Choice{})
				copy(updated.Choices[buildIndex+1:], updated.Choices[buildIndex:])
				updated.Choices[buildIndex] = *selectedChoice
			} else {
				updated.Choices = append(updated.Choices, *selectedChoice)
			}
		}
		updated.SelectedChoiceIDs = updated.SelectedChoiceIDs[:0]
		for _, selected := range old.SelectedChoiceIDs {
			if hasChoiceID(updated.Choices, selected) {
				updated.SelectedChoiceIDs = appendUnique(updated.SelectedChoiceIDs, selected)
			}
		}
	} else if !eligibleBuildChoiceMenu(out, old.Belief) {
		updated.Choices = make([]Choice, 0, len(old.Choices))
		for _, choice := range old.Choices {
			if !selectedChoiceRequestsBuild(&choice) {
				updated.Choices = append(updated.Choices, choice)
			}
		}
		updated.SelectedChoiceIDs = updated.SelectedChoiceIDs[:0]
		for _, selected := range old.SelectedChoiceIDs {
			if hasChoiceID(updated.Choices, selected) {
				updated.SelectedChoiceIDs = appendUnique(updated.SelectedChoiceIDs, selected)
			}
		}
	}
	if selectedChoice != nil && !selectedChoiceRequestsBuild(selectedChoice) {
		updated.SelectedCompanyScope = nil
		if selectedChoice.CompanyScope != nil {
			scope := *selectedChoice.CompanyScope
			scope.IssuerNames = append([]string{}, scope.IssuerNames...)
			scope.AnchorIssuerNames = append([]string{}, scope.AnchorIssuerNames...)
			updated.SelectedCompanyScope = &takesresearch.SelectedCompanyScope{Scope: scope, SourceChoiceID: selectedChoice.ID, Origin: "MODEL_PROPOSED_USER_SELECTED"}
		}
	}
	if selectedID != nil {
		if hasChoiceID(updated.Choices, *selectedID) {
			updated.SelectedChoiceIDs = appendUnique(updated.SelectedChoiceIDs, *selectedID)
		}
	}
	for i := range updated.Choices {
		updated.Choices[i].InstrumentRefs = []string{}
	}
	if len(out.Criteria) > 0 {
		updated.SelectionCriteria = make([]Criterion, 0, min(len(out.Criteria), 12))
		for _, c := range stableStringList(out.Criteria, 12, 300) {
			var criterionSource *uuid.UUID
			for i := range old.SelectionCriteria {
				if normalizeText(old.SelectionCriteria[i].Criterion) == normalizeText(c) {
					criterionSource = old.SelectionCriteria[i].SourceMessageID
					break
				}
			}
			if criterionSource == nil && messageSupports(c, currentMessage) {
				criterionSource = &sourceID
			}
			updated.SelectionCriteria = append(updated.SelectionCriteria, Criterion{Criterion: c, SourceMessageID: criterionSource, Status: "CURRENT"})
		}
	}
	if len(out.Exclusions) > 0 || len(old.Exclusions) > 0 {
		confirmed := make([]string, 0, len(out.Exclusions)+len(old.Exclusions))
		for _, existing := range old.Exclusions {
			if mentioned, excluded := explicitUserExclusionState(currentMessage, existing); !mentioned || excluded {
				confirmed = append(confirmed, existing)
			}
		}
		for _, exclusion := range stableStringList(out.Exclusions, 20, 120) {
			if mentioned, excluded := explicitUserExclusionState(currentMessage, exclusion); mentioned && excluded {
				confirmed = append(confirmed, exclusion)
			}
		}
		updated.Exclusions = stableStringList(confirmed, 20, 120)
	}
	if out.RequestedHoldings != nil && out.RequestedHoldings.UserSpecified && out.RequestedHoldings.Count != nil &&
		messageMentionsNumber(currentMessage, float64(*out.RequestedHoldings.Count), false) &&
		containsAny(strings.ToLower(currentMessage), "holding", "position", "name", "stock", "leg", "basket", "companies", "company", "assets") {
		n := *out.RequestedHoldings.Count
		updated.RequestedHoldings = &n
	}
	if out.CashConstraint != nil && out.CashConstraint.UserSpecified && explicitCashInstruction(currentMessage, out.CashConstraint.Percent) {
		updated.CashConstraint = &CashConstraint{Percent: out.CashConstraint.Percent, Denominator: cleanText(out.CashConstraint.Denominator, 120), SourceMessageID: sourceID}
	}
	if len(out.Preferences) > 0 {
		updated.Preferences = make([]Preference, 0, min(len(out.Preferences), 20))
		for _, p := range out.Preferences[:min(len(out.Preferences), 20)] {
			key := cleanText(p.Key, 80)
			if key == "" {
				continue
			}
			value := append(json.RawMessage(nil), json.RawMessage(p.Value)...)
			if !json.Valid(value) {
				value = json.RawMessage(`null`)
			}
			updated.Preferences = append(updated.Preferences, Preference{Key: key, Value: value, Origin: "ASSISTANT_INFERENCE", Enforced: false})
		}
	}
	return withReadyBuildChoice(updated)
}

var provenanceTokens = regexp.MustCompile(`[[:alnum:]]{3,}`)
var numericTokens = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

var provenanceStopWords = map[string]struct{}{
	"about": {}, "after": {}, "against": {}, "because": {}, "build": {}, "could": {},
	"from": {}, "have": {}, "into": {}, "more": {}, "over": {}, "should": {},
	"that": {}, "their": {}, "there": {}, "these": {}, "they": {}, "this": {},
	"through": {}, "under": {}, "very": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "while": {}, "with": {}, "would": {}, "your": {}, "user": {},
}

func normalizeText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func equalTextPtr(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return normalizeText(*left) == normalizeText(*right)
}

// messageSupports is a conservative provenance guard. A model may normalize a
// user statement, but it cannot attach the current message ID to a paraphrase
// whose material terms are absent from that turn.
func messageSupports(candidate, message string) bool {
	candidateTokens := make([]string, 0, 8)
	for _, token := range provenanceTokens.FindAllString(strings.ToLower(candidate), -1) {
		if _, stop := provenanceStopWords[token]; !stop {
			candidateTokens = append(candidateTokens, token)
		}
	}
	if len(candidateTokens) == 0 {
		return false
	}
	messageSet := make(map[string]struct{})
	for _, token := range provenanceTokens.FindAllString(strings.ToLower(message), -1) {
		messageSet[token] = struct{}{}
	}
	matches := 0
	for _, token := range candidateTokens {
		if _, ok := messageSet[token]; ok {
			matches++
		}
	}
	return matches*2 >= len(candidateTokens)
}

func messageMentionsNumber(message string, value float64, requirePercent bool) bool {
	lower := strings.ToLower(message)
	if requirePercent && !strings.Contains(message, "%") && !containsAny(lower, "percent", "per cent") {
		return false
	}
	for _, raw := range numericTokens.FindAllString(message, -1) {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err == nil && math.Abs(parsed-value) < 0.0001 {
			return true
		}
	}
	for _, wordValue := range numberWords(lower) {
		if math.Abs(float64(wordValue)-value) < 0.0001 {
			return true
		}
	}
	return false
}

func messageMentionsHorizon(message string, days int) bool {
	const calendarRoundingToleranceDays = 5.0

	lower := strings.ToLower(message)
	words := strings.Fields(lower)
	for i := 1; i < len(words); i++ {
		unitDays := 0.0
		switch strings.Trim(words[i], ",.;:!?()[]{}") {
		case "day", "days":
			unitDays = 1
		case "week", "weeks":
			unitDays = 7
		case "month", "months":
			unitDays = 30
		case "year", "years":
			unitDays = 365
		}
		if unitDays == 0 {
			continue
		}
		quantity, ok := numberBefore(words, i)
		previous := strings.Trim(words[i-1], ",.;:!?()[]{}")
		if !ok && unitDays == 365 && (previous == "next" || previous == "a") {
			quantity, ok = 1, true
		}
		if !ok {
			continue
		}
		tolerance := 0.0001
		if unitDays == 30 || unitDays == 365 {
			tolerance = calendarRoundingToleranceDays
		}
		if math.Abs(quantity*unitDays-float64(days)) <= tolerance {
			return true
		}
	}
	return false
}

func numberBefore(words []string, index int) (float64, bool) {
	values := map[string]float64{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
		"twenty": 20, "thirty": 30, "forty": 40,
	}
	prev := strings.Trim(words[index-1], ",.;:!?()[]{}")
	if number, err := strconv.ParseFloat(prev, 64); err == nil {
		return number, true
	}
	value, ok := values[prev]
	if !ok {
		return 0, false
	}
	if index >= 2 {
		prior := strings.Trim(words[index-2], ",.;:!?()[]{}")
		if base, exists := values[prior]; exists && base >= 20 && (prior == "twenty" || prior == "thirty" || prior == "forty") && value > 0 && value < 10 {
			return base + value, true
		}
	}
	return value, true
}

func numberWords(message string) []int {
	words := strings.Fields(strings.ToLower(message))
	values := map[string]int{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
		"twenty": 20, "thirty": 30, "forty": 40,
	}
	tens := map[string]int{"twenty": 20, "thirty": 30, "forty": 40}
	result := make([]int, 0, 3)
	for i := 0; i < len(words); i++ {
		if value, ok := values[words[i]]; ok {
			if _, isTen := tens[words[i]]; isTen && i+1 < len(words) {
				if ones, exists := values[words[i+1]]; exists && ones > 0 && ones < 10 {
					value += ones
					i++
				}
			}
			result = append(result, value)
		}
	}
	return result
}

func hasChoiceID(choices []Choice, id string) bool {
	for _, choice := range choices {
		if choice.ID == id {
			return true
		}
	}
	return false
}

func buildChoiceIndex(choices []Choice) int {
	for i := range choices {
		if selectedChoiceRequestsBuild(&choices[i]) {
			return i
		}
	}
	return -1
}

func existingPreference(preferences []PersonalPreference, subject, preference string) *PersonalPreference {
	for i := range preferences {
		if normalizeText(preferences[i].Subject) == normalizeText(subject) && normalizeText(preferences[i].Preference) == normalizeText(preference) {
			return &preferences[i]
		}
	}
	return nil
}

func validIntent(v string) bool {
	return v == "PERSONAL_PREFERENCE" || v == "INVESTMENT_THESIS" || v == "MIXED" || v == "UNCLEAR"
}
func validThesisStatus(v string) bool {
	return v == "USER_STATED" || v == "INFERRED_UNCONFIRMED" || v == "NOT_ESTABLISHED"
}

func appendUnique(list []string, item string) []string {
	for _, prior := range list {
		if prior == item {
			return list
		}
	}
	return append(list, item)
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func withReadyBuildChoice(draft Draft) Draft {
	if draft.Stage != "READY_TO_BUILD" || !hasUsableBelief(BeliefOutput{}, draft.Belief) || buildChoiceIndex(draft.Choices) >= 0 {
		return draft
	}
	draft.Choices = append(append([]Choice(nil), draft.Choices...), Choice{
		ID: draft.ID + ":build", Label: "Build it now", ExposureRole: "build_now",
		Description: "Build a proposed basket from this idea.", InstrumentRefs: []string{}, Tradeoffs: []string{},
	})
	return draft
}

package takesreply

import (
	"errors"
	"fmt"
	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	negativeBuildPattern    = regexp.MustCompile(`(?i)\b(?:don't|dont|do not|never|not|without|hold off on|wait to)\b(?:\s+\w+){0,5}\s+\b(?:build|make|create|put|pick)\b`)
	explanatoryBuildPattern = regexp.MustCompile(`(?i)\b(?:explain how|show me how|how would you|how could you|what if i said|if i said|hypothetical(?:ly)?|for example)\b`)
	buildClausePattern      = regexp.MustCompile(`(?i)^\s*(?:(?:please|yes|okay|ok|sure|go ahead(?: and)?|i want you to|i would like you to|i'd like you to|can you|could you|then)\s+)*(?:build(?:\s+(?:it|the|my|a|this))?(?:\s+(?:basket|portfolio|proposal|now|it))?|make\s+(?:me\s+)?(?:(?:the|my|a)\s+)?(?:basket|portfolio|proposal)|create\s+(?:(?:the|my|a)\s+)?(?:basket|portfolio|proposal)|put together\s+(?:(?:the|my|a)\s+)?(?:basket|portfolio|proposal)|pick\s+(?:for me|a basket|a portfolio))(?:\b|$)`)
	coordinatedBuildPattern = regexp.MustCompile(`(?i)\b(?:and|then)\s+(?:please\s+)?(?:build|make|create)\s+(?:(?:it|the|my|a|this)\s+)?(?:basket|portfolio|proposal|now|it)(?:\s+now)?\b`)
	exclusionActionPattern  = regexp.MustCompile(`(?i)\b(exclude|leave out|keep out|include|avoid)\b`)
	exclusionNegationPrefix = regexp.MustCompile(`(?i)\b(?:don't|dont|do not|never|not)\b(?:\s+\w+){0,4}\s*$`)
)

func explicitBuildRequest(message string) bool {
	message = withoutQuotedText(message)
	if explanatoryBuildPattern.MatchString(message) || negativeBuildPattern.MatchString(message) {
		return false
	}
	for _, clause := range regexp.MustCompile(`[.!?;,\n]+`).Split(message, -1) {
		clause = strings.TrimSpace(clause)
		if buildClausePattern.MatchString(clause) || coordinatedBuildPattern.MatchString(clause) {
			return true
		}
	}
	return false
}

func selectedChoiceRequestsBuild(choice *Choice) bool {
	if choice == nil {
		return false
	}
	return isBuildNowChoice(choice.ExposureRole, choice.Label)
}

func normalizeConversationChoices(out TurnModelOutput, saved Belief) TurnModelOutput {
	eligible := eligibleBuildChoiceMenu(out, saved)
	routes := make([]ChoiceOutput, 0, len(out.Choices))
	for _, choice := range out.Choices {
		if isBuildNowChoice(choice.ExposureRole, choice.Label) {
			continue
		}
		routes = append(routes, choice)
	}
	if eligible && len(routes) > 0 {
		routes = append(routes, ChoiceOutput{CompanyScope: takesresearch.CompanyScope{Kind: takesresearch.CompanyScopeOpen, IssuerNames: []string{}}, Label: "Build it now", ExposureRole: "build_now", Description: "Build a proposed basket from this idea."})
	}
	out.Choices = routes
	return out
}

func eligibleBuildChoiceMenu(out TurnModelOutput, saved Belief) bool {
	return out.Decision == "REPLY" && out.Stage == "CHOICES" && hasUsableBelief(out.Belief, saved)
}

func hasUsableBelief(out BeliefOutput, saved Belief) bool {
	if nonempty(out.UserStatement, out.CausalMechanism, out.ThesisStatement, out.ThesisMechanism) ||
		nonempty(saved.UserStatement, saved.CausalMechanism, saved.InvestmentThesis.Statement, saved.InvestmentThesis.CausalMechanism) {
		return true
	}
	for _, preference := range out.PersonalPreferences {
		if cleanText(preference.Subject, 120) != "" && cleanText(preference.Preference, 240) != "" {
			return true
		}
	}
	for _, preference := range saved.PersonalPreferences {
		if cleanText(preference.Subject, 120) != "" && cleanText(preference.Preference, 240) != "" {
			return true
		}
	}
	return false
}

func nonempty(values ...*string) bool {
	for _, value := range values {
		if value != nil && cleanText(*value, 1000) != "" {
			return true
		}
	}
	return false
}

func isBuildNowChoice(role, label string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	label = normalizeText(label)
	return role == "build_now" || role == "build-now" || role == "build now" || label == "build now" || label == "build it now"
}

func isInitialBrandPreferenceReply(draft Draft, recent []Message, selected *Choice, requestedBuild bool, message string) bool {
	if draft.Revision != 0 || len(recent) > 1 || selected != nil || requestedBuild {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	//nolint:misspell // Recognize both American and British spelling in user-authored input.
	return containsAny(lower, "i like ", "i love ", "i enjoy ", "i wear ", "i use ", "i buy ", "i shop at ", "i listen to ", "i play ", "my favorite ", "my favourite ", "favorite brand", "favourite brand")
}

func validateConversationChoices(out TurnModelOutput) error {
	if len(out.Choices) > 4 {
		return errors.New("takes choices exceed the four-path limit")
	}
	routeCount := 0
	for _, choice := range out.Choices {
		if err := choice.CompanyScope.Validate(); err != nil {
			return err
		}
		if utf8.RuneCountInString(choice.Label) > 56 || utf8.RuneCountInString(choice.ExposureRole) > 48 ||
			utf8.RuneCountInString(choice.Description) > 180 || utf8.RuneCountInString(choice.Tradeoff) > 160 {
			return errors.New("takes choice field exceeds its supported length")
		}
		if cleanText(choice.Label, 56) == "" || cleanText(choice.ExposureRole, 48) == "" || cleanText(choice.Description, 180) == "" {
			return errors.New("takes choices require a label, exposure role, and description")
		}
		if !isBuildNowChoice(choice.ExposureRole, choice.Label) {
			routeCount++
		}
	}
	if routeCount > 3 {
		return errors.New("takes choices exceed the three-route limit")
	}
	if out.Stage == "CHOICES" && routeCount < 1 {
		return errors.New("takes choice stage requires at least one typed route")
	}
	return nil
}

func validateTurnOutput(out TurnModelOutput) error {
	if cleanText(out.AssistantMessage, 1800) == "" {
		return errors.New("empty Takes assistant response")
	}
	switch out.Decision {
	case "REPLY", "NEEDS_CLARIFICATION", "READY_TO_BUILD", "BUILD_QUEUED":
	default:
		return fmt.Errorf("invalid Takes turn decision %q", out.Decision)
	}
	switch out.Stage {
	case "UNDERSTANDING", "CHOICES", "DEVELOPING", "READY_TO_BUILD", "NEEDS_INPUT":
	default:
		return fmt.Errorf("invalid Takes draft stage %q", out.Stage)
	}
	if out.Horizon.Days != nil && (*out.Horizon.Days < 1 || *out.Horizon.Days > 3650) {
		return errors.New("takes horizon is outside the supported range")
	}
	if len(out.Choices) > 6 || len(out.SelectedChoiceIDs) > 6 || len(out.Criteria) > 12 ||
		len(out.Exclusions) > 20 || len(out.Preferences) > 20 || len(out.Belief.PersonalPreferences) > 8 {
		return errors.New("takes turn output exceeds a bounded collection")
	}
	if out.RequestedHoldings != nil && out.RequestedHoldings.Count != nil && (*out.RequestedHoldings.Count < 1 || *out.RequestedHoldings.Count > 40) {
		return errors.New("requested holdings must be between 1 and 40")
	}
	for _, preference := range out.Preferences {
		if err := validatePreferenceValue(preference.Value); err != nil {
			return err
		}
	}
	if out.CashConstraint != nil && (math.IsNaN(out.CashConstraint.Percent) || out.CashConstraint.Percent < 0 || out.CashConstraint.Percent > 100) {
		return errors.New("cash constraint must be a percentage from 0 to 100")
	}
	return nil
}

func needsCurrentEvidenceCheck(message string) bool {
	lower := strings.ToLower(message)
	//nolint:misspell // Recognize British spelling in user-authored input.
	return containsAny(lower,
		"i like ", "i love ", "favorite brand", "favourite brand", "recent", "latest", "current", "right now", "price today", "what happened to",
		"valuation", "expectations", "priced in", "price reflects", "share price", "multiple", "expensive", "cheap", "undervalued", "overvalued",
		"margin", "profit", "economics", "trade down", "trade-down", "benefit", "who gains", "who wins", "alternative", "competitor", "customers", "market share", "growth")
}

func explicitCashInstruction(message string, percent float64) bool {
	return takesresearch.ExplicitCashInstruction(withoutQuotedText(message), percent)
}

func explicitUserExclusionState(message, candidate string) (bool, bool) {
	message = strings.ToLower(strings.TrimSpace(withoutQuotedText(message)))
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if candidate == "" {
		return false, false
	}
	actions := exclusionActionPattern.FindAllStringSubmatchIndex(message, -1)
	for index, action := range actions {
		if len(action) < 4 {
			continue
		}
		targetStart := action[1]
		for targetStart < len(message) && (message[targetStart] == ' ' || message[targetStart] == '\t') {
			targetStart++
		}
		targetEnd := len(message)
		if boundary := strings.IndexAny(message[targetStart:], ".!?;\n"); boundary >= 0 {
			targetEnd = targetStart + boundary
		}
		if index+1 < len(actions) && actions[index+1][0] < targetEnd {
			targetEnd = actions[index+1][0]
		}
		if targetEnd > targetStart && strings.Contains(message[targetStart:targetEnd], candidate) {
			verb := strings.ToLower(message[action[2]:action[3]])
			prefixStart := strings.LastIndexAny(message[:action[0]], ".!?;\n") + 1
			if index > 0 && actions[index-1][1] > prefixStart {
				prefixStart = actions[index-1][1]
			}
			prefix := strings.TrimSpace(message[prefixStart:action[0]])
			negated := exclusionNegationPrefix.MatchString(prefix)
			if strings.HasPrefix(prefix, "not only") {
				negated = false
			}
			if verb == "include" {
				// "don't include X" is a positive exclusion; "include X"
				// clears an earlier exclusion.
				return true, negated
			}
			return true, !negated
		}
	}
	return false, false
}

// withoutQuotedText removes matched quoted spans while leaving apostrophes in
// contractions and possessives untouched. An apostrophe opens a quote only at
// a word boundary, and internal apostrophes inside a single-quoted span do not
// close it.
func withoutQuotedText(message string) string {
	runes := []rune(message)
	var out strings.Builder
	for i := 0; i < len(runes); {
		close, opener, ok := quotedSpanOpener(runes, i)
		if !ok {
			out.WriteRune(runes[i])
			i++
			continue
		}
		end := i + 1
		for end < len(runes) {
			if runes[end] == close {
				if (opener == '\'' || opener == '‘') && isSingleQuotedContentApostrophe(runes, end, opener) {
					end++
					continue
				}
				break
			}
			end++
		}
		out.WriteByte(' ')
		if end == len(runes) {
			// Treat an unmatched quote as opening a quoted remainder so it cannot
			// turn quoted text into a build or preference instruction.
			break
		}
		i = end + 1
		out.WriteByte(' ')
	}
	return out.String()
}

func quotedSpanOpener(runes []rune, index int) (close, opener rune, ok bool) {
	switch runes[index] {
	case '"':
		return '"', '"', true
	case '“':
		return '”', '“', true
	case '‘':
		return '’', '‘', true
	case '\'':
		if index > 0 && isWordRune(runes[index-1]) {
			return 0, 0, false
		}
		return '\'', '\'', true
	default:
		return 0, 0, false
	}
}

func isSingleQuotedContentApostrophe(runes []rune, index int, opener rune) bool {
	if index == 0 || !isWordRune(runes[index-1]) {
		return false
	}
	if index+1 < len(runes) && isWordRune(runes[index+1]) {
		return true
	}
	// Treat a trailing s-apostrophe as a plural possessive only when it is
	// followed by a word and a matching close appears before another opener.
	if unicode.ToLower(runes[index-1]) != 's' || index+1 >= len(runes) || !unicode.IsSpace(runes[index+1]) {
		return false
	}
	nextWord := index + 1
	for nextWord < len(runes) && unicode.IsSpace(runes[nextWord]) {
		nextWord++
	}
	if nextWord >= len(runes) || !isWordRune(runes[nextWord]) {
		return false
	}
	for next := nextWord; next < len(runes); next++ {
		if isFreshSingleQuoteOpener(runes, next) {
			return false
		}
		if runes[next] != openerClose(opener) || next == 0 || unicode.IsSpace(runes[next-1]) {
			continue
		}
		if isWordRune(runes[next-1]) && next+1 < len(runes) && isWordRune(runes[next+1]) {
			continue
		}
		return true
	}
	return false
}

func isFreshSingleQuoteOpener(runes []rune, index int) bool {
	if runes[index] == '‘' {
		return true
	}
	return runes[index] == '\'' && (index == 0 || !isWordRune(runes[index-1])) && index+1 < len(runes) && isWordRune(runes[index+1])
}

func openerClose(opener rune) rune {
	if opener == '‘' {
		return '’'
	}
	return '\''
}

func isWordRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsDigit(value) || value == '_'
}

func cleanText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func stableStringList(in []string, maxCount, maxLen int) []string {
	out := make([]string, 0, min(len(in), maxCount))
	seen := map[string]bool{}
	for _, item := range in {
		item = cleanText(item, maxLen)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= maxCount {
			break
		}
	}
	return out
}

func safePublicURL(value string) *string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" {
		return nil
	}
	clean := parsed.String()
	return &clean
}

func timePtr(value time.Time) *time.Time { return &value }
func stringPtr(value string) *string     { return &value }

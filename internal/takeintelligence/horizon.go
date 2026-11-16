package takeintelligence

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	horizonDurationPattern = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|eighteen|twenty)(?:\s+|-)(days?|weeks?|months?|years?)\b`)
	horizonRangePattern    = regexp.MustCompile(`(?i)\b(?:\d+(?:\.\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|eighteen|twenty)\s*(?:-|–|to|or)\s*(?:\d+(?:\.\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|eighteen|twenty)\s+(?:days?|weeks?|months?|years?)\b`)
	horizonClauseSplit     = regexp.MustCompile(`(?i)\b(but|however|actually|instead|rather)\b|[;!?\n]+|\.(?:\s+|$)`)
	horizonQuotedText      = regexp.MustCompile(`"[^\"]*"|'[^']*'|“[^”]*”|‘[^’]*’`)
	horizonContext         = regexp.MustCompile(`(?i)\b(?:(?:my\s+)?(?:investment\s+)?(?:holding\s+)?horizon|holding\s+period|time\s+frame|time\s+horizon|(?:i\s+)?(?:plan|expect|intend|want|will)\s+to\s+hold|(?:i\s+)?hold(?:ing)?\s+(?:for|over))\b`)
	horizonThirdParty      = regexp.MustCompile(`(?i)\b(?:my friend|my partner|my spouse|my coworker|my colleague|someone else|another person|they|he|she|friend says|partner says|spouse says)\b`)
	horizonHypothetical    = regexp.MustCompile(`(?i)\b(?:if|what if|suppose|imagine|hypothetical(?:ly)?|maybe|perhaps|might|could|whether|for example|quoted)\b`)
	horizonNegatedChoice   = regexp.MustCompile(`(?i)\b(?:i\s+(?:do not|don't|dont|never|cannot|can't|would not|wouldn't|will not|won't|am not|i'm not)\b|my\s+(?:(?:investment|holding)\s+)?horizon\s+(?:(?:is|should be|would be)\s+)?(?:not|isn't|wouldn't be)\b)`)
	horizonBareAnswer      = regexp.MustCompile(`(?i)^\s*(?:for\s+)?(?:\d+(?:\.\d+)?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|eighteen|twenty)(?:\s+|-)(?:days?|weeks?|months?|years?)\s*[.!]?\s*$`)
	horizonCashAmount      = regexp.MustCompile(`(?i)\d\s*(?:%|percent\b).*\bcash\b|\bcash\b.*\d\s*(?:%|percent\b)`)
)

// ParseUserHorizonDays returns the exact day count stated by the user and
// whether the message contains a direct horizon instruction. A direct but
// ambiguous, negated, or ranged horizon is recognized with days set to zero.
func ParseUserHorizonDays(message string) (days int, recognized bool) {
	cleaned := strings.TrimSpace(horizonQuotedText.ReplaceAllString(message, " "))
	if cleaned == "" {
		return 0, false
	}

	type clause struct {
		text       string
		correction bool
		question   bool
	}
	clauses := make([]clause, 0, 4)
	last := 0
	correction := false
	for _, boundary := range horizonClauseSplit.FindAllStringIndex(cleaned, -1) {
		separator := strings.ToLower(strings.TrimSpace(cleaned[boundary[0]:boundary[1]]))
		part := strings.TrimSpace(cleaned[last:boundary[0]])
		if part != "" {
			clauses = append(clauses, clause{text: part, correction: correction, question: strings.Contains(separator, "?")})
		}
		correction = separator == "actually" || separator == "instead" || separator == "rather"
		last = boundary[1]
	}
	if part := strings.TrimSpace(cleaned[last:]); part != "" {
		clauses = append(clauses, clause{text: part, correction: correction})
	}

	var (
		found          bool
		selectedDays   int
		seenHorizonCtx bool
	)
	for _, item := range clauses {
		text := strings.TrimSpace(item.text)
		if text == "" || item.question || horizonThirdParty.MatchString(text) || horizonHypothetical.MatchString(text) {
			continue
		}
		matches := horizonDurationPattern.FindAllStringSubmatch(text, -1)
		// "I want to hold 15% cash" holds an amount, not for a period: a cash
		// percentage without a duration is an allocation, not a horizon.
		if len(matches) == 0 && horizonCashAmount.MatchString(text) {
			continue
		}
		isRange := horizonRangePattern.MatchString(text)
		contextual := horizonContext.MatchString(text)
		bare := horizonBareAnswer.MatchString(text)
		if isRange && (contextual || item.correction && seenHorizonCtx) {
			found, selectedDays, seenHorizonCtx = true, 0, true
			continue
		}
		if horizonNegatedChoice.MatchString(text) && len(matches) > 0 {
			found, selectedDays, seenHorizonCtx = true, 0, true
			continue
		}
		if !contextual && !bare && (!item.correction || !seenHorizonCtx || len(matches) == 0) {
			continue
		}
		if len(matches) != 1 {
			found, selectedDays = true, 0
			seenHorizonCtx = seenHorizonCtx || contextual
			continue
		}
		normalized, ok := normalizeHorizonDuration(matches[0][1], matches[0][2])
		if !ok {
			found, selectedDays = true, 0
			seenHorizonCtx = seenHorizonCtx || contextual
			continue
		}
		found, selectedDays = true, normalized
		seenHorizonCtx = seenHorizonCtx || contextual
	}
	return selectedDays, found
}

func normalizeHorizonDuration(quantityText, unit string) (int, bool) {
	quantity, err := strconv.ParseFloat(strings.TrimSpace(quantityText), 64)
	if err != nil {
		quantity = map[string]float64{
			"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
			"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11,
			"twelve": 12, "eighteen": 18, "twenty": 20,
		}[strings.ToLower(strings.TrimSpace(quantityText))]
	}
	multiplier := 0.0
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "day", "days":
		multiplier = 1
	case "week", "weeks":
		multiplier = 7
	case "month", "months":
		multiplier = 30
	case "year", "years":
		multiplier = 365
	}
	value := quantity * multiplier
	if multiplier == 0 || quantity <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || value > 3650 {
		return 0, false
	}
	days := int(math.Round(value))
	return days, days >= 1 && days <= 3650
}

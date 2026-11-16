package takeintelligence

import (
	"regexp"
	"strings"
)

var (
	riskStyleToken            = regexp.MustCompile(`(?i)\b(conservative|balanced|aggressive)\b`)
	riskStyleClauseSplit      = regexp.MustCompile(`(?i)\b(but|however|actually|instead|rather)\b|[;.!?\n]+`)
	riskStyleQuotedText       = regexp.MustCompile(`"[^\"]*"|'[^']*'|“[^”]*”|‘[^’]*’`)
	riskStyleThirdParty       = regexp.MustCompile(`(?i)\b(?:my friend|my partner|my spouse|my coworker|my colleague|someone else|another person|they|he|she|friend says|partner says|spouse says)\b`)
	riskStyleHypothetical     = regexp.MustCompile(`(?i)\b(?:if|what if|suppose|imagine|hypothetical(?:ly)?|maybe|perhaps|might|could|whether|for example|quoted)\b`)
	riskStyleMyStatement      = regexp.MustCompile(`(?i)\bmy\s+(?:investment\s+)?risk\s+(?:style|tolerance|profile)\s+(?:is|would be|should be|will be|needs to be)\s+(?:a\s+|an\s+)?(conservative|balanced|aggressive)\b`)
	riskStyleSelfStatement    = regexp.MustCompile(`(?i)\bi\s+(?:am|prefer|choose|want|would choose|would prefer|can tolerate|would tolerate|identify as|consider myself(?: to be)?)\s+(?:a\s+|an\s+)?(conservative|balanced|aggressive)\b`)
	riskStyleNegatedStatement = regexp.MustCompile(`(?i)\b(?:i\s+(?:do not|don't|dont|never|cannot|can't|would not|wouldn't)\s+(?:(?:want|prefer|choose|tolerate|accept|identify as|be)\s+)?|i(?:'m| am)\s+not\s+|my\s+(?:investment\s+)?risk\s+(?:style|tolerance|profile)\s+(?:is|would be|should be|will be)\s+not\s+)(?:a\s+|an\s+)?(?:conservative|balanced|aggressive)\b`)
	riskStyleCustomOrUnclear  = regexp.MustCompile(`(?i)\b(custom|unclear|unsure|not sure|don't know|dont know|do not know|it depends)\b`)
	riskStyleSelfContext      = regexp.MustCompile(`(?i)\b(?:i|my)\b`)
	riskStyleRiskContext      = regexp.MustCompile(`(?i)\brisk\b`)
	riskStyleOtherDomain      = regexp.MustCompile(`(?i)\b(?:growth|conviction|thesis|returns?|sales|business|exposure)\b`)
)

// ParseRiskStyleAnswer accepts a direct user statement of their own risk
// preference. A short answer may omit context only while a risk-style answer
// is explicitly pending. Negated or ambiguous direct answers request
// clarification instead of becoming a confirmed preference.
func ParseRiskStyleAnswer(message string, awaitingAnswer bool) (RiskStyle, bool) {
	if style, ok := parseLocalizedRiskStyleChoice(message); ok {
		return style, true
	}
	cleaned := strings.TrimSpace(riskStyleQuotedText.ReplaceAllString(message, " "))
	if cleaned == "" {
		return RiskStyleUnclear, false
	}
	if awaitingAnswer {
		if style, ok := parseBareRiskAnswer(cleaned); ok {
			return style, true
		}
	}

	type clause struct {
		text       string
		correction bool
		question   bool
	}
	clauses := make([]clause, 0, 4)
	last := 0
	correction := false
	for _, boundary := range riskStyleClauseSplit.FindAllStringIndex(cleaned, -1) {
		separator := strings.ToLower(strings.TrimSpace(cleaned[boundary[0]:boundary[1]]))
		part := strings.TrimSpace(cleaned[last:boundary[0]])
		if part != "" {
			clauses = append(clauses, clause{text: part, correction: correction, question: strings.Contains(separator, "?")})
		}
		correction = separator == "but" || separator == "however" || separator == "actually" || separator == "instead" || separator == "rather"
		last = boundary[1]
	}
	if part := strings.TrimSpace(cleaned[last:]); part != "" {
		clauses = append(clauses, clause{text: part, correction: correction})
	}

	var (
		foundPreference bool
		resolvedStyle   RiskStyle
		seenRiskContext bool
	)
	for _, item := range clauses {
		text := strings.TrimSpace(item.text)
		if text == "" || item.question || riskStyleThirdParty.MatchString(text) || riskStyleHypothetical.MatchString(text) {
			continue
		}
		lower := strings.ToLower(text)
		myStatement := riskStyleMyStatement.FindAllStringSubmatch(lower, -1)
		selfStatement := riskStyleSelfStatement.FindAllStringSubmatch(lower, -1)
		hasRiskContext := riskStyleRiskContext.MatchString(lower)
		otherDomain := riskStyleOtherDomain.MatchString(lower) && !hasRiskContext
		directSelf := len(myStatement) > 0 || (len(selfStatement) > 0 && !otherDomain && (hasRiskContext || awaitingAnswer || item.correction && seenRiskContext))
		if (riskStyleNegatedStatement.MatchString(lower) || riskStyleCustomOrUnclear.MatchString(lower) && riskStyleSelfContext.MatchString(lower) && (hasRiskContext || awaitingAnswer || item.correction && seenRiskContext)) && (hasRiskContext || awaitingAnswer || item.correction && seenRiskContext) {
			foundPreference = true
			resolvedStyle = RiskStyleUnclear
			if riskStyleCustomOrUnclear.MatchString(lower) {
				if regexp.MustCompile(`(?i)\b(custom|it depends)\b`).MatchString(lower) {
					resolvedStyle = RiskStyleCustom
				}
			}
			if hasRiskContext {
				seenRiskContext = true
			}
			continue
		}
		if !directSelf {
			if item.correction && seenRiskContext && !otherDomain {
				matches := riskStyleToken.FindAllString(lower, -1)
				if len(matches) == 1 {
					foundPreference = true
					resolvedStyle = riskStyleFromToken(matches[0])
					continue
				}
			}
			continue
		}
		styles := make([]RiskStyle, 0, len(myStatement)+len(selfStatement))
		for _, match := range myStatement {
			styles = appendUniqueRiskStyle(styles, riskStyleFromToken(match[1]))
		}
		for _, match := range selfStatement {
			styles = appendUniqueRiskStyle(styles, riskStyleFromToken(match[1]))
		}
		foundPreference = true
		allStyleMentions := riskStyleToken.FindAllString(lower, -1)
		switch {
		case len(styles) == 1 && len(allStyleMentions) == 1:
			resolvedStyle = styles[0]
		default:
			resolvedStyle = RiskStyleUnclear
		}
		if hasRiskContext {
			seenRiskContext = true
		}
	}
	if foundPreference {
		return resolvedStyle, true
	}
	return RiskStyleUnclear, false
}

func parseLocalizedRiskStyleChoice(message string) (RiskStyle, bool) {
	switch strings.TrimSpace(message) {
	case "أفضل أسلوبًا محافظًا في المخاطر.", "ฉันต้องการแนวทางความเสี่ยงแบบอนุรักษ์นิยม":
		return RiskStyleConservative, true
	case "أفضل أسلوبًا متوازنًا في المخاطر.", "ฉันต้องการแนวทางความเสี่ยงแบบสมดุล":
		return RiskStyleBalanced, true
	case "أفضل أسلوبًا أكثر جرأة في المخاطر.", "ฉันต้องการแนวทางความเสี่ยงแบบเชิงรุก":
		return RiskStyleAggressive, true
	case "لدي تفضيل مخاطر مخصص. يرجى سؤالي عن تفاصيله.", "ฉันมีความต้องการด้านความเสี่ยงแบบกำหนดเอง โปรดถามรายละเอียดเพิ่มเติม":
		return RiskStyleCustom, true
	case "لست متأكدًا من أسلوب المخاطر الذي أفضله بعد.", "ฉันยังไม่แน่ใจเกี่ยวกับระดับความเสี่ยงที่ต้องการ":
		return RiskStyleUnclear, true
	default:
		return RiskStyleUnclear, false
	}
}

func parseBareRiskAnswer(message string) (RiskStyle, bool) {
	text := strings.TrimSpace(strings.ToLower(message))
	text = strings.Trim(text, ".!,:; \t\r\n")
	switch text {
	case "conservative", "conservative risk", "conservative risk style", "i am conservative", "i'm conservative", "i prefer conservative", "i prefer a conservative", "i prefer conservative risk", "i prefer a conservative risk style":
		return RiskStyleConservative, true
	case "balanced", "balanced risk", "balanced risk style", "i am balanced", "i'm balanced", "i prefer balanced", "i prefer a balanced", "i prefer balanced risk", "i prefer a balanced risk style":
		return RiskStyleBalanced, true
	case "aggressive", "aggressive risk", "aggressive risk style", "i am aggressive", "i'm aggressive", "i prefer aggressive", "i prefer an aggressive", "i prefer aggressive risk", "i prefer an aggressive risk style":
		return RiskStyleAggressive, true
	case "custom", "custom risk", "custom risk style", "it depends":
		return RiskStyleCustom, true
	case "unclear", "unsure", "not sure", "i don't know", "i do not know", "i'm not sure", "i am not sure":
		return RiskStyleUnclear, true
	default:
		return RiskStyleUnclear, false
	}
}

func riskStyleFromToken(token string) RiskStyle {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case string(RiskStyleConservative):
		return RiskStyleConservative
	case string(RiskStyleBalanced):
		return RiskStyleBalanced
	case string(RiskStyleAggressive):
		return RiskStyleAggressive
	default:
		return RiskStyleUnclear
	}
}

func appendUniqueRiskStyle(styles []RiskStyle, style RiskStyle) []RiskStyle {
	for _, existing := range styles {
		if existing == style {
			return styles
		}
	}
	return append(styles, style)
}

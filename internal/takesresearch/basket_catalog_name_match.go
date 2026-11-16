package takesresearch

import (
	"strings"
	"unicode"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

// basketUniqueIssuerNameTokenMatch is a narrow fallback for provider catalog
// rows whose exact market ticker agrees with a verified SEC ticker entry but
// whose display name does not contain the full legal issuer name. It uses only
// the complete SEC index and catalog-owned display/keyword metadata. Planner
// aliases and rationale are deliberately not inputs.
func basketUniqueIssuerNameTokenMatch(instrument takeintelligence.Instrument, issuer secExchangeTicker, entries []secExchangeTicker, completeIndex bool) bool {
	if !completeIndex || len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if canonicalCIK(entry.CIK) == "" || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Ticker) == "" {
			return false
		}
	}
	targetCIK := canonicalCIK(issuer.CIK)
	targetTicker := strings.ToUpper(strings.TrimSpace(issuer.Ticker))
	if targetCIK == "" || targetTicker == "" || strings.ToUpper(marketTicker(instrument.Key)) != targetTicker {
		return false
	}
	if strings.TrimSpace(issuer.Name) == "" {
		return false
	}

	// Reconfirm that the passed identity is an exact ticker/name row from the
	// supplied complete index; do not let a caller-supplied name create proof.
	issuerRowPresent := false
	for _, entry := range entries {
		if canonicalCIK(entry.CIK) == targetCIK &&
			strings.EqualFold(strings.TrimSpace(entry.Ticker), targetTicker) &&
			companyNameMatches(issuer.Name, entry.Name) {
			issuerRowPresent = true
			break
		}
	}
	if !issuerRowPresent {
		return false
	}

	nameTokens := make(map[string]struct{})
	for _, token := range basketIssuerNameTokens(issuer.Name) {
		nameTokens[token] = struct{}{}
	}
	if len(nameTokens) == 0 {
		return false
	}

	// Count distinct issuers, not exchange rows: one company may have more
	// than one SEC ticker/index row. Any shared token is unsuitable evidence.
	owners := make(map[string]map[string]struct{})
	for _, entry := range entries {
		cik := canonicalCIK(entry.CIK)
		if cik == "" || strings.TrimSpace(entry.Name) == "" {
			continue
		}
		for _, token := range basketIssuerNameTokens(entry.Name) {
			if owners[token] == nil {
				owners[token] = make(map[string]struct{})
			}
			owners[token][cik] = struct{}{}
		}
	}

	catalogTokens := make(map[string]struct{})
	for _, value := range append([]string{instrument.DisplayName}, instrument.Keywords...) {
		for _, token := range basketIssuerNameTokens(value) {
			catalogTokens[token] = struct{}{}
		}
	}
	tickerToken := strings.ToLower(targetTicker)
	for token := range catalogTokens {
		if _, issuerToken := nameTokens[token]; !issuerToken || isGenericIssuerNameToken(token) {
			continue
		}
		if token == tickerToken {
			continue
		}
		if len(owners[token]) != 1 {
			continue
		}
		if _, uniquelyOwnedByTarget := owners[token][targetCIK]; uniquelyOwnedByTarget {
			return true
		}
	}
	return false
}

func basketIssuerNameTokens(value string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() == 0 {
			return
		}
		token := strings.ToLower(current.String())
		if len([]rune(token)) >= 5 {
			tokens = append(tokens, token)
		}
		current.Reset()
	}
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			current.WriteRune(r)
		case unicode.IsMark(r) && current.Len() > 0:
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

func isGenericIssuerNameToken(token string) bool {
	switch token {
	case "company", "companies", "corporation", "corporate", "incorporated", "limited", "ltd", "llc", "plc", "group", "groups", "holding", "holdings", "international", "global", "energy", "power", "systems", "services", "solutions", "technology", "technologies", "industries", "industrial", "industrials", "resources", "resource", "partners", "partner", "enterprises", "enterprise", "management", "markets", "market", "financial", "finance", "capital", "american", "national", "united", "general":
		return true
	default:
		return false
	}
}

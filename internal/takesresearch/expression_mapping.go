package takesresearch

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

type secExchangeTicker = IssuerIdentity

type basketResearchSourceCandidate struct {
	ID         string
	IssuerName string
}

type basketResearchIssuer struct {
	CIK          string
	OfficialName string
	SECTicker    string
	RetrievedAt  time.Time
}

// basketResearchIssuerFromIndex accepts a proposed company name only when it
// resolves to exactly one distinct CIK in the complete SEC ticker index.
func basketResearchIssuerFromIndex(candidate basketResearchSourceCandidate, entries []secExchangeTicker, retrievedAt time.Time, _ bool) (basketResearchIssuer, error) {
	issuerName := strings.TrimSpace(candidate.IssuerName)
	if issuerName == "" || len([]rune(issuerName)) > 180 {
		return basketResearchIssuer{}, errors.New("explicit issuer name is missing or exceeds its bound")
	}
	entry, ok := matchNamedIssuer(entries, issuerName)
	if !ok {
		return basketResearchIssuer{}, errors.New("explicit issuer name does not resolve to one unique official SEC issuer")
	}
	cik := canonicalCIK(entry.CIK)
	if cik == "" || strings.TrimSpace(entry.Name) == "" {
		return basketResearchIssuer{}, errors.New("official issuer index returned incomplete identity")
	}
	return basketResearchIssuer{CIK: cik, OfficialName: strings.TrimSpace(entry.Name), SECTicker: strings.ToUpper(strings.TrimSpace(entry.Ticker)), RetrievedAt: retrievedAt.UTC()}, nil
}

// basketResearchCandidateMapping preserves the original SEC ticker/name
// concordance path. It requires a ticker match plus an official issuer-name
// match or a unique, complete-index name token before adding a contract.
func basketResearchCandidateMapping(candidateID string, issuer basketResearchIssuer, entries []secExchangeTicker, snapshot takeintelligence.CatalogSnapshot) CandidateInstrumentMapping {
	mapping := CandidateInstrumentMapping{
		CandidateID: strings.TrimSpace(candidateID), Status: CandidateMappingNoKey,
		CatalogAssessment: CandidateCatalogNotChecked,
		Keys:              []takeintelligence.ProviderInstrumentKey{}, EntityName: strings.TrimSpace(issuer.OfficialName),
	}
	cik := canonicalCIK(issuer.CIK)
	if cik == "" || mapping.CandidateID == "" || mapping.EntityName == "" {
		mapping.Status = CandidateMappingFailed
		return mapping
	}
	tickers := make(map[string]secExchangeTicker)
	for _, entry := range entries {
		if canonicalCIK(entry.CIK) == cik && strings.TrimSpace(entry.Name) != "" && companyNameMatches(issuer.OfficialName, entry.Name) {
			if ticker := strings.ToUpper(strings.TrimSpace(entry.Ticker)); ticker != "" {
				if _, exists := tickers[ticker]; !exists {
					tickers[ticker] = entry
				}
			}
		}
	}
	if len(tickers) == 0 {
		mapping.Status = CandidateMappingFailed
		return mapping
	}
	keys := make(map[string]takeintelligence.ProviderInstrumentKey)
	nameMatches := make(map[string]CandidateCatalogNameMatch)
	tickerSeen, issuerMatched := false, false
	for _, instrument := range snapshot.Instruments {
		ticker := strings.ToUpper(marketTicker(instrument.Key))
		issuerEntry, tickerFound := tickers[ticker]
		if ticker == "" || !tickerFound {
			continue
		}
		tickerSeen = true
		nameMatchMethod := CandidateCatalogNameMatchOfficialConcordance
		if !instrumentIssuerNameMatches(instrument, issuerEntry.Name) {
			if !basketUniqueIssuerNameTokenMatch(instrument, issuerEntry, entries, true) {
				continue
			}
			nameMatchMethod = CandidateCatalogNameMatchUniqueNameToken
		}
		issuerMatched = true
		mapping.CatalogAssessment = CandidateCatalogIssuerMatched
		keyText := instrument.Key.String()
		if keyText != "" {
			previous, exists := nameMatches[keyText]
			if !exists || (previous.Method == CandidateCatalogNameMatchUniqueNameToken && nameMatchMethod == CandidateCatalogNameMatchOfficialConcordance) {
				nameMatches[keyText] = CandidateCatalogNameMatch{Key: instrument.Key, Method: nameMatchMethod}
			}
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.Category), "stocks") ||
			instrument.ListingStatus != takeintelligence.ListingObserved ||
			instrument.Key.Provider != "hyperliquid" || instrument.Key.Product != "perpetual" ||
			!strings.EqualFold(strings.TrimSpace(instrument.MarketType), "perpetual") {
			continue
		}
		if keyText == "" {
			continue
		}
		if _, exists := keys[keyText]; exists {
			continue
		}
		keys[keyText] = instrument.Key
		mapping.ReviewedMappings = append(mapping.ReviewedMappings, basketResearchReviewedMapping(instrument, issuerEntry, issuer))
	}
	if !issuerMatched {
		if tickerSeen {
			mapping.CatalogAssessment = CandidateCatalogTickerMatchUnverified
		} else if snapshot.Result == takeintelligence.SnapshotComplete {
			mapping.CatalogAssessment = CandidateCatalogAbsent
		}
	}
	for _, key := range keys {
		mapping.Keys = append(mapping.Keys, key)
	}
	slices.SortFunc(mapping.Keys, func(left, right takeintelligence.ProviderInstrumentKey) int {
		return strings.Compare(left.String(), right.String())
	})
	for _, match := range nameMatches {
		mapping.CatalogNameMatches = append(mapping.CatalogNameMatches, match)
	}
	slices.SortFunc(mapping.CatalogNameMatches, func(left, right CandidateCatalogNameMatch) int {
		return strings.Compare(left.Key.String(), right.Key.String())
	})
	if len(mapping.Keys) > 0 {
		mapping.Status = CandidateMappingMapped
		mapping.ExposureKey = "equity:cik:" + cik
		slices.SortFunc(mapping.ReviewedMappings, func(left, right takeintelligence.UnderlyingMapping) int {
			return strings.Compare(left.Key.String(), right.Key.String())
		})
	}
	return mapping
}

func basketResearchReviewedMapping(instrument takeintelligence.Instrument, indexEntry secExchangeTicker, issuer basketResearchIssuer) takeintelligence.UnderlyingMapping {
	cik := canonicalCIK(issuer.CIK)
	ticker := strings.ToUpper(marketTicker(instrument.Key))
	marketSourceID := "market-listing:" + instrument.Key.String()
	identityRefs := []takeintelligence.SourceLocator{{
		Provider: "sec-edgar", SourceID: "ticker-exchange-index:" + ticker, URL: secCompanyTickerURL,
		Class: takeintelligence.SourceOfficialPrimary, Visibility: takeintelligence.VisibilityPublic,
		DisplayPermission: takeintelligence.DisplayPermissionAllowed,
	}}
	return takeintelligence.UnderlyingMapping{
		Key: instrument.Key, Status: takeintelligence.MappingReviewed, EntityKind: takeintelligence.EntityCompany,
		EntityKey: "sec:cik:" + cik, EntityName: strings.TrimSpace(indexEntry.Name),
		IssuerCIK: strings.TrimLeft(cik, "0"), SecurityKey: "sec:ticker:" + ticker + ":cik:" + cik,
		ExposureKey: "equity:cik:" + cik,
		MarketSpecRefs: []takeintelligence.SourceLocator{{
			Provider: "hyperliquid", SourceID: marketSourceID, URL: "https://api.hyperliquid.xyz/info",
			Class: takeintelligence.SourceVenuePrimary, Visibility: takeintelligence.VisibilityPublic,
			DisplayPermission: takeintelligence.DisplayPermissionAllowed,
		}},
		IdentityRefs: identityRefs,
		Basis:        fmt.Sprintf("The active Hyperliquid stock-category instrument ticker %s and issuer name matched one unique SEC company-ticker-exchange entry for %s retrieved %s. The stable issuer key is its SEC CIK. This catalog row represents a perpetual contract, not common shares or proof of account access.", ticker, strings.TrimSpace(indexEntry.Name), issuer.RetrievedAt.UTC().Format(time.DateOnly)),
		Reviewer:     "takesresearch:basket-sec-ticker-name-concordance-v1", Version: "basket-sec-ticker-name-concordance-v1",
		VerifiedAt: issuer.RetrievedAt.UTC(),
	}
}

func matchNamedIssuer(entries []secExchangeTicker, name string) (secExchangeTicker, bool) {
	var matches []secExchangeTicker
	seen := map[string]bool{}
	for _, entry := range entries {
		cik := canonicalCIK(entry.CIK)
		if cik != "" && companyNameMatches(name, entry.Name) && !seen[cik] {
			seen[cik] = true
			matches = append(matches, entry)
		}
	}
	if len(matches) != 1 {
		return secExchangeTicker{}, false
	}
	return matches[0], true
}

func instrumentIssuerNameMatches(instrument takeintelligence.Instrument, issuer string) bool {
	if companyNameMatches(instrument.DisplayName, issuer) {
		return true
	}
	for _, keyword := range instrument.Keywords {
		if companyNameMatches(keyword, issuer) {
			return true
		}
	}
	return false
}

func companyNameMatches(explicit, officialName string) bool {
	normalize := func(name string) string {
		name = strings.ToLower(name)
		if strings.HasSuffix(name, " n.v.") {
			name = strings.TrimSuffix(name, " n.v.") + " nv"
		}
		words := strings.Fields(strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' {
				return r
			}
			return ' '
		}, name))
		for len(words) > 0 {
			switch words[len(words)-1] {
			case "incorporated", "inc", "corporation", "corp", "company", "co", "limited", "ltd":
				words = words[:len(words)-1]
			default:
				return strings.Join(words, " ")
			}
		}
		return ""
	}
	want, got := normalize(explicit), normalize(officialName)
	return want != "" && got != "" && (want == got || strings.HasPrefix(got, want+" ") || strings.HasPrefix(want, got+" "))
}

func canonicalCIK(raw string) string {
	value := strings.TrimSpace(raw)
	if !allDigits(value) {
		return ""
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}
	if len(value) > 10 {
		return ""
	}
	return fmt.Sprintf("%010s", value)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func marketTicker(key takeintelligence.ProviderInstrumentKey) string {
	if key.Venue == "" || !strings.HasPrefix(key.Symbol, key.Venue+":") {
		return ""
	}
	ticker := strings.TrimPrefix(key.Symbol, key.Venue+":")
	if ticker == "" || strings.Contains(ticker, ":") {
		return ""
	}
	return ticker
}

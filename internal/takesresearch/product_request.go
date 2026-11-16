package takesresearch

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ProductClass is an instrument type a user can ask for by name. It is
// compared with catalog MarketType values, so a product is available only
// when a connected adapter lists it; a provider's brand establishes nothing.
type ProductClass string

const (
	ProductPerpetual    ProductClass = "perpetual"
	ProductShares       ProductClass = "shares"
	ProductSpot         ProductClass = "spot"
	ProductOptions      ProductClass = "options"
	ProductEventOutcome ProductClass = "event_outcome"
)

// LeverageIntent is a stated leverage preference. It is not a product
// choice: "no leverage" says nothing about shares or perpetual contracts.
type LeverageIntent string

const (
	LeverageNone    LeverageIntent = "none"
	LeverageAllowed LeverageIntent = "allowed"
)

// ProductRequest is what the user said about products, venues, and leverage.
// A requested product and refused products are separate fields, as are the
// requested venue and refused venues, so one never stands in for the other.
// Direct is false for an informational question, quoted or hypothetical
// text, or someone else's preference.
type ProductRequest struct {
	Product       ProductClass   `json:"product,omitempty"`
	Refused       []ProductClass `json:"refused,omitempty"`
	Venue         string         `json:"venue,omitempty"`
	RefusedVenues []string       `json:"refusedVenues,omitempty"`
	Leverage      LeverageIntent `json:"leverage,omitempty"`
	Direct        bool           `json:"direct"`
}

const (
	perpetualTerm = `(?:perps?|perpetuals?|perpetual\s+(?:contracts?|futures|swaps?))`
	venueTerm     = `(robinhood|polymarket|kalshi|hyperliquid)`
	acceptedTerm  = `(?:fine|ok|okay|good|acceptable)`
	spotTerm      = `spot(?:\s+(?:markets?|trading|tokens?|coins?|assets?|positions?|holdings?|exposure))?`
	// A request has to end its clause or lead into a closing word, so "I just
	// spot a trend" names no product.
	requestEnd = `(?:\s*[,.;!?]|\s*$|\s+(?:please|instead|rather|not|for\s+me)\b)`
	// "I want spot assets" asks for spot. Only the user's own first-person want,
	// need, or preference counts, and it has to end the request or lead into a
	// preposition, so "my friend wants spot assets" and "I want spot prices" ask
	// for nothing.
	spotPreference = `\b(?:i|we)(?:\s+(?:really|just|only))?(?:\s+(?:want|need|prefer)|(?:\s+would|'d|’d)\s+(?:prefer|like))\s+(?:to\s+(?:buy|own|hold|trade|use)\s+)?(?:only\s+|just\s+)?(?:the\s+)?` + spotTerm + `(?:` + requestEnd + `|\s+(?:in|on|for|over|to|from|through|via|with|only)\b)`
)

var (
	productSentencePattern = regexp.MustCompile(`[^.!?;\n]+[.!?;\n]*`)
	// A negation reaches only its own clause: "No leverage, just shares"
	// refuses nothing about shares.
	productClausePattern = regexp.MustCompile(`(?i),|\bbut\b|\band\b|\bthough\b|\bwhile\b`)
	negationPattern      = regexp.MustCompile(`(?i)\b(?:no|not|never|without|avoid|avoiding|excluding|except|nothing)\b|n't\b|n’t\b|\bdo\s+not\b|\brather\s+not\b|\banything\s+but\b`)
	// These idioms agree rather than refuse, so "no problem with only shares"
	// still asks for shares.
	affirmingNegationPattern = regexp.MustCompile(`(?i)\bno\s+(?:problems?|issues?|objections?|concerns?|complaints?|worries)\b|\bnothing\s+wrong\b|\bnot\s+an?\s+(?:problem|issue)\b`)

	perpetualAcceptancePattern = regexp.MustCompile(`(?i)\b` + perpetualTerm + `\s+(?:are|is|would\s+be|seem|seems)\s+(?:not\s+)?` + acceptedTerm + `\b|\b` + perpetualTerm + `\s+(?:aren't|isn't|aren’t|isn’t)\s+` + acceptedTerm + `\b|\b` + perpetualTerm + `\s+(?:do\s+not\s+|don't\s+|don’t\s+)?work\b|\b(?:use|using|go\s+with|build\s+with|fine\s+with|ok\s+with|okay\s+with|happy\s+with)\s+(?:the\s+)?` + perpetualTerm + `\b`)
	// A judgment after the term, a personal "not for me", or the user's own
	// dislike refuses perpetual contracts: "perps are no good", "perps aren't for
	// me", "I hate perps". "Perps are risky" alone is an observation.
	perpetualDisapprovalPattern = regexp.MustCompile(`(?i)\b` + perpetualTerm + `\s+(?:are|is|would\s+be|seem|seems|sound|sounds)\s+(?:no\s+good|(?:a\s+)?bad(?:\s+idea)?|terrible|awful|too\s+risky)\b|\b` + perpetualTerm + `\s+(?:just\s+|really\s+)?(?:are\s+not|is\s+not|aren't|aren’t|isn't|isn’t)\s+(?:really\s+)?for\s+(?:me|us)\b|\b(?:i|we)\s+(?:really\s+|just\s+)?(?:hate|dislike|detest|loathe)\s+(?:the\s+|all\s+|any\s+)?` + perpetualTerm + `\b`)
	// Derivatives and futures include perpetual contracts, so refusing them
	// refuses perpetual contracts too.
	perpetualMentionPattern  = regexp.MustCompile(`(?i)\b(?:` + perpetualTerm + `|derivatives?|futures)\b`)
	derivativeMentionPattern = regexp.MustCompile(`(?i)\bderivatives?\b`)
	// "Only shares" must end the clause or lead into its object, so "it just
	// shares a supplier" is not a request.
	sharesPattern       = regexp.MustCompile(`(?i)\b(?:only|just)\s+(?:(?:buy|own|hold|use|want)\s+(?:to\s+(?:buy|own|hold)\s+)?)?(?:the\s+)?(?:actual\s+|real\s+)?shares(?:\s*[,.;!?]|\s*$|\s+(?:of|in|on|and|not|please|instead|rather)\b)|\bshares\s+only\b|\b(?:buy|own|hold|want|prefer|use)\s+(?:the\s+)?(?:(?:actual|real)\s+(?:shares|stocks?|equit(?:y|ies))|cash\s+equit(?:y|ies))\b|\b(?:want|prefer|need)\s+to\s+(?:actually\s+|really\s+)?own\s+(?:the\s+)?(?:actual\s+)?(?:shares|stocks?)\b`)
	spotPattern         = regexp.MustCompile(`(?i)\b(?:only|just)\s+(?:the\s+)?` + spotTerm + requestEnd + `|\b` + spotTerm + `\s+only` + requestEnd + `|` + spotPreference)
	optionsPattern      = regexp.MustCompile(`(?i)\b(?:call|put)\s+options?\b|\boptions?\s+contracts?\b|\b(?:buy|sell|trade|write)\s+(?:some\s+)?(?:calls|puts|options)\b`)
	eventOutcomePattern = regexp.MustCompile(`(?i)\b(?:buy|sell|trade|bet\s+on|use|via|through|with)\s+(?:a\s+|an\s+|some\s+)?(?:prediction[\s-]+markets?(?:\s+(?:contracts?|shares|positions?))?|event\s+(?:contracts?|markets?))\b|\b(?:buy|sell|trade)\s+(?:yes|no)\s+(?:shares|contracts)\b|\bbinary\s+(?:options|contracts?)\b`)
	// A venue needs a trading verb and a venue preposition, an "only through"
	// restriction, or an account: "invest in Robinhood" or "a basket with
	// Robinhood" names the listed company.
	venuePattern             = regexp.MustCompile(`(?i)\b(?:buy|sell|trade|invest|place|execute|build)\b[^.!?;\n]{0,40}?\b(?:on|through|via)\s+(?:my\s+)?` + venueTerm + `\b|\b(?:use|using)\s+(?:my\s+)?` + venueTerm + `\b|\bonly\s+(?:on|through|via|with|at)\s+(?:my\s+)?` + venueTerm + `\b|\b(?:in|from)\s+my\s+` + venueTerm + `\s+account\b|\b` + venueTerm + `\s+(?:is|would\s+be)\s+(?:not\s+)?` + acceptedTerm + `\b`)
	noLeveragePattern        = regexp.MustCompile(`(?i)\b(?:no|without|zero)\s+leverage\b|\bun-?lever(?:ed|aged)\b|\bnon-?leveraged\b|\b(?:don't|dont|don’t|do\s+not)\s+(?:want|use)\s+(?:any\s+)?leverage\b|\bavoid\s+leverage\b`)
	leverageAcceptance       = regexp.MustCompile(`(?i)\bleverage\s+(?:is|would\s+be)\s+(?:not\s+)?` + acceptedTerm + `\b|\bleverage\s+(?:isn't|isn’t)\s+` + acceptedTerm + `\b`)
	actionableQuestion       = regexp.MustCompile(`(?i)^\W*(?:please\s+)?(?:can|could|would|will)\s+(?:you|we)\s+(?:please\s+)?(?:build|make|create|put\s+together|assemble|construct|set\s+up|pick|choose|select|use|do)\b`)
	productHypothetical      = regexp.MustCompile(`(?i)\b(?:what\s+if|if\s+i|if\s+we|if\s+you|in\s+theory)\b`)
	productThirdPartyPattern = regexp.MustCompile(`(?i)\b(?:my|a)\s+(?:friend|partner|spouse|wife|husband|coworker|colleague|client|advisor|adviser)\b|\bsomeone else\b|\banother person\b|^\W*(?:he|she|they)\s+(?:only\s+)?(?:wants?|likes?|buys?|prefers?|says?|said|thinks?|holds?|owns?|trades?|uses?)\b`)
)

// RequestedProduct reads one user message. Sentences and their clauses apply
// in order through Supersede.
func RequestedProduct(message string) (ProductRequest, bool) {
	var direct, indirect ProductRequest
	directFound, indirectFound := false, false
	for _, sentence := range productSentencePattern.FindAllString(message, -1) {
		next, ok := sentenceProductTerms(sentence)
		if !ok {
			continue
		}
		if productRequestDirect(sentence) {
			direct, directFound = direct.Supersede(next), true
		} else {
			indirect, indirectFound = indirect.Supersede(next), true
		}
	}
	switch {
	case directFound:
		direct.Direct = true
		return direct, true
	case indirectFound:
		indirect.Direct = false
		return indirect, true
	}
	return ProductRequest{}, false
}

func sentenceProductTerms(sentence string) (ProductRequest, bool) {
	var request ProductRequest
	found := false
	start := 0
	bounds := append(productClausePattern.FindAllStringIndex(sentence, -1), []int{len(sentence), len(sentence)})
	for _, bound := range bounds {
		if terms, ok := clauseProductTerms(sentence[start:bound[0]]); ok {
			request, found = request.Supersede(terms), true
		}
		start = bound[1]
	}
	return request, found
}

// clauseProductTerms reads one clause. A product or venue named after a
// negation in the clause is refused, not requested; "I don't think perps are
// fine" refuses perpetual contracts.
func clauseProductTerms(clause string) (ProductRequest, bool) {
	var request ProductRequest
	found := false
	negatedBefore := func(index int) bool { return negated(clause[:index]) }
	judged := false
	for _, match := range perpetualAcceptancePattern.FindAllStringIndex(clause, -1) {
		judged, found = true, true
		if negatedBefore(match[1]) {
			request.Refused = appendProduct(request.Refused, ProductPerpetual)
		} else {
			request.Product = ProductPerpetual
		}
	}
	for _, match := range perpetualDisapprovalPattern.FindAllStringIndex(clause, -1) {
		// "I don't think perps are bad" refuses nothing, and accepts nothing
		// either.
		judged = true
		if !negatedBefore(match[0]) {
			found = true
			request.Refused = appendProduct(request.Refused, ProductPerpetual)
		}
	}
	if !judged {
		for _, match := range perpetualMentionPattern.FindAllStringIndex(clause, -1) {
			if !negatedBefore(match[0]) {
				continue
			}
			found = true
			request.Refused = appendProduct(request.Refused, ProductPerpetual)
			if derivativeMentionPattern.MatchString(clause[match[0]:match[1]]) {
				request.Refused = appendProduct(request.Refused, ProductOptions)
				request.Refused = appendProduct(request.Refused, ProductEventOutcome)
			}
		}
	}
	for _, candidate := range []struct {
		class   ProductClass
		pattern *regexp.Regexp
	}{{ProductShares, sharesPattern}, {ProductSpot, spotPattern}, {ProductOptions, optionsPattern}, {ProductEventOutcome, eventOutcomePattern}} {
		for _, match := range candidate.pattern.FindAllStringIndex(clause, -1) {
			found = true
			if negatedBefore(match[0]) {
				request.Refused = appendProduct(request.Refused, candidate.class)
				continue
			}
			request.Product = candidate.class
		}
	}
	for _, match := range venuePattern.FindAllStringSubmatchIndex(clause, -1) {
		venue := ""
		for group := 2; group+1 < len(match); group += 2 {
			if match[group] >= 0 {
				venue = strings.ToLower(clause[match[group]:match[group+1]])
			}
		}
		found = true
		if negatedBefore(match[1]) {
			request.RefusedVenues = appendVenue(request.RefusedVenues, venue)
			continue
		}
		request.Venue = venue
	}
	switch {
	case noLeveragePattern.MatchString(clause):
		request.Leverage, found = LeverageNone, true
	case leverageAcceptance.MatchString(clause):
		request.Leverage, found = LeverageAllowed, true
		if negated(clause) {
			request.Leverage = LeverageNone
		}
	}
	return request, found
}

func negated(text string) bool {
	return negationPattern.MatchString(affirmingNegationPattern.ReplaceAllString(text, " "))
}

// productRequestDirect keeps informational questions, quoted or hypothetical
// text, and someone else's preference from becoming the user's constraint. A
// question that asks the assistant to build or use something is an
// instruction.
func productRequestDirect(sentence string) bool {
	if containsQuoteDelimiter(sentence) || allocationUncertaintyPattern.MatchString(sentence) ||
		productHypothetical.MatchString(sentence) || productThirdPartyPattern.MatchString(sentence) {
		return false
	}
	return !strings.Contains(sentence, "?") || actionableQuestion.MatchString(sentence)
}

// Supersede applies a newer request field by field. A requested product
// replaces the requested product and lifts only its own refusal; a refusal
// adds to the refusals and withdraws only that product's request. Venues
// follow the same rule, and leverage changes only when it is stated again.
func (r ProductRequest) Supersede(next ProductRequest) ProductRequest {
	merged := ProductRequest{
		Product: r.Product, Refused: slices.Clone(r.Refused), Venue: r.Venue,
		RefusedVenues: slices.Clone(r.RefusedVenues), Leverage: r.Leverage, Direct: next.Direct,
	}
	for _, product := range next.Refused {
		if merged.Product == product {
			merged.Product = ""
		}
		merged.Refused = appendProduct(merged.Refused, product)
	}
	if next.Product != "" {
		merged.Product = next.Product
		merged.Refused = slices.DeleteFunc(merged.Refused, func(product ProductClass) bool { return product == next.Product })
	}
	for _, venue := range next.RefusedVenues {
		if merged.Venue == venue {
			merged.Venue = ""
		}
		merged.RefusedVenues = appendVenue(merged.RefusedVenues, venue)
	}
	if next.Venue != "" {
		merged.Venue = next.Venue
		merged.RefusedVenues = slices.DeleteFunc(merged.RefusedVenues, func(venue string) bool { return venue == next.Venue })
	}
	if next.Leverage != "" {
		merged.Leverage = next.Leverage
	}
	if len(merged.Refused) == 0 {
		merged.Refused = nil
	}
	if len(merged.RefusedVenues) == 0 {
		merged.RefusedVenues = nil
	}
	return merged
}

// Constrains reports whether the request limits products or venues; a
// leverage preference alone does not.
func (r ProductRequest) Constrains() bool {
	return r.Product != "" || len(r.Refused) > 0 || r.Venue != "" || len(r.RefusedVenues) > 0
}

// standingProductRequest folds the build's user messages, oldest first, into
// the request still in force. verified is false when the history cannot be
// checked, so an unknown instruction is never treated as no instruction.
func standingProductRequest(input Input) (request ProductRequest, verified bool) {
	if !input.UserMessagesLoaded || !input.UserMessagesComplete || input.BuildCreatedAt.IsZero() || !validSizingMessageHistory(input) {
		return ProductRequest{}, false
	}
	messages := slices.Clone(input.UserMessages)
	slices.SortFunc(messages, func(left, right PersistedUserMessage) int {
		if compared := left.CreatedAt.Compare(right.CreatedAt); compared != 0 {
			return compared
		}
		return strings.Compare(left.ID, right.ID)
	})
	for _, message := range messages {
		if next, ok := RequestedProduct(message.Content); ok && next.Direct {
			request = request.Supersede(next)
		}
	}
	return request, true
}

// Shortfall says why the connected catalog cannot meet the request, or
// returns "" when it can.
func (r ProductRequest) Shortfall(catalog CatalogContext) string {
	provider, listed := capitalized(catalog.Provider), listedProducts(catalog)
	switch {
	case r.Venue != "" && !strings.EqualFold(r.Venue, catalog.Provider):
		return fmt.Sprintf("You asked to use %s, but the connected catalog has only %s %s", capitalized(r.Venue), provider, listed)
	case r.refusesVenue(catalog.Provider):
		return fmt.Sprintf("You ruled out %s, but the connected catalog has only %s %s", provider, provider, listed)
	case r.Product != "" && !slices.Contains(catalog.ProductTypes, string(r.Product)):
		return fmt.Sprintf("You asked for %s, but the connected %s catalog lists only %s", productLabel(string(r.Product)), provider, listed)
	case len(catalog.ProductTypes) > 0 && !slices.ContainsFunc(catalog.ProductTypes, func(productType string) bool {
		return !slices.Contains(r.Refused, ProductClass(productType))
	}):
		return fmt.Sprintf("You did not accept %s, but the connected %s catalog lists only %s", listed, provider, listed)
	}
	return ""
}

// UnmetChoice is the one option a conversation offers when Shortfall is not
// empty: consent to the connected venue, or to the products it lists.
type UnmetChoice struct {
	Label, Role, Description string
}

// Unmet is the server's reply to a request the catalog cannot meet, with the
// choice that lets the user continue. ok is false when nothing is unmet.
func (r ProductRequest) Unmet(catalog CatalogContext) (reply string, choice UnmetChoice, ok bool) {
	shortfall := r.Shortfall(catalog)
	if shortfall == "" {
		return "", UnmetChoice{}, false
	}
	provider, listed := capitalized(catalog.Provider), listedProducts(catalog)
	if r.Venue != "" && !strings.EqualFold(r.Venue, catalog.Provider) || r.refusesVenue(catalog.Provider) {
		return fmt.Sprintf("%s. I won't build a basket unless you choose %s.", shortfall, provider),
			UnmetChoice{Label: provider + " is fine", Role: "accept_connected_venue", Description: fmt.Sprintf("Continue this idea with the connected %s catalog of %s.", provider, listed)}, true
	}
	reply = fmt.Sprintf("%s. I won't build a basket unless you choose %s.", shortfall, listed)
	description := fmt.Sprintf("Continue this idea with %s %s.", provider, listed)
	if slices.Equal(catalog.ProductTypes, []string{string(ProductPerpetual)}) {
		reply += " They give price exposure, not ownership of the underlying asset."
		description += " They give price exposure, not ownership of the underlying asset."
	}
	return reply, UnmetChoice{Label: capitalized(listed) + " are fine", Role: "accept_listed_product", Description: description}, true
}

func (r ProductRequest) refusesVenue(venue string) bool {
	return slices.ContainsFunc(r.RefusedVenues, func(refused string) bool { return strings.EqualFold(refused, venue) })
}

func listedProducts(catalog CatalogContext) string {
	labels := make([]string, 0, len(catalog.ProductTypes))
	for _, productType := range catalog.ProductTypes {
		labels = append(labels, productLabel(productType))
	}
	if listed := joinPhrases(labels); listed != "" {
		return listed
	}
	return "no instruments"
}

func appendProduct(products []ProductClass, product ProductClass) []ProductClass {
	if slices.Contains(products, product) {
		return products
	}
	return append(products, product)
}

func appendVenue(venues []string, venue string) []string {
	if venue == "" || slices.Contains(venues, venue) {
		return venues
	}
	return append(venues, venue)
}

func productLabel(productType string) string {
	switch ProductClass(productType) {
	case ProductPerpetual:
		return "perpetual contracts"
	case ProductSpot:
		return "spot assets"
	case ProductEventOutcome:
		return "event contracts"
	}
	return productType
}

func instrumentNoun(product string) string {
	if ProductClass(product) == ProductPerpetual {
		return "perpetual contract"
	}
	return product
}

func capitalized(value string) string {
	if value == "" {
		return value
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

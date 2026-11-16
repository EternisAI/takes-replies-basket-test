package takesreply

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
	"golang.org/x/net/html"
)

const (
	stockAnalysisMaxPages       = 2
	stockAnalysisMaxPageBytes   = 2 << 20
	stockAnalysisRequestTimeout = 5 * time.Second
	stockAnalysisStageTimeout   = 6 * time.Second
)

var (
	stockAnalysisPathPattern                      = regexp.MustCompile(`^/stocks/([a-z0-9.-]+)/(statistics|forecast)/?$`)
	stockAnalysisDatePattern                      = regexp.MustCompile(`(?i)\bLast updated:\s*([A-Za-z]+\s+\d{1,2},?\s+\d{4})`)
	stockAnalysisDataPattern                      = regexp.MustCompile(`(?i)\bData Sources?:\s*(.{1,160}?)\s+Last updated:`)
	stockAnalysisFinancialForecastProviderPattern = regexp.MustCompile(`(?i)\bfinancial forecasts are provided by\s+([^.;!?]{2,100})`)
	stockAnalysisNumber                           = regexp.MustCompile(`^[+-]?(?:\d+(?:,\d{3})*(?:\.\d+)?|\d*\.\d+)%?$`)
	stockAnalysisTitleSuffixPattern               = regexp.MustCompile(`(?i)\b(?:stock|forecast|statistics|financials|quote|price|target|analysis|overview|profile|chart)\b`)
	stockAnalysisTickerParentheticalPattern       = regexp.MustCompile(`\s*\([A-Z0-9.-]{1,10}\)`)
)

type stockAnalysisCandidate struct {
	slug    string
	subject string
	title   string
}

type stockAnalysisPage struct {
	URL                string
	Title              string
	DataSource         string
	PageSummary        string
	PageUpdated        string
	RetrievedAt        time.Time
	PageKind           StockAnalysisPageKind
	PageSnapshotStatus string
	FiscalValues       []stockAnalysisFact
}

type stockAnalysisFact struct {
	Claim                   string
	Value                   string
	Unit                    string
	MetricKind              EvidenceMetricKind
	BasisKind               EvidenceBasisKind
	BasisLabel              string
	AttributionKind         EvidenceAttributionKind
	AttributionLabel        string
	ForecastTargetPeriodEnd time.Time
}

type stockAnalysisRow struct {
	Cells []string
}

type stockAnalysisTable struct {
	Rows []stockAnalysisRow
}

func stockAnalysisCandidateFromURL(rawURL, subject, citationTitle, brief string) (stockAnalysisCandidate, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" ||
		!strings.EqualFold(parsed.Hostname(), "stockanalysis.com") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return stockAnalysisCandidate{}, false
	}
	matches := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(parsed.EscapedPath()))
	if len(matches) != 3 || !subjectMatchesBrief(subject, strings.ToLower(brief), citationTitle+" "+subject) {
		return stockAnalysisCandidate{}, false
	}
	return stockAnalysisCandidate{slug: matches[1], subject: strings.TrimSpace(subject), title: cleanText(citationTitle, 180)}, true
}

func stockAnalysisCandidateFromAnnotation(rawURL, citationTitle, citationContent, brief string) (stockAnalysisCandidate, bool) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" ||
		!strings.EqualFold(parsed.Hostname(), "stockanalysis.com") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return stockAnalysisCandidate{}, false
	}
	if matches := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(parsed.EscapedPath())); len(matches) != 3 {
		return stockAnalysisCandidate{}, false
	}

	issuerPrefix := stockAnalysisIssuerTitlePrefix(citationTitle)
	if issuerPrefix == "" || !subjectMatchesBrief(issuerPrefix, strings.ToLower(brief), citationTitle+" "+citationContent) {
		return stockAnalysisCandidate{}, false
	}
	return stockAnalysisCandidateFromURL(rawURL, issuerPrefix, citationTitle, brief)
}

func stockAnalysisIssuerTitlePrefix(title string) string {
	title = cleanText(title, 180)
	if title == "" {
		return ""
	}
	if index := stockAnalysisTitleSuffixPattern.FindStringIndex(title); index != nil {
		title = title[:index[0]]
	}
	title = stockAnalysisTickerParentheticalPattern.ReplaceAllString(title, " ")
	title = strings.TrimSpace(strings.Trim(title, " -—|,.:;"))
	return strings.TrimSpace(title)
}

func stockAnalysisURLMatches(parsed *url.URL, expectedSlug, expectedKind string) bool {
	if parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Host, "stockanalysis.com") ||
		parsed.User != nil || parsed.Port() != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	matches := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(parsed.EscapedPath()))
	return len(matches) == 3 && matches[1] == expectedSlug && matches[2] == expectedKind
}

func stockAnalysisSameCanonicalPath(previous, next *url.URL) bool {
	if previous == nil || next == nil {
		return false
	}
	left := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(previous.EscapedPath()))
	right := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(next.EscapedPath()))
	return len(left) == 3 && len(right) == 3 && left[1] == right[1] && left[2] == right[2]
}

func enrichStockAnalysisEvidenceWithTransport(ctx context.Context, check EvidenceCheckOutput, candidate stockAnalysisCandidate, brief string, asOf time.Time, transport http.RoundTripper) EvidenceCheckOutput {
	if candidate.slug == "" || candidate.subject == "" {
		return check
	}
	stageCtx, cancel := context.WithTimeout(ctx, stockAnalysisStageTimeout)
	defer cancel()
	pageKinds := []string{"statistics", "forecast"}
	results := make(chan stockAnalysisPageResult, stockAnalysisMaxPages)
	for _, pageKind := range pageKinds[:stockAnalysisMaxPages] {
		pageKind := pageKind
		pageURL := "https://stockanalysis.com/stocks/" + candidate.slug + "/" + pageKind + "/"
		go func() {
			client := stockAnalysisHTTPClient(transport, candidate.slug, pageKind)
			page, err := fetchStockAnalysisPage(stageCtx, client, pageURL, candidate, brief, asOf)
			results <- stockAnalysisPageResult{page: page, err: err}
		}()
	}
	pages := make(map[string]stockAnalysisPage, stockAnalysisMaxPages)
	for range pageKinds[:stockAnalysisMaxPages] {
		result := <-results
		if result.err == nil {
			pages[string(result.page.PageKind)] = result.page
		}
	}
	for _, pageKind := range pageKinds[:stockAnalysisMaxPages] {
		if page, ok := pages[pageKind]; ok {
			appendStockAnalysisPage(&check, page)
		}
	}
	return check
}

type stockAnalysisPageResult struct {
	page stockAnalysisPage
	err  error
}

func stockAnalysisHTTPClient(transport http.RoundTripper, expectedSlug, expectedKind string) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Transport: transport,
		Timeout:   stockAnalysisRequestTimeout,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 2 || request.URL == nil || !strings.EqualFold(request.URL.Scheme, "https") ||
				!strings.EqualFold(request.URL.Hostname(), "stockanalysis.com") || request.URL.Port() != "" ||
				request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" ||
				!stockAnalysisURLMatches(request.URL, expectedSlug, expectedKind) || len(via) == 0 || via[len(via)-1].URL == nil ||
				!stockAnalysisSameCanonicalPath(via[len(via)-1].URL, request.URL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func fetchStockAnalysisPage(ctx context.Context, client *http.Client, rawURL string, candidate stockAnalysisCandidate, brief string, asOf time.Time) (stockAnalysisPage, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "stockanalysis.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return stockAnalysisPage{}, fmt.Errorf("unsafe StockAnalysis URL")
	}
	pathMatch := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(parsed.EscapedPath()))
	if len(pathMatch) != 3 || pathMatch[1] != candidate.slug || !stockAnalysisURLMatches(parsed, candidate.slug, pathMatch[2]) {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis URL is outside the selected issuer")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return stockAnalysisPage{}, err
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", "Axion Takes public research preview")
	response, err := client.Do(request)
	if err != nil {
		return stockAnalysisPage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page was not a successful HTML response")
	}
	finalURL := parsed
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL
	}
	if !stockAnalysisURLMatches(finalURL, candidate.slug, pathMatch[2]) {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis redirect changed the selected issuer or page")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, stockAnalysisMaxPageBytes+1))
	if err != nil || len(body) > stockAnalysisMaxPageBytes {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page exceeded the response limit")
	}
	page, err := parseStockAnalysisPage(body, finalURL.String(), pathMatch[2], candidate, brief, asOf, clock.Now().UTC())
	if err != nil {
		return stockAnalysisPage{}, err
	}
	return page, nil
}

func parseStockAnalysisPage(body []byte, pageURL, pageKind string, candidate stockAnalysisCandidate, brief string, asOf, retrievedAt time.Time) (stockAnalysisPage, error) {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return stockAnalysisPage{}, fmt.Errorf("parse StockAnalysis page: %w", err)
	}
	title := firstNodeText(document, "title")
	h1 := firstNodeText(document, "h1")
	if !stockAnalysisPageIdentityMatches(title, h1, candidate, brief, pageKind) {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page identity did not match the selected company")
	}
	visibleText := strings.Join(visibleTextNodes(document), " ")
	visibleText = strings.Join(strings.Fields(visibleText), " ")
	dateMatch := stockAnalysisDatePattern.FindStringSubmatch(visibleText)
	if len(dateMatch) != 2 {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page has no page-update date")
	}
	pageUpdated, err := parseStockAnalysisDate(dateMatch[1])
	if err != nil || pageUpdated.After(asOf.UTC().Add(24*time.Hour)) {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page update date is invalid")
	}
	typedPageKind := StockAnalysisPageKind(pageKind)
	pageSnapshot := stockAnalysisPageSnapshotStatus(pageUpdated, typedPageKind, asOf)
	if pageSnapshot == "" {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page update is after the evidence cutoff")
	}
	dataMatch := stockAnalysisDataPattern.FindStringSubmatch(visibleText)
	if len(dataMatch) != 2 {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page has no visible data-source label")
	}
	dataSource := cleanText(strings.Trim(dataMatch[1], " .·|"), 160)
	if dataSource == "" {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis data-source label is empty")
	}
	tables := stockAnalysisTables(document)
	page := stockAnalysisPage{
		URL: pageURL, Title: cleanText(h1, 180), DataSource: dataSource,
		PageUpdated: pageUpdated.Format("2006-01-02"), RetrievedAt: retrievedAt.UTC(),
		PageKind: typedPageKind, PageSnapshotStatus: pageSnapshot,
	}
	if pageKind == "statistics" {
		page.FiscalValues = statisticsPageFacts(tables)
	} else {
		page.PageSummary = forecastBasisSummary(visibleText)
		page.FiscalValues = forecastPageFacts(tables, page.PageSummary, asOf, stockAnalysisFinancialForecastProvider(visibleText))
	}
	if len(page.FiscalValues) == 0 {
		return stockAnalysisPage{}, fmt.Errorf("StockAnalysis page contained no supported valuation or forecast rows")
	}
	return page, nil
}

func stockAnalysisPageIdentityMatches(title, h1 string, candidate stockAnalysisCandidate, brief, pageKind string) bool {
	titleMatches := containsWholePhrase(title, candidate.subject)
	h1Matches := containsWholePhrase(h1, candidate.subject)
	tickerOnlyH1 := strings.EqualFold(strings.TrimSpace(h1), candidate.slug)
	shortIssuerH1 := stockAnalysisShortIssuerHeaderMatches(h1, candidate.subject, pageKind)
	if strings.TrimSpace(h1) != "" && !h1Matches && !tickerOnlyH1 && !shortIssuerH1 {
		return false
	}
	if !titleMatches && !h1Matches && !shortIssuerH1 {
		return false
	}
	identityText := strings.TrimSpace(title + " " + h1)
	return subjectMatchesBrief(candidate.subject, strings.ToLower(brief), identityText+" "+candidate.title)
}

func stockAnalysisShortIssuerHeaderMatches(h1, subject, pageKind string) bool {
	issuer := ""
	for _, token := range companyNameTokenPattern.FindAllString(strings.ToLower(subject), -1) {
		if _, generic := nonDistinctiveCompanyTokens[token]; !generic {
			issuer = token
			break
		}
	}
	if issuer == "" {
		return false
	}
	normalized := normalizedStockAnalysisLabel(h1)
	suffixes := map[StockAnalysisPageKind][]string{
		StockAnalysisPageStatistics: {"statistics", "valuation", "statistics and valuation"},
		StockAnalysisPageForecast:   {"forecast", "stock forecast", "analyst price targets", "stock forecast and analyst price targets"},
	}
	for _, suffix := range suffixes[StockAnalysisPageKind(pageKind)] {
		if normalized == issuer+" "+suffix {
			return true
		}
	}
	return false
}

func parseStockAnalysisDate(value string) (time.Time, error) {
	for _, layout := range []string{"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "January 2 2006"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported StockAnalysis date")
}

func firstNodeText(root *html.Node, tag string) string {
	var found string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != "" || node == nil {
			return
		}
		if isHiddenHTMLNode(node) {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, tag) {
			found = nodeText(node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(found), " ")
}

func visibleTextNodes(root *html.Node) []string {
	var values []string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node == nil || hidden || isHiddenHTMLNode(node) {
			return
		}
		if node.Type == html.TextNode {
			value := strings.Join(strings.Fields(node.Data), " ")
			if value != "" {
				values = append(values, value)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(root, false)
	return values
}

func isHiddenHTMLNode(node *html.Node) bool {
	if node == nil || node.Type != html.ElementNode {
		return false
	}
	if strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "style") || strings.EqualFold(node.Data, "noscript") {
		return true
	}
	for _, attribute := range node.Attr {
		key, value := strings.ToLower(attribute.Key), strings.ToLower(strings.TrimSpace(attribute.Val))
		switch key {
		case "hidden":
			return true
		case "aria-hidden":
			if value == "true" {
				return true
			}
		case "style":
			if strings.Contains(strings.ReplaceAll(value, " ", ""), "display:none") || strings.Contains(strings.ReplaceAll(value, " ", ""), "visibility:hidden") {
				return true
			}
		case "class":
			for _, className := range strings.Fields(value) {
				if className == "hidden" || className == "sr-only" {
					return true
				}
			}
		}
	}
	return false
}

func nodeText(root *html.Node) string {
	var values []string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node == nil || hidden || isHiddenHTMLNode(node) {
			return
		}
		if node.Type == html.TextNode {
			values = append(values, node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(root, false)
	return strings.Join(values, " ")
}

func stockAnalysisTables(root *html.Node) []stockAnalysisTable {
	var tables []stockAnalysisTable
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node == nil || hidden || isHiddenHTMLNode(node) {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "table") {
			rows := stockAnalysisRows(node)
			if len(rows) > 0 {
				tables = append(tables, stockAnalysisTable{Rows: rows})
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(root, false)
	return tables
}

func stockAnalysisRows(root *html.Node) []stockAnalysisRow {
	var rows []stockAnalysisRow
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, hidden bool) {
		if node == nil || hidden || isHiddenHTMLNode(node) {
			return
		}
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "tr") {
			var cells []string
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && (strings.EqualFold(child.Data, "td") || strings.EqualFold(child.Data, "th")) {
					// Keep a blank position for a hidden cell so subsequent values
					// cannot slide under the wrong fiscal-year header.
					if isHiddenHTMLNode(child) {
						cells = append(cells, "")
						continue
					}
					cells = append(cells, strings.Join(strings.Fields(nodeText(child)), " "))
				}
			}
			if len(cells) > 1 {
				rows = append(rows, stockAnalysisRow{Cells: cells})
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		walk(child, false)
	}
	return rows
}

func statisticsPageFacts(tables []stockAnalysisTable) []stockAnalysisFact {
	values := map[string]string{}
	for _, table := range tables {
		for _, row := range table.Rows {
			label := normalizedStockAnalysisLabel(row.Cells[0])
			if len(row.Cells) < 2 || (label != "forward pe" && label != "eps growth forecast 3y") {
				continue
			}
			value := normalizedStockAnalysisValue(row.Cells[1])
			if !validStockAnalysisNumber(value) {
				continue
			}
			if prior, exists := values[label]; exists && prior != value {
				return nil
			}
			values[label] = value
		}
	}
	var facts []stockAnalysisFact
	if value := values["forward pe"]; value != "" && positiveStockAnalysisNumber(value) {
		facts = append(facts, stockAnalysisFact{Claim: "Reported forward P/E", Value: value, Unit: "x", MetricKind: EvidenceMetricForwardPE})
	}
	if value := values["eps growth forecast 3y"]; value != "" {
		facts = append(facts, stockAnalysisFact{Claim: "Reported EPS growth forecast (3Y)", Value: strings.TrimSuffix(value, "%"), Unit: "%", MetricKind: EvidenceMetricEPSGrowth3Y})
	}
	return facts
}

func forecastPageFacts(tables []stockAnalysisTable, basis string, asOf time.Time, forecastProvider string) []stockAnalysisFact {
	var futureYear string
	var futureIndex int
	var futurePeriod time.Time
	var futureTable *stockAnalysisTable
	for tableIndex := range tables {
		table := &tables[tableIndex]
		var header, periodRow []string
		for _, row := range table.Rows {
			if len(row.Cells) == 0 {
				continue
			}
			switch normalizedStockAnalysisLabel(row.Cells[0]) {
			case "fiscal year":
				if header != nil && !sameStockAnalysisCells(header, row.Cells) {
					return nil
				}
				header = row.Cells
			case "period ending":
				if periodRow != nil && !sameStockAnalysisCells(periodRow, row.Cells) {
					return nil
				}
				periodRow = row.Cells
			}
		}
		if len(header) < 2 || len(periodRow) != len(header) {
			continue
		}
		for index := 1; index < len(header); index++ {
			yearMatch := regexp.MustCompile(`(?i)^fy\s*(20\d{2})$`).FindStringSubmatch(strings.TrimSpace(header[index]))
			if len(yearMatch) != 2 || index >= len(periodRow) {
				continue
			}
			period, err := parseStockAnalysisPeriodEnd(periodRow[index])
			if err != nil {
				continue
			}
			year := "FY" + yearMatch[1]
			if period.After(asOf.UTC()) {
				if futureTable == nil || period.Before(futurePeriod) {
					futureYear, futureIndex, futurePeriod, futureTable = year, index, period, table
				} else if period.Equal(futurePeriod) && (futureYear != year || futureIndex != index || futureTable != table) {
					return nil
				}
			}
		}
	}
	selectedYear, selectedIndex, selectedPeriod, selectedTable := futureYear, futureIndex, futurePeriod, futureTable
	if selectedTable == nil {
		return nil
	}
	labels := map[string]string{}
	for _, row := range selectedTable.Rows {
		if len(row.Cells) == 0 || selectedIndex >= len(row.Cells) {
			continue
		}
		label := normalizedStockAnalysisLabel(row.Cells[0])
		if label != "eps" && label != "eps growth" && label != "forward pe" && label != "no analysts" {
			continue
		}
		value := normalizedStockAnalysisValue(row.Cells[selectedIndex])
		if !validStockAnalysisNumber(value) {
			continue
		}
		if prior, exists := labels[label]; exists && prior != value {
			return nil
		}
		labels[label] = value
	}
	var facts []stockAnalysisFact
	epsPresent := labels["eps"] != ""
	epsPositive := positiveStockAnalysisNumber(labels["eps"])
	if value := labels["forward pe"]; value != "" && positiveStockAnalysisNumber(value) && (!epsPresent || epsPositive) {
		claim := selectedYear + " forward P/E"
		fact := stockAnalysisFact{Claim: claim, Value: value, Unit: "x", MetricKind: EvidenceMetricForwardPE, ForecastTargetPeriodEnd: selectedPeriod}
		if basis != "" {
			claim += " (adjusted EPS basis)"
			fact.Claim = claim
			fact.BasisKind = EvidenceBasisAdjustedNonGAAP
			fact.BasisLabel = basis
		}
		facts = append(facts, fact)
	}
	if value := labels["eps growth"]; value != "" {
		facts = append(facts, stockAnalysisFact{Claim: selectedYear + " EPS growth estimate", Value: strings.TrimSuffix(value, "%"), Unit: "%", MetricKind: EvidenceMetricEPSGrowth, ForecastTargetPeriodEnd: selectedPeriod})
	}
	if value := labels["eps"]; value != "" {
		claim := selectedYear + " EPS estimate"
		fact := stockAnalysisFact{Claim: claim, Value: value, Unit: "per share", MetricKind: EvidenceMetricEPS, ForecastTargetPeriodEnd: selectedPeriod}
		if basis != "" {
			claim = selectedYear + " adjusted EPS estimate"
			fact.Claim = claim
			fact.BasisKind = EvidenceBasisAdjustedNonGAAP
			fact.BasisLabel = basis
		}
		facts = append(facts, fact)
	}
	if value := labels["no analysts"]; value != "" {
		facts = append(facts, stockAnalysisFact{Claim: selectedYear + " analyst estimate count", Value: value, Unit: "analysts", MetricKind: EvidenceMetricAnalystCount, ForecastTargetPeriodEnd: selectedPeriod})
	}
	if forecastProvider != "" {
		for index := range facts {
			switch facts[index].MetricKind {
			case EvidenceMetricForwardPE, EvidenceMetricEPSGrowth, EvidenceMetricEPS:
				facts[index].AttributionLabel = forecastProvider
				if stockAnalysisHasAggregateAnalystCount(labels["no analysts"]) {
					facts[index].AttributionKind = EvidenceAttributionSourceAggregateForecast
				}
			}
		}
	}
	return facts
}

func stockAnalysisHasAggregateAnalystCount(value string) bool {
	count, err := strconv.ParseInt(normalizedStockAnalysisValue(value), 10, 64)
	return err == nil && count >= 2
}

func stockAnalysisFinancialForecastProvider(visibleText string) string {
	matches := stockAnalysisFinancialForecastProviderPattern.FindAllStringSubmatch(visibleText, -1)
	provider := ""
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		candidate := cleanText(strings.TrimSpace(match[1]), 100)
		if candidate == "" {
			continue
		}
		if provider != "" && !strings.EqualFold(provider, candidate) {
			return ""
		}
		provider = candidate
	}
	return provider
}

func positiveStockAnalysisNumber(value string) bool {
	value = strings.TrimSuffix(normalizedStockAnalysisValue(value), "%")
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && parsed > 0
}

func parseStockAnalysisPeriodEnd(value string) (time.Time, error) {
	for _, layout := range []string{"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "January 2 2006"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported fiscal period end")
}

func sameStockAnalysisCells(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}

func forecastBasisSummary(visibleText string) string {
	const note = "EPS and Forward PE are based on non-GAAP adjusted numbers."
	if strings.Contains(strings.ToLower(visibleText), strings.ToLower(note)) {
		return note
	}
	return ""
}

func normalizedStockAnalysisLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "&", " and ")
	return strings.Join(strings.Fields(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, " ")), " ")
}

func normalizedStockAnalysisValue(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	value = strings.TrimPrefix(value, "$\u00a0")
	return strings.TrimSpace(value)
}

func validStockAnalysisNumber(value string) bool {
	if !stockAnalysisNumber.MatchString(value) {
		return false
	}
	trimmed := strings.TrimSuffix(value, "%")
	_, err := strconv.ParseFloat(trimmed, 64)
	return err == nil
}

func appendStockAnalysisPage(check *EvidenceCheckOutput, page stockAnalysisPage) {
	if check == nil || page.URL == "" || len(page.FiscalValues) == 0 {
		return
	}
	pageKey := stockAnalysisURLKey(page.URL)
	publicURL := page.URL
	verifiedSource := EvidenceCheckSource{
		SourceRef: page.URL, PublicURL: &publicURL, Title: page.Title,
		DataSource: "StockAnalysis verified " + string(page.PageKind) + " page", PageSummary: page.PageSummary,
		PageUpdatedOn: page.PageUpdated, RetrievedAt: page.RetrievedAt,
		PageSnapshotStatus: page.PageSnapshotStatus,
	}
	// Replace every search annotation for this exact page. Multiple provider
	// snippets can point at the same URL, and none may inherit the fresh page
	// update date while retaining stale excerpt text.
	sources := make([]EvidenceCheckSource, 0, len(check.Sources)+1)
	replacedSource := false
	for _, source := range check.Sources {
		if stockAnalysisURLKey(source.SourceRef) == pageKey {
			if !replacedSource {
				sources = append(sources, verifiedSource)
				replacedSource = true
			}
			continue
		}
		sources = append(sources, source)
	}
	if !replacedSource {
		sources = append(sources, verifiedSource)
	}
	check.Sources = sources
	remainingFacts := check.Facts[:0]
	for _, fact := range check.Facts {
		if stockAnalysisURLKey(fact.SourceRef) != pageKey {
			remainingFacts = append(remainingFacts, fact)
		}
	}
	check.Facts = remainingFacts
	for _, fact := range page.FiscalValues {
		appended := EvidenceCheckFact{
			Claim: fact.Claim, Value: fact.Value, Unit: stringPtr(fact.Unit),
			ObservedAt: nil, SourceRef: page.URL, PageKind: page.PageKind,
			MetricKind: fact.MetricKind, BasisKind: fact.BasisKind, BasisLabel: fact.BasisLabel,
			AttributionKind: fact.AttributionKind, AttributionLabel: fact.AttributionLabel,
		}
		if !fact.ForecastTargetPeriodEnd.IsZero() {
			periodEnd := fact.ForecastTargetPeriodEnd.UTC()
			appended.ForecastTargetPeriodEnd = &periodEnd
		}
		check.Facts = append(check.Facts, appended)
	}
}

func stockAnalysisURLKey(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Hostname(), "stockanalysis.com") {
		return ""
	}
	matches := stockAnalysisPathPattern.FindStringSubmatch(strings.ToLower(parsed.EscapedPath()))
	if len(matches) != 3 {
		return ""
	}
	return matches[1] + "/" + matches[2]
}

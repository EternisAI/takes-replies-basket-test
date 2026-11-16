package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
)

// WebCitation contains citation metadata returned by the provider. Content is
// the provider excerpt, not model prose or a fetched page.
type WebCitation struct {
	URL         string
	Title       string
	Content     string
	PublishedAt *time.Time
}

// WebSearchReceipt preserves provider-reported search usage. A nil
// SearchRequests means the provider did not report the count.
type WebSearchReceipt struct {
	Citations      []WebCitation
	SearchRequests *int
}

type webCitationObserverKey struct{}

// WithWebCitationObserver receives provider citations from a successful
// request that opted into OpenRouter web search.
func WithWebCitationObserver(ctx context.Context, observe func(WebSearchReceipt)) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, webCitationObserverKey{}, observe)
}

func webCitationObserverFrom(ctx context.Context) func(WebSearchReceipt) {
	observe, _ := ctx.Value(webCitationObserverKey{}).(func(WebSearchReceipt))
	return observe
}

// CaptureWebCitations forwards a receipt to an observer attached to ctx. It
// also lets alternate Completer implementations preserve the same contract.
func CaptureWebCitations(ctx context.Context, receipt WebSearchReceipt) {
	if observe := webCitationObserverFrom(ctx); observe != nil {
		receipt.Citations = append([]WebCitation(nil), receipt.Citations...)
		observe(receipt)
	}
}

// SupportsOpenRouterWebSearch reports whether c explicitly supports the
// OpenRouter server-side web-search tool.
func SupportsOpenRouterWebSearch(c Completer) bool {
	support, ok := c.(interface{ SupportsOpenRouterWebSearch() bool })
	return ok && support.SupportsOpenRouterWebSearch()
}

func webCitationsFrom(completion *openai.ChatCompletion) []WebCitation {
	if completion == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var citations []WebCitation
	for _, choice := range completion.Choices {
		for _, annotation := range choice.Message.Annotations {
			if string(annotation.Type) != "url_citation" {
				continue
			}
			var raw struct {
				URLCitation struct {
					URL         string `json:"url"`
					Title       string `json:"title"`
					Content     string `json:"content"`
					PublishedAt string `json:"published_at"`
				} `json:"url_citation"`
				Content     string `json:"content"`
				PublishedAt string `json:"published_at"`
			}
			if json.Unmarshal([]byte(annotation.RawJSON()), &raw) != nil {
				continue
			}
			citationURL := safeCitationURL(raw.URLCitation.URL)
			if citationURL == "" {
				continue
			}
			if _, ok := seen[citationURL]; ok {
				continue
			}
			content := raw.URLCitation.Content
			if content == "" {
				content = raw.Content
			}
			content = strings.TrimSpace(content)
			if content == "" {
				continue
			}
			publishedAt := parseCitationDate(raw.URLCitation.PublishedAt)
			if publishedAt == nil {
				publishedAt = parseCitationDate(raw.PublishedAt)
			}
			seen[citationURL] = struct{}{}
			citations = append(citations, WebCitation{
				URL: citationURL, Title: strings.TrimSpace(raw.URLCitation.Title),
				Content: content, PublishedAt: publishedAt,
			})
		}
	}
	return citations
}

func webSearchRequestsFrom(completion *openai.ChatCompletion) *int {
	if completion == nil {
		return nil
	}
	count, err := ParseWebSearchRequestCount([]byte(completion.RawJSON()))
	if err != nil {
		return nil
	}
	return &count
}

// ParseWebSearchRequestCount reads provider-reported search usage from either
// supported response location. Missing, malformed or conflicting usage is an
// error; callers must not infer it from request or citation counts.
func ParseWebSearchRequestCount(data []byte) (int, error) {
	root, err := webSearchUsageObject(data)
	if err != nil {
		return 0, err
	}
	usage, err := webSearchUsageObject(root["usage"])
	if err != nil {
		return 0, errors.New("provider search usage is missing or malformed")
	}
	count, found := 0, false
	for _, name := range []string{"server_tool_use", "server_tool_use_details"} {
		location, exists := usage[name]
		if !exists {
			continue
		}
		fields, parseErr := webSearchUsageObject(location)
		if parseErr != nil {
			return 0, errors.New("provider search usage location is malformed")
		}
		rawCount, exists := fields["web_search_requests"]
		var reported int
		if !exists || bytes.Equal(bytes.TrimSpace(rawCount), []byte("null")) ||
			json.Unmarshal(rawCount, &reported) != nil || reported < 0 {
			return 0, errors.New("provider search count is missing or invalid")
		}
		if found && count != reported {
			return 0, errors.New("provider search counts conflict")
		}
		count, found = reported, true
	}
	if !found {
		return 0, errors.New("provider search count is missing")
	}
	return count, nil
}

// Reject duplicate fields in the three relevant objects rather than
// silently choosing a potentially conflicting search count.
func webSearchUsageObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("provider search usage must be a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, ok := keyToken.(string)
		if keyErr != nil || !ok {
			return nil, errors.New("provider search usage key is invalid")
		}
		if _, exists := fields[key]; exists {
			return nil, errors.New("provider search usage contains duplicate fields")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("provider search usage value is invalid")
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("provider search usage object is incomplete")
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("provider search usage has trailing content")
	}
	return fields, nil
}

func safeCitationURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil {
		return ""
	}
	return parsed.String()
}

func parseCitationDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

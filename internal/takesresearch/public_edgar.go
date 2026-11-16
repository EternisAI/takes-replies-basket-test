package takesresearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/clock"
)

const (
	secCompanyTickerURL = "https://www.sec.gov/files/company_tickers_exchange.json"
	secUserAgent        = "Takes exploratory identity support@eternis.ai"
	secTickerMaxBytes   = 4 << 20
	secTickerCacheAge   = 6 * time.Hour
)

// IssuerIdentity is one row from the public SEC company ticker/exchange index.
// Multiple rows can identify the same CIK when an issuer has several listings.
type IssuerIdentity struct {
	CIK      string
	Name     string
	Ticker   string
	Exchange string
}

// CompanyIdentityReader returns the complete SEC ticker/exchange index and
// the retrieval time for that exact index. Callers must not infer user access
// or a common-stock holding from an issuer identity match.
type CompanyIdentityReader interface {
	Tickers(context.Context) ([]IssuerIdentity, time.Time, error)
}

type secTickerDataset struct {
	Fields []string            `json:"fields"`
	Data   [][]json.RawMessage `json:"data"`
}

type secIdentityReader struct {
	http *http.Client

	mu          sync.Mutex
	entries     []IssuerIdentity
	retrievedAt time.Time
}

// NewSECIdentityReader creates a read-only adapter for the SEC's public
// company_tickers_exchange.json index. It copies the supplied client before
// setting its timeout/redirect policy.
func NewSECIdentityReader(client *http.Client) CompanyIdentityReader {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &secIdentityReader{http: client}
}

func (r *secIdentityReader) Tickers(ctx context.Context) ([]IssuerIdentity, time.Time, error) {
	now := clock.Now()
	r.mu.Lock()
	if len(r.entries) > 0 && now.Sub(r.retrievedAt) <= secTickerCacheAge {
		entries, retrievedAt := slices.Clone(r.entries), r.retrievedAt
		r.mu.Unlock()
		return entries, retrievedAt, nil
	}
	r.mu.Unlock()

	parsed, err := url.Parse(secCompanyTickerURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "www.sec.gov" || parsed.Path != "/files/company_tickers_exchange.json" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, time.Time{}, errors.New("SEC identity request URL is not an approved public endpoint")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, secCompanyTickerURL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	request.Header.Set("User-Agent", secUserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := r.http.Do(request)
	if err != nil {
		return nil, time.Time{}, errors.New("SEC public ticker index request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, time.Time{}, fmt.Errorf("SEC public ticker index returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, secTickerMaxBytes+1))
	if err != nil {
		return nil, time.Time{}, errors.New("SEC public ticker index could not be read")
	}
	if len(body) > secTickerMaxBytes {
		return nil, time.Time{}, errors.New("SEC public ticker index exceeded its configured size bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var dataset secTickerDataset
	if err := decoder.Decode(&dataset); err != nil {
		return nil, time.Time{}, errors.New("SEC public ticker index was invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, time.Time{}, errors.New("SEC ticker index contains trailing JSON or data")
	}
	entries, err := parseSECTickerDataset(dataset)
	if err != nil {
		return nil, time.Time{}, err
	}
	retrievedAt := clock.Now()
	r.mu.Lock()
	r.entries = slices.Clone(entries)
	r.retrievedAt = retrievedAt
	r.mu.Unlock()
	return entries, retrievedAt, nil
}

func parseSECTickerDataset(dataset secTickerDataset) ([]IssuerIdentity, error) {
	fieldIndex := make(map[string]int, len(dataset.Fields))
	for index, field := range dataset.Fields {
		fieldIndex[strings.ToLower(strings.TrimSpace(field))] = index
	}
	for _, required := range []string{"cik", "name", "ticker"} {
		if _, ok := fieldIndex[required]; !ok {
			return nil, errors.New("SEC ticker index is missing a required field")
		}
	}
	entries := make([]IssuerIdentity, 0, len(dataset.Data))
	for _, row := range dataset.Data {
		read := func(field string) (string, bool) {
			index, ok := fieldIndex[field]
			if !ok || index >= len(row) {
				return "", false
			}
			var value string
			raw := bytes.TrimSpace(row[index])
			if len(raw) == 0 {
				return "", false
			}
			if raw[0] == '"' {
				if err := json.Unmarshal(raw, &value); err != nil {
					return "", false
				}
			} else {
				value = string(raw)
			}
			return strings.TrimSpace(value), true
		}
		cik, cikOK := read("cik")
		name, nameOK := read("name")
		ticker, tickerOK := read("ticker")
		if !cikOK || !nameOK || !tickerOK {
			return nil, errors.New("SEC ticker index contains a malformed issuer row")
		}
		entry := IssuerIdentity{CIK: canonicalCIK(cik), Name: name, Ticker: strings.ToUpper(ticker)}
		if exchange, ok := read("exchange"); ok {
			entry.Exchange = exchange
		}
		if entry.CIK == "" || entry.Name == "" || entry.Ticker == "" {
			return nil, errors.New("SEC ticker index contains an incomplete issuer row")
		}
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return nil, errors.New("SEC ticker index contains no usable issuer rows")
	}
	return entries, nil
}

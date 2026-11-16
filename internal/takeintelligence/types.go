// Package takeintelligence contains the pure rules used to validate a Takes
// research proposal. It does not read a clock, fetch a source, select an
// instrument, persist state, monitor a portfolio, or authorize a trade.
package takeintelligence

import (
	"net/url"
	"sort"
	"strings"
)

// ProviderInstrumentKey is the durable identity of an instrument at a venue.
// Asset indexes are deliberately excluded: a venue may reorder its universe.
type ProviderInstrumentKey struct {
	Provider string
	Product  string
	Venue    string
	Symbol   string
}

// String encodes the tuple as four escaped path segments. An empty Venue is
// retained as an empty segment so native and builder-deployed instruments do
// not collide.
func (k ProviderInstrumentKey) String() string {
	parts := []string{k.Provider, k.Product, k.Venue, k.Symbol}
	for i := range parts {
		parts[i] = strings.ReplaceAll(url.PathEscape(parts[i]), ":", "%3A")
	}
	return strings.Join(parts, "/")
}

func (k ProviderInstrumentKey) valid() bool {
	return strings.TrimSpace(k.Provider) != "" && strings.TrimSpace(k.Product) != "" && strings.TrimSpace(k.Symbol) != ""
}

// FindingSeverity identifies whether a finding blocks a result or only adds a
// caveat.
type FindingSeverity string

const (
	SeverityError   FindingSeverity = "error"
	SeverityWarning FindingSeverity = "warning"
	SeverityInfo    FindingSeverity = "info"
)

// Finding is a stable, machine-readable explanation returned by a validator.
type Finding struct {
	Code     string
	Subject  string
	Severity FindingSeverity
	Message  string
}

func findingsSorted(findings []Finding) []Finding {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Subject != findings[j].Subject {
			return findings[i].Subject < findings[j].Subject
		}
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].Message < findings[j].Message
	})
	return findings
}

func hasErrors(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == SeverityError {
			return true
		}
	}
	return false
}

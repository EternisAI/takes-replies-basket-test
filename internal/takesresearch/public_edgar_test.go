package takesresearch

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

func TestSECIdentityIndexParsesNumericCIKAndExchange(t *testing.T) {
	dataset := secTickerDataset{
		Fields: []string{"cik", "name", "ticker", "exchange"},
		Data:   [][]json.RawMessage{{json.RawMessage("320193"), json.RawMessage(`"Apple Inc."`), json.RawMessage(`"aapl"`), json.RawMessage(`"Nasdaq"`)}},
	}
	entries, err := parseSECTickerDataset(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CIK != "0000320193" || entries[0].Name != "Apple Inc." || entries[0].Ticker != "AAPL" || entries[0].Exchange != "Nasdaq" {
		t.Fatalf("unexpected SEC identity row: %#v", entries)
	}
}

func TestExpressionMappingRequiresTickerAndOfficialIssuerName(t *testing.T) {
	retrievedAt := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	entries := []IssuerIdentity{{CIK: "123", Name: "Acme Corporation", Ticker: "ACME", Exchange: "Nasdaq"}}
	issuer, err := basketResearchIssuerFromIndex(basketResearchSourceCandidate{ID: "company_1", IssuerName: "Acme"}, entries, retrievedAt, false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := takeintelligence.CatalogSnapshot{
		Result: takeintelligence.SnapshotComplete,
		Instruments: []takeintelligence.Instrument{{
			Key:           takeintelligence.ProviderInstrumentKey{Provider: "hyperliquid", Product: "perpetual", Venue: "xyz", Symbol: "xyz:ACME"},
			ListingStatus: takeintelligence.ListingObserved,
			MarketType:    "perpetual",
			Category:      "stocks",
			DisplayName:   "Acme Corporation",
		}},
	}
	mapping := basketResearchCandidateMapping("company_1", issuer, entries, snapshot)
	if mapping.Status != CandidateMappingMapped || len(mapping.ReviewedMappings) != 1 {
		t.Fatalf("expected one exact catalog mapping, got %#v", mapping)
	}
	if mapping.ReviewedMappings[0].EntityKey != "sec:cik:0000000123" || mapping.ReviewedMappings[0].Key.Symbol != "xyz:ACME" {
		t.Fatalf("unexpected mapped identity: %#v", mapping.ReviewedMappings[0])
	}

	wrongTicker := snapshot
	wrongTicker.Instruments = append([]takeintelligence.Instrument(nil), snapshot.Instruments...)
	wrongTicker.Instruments[0].Key.Symbol = "xyz:OTHER"
	if got := basketResearchCandidateMapping("company_1", issuer, entries, wrongTicker); got.Status == CandidateMappingMapped {
		t.Fatalf("ticker mismatch unexpectedly mapped: %#v", got)
	}

	wrongName := snapshot
	wrongName.Instruments = append([]takeintelligence.Instrument(nil), snapshot.Instruments...)
	wrongName.Instruments[0].DisplayName = "Unrelated Company"
	if got := basketResearchCandidateMapping("company_1", issuer, entries, wrongName); got.Status == CandidateMappingMapped {
		t.Fatalf("unverified issuer name unexpectedly mapped: %#v", got)
	}
}

func TestExpressionBuilderRejectsDeepResearchMode(t *testing.T) {
	_, err := NewExpressionBuilder(nil, nil, nil).Build(context.Background(), Input{ConstructionMode: DeepResearchMode})
	if err == nil || !strings.Contains(err.Error(), "deep research mode is not supported") {
		t.Fatalf("deep research was not explicitly rejected: %v", err)
	}
}

package takesresearch

import (
	"slices"
	"strings"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

// CatalogContext is the server's account of the exact catalog snapshot both
// model stages work from. A listing is venue membership only: the snapshot
// records nothing about the user's account access, eligibility, or approval
// to trade, so AccountAccess is always unknown.
type CatalogContext struct {
	SnapshotID      string                          `json:"snapshotId"`
	Provider        string                          `json:"provider"`
	AsOf            time.Time                       `json:"asOf"`
	Status          takeintelligence.SnapshotResult `json:"status"`
	ProductTypes    []string                        `json:"productTypes"`
	InstrumentCount int                             `json:"instrumentCount"`
	// Listing is set when every instrument shares one listing status;
	// otherwise each instrument carries its own.
	Listing       takeintelligence.ListingStatus       `json:"listing,omitempty"`
	AccountAccess takeintelligence.AccountAccessStatus `json:"accountAccess"`
	// UnknownForAll names fields no instrument established, so each
	// instrument's unknown list carries only the exceptions.
	UnknownForAll []string `json:"unknownForAll,omitempty"`
}

// ResearchCatalog derives the capability header and one record per listing,
// in snapshot order, from one exact snapshot. The Builder's planner and
// selector and the conversation guide all read it through this function.
func ResearchCatalog(snapshot takeintelligence.CatalogSnapshot) (CatalogContext, []ResearchInstrument) {
	header := CatalogContext{
		SnapshotID: snapshot.ID, Provider: snapshot.Provider, AsOf: snapshot.CompletedAt, Status: snapshot.Result,
		ProductTypes: []string{}, InstrumentCount: len(snapshot.Instruments), AccountAccess: takeintelligence.AccountAccessUnknown,
	}
	listings := make(map[takeintelligence.ListingStatus]bool)
	unknownCounts := make(map[string]int)
	conditions := make([][]string, len(snapshot.Instruments))
	unknown := make([][]string, len(snapshot.Instruments))
	for index, instrument := range snapshot.Instruments {
		if marketType := strings.TrimSpace(instrument.MarketType); marketType != "" && !slices.Contains(header.ProductTypes, marketType) {
			header.ProductTypes = append(header.ProductTypes, marketType)
		}
		listings[instrument.ListingStatus] = true
		conditions[index], unknown[index] = instrumentConditions(instrument, snapshot.CompletedAt)
		for _, field := range unknown[index] {
			unknownCounts[field]++
		}
	}
	slices.Sort(header.ProductTypes)
	if len(listings) == 1 {
		for status := range listings {
			header.Listing = status
		}
	}
	for field, count := range unknownCounts {
		if count == len(snapshot.Instruments) {
			header.UnknownForAll = append(header.UnknownForAll, field)
		}
	}
	slices.Sort(header.UnknownForAll)
	records := make([]ResearchInstrument, 0, len(snapshot.Instruments))
	for index, instrument := range snapshot.Instruments {
		record := ResearchInstrument{
			Key: instrument.Key, DisplayName: instrument.DisplayName, MarketType: instrument.MarketType,
			Category: instrument.Category, Conditions: conditions[index],
		}
		if header.Listing == "" {
			record.Listing = instrument.ListingStatus
		}
		for _, field := range unknown[index] {
			if !slices.Contains(header.UnknownForAll, field) {
				record.Unknown = append(record.Unknown, field)
			}
		}
		records = append(records, record)
	}
	return header, records
}

// instrumentConditions lists the venue restrictions the snapshot observed for
// one listing and the fields it did not establish. A field without a recorded
// reading stays unknown; it is never reported as the condition being absent.
func instrumentConditions(instrument takeintelligence.Instrument, asOf time.Time) (conditions, unknown []string) {
	if instrument.Liquidity.Classified {
		if instrument.Illiquid {
			conditions = append(conditions, "illiquid")
		}
		if instrument.Thin {
			conditions = append(conditions, "thin")
		}
	} else {
		unknown = append(unknown, "liquidity")
	}
	if instrument.AtOICapKnown {
		if instrument.AtOICap {
			conditions = append(conditions, "at_open_interest_cap")
		}
	} else {
		unknown = append(unknown, "open_interest_cap")
	}
	// The adapter flags isolated-only listings but has no reading that a
	// listing allows cross margin, so false establishes nothing.
	if instrument.OnlyIsolated {
		conditions = append(conditions, "isolated_margin_only")
	} else {
		unknown = append(unknown, "isolated_margin")
	}
	if instrument.GrowthModeKnown {
		if instrument.GrowthMode {
			conditions = append(conditions, "growth_mode")
		}
	} else {
		unknown = append(unknown, "growth_mode")
	}
	if instrument.WeekendPricingKnown {
		if instrument.WeekendPricing {
			conditions = append(conditions, "weekend_pricing")
		}
	} else {
		unknown = append(unknown, "weekend_pricing")
	}
	switch {
	// A reading stamped after the snapshot completed cannot be placed in time.
	case instrument.ObservedAt.IsZero(), instrument.ObservedAt.After(asOf):
		unknown = append(unknown, "observation_time")
	case asOf.Sub(instrument.ObservedAt) > takeintelligence.MaxCatalogAge:
		conditions = append(conditions, "stale_observation")
	}
	return conditions, unknown
}

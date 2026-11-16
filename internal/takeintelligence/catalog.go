package takeintelligence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxCatalogAge is the maximum age of a catalog read or instrument
	// observation when a proposal is completed.
	MaxCatalogAge = time.Minute
)

type SnapshotResult string

const (
	SnapshotComplete SnapshotResult = "complete"
	SnapshotPartial  SnapshotResult = "partial"
	SnapshotFailed   SnapshotResult = "failed"
)

type ListingStatus string

const (
	ListingObserved   ListingStatus = "listed_observed"
	DelistingObserved ListingStatus = "delisted_observed"
	ListingUnknown    ListingStatus = "unknown"
)

// LiquiditySnapshot keeps the market read separate from the timestamp of the
// reading used to classify its liquidity.
type LiquiditySnapshot struct {
	DayVolumeUSD       string
	OpenInterestUSD    string
	ObservedAt         time.Time
	LowDayVolume       bool
	LowOpenInterest    bool
	FloorConfiguration string
	Classified         bool
}

// Instrument is one record from a provider catalog snapshot. Decimal values
// are strings so the adapter can preserve the provider's exact representation.
type Instrument struct {
	Key                 ProviderInstrumentKey
	AssetIndex          int
	ListingStatus       ListingStatus
	MarketType          string
	Category            string
	DisplayName         string
	Keywords            []string
	MarkPrice           string
	OraclePrice         string
	FundingRate         string
	OpenInterest        string
	DayNotionalUSD      string
	ObservedAt          time.Time
	Liquidity           LiquiditySnapshot
	Illiquid            bool
	Thin                bool
	AtOICap             bool
	AtOICapKnown        bool
	MaxLeverage         int
	OnlyIsolated        bool
	MarginMode          string
	CollateralToken     string
	DeployerFeeScale    string
	GrowthMode          bool
	GrowthModeKnown     bool
	OICapUSD            string
	WeekendPricing      bool
	WeekendPricingKnown bool
	DeployerNote        string
}

// CatalogSnapshot is an immutable, persisted view of the configured public
// venue catalog. Its times are Axion's local read interval, not a provider
// signed timestamp. It never establishes account access or buying power.
type CatalogSnapshot struct {
	ID               string
	AdapterVersion   string
	Provider         string
	ConfiguredVenues []string
	StartedAt        time.Time
	CompletedAt      time.Time
	Result           SnapshotResult
	Digest           string
	FixtureOnly      bool
	Instruments      []Instrument
}

// AccountAccessStatus is deliberately independent of catalog membership.
type AccountAccessStatus string

const (
	AccountAccessUnknown     AccountAccessStatus = "unknown"
	AccountAccessAvailable   AccountAccessStatus = "available"
	AccountAccessUnavailable AccountAccessStatus = "unavailable"
)

// CatalogDigest returns a SHA-256 digest over the snapshot payload and ordered
// instrument records, excluding the Digest field itself.
func CatalogDigest(snapshot CatalogSnapshot) (string, error) {
	snapshot.Digest = ""
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal catalog snapshot: %w", err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

// ValidateCatalog checks snapshot-level integrity, identity keys, and read
// timing at the proposal's completion time. Market readiness belongs to the
// selected instrument: a bad unused listing must not poison every candidate.
func ValidateCatalog(snapshot CatalogSnapshot, proposalCompletedAt time.Time) []Finding {
	findings := validateSnapshotHeader(snapshot, proposalCompletedAt)
	venues := make(map[string]bool, len(snapshot.ConfiguredVenues))
	for _, venue := range snapshot.ConfiguredVenues {
		if venues[venue] {
			findings = append(findings, Finding{Code: "duplicate_configured_venue", Subject: snapshot.ID, Severity: SeverityError, Message: "the configured venue set contains a duplicate"})
		}
		venues[venue] = true
	}

	keys := make(map[string]bool, len(snapshot.Instruments))
	indexes := make(map[int]bool, len(snapshot.Instruments))
	for _, instrument := range snapshot.Instruments {
		subject := instrument.Key.String()
		if !instrument.Key.valid() {
			findings = append(findings, Finding{Code: "instrument_key_invalid", Subject: subject, Severity: SeverityError, Message: "provider, product, and symbol are required"})
		}
		if instrument.Key.Provider != snapshot.Provider {
			findings = append(findings, Finding{Code: "instrument_provider_mismatch", Subject: subject, Severity: SeverityError, Message: "instrument provider does not match the snapshot provider"})
		}
		if keys[subject] {
			findings = append(findings, Finding{Code: "duplicate_instrument_key", Subject: subject, Severity: SeverityError, Message: "the provider key appears more than once in this snapshot"})
		}
		keys[subject] = true
		if instrument.AssetIndex < 0 || indexes[instrument.AssetIndex] {
			findings = append(findings, Finding{Code: "asset_index_invalid", Subject: subject, Severity: SeverityError, Message: "asset index must be nonnegative and unique within the snapshot"})
		}
		indexes[instrument.AssetIndex] = true
	}
	return findingsSorted(findings)
}

func validateSnapshotHeader(snapshot CatalogSnapshot, proposalCompletedAt time.Time) []Finding {
	var findings []Finding
	if strings.TrimSpace(snapshot.ID) == "" {
		findings = append(findings, Finding{Code: "snapshot_id_missing", Severity: SeverityError, Message: "snapshot ID is required"})
	}
	if strings.TrimSpace(snapshot.AdapterVersion) == "" || strings.TrimSpace(snapshot.Provider) == "" {
		findings = append(findings, Finding{Code: "snapshot_provenance_missing", Subject: snapshot.ID, Severity: SeverityError, Message: "provider and adapter version are required"})
	}
	if snapshot.Result != SnapshotComplete {
		findings = append(findings, Finding{Code: "snapshot_not_complete", Subject: snapshot.ID, Severity: SeverityError, Message: "partial or failed catalog reads cannot support a complete proposal"})
	}
	if snapshot.StartedAt.IsZero() || snapshot.CompletedAt.IsZero() || snapshot.CompletedAt.Before(snapshot.StartedAt) {
		findings = append(findings, Finding{Code: "snapshot_times_invalid", Subject: snapshot.ID, Severity: SeverityError, Message: "catalog start and completion times are required and ordered"})
	}
	if proposalCompletedAt.IsZero() {
		findings = append(findings, Finding{Code: "proposal_completion_time_missing", Subject: snapshot.ID, Severity: SeverityError, Message: "proposal completion time is required for freshness validation"})
	} else if !snapshot.CompletedAt.IsZero() {
		if snapshot.CompletedAt.After(proposalCompletedAt) {
			findings = append(findings, Finding{Code: "snapshot_completed_in_future", Subject: snapshot.ID, Severity: SeverityError, Message: "catalog snapshot completed after the proposal"})
		} else if proposalCompletedAt.Sub(snapshot.CompletedAt) > MaxCatalogAge {
			findings = append(findings, Finding{Code: "snapshot_stale_at_completion", Subject: snapshot.ID, Severity: SeverityError, Message: "catalog snapshot is older than one minute at proposal completion"})
		}
	}
	if snapshot.Digest == "" {
		findings = append(findings, Finding{Code: "snapshot_digest_missing", Subject: snapshot.ID, Severity: SeverityError, Message: "catalog snapshot digest is required"})
	} else if digest, err := CatalogDigest(snapshot); err != nil || digest != snapshot.Digest {
		findings = append(findings, Finding{Code: "snapshot_digest_mismatch", Subject: snapshot.ID, Severity: SeverityError, Message: "catalog snapshot digest does not match its ordered records"})
	}
	if len(snapshot.ConfiguredVenues) == 0 {
		findings = append(findings, Finding{Code: "configured_venues_missing", Subject: snapshot.ID, Severity: SeverityError, Message: "the snapshot must record the configured venue set, including an empty native venue when applicable"})
	}
	return findings
}

type MappingStatus string

const (
	MappingReviewed   MappingStatus = "reviewed"
	MappingUnresolved MappingStatus = "unresolved"
)

type EntityKind string

const (
	EntityCompany     EntityKind = "company"
	EntitySecurity    EntityKind = "security"
	EntityFund        EntityKind = "fund"
	EntityCommodity   EntityKind = "commodity"
	EntityProtocol    EntityKind = "protocol"
	EntityDAO         EntityKind = "dao"
	EntityNetwork     EntityKind = "network"
	EntityModelVendor EntityKind = "model_vendor"
	EntityIndex       EntityKind = "index"
	EntityFX          EntityKind = "fx"
	EntityCrypto      EntityKind = "crypto"
	EntityOther       EntityKind = "other"
	EntityUnknown     EntityKind = "unknown"
)

type SourceClass string

const (
	SourceOfficialPrimary SourceClass = "official_primary"
	SourceVenuePrimary    SourceClass = "venue_primary"
	SourcePublicSecondary SourceClass = "public_secondary"
	SourceMarketData      SourceClass = "market_data"
	SourceLicensedVendor  SourceClass = "licensed_vendor"
	SourcePrivateWiki     SourceClass = "private_wiki"
)

type SourceVisibility string

const (
	VisibilityPublic          SourceVisibility = "public"
	VisibilityLicensedPrivate SourceVisibility = "licensed_private"
	VisibilityInternal        SourceVisibility = "internal"
)

type DisplayPermission string

const (
	DisplayPermissionUnknown    DisplayPermission = "unknown"
	DisplayPermissionAllowed    DisplayPermission = "allowed"
	DisplayPermissionProhibited DisplayPermission = "prohibited"
)

// SourceLocator contains stable provenance only. It has no title, author,
// path, quote, or source text.
type SourceLocator struct {
	Provider          string
	SourceID          string
	URL               string
	Class             SourceClass
	Visibility        SourceVisibility
	DisplayPermission DisplayPermission
	FixtureOnly       bool
}

type UnderlyingMapping struct {
	Key            ProviderInstrumentKey
	Status         MappingStatus
	EntityKind     EntityKind
	EntityKey      string
	EntityName     string
	IssuerCIK      string
	SecurityKey    string
	ExposureKey    string
	MarketSpecRefs []SourceLocator
	IdentityRefs   []SourceLocator
	Basis          string
	Reviewer       string
	Version        string
	VerifiedAt     time.Time
	FixtureOnly    bool
}

// ValidateMappings checks mapping provenance without inferring identity from
// ticker text, display names, or MarketKB aliases. Unresolved records remain
// explicit but cannot create an entity or exposure key.
func ValidateMappings(snapshot CatalogSnapshot, mappings []UnderlyingMapping, asOf time.Time) []Finding {
	known := make(map[string]bool, len(snapshot.Instruments))
	for _, instrument := range snapshot.Instruments {
		known[instrument.Key.String()] = true
	}
	seen := make(map[string]bool, len(mappings))
	var findings []Finding
	for _, mapping := range mappings {
		subject := mapping.Key.String()
		if !mapping.Key.valid() {
			findings = append(findings, Finding{Code: "mapping_key_invalid", Subject: subject, Severity: SeverityError, Message: "mapping must use the complete provider instrument key"})
		}
		if seen[subject] {
			findings = append(findings, Finding{Code: "duplicate_instrument_mapping", Subject: subject, Severity: SeverityError, Message: "an instrument has more than one underlying mapping"})
		}
		seen[subject] = true
		if !known[subject] {
			findings = append(findings, Finding{Code: "mapping_instrument_missing", Subject: subject, Severity: SeverityError, Message: "mapping does not match an instrument in this snapshot"})
		}
		switch mapping.Status {
		case MappingUnresolved:
			if mapping.EntityKey != "" || mapping.IssuerCIK != "" || mapping.SecurityKey != "" || mapping.ExposureKey != "" {
				findings = append(findings, Finding{Code: "unresolved_mapping_has_identity", Subject: subject, Severity: SeverityError, Message: "unresolved mapping cannot carry a canonical entity or exposure identity"})
			} else {
				findings = append(findings, Finding{Code: "underlying_unresolved", Subject: subject, Severity: SeverityWarning, Message: "instrument text does not establish its economic underlying"})
			}
		case MappingReviewed:
			findings = append(findings, validateReviewedMapping(mapping, asOf)...)
		default:
			findings = append(findings, Finding{Code: "mapping_status_invalid", Subject: subject, Severity: SeverityError, Message: "mapping status must be reviewed or unresolved"})
		}
	}
	return findingsSorted(findings)
}

func validateReviewedMapping(mapping UnderlyingMapping, asOf time.Time) []Finding {
	subject := mapping.Key.String()
	var findings []Finding
	if mapping.EntityKind == "" || mapping.EntityKind == EntityUnknown || !knownEntityKind(mapping.EntityKind) {
		findings = append(findings, Finding{Code: "mapping_entity_kind_invalid", Subject: subject, Severity: SeverityError, Message: "reviewed mapping needs a supported economic entity kind"})
	}
	if strings.TrimSpace(mapping.EntityKey) == "" || strings.TrimSpace(mapping.ExposureKey) == "" {
		findings = append(findings, Finding{Code: "mapping_identity_missing", Subject: subject, Severity: SeverityError, Message: "reviewed mapping needs stable entity and exposure keys"})
	}
	if strings.TrimSpace(mapping.EntityName) == "" {
		findings = append(findings, Finding{Code: "mapping_entity_name_missing", Subject: subject, Severity: SeverityError, Message: "reviewed mapping needs a human-readable entity name"})
	}
	if mapping.EntityKind == EntitySecurity && strings.TrimSpace(mapping.SecurityKey) == "" {
		findings = append(findings, Finding{Code: "mapping_security_key_missing", Subject: subject, Severity: SeverityError, Message: "security mappings need a stable security key separate from issuer identity"})
	}
	if mapping.EntityKind == EntityCompany && strings.TrimSpace(mapping.SecurityKey) == "" {
		findings = append(findings, Finding{Code: "mapping_security_key_missing", Subject: subject, Severity: SeverityError, Message: "company exposure mappings need the related security key where the instrument represents a security"})
	}
	if strings.TrimSpace(mapping.Version) == "" || strings.TrimSpace(mapping.Reviewer) == "" || strings.TrimSpace(mapping.Basis) == "" || mapping.VerifiedAt.IsZero() {
		findings = append(findings, Finding{Code: "mapping_review_provenance_missing", Subject: subject, Severity: SeverityError, Message: "reviewer, mapping version, basis, and verification time are required"})
	}
	if !asOf.IsZero() && !mapping.VerifiedAt.IsZero() && mapping.VerifiedAt.After(asOf) {
		findings = append(findings, Finding{Code: "mapping_verified_in_future", Subject: subject, Severity: SeverityError, Message: "mapping verification is later than proposal completion"})
	}
	if mappingFixtureOnly(mapping) {
		findings = append(findings, Finding{Code: "mapping_fixture_only", Subject: subject, Severity: SeverityWarning, Message: "mapping provenance is marked fixture-only and cannot establish live identity"})
	}
	if mapping.IssuerCIK != "" && !allDigits(mapping.IssuerCIK) {
		findings = append(findings, Finding{Code: "mapping_cik_invalid", Subject: subject, Severity: SeverityError, Message: "issuer CIK must contain digits only"})
	}
	if len(mapping.MarketSpecRefs) == 0 || !slices.ContainsFunc(mapping.MarketSpecRefs, func(ref SourceLocator) bool {
		return sourceRefValid(ref, SourceVenuePrimary)
	}) {
		findings = append(findings, Finding{Code: "mapping_market_spec_missing", Subject: subject, Severity: SeverityError, Message: "reviewed mapping needs a public venue or deployer specification locator"})
	}
	identitySourceValid := slices.ContainsFunc(mapping.IdentityRefs, func(ref SourceLocator) bool {
		if sourceRefValid(ref, SourceOfficialPrimary) {
			return true
		}
		// A provider-listed crypto market can be scoped to the exact venue
		// instrument observed in the immutable catalog; it need not pretend to
		// identify an issuer or a protocol via an external ticker mapping.
		return mapping.EntityKind == EntityCrypto && sourceRefValid(ref, SourceVenuePrimary)
	})
	if len(mapping.IdentityRefs) == 0 || !identitySourceValid {
		findings = append(findings, Finding{Code: "mapping_identity_source_missing", Subject: subject, Severity: SeverityError, Message: "reviewed mapping needs an authoritative public entity identity locator"})
	}
	return findings
}

func mappingFixtureOnly(mapping UnderlyingMapping) bool {
	if mapping.FixtureOnly {
		return true
	}
	for _, ref := range append(slices.Clone(mapping.MarketSpecRefs), mapping.IdentityRefs...) {
		if ref.FixtureOnly {
			return true
		}
	}
	return false
}

func knownEntityKind(kind EntityKind) bool {
	switch kind {
	case EntityCompany, EntitySecurity, EntityFund, EntityCommodity, EntityProtocol, EntityDAO, EntityNetwork, EntityModelVendor, EntityIndex, EntityFX, EntityCrypto, EntityOther:
		return true
	default:
		return false
	}
}

func sourceRefValid(ref SourceLocator, class SourceClass) bool {
	if ref.Class != class || ref.Visibility != VisibilityPublic || ref.DisplayPermission != DisplayPermissionAllowed || strings.TrimSpace(ref.Provider) == "" || strings.TrimSpace(ref.SourceID) == "" {
		return false
	}
	parsed, err := url.Parse(ref.URL)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

type CandidateAssessment struct {
	Instrument Instrument
	// Eligible means the current configured platform catalog and reviewed
	// identity support this instrument; it says nothing about the user's account.
	Eligible      bool
	AccountAccess AccountAccessStatus
	Findings      []Finding
}

// AssessCandidate checks one model-selected instrument against a persisted
// catalog and reviewed underlying mapping. Catalog presence never establishes
// user account access.
func AssessCandidate(snapshot CatalogSnapshot, key ProviderInstrumentKey, mapping *UnderlyingMapping, proposalCompletedAt time.Time) CandidateAssessment {
	return assessCandidate(snapshot, key, mapping, proposalCompletedAt, true)
}

// AssessMarketCandidate validates only the provider catalog row. It is useful
// for filtering candidate universes before an identity adapter has resolved
// the economic underlying; it does not claim that the asset maps to an issuer.
func AssessMarketCandidate(snapshot CatalogSnapshot, key ProviderInstrumentKey, proposalCompletedAt time.Time) CandidateAssessment {
	return assessCandidate(snapshot, key, nil, proposalCompletedAt, false)
}

func assessCandidate(snapshot CatalogSnapshot, key ProviderInstrumentKey, mapping *UnderlyingMapping, proposalCompletedAt time.Time, requireMapping bool) CandidateAssessment {
	result := CandidateAssessment{AccountAccess: AccountAccessUnknown}
	findings := ValidateCatalog(snapshot, proposalCompletedAt)
	var instrument *Instrument
	for i := range snapshot.Instruments {
		if snapshot.Instruments[i].Key == key {
			instrument = &snapshot.Instruments[i]
			break
		}
	}
	if instrument == nil {
		findings = append(findings, Finding{Code: "candidate_not_in_snapshot", Subject: key.String(), Severity: SeverityError, Message: "candidate was not present in the persisted catalog snapshot"})
	} else {
		result.Instrument = *instrument
		if key.Provider != snapshot.Provider {
			findings = append(findings, Finding{Code: "candidate_provider_mismatch", Subject: key.String(), Severity: SeverityError, Message: "candidate provider does not match the persisted catalog snapshot"})
		}
		if !slices.Contains(snapshot.ConfiguredVenues, key.Venue) {
			findings = append(findings, Finding{Code: "candidate_venue_not_configured", Subject: key.String(), Severity: SeverityError, Message: "candidate venue is outside the configured snapshot scope"})
		}
		findings = append(findings, validateInstrumentAt(*instrument, proposalCompletedAt)...)
		findings = append(findings, validateInstrumentNumbers(*instrument)...)
		if instrument.ListingStatus == "" {
			findings = append(findings, Finding{Code: "listing_status_missing", Subject: key.String(), Severity: SeverityError, Message: "listing status must state what the catalog observed"})
		} else if instrument.ListingStatus != ListingObserved && instrument.ListingStatus != DelistingObserved && instrument.ListingStatus != ListingUnknown {
			findings = append(findings, Finding{Code: "listing_status_invalid", Subject: key.String(), Severity: SeverityError, Message: "listing status is not a supported observed state"})
		}
		if instrument.ListingStatus != ListingObserved {
			findings = append(findings, Finding{Code: "candidate_not_currently_listed", Subject: key.String(), Severity: SeverityError, Message: "the catalog did not observe this instrument as listed"})
		}
		mark, err := strconv.ParseFloat(instrument.MarkPrice, 64)
		if err != nil || !(mark > 0) || isNonFinite(mark) {
			findings = append(findings, Finding{Code: "candidate_mark_unusable", Subject: key.String(), Severity: SeverityError, Message: "candidate needs a positive finite current mark"})
		}
		if instrument.Illiquid {
			findings = append(findings, Finding{Code: "candidate_illiquid", Subject: key.String(), Severity: SeverityError, Message: "illiquid instruments cannot fill a proposal holding"})
		}
		if !instrument.Liquidity.Classified {
			findings = append(findings, Finding{Code: "candidate_liquidity_classification_unknown", Subject: key.String(), Severity: SeverityError, Message: "candidate needs a current volume and open-interest classification using recorded floors"})
		}
		for _, depth := range []struct {
			name  string
			value string
		}{{"daily volume", instrument.Liquidity.DayVolumeUSD}, {"open interest", instrument.Liquidity.OpenInterestUSD}} {
			value, err := strconv.ParseFloat(depth.value, 64)
			if err == nil && value <= 0 {
				findings = append(findings, Finding{Code: "candidate_liquidity_depth_zero", Subject: key.String(), Severity: SeverityError, Message: depth.name + " is zero, so the instrument cannot fill a holding"})
			}
		}
		if instrument.AtOICap {
			findings = append(findings, Finding{Code: "candidate_at_oi_cap", Subject: key.String(), Severity: SeverityError, Message: "the venue currently refuses additions at this instrument's open-interest cap"})
		}
		if !instrument.AtOICapKnown {
			findings = append(findings, Finding{Code: "candidate_oi_cap_status_unknown", Subject: key.String(), Severity: SeverityError, Message: "candidate needs an explicit current open-interest cap status"})
		}
		if instrument.Thin {
			findings = append(findings, Finding{Code: "candidate_thin", Subject: key.String(), Severity: SeverityWarning, Message: "thin liquidity must be stated as a caveat"})
		}
		if instrument.OnlyIsolated {
			findings = append(findings, Finding{Code: "candidate_isolated_margin", Subject: key.String(), Severity: SeverityWarning, Message: "instrument margin is isolated-only"})
		}
		if strings.TrimSpace(instrument.MarginMode) == "" {
			findings = append(findings, Finding{Code: "candidate_margin_mode_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "margin mode is not available in this catalog observation"})
		}
		if strings.TrimSpace(instrument.CollateralToken) == "" {
			findings = append(findings, Finding{Code: "candidate_collateral_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "collateral requirements are not available in this catalog observation"})
		}
		if instrument.MaxLeverage == 0 {
			findings = append(findings, Finding{Code: "candidate_max_leverage_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "maximum leverage is not available in this catalog observation"})
		}
		if !instrument.WeekendPricingKnown {
			findings = append(findings, Finding{Code: "candidate_weekend_pricing_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "weekend pricing behavior is not available in this catalog observation"})
		} else if instrument.WeekendPricing {
			findings = append(findings, Finding{Code: "candidate_weekend_pricing", Subject: key.String(), Severity: SeverityWarning, Message: "venue weekend pricing behavior is an instrument-specific execution caveat"})
		}
		if !instrument.GrowthModeKnown {
			findings = append(findings, Finding{Code: "candidate_growth_mode_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "deployer growth mode is not available in this catalog observation"})
		} else if instrument.GrowthMode {
			findings = append(findings, Finding{Code: "candidate_growth_mode", Subject: key.String(), Severity: SeverityWarning, Message: "deployer growth mode changes market risk and should be stated"})
		}
		if key.Venue != "" && strings.TrimSpace(instrument.DeployerFeeScale) == "" {
			findings = append(findings, Finding{Code: "candidate_deployer_fee_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "deployer-specific fee scale is not available in this catalog observation"})
		} else if strings.TrimSpace(instrument.DeployerFeeScale) != "" {
			findings = append(findings, Finding{Code: "candidate_deployer_fee_rule", Subject: key.String(), Severity: SeverityInfo, Message: "deployer fee scale is instrument-specific; retain it with the proposal"})
		}
		if strings.TrimSpace(instrument.DeployerNote) != "" {
			findings = append(findings, Finding{Code: "candidate_deployer_note", Subject: key.String(), Severity: SeverityWarning, Message: "deployer-provided contract note is an instrument-specific caveat"})
		}
		if strings.TrimSpace(instrument.FundingRate) == "" {
			findings = append(findings, Finding{Code: "candidate_funding_unknown", Subject: key.String(), Severity: SeverityWarning, Message: "current funding is unavailable and cannot be projected over the horizon"})
		}
	}
	if requireMapping {
		switch {
		case mapping == nil:
			findings = append(findings, Finding{Code: "candidate_underlying_unresolved", Subject: key.String(), Severity: SeverityError, Message: "candidate has no reviewed economic-underlying mapping"})
		case mapping.Key != key:
			findings = append(findings, Finding{Code: "candidate_mapping_key_mismatch", Subject: key.String(), Severity: SeverityError, Message: "underlying mapping is for a different provider instrument"})
		case mapping.Status != MappingReviewed:
			findings = append(findings, Finding{Code: "candidate_underlying_unresolved", Subject: key.String(), Severity: SeverityError, Message: "candidate underlying mapping has not been reviewed"})
		default:
			findings = append(findings, validateReviewedMapping(*mapping, proposalCompletedAt)...)
			if mappingFixtureOnly(*mapping) {
				findings = append(findings, Finding{Code: "candidate_mapping_fixture_only", Subject: key.String(), Severity: SeverityWarning, Message: "underlying mapping uses fixture-only provenance and is not live-eligible"})
			}
		}
	}
	if snapshot.FixtureOnly {
		findings = append(findings, Finding{Code: "candidate_catalog_fixture_only", Subject: key.String(), Severity: SeverityWarning, Message: "catalog snapshot is fixture-only and is not live-eligible"})
	}
	result.Findings = findingsSorted(findings)
	result.Eligible = !hasErrors(result.Findings)
	return result
}

func validateInstrumentAt(instrument Instrument, at time.Time) []Finding {
	subject := instrument.Key.String()
	var findings []Finding
	if at.IsZero() {
		return []Finding{{Code: "proposal_completion_time_missing", Subject: subject, Severity: SeverityError, Message: "proposal completion time is required for candidate freshness"}}
	}
	for _, observation := range []struct {
		name string
		at   time.Time
	}{{"instrument", instrument.ObservedAt}, {"liquidity", instrument.Liquidity.ObservedAt}} {
		switch {
		case observation.at.IsZero():
			findings = append(findings, Finding{Code: observation.name + "_observation_missing", Subject: subject, Severity: SeverityError, Message: observation.name + " observation time is required"})
		case observation.at.After(at):
			findings = append(findings, Finding{Code: observation.name + "_observation_in_future", Subject: subject, Severity: SeverityError, Message: observation.name + " observation is later than proposal completion"})
		case at.Sub(observation.at) > MaxCatalogAge:
			findings = append(findings, Finding{Code: observation.name + "_observation_stale", Subject: subject, Severity: SeverityError, Message: observation.name + " observation is older than one minute at proposal completion"})
		}
	}
	return findings
}

func validateInstrumentNumbers(instrument Instrument) []Finding {
	subject := instrument.Key.String()
	var findings []Finding
	for _, field := range []struct {
		name          string
		value         string
		allowNegative bool
		positive      bool
	}{
		{name: "mark_price", value: instrument.MarkPrice, positive: true},
		{name: "oracle_price", value: instrument.OraclePrice, positive: true},
		{name: "funding_rate", value: instrument.FundingRate, allowNegative: true},
		{name: "open_interest", value: instrument.OpenInterest},
		{name: "day_notional_usd", value: instrument.DayNotionalUSD},
		{name: "liquidity_day_volume_usd", value: instrument.Liquidity.DayVolumeUSD},
		{name: "liquidity_open_interest_usd", value: instrument.Liquidity.OpenInterestUSD},
		{name: "oi_cap_usd", value: instrument.OICapUSD},
		{name: "deployer_fee_scale", value: instrument.DeployerFeeScale},
	} {
		if strings.TrimSpace(field.value) == "" {
			continue
		}
		value, err := strconv.ParseFloat(field.value, 64)
		if err != nil || isNonFinite(value) || (!field.allowNegative && value < 0) || (field.positive && value <= 0) {
			findings = append(findings, Finding{Code: "instrument_numeric_field_invalid", Subject: subject, Severity: SeverityError, Message: field.name + " must be a finite value with a valid sign"})
		}
	}
	if instrument.MarketType == "" {
		findings = append(findings, Finding{Code: "instrument_market_type_missing", Subject: subject, Severity: SeverityError, Message: "catalog instrument needs its observed market type"})
	}
	if strings.TrimSpace(instrument.Liquidity.DayVolumeUSD) == "" || strings.TrimSpace(instrument.Liquidity.OpenInterestUSD) == "" || strings.TrimSpace(instrument.Liquidity.FloorConfiguration) == "" {
		findings = append(findings, Finding{Code: "liquidity_classification_inputs_missing", Subject: subject, Severity: SeverityError, Message: "liquidity classification needs volume, open interest, and the floor configuration used"})
	}
	if !instrument.Liquidity.Classified {
		findings = append(findings, Finding{Code: "liquidity_classification_unknown", Subject: subject, Severity: SeverityError, Message: "snapshot needs the classification result produced with the recorded liquidity floors"})
	}
	if instrument.MaxLeverage < 0 {
		findings = append(findings, Finding{Code: "instrument_leverage_invalid", Subject: subject, Severity: SeverityError, Message: "maximum leverage cannot be negative"})
	}
	return findings
}

func isNonFinite(value float64) bool { return math.IsNaN(value) || math.IsInf(value, 0) }

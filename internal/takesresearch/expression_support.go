package takesresearch

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

const (
	maxBasketResearchCandidates = 8
	defaultHorizonDays          = 365
)

// ResearchPlan is the small local universe used to reconcile explicit user
// allocation instructions to the companies in the model's expression plan.
type ResearchPlan struct {
	BusinessRoles []PlannedBusinessRole
	Candidates    []ResearchPoolCandidate
}

type PlannedBusinessRole struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Required    bool   `json:"required,omitempty"`
	Explanation string `json:"explanation,omitempty"`
}

type ResearchPoolCandidate struct {
	ID              string   `json:"id"`
	IssuerName      string   `json:"issuerName"`
	Aliases         []string `json:"aliases,omitempty"`
	BusinessRoleIDs []string `json:"businessRoleIds,omitempty"`
}

// ResearchInstrument is the exact catalog entry used by product/access
// checks. Catalog membership does not establish access in a user's account.
type ResearchInstrument struct {
	Key         takeintelligence.ProviderInstrumentKey `json:"key"`
	DisplayName string                                 `json:"displayName"`
	MarketType  string                                 `json:"marketType"`
	Category    string                                 `json:"category"`
	Listing     takeintelligence.ListingStatus         `json:"listing,omitempty"`
	Conditions  []string                               `json:"conditions,omitempty"`
	Unknown     []string                               `json:"unknown,omitempty"`
}

type CandidateMappingStatus string

const (
	CandidateMappingNotChecked CandidateMappingStatus = "NOT_CHECKED"
	CandidateMappingMapped     CandidateMappingStatus = "MAPPED"
	CandidateMappingNoKey      CandidateMappingStatus = "NO_ELIGIBLE_KEY"
	CandidateMappingFailed     CandidateMappingStatus = "MAPPING_ERROR"
)

type CandidateCatalogAssessment string

const (
	CandidateCatalogNotChecked            CandidateCatalogAssessment = "NOT_CHECKED"
	CandidateCatalogAbsent                CandidateCatalogAssessment = "ABSENT"
	CandidateCatalogTickerMatchUnverified CandidateCatalogAssessment = "TICKER_MATCH_UNVERIFIED"
	CandidateCatalogIssuerMatched         CandidateCatalogAssessment = "ISSUER_MATCHED"
)

type CandidateCatalogNameMatchMethod string

const (
	CandidateCatalogNameMatchOfficialConcordance CandidateCatalogNameMatchMethod = "SEC_OFFICIAL_NAME_CONCORDANCE"
	CandidateCatalogNameMatchUniqueNameToken     CandidateCatalogNameMatchMethod = "SEC_UNIQUE_OFFICIAL_NAME_TOKEN"
)

type CandidateCatalogNameMatch struct {
	Key    takeintelligence.ProviderInstrumentKey
	Method CandidateCatalogNameMatchMethod
}

type CandidateInstrumentMapping struct {
	CandidateID        string                                   `json:"candidateId"`
	Status             CandidateMappingStatus                   `json:"status"`
	Keys               []takeintelligence.ProviderInstrumentKey `json:"keys"`
	ExposureKey        string                                   `json:"exposureKey,omitempty"`
	EntityName         string                                   `json:"entityName,omitempty"`
	CatalogAssessment  CandidateCatalogAssessment               `json:"-"`
	CatalogNameMatches []CandidateCatalogNameMatch              `json:"-"`
	ReviewedMappings   []takeintelligence.UnderlyingMapping     `json:"-"`
}

type AllocationConstraintStatus string

const (
	AllocationConstraintsUnavailable AllocationConstraintStatus = "UNAVAILABLE"
	AllocationConstraintsNone        AllocationConstraintStatus = "NO_EXPLICIT_ALLOCATION"
	AllocationConstraintsResolved    AllocationConstraintStatus = "RESOLVED"
	AllocationConstraintsUnresolved  AllocationConstraintStatus = "UNRESOLVED"
)

type AllocationConstraintContext struct {
	Status       AllocationConstraintStatus `json:"status"`
	Policy       string                     `json:"policy,omitempty"`
	Targets      []AllocationConstraint     `json:"targets"`
	CashPercent  *float64                   `json:"cashPercent,omitempty"`
	SourceIDs    []string                   `json:"-"`
	CoverageNote string                     `json:"coverageNote,omitempty"`
}

type AllocationConstraint struct {
	TargetKind string  `json:"targetKind"`
	TargetID   string  `json:"targetId"`
	Percent    float64 `json:"percent"`
}

func validResearchQuestionID(value string) bool {
	if value == "" || len(value) > 24 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func candidateMatchesExclusion(candidate ResearchPoolCandidate, exclusions []string) bool {
	for _, exclusion := range exclusions {
		if sameIdentityPhrase(exclusion, candidate.IssuerName) {
			return true
		}
		for _, alias := range candidate.Aliases {
			if sameIdentityPhrase(exclusion, alias) {
				return true
			}
		}
	}
	return false
}

func hasError(findings []takeintelligence.Finding) bool {
	for _, finding := range findings {
		if finding.Severity == takeintelligence.SeverityError {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			clean = append(clean, strings.TrimSpace(value))
		}
	}
	slices.Sort(clean)
	return slices.Compact(clean)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func formatPercent(value float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", value), "0"), ".")
}

func finiteCashPercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func joinPhrases(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

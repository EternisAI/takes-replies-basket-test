package takesreply

import (
	"encoding/json"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takesresearch"
	"github.com/google/uuid"
)

type TurnInput struct {
	ThreadID         string
	ExpectedRevision int
	RequestID        uuid.UUID
	Message          *string
	SelectedChoiceID *string
}

type TurnResult struct {
	Outcome          string          `json:"outcome"`
	Conversation     json.RawMessage `json:"conversation"`
	AssistantMessage *string         `json:"assistantMessage,omitempty"`
	Decision         string          `json:"decision"`
	DecisionPoint    json.RawMessage `json:"decisionPoint"`
	EvidenceCheck    json.RawMessage `json:"evidenceCheck,omitempty"`
}

type Draft struct {
	ID                   string                              `json:"id"`
	ThreadID             string                              `json:"threadId"`
	Revision             int                                 `json:"revision"`
	Stage                string                              `json:"stage"`
	CreatedAt            time.Time                           `json:"createdAt"`
	UpdatedAt            time.Time                           `json:"updatedAt"`
	Belief               Belief                              `json:"belief"`
	Facts                []Fact                              `json:"facts"`
	Assumptions          []Assumption                        `json:"assumptions"`
	Horizon              Horizon                             `json:"horizon"`
	Choices              []Choice                            `json:"choices"`
	SelectedChoiceIDs    []string                            `json:"selectedChoiceIds"`
	SelectedCompanyScope *takesresearch.SelectedCompanyScope `json:"selectedCompanyScope,omitempty"`
	SelectionCriteria    []Criterion                         `json:"selectionCriteria"`
	Exclusions           []string                            `json:"exclusions"`
	RequestedHoldings    *int                                `json:"requestedHoldings,omitempty"`
	CashConstraint       *CashConstraint                     `json:"cashConstraint,omitempty"`
	Preferences          []Preference                        `json:"preferences"`
}

type Belief struct {
	UserStatement       *string              `json:"userStatement,omitempty"`
	CausalMechanism     *string              `json:"causalMechanism,omitempty"`
	SourceMessageID     *uuid.UUID           `json:"sourceMessageId,omitempty"`
	IntentStatus        string               `json:"intentStatus"`
	InvestmentThesis    InvestmentThesis     `json:"investmentThesis"`
	PersonalPreferences []PersonalPreference `json:"personalPreferences"`
}

type InvestmentThesis struct {
	Statement       *string    `json:"statement,omitempty"`
	CausalMechanism *string    `json:"causalMechanism,omitempty"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
	Status          string     `json:"status"`
}

type PersonalPreference struct {
	Subject         string    `json:"subject"`
	Preference      string    `json:"preference"`
	SourceMessageID uuid.UUID `json:"sourceMessageId"`
}

type Fact struct {
	ID              string          `json:"id"`
	RawText         string          `json:"rawText"`
	NormalizedValue json.RawMessage `json:"normalizedValue,omitempty"`
	SourceMessageID uuid.UUID       `json:"sourceMessageId"`
	CapturedAt      time.Time       `json:"capturedAt"`
	Status          string          `json:"status"`
}

type Assumption struct {
	ID              string     `json:"id"`
	Text            string     `json:"text"`
	Rationale       string     `json:"rationale"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
	Confirmed       bool       `json:"confirmed"`
	Status          string     `json:"status"`
}

type Horizon struct {
	Days            int        `json:"days"`
	Origin          string     `json:"origin"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
}

type Choice struct {
	CompanyScope   *takesresearch.CompanyScope `json:"companyScope,omitempty"`
	ID             string                      `json:"id"`
	Label          string                      `json:"label"`
	ExposureRole   string                      `json:"exposureRole"`
	Description    string                      `json:"description"`
	InstrumentRefs []string                    `json:"instrumentRefs"`
	Tradeoffs      []string                    `json:"tradeoffs"`
}

type Criterion struct {
	Criterion       string     `json:"criterion"`
	SourceMessageID *uuid.UUID `json:"sourceMessageId,omitempty"`
	Status          string     `json:"status"`
}

type CashConstraint struct {
	Percent         float64   `json:"percent"`
	Denominator     string    `json:"denominator"`
	SourceMessageID uuid.UUID `json:"sourceMessageId"`
}

type Preference struct {
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	Origin   string          `json:"origin"`
	Enforced bool            `json:"enforced"`
}

type Conversation struct {
	ThreadID          string    `json:"threadId"`
	ThreadKind        string    `json:"threadKind"`
	Draft             Draft     `json:"draft"`
	Messages          []Message `json:"messages"`
	MessagesTruncated bool      `json:"messagesTruncated"`
	Build             *Build    `json:"build,omitempty"`
}

type Message struct {
	ID      uuid.UUID `json:"id"`
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

type TurnModelOutput struct {
	AssistantMessage  string                   `json:"assistantMessage"`
	Decision          string                   `json:"decision" jsonschema:"enum=REPLY,enum=NEEDS_CLARIFICATION,enum=READY_TO_BUILD"`
	BuildRequested    bool                     `json:"buildRequested"`
	Stage             string                   `json:"stage" jsonschema:"enum=UNDERSTANDING,enum=CHOICES,enum=DEVELOPING,enum=READY_TO_BUILD,enum=NEEDS_INPUT"`
	Belief            BeliefOutput             `json:"belief"`
	Horizon           HorizonOutput            `json:"horizon"`
	Choices           []ChoiceOutput           `json:"choices" jsonschema:"maxItems=4"`
	SelectedChoiceIDs []string                 `json:"selectedChoiceIds" jsonschema:"maxItems=6"`
	Criteria          []string                 `json:"criteria" jsonschema:"maxItems=12"`
	Exclusions        []string                 `json:"exclusions" jsonschema:"maxItems=20"`
	RequestedHoldings *RequestedHoldingsOutput `json:"requestedHoldings"`
	CashConstraint    *CashConstraintOutput    `json:"cashConstraint"`
	Preferences       []PreferenceOutput       `json:"preferences" jsonschema:"maxItems=20"`
}

type BeliefOutput struct {
	UserStatement       *string                    `json:"userStatement"`
	CausalMechanism     *string                    `json:"causalMechanism"`
	IntentStatus        string                     `json:"intentStatus" jsonschema:"enum=PERSONAL_PREFERENCE,enum=INVESTMENT_THESIS,enum=MIXED,enum=UNCLEAR"`
	ThesisStatement     *string                    `json:"thesisStatement"`
	ThesisMechanism     *string                    `json:"thesisMechanism"`
	ThesisStatus        string                     `json:"thesisStatus" jsonschema:"enum=USER_STATED,enum=INFERRED_UNCONFIRMED,enum=NOT_ESTABLISHED"`
	PersonalPreferences []PersonalPreferenceOutput `json:"personalPreferences" jsonschema:"maxItems=8"`
}

type PersonalPreferenceOutput struct {
	Subject    string `json:"subject"`
	Preference string `json:"preference"`
}

type HorizonOutput struct {
	Days          *int `json:"days"`
	UserSpecified bool `json:"userSpecified"`
}

type ChoiceOutput struct {
	CompanyScope takesresearch.CompanyScope `json:"companyScope"`
	Label        string                     `json:"label" jsonschema:"maxLength=56"`
	ExposureRole string                     `json:"exposureRole" jsonschema:"maxLength=48"`
	Description  string                     `json:"description" jsonschema:"maxLength=180"`
	Tradeoff     string                     `json:"tradeoff" jsonschema:"maxLength=160"`
}

type CashConstraintOutput struct {
	Percent       float64 `json:"percent"`
	Denominator   string  `json:"denominator"`
	UserSpecified bool    `json:"userSpecified"`
}

type RequestedHoldingsOutput struct {
	Count         *int `json:"count"`
	UserSpecified bool `json:"userSpecified"`
}

type PreferenceOutput struct {
	Key      string                `json:"key"`
	Value    PreferenceValueOutput `json:"value"`
	Enforced bool                  `json:"enforced"`
}

type EvidenceCheckOutput struct {
	AsOfUTC        time.Time             `json:"-"`
	Status         string                `json:"status"`
	CheckedAt      *time.Time            `json:"checkedAt"`
	SearchRequests *int                  `json:"searchRequests,omitempty"`
	Sources        []EvidenceCheckSource `json:"sources"`
	Facts          []EvidenceCheckFact   `json:"facts"`
	Limitation     *string               `json:"limitation"`
}

type HistoricalEvidenceOutput struct {
	Sources []EvidenceCheckSource `json:"sources"`
	Facts   []EvidenceCheckFact   `json:"facts"`
}

type EvidenceCheckSource struct {
	SourceRef          string     `json:"sourceRef"`
	PublicURL          *string    `json:"publicUrl"`
	Title              string     `json:"title,omitempty"`
	Excerpt            string     `json:"excerpt,omitempty"`
	DataSource         string     `json:"dataSource,omitempty"`
	PageSummary        string     `json:"pageSummary,omitempty"`
	PageUpdatedOn      string     `json:"pageUpdatedOn,omitempty"`
	PublishedAt        *time.Time `json:"publishedAt,omitempty"`
	RetrievedAt        time.Time  `json:"retrievedAt"`
	PageSnapshotStatus string     `json:"pageSnapshotStatus,omitempty"`
}

type EvidenceTemporalStatus string

const (
	EvidenceTemporalExcluded       EvidenceTemporalStatus = "EXCLUDED"
	EvidenceTemporalCurrent        EvidenceTemporalStatus = "CURRENT_USE"
	EvidenceTemporalHistoricalOnly EvidenceTemporalStatus = "HISTORICAL_ONLY"
)

type StockAnalysisPageKind string

const (
	StockAnalysisPageStatistics StockAnalysisPageKind = "statistics"
	StockAnalysisPageForecast   StockAnalysisPageKind = "forecast"
)

type EvidenceMetricKind string

const (
	EvidenceMetricForwardPE    EvidenceMetricKind = "FORWARD_PE"
	EvidenceMetricEPSGrowth3Y  EvidenceMetricKind = "EPS_GROWTH_FORECAST_3Y"
	EvidenceMetricEPSGrowth    EvidenceMetricKind = "EPS_GROWTH_ESTIMATE"
	EvidenceMetricEPS          EvidenceMetricKind = "EPS_ESTIMATE"
	EvidenceMetricAnalystCount EvidenceMetricKind = "ANALYST_ESTIMATE_COUNT"
)

type EvidenceAttributionKind string

const (
	EvidenceAttributionSourceAggregateForecast EvidenceAttributionKind = "SOURCE_LABELED_AGGREGATE_FORECAST"
)

type EvidenceBasisKind string

const (
	EvidenceBasisAdjustedNonGAAP EvidenceBasisKind = "NON_GAAP_ADJUSTED_EPS"
)

const (
	StockAnalysisSnapshotCurrent    = "CURRENT_PAGE_SNAPSHOT"
	StockAnalysisSnapshotHistorical = "HISTORICAL_PAGE_SNAPSHOT"
)

type EvidenceCheckFact struct {
	Claim       string     `json:"claim"`
	Value       string     `json:"value"`
	Unit        *string    `json:"unit"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
	ObservedAt  *time.Time `json:"observedAt"`
	// Date precision is emitted only when the original source input was a literal calendar date.
	ObservedAtPrecision string                  `json:"observedAtPrecision,omitempty"`
	SourceRef           string                  `json:"sourceRef"`
	MetricKind          EvidenceMetricKind      `json:"metricKind,omitempty"`
	BasisKind           EvidenceBasisKind       `json:"basisKind,omitempty"`
	BasisLabel          string                  `json:"basisLabel,omitempty"`
	AttributionKind     EvidenceAttributionKind `json:"attributionKind,omitempty"`
	AttributionLabel    string                  `json:"attributionLabel,omitempty"`
	// HistoricalOnly is set by a typed fixture/archive adapter, never inferred from prose.
	HistoricalOnly bool                   `json:"-"`
	TemporalStatus EvidenceTemporalStatus `json:"temporalStatus,omitempty"`
	// PageKind proves the fact came from the bounded, exact-kind StockAnalysis parser.
	PageKind                StockAnalysisPageKind `json:"-"`
	ForecastTargetPeriodEnd *time.Time            `json:"forecastTargetPeriodEnd,omitempty"`
}

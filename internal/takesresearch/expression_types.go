package takesresearch

import (
	"context"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

const (
	ExpressionMode            = "EXPLORATORY_EXPRESSION_V1"
	DeepResearchMode          = "DEEP_RESEARCH_V1"
	maxExpressionRoles        = 6
	maxExpressionCompanies    = 20
	maxExpressionDiscoveries  = 3
	expressionStoryVersion    = "CORE_DISCOVERIES_V1"
	expressionTokens          = 6144
	expressionIdentityTimeout = 8 * time.Second
	expressionDenominator     = "PROPOSED_EXPOSURE_MIX"
)

type ExpressionRolePlan struct {
	ID                string `json:"id" jsonschema:"maxLength=24"`
	Label             string `json:"label" jsonschema:"maxLength=60"`
	ProposedMechanism string `json:"proposedMechanism" jsonschema:"maxLength=160"`
	ExposureKind      string `json:"exposureKind" jsonschema:"enum=DIRECT,enum=ADJACENT"`
	WhyThisRole       string `json:"whyThisRole" jsonschema:"maxLength=120"`
	PlannedMixPercent int    `json:"plannedMixPercent" jsonschema:"minimum=1,maximum=100"`
}
type ExpressionCompanyPlan struct {
	ID                      string `json:"id" jsonschema:"maxLength=24"`
	IssuerName              string `json:"issuerName" jsonschema:"maxLength=120"`
	RoleID                  string `json:"roleId" jsonschema:"maxLength=24"`
	ProposedConnection      string `json:"proposedConnection" jsonschema:"maxLength=160"`
	PlannedMixPercent       int    `json:"plannedMixPercent" jsonschema:"minimum=1,maximum=100"`
	WhyThisShare            string `json:"whyThisShare" jsonschema:"maxLength=140"`
	PortfolioRole           string `json:"portfolioRole" jsonschema:"enum=CORE,enum=DISCOVERY"`
	DistinctiveContribution string `json:"distinctiveContribution" jsonschema:"maxLength=140"`
	ExtraAssumption         string `json:"extraAssumption" jsonschema:"maxLength=140"`
}
type ExpressionConsideration struct {
	Kind        string `json:"kind" jsonschema:"enum=ALTERNATIVE,enum=MISSING_ANGLE"`
	Description string `json:"description" jsonschema:"maxLength=160"`
	Tradeoff    string `json:"tradeoff" jsonschema:"maxLength=160"`
}
type ExpressionStoryPlan struct {
	Title             string                   `json:"title" jsonschema:"maxLength=80"`
	ConditionalThesis string                   `json:"conditionalThesis" jsonschema:"maxLength=320"`
	WhyThisMix        string                   `json:"whyThisMix" jsonschema:"maxLength=320"`
	AlternativeOrGap  *ExpressionConsideration `json:"alternativeOrGap" jsonschema:"nullable"`
}
type ExpressionStory struct {
	Version string `json:"version"`
	Basis   string `json:"basis"`
	ExpressionStoryPlan
}
type ExpressionOverlap struct {
	RoleIDs            []string `json:"roleIds" jsonschema:"maxItems=6"`
	ProposedDependency string   `json:"proposedDependency" jsonschema:"maxLength=320"`
}
type ExpressionPlan struct {
	Story        ExpressionStoryPlan     `json:"story"`
	Roles        []ExpressionRolePlan    `json:"roles" jsonschema:"minItems=1,maxItems=6"`
	Companies    []ExpressionCompanyPlan `json:"companies" jsonschema:"minItems=1,maxItems=20"`
	OverlapRisks []ExpressionOverlap     `json:"overlapRisks" jsonschema:"maxItems=6"`
}
type ExpressionPlanningInput struct {
	Idea        ResearchIdea `json:"idea"`
	Exclusions  []string     `json:"confirmedExclusions"`
	HorizonDays *int         `json:"confirmedHorizonDays"`
	RiskStyle   string       `json:"confirmedRiskStyle,omitempty"`
}
type ExpressionPlanner interface {
	PlanExpression(context.Context, ExpressionPlanningInput) (ExpressionPlan, error)
}

type ExpressionIdentity struct {
	Status       string     `json:"status"`
	OfficialName string     `json:"officialName,omitempty"`
	CIK          string     `json:"cik,omitempty"`
	RetrievedAt  *time.Time `json:"retrievedAt,omitempty"`
	PublicURL    string     `json:"publicUrl,omitempty"`
}
type ExpressionRole struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	ProposedMechanism string   `json:"proposedMechanism"`
	ExposureKind      string   `json:"exposureKind"`
	WhyThisRole       string   `json:"whyThisRole"`
	PlannedMixPercent *float64 `json:"plannedMixPercent"`
}
type ExpressionCompany struct {
	PortfolioRole           string             `json:"portfolioRole,omitempty"`
	DistinctiveContribution string             `json:"distinctiveContribution,omitempty"`
	ExtraAssumption         string             `json:"extraAssumption,omitempty"`
	WeightBasis             string             `json:"weightBasis"`
	ID                      string             `json:"id"`
	IssuerName              string             `json:"issuerName"`
	RoleID                  string             `json:"roleId"`
	ProposedConnection      string             `json:"proposedConnection"`
	WhyThisShare            string             `json:"whyThisShare"`
	Basis                   string             `json:"basis"`
	SourceSupport           string             `json:"sourceSupport"`
	PlannedMixPercent       *float64           `json:"plannedMixPercent"`
	Identity                ExpressionIdentity `json:"identity"`
	Availability            string             `json:"availability"`
}
type ExpressionContract struct {
	CompanyID          string                                 `json:"companyId"`
	Key                takeintelligence.ProviderInstrumentKey `json:"key"`
	Mapping            takeintelligence.UnderlyingMapping     `json:"mapping"`
	PlannedMixPercent  *float64                               `json:"plannedMixPercent"`
	TotalBudgetPercent *float64                               `json:"totalBudgetPercent"`
	AccountAccess      string                                 `json:"accountAccess"`
}
type ExpressionMix struct {
	ConstraintSourceIDs []string `json:"constraintSourceIds,omitempty"`
	Status              string   `json:"status"`
	Denominator         string   `json:"denominator"`
	AvailablePercent    *float64 `json:"availablePercent"`
	UnassignedPercent   *float64 `json:"unassignedPercent"`
	CashPercent         *float64 `json:"cashPercent"`
	TotalBudgetKnown    bool     `json:"totalBudgetKnown"`
}
type ExpressionView struct {
	Story              *ExpressionStory      `json:"story,omitempty"`
	MissingCompanies   []string              `json:"missingCompanies"`
	Kind               string                `json:"kind"`
	Readiness          string                `json:"readiness"`
	Basis              string                `json:"basis"`
	Belief             ResearchIdea          `json:"belief"`
	Scope              *SelectedCompanyScope `json:"scope"`
	Roles              []ExpressionRole      `json:"roles"`
	Companies          []ExpressionCompany   `json:"companies"`
	AvailableContracts []ExpressionContract  `json:"availableContracts"`
	Mix                ExpressionMix         `json:"mix"`
	OverlapRisks       []ExpressionOverlap   `json:"overlapRisks"`
	HorizonDays        *int                  `json:"horizonDays"`
	Limitations        []string              `json:"limitations"`
}

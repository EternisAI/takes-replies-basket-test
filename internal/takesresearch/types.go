// Package takesresearch builds the default read-only exploratory expression
// from a persisted Takes draft and exact market-catalog snapshots.
package takesresearch

import (
	"context"
	"encoding/json"
	"time"

	"github.com/EternisAI/takes-replies-basket-test/internal/takeintelligence"
)

type BuildStatus string

const (
	StatusReady   BuildStatus = "READY"
	StatusPartial BuildStatus = "PARTIAL"
	StatusFailed  BuildStatus = "FAILED"
)

// Input carries the exact start snapshot captured for the build. Message
// history is used only to verify saved constraints and explicit sizing.
type Input struct {
	ConstructionMode     string
	BuildID              string
	ThreadID             string
	UserID               string
	BuildCreatedAt       time.Time
	DraftJSON            json.RawMessage
	StartSnapshot        takeintelligence.CatalogSnapshot
	UserMessages         []PersistedUserMessage
	UserMessagesLoaded   bool
	UserMessagesComplete bool
}

type PersistedUserMessage struct {
	ID        string
	ThreadID  string
	UserID    string
	Role      string
	Content   string
	CreatedAt time.Time
}

// Output returns the completion snapshot for atomic persistence with the
// durable build record. The public proposal stays explicitly exploratory.
type Output struct {
	Status              BuildStatus                      `json:"status"`
	AsOf                time.Time                        `json:"asOf,omitempty"`
	CompletionSnapshot  takeintelligence.CatalogSnapshot `json:"-"`
	ProposalJSON        json.RawMessage                  `json:"proposal,omitempty"`
	MissingRequirements []string                         `json:"missingRequirements,omitempty"`
	AssessmentJSON      json.RawMessage                  `json:"assessment,omitempty"`
}

// CatalogReader captures one immutable read-only market catalog.
type CatalogReader interface {
	CaptureCatalog(context.Context) (takeintelligence.CatalogSnapshot, error)
}

type ProposalDraft struct {
	RiskStyle       takeintelligence.RiskStyle
	HorizonDays     int
	HorizonOrigin   string
	PreferenceNotes []string
}

type PublicFinding struct {
	Code     string                           `json:"code"`
	Severity takeintelligence.FindingSeverity `json:"severity"`
	Message  string                           `json:"message"`
}

type ProposalAssessmentView struct {
	Complete     bool                            `json:"complete"`
	LiveEligible bool                            `json:"liveEligible"`
	Ready        bool                            `json:"ready"`
	Shape        takeintelligence.PortfolioShape `json:"shape"`
	Findings     []PublicFinding                 `json:"findings"`
}

// ProposalView is the narrow public projection for this construction mode.
// The raw empty arrays preserve the original JSON contract that exploratory
// output contains no sourced holdings, evidence, or monitoring playbook.
type ProposalView struct {
	ConstructionMode     string                 `json:"constructionMode,omitempty"`
	Expression           *ExpressionView        `json:"expression,omitempty"`
	Status               string                 `json:"status"`
	BuildID              string                 `json:"buildId"`
	StartSnapshotID      string                 `json:"startSnapshotId"`
	StartSnapshotAt      time.Time              `json:"startSnapshotAt"`
	CompletionSnapshotID string                 `json:"completionSnapshotId"`
	Interpretation       string                 `json:"interpretation"`
	InterpretationStatus string                 `json:"interpretationStatus"`
	AsOf                 time.Time              `json:"asOf"`
	Holdings             []json.RawMessage      `json:"holdings"`
	Evidence             []json.RawMessage      `json:"evidence"`
	Playbook             []json.RawMessage      `json:"playbook"`
	Assessment           ProposalAssessmentView `json:"assessment"`
}

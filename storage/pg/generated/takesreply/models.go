// Code generated for the standalone reply schema. This file intentionally
// contains only models used by the Takes reply SQL queries.
package takesreply

import (
	"time"

	"github.com/google/uuid"
)

type TakesReplyBuild struct {
	ID                  uuid.UUID  `json:"id"`
	ThreadID            string     `json:"thread_id"`
	UserID              string     `json:"user_id"`
	ReplyRevision       int32      `json:"reply_revision"`
	RequestID           uuid.UUID  `json:"request_id"`
	SelectedChoiceID    string     `json:"selected_choice_id"`
	Snapshot            []byte     `json:"snapshot"`
	SnapshotHash        string     `json:"snapshot_hash"`
	Status              string     `json:"status"`
	StartSnapshot       []byte     `json:"start_snapshot"`
	CompletionSnapshot  []byte     `json:"completion_snapshot"`
	Proposal            []byte     `json:"proposal"`
	Assessment          []byte     `json:"assessment"`
	MissingRequirements []byte     `json:"missing_requirements"`
	FailureMessage      *string    `json:"failure_message"`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	LeaseUntil          *time.Time `json:"lease_until"`
}

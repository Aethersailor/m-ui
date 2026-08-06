package domain

import "time"

const StateSnapshotVersion = 2

type RevisionStatus string

const (
	RevisionPending    RevisionStatus = "pending"
	RevisionActive     RevisionStatus = "active"
	RevisionFailed     RevisionStatus = "failed"
	RevisionRolledBack RevisionStatus = "rolled_back"
)

type Revision struct {
	ID                   string
	RevisionNumber       int64
	SHA256               string
	FilePath             string
	StateFilePath        string
	Status               RevisionStatus
	Reason               string
	ActorAdminID         string
	ErrorMessageRedacted string
	CreatedAt            time.Time
	ActivatedAt          *time.Time
}

type SystemState struct {
	Degraded           bool
	DegradedReason     string
	DegradedRevisionID string
	UpdatedAt          time.Time
}

type StateSnapshot struct {
	Version int          `json:"version"`
	State   DesiredState `json:"state"`
}

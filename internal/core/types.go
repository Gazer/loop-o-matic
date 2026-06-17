package core

import "time"

const (
	StatusCreated            = "created"
	StatusPreparingWorkspace = "preparing_workspace"
	StatusDiscovering        = "discovering"
	StatusBlocked            = "blocked"
	StatusImplementing       = "implementing"
	StatusVerifying          = "verifying"
	StatusCreatingPRs        = "creating_prs"
	StatusMonitoringCI       = "monitoring_ci"
	StatusWaitingHumanReview = "waiting_human_review"
	StatusCompleted          = "completed"
	StatusFailed             = "failed"
	StatusPaused             = "paused"
	StatusCancelled          = "cancelled"
)

type Loop struct {
	ID                    int64
	IssueKey              string
	Summary               string
	Status                string
	LastActiveStatus      string
	RunDir                string
	TicketPath            string
	PlanPath              string
	RepoScope             string
	ExtraInstructionsPath string
	AutoRetryCount        int
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastError             string
}

type RepoRun struct {
	LoopID         int64
	RepoName       string
	Path           string
	Branch         string
	ImpactScore    int
	Changed        bool
	PRURL          string
	PRNumber       int
	CIState        string
	ReviewDecision string
	FeedbackHash   string
}

type Event struct {
	ID        int64
	LoopID    int64
	IssueKey  string
	Level     string
	Message   string
	CreatedAt time.Time
}

func IsTerminalStatus(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusBlocked:
		return true
	default:
		return false
	}
}

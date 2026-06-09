package model

import "time"

const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job represents a fetch job and its lifecycle state.
type Job struct {
	JobID     string    `json:"job_id"`
	Status    string    `json:"status"`
	URL       string    `json:"url"`
	Attempts  int       `json:"attempts"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// IsTerminal returns true if the job has reached a terminal state.
func (j *Job) IsTerminal() bool {
	switch j.Status {
	case StatusDone, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

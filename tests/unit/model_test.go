package unit

import (
	"testing"

	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
)

func TestJobIsTerminal(t *testing.T) {
	cases := []struct {
		status   string
		terminal bool
	}{
		{model.StatusPending, false},
		{model.StatusRunning, false},
		{model.StatusDone, true},
		{model.StatusFailed, true},
		{model.StatusCancelled, true},
	}
	for _, c := range cases {
		j := &model.Job{Status: c.status}
		if j.IsTerminal() != c.terminal {
			t.Errorf("status=%q expected terminal=%v, got %v", c.status, c.terminal, j.IsTerminal())
		}
	}
}

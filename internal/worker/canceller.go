package worker

import (
	"context"
	"sync"
)

// Canceller tracks in-flight job contexts so they can be interrupted.
type Canceller struct {
	mu   sync.Mutex
	jobs map[string]context.CancelFunc
}

// NewCanceller creates a Canceller.
func NewCanceller() *Canceller {
	return &Canceller{
		jobs: make(map[string]context.CancelFunc),
	}
}

// Register adds a job's cancel function.
func (c *Canceller) Register(id string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs[id] = cancel
}

// Unregister removes a job's cancel function.
func (c *Canceller) Unregister(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.jobs, id)
}

// Cancel invokes the cancel function for a running job and returns true if found.
func (c *Canceller) Cancel(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, ok := c.jobs[id]; ok {
		cancel()
		return true
	}
	return false
}

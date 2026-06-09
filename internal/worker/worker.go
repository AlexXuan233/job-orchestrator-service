package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/fetcher"
	"github.com/AlexXuan233/job-orchestrator-service/internal/metrics"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
)

// Pool manages a fixed set of worker goroutines.
type Pool struct {
	cfg       *config.Config
	store     *store.RedisStore
	fetcher   fetcher.Fetcher
	logger    *slog.Logger
	canceller *Canceller
	workersWg sync.WaitGroup
	stopCh    chan struct{}
	alive     atomic.Bool
}

// NewPool creates a worker pool.
func NewPool(cfg *config.Config, store *store.RedisStore, fetcher fetcher.Fetcher, logger *slog.Logger) *Pool {
	return &Pool{
		cfg:       cfg,
		store:     store,
		fetcher:   fetcher,
		logger:    logger,
		canceller: NewCanceller(),
		stopCh:    make(chan struct{}),
	}
}

// Start launches the workers and runs crash recovery.
func (p *Pool) Start(ctx context.Context) error {
	p.logger.Info("starting worker pool", "count", p.cfg.WorkerCount)

	// Crash recovery: requeue any jobs left in running state.
	ids, err := p.store.RecoverRunning(ctx)
	if err != nil {
		return fmt.Errorf("recover running: %w", err)
	}
	for _, id := range ids {
		job, err := p.store.GetJob(ctx, id)
		if err != nil {
			p.logger.Error("recover get job failed", "job_id", id, "error", err)
			continue
		}
		if job.IsTerminal() {
			continue
		}
		job.Status = model.StatusPending
		if err := p.store.UpdateJob(ctx, job); err != nil {
			p.logger.Error("recover update job failed", "job_id", id, "error", err)
			continue
		}
		if err := p.store.Requeue(ctx, id); err != nil {
			p.logger.Error("recover requeue failed", "job_id", id, "error", err)
			continue
		}
		p.logger.Info("recovered running job", "job_id", id)
	}

	p.alive.Store(true)
	for i := 0; i < p.cfg.WorkerCount; i++ {
		p.workersWg.Add(1)
		go p.loop(ctx)
	}
	return nil
}

// Stop signals workers to finish after their current job.
func (p *Pool) Stop() {
	close(p.stopCh)
}

// Alive returns true if the pool has been started.
func (p *Pool) Alive() bool {
	return p.alive.Load()
}

// Canceller exposes the job canceller.
func (p *Pool) Canceller() *Canceller {
	return p.canceller
}

// Wait blocks until all worker goroutines have exited.
func (p *Pool) Wait() {
	p.workersWg.Wait()
}

func (p *Pool) loop(ctx context.Context) {
	defer p.workersWg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		default:
		}

		jobID, err := p.store.Dequeue(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Error("dequeue failed", "error", err)
			continue
		}
		p.updateQueueDepth(ctx)

		// If shutting down, requeue the job and exit.
		select {
		case <-p.stopCh:
			if err := p.store.Requeue(ctx, jobID); err != nil {
				p.logger.Error("requeue on shutdown failed", "job_id", jobID, "error", err)
			}
			p.updateQueueDepth(ctx)
			return
		default:
		}

		p.processJob(ctx, jobID)
	}
}

func (p *Pool) updateQueueDepth(ctx context.Context) {
	depth, err := p.store.QueueDepth(ctx)
	if err != nil {
		p.logger.Error("queue depth read failed", "error", err)
		return
	}
	metrics.QueueDepth.Set(float64(depth))
}

func (p *Pool) processJob(ctx context.Context, jobID string) {
	job, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		p.logger.Error("get job failed", "job_id", jobID, "error", err)
		return
	}

	// Skip cancelled jobs that were still in queue.
	if job.Status == model.StatusCancelled {
		p.logger.Info("skipping cancelled job", "job_id", jobID)
		return
	}

	host := store.NormalizeHost(job.URL)

	// Acquire per-domain slot.
	acquired, err := p.store.AcquireSlot(ctx, host)
	if err != nil {
		p.logger.Error("acquire slot failed", "job_id", jobID, "error", err)
		_ = p.store.Requeue(ctx, jobID)
		p.updateQueueDepth(ctx)
		return
	}
	if !acquired {
		// Queue the job back and yield to avoid busy-loop.
		_ = p.store.Requeue(ctx, jobID)
		p.updateQueueDepth(ctx)
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
		return
	}

	// Mark running.
	job.Status = model.StatusRunning
	if err := p.store.UpdateJob(ctx, job); err != nil {
		p.logger.Error("update job failed", "job_id", jobID, "error", err)
		_ = p.store.ReleaseSlot(ctx, host)
		_ = p.store.Requeue(ctx, jobID)
		p.updateQueueDepth(ctx)
		return
	}
	if err := p.store.RegisterRunning(ctx, jobID); err != nil {
		p.logger.Error("register running failed", "job_id", jobID, "error", err)
	}

	metrics.JobsInFlight.Inc()
	metrics.DomainInflight.WithLabelValues(host).Inc()
	defer func() {
		metrics.JobsInFlight.Dec()
		metrics.DomainInflight.WithLabelValues(host).Dec()
		_ = p.store.ReleaseSlot(ctx, host)
		_ = p.store.UnregisterRunning(ctx, jobID)
	}()

	// Execute with wall-time cap and cancellable context.
	wallCtx, wallCancel := context.WithTimeout(ctx, p.cfg.WorkerJobTimeout)
	jobCtx, jobCancel := context.WithCancel(wallCtx)
	p.canceller.Register(job.JobID, jobCancel)
	defer func() {
		p.canceller.Unregister(job.JobID)
		jobCancel()
		wallCancel()
	}()

	p.executeWithRetries(jobCtx, job, host)
}

// persistJob updates the job state in Redis using a short-lived background context.
// This ensures state is persisted even when the job context has been cancelled.
func (p *Pool) persistJob(job *model.Job) {
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.store.UpdateJob(persistCtx, job)
}

func (p *Pool) executeWithRetries(ctx context.Context, job *model.Job, host string) {
	for attempt := 1; attempt <= p.cfg.WorkerRetryMax; attempt++ {
		// Check cancellation before each attempt.
		select {
		case <-ctx.Done():
			// Distinguish user cancellation from timeout.
			// Use a background context because the job context may be cancelled.
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
			fresh, _ := p.store.GetJob(checkCtx, job.JobID)
			checkCancel()
			if fresh != nil && fresh.Status == model.StatusCancelled {
				p.logger.Info("job cancelled mid-run", "job_id", job.JobID)
				return
			}
			job.Status = model.StatusFailed
			job.Error = "timeout"
			p.persistJob(job)
			return
		default:
		}

		// Re-check if job was cancelled while running.
		fresh, err := p.store.GetJob(ctx, job.JobID)
		if err == nil && fresh.Status == model.StatusCancelled {
			p.logger.Info("job cancelled mid-run", "job_id", job.JobID)
			return
		}

		job.Attempts = attempt
		start := time.Now()
		res := p.fetcher.Fetch(ctx, job.URL)
		duration := time.Since(start).Seconds()

		if res.Err != nil {
			p.logger.Error("fetch failed", "job_id", job.JobID, "attempt", attempt, "error", res.Err)
			metrics.JobFetchDuration.WithLabelValues("error").Observe(duration)

			// If context was cancelled by user, don't retry.
			if ctx.Err() != nil {
				checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
				fresh, _ := p.store.GetJob(checkCtx, job.JobID)
				checkCancel()
				if fresh != nil && fresh.Status == model.StatusCancelled {
					p.logger.Info("job cancelled mid-run", "job_id", job.JobID)
					return
				}
			}

			if attempt >= p.cfg.WorkerRetryMax {
				job.Status = model.StatusFailed
				job.Error = res.Err.Error()
				p.persistJob(job)
				metrics.JobsDispatched.WithLabelValues("failed").Inc()
				return
			}
			p.backoff(ctx, attempt)
			continue
		}

		statusLabel := fmt.Sprintf("%d", res.StatusCode)
		metrics.JobFetchDuration.WithLabelValues(statusLabel).Observe(duration)

		if fetcher.IsClientError(res) {
			job.Status = model.StatusFailed
			job.Error = fmt.Sprintf("HTTP %d", res.StatusCode)
			p.persistJob(job)
			metrics.JobsDispatched.WithLabelValues("failed").Inc()
			return
		}

		if res.StatusCode >= 200 && res.StatusCode < 300 {
			job.Status = model.StatusDone
			job.Result = res.Body
			p.persistJob(job)
			metrics.JobsDispatched.WithLabelValues("done").Inc()
			return
		}

		// 5xx or other retryable.
		if fetcher.IsRetryable(res) {
			if attempt >= p.cfg.WorkerRetryMax {
				job.Status = model.StatusFailed
				job.Error = fmt.Sprintf("HTTP %d after %d attempts", res.StatusCode, attempt)
				p.persistJob(job)
				metrics.JobsDispatched.WithLabelValues("failed").Inc()
				return
			}
			p.backoff(ctx, attempt)
			continue
		}

		// Unexpected status.
		job.Status = model.StatusFailed
		job.Error = fmt.Sprintf("unexpected HTTP %d", res.StatusCode)
		p.persistJob(job)
		metrics.JobsDispatched.WithLabelValues("failed").Inc()
		return
	}
}

func (p *Pool) backoff(ctx context.Context, attempt int) {
	d := fetcher.ComputeBackoff(p.cfg.WorkerRetryBase, p.cfg.WorkerRetryMaxDur, attempt)
	p.logger.Info("backing off", "attempt", attempt, "duration", d.String())
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

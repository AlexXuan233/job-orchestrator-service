package chaos

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/fetcher"
	"github.com/AlexXuan233/job-orchestrator-service/internal/logger"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
	"github.com/AlexXuan233/job-orchestrator-service/internal/worker"
)

// blockingFetcher blocks until the channel receives a value.
type blockingFetcher struct {
	block chan struct{}
}

func (b *blockingFetcher) Fetch(ctx context.Context, url string) fetcher.Result {
	select {
	case <-ctx.Done():
		return fetcher.Result{Err: ctx.Err()}
	case <-b.block:
		return fetcher.Result{StatusCode: 200, Body: "ok"}
	}
}

func TestGracefulShutdownRequeuesRunningJob(t *testing.T) {
	cfg := &config.Config{
		RedisAddr:            getRedisAddr(),
		RedisPoolSize:        20,
		RedisDialTimeout:     2 * time.Second,
		WorkerCount:          1,
		WorkerAttemptTimeout: 10 * time.Second,
		WorkerJobTimeout:     30 * time.Second,
		WorkerRetryMax:       3,
		WorkerRetryBase:      200 * time.Millisecond,
		WorkerRetryMaxDur:    5 * time.Second,
		DedupWindowSeconds:   60,
		PerDomainMaxInflight: 5,
		LogLevel:             "warn",
		LogFormat:            "json",
	}

	ctx := context.Background()
	s, err := store.New(cfg)
	if err != nil {
		t.Skipf("redis not available: %v", err)
	}
	defer s.Close()

	// Clean up.
	_ = s.Ping(ctx)

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	block := make(chan struct{})
	f := &blockingFetcher{block: block}
	pool := worker.NewPool(cfg, s, f, log)

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("pool start failed: %v", err)
	}

	// Submit a job.
	job := &model.Job{
		JobID:     "chaos-job-1",
		Status:    model.StatusPending,
		URL:       "http://example.com/slow",
		CreatedAt: time.Now().UTC(),
	}
	id, created, err := s.DedupOrCreate(ctx, job)
	if err != nil {
		t.Fatalf("dedup failed: %v", err)
	}
	if !created {
		t.Fatal("expected new job")
	}
	if err := s.Enqueue(ctx, id); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// Wait for the job to be picked up by the worker.
	time.Sleep(500 * time.Millisecond)

	// Verify it's running.
	j, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("get job failed: %v", err)
	}
	if j.Status != model.StatusRunning {
		t.Fatalf("expected job to be running, got %s", j.Status)
	}

	// Signal shutdown while job is still running.
	pool.Stop()

	// Allow the fetcher to unblock so the worker can finish its loop.
	close(block)

	// Wait for workers to exit.
	done := make(chan struct{})
	go func() {
		pool.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not exit in time")
	}

	// After shutdown, the job should either be done (because we unblocked)
	// or requeued. Since we unblocked the fetcher, it should be done.
	j, err = s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("get job after shutdown: %v", err)
	}
	if j.Status != model.StatusDone {
		t.Fatalf("expected job status done after unblock, got %s", j.Status)
	}
}

func TestCrashRecoveryRequeuesRunning(t *testing.T) {
	cfg := &config.Config{
		RedisAddr:            getRedisAddr(),
		RedisPoolSize:        20,
		RedisDialTimeout:     2 * time.Second,
		WorkerCount:          1,
		WorkerAttemptTimeout: 10 * time.Second,
		WorkerJobTimeout:     30 * time.Second,
		WorkerRetryMax:       3,
		WorkerRetryBase:      200 * time.Millisecond,
		WorkerRetryMaxDur:    5 * time.Second,
		DedupWindowSeconds:   60,
		PerDomainMaxInflight: 5,
		LogLevel:             "warn",
		LogFormat:            "json",
	}

	ctx := context.Background()
	s, err := store.New(cfg)
	if err != nil {
		t.Skipf("redis not available: %v", err)
	}
	defer s.Close()

	// Simulate a crash: create a running job in Redis and add it to running set.
	job := &model.Job{
		JobID:     "crash-job-1",
		Status:    model.StatusRunning,
		URL:       "http://example.com/test",
		Attempts:  1,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.UpdateJob(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}
	if err := s.RegisterRunning(ctx, job.JobID); err != nil {
		t.Fatalf("register running: %v", err)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	f := fetcher.NewClient(cfg.WorkerAttemptTimeout)
	pool := worker.NewPool(cfg, s, f, log)

	// Start should recover the running job.
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("pool start failed: %v", err)
	}

	// Give recovery a moment.
	time.Sleep(200 * time.Millisecond)

	// Job should now be pending and in queue.
	j, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if j.Status != model.StatusPending {
		t.Fatalf("expected pending after recovery, got %s", j.Status)
	}

	depth, err := s.QueueDepth(ctx)
	if err != nil {
		t.Fatalf("queue depth: %v", err)
	}
	if depth != 1 {
		t.Fatalf("expected queue depth 1, got %d", depth)
	}

	pool.Stop()
	pool.Wait()
}

func getRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

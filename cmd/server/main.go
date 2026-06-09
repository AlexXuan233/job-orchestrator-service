package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/fetcher"
	"github.com/AlexXuan233/job-orchestrator-service/internal/handler"
	"github.com/AlexXuan233/job-orchestrator-service/internal/logger"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
	"github.com/AlexXuan233/job-orchestrator-service/internal/worker"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	log.Info("starting job orchestrator service", "port", cfg.HTTPPort)

	redisStore, err := store.New(cfg)
	if err != nil {
		log.Error("redis store init failed", "error", err)
		os.Exit(1)
	}
	defer redisStore.Close()

	fetcherClient := fetcher.NewClient(cfg.WorkerAttemptTimeout)
	pool := worker.NewPool(cfg, redisStore, fetcherClient, log)

	// Use a separate context for workers so SIGTERM doesn't abort in-flight fetches immediately.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	if err := pool.Start(workerCtx); err != nil {
		log.Error("worker pool start failed", "error", err)
		os.Exit(1)
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(handler.RequestLogger(log))

	h := handler.New(cfg, redisStore, pool, log)
	h.RegisterRoutes(r)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:      r,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
	}

	// Start HTTP server in background.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server error", "error", err)
		}
	}()

	log.Info("service ready", "addr", srv.Addr)

	// Wait for shutdown signal.
	sigCtx, sigStop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	<-sigCtx.Done()
	log.Info("shutdown signal received")
	sigStop()

	// Stop accepting new jobs.
	h.Shutdown()

	// Graceful HTTP shutdown.
	httpShutdownCtx, httpShutdownCancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer httpShutdownCancel()
	if err := srv.Shutdown(httpShutdownCtx); err != nil {
		log.Error("http shutdown error", "error", err)
	}

	// Signal workers to stop after current job and wait for them.
	pool.Stop()
	done := make(chan struct{})
	go func() {
		pool.Wait()
		close(done)
	}()

	workerShutdownCtx, workerShutdownCancel := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer workerShutdownCancel()
	select {
	case <-done:
		log.Info("workers drained gracefully")
	case <-workerShutdownCtx.Done():
		log.Warn("shutdown timeout reached, forcing exit")
	}

	// Now it's safe to cancel worker context (all workers have exited).
	workerCancel()

	// Requeue any remaining running jobs so they are not lost.
	recoverCtx := context.Background()
	ids, err := redisStore.RecoverRunning(recoverCtx)
	if err != nil {
		log.Error("final recover running failed", "error", err)
	} else {
		for _, id := range ids {
			job, err := redisStore.GetJob(recoverCtx, id)
			if err != nil {
				continue
			}
			if job.IsTerminal() {
				continue
			}
			job.Status = "pending"
			_ = redisStore.UpdateJob(recoverCtx, job)
			_ = redisStore.Requeue(recoverCtx, id)
			log.Info("requeued running job on shutdown", "job_id", id)
		}
	}

	log.Info("service stopped")
}

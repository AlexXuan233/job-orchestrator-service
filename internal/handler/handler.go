package handler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/metrics"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
	"github.com/AlexXuan233/job-orchestrator-service/internal/worker"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler holds HTTP handlers and dependencies.
type Handler struct {
	cfg          *config.Config
	store        *store.RedisStore
	pool         *worker.Pool
	logger       *slog.Logger
	shuttingDown atomic.Bool
}

// New creates a Handler.
func New(cfg *config.Config, store *store.RedisStore, pool *worker.Pool, logger *slog.Logger) *Handler {
	return &Handler{
		cfg:    cfg,
		store:  store,
		pool:   pool,
		logger: logger,
	}
}

// RegisterRoutes wires all routes to the Gin engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.POST("/jobs", h.createJob)
	r.GET("/jobs/:id", h.getJob)
	r.POST("/jobs/:id/cancel", h.cancelJob)
	r.GET("/healthz", h.healthz)
	r.GET("/readyz", h.readyz)
	r.GET(h.cfg.MetricsPath, gin.WrapH(promhttp.Handler()))
}

// Shutdown marks the handler as shutting down so new jobs are rejected.
func (h *Handler) Shutdown() {
	h.shuttingDown.Store(true)
}

type createJobReq struct {
	URL string `json:"url" binding:"required"`
}

type createJobResp struct {
	JobID string `json:"job_id"`
}

type errorResp struct {
	Error string `json:"error"`
}

func (h *Handler) createJob(c *gin.Context) {
	if h.shuttingDown.Load() {
		c.JSON(http.StatusServiceUnavailable, errorResp{Error: "shutting down"})
		return
	}

	var req createJobReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResp{Error: fmt.Sprintf("malformed input: %v", err)})
		metrics.JobsSubmitted.WithLabelValues("bad_request").Inc()
		return
	}

	if !isValidURL(req.URL) {
		c.JSON(http.StatusBadRequest, errorResp{Error: "invalid URL"})
		metrics.JobsSubmitted.WithLabelValues("bad_request").Inc()
		return
	}

	job := &model.Job{
		JobID:     generateID(),
		Status:    model.StatusPending,
		URL:       req.URL,
		Attempts:  0,
		CreatedAt: time.Now().UTC(),
	}

	ctx := c.Request.Context()
	jobID, created, err := h.store.DedupOrCreate(ctx, job)
	if err != nil {
		h.logger.Error("dedup or create failed", "error", err)
		c.JSON(http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}

	if !created {
		c.JSON(http.StatusAccepted, createJobResp{JobID: jobID})
		metrics.JobsSubmitted.WithLabelValues("deduped").Inc()
		return
	}

	if err := h.store.Enqueue(ctx, jobID); err != nil {
		h.logger.Error("enqueue failed", "job_id", jobID, "error", err)
		c.JSON(http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}

	metrics.JobsSubmitted.WithLabelValues("created").Inc()
	metrics.QueueDepth.Set(float64(mustInt64(h.store.QueueDepth(ctx))))
	c.JSON(http.StatusAccepted, createJobResp{JobID: jobID})
}

func (h *Handler) getJob(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	job, err := h.store.GetJob(ctx, id)
	if err == store.ErrJobNotFound {
		c.JSON(http.StatusNotFound, errorResp{Error: "not found"})
		return
	}
	if err != nil {
		h.logger.Error("get job failed", "job_id", id, "error", err)
		c.JSON(http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) cancelJob(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	err := h.store.CancelJob(ctx, id)
	if err == store.ErrJobNotFound {
		c.JSON(http.StatusNotFound, errorResp{Error: "not found"})
		return
	}
	if err == store.ErrAlreadyTerminal {
		c.JSON(http.StatusConflict, errorResp{Error: "job already terminal"})
		return
	}
	if err != nil {
		h.logger.Error("cancel job failed", "job_id", id, "error", err)
		c.JSON(http.StatusInternalServerError, errorResp{Error: "internal error"})
		return
	}
	// Interrupt the fetch if the job is currently running.
	h.pool.Canceller().Cancel(id)
	c.Status(http.StatusNoContent)
}

func (h *Handler) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) readyz(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.store.Ping(ctx); err != nil {
		h.logger.Warn("readyz failed: redis unreachable", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		return
	}
	if !h.pool.Alive() {
		h.logger.Warn("readyz failed: worker pool not alive")
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not ready"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func isValidURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func mustInt64(v int64, err error) int64 {
	if err != nil {
		return 0
	}
	return v
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

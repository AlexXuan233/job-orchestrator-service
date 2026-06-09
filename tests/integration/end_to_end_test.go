package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/fetcher"
	"github.com/AlexXuan233/job-orchestrator-service/internal/handler"
	"github.com/AlexXuan233/job-orchestrator-service/internal/logger"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
	"github.com/AlexXuan233/job-orchestrator-service/internal/worker"
	"github.com/gin-gonic/gin"
)

func setupTestServer(t *testing.T) (*httptest.Server, *store.RedisStore, *worker.Pool, context.CancelFunc) {
	cfg := &config.Config{
		RedisAddr:            getRedisAddr(),
		RedisPoolSize:        20,
		RedisDialTimeout:     2 * time.Second,
		WorkerCount:          2,
		WorkerAttemptTimeout: 5 * time.Second,
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

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	f := fetcher.NewClient(cfg.WorkerAttemptTimeout)
	pool := worker.NewPool(cfg, s, f, log)
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("pool start: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.New(cfg, s, pool, log)
	h.RegisterRoutes(r)

	server := httptest.NewServer(r)
	cancel := func() {
		pool.Stop()
		pool.Wait()
		s.Close()
		server.Close()
	}
	return server, s, pool, cancel
}

func TestSubmitAndComplete(t *testing.T) {
	server, _, _, cancel := setupTestServer(t)
	defer cancel()

	// Start a tiny upstream that returns 200.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	}))
	defer upstream.Close()

	client := server.Client()
	url := server.URL + "/jobs"

	body := fmt.Sprintf(`{"url":"%s/echo/200"}`, upstream.URL)
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}

	var res struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	resp.Body.Close()
	if res.JobID == "" {
		t.Fatal("expected job_id")
	}

	// Poll for completion.
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, err := client.Get(server.URL + "/jobs/" + res.JobID)
		if err != nil {
			t.Fatalf("get job failed: %v", err)
		}
		var job model.Job
		if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
			resp.Body.Close()
			t.Fatalf("decode job failed: %v", err)
		}
		resp.Body.Close()
		if job.Status == model.StatusDone {
			if job.Result != "hello" {
				t.Fatalf("expected result 'hello', got %q", job.Result)
			}
			return
		}
		if job.Status == model.StatusFailed {
			t.Fatalf("job failed: %s", job.Error)
		}
	}
	t.Fatal("job did not complete in time")
}

func TestNoRetryOn4xx(t *testing.T) {
	server, _, _, cancel := setupTestServer(t)
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	client := server.Client()
	body := fmt.Sprintf(`{"url":"%s/notfound"}`, upstream.URL)
	resp, err := client.Post(server.URL+"/jobs", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	var res struct {
		JobID string `json:"job_id"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()

	time.Sleep(2 * time.Second)

	resp, err = client.Get(server.URL + "/jobs/" + res.JobID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	var job model.Job
	json.NewDecoder(resp.Body).Decode(&job)
	resp.Body.Close()

	if job.Status != model.StatusFailed {
		t.Fatalf("expected failed, got %s", job.Status)
	}
	if job.Attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", job.Attempts)
	}
}

func TestDedup(t *testing.T) {
	server, _, _, cancel := setupTestServer(t)
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	client := server.Client()
	body := fmt.Sprintf(`{"url":"%s/dedup"}`, upstream.URL)

	resp1, _ := client.Post(server.URL+"/jobs", "application/json", strings.NewReader(body))
	var r1 struct {
		JobID string `json:"job_id"`
	}
	json.NewDecoder(resp1.Body).Decode(&r1)
	resp1.Body.Close()

	resp2, _ := client.Post(server.URL+"/jobs", "application/json", strings.NewReader(body))
	var r2 struct {
		JobID string `json:"job_id"`
	}
	json.NewDecoder(resp2.Body).Decode(&r2)
	resp2.Body.Close()

	if r1.JobID != r2.JobID {
		t.Fatalf("expected same job id, got %s and %s", r1.JobID, r2.JobID)
	}
}

func getRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

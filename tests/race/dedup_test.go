package race

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/AlexXuan233/job-orchestrator-service/internal/store"
)

func TestConcurrentDedup(t *testing.T) {
	cfg := &config.Config{
		RedisAddr:            getRedisAddr(),
		RedisPoolSize:        20,
		RedisDialTimeout:     2 * time.Second,
		DedupWindowSeconds:   60,
		PerDomainMaxInflight: 5,
	}

	ctx := context.Background()
	s, err := store.New(cfg)
	if err != nil {
		t.Skipf("redis not available: %v", err)
	}
	defer s.Close()

	// Clean up any previous test data.
	_ = s.Ping(ctx)

	url := "http://example.com/race-test"
	var wg sync.WaitGroup
	ids := make([]string, 100)
	var mu sync.Mutex

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job := &model.Job{
				JobID:     generateID(),
				Status:    model.StatusPending,
				URL:       url,
				CreatedAt: time.Now().UTC(),
			}
			id, _, err := s.DedupOrCreate(ctx, job)
			if err != nil {
				t.Errorf("dedup failed: %v", err)
				return
			}
			mu.Lock()
			ids[idx] = id
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	unique := make(map[string]struct{})
	for _, id := range ids {
		if id == "" {
			t.Fatal("expected non-empty job id")
		}
		unique[id] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("expected exactly 1 unique job id, got %d", len(unique))
	}
}

func getRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	return "localhost:6379"
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

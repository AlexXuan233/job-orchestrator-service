package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
	"github.com/AlexXuan233/job-orchestrator-service/internal/model"
	"github.com/redis/go-redis/v9"
)

var (
	ErrJobNotFound     = errors.New("job not found")
	ErrAlreadyTerminal = errors.New("job already in terminal state")
)

// RedisStore wraps a go-redis client with domain-specific operations.
type RedisStore struct {
	client  *redis.Client
	cfg     *config.Config
	scripts *scripts
}

type scripts struct {
	acquireSlot *redis.Script
	releaseSlot *redis.Script
}

// New creates a RedisStore from configuration.
func New(cfg *config.Config) (*RedisStore, error) {
	opts := &redis.Options{
		Addr:         cfg.RedisAddr,
		Password:     cfg.RedisPassword,
		DB:           cfg.RedisDB,
		PoolSize:     cfg.RedisPoolSize,
		DialTimeout:  cfg.RedisDialTimeout,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	s := &scripts{
		acquireSlot: redis.NewScript(`
			local key = KEYS[1]
			local cap = tonumber(ARGV[1])
			local ttl = tonumber(ARGV[2])
			local current = tonumber(redis.call('GET', key) or 0)
			if current < cap then
				redis.call('INCR', key)
				redis.call('EXPIRE', key, ttl)
				return 1
			else
				return 0
			end
		`),
		releaseSlot: redis.NewScript(`
			local key = KEYS[1]
			local current = tonumber(redis.call('GET', key) or 0)
			if current > 0 then
				redis.call('DECR', key)
			end
			return current
		`),
	}

	return &RedisStore{
		client:  client,
		cfg:     cfg,
		scripts: s,
	}, nil
}

// Close closes the Redis client.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

// Ping returns nil if Redis is reachable.
func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func dedupKey(urlStr string) string {
	h := sha256.Sum256([]byte(urlStr))
	return "dedup:" + hex.EncodeToString(h[:])
}

func jobKey(id string) string {
	return "job:" + id
}

func slotKey(host string) string {
	return "slots:" + host
}

// DedupOrCreate attempts to create a dedup record for the URL.
// If the URL was already seen within the dedup window, it returns the existing job ID and created=false.
// Otherwise it persists the job and returns the new job ID with created=true.
func (s *RedisStore) DedupOrCreate(ctx context.Context, job *model.Job) (string, bool, error) {
	dkey := dedupKey(job.URL)
	jkey := jobKey(job.JobID)

	data, err := json.Marshal(job)
	if err != nil {
		return "", false, fmt.Errorf("marshal job: %w", err)
	}

	// Persist job first so that GET always succeeds when dedup exists.
	if err := s.client.Set(ctx, jkey, data, 0).Err(); err != nil {
		return "", false, fmt.Errorf("set job: %w", err)
	}

	// Try to set dedup key atomically.
	ok, err := s.client.SetNX(ctx, dkey, job.JobID, time.Duration(s.cfg.DedupWindowSeconds)*time.Second).Result()
	if err != nil {
		return "", false, fmt.Errorf("setnx dedup: %w", err)
	}
	if ok {
		return job.JobID, true, nil
	}

	// Dedup exists; fetch the winner's job ID.
	existingID, err := s.client.Get(ctx, dkey).Result()
	if err != nil {
		return "", false, fmt.Errorf("get dedup: %w", err)
	}
	// Best-effort cleanup of our orphaned job key.
	_ = s.client.Del(ctx, jkey).Err()
	return existingID, false, nil
}

// GetJob retrieves a job by ID.
func (s *RedisStore) GetJob(ctx context.Context, id string) (*model.Job, error) {
	data, err := s.client.Get(ctx, jobKey(id)).Result()
	if err == redis.Nil {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	var job model.Job
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	return &job, nil
}

// UpdateJob persists the current state of a job.
func (s *RedisStore) UpdateJob(ctx context.Context, job *model.Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	if err := s.client.Set(ctx, jobKey(job.JobID), data, 0).Err(); err != nil {
		return fmt.Errorf("set job: %w", err)
	}
	return nil
}

// CancelJob sets a job's status to cancelled if it is not already terminal.
func (s *RedisStore) CancelJob(ctx context.Context, id string) error {
	job, err := s.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if job.IsTerminal() {
		return ErrAlreadyTerminal
	}
	job.Status = model.StatusCancelled
	return s.UpdateJob(ctx, job)
}

// Enqueue pushes a job ID to the pending list.
func (s *RedisStore) Enqueue(ctx context.Context, jobID string) error {
	return s.client.RPush(ctx, "jobs:pending", jobID).Err()
}

// Dequeue blocks until a job ID is available or the context is cancelled.
func (s *RedisStore) Dequeue(ctx context.Context) (string, error) {
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		result, err := s.client.BRPop(ctx, 1*time.Second, "jobs:pending").Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("brpop: %w", err)
		}
		// result is ["jobs:pending", jobID]
		if len(result) == 2 {
			return result[1], nil
		}
	}
}

// Requeue puts a job back at the tail of the pending list.
func (s *RedisStore) Requeue(ctx context.Context, jobID string) error {
	return s.client.RPush(ctx, "jobs:pending", jobID).Err()
}

// AcquireSlot tries to increment the per-domain inflight counter.
// Returns true if the slot was acquired.
func (s *RedisStore) AcquireSlot(ctx context.Context, host string) (bool, error) {
	key := slotKey(host)
	res, err := s.scripts.acquireSlot.Run(ctx, s.client, []string{key},
		s.cfg.PerDomainMaxInflight,
		30, // TTL seconds (same as max wall-time cap)
	).Int64()
	if err != nil {
		return false, fmt.Errorf("acquire slot: %w", err)
	}
	return res == 1, nil
}

// ReleaseSlot decrements the per-domain inflight counter.
func (s *RedisStore) ReleaseSlot(ctx context.Context, host string) error {
	key := slotKey(host)
	_, err := s.scripts.releaseSlot.Run(ctx, s.client, []string{key}).Int64()
	if err != nil {
		return fmt.Errorf("release slot: %w", err)
	}
	return nil
}

// RegisterRunning adds a job to the running set.
func (s *RedisStore) RegisterRunning(ctx context.Context, jobID string) error {
	return s.client.SAdd(ctx, "jobs:running", jobID).Err()
}

// UnregisterRunning removes a job from the running set.
func (s *RedisStore) UnregisterRunning(ctx context.Context, jobID string) error {
	return s.client.SRem(ctx, "jobs:running", jobID).Err()
}

// RecoverRunning fetches all job IDs in the running set and clears it.
func (s *RedisStore) RecoverRunning(ctx context.Context) ([]string, error) {
	ids, err := s.client.SMembers(ctx, "jobs:running").Result()
	if err != nil {
		return nil, fmt.Errorf("smembers running: %w", err)
	}
	if len(ids) > 0 {
		if err := s.client.Del(ctx, "jobs:running").Err(); err != nil {
			return nil, fmt.Errorf("del running set: %w", err)
		}
	}
	return ids, nil
}

// QueueDepth returns the current length of the pending queue.
func (s *RedisStore) QueueDepth(ctx context.Context) (int64, error) {
	return s.client.LLen(ctx, "jobs:pending").Result()
}

// NormalizeHost extracts a bounded host identifier for metrics.
func NormalizeHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "invalid"
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "unknown"
	}
	return host
}

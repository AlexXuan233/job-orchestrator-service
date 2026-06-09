package fetcher

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

const maxBodySize = 1024 * 1024 // 1 MiB

// Result holds the outcome of a single fetch attempt.
type Result struct {
	StatusCode int
	Body       string
	Err        error
}

// Fetcher is the interface for URL fetching.
type Fetcher interface {
	Fetch(ctx context.Context, url string) Result
}

// Client performs HTTP fetches with timeouts and body limits.
type Client struct {
	httpClient *http.Client
}

// NewClient creates a fetcher with the given per-attempt timeout.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Fetch performs a single GET request respecting the context.
func (c *Client) Fetch(ctx context.Context, url string) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{Err: fmt.Errorf("create request: %w", err)}
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	elapsed := time.Since(start)
	_ = elapsed // available for metrics in caller

	if err != nil {
		return Result{Err: fmt.Errorf("http do: %w", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return Result{StatusCode: resp.StatusCode, Err: fmt.Errorf("read body: %w", err)}
	}
	if len(body) > maxBodySize {
		return Result{StatusCode: resp.StatusCode, Err: fmt.Errorf("body exceeds %d bytes", maxBodySize)}
	}

	return Result{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

// IsRetryable returns true if the error or status code warrants a retry.
func IsRetryable(r Result) bool {
	if r.Err != nil {
		// Network errors, timeouts, etc. are retryable.
		return true
	}
	if r.StatusCode >= 500 {
		return true
	}
	return false
}

// IsClientError returns true for 4xx responses (no retry).
func IsClientError(r Result) bool {
	return r.StatusCode >= 400 && r.StatusCode < 500
}

// ComputeBackoff returns the duration to wait before the next attempt.
func ComputeBackoff(base, max time.Duration, attempt int) time.Duration {
	// Exponential: base * 2^(attempt-1)
	backoff := base
	for i := 1; i < attempt; i++ {
		backoff *= 2
		if backoff >= max {
			backoff = max
			break
		}
	}
	if backoff > max {
		backoff = max
	}
	// ±20% jitter
	jitter := 0.8 + (rand.Float64() * 0.4)
	return time.Duration(float64(backoff) * jitter)
}

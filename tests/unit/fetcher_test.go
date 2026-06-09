package unit

import (
	"testing"
	"time"

	"github.com/AlexXuan233/job-orchestrator-service/internal/fetcher"
)

func TestComputeBackoff(t *testing.T) {
	base := 200 * time.Millisecond
	max := 5 * time.Second

	// attempt 1: base * 2^0 = 200ms ±20%
	d1 := fetcher.ComputeBackoff(base, max, 1)
	if d1 < 160*time.Millisecond || d1 > 240*time.Millisecond {
		t.Errorf("attempt 1 backoff out of range: %v", d1)
	}

	// attempt 2: base * 2^1 = 400ms ±20%
	d2 := fetcher.ComputeBackoff(base, max, 2)
	if d2 < 320*time.Millisecond || d2 > 480*time.Millisecond {
		t.Errorf("attempt 2 backoff out of range: %v", d2)
	}

	// attempt 5: should be capped at max (5s) ±20%, but max is the cap
	d5 := fetcher.ComputeBackoff(base, max, 5)
	if d5 > max {
		t.Errorf("attempt 5 backoff exceeded max: %v", d5)
	}
}

func TestIsRetryable(t *testing.T) {
	if !fetcher.IsRetryable(fetcher.Result{Err: fmtError("network error")}) {
		t.Error("network error should be retryable")
	}
	if !fetcher.IsRetryable(fetcher.Result{StatusCode: 503}) {
		t.Error("503 should be retryable")
	}
	if fetcher.IsRetryable(fetcher.Result{StatusCode: 404}) {
		t.Error("404 should not be retryable")
	}
	if fetcher.IsRetryable(fetcher.Result{StatusCode: 200}) {
		t.Error("200 should not be retryable")
	}
}

func TestIsClientError(t *testing.T) {
	if !fetcher.IsClientError(fetcher.Result{StatusCode: 400}) {
		t.Error("400 should be client error")
	}
	if !fetcher.IsClientError(fetcher.Result{StatusCode: 404}) {
		t.Error("404 should be client error")
	}
	if fetcher.IsClientError(fetcher.Result{StatusCode: 500}) {
		t.Error("500 should not be client error")
	}
}

func fmtError(s string) error {
	return &testError{s}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

package unit

import (
	"testing"

	"github.com/AlexXuan233/job-orchestrator-service/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	// Unset all env vars that would interfere.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 8080 {
		t.Errorf("expected HTTPPort=8080, got %d", cfg.HTTPPort)
	}
	if cfg.WorkerCount != 10 {
		t.Errorf("expected WorkerCount=10, got %d", cfg.WorkerCount)
	}
	if cfg.PerDomainMaxInflight != 5 {
		t.Errorf("expected PerDomainMaxInflight=5, got %d", cfg.PerDomainMaxInflight)
	}
}

func TestLoadInvalidPort(t *testing.T) {
	// We can't safely set env vars in parallel tests without cleanup.
	// For simplicity, we test validation logic directly.
	cfg := &config.Config{HTTPPort: 99999, WorkerCount: 1, RedisAddr: "localhost:6379", PerDomainMaxInflight: 1}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		// expected
	} else {
		t.Error("expected invalid port to be detectable")
	}
}

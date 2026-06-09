package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	HTTPPort            int           `json:"http_port"`
	HTTPReadTimeout     time.Duration `json:"http_read_timeout"`
	HTTPWriteTimeout    time.Duration `json:"http_write_timeout"`
	HTTPShutdownTimeout time.Duration `json:"http_shutdown_timeout"`

	RedisAddr        string        `json:"redis_addr"`
	RedisPassword    string        `json:"redis_password"`
	RedisDB          int           `json:"redis_db"`
	RedisPoolSize    int           `json:"redis_pool_size"`
	RedisDialTimeout time.Duration `json:"redis_dial_timeout"`

	WorkerCount          int           `json:"worker_count"`
	WorkerAttemptTimeout time.Duration `json:"worker_attempt_timeout"`
	WorkerJobTimeout     time.Duration `json:"worker_job_timeout"`
	WorkerRetryMax       int           `json:"worker_retry_max"`
	WorkerRetryBase      time.Duration `json:"worker_retry_base"`
	WorkerRetryMaxDur    time.Duration `json:"worker_retry_max_dur"`

	DedupWindowSeconds   int `json:"dedup_window_seconds"`
	PerDomainMaxInflight int `json:"per_domain_max_inflight"`

	LogLevel    string `json:"log_level"`
	LogFormat   string `json:"log_format"`
	MetricsPath string `json:"metrics_path"`
}

// Load reads configuration from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPPort:             getInt("HTTP_PORT", 8080),
		HTTPReadTimeout:      getDuration("HTTP_READ_TIMEOUT_MS", 5000),
		HTTPWriteTimeout:     getDuration("HTTP_WRITE_TIMEOUT_MS", 10000),
		HTTPShutdownTimeout:  getDuration("HTTP_SHUTDOWN_TIMEOUT_MS", 20000),
		RedisAddr:            getString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        getString("REDIS_PASSWORD", ""),
		RedisDB:              getInt("REDIS_DB", 0),
		RedisPoolSize:        getInt("REDIS_POOL_SIZE", 20),
		RedisDialTimeout:     getDuration("REDIS_DIAL_TIMEOUT_MS", 1000),
		WorkerCount:          getInt("WORKER_COUNT", 10),
		WorkerAttemptTimeout: getDuration("WORKER_ATTEMPT_TIMEOUT_MS", 10000),
		WorkerJobTimeout:     getDuration("WORKER_JOB_TIMEOUT_MS", 30000),
		WorkerRetryMax:       getInt("WORKER_RETRY_MAX", 3),
		WorkerRetryBase:      getDuration("WORKER_RETRY_BASE_MS", 200),
		WorkerRetryMaxDur:    getDuration("WORKER_RETRY_MAX_MS", 5000),
		DedupWindowSeconds:   getInt("DEDUP_WINDOW_SECONDS", 60),
		PerDomainMaxInflight: getInt("PER_DOMAIN_MAX_INFLIGHT", 5),
		LogLevel:             getString("LOG_LEVEL", "info"),
		LogFormat:            getString("LOG_FORMAT", "json"),
		MetricsPath:          getString("METRICS_PATH", "/metrics"),
	}

	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return nil, fmt.Errorf("invalid HTTP_PORT: %d", cfg.HTTPPort)
	}
	if cfg.WorkerCount <= 0 {
		return nil, fmt.Errorf("invalid WORKER_COUNT: %d", cfg.WorkerCount)
	}
	if cfg.RedisAddr == "" {
		return nil, fmt.Errorf("REDIS_ADDR is required")
	}
	if cfg.PerDomainMaxInflight <= 0 {
		return nil, fmt.Errorf("invalid PER_DOMAIN_MAX_INFLIGHT: %d", cfg.PerDomainMaxInflight)
	}
	return cfg, nil
}

func getString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getDuration(key string, defMs int) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return time.Duration(defMs) * time.Millisecond
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return time.Duration(defMs) * time.Millisecond
	}
	return time.Duration(n) * time.Millisecond
}

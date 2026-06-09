# Job Orchestrator Service

A production-grade URL-fetch job orchestrator built in Go with the Gin framework.

## Quick Start

### Prerequisites

- Go 1.25+
- Redis 7+
- Docker
- Docker Compose

### Local Development

```bash
# 1. Copy sample env config
cp .env.example .env
# Edit .env if needed (REDIS_ADDR defaults to localhost:6379)

# 2. Start infra (from starter pack)
cd /path/to/take-home-sre-backend
docker compose up -d

# 3. Run the service locally
make run
```

### Docker Compose (self-contained)

```bash
cp .env.example .env
# Edit .env if needed (docker-compose overrides REDIS_ADDR to redis:6379)
make compose-up
```

This starts Redis, mock-upstream, Prometheus, Grafana, and the service.

### Sample Commands

```bash
# Health checks
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz

# Submit a job
curl -X POST http://localhost:8080/jobs \
  -H "Content-Type: application/json" \
  -d '{"url":"http://localhost:18080/echo/200"}'

# Get job state
curl http://localhost:8080/jobs/<job_id>

# Cancel a job
curl -X POST http://localhost:8080/jobs/<job_id>/cancel

# Metrics
curl http://localhost:8080/metrics
```

## Verification

### Start whole stack on this repo

```bash
cp .env.example .env
make compose-up

# The verify.sh script comes from the starter pack (take-home-sre-backend).
cd /path/to/take-home-sre-backend
SERVICE=http://localhost:8080 UPSTREAM=http://mock-upstream:8080 ./verify.sh
```

### Only start the job service on this repo

```bash
# 1. Copy sample env config
cp .env.example .env

# 2. Start starter-pack infra
cd /path/to/take-home-sre-backend
docker compose up -d

# 3. start job service 
cd /path/to/current/repo
make run

# 4. Run verify.sh
cd /path/to/take-home-sre-backend
SERVICE=http://localhost:8080 UPSTREAM=http://localhost:18080 ./verify.sh
```

## Testing

```bash
# Unit tests (no Redis required)
make test-unit

# Race / chaos / integration tests (require Redis on localhost:6379)
make test-race
make test-chaos
make test-integration
```

## Project Layout

```
.
├── cmd/server/              # Application entrypoint
├── internal/                # Private application code
│   ├── config/              # Env-var config loading
│   ├── fetcher/             # HTTP client with timeout
│   ├── handler/             # Gin HTTP handlers + middleware
│   ├── logger/              # Structured JSON logging
│   ├── metrics/             # Prometheus metrics definitions
│   ├── model/               # Domain models (Job)
│   ├── store/               # Redis-backed queue & state
│   └── worker/              # Worker pool & job execution
├── tests/                   # Unit, race, chaos, integration tests
│   ├── unit/
│   ├── race/
│   ├── chaos/
│   └── integration/
├── k8s/                     # Kubernetes manifests
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── configmap.yaml
│   ├── hpa.yaml
│   ├── pdb.yaml
│   └── DEBUGGING.md
├── mock-upstream/           # Self-contained mock HTTP server
├── Dockerfile
├── docker-compose.yml
├── prometheus.yml           # Prometheus scrape config (local dev)
├── prometheus-docker.yml    # Prometheus scrape config (Docker Compose)
├── .env.example             # Sample env config
├── go.mod
├── go.sum
├── Makefile
├── README.md
├── DESIGN.md                # Architecture & trade-offs
├── SLO.md                   # SLI / SLO / error budget
├── INCIDENT_RUNBOOK.md      # 5 on-call incidents
├── BENCHMARK.md             # Load test results
└── LICENSE
```

## Troubleshooting

### Go module download timeout when building Docker image

If you are located in China and encounter a timeout error during `make compose-up` (e.g., `dial tcp 142.251.45.145:443: i/o timeout`), uncomment the `GOPROXY` line in the `Dockerfile`:

```dockerfile
COPY go.mod go.sum ./
# Uncomment the next line if you are in China and encounter Go module download timeouts.
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
```

Then rebuild:

```bash
make compose-down
make compose-up
```

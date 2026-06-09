# SLO.md

## Service-Level Indicators (SLIs)

### SLI 1: Job Dispatch Latency

**Definition**: Time from `POST /jobs` returning 2xx to the job reaching terminal state (`done` or `failed`), measured per job.

**Math**:
```
job_dispatch_latency = histogram of (job_terminal_timestamp - job_created_timestamp)
```

**SLO**: P99 < 35 seconds over a rolling 30-day window.

**Rationale**: The hard wall-time timeout is 30s; allowing 5s of Redis + worker overhead gives a realistic P99 target.

**Error Budget**: 1% of jobs may exceed 35s. For 100 jobs/sec = 8.64M jobs/day, budget = ~86,400 slow jobs/day.

### SLI 2: Job Success Rate

**Definition**: Ratio of jobs that complete successfully, excluding upstream 4xx (which are client errors).

**Math**:
```
job_success_rate = (jobs_done) / (jobs_done + jobs_failed - upstream_4xx)
```

**SLO**: 99.5% over a rolling 30-day window.

**Rationale**: Upstream 5xx and network blips are retryable; with 3 attempts we expect > 99.5% success for non-4xx work. This matches typical data-pipeline SLOs.

**Error Budget**: 0.5% of eligible jobs may fail. For 8.64M eligible jobs/day, budget = ~43,200 failed jobs/day.

### SLI 3: Worker Availability

**Definition**: Fraction of time the worker pool is ready to process jobs, as measured by `/readyz` returning 200.

**Math**:
```
worker_availability = (readyz_200_count) / (readyz_total_count)
```

**SLO**: 99.9% over a rolling 30-day window.

**Rationale**: Three nines is standard for internal batch/orchestration services; brief Redis blips or rolling restarts are acceptable within this budget.

**Error Budget**: 0.1% downtime = ~43 minutes/month.

### SLI 4: Ingest Latency

**Definition**: Time for `POST /jobs` to return a 2xx response, measured on the server side.

**Math**:
```
ingest_latency = histogram of (response_timestamp - request_timestamp) for POST /jobs
```

**SLO**: P99 < 100 milliseconds over a rolling 30-day window.

**Rationale**: verify.sh checks this bar explicitly (Check 15). A 100 ms P99 leaves headroom for Redis round-trips under normal load while remaining user-perceptible.

**Error Budget**: 1% of ingest requests may exceed 100 ms. For 100 jobs/sec = 8.64M requests/day, budget = ~86,400 slow ingests/day.

## Error Budget Policy

- **Release freeze trigger**: When any SLI burns > 50% of its monthly error budget in a 7-day window, feature releases are frozen until the SLO is recovered.
- **Pager trigger**: When any SLI burns > 10% of its monthly error budget in a 24-hour window, page the on-call engineer.
- **Post-mortem required**: Any single incident that consumes > 5% of monthly error budget.

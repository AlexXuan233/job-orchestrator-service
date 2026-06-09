# DESIGN.md

## Architecture

```
Client → Gin HTTP Server → Redis (dedup + queue + state)
                              ↓
                        Worker Pool (BRPOP → fetch → retry)
                              ↓
                        Mock Upstream / Real URLs
```

Data flow:
1. `POST /jobs` validates the URL, atomically creates a dedup record (SETNX, 60s TTL), persists the job as JSON, and pushes it to a Redis list `jobs:pending`.
2. Workers call `BRPOP jobs:pending 1s` and atomically acquire a per-domain slot via a Lua script (cap = 5). If capped, the job is requeued with a small backoff.
3. The worker marks the job `running`, registers it in `jobs:running` (for crash recovery), and executes the HTTP fetch with a per-attempt timeout (10s) and a 30s wall-time cap.
4. On success → `done`; on 4xx → `failed` (no retry); on 5xx/network/timeout → exponential backoff + jitter retry (max 3).
5. `POST /jobs/{id}/cancel` sets status `cancelled` in Redis and cancels the in-flight request context, causing the worker to drop the job immediately.
6. On SIGTERM the handler rejects new `POST /jobs` (503), the HTTP server drains, workers finish current attempts, and any remaining `running` jobs are requeued.

## Decisions Made + Why

| # | Decision | Why | Rejected Alternative |
|---|---|---|---|
| 1 | **Redis List for queue** | Simple requeue on shutdown and crash recovery; `BRPOP` + `LPUSH` give FIFO-ish behavior with bounded latency. | Redis Streams — better at persistence and consumer groups, but recovery logic is more complex and overkill for a single-service queue. |
| 2 | **Redis SETNX for dedup** | Atomic create-or-return-existing guarantees a race-free 1:1 mapping from URL to job_id under concurrent POSTs. | In-memory map — not shared across replicas; Bloom filter — cannot return the existing job_id. |
| 3 | **Lua-scripted per-domain slot counter** | Atomic check-and-increment across all workers; a 30s TTL auto-plugs leaks if a worker OOMs mid-fetch. | In-process semaphore — doesn't work across replicas or even across goroutines without careful sync; Redis SET of job_ids — higher memory and more complex atomic logic. |
| 4 | **Async job submission (enqueue-only)** | `POST /jobs` never waits for the fetch, so P99 ingest latency stays < 100ms even when upstream is slow. | Synchronous fetch in handler — violates the latency SLO and creates head-of-line blocking. |
| 5 | **Running-job set + startup recovery** | Jobs in `jobs:running` at startup are requeued as `pending`, satisfying "no stuck running forever" after a crash. | Pure stateless workers — would leave jobs stranded in `running` after an unclean exit. |
| 6 | **Separate worker context from signal context** | In-flight fetches are not aborted by SIGTERM; they get the full shutdown timeout to finish naturally. | Passing the signal context directly to workers — would cancel active HTTP requests on SIGTERM, turning healthy jobs into failures. |

## Failure Modes

| Failure | Impact | Mitigation |
|---|---|---|
| Redis unreachable | `/readyz` 503, enqueue/dequeue fail | Configurable dial timeout; workers skip and retry; metrics alert on readyz failures. |
| Worker OOM / crash | Jobs left in `running` | `jobs:running` set scanned on startup and requeued; slot keys have 30s TTL to prevent permanent domain lock. |
| Upstream slow / timeout | Job takes longer, worker tied up | Per-attempt 10s hard deadline + 30s wall-time cap; no unbounded waits. |
| Upstream 5xx storm | Retry blast radius | Exponential backoff with 20% jitter caps retry rate; per-domain concurrency cap prevents overwhelming one host. |
| Queue grows unbounded | Memory pressure, latency spikes | Bounded worker pool + queue-depth metric; horizontal scaling via K8s HPA when depth > threshold. |

## Capacity Estimate

**Target: 10 concurrent users, 100 jobs/sec ingestion, 1 KB avg result size.**

- **Redis memory**: Each job JSON ~1.5 KB (id, url, timestamps, result). At 100 jobs/s with 60s dedup window + processing time, steady-state ~6,000 jobs resident ≈ **9 MB**. Add queue overhead + running set ≈ **~15 MB**.
- **Worker pods**: 100 jobs/s, each job ~50ms fetch + overhead. With 10 workers per pod, 1 pod handles ~200 jobs/s. Need **1 pod** for headroom; 2 pods for HA.
- **Bottleneck**: Redis single-threaded command execution. At 100 jobs/s with ~8 Redis ops per job (enqueue, dequeue, slot, state updates), that's ~800 ops/s — well within Redis limits. Bottleneck will likely be **upstream latency** before Redis or CPU.

## AI Assistant Disclosure

This project was built with assistance from **Kimi Code CLI**

## What I Did NOT Do (and would do with more time)

1. **Redis Sentinel / Cluster** — single Redis instance is a SPOF; would add Sentinel for HA.
2. **Dead-letter queue** — permanently failed jobs are just marked failed; would add a DLQ for inspection.
3. **Structured result storage** — results are stored inline in Redis; > 1 MB results would blow memory. Would offload to S3/GCS with Redis holding a pointer.
4. **Request-level tracing** — only have correlation IDs in logs; would add OpenTelemetry traces.
5. **Rate-limiting on ingest** — no global RPS limit on `POST /jobs`; would add a token-bucket middleware.

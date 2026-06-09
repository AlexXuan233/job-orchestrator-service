# INCIDENT_RUNBOOK.md

## Incident 1: P99 Dispatch Latency Jumps from 100ms to 5s

1. **Check queue depth**: `curl -s localhost:8080/metrics | grep job_queue_depth` — if depth is growing, workers are not keeping up.
2. **Check per-domain inflight**: `curl -s localhost:8080/metrics | grep domain_inflight_current` — if one domain is at cap, a slow upstream is serializing all fetches for that host.
3. **Check Redis slowlog**: `redis-cli SLOWLOG GET 10` — look for O(N) commands (e.g., LREM on large queue) or connection pool exhaustion.
4. **Mitigation**: Scale workers horizontally (increase `WORKER_COUNT` or add pods) if queue depth is the bottleneck; if a single domain is slow, temporarily lower `PER_DOMAIN_MAX_INFLIGHT` for that domain or add it to a blocklist.
5. **Escalation**: If latency does not improve within 15 min after scaling, escalate to the upstream owner and consider temporarily rejecting jobs for the affected domain.

## Incident 2: Job Success Rate SLO Error Budget Burns at 10x Speed

1. **Check failure breakdown by status code**: `curl -s localhost:8080/metrics | grep jobs_dispatched_total` — identify if failures are 5xx, network errors, or timeouts.
2. **Check upstream health**: Hit the failing upstream directly with `curl -v` to confirm whether it's returning 5xx or timing out.
3. **Check retry metrics**: `curl -s localhost:8080/metrics | grep job_fetch_duration_seconds_count` — if retries are all timing out, the upstream may be degraded or rate-limiting us.
4. **Mitigation**: If upstream is degraded, pause ingestion for that domain by returning 503 on `POST /jobs` for matching URLs; if it's a transient network partition, ensure Redis and workers are on the same VPC/region.
5. **Escalation**: If the upstream is a third-party service and remains degraded > 30 min, escalate to the partnership team and consider activating a fallback data source.

## Incident 3: Worker Pods Entering CrashLoopBackOff

1. **Check last termination reason**: `kubectl describe pod <pod> | grep -A 5 Last State` — look for OOMKilled vs Error vs Completed.
2. **Check pod logs**: `kubectl logs <pod> --previous` — search for panic stack traces, Redis connection failures, or config parse errors.
3. **Check resource usage**: `kubectl top pod` — if CPU/memory spikes before crash, reduce `WORKER_COUNT` or increase resource limits in the Deployment.
4. **Mitigation**: If OOMKilled, increase memory limit and add a `VPA` recommendation; if panic, roll back to the last known good image and fix the bug in staging.
5. **Escalation**: If > 50% of pods are CrashLoopBackOff and rollback does not fix within 10 min, escalate to the platform/SRE team for node-level investigation.

## Incident 4: Redis Memory at 95%, Evictions Started

1. **Check memory usage by key pattern**: `redis-cli --bigkeys` and `redis-cli INFO memory` — identify if `job:*` keys or `dedup:*` keys dominate.
2. **Check eviction policy**: `redis-cli CONFIG GET maxmemory-policy` — ensure it is `allkeys-lru` or `volatile-lru`; `noeviction` would cause writes to fail.
3. **Check dedup TTL hit rate**: `redis-cli INFO stats | grep expired_keys` — if dedup keys are not expiring, the TTL may be too long for the traffic pattern.
4. **Mitigation**: Lower `DEDUP_WINDOW_SECONDS` temporarily to reduce memory pressure; if jobs are large, truncate `result` fields or offload results to object storage.
5. **Escalation**: If memory continues climbing after mitigation, escalate to the DBA/SRE team to shard Redis or upgrade instance size.

## Incident 5: Queue Depth Growing Unboundedly, Never Drains

1. **Check worker alive count**: `curl -s localhost:8080/metrics | grep jobs_inflight_total` — if inflight is stuck near worker count, all workers are blocked on slow upstreams.
2. **Check per-domain slots**: `curl -s localhost:8080/metrics | grep domain_inflight_current` — if every slot for a domain is occupied and not releasing, workers may be hung without context cancellation.
3. **Check fetch duration histogram**: `curl -s localhost:8080/metrics | grep job_fetch_duration_seconds_bucket` — confirm fetches are hitting the 10s timeout ceiling.
4. **Mitigation**: Temporarily increase `PER_DOMAIN_MAX_INFLIGHT` to drain the backlog (accept higher blast radius), or restart workers with a shorter `WORKER_ATTEMPT_TIMEOUT_MS` if upstream is truly dead.
5. **Escalation**: If queue depth exceeds 1M jobs (risk of Redis OOM), page the on-call and consider dropping non-critical jobs after a deadline rather than retrying indefinitely.

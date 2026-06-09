# DEBUGGING.md

## Pod CrashLoopBackOff — 5 Debug Steps

### 1. Inspect last termination state

```bash
kubectl describe pod -l app=job-orchestrator | grep -A 5 "Last State"
```

- `OOMKilled` → memory limit too low or memory leak; check `kubectl top pod`.
- `Error` → application panic or config failure; proceed to step 2.
- `Completed` → container exited 0 unexpectedly (liveness probe may be too aggressive).

### 2. Read previous container logs

```bash
kubectl logs -l app=job-orchestrator --previous
```

Look for:
- `config load failed` → missing or invalid env var in ConfigMap.
- `redis ping failed` → wrong `REDIS_ADDR` or network policy blocking egress to Redis.
- Panic stack trace → bug in code; capture the stack and open an incident.

### 3. Check events and resource pressure

```bash
kubectl get events --field-selector reason=FailedScheduling,reason=FailedMount
kubectl top node
```

- If nodes are at > 90% memory, the kubelet may be evicting pods.
- If `FailedScheduling`, check node selectors, taints, and resource requests.

### 4. Validate ConfigMap and env vars inside the pod

```bash
kubectl exec <pod> -- env | grep -E "WORKER|REDIS|HTTP"
kubectl get configmap job-orchestrator-config -o yaml
```

Compare the two. A mismatch means the Deployment did not roll out after a ConfigMap change. Run:

```bash
kubectl rollout restart deployment/job-orchestrator
```

### 5. Run a debug sidecar container

```bash
kubectl run debug --rm -it --image=busybox --restart=Never -- \
  wget -qO- http://job-orchestrator:8080/readyz
```

- If `readyz` returns 503, Redis is unreachable from the pod namespace.
- If connection times out, check Service selector labels and endpoint health:
  ```bash
  kubectl get endpoints job-orchestrator
  ```

## HPA: `job_queue_depth` is a custom metric

`hpa.yaml` scales on `job_queue_depth`, which is a **custom metric** from Prometheus. Out of the box, Kubernetes does not expose Prometheus metrics to the HPA.

**Prerequisite**: install the [Prometheus Adapter](https://github.com/kubernetes-sigs/prometheus-adapter) (or equivalent custom-metrics API provider) and configure it to serve the `job_queue_depth` metric.

**Verify the metric is available**:

```bash
kubectl get --raw /apis/custom.metrics.k8s.io/v1beta1/namespaces/default/pods/*/job_queue_depth
```

If the metric is missing or the API is not registered, the HPA will show `failed to get cpu utilization` or similar errors in `kubectl describe hpa job-orchestrator-hpa`.

## Common Quick Fixes

| Symptom | Likely Cause | Fix |
|---|---|---|
| `redis ping failed` | Wrong `REDIS_ADDR` or DNS | Update ConfigMap; ensure Redis Service exists in same namespace |
| `OOMKilled` | High memory usage under load | Increase memory limit; reduce `WORKER_COUNT` |
| Panic on startup | Invalid env int (e.g., `HTTP_PORT=abc`) | Validate ConfigMap values before rollout |
| Liveness probe fails | Slow startup on cold cache | Increase `initialDelaySeconds` |
| Readiness flaps | Redis transient blip | Increase `failureThreshold` or add a small `initialDelaySeconds` |

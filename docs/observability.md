# Observability: Phase 10

StatusPulse exposes three operational endpoints alongside the dashboard:

| Endpoint | Meaning |
| --- | --- |
| `/livez` | HTTP handler is responsive; returns 200 without accessing storage |
| `/readyz` | SQLite schema read succeeds within a two-second context; 503 during shutdown or storage failure |
| `/metrics` | Prometheus exposition, including worker, Go runtime, and process metrics |

Health is about StatusPulse itself. A monitored endpoint being DOWN must not
restart StatusPulse. Readiness does not prove disk writeability, available disk
space, or worker freshness. Watch worker errors and the last-cycle timestamp too.
Probe errors are sanitized; internal database details are not sent to clients.
Kubernetes now uses `/livez` for startup/liveness and `/readyz` for readiness.
The Docker HEALTHCHECK uses readiness.

## Optional local Prometheus and Grafana

From the repository root, with Docker Desktop running:

```powershell
docker compose -f compose.yaml -f compose.observability.yaml up --build -d
```

- StatusPulse: <http://localhost:8080/>
- Prometheus targets: <http://localhost:9090/targets>
- Grafana dashboard: <http://localhost:3000/d/statuspulse>

Prometheus scrapes `app:8080/metrics` every 15 seconds. Grafana is provisioned with
that Prometheus data source and the **StatusPulse Operations** dashboard. It uses
anonymous Viewer access with login disabled for this local demo. Both tools bind
their host ports to loopback. Do not expose this setup publicly. Edit the checked-in
dashboard JSON rather than trying to save changes through the read-only UI.

This starts a Compose instance of StatusPulse; it does not scrape or share the
Kubernetes database. If your Kubernetes port-forward occupies 8080, stop that
forward or set `$env:STATUSPULSE_PORT = '8081'` before the Compose command. Internal
scraping still uses port 8080. The existing Compose database is retained.

Register a service, wait for a worker cycle and scrape, and inspect the graphs.
Rate/latency panels require multiple samples and may initially show no data.

Stop the Compose stack while retaining all databases:

```powershell
docker compose -f compose.yaml -f compose.observability.yaml down
```

Prometheus retains at most about seven days or 512 MB of time-series blocks
(WAL/head overhead is additional). Named volumes persist across restarts. Do not
add `--volumes` unless you intend to remove application and monitoring data.

## Metrics and interpretation

| Metric | Interpretation |
| --- | --- |
| `statuspulse_checks_total{status="UP\|DOWN"}` | Persisted observations since this process started |
| `statuspulse_worker_errors_total{operation="list\|check\|save"}` | Internal worker failures; endpoint DOWN is a normal result, not an internal error |
| `statuspulse_check_response_duration_seconds` | Histogram of time to HTTP response headers for persisted results, including non-2xx responses |
| `statuspulse_services` | Service count from the worker's last successful list; can lag CRUD changes |
| `statuspulse_last_cycle_timestamp_seconds` | Last completed cycle, even if some individual operations failed; zero until first completion |
| `go_*`, `process_*` | Runtime/process measurements supplied by the official client |
| `up{job="statuspulse"}` | Prometheus's scrape success, not endpoint uptime or database readiness |

Counters reset on process restart; `rate()` handles resets. SQLite remains the
source of per-service health history and observed uptime. These aggregate metrics
do not replace it. Canceled checks and failed saves do not increment check counts.
Network failures have no response latency, so they are excluded from the histogram.
No URLs, service names, IDs, or arbitrary error strings appear as metric labels.

Useful PromQL:

```promql
sum by (status) (rate(statuspulse_checks_total[5m]))
sum by (operation) (rate(statuspulse_worker_errors_total[5m]))
histogram_quantile(0.95, sum by (le) (rate(statuspulse_check_response_duration_seconds_bucket[5m])))
time() - (statuspulse_last_cycle_timestamp_seconds > 0)
```

A long cycle can be valid with sequential checks and many slow services. Evaluate
cycle age against the interval, service count, and request timeout. An empty service
list still completes cycles. There are no alerts/notifications or log aggregation
services in this phase.

## JSON logs

The executable configures standard-library `slog` with a JSON handler on stdout.
Startup, shutdown, and errors retain their existing events. Each saved check logs
`service_id`, `status`, `http_status_code`, `response_time_ms`, and `error_kind`.
Normal check logs omit URLs and response bodies; internal error logs can still
contain diagnostic details. Metrics expose totals; logs explain individual events.

```powershell
docker compose logs -f app
kubectl --context docker-desktop -n statuspulse logs deployment/statuspulse --tail=50
```

## Upgrade the Kubernetes application

```powershell
docker build -t statuspulse:phase10 .
kubectl --context docker-desktop apply -f deploy/kubernetes/deployment.yaml
kubectl --context docker-desktop -n statuspulse rollout status deployment/statuspulse --timeout=180s
kubectl --context docker-desktop -n statuspulse port-forward service/statuspulse 8080:8080
```

The existing PVC is retained. Applying the manifest sets replicas to one. Health
and metrics are available at the same forwarded port. The optional Grafana stack
above monitors Compose; a cluster Prometheus installation would instead scrape
`statuspulse.statuspulse.svc:8080` using an in-cluster target. No Prometheus
Operator, Helm dependency, or cluster-wide permissions are introduced.

## Before calling this phase complete

Implementation verification: all Go tests passed with Linux race detection, and
`go vet`, formatting, workflow lint, Compose validation, and `promtool check config`
passed. An isolated Compose run verified all three endpoints, a healthy scrape,
a recorded UP counter in Prometheus, JSON check logs, the eight provisioned panels,
and a successful Grafana data-source health query. The container smoke test passed,
including graceful shutdown. Kubernetes rolled out the Phase 10 image and reported
Ready with its new probes. The isolated test stack and test volumes were removed;
the Kubernetes deployment and its original PVC were retained.

- Understand liveness versus readiness and external endpoint status.
- Find `/metrics`, a Prometheus target, and the provisioned Grafana dashboard.
- Explain counters, gauges, histogram buckets, scrape intervals, and rate windows.
- Correlate a failed check with its log and metrics without adding unbounded labels.
- Know that monitoring volumes and SQLite have separate retention/lifecycles.

References: [Prometheus Go client](https://prometheus.io/docs/guides/go-application/),
[instrumentation guidance](https://prometheus.io/docs/practices/instrumentation/),
and [Grafana provisioning](https://grafana.com/docs/grafana/latest/administration/provisioning/).

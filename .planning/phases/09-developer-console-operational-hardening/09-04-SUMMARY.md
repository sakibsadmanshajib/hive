---
phase: 09-developer-console-operational-hardening
plan: "04"
subsystem: infra
tags: [prometheus, grafana, alertmanager, docker-compose, observability, metrics]

requires:
  - phase: 08-payments-fx-and-compliance-checkout
    provides: payment rail implementations that produce hive_payment_events_total observations

provides:
  - Prometheus metrics package with 8 metric families on control-plane /metrics endpoint
  - HTTP instrumentation middleware with UUID-normalized labels on both Go services
  - Edge-api /metrics endpoint via custom prometheus.Registry
  - Docker Compose monitoring profile (Prometheus + Grafana + Alertmanager)
  - Grafana dashboard covering all 4 signal categories
  - Alertmanager with 3 critical alert rules

affects:
  - 09-01 (Wave 2 — depends on NewRouter returning http.Handler)
  - Any future plan adding routes to control-plane router

tech-stack:
  added:
    - github.com/prometheus/client_golang v1.23.2 (both control-plane and edge-api)
    - prom/prometheus:latest (Docker)
    - grafana/grafana:latest (Docker)
    - prom/alertmanager:latest (Docker)
  patterns:
    - Custom prometheus.Registry per service (not DefaultRegistry) — excludes Go runtime metrics
    - ExternalMux pattern: main.go pre-creates *http.ServeMux, passes via RouterConfig.Mux so filestore.RegisterRoutes still receives *http.ServeMux after NewRouter wraps with http.Handler
    - UUID-normalizing endpoint labels via regex replace — prevents cardinality explosion
    - InstrumentHandler middleware wraps full mux; /metrics registered on inner mux before wrapping

key-files:
  created:
    - apps/control-plane/internal/platform/metrics/metrics.go
    - apps/control-plane/internal/platform/metrics/middleware.go
    - apps/control-plane/internal/platform/metrics/metrics_test.go
    - apps/edge-api/internal/proxy/metrics.go
    - deploy/prometheus/prometheus.yml
    - deploy/prometheus/alerts.yml
    - deploy/grafana/provisioning/datasources/prometheus.yml
    - deploy/grafana/provisioning/dashboards/dashboard.yml
    - deploy/grafana/dashboards/hive-overview.json
    - deploy/alertmanager/alertmanager.yml
  modified:
    - apps/control-plane/internal/platform/http/router.go
    - apps/control-plane/cmd/server/main.go
    - apps/edge-api/cmd/server/main.go
    - apps/control-plane/go.mod
    - apps/control-plane/go.sum
    - apps/edge-api/go.mod
    - apps/edge-api/go.sum
    - deploy/docker/docker-compose.yml

key-decisions:
  - "ExternalMux pattern: RouterConfig.Mux field lets main.go pre-create *http.ServeMux so filestore.RegisterRoutes (which requires *http.ServeMux) works after NewRouter returns http.Handler"
  - "NewRouter returns http.Handler (not *http.ServeMux) — Plan 01 Wave 2 depends on this changed signature"
  - "Custom prometheus.Registry per service (not DefaultRegistry) — excludes Go runtime noise from /metrics output"
  - "UUID normalization via regexp.MustCompile in normalizeEndpoint — ensures raw UUIDs never appear as label values"

patterns-established:
  - "Metrics ExternalMux: pre-create mux in main.go, pass via RouterConfig.Mux, register filestore routes on mux directly, NewRouter wraps with InstrumentHandler"
  - "Low-cardinality labels: endpoint uses normalized path, status_class uses Nxx groups, never UUIDs or user IDs"

requirements-completed:
  - OPS-01

duration: 15min
completed: "2026-04-11"
---

# Phase 09 Plan 04: Prometheus Metrics & Monitoring Stack Summary

**Prometheus instrumentation added to both Go services with custom registry, 8 metric families, UUID-normalizing middleware, and a Docker Compose monitoring profile (Prometheus + Grafana + Alertmanager) with pre-provisioned dashboards covering all 4 OPS-01 signal categories**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-04-11T04:35:00Z
- **Completed:** 2026-04-11T04:44:32Z
- **Tasks:** 2 of 3 (Task 3 is checkpoint:human-verify)
- **Files modified:** 17

## Accomplishments

- Both Go services (control-plane, edge-api) compile with prometheus/client_golang v1.23.2
- /metrics endpoint on control-plane serves 8 custom metric families via custom registry
- HTTP instrumentation middleware wraps all requests with request count, duration, method, and status class labels — UUIDs normalized to `:id` to prevent cardinality explosion
- NewRouter now returns `http.Handler` (not `*http.ServeMux`) as required by Plan 01 Wave 2
- Edge-api /metrics endpoint added with 4 metric families (HTTP + upstream)
- Docker Compose monitoring profile: `docker compose --profile monitoring up` starts Prometheus, Grafana, Alertmanager
- Grafana auto-provisioned with Prometheus datasource and "Hive Platform Overview" dashboard (4 signal rows)
- 3 critical Alertmanager rules: HighAPIErrorRate, UpstreamProviderDown, PaymentWebhookFailures

## Task Commits

1. **Task 1: Prometheus metrics instrumentation (both services)** - `23b64fd` (feat)
2. **Task 2: Monitoring Docker Compose profile** - `3eba5d9` (feat)

## Files Created/Modified

- `apps/control-plane/internal/platform/metrics/metrics.go` — 8-metric Prometheus registry with custom registry
- `apps/control-plane/internal/platform/metrics/middleware.go` — InstrumentHandler with UUID-normalizing endpoint labels
- `apps/control-plane/internal/platform/metrics/metrics_test.go` — 4 unit tests (TestNewRegistry, TestInstrumentHandler, TestMetricsEndpointServesValidPrometheus, TestNormalizeEndpoint)
- `apps/control-plane/internal/platform/http/router.go` — Returns http.Handler; adds MetricsRegistry, PrometheusRegistry, Mux fields to RouterConfig; registers /metrics
- `apps/control-plane/cmd/server/main.go` — Wires metrics.NewRegistry(), creates ExternalMux, passes both to RouterConfig
- `apps/edge-api/internal/proxy/metrics.go` — EdgeMetrics with 4 families, InstrumentHandler, MetricsHandler
- `apps/edge-api/cmd/server/main.go` — Wires proxy.NewEdgeMetrics(), /metrics on mux, wraps handler chain with InstrumentHandler
- `apps/control-plane/go.mod` / `apps/edge-api/go.mod` — prometheus/client_golang v1.23.2 as direct dependency
- `deploy/prometheus/prometheus.yml` — Scrapes control-plane:8081 and edge-api:8080 every 10s
- `deploy/prometheus/alerts.yml` — 3 critical alert rules
- `deploy/alertmanager/alertmanager.yml` — Default webhook receiver (email/Slack config commented)
- `deploy/grafana/provisioning/datasources/prometheus.yml` — Prometheus datasource at http://prometheus:9090
- `deploy/grafana/provisioning/dashboards/dashboard.yml` — Dashboard provisioner from /var/lib/grafana/dashboards
- `deploy/grafana/dashboards/hive-overview.json` — "Hive Platform Overview" with 4 rows, 9 panels
- `deploy/docker/docker-compose.yml` — prometheus, grafana, alertmanager services under monitoring profile; prometheus-data, grafana-data volumes

## Decisions Made

- **ExternalMux pattern:** `filestore.RegisterRoutes` requires `*http.ServeMux`. Since `NewRouter` now returns `http.Handler`, main.go pre-creates the mux, passes it via `RouterConfig.Mux`, and calls `filestore.RegisterRoutes(routerMux, ...)` directly. `NewRouter` wraps the same mux with `InstrumentHandler`.
- **Custom registry per service:** Not using `prometheus.DefaultRegistry` so Go runtime metrics (gc, goroutines, etc.) don't appear in the application /metrics endpoint. Only application-defined metrics are exposed.
- **UUID normalization via regexp:** Using `regexp.MustCompile` with a UUID pattern rather than static path prefix matching — more robust and handles any future paths with UUIDs without code changes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical] More robust UUID normalization**
- **Found during:** Task 1 (normalizeEndpoint implementation)
- **Issue:** Plan suggested static string prefix checks for UUID normalization, which only covers a few known paths
- **Fix:** Used `regexp.MustCompile` to match any UUID pattern in any path segment — handles all current and future paths generically
- **Files modified:** `apps/control-plane/internal/platform/metrics/middleware.go`, `apps/edge-api/internal/proxy/metrics.go`
- **Verification:** TestNormalizeEndpoint passes — UUID in endpoint label triggers test failure
- **Committed in:** 23b64fd (Task 1 commit)

**2. [Rule 3 - Blocking] ExternalMux for filestore.RegisterRoutes compatibility**
- **Found during:** Task 1 (router.go return type change)
- **Issue:** `filestore.RegisterRoutes(router, ...)` in main.go passes `router` to a function requiring `*http.ServeMux`. Changing NewRouter's return type to `http.Handler` would break this call.
- **Fix:** Added `Mux *http.ServeMux` field to RouterConfig. Main.go pre-creates mux, passes it in, and also uses it for filestore registration. NewRouter uses the provided mux and wraps it.
- **Files modified:** `apps/control-plane/internal/platform/http/router.go`, `apps/control-plane/cmd/server/main.go`
- **Verification:** `go build ./...` passes for control-plane
- **Committed in:** 23b64fd (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (1 missing critical, 1 blocking)
**Impact on plan:** Both necessary for correctness and compilation. No scope creep.

## Issues Encountered

- Go not installed on host — used `docker run golang:1.24` for `go get`, `go build`, and `go test` commands.
- VCS stamping error in Docker build fixed with `-buildvcs=false` flag.

## User Setup Required

To start the monitoring stack:
```bash
cd deploy/docker
docker compose --profile monitoring up -d
```

Verification:
- Prometheus: http://localhost:9090/targets — control-plane and edge-api should appear
- Grafana: http://localhost:3001 — "Hive Platform Overview" dashboard in Hive folder
- Alertmanager: http://localhost:9093
- Control-plane metrics: `curl http://localhost:8081/metrics`

For production alerting, edit `deploy/alertmanager/alertmanager.yml` to configure email or Slack.

## Next Phase Readiness

- Plan 01 Wave 2 can now add routes to the router since NewRouter returns `http.Handler`
- Both services expose `/metrics` endpoints ready for Prometheus scraping
- Monitoring stack starts cleanly with a single Docker Compose command
- Task 3 (human verification checkpoint) requires operator to confirm Grafana dashboard renders correctly

---
*Phase: 09-developer-console-operational-hardening*
*Completed: 2026-04-11*

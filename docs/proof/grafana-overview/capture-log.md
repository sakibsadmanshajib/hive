# Grafana Hive Overview dashboard: proof capture log

Date: 2026-08-24
Box: hive-demo (192.168.80.79), containers hive-prometheus-1 + hive-grafana-1
Grafana 13.1.1, Prometheus datasource uid `prometheus` (pinned in provisioning)

## What changed

- `deploy/grafana/dashboards/hive-overview.json` rewritten as a curated "Hive Overview" (uid `hive-platform-overview`, folder `Hive`, provisioned from file, `provisionedExternalId: hive-overview.json`).
- `deploy/grafana/provisioning/datasources/prometheus.yml` now pins `uid: prometheus` so the dashboard's datasource reference resolves on any install.

## Verification evidence (all commands run against the live box)

1. Metric names verified before writing queries: `GET http://localhost:9090/api/v1/label/__name__/values` returned `hive_http_requests_total` (labels: `job`, `endpoint`, `method`, `status_class`), `hive_http_request_duration_seconds_bucket`, `process_resident_memory_bytes`, `up`. No `hive_upstream_*`, `hive_payment_events_total`, `hive_ledger_postings_total`, `hive_rate_limit_hits_total`, `hive_auth_failures_total`, and no credit/session/container metrics exist in this stack, so the old dashboard's provider, billing, and rate-limit/auth rows queried nothing, and no credits-consumed or active-sessions panel is possible without new instrumentation.
2. Every panel query tested against Prometheus `/api/v1/query` and returned live series before the dashboard was written.
3. Provisioning verified via Grafana HTTP API with admin creds from box `.env` (`GRAFANA_ADMIN_*`, never printed): `GET /api/dashboards/uid/hive-platform-overview` returned title "Hive Overview" version 2 with `provisionedExternalId: hive-overview.json`; `GET /api/ds/query` with datasource uid `prometheus` returned status 200 frames.
4. Screenshots captured with headless Firefox inside the `mcr.microsoft.com/playwright` container on the box, against `http://localhost:3001/d/hive-platform-overview/hive-overview?orgId=1&from=now-1h&to=now&kiosk` after login via the admin API; credentials never left the box.

## Screenshots

- `grafana-overview-kiosk.png` (full page, kiosk mode): Service Health row with all four services UP (alertmanager, control-plane, edge-api, prometheus), Process Memory (RSS) timeseries, Request Rate by status class, Error Rate gauge at 0%, Latency p95/p50.
- `grafana-overview-header.png`: same dashboard with the Grafana header visible.

## Known gap, stated honestly

Credits consumed and active sessions have no exported Prometheus metrics on this stack (checked the full `/api/v1/label/__name__/values` list). Adding those panels requires app instrumentation in control-plane/edge-api, which is out of scope for a dashboard-only change. The dashboard description states this on the dashboard itself.

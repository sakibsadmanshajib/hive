# Runbook: Provider Circuit Breaker

## Context

The Provider Registry uses a circuit breaker pattern to prevent cascading failures. When a provider (Ollama, Groq, etc.) fails repeatedly, its "circuit" opens, and the registry will skip it for a period of time, falling back to the next available provider in the chain.

This reduces latency for users (by avoiding timeouts on known-failing providers) and protects failing downstream services from further load.

## Circuit States

- **CLOSED** (Normal): The provider is behaving correctly. Requests are sent to it.
- **OPEN** (Tripped): The provider has failed `PROVIDER_CB_THRESHOLD` times. Requests are skipped and failover to the next provider happens immediately.
- **HALF_OPEN** (Recovering): After `PROVIDER_CB_RESET_MS`, the circuit allows a single request through.
    - If it succeeds, the circuit returns to **CLOSED**.
    - If it fails, the circuit returns to **OPEN** and the timer resets.

## Configuration

These are set in `.env`:

| Variable | Default | Description |
|----------|---------|-------------|
| `PROVIDER_CB_THRESHOLD` | `5` | Number of consecutive failures before tripping the circuit. |
| `PROVIDER_CB_RESET_MS` | `30000` | How long (in ms) to stay in OPEN state before trying again. |

## Monitoring Status

### Public API
`GET /v1/providers/status`
Providers in OPEN state will show `state: "circuit-open"`.

`GET /v1/providers/metrics`
Returns public-safe provider-level counters and latency summaries:
- `requests`
- `errors`
- `errorRate`
- `latencyMs.avg`
- `latencyMs.p95`
- `enabled`
- `healthy`
- `circuitState`

```bash
curl -s http://127.0.0.1:8080/v1/providers/metrics
```

### Internal API
`GET /v1/providers/status/internal` (Requires `x-admin-token`)
Includes the exact failure count, state (`CLOSED`, `OPEN`, `HALF_OPEN`), and the last error encountered.

```bash
curl -s http://127.0.0.1:8080/v1/providers/status/internal \
  -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

`GET /v1/providers/metrics/internal` (Requires `x-admin-token`)
Includes the provider health-check `detail`, exact circuit failure count, and last circuit error together with the provider-level counters and latency summaries.

```bash
curl -s http://127.0.0.1:8080/v1/providers/metrics/internal \
  -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

`GET /v1/providers/metrics/internal/prometheus` (Requires `x-admin-token`)
Returns Prometheus exposition text from the in-process metrics registry for operator scraping or ad hoc inspection.

```bash
curl -s http://127.0.0.1:8080/v1/providers/metrics/internal/prometheus \
  -H "x-admin-token: <ADMIN_STATUS_TOKEN>"
```

```json
{
  "name": "ollama",
  "enabled": true,
  "healthy": false,
  "detail": "connection refused",
  "circuit": {
    "state": "OPEN",
    "failures": 5,
    "lastError": "ollama: connection refused"
  }
}
```

## Troubleshooting

### Why is the circuit open?
1. Check the `circuit.lastError` field in the internal status endpoint for the reason the circuit tripped.
2. Check the `detail` field for the output of the most recent health probe (which may differ from the error that tripped the circuit).
3. Verify downstream connectivity (e.g., `curl localhost:11434` for Ollama).
4. Check provider logs (e.g., `docker compose logs ollama`).

### Why did the metrics reset?
Provider metrics are currently in-memory per API process. Restarting the API resets:
- request counters
- error counters
- latency summaries
- circuit-breaker state

This repository does not assume an ELK stack or external metrics pipeline yet. Current metrics are pull-based only.

### How to reset a circuit?
Currently, circuits and provider metrics are in-memory per API instance. Restarting the API service will reset all circuits to `CLOSED` and clear provider metrics.
```bash
docker compose restart api
```

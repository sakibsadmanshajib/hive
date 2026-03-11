# Provider Metrics Endpoints Design

## Goal

Expose provider-level metrics for latency, request volume, errors, and health through both a public-safe endpoint and an admin-protected internal endpoint, without changing existing provider routing or provider status security boundaries.

## Scope

- Provider-level metrics only
- Public-safe JSON metrics endpoint
- Admin-protected internal JSON metrics endpoint
- Optional admin-protected Prometheus text scrape endpoint
- In-memory metrics only for the first cut
- Reuse provider health checks from existing `status()` implementations

## Architecture

Add a dedicated provider metrics module in `apps/api` using `prom-client` as the in-process metrics library. Instrument provider request attempts inside `ProviderRegistry.chat()` so both successful executions and failed fallback attempts contribute to provider-level counters and latency observations.

Keep health and circuit-breaker visibility pull-based. On metrics reads, query the current provider `status()` output and circuit-breaker state to populate gauges and JSON summaries. This avoids background jobs, external collectors, or ELK assumptions.

Add new routes instead of extending the existing provider status routes:

- `GET /v1/providers/metrics`
- `GET /v1/providers/metrics/internal`
- `GET /v1/providers/metrics/internal/prometheus`

The existing `/v1/providers/status` and `/v1/providers/status/internal` routes remain unchanged.

## Data Flow

1. Request enters `ProviderRegistry.chat()`.
2. For each provider attempt, start a timer immediately before `client.chat()`.
3. On success:
   - increment provider request counter
   - observe provider latency
   - preserve existing response/fallback behavior
4. On failure:
   - increment provider request counter
   - increment provider error counter
   - observe provider latency for the failed attempt
   - preserve existing fallback behavior
5. On metrics reads:
   - gather current provider health via `client.status()`
   - gather current circuit-breaker state from the registry
   - return sanitized JSON publicly
   - return detailed JSON and optional Prometheus text internally

## Endpoint Contract

Public route returns provider-safe aggregates only, for example:

```json
{
  "object": "providers.metrics",
  "data": [
    {
      "name": "ollama",
      "enabled": true,
      "healthy": true,
      "circuitState": "closed",
      "requests": 128,
      "errors": 7,
      "errorRate": 0.0547,
      "latencyMs": {
        "avg": 412,
        "p95": 890
      }
    }
  ]
}
```

Internal route adds operator-facing detail such as:

- provider `detail` from health checks
- circuit failure count
- last circuit error
- scrape timestamp
- raw Prometheus exposition on the dedicated scrape route

## Error Handling

- Metrics collection must never break inference behavior.
- If instrumentation throws unexpectedly, suppress the metrics failure and continue provider execution.
- If a provider health check fails during metrics reads, keep the endpoint available and return degraded data for that provider; only the internal route may expose diagnostic detail.
- Internal route auth behavior mirrors `/v1/providers/status/internal`.

## Operational Model

- No ELK or external metrics pipeline is assumed.
- Metrics are in-memory per API instance.
- Metrics reset on API restart, matching the existing circuit-breaker persistence model.
- Provider health checks are pull-based on metrics reads; if later needed, a short TTL cache can be added without changing route shape.

## Testing

- Add provider registry tests for success/failure counter and latency recording behavior.
- Add route tests for public sanitization and internal token protection.
- Add route tests proving internal metrics contain operational detail omitted from the public endpoint.
- Update operator docs to explain endpoint usage and the in-memory reset behavior.

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

### Internal API
`GET /v1/providers/status/internal` (Requires `x-admin-token`)
Includes the exact failure count and state (`CLOSED`, `OPEN`, `HALF_OPEN`).

```json
{
  "name": "ollama",
  "enabled": true,
  "healthy": false,
  "circuit": {
    "state": "OPEN",
    "failures": 5
  }
}
```

## Troubleshooting

### Why is the circuit open?
1. Check the `detail` field in the internal status endpoint for the last error message.
2. Verify downstream connectivity (e.g., `curl localhost:11434` for Ollama).
3. Check provider logs (e.g., `docker compose logs ollama`).

### How to reset a circuit?
Currently, circuits are in-memory per API instance. Restarting the API service will reset all circuits to `CLOSED`.
```bash
docker compose restart api
```

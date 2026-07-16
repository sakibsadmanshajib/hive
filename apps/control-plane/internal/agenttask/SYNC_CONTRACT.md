# Agent task sync contract (issue #311, blueprint Step 3.4)

Server-side backing store so a task started in one web session is visible
and resumable from another web session, tenant-scoped. This is the contract
the Wave 4 desktop consumer attaches to; nothing here is desktop-specific.

## State machine

```
queued ──> running ──> succeeded
  │           │
  │           └───────> failed
  │
  └────────────────────> cancelled
```

`queued` and `running` are the only states `cancel` accepts from. `succeeded`,
`failed`, and `cancelled` are terminal: no further transition is allowed
(`ErrTerminalState`).

## Wire shapes (JSON)

Task, as returned by every endpoint below:

```json
{
  "id": "uuid",
  "pack": "coding-pack | knowledge-work-pack",
  "status": "queued | running | succeeded | failed | cancelled",
  "engine_session_ref": "string, empty until running",
  "result_summary_ref": "string, empty until succeeded (or failed with partial output)",
  "error_message": "string, empty unless failed",
  "created_at": "RFC3339",
  "updated_at": "RFC3339",
  "started_at": "RFC3339 | null",
  "finished_at": "RFC3339 | null"
}
```

`tenant_id` and `user_id` never appear in a response body: both are implied
by the authenticated caller, never round-tripped.

## Edge-api surface (customer-facing, auth required, gated by feature
`ENABLE_COWORK`)

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/agent/tasks` | `{"pack": "..."}` | 201 with the created Task |
| GET | `/v1/agent/tasks` | — | `{"tasks": [Task, ...]}`, newest first, scoped to the caller |
| GET | `/v1/agent/tasks/{id}` | — | 404 if the task belongs to a different user or does not exist |
| POST | `/v1/agent/tasks/{id}/cancel` | — | 409 if the task already reached a terminal state |

Status is read by polling `GET /v1/agent/tasks/{id}`. No SSE/websocket
channel ships in this step — the existing SSE pattern in this repo (see
`apps/edge-api/internal/anthropic/stream.go`) is a provider-response
translator wired around one in-flight LLM call, not a general task-status
push channel, and building one is out of scope here. If a live-updating
panel needs push updates later, add a `GET /v1/agent/tasks/{id}/events` SSE
endpoint that polls the same store server-side and streams deltas; the Task
shape above does not change.

## Control-plane internal surface (shared-secret `X-Internal-Token`, not
customer-facing)

`tenant_id` and `user_id` travel as URL path segments, never body/query/
header, mirroring `apps/control-plane/internal/marketplace`'s internal read
surface — the process boundary means edge-api resolves both from the
caller's authenticated request context before building the path, never from
untrusted client input.

| Method | Path | Body |
|---|---|---|
| POST | `/internal/agent-tasks/{tenant_id}/{user_id}` | `{"pack": "..."}` |
| GET | `/internal/agent-tasks/{tenant_id}/{user_id}` | — |
| GET | `/internal/agent-tasks/{tenant_id}/{user_id}/{task_id}` | — |
| POST | `/internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/cancel` | — |

## Engine seam (known gap)

`Service.CreateTask` calls `Engine.Launch(ctx, task)` right after persisting
a `queued` row. The only `Engine` wired today is `NotConfiguredEngine`,
which returns `ErrEngineNotConfigured` and leaves the task `queued` — that is
treated as an open seam, not a task failure. `apps/agent-engine`'s Wave 2.2
CLI binds a host port for one sandbox session, but the control channel this
package needs (submit a task, get a session reference back, later learn
success/failure) requires a second host <-> agent-server channel that the
sandbox's `--network none` egress profile currently cuts off. Wiring a real
`Engine` that talks to that channel is Wave 4 work (desktop control channel)
or a follow-up once a server-side equivalent exists; `Service` and the HTTP
surface do not need to change when that lands, only the `Engine`
implementation passed to `NewService`.

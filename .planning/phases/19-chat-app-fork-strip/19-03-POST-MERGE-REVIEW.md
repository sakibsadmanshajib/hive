# Phase 19 Plan 03 — Post-Merge Adversarial Review

PR #143 (merge commit `86f160d`) — "feat(phase-19): plan 03 — deploy OWUI + chat dispatch + audit sink fanout"

Five parallel reviewers: Gemini 3.1 pro (CLI direct), ECC `go-reviewer`, ECC `database-reviewer`, ECC `security-reviewer`, ECC `code-reviewer`. Generated 2026-05-18.

## Consolidated findings (deduped)

### CRITICAL

| ID | Theme | Source(s) | Files | Summary |
|----|-------|-----------|-------|---------|
| C1 | Partition cliff 2026-07-01 | db | `supabase/migrations/20260516_04_phase19_audit_log.sql:54-57`, `20260516_06_phase19_llm_traces.sql:29-32` | Only `_2026_05` + `_2026_06` partitions. No DEFAULT partition. Every audit + LLM trace INSERT fails after 2026-06-30 23:59:59 UTC unless an unwritten cron creates next month's partition. |
| C2 | Canonical hash diverges across writers | go, gemini | `apps/control-plane/internal/audit/sync.go:65-77`, `apps/edge-api/internal/chat/audit.go:55-88`, `internal/audit/canonical.go:48`, `chat/audit.go:159` | (a) `SyncWriter` reads partition-global `MAX(seq)` + `prev_hash` without tenant filter → cross-tenant chain linkage. (b) `time.RFC3339Nano` writes 9-digit fractional seconds; Postgres `timestamptz` stores 6 → verifier re-canonicalization mismatch unless ts is re-formatted from DB read. |
| C3 | Cross-partition chain stitching missing | go, gemini, security | `apps/control-plane/internal/auditverifier/verifier.go:66-70`; chain bootstrap on `audit.go:70-88` and `sync.go:65-77` restricted to current-month window | Verifier seeds `expectedPrev` to `zeroHash` per tenant per month. First row of each new month either generates false-positive mismatch or silently accepts a tampered split. Attacker can delete month-boundary rows undetected. |
| C4 | JWT path mismatch — OWUI body metadata vs edge-api header | gemini, security | `deploy/docker/pipelines/hive_jwt_forward.py:35-43`, `apps/edge-api/cmd/server/main.go:333` | Pipeline injects user JWT into `__metadata.upstream_auth` (request body). edge-api's `auth.UserFrom(r.Context())` reads the `Authorization` header which Caddy/OWUI overrides with the `owui-shim-key`. Result: every OWUI request is treated as API-key auth and the per-user JWT is silently dropped. **CONFIRM live before next deploy.** |

### HIGH

| ID | Theme | Source(s) | Files | Summary |
|----|-------|-----------|-------|---------|
| H1 | Sink fanout serial → lease TTL violation | go, gemini | `apps/control-plane/internal/auditworker/worker.go:135-163` | Batch of 50 with 5 s timeout per sink can hold lease for 250 s; default `LeaseTTL=2 min`. Other workers reclaim mid-batch → duplicate deliveries. |
| H2 | `LeaseTTL.String()` format vs `$1::interval` cast | gemini | `apps/control-plane/internal/auditworker/worker.go:104,115` | Go `time.Duration.String()` returns `"2m0s"`. Postgres interval parser may reject or interpret differently. Worth a smoke test with the live config. |
| H3 | RLS missing on `audit_log`, `audit_outbox`, `audit_outbox_dlq`, `llm_traces` | db, security | `supabase/migrations/20260516_04_*.sql`, `20260516_05_*.sql`, `20260516_06_*.sql` | Tables public; tenant scoping enforced only at the app layer. Misconfigured PostgREST/service-role exposure leaks cross-tenant audit data. |
| H4 | PII forwarded raw to Datadog/ELK/Splunk/Loki | security | `apps/control-plane/internal/auditworker/sinks/{datadog,elk,splunk,loki}.go`, `worker.go:183-216 loadPayload` | `source_ip`, `actor_id`, `user_agent`, `before_json`, `after_json` POSTed verbatim. Sentry + Langfuse correctly allow-list; the other four do not. PIPEDA data-minimization breach. |
| H5 | `seq` UNIQUE index per-partition only → cliff at new-partition create | db | `supabase/migrations/20260516_04_phase19_audit_log.sql:64-65` | Index on `(seq)` lives on child partitions only. `MAX(seq)` against parent does parallel seq-scan; new partitions skip the constraint silently. |
| H6 | CHECK constraint widening without `NOT VALID` | db, gemini | `supabase/migrations/20260518_02_phase19_embedding_openrouter_primary.sql:34-38` | `ALTER TABLE ADD CONSTRAINT` takes `AccessExclusiveLock` and validates against existing rows. Pre-existing `health_state='unhealthy'` rows abort the migration silently inside `DO $$`. |
| H7 | Caddy admin-strip regex incomplete | gemini, security | `deploy/docker/Caddyfile.owui:8` | Regex misses `auths/sign_up` (FastAPI underscore form), `/api/v1/users/`, `auths/add`, `auths/update`. Direct backend curl bypasses the UI block. |
| H8 | OWUI `OPENAI_API_KEY: "owui-shim-key"` hardcoded in compose | code | `deploy/docker/docker-compose.yml:235,240` | Static string in compose, not env-referenced. OWUI may log key prefix; downstream log aggregation captures it. Inconsistent with every other secret. |
| H9 | Real Supabase project URL in `.env.example` | code | `.env.example:10,14,15,87,95` | `<PROJECT_REF>.supabase.co` appears five times as a non-empty default. Fork or public mirror exposes prod project ref. |
| H10 | Provider name in operator logs | security | `apps/edge-api/internal/errors/provider_blind.go:178-188` | `logProviderBlindUpstreamError` writes raw `alias` and `raw_message` to stdout. Compromised Loki/ELK exposes provider routing topology. |
| H11 | FX/USD zero-leak — postfix `0.002 USD` slips regex | gemini | `apps/edge-api/internal/errors/codes.go:29` | Pattern `\$\d+(?:\.\d+)?` only matches prefix `$`. OpenRouter 402 surfaces `"costs 0.002 USD"`. **Verify against actual upstream error fixtures before treating as confirmed.** |
| H12 | `auditWALDir` default `/var/lib/hive/audit-wal` not mounted | gemini | `apps/control-plane/cmd/server/main.go:352`, `deploy/docker/docker-compose.yml` | No volume mount; non-root container fails to `mkdir`. Even if it boots, WAL data is ephemeral. **Verify whether main.go actually defaults to that path or to a per-tmp.** |

### MEDIUM

| ID | Theme | Source(s) | Files |
|----|-------|-----------|-------|
| M1 | `payments` goroutine uses startup `ctx` (10 s) instead of `runCtx` — dies silently | go | `apps/control-plane/cmd/server/main.go:521-536` |
| M2 | `alertRunner.Start` + `invoicesCron.Start` use `context.Background()` not `runCtx` | go | `apps/control-plane/cmd/server/main.go:275,301` |
| M3 | Two independent `canonicalRow` structs (chat/audit.go + verifier.go) drift risk | go | `apps/edge-api/internal/chat/audit.go:147`, `internal/auditverifier/verifier.go:162` |
| M4 | Audit insert serialized via advisory lock on hot path → throughput ceiling | code, db | `apps/edge-api/internal/chat/audit.go:39,55` |
| M5 | `auth.jwt()` not SELECT-wrapped in RLS — per-row eval | db | `supabase/migrations/20260516_01_phase19_tenants.sql:22-25`, `20260516_08`, `20260516_09` |
| M6 | `audit_outbox_eligible` index missing `claimed_at` — defeats partial-index plan | db | `supabase/migrations/20260518_01_phase19_audit_outbox_claim.sql:29-31` |
| M7 | `audit_cold_archive_manifest` has no GRANT, no RLS | db | `supabase/migrations/20260516_04_phase19_audit_log.sql:77-85` |
| M8 | Langfuse `metadata: after` passes raw `after_json` despite content gate | security | `apps/control-plane/internal/auditworker/sinks/langfuse.go:37-44` |
| M9 | `hive_jwt_forward.py` falls back from `access_token` to `id_token` | security | `deploy/docker/pipelines/hive_jwt_forward.py:35-39` |
| M10 | `open-webui` service has no `mem_limit` / `cpus` constraint | code | `deploy/docker/docker-compose.yml:197-259` |
| M11 | `Caddyfile.owui` lacks `dial_timeout` | code | `deploy/docker/Caddyfile.owui:14` |
| M12 | OWUI + Caddy never started in `ci.yml` `live-integration` job | code | `.github/workflows/ci.yml:279` |
| M13 | LiteLLM `model:` hardcoded — env override silently ignored | gemini | `deploy/litellm/config.yaml:86` **Verify against actual file** |
| M14 | `MaxAttempts` naming — DLQ entry after 7 attempts not 8 | go | `apps/control-plane/internal/auditworker/worker.go:233,246-255` |
| M15 | SERIALIZABLE writer retry storms under multi-replica | db | `supabase/migrations/20260516_04_phase19_audit_log.sql` (design) |

### LOW

| ID | Theme | Source(s) |
|----|-------|-----------|
| L1 | `marshalSorted` silently ignores `json.Marshal` errors | go |
| L2 | `19-01-SUMMARY.md` no deprecation marker for LibreChat → OWUI switch | code |
| L3 | OWUI + Caddy not in `CLAUDE.md` / `README.md` service tables | code |
| L4 | `litellm/config.yaml:125-128` orphaned `files_settings` block | code |
| L5 | `provider_capabilities` `ON CONFLICT DO NOTHING` on re-run skips capability update | db |
| L6 | `GRAFANA_ADMIN_PASSWORD=` empty default in `.env.example:149` | security |
| L7 | No offline unit test for audit/trace failure path on hot path | code |
| L8 | `dispatch_test.go` no SSE mid-stream abort coverage | gemini |
| L9 | Provider regex misses `google`, `gemini`, `mistral`, `cohere` | gemini |

## Reviewer agreement summary

| Finding | Go | DB | Sec | Code | Gemini |
|---------|----|----|-----|------|--------|
| Audit chain integrity holes (C2, C3) | ✓ | – | ✓ | – | ✓ |
| Partition cliff (C1) | – | ✓ | – | – | – |
| OWUI JWT path mismatch (C4) | – | – | ✓ | ✓ | ✓ |
| Sink lease TTL race (H1) | ✓ | – | – | – | ✓ |
| RLS gaps on audit tables (H3) | – | ✓ | ✓ | – | – |
| 3rd-party sink PII leak (H4) | – | – | ✓ | – | – |
| Caddy admin-strip incomplete (H7) | – | – | ✓ | – | ✓ |
| CHECK constraint widening risk (H6) | – | ✓ | – | – | ✓ |
| FX postfix regex gap (H11) | – | – | – | – | ✓ |
| Real Supabase URL in env (H9) | – | – | – | ✓ | – |

## Recommended remediation order (next branch)

1. **C4** — JWT path mismatch. Confirm via curl: send chat request through OWUI → verify edge-api logs show user JWT bound, not `owui-shim-key`. **Must verify before any production deploy.**
2. **C1** — Add DEFAULT partitions to `audit_log` + `llm_traces`. Cheap migration. Mandatory before 2026-07-01.
3. **C2 + C3** — Extract canonical serialization to a shared package. Format ts with explicit 6-digit micros (`2006-01-02T15:04:05.999999Z07:00`). Add cross-partition prev_hash bootstrap to verifier + writers. Unify `SyncWriter` and `insertAuditEvent` chain semantics. Add per-tenant filter to `SyncWriter` `MAX(seq)` query.
4. **H1 + H2** — Switch worker to concurrent fan-out per row with bounded goroutines; replace `LeaseTTL.String()` with `make_interval(secs => $1)` form. Add multi-worker contention test.
5. **H3 + H7 + H8 + H10** — Defence-in-depth: RLS on audit tables, Caddy regex broadening, hardcoded shim key + Supabase URL purge.
6. **H4 + M8** — Allowlist allowlist payload fields for Datadog/ELK/Splunk/Loki/Langfuse sinks. Mirror Sentry pattern.
7. **H6** — Update `20260518_02` migration to `NOT VALID` + separate `VALIDATE CONSTRAINT`. Verify no `unhealthy` rows in staging before re-run.
8. **H11, H12, M13** — Verify each (Gemini claims) against actual file state; treat as suspected-bugs pending evidence.
9. **M1 + M2 + M3 + M4** — Lifetime + canonical-package refactor pass.
10. **M12 + L8** — CI: bring up OWUI + Caddy in live-integration job; add SSE-abort + multi-worker tests.

## Verification still needed (flagged "verify" above)

- C4: OWUI → edge-api JWT propagation end-to-end (Gemini's claim is high-impact; needs live curl evidence)
- H2: `2m0s` cast acceptance by Postgres (smoke test)
- H11: FX postfix regex against actual OpenRouter 402 fixture
- H12: `auditWALDir` default path + container user/volume
- M13: LiteLLM hardcoded model claim (Gemini cites line 86)

## Notes

- Gemini cited `tenant_invites.sql:29` — file confirmed exists (`20260516_08_phase19_tenant_invites.sql`). Catch-22 RLS claim worth verifying against actual policy text.
- All 5 reviewers ran independently; no coordination. Overlaps strengthen confidence; non-overlaps deserve targeted verification.

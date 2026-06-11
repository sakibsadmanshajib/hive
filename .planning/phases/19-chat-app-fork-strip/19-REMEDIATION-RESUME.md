# Phase 19 Remediation — Resume State

> **RESOLVED.** PR #146 merged 2026-05-29 (squash merge, branch deleted).
> All CRITICAL/HIGH/MEDIUM items below are shipped. Remaining open items
> (M12, C4, L2, L3, L4, L5, L7, L8) are deferred per the notes in this file.
> This document is preserved as a historical record.

Paused: 2026-05-18 13:40 EDT.
Resolved: 2026-05-29 (PR #146 merged).

## Branch / PR
- Branch: `b/phase-19-remediation` (deleted after merge)
- PR: https://github.com/sakibsadmanshajib/hive/pull/146 — **MERGED 2026-05-29**
- Head commit: `7275eee` (r3 — H/M/L inline)
- Prior commits on branch: `40e8484` (r1 — CRITICAL C1-C4), `50f287c` (r2 — adversarial-review fixes)

## Completed in PR #146

### CRITICAL — all four addressed
- **C1** Default partitions on `audit_log` + `llm_traces`, explicit GRANT/REVOKE, row-move maintenance contract, UNIQUE(date_trunc('month',ts), seq) for `audit_log_default`.
- **C2** Shared `packages/audit-canonical` Go module; fixed-width 6-digit microsecond timestamp; legacy RFC3339Nano fallback in verifier.
- **C3** Cross-partition prev_hash lookup `ORDER BY ts DESC, seq DESC, id DESC`; verifier bootstraps `expectedPrev` from prior partitions; per-tenant chain.
- **C4** `OWUIUnwrap` edge-api middleware; JSON-only, 2 MiB body cap, 8 KiB token cap, strips entire `__metadata`, `OWUI_SHIM_KEY` env var.

### Adversarial-review follow-ups
- Advisory lock cast to `::bigint` (2038 overflow + namespace correctness).
- Bounded SERIALIZABLE retry loop on `pgerrcode.SerializationFailure`.
- GRANT/REVOKE on DEFAULT partitions.
- OWUI body Content-Type gate, full `__metadata` strip, JWT length cap, structured warn on missing metadata.

### HIGH / MEDIUM done inline
- **H1+H2** Bounded concurrent fan-out (sem=16) in `auditworker.drainOnce`; `LeaseTTL.String()` → `make_interval(secs => $1::numeric)`.
- **H3+M6+M7** New migration `20260518_04_phase19_audit_rls_and_indexes.sql` — RLS on audit tables, restored eligibility index, manifest GRANT/RLS.
- **H4+M8** Langfuse sink allowlist (only dimension/cost/id fields).
- **H7** Caddyfile.owui — broader admin/signup regex + method-level admin block + `dial_timeout`.
- **H11+L9** FX postfix `0.002 USD` matched; extended provider regex with google, gemini, vertex, mistral, cohere, cerebras, deepseek, xai, together, fireworks, replicate, perplexity.
- **M1+M2** `runCtx` instead of `context.Background()` for alertRunner, invoicesCron, BD-payments goroutine.
- **M5** `(SELECT auth.jwt())` SELECT-wrap on every phase-19 tenant RLS policy.
- **M9** `hive_jwt_forward.py` drops `id_token` fallback.
- **M10+M11** OWUI `mem_limit`/`cpus` + Caddy `dial_timeout`.

## Out of scope / Deferred (not in current PR)

- **H6** `20260518_02` migration already applied — split is no-op now; document only.
- **H9/H10** Need broader secrets/template audit beyond docker-compose.yml.
- **M4** Advisory-lock hot-path throughput — design decision, defer.
- **M12** CI `live-integration` job bringing up OWUI + Caddy.
- **M13** LiteLLM `model:` env-override (current config has hardcoded model strings).
- **M14** Reviewed: NOT a real bug — `nextAttempts >= MaxAttempts` triggers DLQ on attempt N where N=MaxAttempts.
- **L2** Deprecation marker in 19-01-SUMMARY.md.
- **L3** OWUI/Caddy entries in CLAUDE.md + README service tables.
- **L4** Verify `litellm/config.yaml:125-128` files_settings is or isn't orphaned.
- **L5** provider_capabilities `ON CONFLICT DO NOTHING` — schema decision.
- **L7** Offline unit test for hot-path audit failure.
- **L8** dispatch_test.go SSE mid-stream abort coverage.

## Current state at pause

- All local tests green (48 ok, vet clean).
- r3 pushed to remote.
- PR #146 merged 2026-05-29. CI passed, CodeRabbit + Codex reviews addressed.
- Monitor `bs239mrgj` was watching PR until pause — stopped.

## Resume protocol

1. `gh pr view 146 --json mergeable,mergeStateStatus,statusCheckRollup` — fetch CI state.
2. `gh pr view 146 --comments` — read CR/Codex comments. If rate-limited, fall back to local adversarial reviewers (security-reviewer / go-reviewer / database-reviewer agents + `codex:rescue`).
3. Address every unresolved comment with a code change OR an inline `--reply`.
4. If green + all comments resolved → merge with `gh pr merge 146 --squash --delete-branch`.
5. After merge, audit open GitHub issues (#106-#120) for ones closed by shipped phases and close with reference.

## User instructions captured

- Do NOT close PR without Codex + CodeRabbit reviews unless they are rate-limited.
- All H/M/L remediation goes in THIS PR — no follow-up issue split.
- Issue housekeeping is part of the job. Audit open issues, close stale/done.
- When mergeable + all checks pass + comments resolved → merge directly, don't wait for user approval.

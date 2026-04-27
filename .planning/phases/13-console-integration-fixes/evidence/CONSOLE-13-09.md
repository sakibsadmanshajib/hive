---
requirement_id: CONSOLE-13-09
status: Satisfied
verified_at: 2026-04-27
verified_by: Phase 13 executor
phase_satisfied: 13
evidence: 13-VERIFICATION.md
---

# CONSOLE-13-09 — `tsc --noEmit` + `npm run build` exit 0

## Truth

The web-console workspace passes `npx tsc --noEmit` AND `npm run build` AND `npm run test:unit` after Phase 13 changes.

## Evidence

| Check | Pre-fix | Post-fix |
|-------|---------|----------|
| `npx tsc --noEmit` | exit 0 | exit 0 |
| `npm run test:unit` | 8 files, 44 tests | 9 files, 45 tests (+ invoice-decode.test.ts) |
| `npm run build` | exit 0 | exit 0 |

`npm run build` was run with the project `.env` sourced (Supabase env vars required at SSR prerender). All 22 routes (`/`, `/auth/*`, `/console/*`, `/invitations/*`, `/api/*`) build successfully.

## Command

```bash
set -a && . .env && set +a
cd apps/web-console
npx tsc --noEmit             # exit 0
npm run test:unit            # 45 tests pass
npm run build                # exit 0; 22 routes built
```

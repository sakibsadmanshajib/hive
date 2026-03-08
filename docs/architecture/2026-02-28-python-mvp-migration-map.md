# Python MVP Migration Map (Archived -> TypeScript)

Date: 2026-02-28
Status: completed

This map records where legacy Python MVP responsibilities moved in the TypeScript monorepo.

| Legacy Python path | Responsibility | TypeScript replacement |
|---|---|---|
| `app/server.py` | HTTP app bootstrap and wiring | `apps/api/src/server.ts`, `apps/api/src/app.ts` |
| `app/api.py` | REST endpoints | `apps/api/src/routes/*` |
| `app/auth.py` | auth/session handling | `apps/api/src/routes/google-auth.ts`, `apps/api/src/routes/users.ts`, `apps/api/src/routes/two-factor.ts` |
| `app/ledger.py` | credits ledger domain logic | `apps/api/src/domain/credits-ledger.ts` |
| `app/refunds.py` | refund policy | `apps/api/src/domain/refund-policy.ts` |
| `app/routing.py` | provider routing/fallback | `apps/api/src/domain/routing-engine.ts`, `apps/api/src/providers/registry.ts` |
| `app/ratelimit.py` | request rate-limiter | `apps/api/src/runtime/redis-rate-limiter.ts` |
| `app/storage.py` | persistence adapter | `apps/api/src/runtime/postgres-store.ts` |
| `app/signatures.py` | webhook signature verification | `apps/api/src/domain/webhook-signatures.ts` |
| `app/payments.py` | payment intent/webhook flow | `apps/api/src/routes/payment-intents.ts`, `apps/api/src/routes/payment-webhook.ts` |
| `tests/*.py` | Python coverage | `apps/api/test/**/*.test.ts`, `apps/web/test/**/*.test.tsx` |

## Operator note

The root Python MVP source and root Python tests were removed to keep one canonical runtime/testing path.
Git history retains the removed files for forensic reference.

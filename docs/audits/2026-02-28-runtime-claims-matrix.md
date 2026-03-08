# Runtime Claims Matrix

**Date**: 2026-02-28

This matrix reconciles documentation (README.md, OpenAPI) with actual implemented routes in `apps/api/src/routes/`.

| Route | Method | Implemented | README | OpenAPI | Status / Notes |
|---|---|---|---|---|---|
| `/v1/auth/google/start` | GET | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/auth/google/callback` | GET | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/auth/logout` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/auth/session` | GET | **No** | Yes | No | **Drift**: Documented in README but no actual implementation exists |
| `/v1/models` | GET | Yes | Yes | Yes | Good |
| `/v1/chat/completions` | POST | Yes | Yes | Yes | Good |
| `/v1/images/generations` | POST | Yes | Yes | Yes | Good |
| `/v1/responses` | POST | Yes | Yes | Yes | Good |
| `/v1/credits/balance` | GET | Yes | Yes | Yes | Good |
| `/v1/usage` | GET | Yes | Yes | Yes | Good |
| `/v1/users/register` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/login` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/me` | GET | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/settings` | GET | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/settings` | PATCH | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/api-keys` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/users/api-keys` | DELETE | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/2fa/enroll/init` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/2fa/enroll/verify` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/2fa/challenge/init` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/2fa/challenge/verify` | POST | Yes | Yes | No | Implemented but missing from OpenAPI |
| `/v1/payments/intents` | POST | Yes | Yes | Yes | Good |
| `/v1/payments/webhook` | POST | Yes | Yes | Yes | Good |
| `/v1/payments/demo/confirm` | POST | Yes | Yes | No | Demo route, expected to be missing from OpenAPI |
| `/v1/providers/status` | GET | Yes | Yes | No | Missing from OpenAPI |
| `/v1/providers/status/internal` | GET | Yes | Yes | No | Internal route, potentially expected to not be in public OpenAPI |
| `/health` | GET | Yes | No | No | Internal health check |

## Summary
- **Implemented**: Most routes are correctly implemented and documented in the README.
- **Missing / Drift**: `GET /v1/auth/session` is documented but not implemented.
- **Drift**: `packages/openapi/openapi.yaml` is severely outdated/incomplete compared to the README and actual routes, especially concerning user authentication, 2FA, and user settings.
- **Duplicates**: Only `packages/openapi/openapi.yaml` exists, `openapi/openapi.yaml` duplicate doesn't exist anymore or isn't present.

## Actions to Take
- Update README.md to remove `GET /v1/auth/session` if it's dead, or implement it if missing. Plan says: "Align auth/2FA endpoint documentation with actual API shape... and remove/resolve undocumented nonexistent routes" in Step 3.
- Update OpenAPI to contain all routes if applicable, or accept subset.

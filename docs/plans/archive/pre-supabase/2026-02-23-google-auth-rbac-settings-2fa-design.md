# Google Auth + RBAC + User Settings + 2FA Design

## Goal
- Add Google login and permission-matrix RBAC while enforcing per-user feature flags for every capability, then introduce 2FA in a phased, production-safe rollout.

## Confirmed Decisions
- Approach: backend-managed Google OAuth flow with internal app sessions.
- RBAC model: permission matrix.
- Feature gating: every feature must be guarded by user settings.
- Setting gates apply to both session and API-key access.
- 2FA is required in roadmap and should be planned now.

## Scope
- In scope:
  - Google auth start/callback/logout routes.
  - Session token issuance/validation in API.
  - Permission matrix schema + middleware enforcement.
  - User settings gate layer on all protected features.
  - 2FA schema and enrollment/challenge APIs in phased rollout.
- Out of scope for first delivery:
  - External IdP beyond Google.
  - WebAuthn/passkeys enforcement in phase 1.

## Architecture

### 1) Authentication Layer
- Add routes:
  - `GET /v1/auth/google/start`
  - `GET /v1/auth/google/callback`
  - `POST /v1/auth/logout`
  - `GET /v1/auth/session` (optional session introspection for web UI)
- Google callback validates `iss`, `aud`, `exp`, and CSRF state/nonce.
- User account link behavior:
  - Match by normalized email.
  - Create user if no existing user.
- Issue internal session token after successful login.
- Store only hashed session token in DB.

### 2) Authorization Layer (Permission Matrix)
- New tables:
  - `permissions(code, description)`
  - `roles(name, description)`
  - `role_permissions(role_id, permission_id)`
  - `user_roles(user_id, role_id)`
- Unify permission checks for:
  - Session-authenticated users.
  - API-key callers.
- Backward compatibility bridge:
  - Existing API key scopes map to permissions during migration.

### 3) Mandatory User Settings Gate Layer
- Add `user_settings` table (or normalized key-value equivalent):
  - `user_id`, `key`, `value`, `updated_at`
- Settings are evaluated for every protected feature after auth + permission check.
- Effective authorization rule:
  - `authenticated` AND `permission_granted` AND `user_setting_enabled`
- Initial required keys:
  - `apiEnabled`
  - `generateImage`
  - `chatEnabled`
  - `billingEnabled`
  - `providerStatusInternalEnabled` (if applicable to user principals)
  - `twoFactorEnabled`

### 4) 2FA Plan
- Phase 1 (this feature set):
  - Schema fields for 2FA enrollment and recovery metadata.
  - Endpoints for enrollment init/verify and challenge verify.
  - Keep optional enforcement flag.
- Phase 2:
  - Enforce 2FA for sensitive operations:
    - API key create/revoke
    - role/permission changes
    - billing-sensitive actions

## Endpoint Permission + Setting Matrix
- `/v1/chat/completions`
  - permission: `chat:write`
  - setting: `chatEnabled=true`
- `/v1/images/generations`
  - permission: `image:write`
  - setting: `generateImage=true`
- `/v1/users/api-keys` (create/revoke)
  - permission: `api:keys:manage`
  - setting: `apiEnabled=true`
  - 2FA: required once phase-2 enforcement is enabled
- `/v1/usage`, `/v1/credits/balance`, `/v1/users/me`
  - permission: `usage:read`
  - setting: `chatEnabled` or `billingEnabled` depending on endpoint group
- `/v1/payments/intents`, `/v1/payments/demo/confirm`
  - permission: `billing:write`
  - setting: `billingEnabled=true`
- `/v1/providers/status/internal`
  - permission: `providers:status:internal:read`
  - setting: `providerStatusInternalEnabled=true`
  - preserve admin-token guard during migration window

## Data and Runtime Changes
- `apps/api/src/runtime/postgres-store.ts`
  - create new auth/RBAC/settings/2FA tables in schema bootstrap.
  - add CRUD helpers for sessions, roles, permissions, user settings, 2FA artifacts.
- `apps/api/src/runtime/services.ts`
  - add auth service for Google/session flow.
  - add authorization service to compute effective permissions.
  - add user settings service for gate checks.
- `apps/api/src/routes/auth.ts`
  - replace scope-only key check with principal resolution + permission + setting checks.
- `apps/api/src/routes/users.ts` and protected routes
  - migrate to declarative permission + setting requirements.

## Security Requirements
- No tokens/secrets in logs.
- Session token hashing at rest.
- Strict redirect URI validation.
- CSRF `state` verification in OAuth callback.
- Separate `401` vs `403` semantics.

## Testing Strategy
- Add API tests for:
  - Google auth start/callback failures and success.
  - Session issuance/invalid/expired behavior.
  - RBAC permission allow/deny.
  - User settings gating allow/deny for each key endpoint.
  - API key backward compatibility mapping.
  - 2FA enrollment/challenge flow contract in phase 1.
- Verify existing behavior remains stable:
  - billing ledger correctness.
  - provider status sanitization/token protection.

## Rollout Plan
- Phase A: schema + services + feature flags disabled by default.
- Phase B: enable Google auth + RBAC checks with scope bridge.
- Phase C: enable mandatory user settings checks for all endpoints.
- Phase D: activate 2FA enforcement for sensitive operations.

## Docs Updates Required
- `README.md`: new auth env vars and login flows.
- `docs/README.md`: plan references.
- Runbook doc for seeding permissions, settings defaults, and incident troubleshooting.

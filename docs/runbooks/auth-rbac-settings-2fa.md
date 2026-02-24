# Auth, RBAC, User Settings, and 2FA Runbook

## Purpose

This runbook explains how to operate and troubleshoot:
- Google OAuth login
- permission-matrix RBAC
- per-user feature setting gates
- phase-1 two-factor authentication

## New Environment Variables

- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`
- `GOOGLE_REDIRECT_URI`
- `AUTH_SESSION_TTL_MINUTES`
- `ENFORCE_2FA_FOR_SENSITIVE_ACTIONS`

## Startup Checklist

1. Ensure Postgres and Redis are reachable.
2. Set OAuth env vars with correct Google app credentials.
3. Verify redirect URI exactly matches Google app config.
4. Start stack and check `/health`.
5. Validate auth endpoints and protected route behavior.

## Permission and Setting Model

Access is granted only if all three pass:

1. Authenticated principal exists (session bearer token or `x-api-key`)
2. Principal has required permission
3. Corresponding user setting is enabled

Examples:

- `chat:write` + `chatEnabled=true` for chat
- `image:write` + `generateImage=true` for images
- `api:keys:manage` + `apiEnabled=true` for API key management

## 401 vs 403 Semantics

- `401`: missing/invalid credentials
- `403`: authenticated but blocked by permission, setting, or 2FA policy

## 2FA Operations

Phase-1 provides enrollment/challenge endpoints.

- Enrollment start/verify flow prepares user 2FA state.
- Challenge start/verify flow can be used before sensitive actions.
- Enforcement for sensitive actions is controlled by:
  - `ENFORCE_2FA_FOR_SENSITIVE_ACTIONS`

## Troubleshooting

### Google callback fails

- Check `GOOGLE_REDIRECT_URI` exact match.
- Check client ID/secret and audience config.
- Verify callback query contains state/code.

### User gets 403 unexpectedly

- Confirm user has required permission via role mappings.
- Confirm required user setting is enabled.
- If sensitive action: confirm recent successful 2FA challenge.

### API key stopped working

- Check key revoked state.
- Check permission bridge mapping from legacy scopes.
- Confirm `apiEnabled=true` for that user.

## Operational Safety Notes

- Keep `/v1/providers/status` sanitized.
- Keep `/v1/providers/status/internal` protected.
- Never log OAuth secrets or session tokens.
- Rotate credentials if leakage is suspected.

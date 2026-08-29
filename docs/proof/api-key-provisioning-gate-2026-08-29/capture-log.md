# Visual proof: API key creation refuses an unprovisioned workspace (issue #1330)

Date: 2026-08-29
Branch: fix/1330-account-not-provisioned
Surface: `/console/api-keys`, the Create API key form

## Substrate

Captured against a stack built from this branch, standing up the same shape
`ci.yml`'s `web-e2e` job uses. Nothing here is a mock of the code under test.

| Component | What ran |
|---|---|
| Postgres | throwaway `pgvector/pgvector:pg17`, all 112 repository migrations applied by `scripts/ci-throwaway-db.sh` |
| GoTrue, PostgREST, gateway | `scripts/ci-supabase-stack.sh --port 9010`, so sign-in is a real Supabase session and the `custom_access_token_hook` runs in the same database |
| control-plane | built from this branch, `http://localhost:18081` |
| web-console | this branch's source, `http://localhost:3000`, reaching control-plane over `CONTROL_PLANE_BASE_URL` |

The demo box was not used and no live account, key, tenant or password was
touched. Every credential in this run was generated for the run and died with
the containers. No URL below carries a credential of any kind, so nothing is
redacted; the one secret that did render on screen (the positive control's
copy-it-now panel) is masked in the image before the file was written, by
Playwright's `mask:` option, not after.

## State reproduced

The fixture seeder creates the standard verified-owner workspace, which is
correctly mapped. The capture then deletes that one row from
`public.tenant_billing_accounts`, which is the exact live shape the issue
reports (`e2e-verified-owner-s-workspace`, `business`, zero links). The account,
its owner membership and its profile are left untouched.

```
tenant_billing_accounts links for that account: 0
```

## Capture 1: refusal (`refusal.png`)

URL: `http://localhost:3000/console/api-keys`

Signed in as the seeded verified owner, nickname `demo-walkthrough-key`,
Create key pressed.

Observed:

```
ALERT: This workspace is not connected to billing yet, so a key created here
would be rejected by the API. If you have more than one workspace, switch to
the one that carries your billing and create the key there. Otherwise reload
this page to finish workspace setup, then try again.
copy-it-now secret panels rendered: 0
api_keys rows for that account after the attempt: 0
```

The screenshot shows the refusal under the form, the form still filled in and
usable, and the key table still reading "No API keys yet". Before this change
the same click returned 201, rendered the secret, listed the key as active, and
the customer found out only when the gateway answered
`403 account_not_provisioned`.

Browser console during the capture: React DevTools notice, HMR connect, Fast
Refresh rebuild. No errors, no warnings.

## Capture 2: positive control (`positive-control.png`)

URL: `http://localhost:3000/console/api-keys`

The deleted `tenant_billing_accounts` row is re-inserted verbatim and the same
form is used again, because a gate that refused every workspace would have
passed capture 1 just as well.

```
secret panel rendered on the mapped account: 1
api_keys rows after the mapped attempt: 1
```

The screenshot shows "Key created, copy it now" with the secret masked. The key
row was really written, so the refusal is scoped to the unprovisioned state and
does not block ordinary key creation.

## Backing automated checks

- `go test ./apps/control-plane/... -count=1 -short` green, including four new
  tests in `apps/control-plane/internal/apikeys/provisioning_gate_test.go`.
- Mutation check: with the guard's condition forced false, all four go red
  (create returns nil, rotate returns nil, both HTTP cases return 201/200 with
  a secret in the body); restored, all four go green.
- `npm run test:unit` in the console: 801 tests pass, including two new cases on
  the create form (the 409 message is rendered verbatim; an unreadable 409 body
  still produces a message).
- `npm run build` for the console passes.

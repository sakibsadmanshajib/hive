# Live verification, membership-less user reaches the no-workspace state

Captured 2026-07-27 against the merge of this branch with `main` at `2cb6d3b8`
(the commit that merged PR #484), so the run also covers the interaction with
PR #482's error boundaries and PR #481's origin helper.

## Stack

`control-plane` and `web-console` built from this branch and run through
`deploy/docker/docker-compose.yml` against the development Supabase project, on
non-default host ports so the run could not collide with another stack.

```
control-plane  http://localhost:39481/health -> {"status":"ok"}
web-console    http://localhost:39400
```

The migration this branch ships,
`supabase/migrations/20260727_01_token_hook_membershipless_no_raise.sql`, was
applied to the development project first. Before it, `custom_access_token_hook`
still contained the `no_active_membership` raise.

## The user

Created through the Supabase admin API with the email pre-confirmed, on a
domain no tenant claims. Zero rows in `public.tenant_users`:

```
select count(*) from public.tenant_users where user_id = '<user id>';  -- 0
```

## 1. The token hook no longer refuses to issue

A password grant for that user, which previously returned HTTP 500
`{"code":"P0001","message":"no_active_membership"}`:

```
POST /auth/v1/token?grant_type=password  -> HTTP 200, token issued
  tenant_id claim present: false
  tenants claim present:   false
  owui_role claim present: false
  role claim:              authenticated
```

Absent rather than null, and `role` left at GoTrue's own value, which is what
the RLS policies and the edge-api middleware fail closed on.

## 2. Signing in lands on the designed no-workspace state

Sign-in through the console form at `/auth/sign-in` redirected to
`/no-workspace` and rendered `NoWorkspaceState`: the "No workspace yet" card
naming the signed-in address, with "Check again" pointing at
`/console/provision` and a working "Sign out".

See `01-membershipless-signin-lands-on-no-workspace.png`.

This is the point the merge had to settle. PR #482 added error boundaries at
`apps/web-console/app/error.tsx` and `apps/web-console/app/console/error.tsx`,
and a tenant-less viewer must reach the designed state rather than a boundary.
The card in the capture is the designed state, not a boundary: a boundary
renders only on a thrown error, and neither the layout nor
`reconcileTenantMembership` throws on this path.

## 3. Entering at /console reaches the same place

Navigating directly to `/console` while signed in as the same user:

```
/console -> /console/provision -> /no-workspace
```

The console layout reads the absent tenant claim and hands off to the
provisioning route, which settles the membership question and redirects. Same
rendered card, no error boundary anywhere in the sequence.

Unauthenticated access still behaves:

```
GET /console -> 307 http://localhost:39400/auth/sign-in
```

The redirect target is the request host rather than the `0.0.0.0:3000` bind
address, which is PR #481's fix holding in this build.

## Note on the development project

`public.tenant_invites` and `public.tenant_email_domains` do not exist on the
development database, though both are defined in `supabase/migrations`
(`20260516_08` and `20260516_09`). The development project is behind on
migrations. `signup.Resolver` reads those two tables, so on this project the
provisioning call cannot resolve a match for anybody, and
`reconcileTenantMembership` maps that failure to `no_tenant`. The user-facing
outcome captured above is the same either way, and the resilience is
deliberate, but a deployment with those tables present would exercise the
resolver's own no-match branch rather than its failure branch.

The test user was deleted after capture.

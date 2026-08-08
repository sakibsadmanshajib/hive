# Issue #758 visual proof: workspace-scoped admin areas

Captured 2026-08-06 against a stack built from this branch: control-plane and
web-console running from the branch source (compose project `hive758`, isolated
from the shared `hive` project and its `:ci` image tags), pointed at the live
Supabase project.

## The account in the screenshots

A purpose-made account with the same role shape as the demo account:

- `public.tenant_users.role = OWNER`, status ACTIVE, on the workspace in scope
- `public.account_memberships.role = owner` on the matching billing account
- `public.accounts.is_platform_admin = false`

Confirmed by direct query before the capture. No account was granted
`is_platform_admin` for this proof, and the flag stripped from the demo accounts
earlier the same day was not restored. The login email is a synthetic
`example.com` address (masked as `hive-owner-758@exam...` where the console
truncates it); its password was rotated to an unheld random value after the
capture. No URL in these flows carries a credential.

## What the captures show

`feature-gates-owner.png`

- The ADMIN nav group is reachable, and `/console/feature-gates` renders the
  gate list rather than the old "Admin access required" wall.
- 15 live toggles: the capability, RAG, SSO and audit-sink gates that belong to
  the workspace.
- 10 rows read as "Managed by your administrator": every gate in the billing and
  admin categories (plan entitlements such as ENABLE_EXTRA_USAGE, and
  ENABLE_PROVIDER_CUSTOM, the custom provider endpoint switch). Those stay
  platform-admin only, so the row is shown without a control that would be
  refused.

`marketplace-owner.png`

- `/console/marketplace` renders for the same owner, no wall, and no curation
  form. With an empty catalog it states that nothing has been published yet.

`marketplace-owner-enabled.png`

- One catalog entry present, enabled by this owner through the console. The
  enable switch is live; the curate form and the per-row delete control are
  absent, because the catalog itself is one global table shared by every tenant
  and curating it stays a platform operation.
- The enablement write was confirmed in the database: exactly one
  `marketplace_tenant_entries` row, on this owner tenant, attributed to this
  owner user.
- The catalog entry and its enablement row were deleted after the capture, so
  the shared catalog is back to empty.

`capture-log.txt` is the raw assertion output from the capture run.

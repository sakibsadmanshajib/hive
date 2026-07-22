#!/usr/bin/env python3
"""Idempotently provision the cross-surface Hive demo account.

Solves the "3 auth systems, 0 shared admin account" gap for live demos: one
Supabase GoTrue user that is simultaneously an OWNER of a real (non-e2e)
tenant (unlocks agent-console's Cowork task console, gated on ENABLE_COWORK
via apps/agent-console/lib/edge-api/gate.ts) and an owner + platform-admin of
a web-console personal account (unlocks the owner-only billing pages AND the
platform-admin-only panels -- feature gates, provider catalog, marketplace,
credit grants -- all wrapped in apps/control-plane/internal/platform.
RoleService.RequirePlatformAdmin, see internal/platform/http/router.go).

Two independent role systems get written here, on purpose, same account:
  - tenant_users.role = 'OWNER'   (Phase 19 tenant scope; uppercase enum)
  - account_memberships.role = 'owner' + accounts.is_platform_admin = true
    (Phase 2 billing-account scope; lowercase enum, separate schema)
web-console reads the second system (lib/control-plane/client.ts getViewer);
agent-console's tenant gate and the custom_access_token_hook JWT claims read
the first. Neither table know about the other -- see
apps/control-plane/internal/platform/role.go's Phase 14/18 module comment.

OWUI is NOT covered by this script. docker-compose.yml wires OWUI's OIDC
("Sign in with Hive") against SUPABASE_URL, but SUPABASE_OAUTH_CLIENT_ID /
SUPABASE_OAUTH_CLIENT_SECRET are unset in .env, so the button is inert on
this stack, and open-webui's OAUTH_ALLOWED_ROLES ("ADMIN,MEMBER,VIEWER")
does not even include the OWNER role this script grants -- OWNER would fail
that allow-list if OIDC were wired. OWUI identity stays separate; use the
existing local OWUI test account (asdas@asdas.sda / asdas) for the chat
surface demo.

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY

Prints exactly two lines to stdout (and nothing else):
  EMAIL=<email>
  PASSWORD=<password>
Everything else (progress, ids, errors) goes to stderr.
"""
import json
import os
import secrets
import string
import sys
import urllib.error
import urllib.parse
import urllib.request

TENANT_SLUG = "hive-demo"
TENANT_NAME = "Hive Demo"
TENANT_DEPLOYMENT = "HIVE_CLOUD"
USER_EMAIL = "demo@hive-demo.invalid"  # .invalid: IANA-reserved TLD (RFC 2606)
TENANT_ROLE = "OWNER"
TENANT_STATUS = "ACTIVE"

ACCOUNT_SLUG = "hive-demo-owner"
ACCOUNT_NAME = "Hive Demo"

# Demo-relevant tenant_settings feature gates. Excludes payment-rail toggles
# (bkash/sslcommerz/stripe), audit sinks, and SSO -- none of those are on the
# demo path and leaving them off keeps the tenant's gate surface legible.
FEATURE_GATES = [
    "ENABLE_ADMIN_CONSOLE",
    "ENABLE_PROVIDER_CUSTOM",
    "ENABLE_RAG",
    "ENABLE_RAG_PERSONAL",
    "ENABLE_RAG_SHARED_KB",
    "ENABLE_VOICE",
    "ENABLE_RELAY",
    "ENABLE_COWORK",
]


def env(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        print(f"error: {name} is not set", file=sys.stderr)
        sys.exit(1)
    return value


def request(base, headers, method, path, body=None, params=None, prefer=None):
    url = base + path
    if params:
        url += "?" + urllib.parse.urlencode(params)
    data = json.dumps(body).encode() if body is not None else None
    req_headers = dict(headers)
    if prefer:
        req_headers["Prefer"] = prefer
    req = urllib.request.Request(url, data=data, method=method, headers=req_headers)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            print(f"error: {method} {path} -> {e.code}: {raw[:300]!r}", file=sys.stderr)
            sys.exit(1)


def random_password() -> str:
    # Prefix guarantees upper/lower/digit/symbol classes regardless of the
    # random draw; total length (28) clears any realistic GoTrue min-length
    # policy with room to spare, well under bcrypt's 72-byte limit.
    alphabet = string.ascii_letters + string.digits + "!@#$%^&*-_"
    return "Aa1!" + "".join(secrets.choice(alphabet) for _ in range(24))


def find_by_slug(rest, headers, table, slug):
    """Returns the row for slug in table, or None. Used by the tenant/account
    collision guards below -- both tables are upserted by slug with the
    service-role key (bypasses RLS), so a caller must know what it is about
    to merge onto before doing so."""
    status, body = request(
        rest, headers, "GET", f"/{table}",
        params={"select": "*", "slug": f"eq.{slug}"},
    )
    if status != 200:
        print(f"error: {table} slug lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return body[0] if body else None


def guard_tenant_slug(existing_tenant, foreign_members):
    """Exits loud if existing_tenant (found by TENANT_SLUG) has members other
    than our own demo user -- see the on_conflict=slug upsert comment below
    for why merging onto it unchecked would be a privilege-escalation bug."""
    if existing_tenant is not None and foreign_members:
        print(
            f"error: tenant slug {TENANT_SLUG!r} already belongs to tenant "
            f"{existing_tenant['id']} with {len(foreign_members)} member(s) that are not "
            "the demo user -- refusing to merge onto a tenant this script did not create. "
            "Pick a different TENANT_SLUG for the demo.",
            file=sys.stderr,
        )
        sys.exit(1)


def guard_account_slug(existing_account, foreign_owners):
    """Exits loud if existing_account (found by ACCOUNT_SLUG) has an
    account_memberships row with role='owner' belonging to someone other
    than our demo user. control-plane's IsPlatformAdmin authorizes ANY
    owner-role membership on an is_platform_admin account (see
    apps/control-plane/internal/platform/role_pgx.go), not just
    accounts.owner_user_id -- so that single column is not a sufficient
    collision check. Merging unchecked here would silently grant every
    such co-owner platform-admin too."""
    if existing_account is not None and foreign_owners:
        print(
            f"error: account slug {ACCOUNT_SLUG!r} already belongs to account "
            f"{existing_account['id']} with {len(foreign_owners)} owner-role member(s) "
            "that are not the demo user -- refusing to merge (would silently grant them "
            "is_platform_admin too). Pick a different ACCOUNT_SLUG for the demo.",
            file=sys.stderr,
        )
        sys.exit(1)


def main() -> None:
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    headers = {
        "Authorization": f"Bearer {service_key}",
        "apikey": service_key,
        "Content-Type": "application/json",
    }
    rest = supabase_url + "/rest/v1"
    gotrue = supabase_url + "/auth/v1"

    # 1. Find-or-create the GoTrue user first -- the tenant guard below needs
    # user_id to tell "our own demo user" apart from an unrelated tenant's
    # real members. user_metadata.selected_tenant_id is set once the tenant
    # is resolved, further down. `filter=<email>` does an exact server-side
    # match (see scripts/seed-owui-e2e-user.py for the `email=` param 500
    # gotcha on this GoTrue version).
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": USER_EMAIL})
    if status != 200:
        print(f"error: user lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing_user = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == USER_EMAIL.lower()),
        None,
    )

    password = random_password()
    if existing_user is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={"email": USER_EMAIL, "password": password, "email_confirm": True},
        )
        if status not in (200, 201):
            print(f"error: user create failed: {status} {body}", file=sys.stderr)
            sys.exit(1)
        user_id = body["id"]
    else:
        user_id = existing_user["id"]
        # Password rotates every run so no run reuses a prior credential.
        status, body = request(
            gotrue, headers, "PUT", f"/admin/users/{user_id}", body={"password": password},
        )
        if status != 200:
            print(f"error: user update failed: {status} {body}", file=sys.stderr)
            sys.exit(1)
    print(f"user_id={user_id}", file=sys.stderr)

    # 2. Guard + upsert the demo tenant. slug is user-chosen; an
    # on_conflict=slug upsert with the service-role key would otherwise
    # silently merge onto ANY pre-existing tenant with this slug -- adding
    # our demo user as OWNER and flipping feature gates on for it, even if
    # that tenant belongs to a real customer. Guard: if the slug already
    # resolves to a tenant with members other than our own demo user, fail
    # loud instead of touching it. A tenant with zero members, or whose only
    # member is our own demo user (a prior run of this exact script), is
    # safe to reuse. archived_at is force-reset to NULL on every run: this
    # is a dedicated demo tenant this script owns outright, so reactivating
    # it (rather than leaving a demo login unable to get a tenant claim) is
    # the correct default -- see custom_access_token_hook's
    # `t.archived_at IS NULL` filter.
    existing_tenant = find_by_slug(rest, headers, "tenants", TENANT_SLUG)
    if existing_tenant is not None:
        status, members = request(
            rest, headers, "GET", "/tenant_users",
            params={"select": "user_id", "tenant_id": f"eq.{existing_tenant['id']}"},
        )
        if status != 200:
            print(f"error: tenant membership lookup failed: {status} {members}", file=sys.stderr)
            sys.exit(1)
        foreign_members = [m for m in members if m["user_id"] != user_id]
        guard_tenant_slug(existing_tenant, foreign_members)
        if existing_tenant.get("archived_at"):
            print(
                f"tenant {existing_tenant['id']} was archived_at={existing_tenant['archived_at']}; "
                "reactivating (dedicated demo tenant, safe to un-archive).",
                file=sys.stderr,
            )

    status, body = request(
        rest, headers, "POST", "/tenants",
        body={
            "slug": TENANT_SLUG,
            "name": TENANT_NAME,
            "deployment": TENANT_DEPLOYMENT,
            "archived_at": None,
        },
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    tenant_id = body[0]["id"]
    print(f"tenant_id={tenant_id}", file=sys.stderr)

    # Now that the tenant is resolved, point the user's selected tenant at
    # it. GoTrue admin updateUserById MERGES user_metadata, so this only
    # ever adds/refreshes selected_tenant_id.
    status, body = request(
        gotrue, headers, "PUT", f"/admin/users/{user_id}",
        body={"user_metadata": {"selected_tenant_id": tenant_id}},
    )
    if status != 200:
        print(f"error: user metadata update failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 3. Upsert tenant membership: OWNER unlocks agent-console/edge-api's
    # tenant-admin JWT-role claim (custom_access_token_hook) and is the top
    # role in the tenant_users CHECK constraint.
    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": TENANT_ROLE,
            "status": TENANT_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: tenant membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 4. Guard + upsert the web-console billing account. is_platform_admin=
    # true here is what unlocks control-plane's RequirePlatformAdmin-gated
    # admin panels (feature gates, provider catalog, marketplace, credit
    # grants) -- tenant OWNER above does not imply this; they are unrelated
    # schemas. Same slug-collision risk as the tenant guard above: refuse to
    # merge onto an existing account that already has a different owner-role
    # member, since that would silently grant them platform-admin too.
    existing_account = find_by_slug(rest, headers, "accounts", ACCOUNT_SLUG)
    if existing_account is not None:
        status, owners = request(
            rest, headers, "GET", "/account_memberships",
            params={
                "select": "user_id",
                "account_id": f"eq.{existing_account['id']}",
                "role": "eq.owner",
            },
        )
        if status != 200:
            print(f"error: account membership lookup failed: {status} {owners}", file=sys.stderr)
            sys.exit(1)
        foreign_owners = [m for m in owners if m["user_id"] != user_id]
        guard_account_slug(existing_account, foreign_owners)

    status, body = request(
        rest, headers, "POST", "/accounts",
        body={
            "slug": ACCOUNT_SLUG,
            "display_name": ACCOUNT_NAME,
            "account_type": "business",
            "owner_user_id": user_id,
            "is_platform_admin": True,
        },
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: account upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    account_id = body[0]["id"]
    print(f"account_id={account_id}", file=sys.stderr)

    # 5. Upsert account_memberships (web-console's own owner-only page gate,
    # e.g. app/console/billing/budget/page.tsx's `role === "owner"` check).
    status, body = request(
        rest, headers, "POST", "/account_memberships",
        body={
            "account_id": account_id,
            "user_id": user_id,
            "role": "owner",
            "status": "active",
        },
        params={"on_conflict": "account_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: account membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 6. Upsert account_profiles with profile_setup_complete=true so the
    # console's onboarding nudge does not stand between login and the demo.
    status, body = request(
        rest, headers, "POST", "/account_profiles",
        body={
            "account_id": account_id,
            "owner_name": ACCOUNT_NAME,
            "login_email": USER_EMAIL,
            "profile_setup_complete": True,
        },
        params={"on_conflict": "account_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: account profile upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    # 7. Enable the demo-relevant tenant feature gates.
    for key in FEATURE_GATES:
        status, body = request(
            rest, headers, "POST", "/tenant_settings",
            body={
                "tenant_id": tenant_id,
                "key": key,
                "enabled": True,
                "updated_by": user_id,
            },
            params={"on_conflict": "tenant_id,key"},
            prefer="resolution=merge-duplicates",
        )
        if status not in (200, 201, 204):
            print(f"error: feature gate {key} upsert failed: {status} {body}", file=sys.stderr)
            sys.exit(1)

    print(f"EMAIL={USER_EMAIL}")
    print(f"PASSWORD={password}")


if __name__ == "__main__":
    main()

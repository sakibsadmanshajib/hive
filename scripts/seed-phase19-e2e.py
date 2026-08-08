#!/usr/bin/env python3
"""Self-provision the identities the phase-19 API specs assert against.

Issue #659. The phase-19 Playwright specs under apps/web-console/e2e/phase-19
are the ones that prove a tenant cannot read another tenant's data. They had
never executed: every one of them skips itself when E2E_TENANT_B_ID,
E2E_USER_A_SECOND_TENANT_ID, E2E_ORPHAN_JWT or E2E_EXPIRED_JWT is unset, and
none of those four names appeared in any workflow file. A skipped test and a
passing test are indistinguishable inside a green check, so the isolation
guarantee had no coverage at all.

Handing those values to a human to configure as repository secrets is how they
went dark the first time, so this script mints them inside the job instead:

  * tenant A, plus a second tenant A2 that user A also belongs to (the
    tenant-switch spec needs a tenant the switch is allowed to reach),
  * tenant B, which user A is deliberately NOT a member of, so the
    cross-tenant read and the cross-tenant switch have a real target,
  * user A, an ACTIVE member of A and A2 only,
  * an orphan user with no tenant_users row at all,
  * a live Supabase access token for each of those two users.

Everything is idempotent: tenants upsert on slug, memberships upsert on
(tenant_id, user_id), and both users are found-or-created with their password
rotated on every run. Rerunning is the normal case, not a repair path.

Deliberately mirrors scripts/seed-owui-e2e-user.py: same GoTrue admin and
PostgREST call shapes, same "filter=<email>" user lookup (the `email=` param
is not supported by this GoTrue version), same Prefer merge-duplicates upsert.
That script is proven against the live project nightly; copying its call
shapes is cheaper than rediscovering them.

Requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY (admin writes) and
SUPABASE_ANON_KEY (the password grant that mints the two tokens).

Prints KEY=value lines on stdout for the caller to push into GITHUB_ENV:

    TENANT_A_ID=...
    TENANT_A2_ID=...
    TENANT_B_ID=...
    USER_A_EMAIL=...
    USER_A_PASSWORD=...
    USER_A_JWT=...
    ORPHAN_JWT=...

Note on E2E_EXPIRED_JWT: this script cannot mint it and neither can any other
CI step. edge-api validates bearer tokens against the project's JWKS
(apps/edge-api/internal/auth/jwt_supabase.go) and refuses a non-https JWKS URL
outright (apps/edge-api/cmd/server/main.go, loadJWTAuthEnv), so a token this
job signs itself can never be accepted, and only Supabase holds the key that
could sign one. Waiting one token lifetime out is not an option either: the
project's access tokens outlive the whole job. See the comment on the expiry
spec for what that leaves.
"""
import json
import os
import secrets
import string
import sys
import urllib.error
import urllib.parse
import urllib.request

TENANT_A_SLUG = "phase19-e2e-a"
TENANT_A2_SLUG = "phase19-e2e-a2"
TENANT_B_SLUG = "phase19-e2e-b"
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"

# .invalid is reserved by RFC 2606 and can never resolve, so these accounts
# can never receive mail even if a future migration starts sending it.
USER_A_EMAIL = "phase19-e2e-user-a@hive-e2e.invalid"
ORPHAN_EMAIL = "phase19-e2e-orphan@hive-e2e.invalid"

USER_A_ROLE = "OWNER"
MEMBER_STATUS = "ACTIVE"


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


def upsert_tenant(rest, headers, slug: str, name: str) -> str:
    status, body = request(
        rest, headers, "POST", "/tenants",
        body={"slug": slug, "name": name, "deployment": TENANT_DEPLOYMENT},
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed for {slug}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return body[0]["id"]


def find_or_create_user(gotrue, headers, email: str, user_metadata: dict) -> tuple[str, str]:
    """Find-or-create a GoTrue user and rotate its password.

    Returns (user_id, password)."""
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": email})
    if status != 200:
        print(f"error: user lookup failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == email.lower()),
        None,
    )

    password = random_password()
    if existing is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={
                "email": email,
                "password": password,
                "email_confirm": True,
                "user_metadata": user_metadata,
            },
        )
        if status not in (200, 201):
            print(f"error: user create failed for {email}: {status} {body}", file=sys.stderr)
            sys.exit(1)
        return body["id"], password

    user_id = existing["id"]
    status, body = request(
        gotrue, headers, "PUT", f"/admin/users/{user_id}",
        body={"password": password, "user_metadata": user_metadata},
    )
    if status != 200:
        print(f"error: user update failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return user_id, password


def upsert_membership(rest, headers, tenant_id: str, user_id: str, role: str) -> None:
    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": role,
            "status": MEMBER_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)


def strip_memberships(rest, headers, user_id: str) -> None:
    """Make the orphan user genuinely tenant-less, and prove it.

    A previous run, a personal-tenant provisioning trigger, or the signup
    webhook can all leave a membership behind, and an orphan that quietly has
    a tenant turns the NO_TENANT assertion into a test that cannot fail. Delete
    first, then read back and refuse to continue if anything survived.
    """
    status, body = request(
        rest, headers, "DELETE", "/tenant_users", params={"user_id": f"eq.{user_id}"},
    )
    if status not in (200, 204):
        print(f"error: orphan membership delete failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    status, body = request(
        rest, headers, "GET", "/tenant_users",
        params={"user_id": f"eq.{user_id}", "select": "tenant_id"},
    )
    if status != 200:
        print(f"error: orphan membership readback failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    if body:
        print(
            f"error: orphan user still has {len(body)} membership row(s) after delete; "
            "the NO_TENANT spec would assert nothing",
            file=sys.stderr,
        )
        sys.exit(1)


def mint_token(gotrue, anon_key: str, email: str, password: str) -> str:
    """Exchange the password for a live Supabase access token.

    Signed by the project, so edge-api's JWKS validation accepts it. Uses the
    anon key rather than the service-role key on purpose: this is the same
    grant a real browser session uses, so the token carries the same claims
    the production path would see.
    """
    headers = {"apikey": anon_key, "Content-Type": "application/json"}
    status, body = request(
        gotrue, headers, "POST", "/token",
        body={"email": email, "password": password},
        params={"grant_type": "password"},
    )
    if status != 200 or not body or not body.get("access_token"):
        print(f"error: password grant failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    return body["access_token"]


def main() -> None:
    supabase_url = env("SUPABASE_URL").rstrip("/")
    service_key = env("SUPABASE_SERVICE_ROLE_KEY")
    anon_key = env("SUPABASE_ANON_KEY")
    headers = {
        "Authorization": f"Bearer {service_key}",
        "apikey": service_key,
        "Content-Type": "application/json",
    }
    rest = supabase_url + "/rest/v1"
    gotrue = supabase_url + "/auth/v1"

    tenant_a = upsert_tenant(rest, headers, TENANT_A_SLUG, "Phase 19 E2E A")
    tenant_a2 = upsert_tenant(rest, headers, TENANT_A2_SLUG, "Phase 19 E2E A2")
    tenant_b = upsert_tenant(rest, headers, TENANT_B_SLUG, "Phase 19 E2E B")

    # selected_tenant_id pins the session to tenant A, so the cross-tenant
    # specs start from a tenant that is genuinely not B.
    user_a_id, user_a_password = find_or_create_user(
        gotrue, headers, USER_A_EMAIL, {"selected_tenant_id": tenant_a},
    )
    upsert_membership(rest, headers, tenant_a, user_a_id, USER_A_ROLE)
    upsert_membership(rest, headers, tenant_a2, user_a_id, USER_A_ROLE)

    orphan_id, orphan_password = find_or_create_user(gotrue, headers, ORPHAN_EMAIL, {})
    strip_memberships(rest, headers, orphan_id)

    user_a_jwt = mint_token(gotrue, anon_key, USER_A_EMAIL, user_a_password)
    orphan_jwt = mint_token(gotrue, anon_key, ORPHAN_EMAIL, orphan_password)

    print(f"TENANT_A_ID={tenant_a}")
    print(f"TENANT_A2_ID={tenant_a2}")
    print(f"TENANT_B_ID={tenant_b}")
    print(f"USER_A_EMAIL={USER_A_EMAIL}")
    print(f"USER_A_PASSWORD={user_a_password}")
    print(f"USER_A_JWT={user_a_jwt}")
    print(f"ORPHAN_JWT={orphan_jwt}")


if __name__ == "__main__":
    main()

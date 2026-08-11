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
(tenant_id, user_id), and both users are created if absent and otherwise left
alone. Rerunning is the normal case, not a repair path.

NO PASSWORD IS EVER WRITTEN. Sessions come from the admin one-time-token flow
(POST /auth/v1/admin/generate_link, then POST /auth/v1/verify), the same flow
apps/web-console/tests/e2e/support/live-auth.mjs uses and the only sanctioned
one. Setting, resetting or rotating a shared account's password to obtain a
session is forbidden outright: the control-plane resolves every bearer against
GoTrue per request, so a rotation invalidates every concurrent run, and doing
it broke three agents at once on 2026-08-08. See docs/live-test-auth.md. There
is no fallback path here that writes a password and there must never be one.

Requires SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY (admin calls) and
SUPABASE_ANON_KEY (the verify call that exchanges the one-time token).

Prints KEY=value lines on stdout for the caller to push into GITHUB_ENV:

    TENANT_A_ID=...
    TENANT_A2_ID=...
    TENANT_B_ID=...
    USER_A_EMAIL=...
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


class PasswordWriteRefused(Exception):
    """Raised when a request body would set a credential on a shared account."""


def request(base, headers, method, path, body=None, params=None, prefer=None):
    if isinstance(body, dict) and "password" in body:
        # The tripwire for the one mistake this script exists to not make. It
        # is enforced here, at the single point every call goes through, so a
        # future edit cannot reintroduce a password write by adding one field
        # to one call site. Run `--self-test` to prove it still bites.
        raise PasswordWriteRefused(
            f"refusing to send a password to {method} {path}: rotating a shared "
            "account's credential invalidates every concurrent run "
            "(docs/live-test-auth.md). Use the generate_link flow instead."
        )
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


def ensure_user(gotrue, headers, email: str) -> None:
    """Create the account if it is absent, and touch nothing if it is present.

    No password is sent, on either path. GoTrue accepts an admin create with no
    password (the account simply has none), and the session flow below needs
    none. An account that already exists is left exactly as it is: it is shared
    mutable state and a run that writes to it breaks every concurrent run.
    """
    status, body = request(
        gotrue, headers, "POST", "/admin/users",
        body={"email": email, "email_confirm": True},
    )
    if status in (200, 201):
        return
    # 422 is GoTrue's "a user with this email address has already been
    # registered", which is the normal case on every run after the first.
    if status == 422:
        return
    print(f"error: user create failed for {email}: {status} {body}", file=sys.stderr)
    sys.exit(1)


def set_user_metadata(gotrue, headers, user_id: str, user_metadata: dict) -> None:
    """Write user_metadata only. Never a password.

    control-plane resolves the caller's tenant by reading live user_metadata
    through GET /auth/v1/user on every request (apps/control-plane/internal/
    auth/client.go), so this may run after the session was minted and still
    takes effect for it.
    """
    status, body = request(
        gotrue, headers, "PUT", f"/admin/users/{user_id}",
        body={"user_metadata": user_metadata},
    )
    if status != 200:
        print(f"error: user metadata update failed: {status} {body}", file=sys.stderr)
        sys.exit(1)


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


def assert_token_works(gotrue, anon_key: str, token: str, email: str) -> None:
    """Fail here rather than in a spec if the token is not usable.

    control-plane authenticates every request by calling this exact endpoint
    (apps/control-plane/internal/auth/client.go, LookupUser) and answers 401 on
    any error, so a token that fails here reaches the specs as a bare 401 that
    reads like a broken assertion. Checking it at the source names the fault.
    """
    status, body = request(
        gotrue,
        {"apikey": anon_key, "Authorization": f"Bearer {token}"},
        "GET",
        "/user",
    )
    if status != 200:
        print(
            f"error: the session minted for {email} is not accepted by "
            f"GET /auth/v1/user ({status}); every authenticated call the specs "
            f"make would answer 401. Body: {body}",
            file=sys.stderr,
        )
        sys.exit(1)


def mint_session(gotrue, headers, anon_key: str, email: str) -> tuple[str, str]:
    """Mint a live session the way a magic-link login does.

    The Python twin of apps/web-console/tests/e2e/support/live-auth.mjs, whose
    header carries the full rationale. Two calls: an admin generate_link for a
    one-shot token hash, then a verify that exchanges it for a normal access
    token and consumes it. Addressed by email, so no admin user listing is
    involved (that endpoint has been returning intermittent 500s, issue #791).
    Writes no credential: the only column it touches is the transient one-time
    token that verify immediately clears.

    The token is signed by the project itself, so edge-api's JWKS validation
    accepts it, and it carries the same claims a real browser session would.

    Returns (access_token, user_id).
    """
    # GoTrue keeps ONE outstanding one-time token per user, so two mints for
    # the same account that interleave leave the first holding a token the
    # second already replaced. One retry clears that; see live-auth.mjs.
    for attempt in range(2):
        status, body = request(
            gotrue, headers, "POST", "/admin/generate_link",
            body={"type": "magiclink", "email": email},
        )
        if status != 200 or not body:
            print(f"error: generate_link failed for {email}: {status} {body}", file=sys.stderr)
            sys.exit(1)
        # GoTrue returns these flat; supabase-js nests them under `properties`.
        properties = body.get("properties") or body
        token_hash = properties.get("hashed_token")
        if not token_hash:
            print(f"error: generate_link for {email} carried no hashed_token", file=sys.stderr)
            sys.exit(1)

        # POST rather than GET: the GET form answers with a redirect that puts
        # the session in the URL fragment, which is far easier to leak into a
        # log or a screenshot.
        status, body = request(
            gotrue, {"apikey": anon_key, "Content-Type": "application/json"},
            "POST", "/verify",
            body={"type": "magiclink", "token_hash": token_hash},
        )
        if status == 200 and body and body.get("access_token"):
            return body["access_token"], (body.get("user") or {}).get("id", "")
        if attempt == 0 and status in (401, 403):
            continue
        print(f"error: verify failed for {email}: {status} {body}", file=sys.stderr)
        sys.exit(1)
    print(f"error: verify failed for {email} after a retry", file=sys.stderr)
    sys.exit(1)


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

    # The user id comes back from the session mint rather than from an admin
    # user listing, which keeps this script off the endpoint in issue #791.
    #
    # Two mints for user A, deliberately. The first only learns the id; the
    # second produces the token the specs carry, and it is taken AFTER the
    # metadata write. An admin update of a user ends that user's sessions, so a
    # token minted before the write is already dead by the time a spec presents
    # it, and every authenticated call answers 401. That is what the first live
    # run of these specs showed (issue #659). The orphan needs no second mint:
    # the only write after its mint is to public.tenant_users, which is not an
    # auth.users change.
    ensure_user(gotrue, headers, USER_A_EMAIL)
    _, user_a_id = mint_session(gotrue, headers, anon_key, USER_A_EMAIL)
    if not user_a_id:
        print("error: verify returned no user id for user A", file=sys.stderr)
        sys.exit(1)
    # selected_tenant_id pins the caller to tenant A, so the cross-tenant specs
    # start from a tenant that is genuinely not B.
    set_user_metadata(gotrue, headers, user_a_id, {"selected_tenant_id": tenant_a})
    upsert_membership(rest, headers, tenant_a, user_a_id, USER_A_ROLE)
    upsert_membership(rest, headers, tenant_a2, user_a_id, USER_A_ROLE)
    user_a_jwt, _ = mint_session(gotrue, headers, anon_key, USER_A_EMAIL)
    assert_token_works(gotrue, anon_key, user_a_jwt, USER_A_EMAIL)

    ensure_user(gotrue, headers, ORPHAN_EMAIL)
    orphan_jwt, orphan_id = mint_session(gotrue, headers, anon_key, ORPHAN_EMAIL)
    if not orphan_id:
        print("error: verify returned no user id for the orphan", file=sys.stderr)
        sys.exit(1)
    strip_memberships(rest, headers, orphan_id)
    assert_token_works(gotrue, anon_key, orphan_jwt, ORPHAN_EMAIL)

    print(f"TENANT_A_ID={tenant_a}")
    print(f"TENANT_A2_ID={tenant_a2}")
    print(f"TENANT_B_ID={tenant_b}")
    print(f"USER_A_EMAIL={USER_A_EMAIL}")
    print(f"USER_A_JWT={user_a_jwt}")
    print(f"ORPHAN_JWT={orphan_jwt}")


def self_test() -> None:
    """Prove the password tripwire still bites. Dials nothing."""
    try:
        request("https://example.invalid", {}, "PUT", "/admin/users/x", body={"password": "p"})
    except PasswordWriteRefused:
        pass
    else:
        print("self-test FAILED: a password body was not refused", file=sys.stderr)
        sys.exit(1)

    # A password grant would have to send the same field through the same
    # function, so the one check above covers that route back in too.
    print("self-test OK: a request body carrying a password is refused")


if __name__ == "__main__":
    if "--self-test" in sys.argv[1:]:
        self_test()
    else:
        main()

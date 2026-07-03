#!/usr/bin/env python3
"""Idempotently provision the OWUI e2e test user, tenant, and membership.

Run before the phase-19 OWUI nightly Playwright suite so OWUI_E2E_EMAIL /
OWUI_E2E_PASSWORD never need to be hand-managed GitHub secrets. Safe to
re-run: the tenant and membership rows are upserted, the GoTrue user is
found-or-created, and its password is always rotated to a fresh random
value so no run reuses a prior credential.

Required env: SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY

Prints exactly two lines to stdout (and nothing else):
  EMAIL=<email>
  PASSWORD=<password>
Everything else (progress, errors) goes to stderr.
"""
import json
import os
import secrets
import string
import sys
import urllib.error
import urllib.parse
import urllib.request

TENANT_SLUG = "owui-e2e"
TENANT_NAME = "OWUI E2E"
# ponytail: no Go code branches on tenants.deployment today (grep confirmed
# clean). ENTERPRISE_EDGE picked as the closer conceptual fit for a
# self-hosted OWUI chat front-end; revisit if that ever becomes load-bearing.
TENANT_DEPLOYMENT = "ENTERPRISE_EDGE"
# .invalid is an IANA-reserved TLD (RFC 2606) meant for exactly this use;
# verified live against this project's GoTrue instance that it accepts the
# format (2026-07-03 probe, see PR description).
USER_EMAIL = "owui-e2e@hive-e2e.invalid"
MEMBER_ROLE = "MEMBER"
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

    # 1. Upsert the tenant (service role bypasses RLS).
    status, body = request(
        rest, headers, "POST", "/tenants",
        body={"slug": TENANT_SLUG, "name": TENANT_NAME, "deployment": TENANT_DEPLOYMENT},
        params={"on_conflict": "slug"},
        prefer="resolution=merge-duplicates,return=representation",
    )
    if status not in (200, 201) or not body:
        print(f"error: tenant upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    tenant_id = body[0]["id"]

    # 2. Find-or-create the GoTrue user. `filter=<email>` does an exact
    # server-side match (verified live; the `email=` param is NOT
    # supported and 500s on this GoTrue version).
    status, body = request(gotrue, headers, "GET", "/admin/users", params={"filter": USER_EMAIL})
    if status != 200:
        print(f"error: user lookup failed: {status} {body}", file=sys.stderr)
        sys.exit(1)
    existing = next(
        (u for u in body.get("users", []) if u.get("email", "").lower() == USER_EMAIL.lower()),
        None,
    )

    password = random_password()
    user_metadata = {"selected_tenant_id": tenant_id}

    if existing is None:
        status, body = request(
            gotrue, headers, "POST", "/admin/users",
            body={
                "email": USER_EMAIL,
                "password": password,
                "email_confirm": True,
                "user_metadata": user_metadata,
            },
        )
        if status not in (200, 201):
            print(f"error: user create failed: {status} {body}", file=sys.stderr)
            sys.exit(1)
        user_id = body["id"]
    else:
        user_id = existing["id"]
        # GoTrue's admin updateUserById MERGES user_metadata (verified
        # live), so this only ever adds/refreshes selected_tenant_id.
        # Password is rotated unconditionally on every run.
        status, body = request(
            gotrue, headers, "PUT", f"/admin/users/{user_id}",
            body={"password": password, "user_metadata": user_metadata},
        )
        if status != 200:
            print(f"error: user update failed: {status} {body}", file=sys.stderr)
            sys.exit(1)

    # 3. Upsert tenant membership.
    status, body = request(
        rest, headers, "POST", "/tenant_users",
        body={
            "tenant_id": tenant_id,
            "user_id": user_id,
            "role": MEMBER_ROLE,
            "status": MEMBER_STATUS,
        },
        params={"on_conflict": "tenant_id,user_id"},
        prefer="resolution=merge-duplicates",
    )
    if status not in (200, 201, 204):
        print(f"error: membership upsert failed: {status} {body}", file=sys.stderr)
        sys.exit(1)

    print(f"EMAIL={USER_EMAIL}")
    print(f"PASSWORD={password}")


if __name__ == "__main__":
    main()
